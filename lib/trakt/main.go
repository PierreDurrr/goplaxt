package trakt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/xanderstrike/goplaxt/lib/store"
	"github.com/xanderstrike/plexhooks"
)

const (
	TheTVDBService    = "tvdb"
	TheMovieDbService = "tmdb"
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

// Handle determine if an item is a show or a movie
func (t Trakt) Handle(pr plexhooks.PlexResponse, user store.User) {
	event, progress := getAction(pr)
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

	if len(pr.Metadata.ExternalGuid) > 0 {
		var episodeID string

		log.Println("Finding episode with new Plex TV agent")

		sort.Sort(SortedExternalGuid(pr.Metadata.ExternalGuid))
		traktService = pr.Metadata.ExternalGuid[0].Id[:4]
		episodeID = pr.Metadata.ExternalGuid[0].Id[7:]

		// The new Plex TV agent use episode ID instead of show ID,
		// so we need to do things a bit differently
		URL := fmt.Sprintf("https://api.trakt.tv/search/%s/%s?type=episode", traktService, episodeID)

		var showInfo []ShowInfo
		respBody := t.makeRequest(URL)
		err := mapstructure.Decode(respBody, &showInfo)
		handleErr(err)

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

	for _, season := range seasons {
		if fmt.Sprintf("%d", season.Number) == showID[2] {
			for _, episode := range season.Episodes {
				if fmt.Sprintf("%d", episode.Number) == showID[3] {
					return episode
				}
			}
		}
	}

	panic("Could not find episode!")
}

func (t Trakt) findMovie(pr plexhooks.PlexResponse) Movie {
	log.Print(fmt.Sprintf("Finding movie for %s (%d)", pr.Metadata.Title, pr.Metadata.Year))

	var URL string
	var searchById bool
	if len(pr.Metadata.ExternalGuid) > 0 {
		sort.Sort(SortedExternalGuid(pr.Metadata.ExternalGuid))
		traktService := pr.Metadata.ExternalGuid[0].Id[:4]
		movieId := pr.Metadata.ExternalGuid[0].Id[7:]

		URL = fmt.Sprintf("https://api.trakt.tv/search/%s/%s?type=movie", traktService, movieId)
		searchById = true
	} else {
		URL = fmt.Sprintf("https://api.trakt.tv/search/movie?query=%s&fields=title", url.PathEscape(pr.Metadata.Title))
		searchById = false
	}
	respBody := t.makeRequest(URL)

	var results []MovieSearchResult

	err := mapstructure.Decode(respBody, &results)
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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

func getAction(pr plexhooks.PlexResponse) (string, int) {
	switch pr.Event {
	case "media.play":
		return "start", 0
	case "media.pause":
		return "stop", 0
	case "media.resume":
		return "start", 0
	case "media.stop":
		return "stop", 0
	case "media.scrobble":
		return "stop", 90
	}
	return "", 0
}

func handleErr(err error) {
	if err != nil {
		panic(err)
	}
}
