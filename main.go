package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/etherlabsio/healthcheck"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/xanderstrike/goplaxt/lib/config"
	"github.com/xanderstrike/goplaxt/lib/store"
	"github.com/xanderstrike/goplaxt/lib/trakt"
	"github.com/xanderstrike/plexhooks"
	"golang.org/x/sync/singleflight"
)

var (
	version  string
	commit   string
	date     string
	storage  store.Store
	apiSf    *singleflight.Group
	traktSrv *trakt.Trakt
)

type AuthorizePage struct {
	SelfRoot   string
	Authorized bool
	URL        string
	ClientID   string
}

func SelfRoot(r *http.Request) string {
	u, _ := url.Parse("")
	u.Host = r.Host
	u.Scheme = r.URL.Scheme
	u.Path = ""
	if u.Scheme == "" {
		u.Scheme = "http"

		proto := r.Header.Get("X-Forwarded-Proto")
		if proto == "https" {
			u.Scheme = "https"
		}
	}
	return u.String()
}

func authorize(w http.ResponseWriter, r *http.Request) {
	args := r.URL.Query()
	username := strings.ToLower(args["username"][0])
	log.Print(fmt.Sprintf("Handling auth request for %s", username))
	code := args["code"][0]
	result, _ := traktSrv.AuthRequest(SelfRoot(r), username, code, "", "authorization_code")

	user := store.NewUser(username, result["access_token"].(string), result["refresh_token"].(string), storage)

	url := fmt.Sprintf("%s/api?id=%s", SelfRoot(r), user.ID)

	log.Print(fmt.Sprintf("Authorized as %s", user.ID))

	tmpl := template.Must(template.ParseFiles("static/index.html"))
	data := AuthorizePage{
		SelfRoot:   SelfRoot(r),
		Authorized: true,
		URL:        url,
		ClientID:   traktSrv.ClientId,
	}
	tmpl.Execute(w, data)
}

func api(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	regex := regexp.MustCompile("({.*})") // not the best way really
	match := regex.FindStringSubmatch(string(body))
	re, err := plexhooks.ParseWebhook([]byte(match[0]))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	username := strings.ToLower(re.Account.Title)
	log.Print(fmt.Sprintf("Webhook call for %s (%s)", id, re.Account.Title))

	// Handle the requests of the same user one at a time
	key := fmt.Sprintf("%s@%s", username, id)
	userInf, err, _ := apiSf.Do(key, func() (interface{}, error) {
		user := storage.GetUser(id)
		if user == nil {
			log.Println("id is invalid")
			return nil, trakt.NewHttpError(http.StatusForbidden, "id is invalid")
		}
		if re.Owner && username != user.Username {
			user = storage.GetUserByName(username)
		}

		if user == nil {
			log.Println("User not found.")
			return nil, trakt.NewHttpError(http.StatusNotFound, "user not found")
		}

		tokenAge := time.Since(user.Updated).Hours()
		if tokenAge > 23 { // tokens expire after 24 hours, so we refresh after 23
			log.Println("User access token outdated, refreshing...")
			result, success := traktSrv.AuthRequest(SelfRoot(r), user.Username, "", user.RefreshToken, "refresh_token")
			if success {
				user.UpdateUser(result["access_token"].(string), result["refresh_token"].(string))
				log.Println("Refreshed, continuing")
			} else {
				log.Println("Refresh failed, skipping and deleting user")
				storage.DeleteUser(user.ID, user.Username)
				return nil, trakt.NewHttpError(http.StatusUnauthorized, "fail")
			}
		}
		return user, nil
	})
	if err != nil {
		w.WriteHeader(err.(trakt.HttpError).Code)
		json.NewEncoder(w).Encode(err.Error())
		return
	}
	user := userInf.(*store.User)

	if username == user.Username {
		// FIXME - make everything take the pointer
		traktSrv.Handle(re, *user)
	} else {
		log.Println(fmt.Sprintf("Plex username %s does not equal %s, skipping", strings.ToLower(re.Account.Title), user.Username))
	}

	json.NewEncoder(w).Encode("success")
}

