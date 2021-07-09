package store

import (
	"context"
)

// Store is the interface for All the store types
type Store interface {
	WriteServer(serverUuid string)
	GetServer(serverUuid string) bool
	WriteUser(user User)
	GetUser(id string) *User
	GetUserByName(username string) *User
	DeleteUser(id, username string) bool
	Ping(ctx context.Context) error
}

// Utils
func flatTransform(s string) []string { return []string{} }
