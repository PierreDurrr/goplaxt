package trakt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/xanderstrike/goplaxt/lib/store"
	"github.com/xanderstrike/plexhooks"
)

const (
	TheTVDBService    = "tvdb"
	TheMovieDbService = "tmdb"
	ProgressThreshold = 95
)

func New(clientId, clientSecret string, storage store.Store) *Trakt {
	return &Trakt{
		clientId:     clientId,
		clientSecret: clientSecret,
		storage:      storage,
	}
}

// AuthRequest authorize the connection with Trakt
func (t Trakt) AuthRequest(root, username, code, refreshToken, grantType string) (map[string]interface{}, bool) {
	values := map[string]string{
		"code":          code,
		"refresh_token": refreshToken,
		"client_id":     t.clientId,
		"client_secret": t.clientSecret,
		"redirect_uri":  fmt.Sprintf("%s/authorize?username=%s", root, url.PathEscape(username)),
		"grant_type":    grantType,
	}
	jsonValue, _ := json.Marshal(values)

	resp, err := http.Post("https://api.trakt.tv/oauth/token", "application/json", bytes.NewBuffer(jsonValue))
	handleErr(err)

	var result map[string]interface{}

	if resp.StatusCode != http.StatusOK {
		log.Println(fmt.Sprintf("Got a %s error while refreshing :(", resp.Status))
		return result, false
	}

	err = json.NewDecoder(resp.Body).Decode(&result)
	handleErr(err)

	return result, true
}

func (t Trakt) SavePlaybackProgress(playerUuid, ratingKey string, percent int, duration float64) {
	if percent <= 0 {
		return
	}
	t.storage.WriteProgress(playerUuid, ratingKey, percent, time.Duration(int64(duration))*time.Millisecond)
}

// Handle determine if an item is a show or a movie
func (t Trakt) Handle(pr plexhooks.PlexResponse, user store.User) {
	event, progress := t.getAction(pr)
	if event == "" {
		log.Printf("Unrecognized event: %s", pr.Event)
		return
	}
	if pr.Metadata.LibrarySectionType == "show" {
		t.handleShow(pr, event, progress, user.AccessToken)
	} else if pr.Metadata.LibrarySectionType == "movie" {
		t.handleMovie(pr, event, progress, user.AccessToken)
	}
	log.Print("Event logged")
}

// handleShow start the scrobbling for a show
func (t Trakt) handleShow(pr plexhooks.PlexResponse, event string, progress int, accessToken string) {
	scrobbleObject := ShowScrobbleBody{
		Progress: progress,
		Episode:  t.findEpisode(pr),
	}

	scrobbleJSON, err := json.Marshal(scrobbleObject)
	handleErr(err)

	t.scrobbleRequest(event, scrobbleJSON, accessToken)
}

// handleMovie start the scrobbling for a movie
func (t Trakt) handleMovie(pr plexhooks.PlexResponse, event string, progress int, accessToken string) {
	scrobbleObject := MovieScrobbleBody{
		Progress: progress,
		Movie:    t.findMovie(pr),
	}

	scrobbleJSON, _ := json.Marshal(scrobbleObject)

	t.scrobbleRequest(event, scrobbleJSON, accessToken)
}

var episodeRegex = regexp.MustCompile("(\\d+)/(\\d+)/(\\d+)")

func (t Trakt) findEpisode(pr plexhooks.PlexResponse) Episode {
	var traktService string
	var showID []string
	var episodeID string
	var err error

	traktService, episodeID, err = parseExternalGuid(pr.Metadata.ExternalGuid)
	handleErr(err)
	if traktService != "" {
		log.Println("Finding episode with new Plex TV agent")

		// The new Plex TV agent use episode ID instead of show ID,
		// so we need to do things a bit differently
		URL := fmt.Sprintf("https://api.trakt.tv/search/%s/%s?type=episode", traktService, episodeID)

		var showInfo []ShowInfo
		respBody := t.makeRequest(URL)
		err := mapstructure.Decode(respBody, &showInfo)
		handleErr(err)
		if len(showInfo) == 0 {
			panic("Could not find episode!")
		}

		log.Print(fmt.Sprintf("Tracking %s - S%02dE%02d using %s", showInfo[0].Show.Title, showInfo[0].Episode.Season, showInfo[0].Episode.Number, traktService))

		return showInfo[0].Episode
	}

	u, err := url.Parse(pr.Metadata.Guid)
	handleErr(err)

	if strings.HasSuffix(u.Scheme, "tvdb") {
		traktService = TheTVDBService
	} else if strings.HasSuffix(u.Scheme, "themoviedb") {
		traktService = TheMovieDbService
	} else if strings.HasSuffix(u.Scheme, "hama") {
		if strings.HasPrefix(u.Host, "tvdb-") || strings.HasPrefix(u.Host, "tvdb2-") {
			traktService = TheTVDBService
		}
	}
	if traktService == "" {
		panic(fmt.Sprintf("Unidentified guid: %s", pr.Metadata.Guid))
	}
	showID = episodeRegex.FindStringSubmatch(pr.Metadata.Guid)
	if showID == nil {
		panic(fmt.Sprintf("Unmatched guid: %s", pr.Metadata.Guid))
	}

	URL := fmt.Sprintf("https://api.trakt.tv/search/%s/%s?type=show", traktService, showID[1])

	log.Print(fmt.Sprintf("Finding show for %s %s %s using %s", showID[1], showID[2], showID[3], traktService))

	respBody := t.makeRequest(URL)

	var showInfo []ShowInfo
	err = mapstructure.Decode(respBody, &showInfo)
	handleErr(err)

	URL = fmt.Sprintf("https://api.trakt.tv/shows/%d/seasons?extended=episodes", showInfo[0].Show.Ids.Trakt)

	respBody = t.makeRequest(URL)
	var seasons []Season
	err = mapstructure.Decode(respBody, &seasons)
	handleErr(err)

	seasonNumber, _ := strconv.Atoi(showID[2])
	episodeNumber, _ := strconv.Atoi(showID[3])
	for _, season := range seasons {
		if season.Number != seasonNumber {
			continue
		}
		for _, episode := range season.Episodes {
			if episode.Number == episodeNumber {
				return episode
			}
		}
	}

	panic("Could not find episode!")
}

