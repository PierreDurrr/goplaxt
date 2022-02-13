package trakt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xanderstrike/goplaxt/lib/common"
	"github.com/xanderstrike/goplaxt/lib/store"
	"github.com/xanderstrike/plexhooks"
)

const (
	TheTVDBService    = "tvdb"
	TheMovieDbService = "tmdb"
	IMDBService       = "imdb"
	ProgressThreshold = 90

	actionStart = "start"
	actionPause = "pause"
	actionStop  = "stop"

	triggerScrobble       = "media.scrobble"
	triggerTimelinePrefix = "timeline."
)

func New(clientId, clientSecret string, storage store.Store) *Trakt {
	return &Trakt{
		clientId:     clientId,
		clientSecret: clientSecret,
		storage:      storage,
		httpClient:   &http.Client{Timeout: time.Second * 10},
		ml:           common.NewMultipleLock(),
	}
}

// AuthRequest authorize the connection with Trakt
func (t *Trakt) AuthRequest(root, username, code, refreshToken, grantType string) (map[string]interface{}, bool) {
	values := map[string]string{
		"code":          code,
		"refresh_token": refreshToken,
		"client_id":     t.clientId,
		"client_secret": t.clientSecret,
		"redirect_uri":  fmt.Sprintf("%s/authorize?username=%s", root, url.PathEscape(username)),
		"grant_type":    grantType,
	}
	jsonValue, _ := json.Marshal(values)

	resp, err := t.httpClient.Post("https://api.trakt.tv/oauth/token", "application/json", bytes.NewBuffer(jsonValue))
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

func (t *Trakt) SavePlaybackProgress(playerUuid, ratingKey, state string, percent int) {
	if percent <= 0 {
		return
	}
	var action string
	switch state {
	case "playing":
		if percent >= 100 {
			action = actionStop
		} else {
			action = actionStart
		}
	case "paused", "stopped":
		if percent >= ProgressThreshold {
			action = actionStop
		} else {
			action = actionPause
		}
	default:
		return
	}

	lockKey := fmt.Sprintf("%s:%s", playerUuid, ratingKey)
	t.ml.Lock(lockKey)
	defer t.ml.Unlock(lockKey)

	cache := t.storage.GetScrobbleBody(playerUuid, ratingKey)
	if (action == cache.LastAction && cache.Body.Progress == percent) ||
		cache.AccessToken == "" || cache.LastAction == actionStop {
		return
	}

	cache.Trigger = fmt.Sprintf("%s%s", triggerTimelinePrefix, state)
	cache.Body.Progress = percent
	t.scrobbleRequest(action, cache)
}

// Handle determine if an item is a show or a movie
func (t *Trakt) Handle(pr plexhooks.PlexResponse, user store.User) {
	if pr.Player.Uuid == "" || pr.Metadata.RatingKey == "" {
		log.Printf("Event %s ignored", pr.Event)
		return
	}
	lockKey := fmt.Sprintf("%s:%s", pr.Player.Uuid, pr.Metadata.RatingKey)
	t.ml.Lock(lockKey)
	defer t.ml.Unlock(lockKey)

	event, cache := t.getAction(pr)
	if event == "" {
		log.Printf("Event %s ignored", pr.Event)
		return
	} else if cache.ServerUuid == pr.Server.Uuid && cache.AccessToken == user.AccessToken {
		if cache.LastAction == actionStop || cache.LastAction == event {
			log.Print("Event already scrobbled")
			return
		} else if pr.Event == triggerScrobble && strings.HasPrefix(cache.Trigger, triggerTimelinePrefix) {
			log.Printf("Event %s ignored", pr.Event)
			return
		}
	}

	var body *common.ScrobbleBody
	switch pr.Metadata.LibrarySectionType {
	case "show":
		body = t.handleShow(pr)
		if body == nil {
			log.Print("Cannot find episode")
			return
		}
	case "movie":
		body = t.handleMovie(pr)
		if body == nil {
			log.Print("Cannot find movie")
			return
		}
	default:
		log.Print("Event ignored")
		return
	}
	body.Progress = cache.Body.Progress

	cache.PlayerUuid = pr.Player.Uuid
	cache.ServerUuid = pr.Server.Uuid
	cache.RatingKey = pr.Metadata.RatingKey
	cache.Trigger = pr.Event
	cache.Body = *body
	cache.AccessToken = user.AccessToken
	t.scrobbleRequest(event, cache)
}

func (t *Trakt) handleShow(pr plexhooks.PlexResponse) *common.ScrobbleBody {
	if len(pr.Metadata.ExternalGuid) > 0 {
		isValid := false
		ids := common.Ids{}
		for _, guid := range pr.Metadata.ExternalGuid {
			if len(guid.Id) < 8 {
				continue
			}
			switch guid.Id[:4] {
			case TheMovieDbService:
				id, err := strconv.Atoi(guid.Id[7:])
				if err != nil {
					continue
				}
				ids.Tmdb = &id
				isValid = true
			case TheTVDBService:
				id, err := strconv.Atoi(guid.Id[7:])
				if err != nil {
					continue
				}
				ids.Tvdb = &id
				isValid = true
			case IMDBService:
				id := guid.Id[7:]
				ids.Imdb = &id
				isValid = true
			}
		}
		if isValid {
			return &common.ScrobbleBody{
				Episode: &common.Episode{
					Ids: &ids,
				},
			}
		}
	}
	return t.findEpisode(pr)
}

func (t *Trakt) handleMovie(pr plexhooks.PlexResponse) *common.ScrobbleBody {
	if len(pr.Metadata.ExternalGuid) > 0 {
		isValid := false
		movie := common.Movie{}
		for _, guid := range pr.Metadata.ExternalGuid {
			if len(guid.Id) < 8 {
				continue
			}
			switch guid.Id[:4] {
			case TheMovieDbService:
				id, err := strconv.Atoi(guid.Id[7:])
				if err != nil {
					continue
				}
				movie.Ids.Tmdb = &id
				isValid = true
			case TheTVDBService:
				id, err := strconv.Atoi(guid.Id[7:])
				if err != nil {
					continue
				}
				movie.Ids.Tvdb = &id
				isValid = true
			case IMDBService:
				id := guid.Id[7:]
				movie.Ids.Imdb = &id
				isValid = true
			}
		}
		if isValid {
			return &common.ScrobbleBody{
				Movie: &movie,
			}
		}
	}
	return t.findMovie(pr)
}

var episodeRegex = regexp.MustCompile(`([0-9]+)/([0-9]+)/([0-9]+)`)

func (t *Trakt) findEpisode(pr plexhooks.PlexResponse) *common.ScrobbleBody {
	u, err := url.Parse(pr.Metadata.Guid)
	if err != nil {
		log.Printf("Invalid guid: %s", pr.Metadata.Guid)
		return nil
	}
	var srv string
	if strings.HasSuffix(u.Scheme, "tvdb") {
		srv = TheTVDBService
	} else if strings.HasSuffix(u.Scheme, "themoviedb") {
		srv = TheMovieDbService
	} else if strings.HasSuffix(u.Scheme, "hama") {
		if strings.HasPrefix(u.Host, "tvdb-") || strings.HasPrefix(u.Host, "tvdb2-") {
			srv = TheTVDBService
		}
	}
	if srv == "" {
		log.Printf(fmt.Sprintf("Unidentified guid: %s", pr.Metadata.Guid))
		return nil
	}
	showID := episodeRegex.FindStringSubmatch(pr.Metadata.Guid)
	if showID == nil {
		log.Printf(fmt.Sprintf("Unmatched guid: %s", pr.Metadata.Guid))
		return nil
	}
	show := common.Show{}
	id, _ := strconv.Atoi(showID[1])
	if srv == TheTVDBService {
		show.Ids.Tvdb = &id
	} else {
		show.Ids.Tmdb = &id
	}
	season, _ := strconv.Atoi(showID[2])
	number, _ := strconv.Atoi(showID[3])
	episode := common.Episode{
		Season: &season,
		Number: &number,
	}
	return &common.ScrobbleBody{
		Show:    &show,
		Episode: &episode,
	}
}

func (t *Trakt) findMovie(pr plexhooks.PlexResponse) *common.ScrobbleBody {
	if pr.Metadata.Title == "" || pr.Metadata.Year == 0 {
		return nil
	}
	return &common.ScrobbleBody{
		Movie: &common.Movie{
			Title: &pr.Metadata.Title,
			Year:  &pr.Metadata.Year,
		},
	}
}

func (t *Trakt) makeRequest(url string) []map[string]interface{} {
	req, err := http.NewRequest("GET", url, nil)
	handleErr(err)

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("trakt-api-version", "2")
	req.Header.Add("trakt-api-key", t.clientId)

	resp, err := t.httpClient.Do(req)
	handleErr(err)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	respBody, err := ioutil.ReadAll(resp.Body)
	handleErr(err)

	var results []map[string]interface{}
	err = json.Unmarshal(respBody, &results)
	handleErr(err)

	return results
}

func (t *Trakt) scrobbleRequest(action string, item common.CacheItem) {
	URL := fmt.Sprintf("https://api.trakt.tv/scrobble/%s", action)

	body, _ := json.Marshal(item.Body)
	req, err := http.NewRequest("POST", URL, bytes.NewBuffer(body))
	handleErr(err)

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", item.AccessToken))
	req.Header.Add("trakt-api-version", "2")
	req.Header.Add("trakt-api-key", t.clientId)

	resp, _ := t.httpClient.Do(req)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		lastAction := item.LastAction
		item.LastAction = action
		respBody, _ := ioutil.ReadAll(resp.Body)
		_ = json.Unmarshal(respBody, &item.Body)
		t.storage.WriteScrobbleBody(item)
		switch action {
		case actionStart:
			if action != lastAction {
				log.Printf("%s started (triggered by: %s)", item.Body, item.Trigger)
			}
		case actionPause:
			log.Printf("%s paused (triggered by: %s)", item.Body, item.Trigger)
		case actionStop:
			log.Printf("%s stopped (triggered by: %s)", item.Body, item.Trigger)
		}
	} else {
		log.Printf("%s failed (triggered by: %s, status code: %d)", string(body), item.Trigger, resp.StatusCode)
	}
}

func (t *Trakt) getAction(pr plexhooks.PlexResponse) (action string, item common.CacheItem) {
	item = t.storage.GetScrobbleBody(pr.Player.Uuid, pr.Metadata.RatingKey)
	switch pr.Event {
	case "media.play", "media.resume", "playback.started":
		action = actionStart
	case "media.pause", "media.stop":
		if item.Body.Progress >= ProgressThreshold {
			action = actionStop
		} else {
			action = actionPause
		}
	case triggerScrobble:
		action = actionStop
		if item.Body.Progress < ProgressThreshold {
			item.Body.Progress = ProgressThreshold
		}
	}
	return
}

func handleErr(err error) {
	if err != nil {
		panic(err)
	}
}
