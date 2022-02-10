package trakt

import (
	"github.com/xanderstrike/goplaxt/lib/store"
)

type Trakt struct {
	clientId     string
	clientSecret string
	storage      store.Store
	ml           MultipleLock
}
