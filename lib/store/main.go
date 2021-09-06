package store

import (
	"context"
	"time"
)

// Store is the interface for All the store types
type Store interface {
	WriteServer(serverUuid string)
	GetServer(serverUuid string) bool
	WriteUser(user User)
	GetUser(id string) *User
	GetUserByName(username string) *User
	DeleteUser(id, username string) bool
	GetResponse(url string) []byte
	WriteResponse(url string, response []byte)
	GetProgress(playerUuid, ratingKey string) int
	WriteProgress(playerUuid, ratingKey string, percent int, duration time.Duration)
	DeleteProgress(playerUuid, ratingKey string)
	Ping(ctx context.Context) error
}

// Utils
func flatTransform(s string) []string { return []string{} }
