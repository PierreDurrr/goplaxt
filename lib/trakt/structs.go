package trakt

import (
	"sync"

	"github.com/xanderstrike/goplaxt/lib/store"
	"github.com/xanderstrike/plexhooks"
)

type Trakt struct {
	clientId     string
	clientSecret string
	storage      store.Store
	mu           sync.Mutex
}

type SortedExternalGuid []plexhooks.ExternalGuid
