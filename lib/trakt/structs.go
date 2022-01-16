package trakt

import (
	"sync"

	"github.com/xanderstrike/goplaxt/lib/store"
)

type Trakt struct {
	clientId     string
	clientSecret string
	storage      store.Store
	mu           sync.Mutex
}
