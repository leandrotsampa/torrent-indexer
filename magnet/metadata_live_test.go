package magnet

import (
	"context"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	goscrape "github.com/leandrotsampa/torrent-indexer/scrape"
)

// TestLiveMetadataFromSwarm resolves a real torrent against the public swarm, to
// check the whole path (tracker announce + ut_metadata) outside of the fake peer.
// It needs network access, so it only runs when LIVE_METADATA_INFOHASH is set:
//
//	LIVE_METADATA_INFOHASH=<hex> go test ./magnet/ -run TestLiveMetadataFromSwarm -v
func TestLiveMetadataFromSwarm(t *testing.T) {
	infoHashHex := os.Getenv("LIVE_METADATA_INFOHASH")
	if infoHashHex == "" {
		t.Skip("set LIVE_METADATA_INFOHASH=<hex> to run this against the real swarm")
	}

	var infoHash T
	if err := infoHash.FromHexString(infoHashHex); err != nil {
		t.Fatalf("bad infohash: %v", err)
	}

	trackers := []string{
		"udp://tracker.opentrackr.org:1337/announce",
		"udp://open.demonii.com:1337/announce",
		"udp://tracker.torrent.eu.org:451/announce",
		"udp://open.stealth.si:80/announce",
		"udp://exodus.desync.com:6969/announce",
		"udp://open.tracker.cl:1337/announce",
	}

	var (
		mu    sync.Mutex
		peers []netip.AddrPort
		seen  = map[netip.AddrPort]bool{}
		wg    sync.WaitGroup
	)

	for _, tracker := range trackers {
		wg.Add(1)
		go func(tracker string) {
			defer wg.Done()

			announcer, err := goscrape.New(tracker)
			if err != nil {
				return
			}
			announcer.SetTimeout(3 * time.Second)
			announcer.SetRetryLimit(1)

			result, err := announcer.Announce([]byte(infoHashHex))
			if err != nil {
				t.Logf("%s: %v", tracker, err)
				return
			}

			t.Logf("%s: seeders=%d leechers=%d peers=%d", tracker, result.Seeders, result.Leechers, len(result.Peers))

			mu.Lock()
			defer mu.Unlock()
			for _, peer := range result.Peers {
				if !seen[peer] {
					seen[peer] = true
					peers = append(peers, peer)
				}
			}
		}(tracker)
	}
	wg.Wait()

	if len(peers) == 0 {
		t.Skip("no peers returned by the trackers")
	}
	t.Logf("unique peers: %d", len(peers))

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	resolver := NewResolver(ResolverConfig{Enabled: true}, nil)
	infoDict, err := resolver.fetchInfoDict(ctx, peers, infoHash)
	if err != nil {
		t.Skipf("no peer served the metadata: %v", err)
	}

	metadata, err := parseInfoDict(infoDict, infoHash)
	if err != nil {
		t.Fatalf("parseInfoDict() error = %v", err)
	}

	t.Logf("name: %s", metadata.Name)
	t.Logf("size: %d bytes", metadata.Size)
	for _, file := range metadata.Files {
		t.Logf("file: %s (%d bytes)", file.Path, file.Size)
	}

	if metadata.Name == "" || metadata.Size == 0 {
		t.Fatalf("metadata is incomplete: %+v", metadata)
	}
}
