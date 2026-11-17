package magnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/zeebo/bencode"

	"github.com/leandrotsampa/torrent-indexer/cache"
	"github.com/leandrotsampa/torrent-indexer/logging"
	goscrape "github.com/leandrotsampa/torrent-indexer/scrape"
)

// ErrNoMetadata is returned when no peer could serve the torrent metadata.
var ErrNoMetadata = errors.New("unable to resolve torrent metadata from peers")

// ErrRecentlyFailed is returned, without touching the swarm, while a torrent is
// still within the cool-down that follows a failed lookup.
var ErrRecentlyFailed = errors.New("torrent metadata lookup failed recently")

const (
	metadataCacheExpiration = 7 * 24 * time.Hour
	// defaultFailureCooldown is how long a failed lookup is remembered. Torrents
	// with no reachable peer stay that way for a while, and every search used to
	// retry them, flooding the swarm and holding searches back.
	defaultFailureCooldown = 30 * time.Minute
	// lookupDeadline caps a single swarm lookup. It runs detached from the
	// callers, so it needs its own bound.
	lookupDeadline = 2 * time.Minute
)

// metadataStore is the slice of the cache the resolver needs; *cache.Redis
// satisfies it.
type metadataStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	SetWithExpiration(ctx context.Context, key string, value []byte, expiration time.Duration) error
}

// lookupCall is a swarm lookup shared by everyone asking for the same torrent.
type lookupCall struct {
	done     chan struct{}
	metadata *MetadataResponse
	err      error
}

type TorrentFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// MetadataResponse is the torrent metadata read from the info dict.
type MetadataResponse struct {
	InfoHash string        `json:"info_hash"`
	Name     string        `json:"name"`
	Size     int64         `json:"size"`
	Files    []TorrentFile `json:"files"`
}

// ResolverConfig configures how hard the resolver tries before giving up. The
// defaults are deliberately short: a search never waits on this (the caller
// bounds it and retries in the background), so it is better to fail fast.
type ResolverConfig struct {
	Enabled bool
	// PeerDiscoveryTimeout bounds the announce round to the trackers.
	PeerDiscoveryTimeout time.Duration
	// PeerTimeout bounds the whole exchange with a single peer.
	PeerTimeout time.Duration
	// MaxPeers is how many peers to collect from the trackers.
	MaxPeers int
	// Concurrency is how many peers are contacted at the same time.
	Concurrency int
	// FailureCooldown is how long a lookup that found no metadata is remembered
	// before the swarm is asked again.
	FailureCooldown time.Duration
}

// Resolver resolves torrent metadata (name, size and files) directly from peers,
// with no external service involved: peers come from the trackers via announce
// (BEP-0015) and the info dict from ut_metadata (BEP-0009). A tracker scrape,
// which is what feeds seeders/leechers, cannot provide any of this — only peers
// hold the info dict.
type Resolver struct {
	config ResolverConfig
	c      *cache.Redis
	store  metadataStore
	// lookup asks the swarm; a field so tests can replace the network.
	lookup func(ctx context.Context, m Magnet) (*MetadataResponse, error)

	mu       sync.Mutex
	inflight map[string]*lookupCall
}

func NewResolver(config ResolverConfig, c *cache.Redis) *Resolver {
	if config.PeerDiscoveryTimeout <= 0 {
		config.PeerDiscoveryTimeout = 2 * time.Second
	}
	if config.PeerTimeout <= 0 {
		config.PeerTimeout = 5 * time.Second
	}
	if config.MaxPeers <= 0 {
		config.MaxPeers = 30
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 8
	}
	if config.FailureCooldown <= 0 {
		config.FailureCooldown = defaultFailureCooldown
	}

	r := &Resolver{config: config, c: c, inflight: make(map[string]*lookupCall)}
	// A nil *cache.Redis in the interface would not compare equal to nil.
	if c != nil {
		r.store = c
	}
	r.lookup = r.lookupSwarm

	return r
}

func (r *Resolver) IsEnabled() bool {
	return r != nil && r.config.Enabled
}

func metadataCacheKey(infoHash string) string { return "metadata:" + infoHash }

func metadataFailureKey(infoHash string) string { return "metadata-miss:" + infoHash }

// FetchMetadata returns the metadata of the given magnet link, from cache when
// available and from the swarm otherwise.
//
// The swarm lookup is shared: concurrent calls for the same torrent wait on a
// single lookup, which runs detached from them, so a caller giving up on its own
// deadline does not cancel it and its result still reaches the cache. A lookup
// that finds no metadata is remembered for FailureCooldown, during which the
// call fails at once with ErrRecentlyFailed.
func (r *Resolver) FetchMetadata(ctx context.Context, magnetURI string) (*MetadataResponse, error) {
	if !r.IsEnabled() {
		return nil, errors.New("magnet metadata resolver is not enabled")
	}

	m, err := ParseMagnetUri(magnetURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse magnet URI: %w", err)
	}
	infoHash := m.InfoHash.HexString()

	if r.store != nil {
		if cached, err := r.store.Get(ctx, metadataCacheKey(infoHash)); err == nil && cached != nil {
			var metadata MetadataResponse
			if err := json.Unmarshal(cached, &metadata); err == nil {
				return &metadata, nil
			}
		}
		if failed, err := r.store.Get(ctx, metadataFailureKey(infoHash)); err == nil && failed != nil {
			return nil, fmt.Errorf("%w: %s", ErrRecentlyFailed, infoHash)
		}
	}

	call := r.sharedLookup(infoHash, m)
	select {
	case <-call.done:
		return call.metadata, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// sharedLookup returns the lookup in flight for infoHash, starting one if there
// is none.
func (r *Resolver) sharedLookup(infoHash string, m Magnet) *lookupCall {
	r.mu.Lock()
	defer r.mu.Unlock()

	if call, ok := r.inflight[infoHash]; ok {
		return call
	}

	call := &lookupCall{done: make(chan struct{})}
	r.inflight[infoHash] = call

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lookupDeadline)
		defer cancel()

		call.metadata, call.err = r.lookup(ctx, m)
		r.remember(ctx, infoHash, call.metadata, call.err)

		r.mu.Lock()
		delete(r.inflight, infoHash)
		r.mu.Unlock()
		close(call.done)
	}()

	return call
}

