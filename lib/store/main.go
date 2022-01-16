package store

import (
	"context"

	"github.com/xanderstrike/goplaxt/lib/internal"
)

// Store is the interface for All the store types
type Store interface {
	WriteUser(user User)
	GetUser(id string) *User
	GetUserByName(username string) *User
	DeleteUser(id, username string) bool
	GetScrobbleBody(playerUuid, ratingKey string) internal.CacheItem
	WriteScrobbleBody(playerUuid, ratingKey string, item internal.CacheItem)
	Ping(ctx context.Context) error
}

// Utils
func flatTransform(s string) []string { return []string{} }