func (t Trakt) findMovie(pr plexhooks.PlexResponse) Movie {
	log.Print(fmt.Sprintf("Finding movie for %s (%d)", pr.Metadata.Title, pr.Metadata.Year))

	var URL string
	var searchById bool

	traktService, movieId, err := parseExternalGuid(pr.Metadata.ExternalGuid)
	handleErr(err)
	if traktService != "" {
		URL = fmt.Sprintf("https://api.trakt.tv/search/%s/%s?type=movie", traktService, movieId)
		searchById = true
	} else {
		URL = fmt.Sprintf("https://api.trakt.tv/search/movie?query=%s&fields=title", url.PathEscape(pr.Metadata.Title))
		searchById = false
	}
	respBody := t.makeRequest(URL)

	var results []MovieSearchResult

	err = mapstructure.Decode(respBody, &results)
	handleErr(err)

	for _, result := range results {
		if result.Movie.Year == pr.Metadata.Year || searchById {
			return result.Movie
		}
	}
	panic("Could not find movie!")
}

func (t Trakt) makeRequest(url string) []map[string]interface{} {
	var results []map[string]interface{}

	respBody := t.storage.GetResponse(url)
	if respBody != nil {
		_ = json.Unmarshal(respBody, &results)
		return results
	}

	client := &http.Client{}

	req, err := http.NewRequest("GET", url, nil)
	handleErr(err)

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("trakt-api-version", "2")
	req.Header.Add("trakt-api-key", t.clientId)

	resp, err := client.Do(req)
	handleErr(err)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	respBody, err = ioutil.ReadAll(resp.Body)
	handleErr(err)

	err = json.Unmarshal(respBody, &results)
	handleErr(err)

	if len(results) > 0 {
		t.storage.WriteResponse(url, respBody)
	}
	return results
}

func (t Trakt) scrobbleRequest(action string, body []byte, accessToken string) []byte {
	client := &http.Client{}

	URL := fmt.Sprintf("https://api.trakt.tv/scrobble/%s", action)

	req, err := http.NewRequest("POST", URL, bytes.NewBuffer(body))
	handleErr(err)

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	req.Header.Add("trakt-api-version", "2")
	req.Header.Add("trakt-api-key", t.clientId)

	resp, _ := client.Do(req)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	respBody, _ := ioutil.ReadAll(resp.Body)

	return respBody
}

func (s SortedExternalGuid) Len() int {
	return len(s)
}

func (s SortedExternalGuid) Less(i, j int) bool {
	urlI, errI := url.Parse(s[i].Id)
	if errI != nil {
		return false
	} else if urlI.Scheme == TheMovieDbService {
		return true
	}
	urlJ, errJ := url.Parse(s[j].Id)
	if errJ != nil {
		return true
	} else if urlJ.Scheme == TheMovieDbService {
		return false
	}
	return urlI.Scheme == TheTVDBService
}

func (s SortedExternalGuid) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (t Trakt) getAction(pr plexhooks.PlexResponse) (action string, percent int) {
	percent = t.storage.GetProgress(pr.Player.Uuid, pr.Metadata.RatingKey)
	if percent < 0 {
		return
	}
	switch pr.Event {
	case "media.play":
		action = "start"
	case "media.pause":
		action = "pause"
	case "media.resume":
		action = "start"
	case "media.stop":
		action = "stop"
	case "media.scrobble":
		action = "stop"
		if percent < ProgressThreshold {
			percent = ProgressThreshold
		}
	}
	if percent >= ProgressThreshold {
		action = "stop"
		t.storage.WriteProgress(pr.Player.Uuid, pr.Metadata.RatingKey, -1, 30*time.Minute)
	} else if action == "stop" {
		action = "pause"
	}
	return
}

func parseExternalGuid(guids []plexhooks.ExternalGuid) (traktSrv, id string, err error) {
	if len(guids) == 0 {
		return
	}
	sort.Sort(SortedExternalGuid(guids))
	guid := guids[0].Id
	if !strings.HasPrefix(guid, TheMovieDbService) && !strings.HasPrefix(guid, TheTVDBService) {
		err = errors.New(fmt.Sprintf("Unidentified guid: %s", guid))
		return
	}
	traktSrv = guid[:4]
	id = guid[7:]
	return
}

func handleErr(err error) {
	if err != nil {
		panic(err)
	}
}
