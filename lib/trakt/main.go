package trakt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
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
)

func New(clientId, clientSecret string, storage store.Store) *Trakt {
	return &Trakt{
		ClientId:     clientId,
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
		"client_id":     t.ClientId,
		"client_secret": t.clientSecret,
		"redirect_uri":  fmt.Sprintf("%s/authorize?username=%s", root, url.PathEscape(username)),
		"grant_type":    grantType,
	}
	jsonValue, _ := json.Marshal(values)

	resp, err := t.httpClient.Post("https://api.trakt.tv/oauth/token", "application/json", bytes.NewBuffer(jsonValue))
	handleErr(err)

	var result map[string]interface{}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Got a %s error while refreshing :(", resp.Status)
		return result, false
	}

	err = json.NewDecoder(resp.Body).Decode(&result)
	handleErr(err)

	return result, true
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

	event, cache, progress := t.getAction(pr)
	itemChanged := true
	if event == "" {
		log.Printf("Event %s ignored", pr.Event)
		return
	} else if cache.ServerUuid == pr.Server.Uuid {
		itemChanged = false
		if cache.LastAction == actionStop ||
			(cache.LastAction == event && progress == cache.Body.Progress) {
			log.Print("Event already scrobbled")
			return
		}
	}

	if itemChanged {
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
		cache.Body = *body
	}

	cache.PlayerUuid = pr.Player.Uuid
	cache.ServerUuid = pr.Server.Uuid
	cache.RatingKey = pr.Metadata.RatingKey
	cache.Trigger = pr.Event
	cache.Body.Progress = progress
	t.scrobbleRequest(event, cache, user.AccessToken)
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
	if len(pr.Metadata.ExternalGuid) == 0 {
		return nil
	}
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
	if !isValid {
		return nil
	}
	return &common.ScrobbleBody{
		Movie: &movie,
	}
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
		log.Printf("Unidentified guid: %s", pr.Metadata.Guid)
		return nil
	}
	showID := episodeRegex.FindStringSubmatch(pr.Metadata.Guid)
	if showID == nil {
		log.Printf("Unmatched guid: %s", pr.Metadata.Guid)
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

func (t *Trakt) scrobbleRequest(action string, item common.CacheItem, accessToken string) {
	URL := fmt.Sprintf("https://api.trakt.tv/scrobble/%s", action)

	body, _ := json.Marshal(item.Body)
	req, err := http.NewRequest("POST", URL, bytes.NewBuffer(body))
	handleErr(err)

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	req.Header.Add("trakt-api-version", "2")
	req.Header.Add("trakt-api-key", t.ClientId)

	resp, _ := t.httpClient.Do(req)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		item.LastAction = action
		respBody, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(respBody, &item.Body)
		t.storage.WriteScrobbleBody(item)
		switch action {
		case actionStart:
			log.Printf("%s started (triggered by: %s)", item.Body, item.Trigger)
		case actionPause:
			log.Printf("%s paused (triggered by: %s)", item.Body, item.Trigger)
		case actionStop:
			log.Printf("%s stopped (triggered by: %s)", item.Body, item.Trigger)
		}
	} else {
		log.Printf("%s failed (triggered by: %s, status code: %d)", string(body), item.Trigger, resp.StatusCode)
	}
}

func (t *Trakt) getAction(pr plexhooks.PlexResponse) (action string, item common.CacheItem, progress int) {
	item = t.storage.GetScrobbleBody(pr.Player.Uuid, pr.Metadata.RatingKey)
	if pr.Metadata.Duration > 0 {
		progress = int(math.Round(float64(pr.Metadata.ViewOffset) / float64(pr.Metadata.Duration) * 100.0))
	} else {
		progress = item.Body.Progress
	}
	switch pr.Event {
	case "media.play", "media.resume", "playback.started":
		action = actionStart
	case "media.pause", "media.stop":
		if progress >= ProgressThreshold {
			action = actionStop
		} else {
			action = actionPause
		}
	case "media.scrobble":
		action = actionStop
		if progress < ProgressThreshold {
			progress = ProgressThreshold
		}
	}
	return
}

func handleErr(err error) {
	if err != nil {
		panic(err)
	}
}

func (e HttpError) Error() string {
	return e.Message
}

func NewHttpError(code int, message string) HttpError {
	return HttpError{
		Code:    code,
		Message: message,
	}
}
