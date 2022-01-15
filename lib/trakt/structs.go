package trakt

import (
	"github.com/xanderstrike/goplaxt/lib/store"
	"github.com/xanderstrike/plexhooks"
)

type Trakt struct {
	clientId     string
	clientSecret string
	storage      store.Store
}

type SortedExternalGuid []plexhooks.ExternalGuid