func allowedHostsHandler(allowedHostnames string) func(http.Handler) http.Handler {
	allowedHosts := strings.Split(regexp.MustCompile("https://|http://|\\s+").ReplaceAllString(strings.ToLower(allowedHostnames), ""), ",")
	log.Println("Allowed Hostnames:", allowedHosts)
	return func(h http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			if r.URL.EscapedPath() == "/healthcheck" {
				h.ServeHTTP(w, r)
				return
			}
			isAllowedHost := false
			lcHost := strings.ToLower(r.Host)
			for _, value := range allowedHosts {
				if lcHost == value {
					isAllowedHost = true
					break
				}
			}
			if !isAllowedHost {
				w.WriteHeader(http.StatusUnauthorized)
				w.Header().Set("Content-Type", "text/plain")
				fmt.Fprintf(w, "Oh no!")
				return
			}
			h.ServeHTTP(w, r)
		}

		return http.HandlerFunc(fn)
	}
}

func healthcheckHandler() http.Handler {
	return healthcheck.Handler(
		healthcheck.WithTimeout(5*time.Second),
		healthcheck.WithChecker("storage", healthcheck.CheckerFunc(func(ctx context.Context) error {
			return storage.Ping(ctx)
		})),
	)
}

func main() {
	log.Printf("Started version=\"%s (%s@%s)\"", version, commit, date)
	if os.Getenv("POSTGRESQL_URL") != "" {
		storage = store.NewPostgresqlStore(store.NewPostgresqlClient(os.Getenv("POSTGRESQL_URL")))
		log.Println("Using postgresql storage:", os.Getenv("POSTGRESQL_URL"))
	} else if os.Getenv("REDIS_URL") != "" {
		storage = store.NewRedisStore(store.NewRedisClientWithUrl(os.Getenv("REDIS_URL")))
		log.Println("Using redis storage: ", os.Getenv("REDIS_URL"))
	} else if os.Getenv("REDIS_URI") != "" {
		storage = store.NewRedisStore(store.NewRedisClient(os.Getenv("REDIS_URI"), os.Getenv("REDIS_PASSWORD")))
		log.Println("Using redis storage:", os.Getenv("REDIS_URI"))
	} else {
		storage = store.NewDiskStore()
		log.Println("Using disk storage:")
	}
	apiSf = &singleflight.Group{}
	traktSrv = trakt.New(config.TraktClientId, config.TraktClientSecret, storage)

	router := mux.NewRouter()
	// Assumption: Behind a proper web server (nginx/traefik, etc) that removes/replaces trusted headers
	router.Use(handlers.ProxyHeaders)
	// which hostnames we are allowing
	// REDIRECT_URI = old legacy list
	// ALLOWED_HOSTNAMES = new accurate config variable
	// No env = all hostnames
	if os.Getenv("REDIRECT_URI") != "" {
		router.Use(allowedHostsHandler(os.Getenv("REDIRECT_URI")))
	} else if os.Getenv("ALLOWED_HOSTNAMES") != "" {
		router.Use(allowedHostsHandler(os.Getenv("ALLOWED_HOSTNAMES")))
	}
	router.HandleFunc("/authorize", authorize).Methods("GET")
	router.HandleFunc("/api", api).Methods("POST")
	router.Handle("/healthcheck", healthcheckHandler()).Methods("GET")
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl := template.Must(template.ParseFiles("static/index.html"))
		data := AuthorizePage{
			SelfRoot:   SelfRoot(r),
			Authorized: false,
			URL:        "https://plaxt.royxiang.me/api?id=generate-your-own-silly",
			ClientID:   traktSrv.ClientId,
		}
		_ = tmpl.Execute(w, data)
	}).Methods("GET")
	listen := os.Getenv("LISTEN")
	if listen == "" {
		listen = "0.0.0.0:8000"
	}
	log.Print("Started on " + listen + "!")
	log.Fatal(http.ListenAndServe(listen, router))
}
