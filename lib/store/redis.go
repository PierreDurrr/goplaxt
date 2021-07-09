package store

import (
	"context"
	"strings"
	"time"

	"github.com/go-redis/redis"
)

const (
	serverPrefix  = "goplaxt:server:"
	userPrefix    = "goplaxt:user:"
	userMapPrefix = "goplaxt:usermap:"
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

// WriteServer will write a server uuid to redis
func (s RedisStore) WriteServer(serverUuid string) {
	s.client.SetNX(serverPrefix+serverUuid, serverUuid, 0)
}

func (s RedisStore) GetServer(serverUuid string) bool {
	count := s.client.Exists(serverPrefix + serverUuid).Val()
	return count > 0
}

// WriteUser will write a user object to redis
func (s RedisStore) WriteUser(user User) {
	oldId := s.client.Get(userMapPrefix + user.Username).String()
	pipe := s.client.Pipeline()
	data := make(map[string]interface{})
	data["username"] = user.Username
	data["access"] = user.AccessToken
	data["refresh"] = user.RefreshToken
	data["updated"] = user.Updated.Format("01-02-2006")
	pipe.HMSet(userPrefix+user.ID, data)
	pipe.Set(userMapPrefix+user.Username, user.ID, 0)
	if oldId != "" {
		pipe.Del(userPrefix + oldId)
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
