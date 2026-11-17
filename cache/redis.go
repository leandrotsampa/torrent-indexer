package cache

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	DefaultExpiration      = 24 * time.Hour * 7 // 7 days
	IndexerComandoTorrents = "indexer:comando_torrents"
)

type Redis struct {
	client            *redis.Client
	defaultExpiration time.Duration
}

func NewRedis() *Redis {
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost"
	}

	redisPassword := os.Getenv("REDIS_PASSWORD")
	// redisPassword can be empty when the server has no authentication enabled
	return &Redis{
		client: redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:6379", redisHost),
			Password: redisPassword,
		}),
		defaultExpiration: DefaultExpiration,
	}
}

func (r *Redis) SetDefaultExpiration(expiration time.Duration) {
	r.defaultExpiration = expiration
}

func (r *Redis) Get(ctx context.Context, key string) ([]byte, error) {
	return r.client.Get(ctx, key).Bytes()
}

func (r *Redis) Set(ctx context.Context, key string, value []byte) error {
	return r.client.Set(ctx, key, value, r.defaultExpiration).Err()
}

func (r *Redis) SetWithExpiration(ctx context.Context, key string, value []byte, expiration time.Duration) error {
	return r.client.Set(ctx, key, value, expiration).Err()
}

// ShortenExpiration lowers key's time to live to expiration, never raising it:
// a key read again and again keeps its original deadline instead of being
// renewed by every read. It needs Redis 7 (EXPIRE ... LT); on an older server
// the key is deleted instead, so it is never kept longer than asked.
func (r *Redis) ShortenExpiration(ctx context.Context, key string, expiration time.Duration) error {
	if err := r.client.ExpireLT(ctx, key, expiration).Err(); err != nil {
		return r.client.Del(ctx, key).Err()
	}
	return nil
}

func (r *Redis) Del(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}
