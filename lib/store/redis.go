package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis"
	"github.com/xanderstrike/goplaxt/lib/internal"
)

const (
	userPrefix         = "goplaxt:user:"
	userMapPrefix      = "goplaxt:usermap:"
	accessTokenTimeout = 75 * 24 * time.Hour
	scrobbleFormat     = "goplaxt:scrobble:%s:%s"
	scrobbleTimeout    = 3 * time.Hour
)

// RedisStore is a storage engine that writes to redis
type RedisStore struct {
	client redis.Client
}

// NewRedisClient creates a new redis client object
func NewRedisClient(addr string, password string) redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})

	_, err := client.Ping().Result()
	// FIXME
	if err != nil {
		panic(err)
	}
	return *client
}

// NewRedisClientWithUrl creates a new redis client object
func NewRedisClientWithUrl(url string) redis.Client {
	option, err := redis.ParseURL(url)
	if err != nil {
		panic(err)
	}

	client := redis.NewClient(option)
	_, err = client.Ping().Result()
	if err != nil {
		panic(err)
	}
	return *client
}

// NewRedisStore creates new store
func NewRedisStore(client redis.Client) RedisStore {
	return RedisStore{
		client: client,
	}
}

// Ping will check if the connection works right
func (s RedisStore) Ping(ctx context.Context) error {
	_, err := s.client.WithContext(ctx).Ping().Result()
	return err
}

// WriteUser will write a user object to redis
func (s RedisStore) WriteUser(user User) {
	oldId := s.client.Get(userMapPrefix + user.Username).Val()
	pipe := s.client.Pipeline()
	data := make(map[string]interface{})
	data["username"] = user.Username
	data["access"] = user.AccessToken
	data["refresh"] = user.RefreshToken
	data["updated"] = user.Updated.Format("01-02-2006")
	pipe.HMSet(userPrefix+user.ID, data)
	pipe.Expire(userPrefix+user.ID, accessTokenTimeout)
	if oldId == "" {
		pipe.Set(userMapPrefix+user.Username, user.ID, accessTokenTimeout)
	}
	_, err := pipe.Exec()
	if err != nil {
		panic(err)
	}
}

// GetUser will load a user from redis
func (s RedisStore) GetUser(id string) *User {
	data, err := s.client.HGetAll(userPrefix + id).Result()
	if err != nil {
		return nil
	}
	updated, err := time.Parse("01-02-2006", data["updated"])
	if err != nil {
		return nil
	}
	user := User{
		ID:           id,
		Username:     strings.ToLower(data["username"]),
		AccessToken:  data["access"],
		RefreshToken: data["refresh"],
		Updated:      updated,
		store:        s,
	}

	return &user
}

// GetUserByName will load a user from redis
func (s RedisStore) GetUserByName(username string) *User {
	id, err := s.client.Get(userMapPrefix + username).Result()
	if err != nil {
		return nil
	}
	return s.GetUser(id)
}

// DeleteUser will delete a user from redis
func (s RedisStore) DeleteUser(id, username string) bool {
	pipe := s.client.Pipeline()
	pipe.Del(userPrefix + id)
	pipe.Del(userMapPrefix + username)
	_, err := pipe.Exec()
	return err == nil
}

func (s RedisStore) GetScrobbleBody(playerUuid, ratingKey string) (item internal.CacheItem) {
	item = internal.CacheItem{
		Body: internal.ScrobbleBody{
			Progress: 0,
		},
	}
	cache, err := s.client.Get(fmt.Sprintf(scrobbleFormat, playerUuid, ratingKey)).Bytes()
	if err != nil {
		return
	}
	_ = json.Unmarshal(cache, &item)
	return
}

func (s RedisStore) WriteScrobbleBody(item internal.CacheItem) {
	b, _ := json.Marshal(item)
	s.client.Set(fmt.Sprintf(scrobbleFormat, item.PlayerUuid, item.RatingKey), b, scrobbleTimeout)
}