// remember caches a lookup's outcome: the metadata on success, and a cool-down
// marker when the swarm had nothing to give. Other errors (a malformed info
// dict, the lookup deadline) are not the torrent's fault and are not remembered.
func (r *Resolver) remember(ctx context.Context, infoHash string, metadata *MetadataResponse, err error) {
	if r.store == nil {
		return
	}

	if err == nil {
		if cacheData, err := json.Marshal(metadata); err == nil {
			_ = r.store.SetWithExpiration(ctx, metadataCacheKey(infoHash), cacheData, metadataCacheExpiration)
		}
		return
	}

	// A lookup cut short by its deadline also reports ErrNoMetadata, but the
	// swarm was never fully asked, so it says nothing about the torrent.
	if errors.Is(err, ErrNoMetadata) && ctx.Err() == nil {
		_ = r.store.SetWithExpiration(ctx, metadataFailureKey(infoHash), []byte("1"), r.config.FailureCooldown)
		logging.Debug().
			Err(err).
			Str("info_hash", infoHash).
			Dur("cooldown", r.config.FailureCooldown).
			Msg("Torrent metadata lookup failed; not retrying until the cool-down ends")
	}
}

// lookupSwarm resolves the metadata from the swarm: peers from the trackers,
// then the info dict from the first peer that serves it.
func (r *Resolver) lookupSwarm(ctx context.Context, m Magnet) (*MetadataResponse, error) {
	peers := goscrape.GetPeers(ctx, r.c, m.InfoHash.HexString(), m.Trackers, r.config.MaxPeers, r.config.PeerDiscoveryTimeout)
	if len(peers) == 0 {
		return nil, fmt.Errorf("%w: no peers found for %s", ErrNoMetadata, m.InfoHash)
	}

	infoDict, err := r.fetchInfoDict(ctx, peers, m.InfoHash)
	if err != nil {
		return nil, err
	}

	metadata, err := parseInfoDict(infoDict, m.InfoHash)
	if err != nil {
		return nil, err
	}

	logging.Debug().
		Str("info_hash", m.InfoHash.HexString()).
		Str("name", metadata.Name).
		Int64("size", metadata.Size).
		Int("peers", len(peers)).
		Msg("Resolved torrent metadata from peers")

	return metadata, nil
}

// fetchInfoDict asks the peers concurrently and keeps the first verified answer.
// Most peers never reply, so contacting several at once is what makes this
// usable at all.
func (r *Resolver) fetchInfoDict(ctx context.Context, peers []netip.AddrPort, infoHash T) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan []byte, len(peers))
	slots := make(chan struct{}, r.config.Concurrency)

	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func(peer netip.AddrPort) {
			defer wg.Done()

			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}

			infoDict, err := fetchInfoDictFromPeer(ctx, peer, infoHash, r.config.PeerTimeout)
			if err != nil {
				logging.Debug().Err(err).Str("peer", peer.String()).Msg("Peer did not serve the torrent metadata")
				return
			}

			results <- infoDict
		}(peer)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	infoDict, ok := <-results
	if !ok {
		return nil, fmt.Errorf("%w: %d peers contacted for %s", ErrNoMetadata, len(peers), infoHash)
	}

	return infoDict, nil
}

type infoDict struct {
	Name   string `bencode:"name"`
	Length int64  `bencode:"length"`
	Files  []struct {
		Length int64    `bencode:"length"`
		Path   []string `bencode:"path"`
	} `bencode:"files"`
}

// parseInfoDict reads name, total size and file list out of a verified info dict.
func parseInfoDict(raw []byte, infoHash T) (*MetadataResponse, error) {
	var info infoDict
	if err := bencode.DecodeBytes(raw, &info); err != nil {
		return nil, fmt.Errorf("failed to decode info dict: %w", err)
	}

	metadata := &MetadataResponse{
		InfoHash: infoHash.HexString(),
		Name:     strings.TrimSpace(info.Name),
	}

	if len(info.Files) == 0 {
		metadata.Size = info.Length
		if metadata.Name != "" {
			metadata.Files = []TorrentFile{{Path: metadata.Name, Size: info.Length}}
		}

		return metadata, nil
	}

	for _, file := range info.Files {
		metadata.Size += file.Length
		metadata.Files = append(metadata.Files, TorrentFile{
			Path: path.Join(file.Path...),
			Size: file.Length,
		})
	}

	return metadata, nil
}
