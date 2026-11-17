package requester

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/leandrotsampa/torrent-indexer/cache"
)

// liveRedis returns a client for the Redis at REDIS_HOST, skipping the test
// when there is none: the TTL rules under test are Redis' own.
func liveRedis(t *testing.T) *redis.Client {
	t.Helper()
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		t.Skip("set REDIS_HOST to run this against a real Redis")
	}
	client := redis.NewClient(&redis.Options{Addr: host + ":6379"})
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Skipf("no Redis at %s: %v", host, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestKeepEmptyDocumentBrieflyShortensButNeverRenews(t *testing.T) {
	client := liveRedis(t)
	ctx := context.Background()

	r := NewRequester(NewFlareSolverr("", 5000, 1), cache.NewRedis(), 5*time.Second)
	r.SetEmptyResultsCacheExpiration(5 * time.Minute)

	url := fmt.Sprintf("https://example.test/?s=empty-%d", time.Now().UnixNano())
	key := fmt.Sprintf("%s:%s", cacheKey, url)
	t.Cleanup(func() { client.Del(ctx, key) })

	// cached as a successful search page, with the short-lived expiration
	if err := client.Set(ctx, key, "<html><body>no results</body></html>", 30*time.Minute).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := r.KeepEmptyDocumentBriefly(ctx, url); err != nil {
		t.Fatalf("KeepEmptyDocumentBriefly() error = %v", err)
	}
	ttl := client.TTL(ctx, key).Val()
	if ttl <= 0 || ttl > 5*time.Minute {
		t.Fatalf("TTL = %v, want at most 5m and still cached", ttl)
	}

	// a repeated empty search (served from cache) must not push it back up
	client.Expire(ctx, key, time.Minute)
	if err := r.KeepEmptyDocumentBriefly(ctx, url); err != nil {
		t.Fatalf("second KeepEmptyDocumentBriefly() error = %v", err)
	}
	if ttl := client.TTL(ctx, key).Val(); ttl > time.Minute {
		t.Fatalf("TTL = %v after a repeated search, want it never renewed past 1m", ttl)
	}
}

func TestKeepEmptyDocumentBrieflyWithZeroDropsThePage(t *testing.T) {
	client := liveRedis(t)
	ctx := context.Background()

	r := NewRequester(NewFlareSolverr("", 5000, 1), cache.NewRedis(), 5*time.Second)
	r.SetEmptyResultsCacheExpiration(0)

	url := fmt.Sprintf("https://example.test/?s=empty-zero-%d", time.Now().UnixNano())
	key := fmt.Sprintf("%s:%s", cacheKey, url)
	if err := client.Set(ctx, key, "<html></html>", 30*time.Minute).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := r.KeepEmptyDocumentBriefly(ctx, url); err != nil {
		t.Fatalf("KeepEmptyDocumentBriefly() error = %v", err)
	}
	if n := client.Exists(ctx, key).Val(); n != 0 {
		t.Fatal("page still cached, want it dropped when the expiration is 0")
	}
}
