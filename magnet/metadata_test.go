package magnet

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testInfoHash = "c9dcf0ec3378fb31fa2c4559ef6b783a385ce66a"

var testMagnet = "magnet:?xt=urn:btih:" + testInfoHash

var errNotFound = errors.New("not found")

// fakeStore is an in-memory metadataStore that also records each expiration.
type fakeStore struct {
	mu          sync.Mutex
	values      map[string][]byte
	expirations map[string]time.Duration
}

func newFakeStore() *fakeStore {
	return &fakeStore{values: map[string][]byte{}, expirations: map[string]time.Duration{}}
}

func (s *fakeStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return nil, errNotFound
	}
	return v, nil
}

func (s *fakeStore) SetWithExpiration(_ context.Context, key string, value []byte, expiration time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	s.expirations[key] = expiration
	return nil
}

func (s *fakeStore) expiration(key string) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.expirations[key]
	return d, ok
}

// newTestResolver returns a resolver whose swarm lookup is replaced by lookup.
func newTestResolver(store *fakeStore, lookup func(ctx context.Context, m Magnet) (*MetadataResponse, error)) *Resolver {
	r := NewResolver(ResolverConfig{Enabled: true, FailureCooldown: 10 * time.Minute}, nil)
	r.store = store
	r.lookup = lookup
	return r
}

// waitLookup blocks until no lookup for testInfoHash is in flight.
func waitLookup(t *testing.T, r *Resolver) {
	t.Helper()
	r.mu.Lock()
	call, ok := r.inflight[testInfoHash]
	r.mu.Unlock()
	if !ok {
		return
	}
	select {
	case <-call.done:
	case <-time.After(5 * time.Second):
		t.Fatal("lookup did not finish")
	}
}

func TestFetchMetadataSharesConcurrentLookups(t *testing.T) {
	var lookups atomic.Int32
	release := make(chan struct{})
	r := newTestResolver(newFakeStore(), func(ctx context.Context, m Magnet) (*MetadataResponse, error) {
		lookups.Add(1)
		<-release
		return &MetadataResponse{InfoHash: testInfoHash, Name: "Ranma", Size: 42}, nil
	})

	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := r.FetchMetadata(context.Background(), testMagnet)
			if err == nil && m.Name != "Ranma" {
				err = fmt.Errorf("unexpected metadata %+v", m)
			}
			errs <- err
		}()
	}

	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("FetchMetadata() error = %v", err)
		}
	}

	if got := lookups.Load(); got != 1 {
		t.Fatalf("swarm lookups = %d, want 1", got)
	}
}

func TestFetchMetadataLookupOutlivesCallerDeadline(t *testing.T) {
	var lookups atomic.Int32
	release := make(chan struct{})
	r := newTestResolver(newFakeStore(), func(ctx context.Context, m Magnet) (*MetadataResponse, error) {
		lookups.Add(1)
		<-release
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return &MetadataResponse{InfoHash: testInfoHash, Name: "Ranma", Size: 42}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := r.FetchMetadata(ctx, testMagnet); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FetchMetadata() error = %v, want deadline exceeded", err)
	}

	close(release)
	waitLookup(t, r)

	m, err := r.FetchMetadata(context.Background(), testMagnet)
	if err != nil {
		t.Fatalf("FetchMetadata() after the lookup error = %v", err)
	}
	if m.Name != "Ranma" || m.Size != 42 {
		t.Fatalf("metadata = %+v, want the one the detached lookup cached", m)
	}
	if got := lookups.Load(); got != 1 {
		t.Fatalf("swarm lookups = %d, want 1 (second call should hit the cache)", got)
	}
}

func TestFetchMetadataRemembersFailedLookups(t *testing.T) {
	var lookups atomic.Int32
	store := newFakeStore()
	r := newTestResolver(store, func(ctx context.Context, m Magnet) (*MetadataResponse, error) {
		lookups.Add(1)
		return nil, fmt.Errorf("%w: 1 peers contacted", ErrNoMetadata)
	})

	if _, err := r.FetchMetadata(context.Background(), testMagnet); !errors.Is(err, ErrNoMetadata) {
		t.Fatalf("first FetchMetadata() error = %v, want ErrNoMetadata", err)
	}
	if _, err := r.FetchMetadata(context.Background(), testMagnet); !errors.Is(err, ErrRecentlyFailed) {
		t.Fatalf("second FetchMetadata() error = %v, want ErrRecentlyFailed", err)
	}

	if got := lookups.Load(); got != 1 {
		t.Fatalf("swarm lookups = %d, want 1", got)
	}
	if d, ok := store.expiration(metadataFailureKey(testInfoHash)); !ok || d != 10*time.Minute {
		t.Fatalf("failure marker expiration = %v (set: %v), want the configured cool-down", d, ok)
	}
}

func TestFetchMetadataDoesNotRememberOtherErrors(t *testing.T) {
	var lookups atomic.Int32
	r := newTestResolver(newFakeStore(), func(ctx context.Context, m Magnet) (*MetadataResponse, error) {
		lookups.Add(1)
		return nil, errors.New("failed to decode info dict")
	})

	for range 2 {
		if _, err := r.FetchMetadata(context.Background(), testMagnet); err == nil {
			t.Fatal("FetchMetadata() error = nil, want the lookup error")
		}
	}

	if got := lookups.Load(); got != 2 {
		t.Fatalf("swarm lookups = %d, want 2 (only ErrNoMetadata starts a cool-down)", got)
	}
}

func TestNewResolverDefaultsFailureCooldown(t *testing.T) {
	r := NewResolver(ResolverConfig{Enabled: true}, nil)
	if r.config.FailureCooldown != defaultFailureCooldown {
		t.Fatalf("FailureCooldown = %v, want %v", r.config.FailureCooldown, defaultFailureCooldown)
	}
}
