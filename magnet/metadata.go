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

const metadataCacheExpiration = 7 * 24 * time.Hour

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
}

// Resolver resolves torrent metadata (name, size and files) directly from peers,
// with no external service involved: peers come from the trackers via announce
// (BEP-0015) and the info dict from ut_metadata (BEP-0009). A tracker scrape,
// which is what feeds seeders/leechers, cannot provide any of this — only peers
// hold the info dict.
type Resolver struct {
	config ResolverConfig
	c      *cache.Redis
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

	return &Resolver{config: config, c: c}
}

func (r *Resolver) IsEnabled() bool {
	return r != nil && r.config.Enabled
}

// FetchMetadata returns the metadata of the given magnet link, from cache when
// available and from the swarm otherwise.
func (r *Resolver) FetchMetadata(ctx context.Context, magnetURI string) (*MetadataResponse, error) {
	if !r.IsEnabled() {
		return nil, errors.New("magnet metadata resolver is not enabled")
	}

	m, err := ParseMagnetUri(magnetURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse magnet URI: %w", err)
	}

	cacheKey := fmt.Sprintf("metadata:%s", m.InfoHash)
	if cached, err := r.c.Get(ctx, cacheKey); err == nil && cached != nil {
		var metadata MetadataResponse
		if err := json.Unmarshal(cached, &metadata); err == nil {
			return &metadata, nil
		}
	}

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

	if cacheData, err := json.Marshal(metadata); err == nil {
		_ = r.c.SetWithExpiration(ctx, cacheKey, cacheData, metadataCacheExpiration)
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
