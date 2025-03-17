package trakt

import (
	"net/http"

	"github.com/xanderstrike/goplaxt/lib/common"
	"github.com/xanderstrike/goplaxt/lib/store"
)

type Trakt struct {
	ClientId     string
	clientSecret string
	storage      store.Store
	httpClient   *http.Client
	ml           common.MultipleLock
}

type HttpError struct {
	Code    int
	Message string
}
