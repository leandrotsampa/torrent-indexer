package goscrape

/*
	This file implements the announce request of BEP-0015, which the scrape
	request (scrape.go) does not cover: only announce returns peers, and peers are
	the ones that can serve the torrent metadata (BEP-0009). The tracker itself
	knows nothing but the peer counts.
	See http://www.bittorrent.org/beps/bep_0015.html
*/

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/leandrotsampa/torrent-indexer/cache"
	"github.com/leandrotsampa/torrent-indexer/logging"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

const (
	announceRequestSize  = 98
	announceResponseHead = 20
	announcePeerSize     = 6
	// announceNumWant is how many peers to ask each tracker for. A handful is
	// plenty: any peer that already holds the metadata can serve it.
	announceNumWant int32 = 50
)

// ErrNoPeers is returned when the tracker answered without any peer.
var ErrNoPeers = errors.New("tracker returned no peers")

// AnnounceResult represents the response of an announce request.
type AnnounceResult struct {
	Interval uint32
	Seeders  uint32
	Leechers uint32
	Peers    []netip.AddrPort
}

var (
	localPeerIDOnce sync.Once
	localPeerID     [20]byte
)

// PeerID returns the peer id used to identify this indexer to peers and
// trackers. It is generated once per process, in the Azureus-style format.
func PeerID() [20]byte {
	localPeerIDOnce.Do(func() {
		copy(localPeerID[:], "-TI0001-")
		suffix := make([]byte, 12)
		if _, err := rand.Read(suffix); err != nil {
			// A predictable suffix is harmless here: the id only has to be stable
			// for the lifetime of a connection.
			for i := range suffix {
				suffix[i] = byte('0' + i)
			}
		}
		copy(localPeerID[8:], suffix)
	})

	return localPeerID
}

// Announce announces the given hex-encoded infohash to the tracker and returns
// the peers it knows about.
func (g *Goscrape) Announce(infohash []byte) (*AnnounceResult, error) {
	decodedInfohash := make([]byte, hex.DecodedLen(len(infohash)))
	if _, err := hex.Decode(decodedInfohash, infohash); err != nil {
		return nil, err
	}
	if len(decodedInfohash) != 20 {
		return nil, ErrResponse
	}

	conn, connectionID, err := g.connect()
	if err != nil {
		return nil, err
	}

	tid := g.transactionID()
	peerID := PeerID()

	buf := make([]byte, announceRequestSize)
	binary.BigEndian.PutUint64(buf[0:], connectionID)
	binary.BigEndian.PutUint32(buf[8:], actionAnnounce)
	binary.BigEndian.PutUint32(buf[12:], tid)
	copy(buf[16:], decodedInfohash)
	copy(buf[36:], peerID[:])
	binary.BigEndian.PutUint64(buf[56:], 0)   // downloaded
	binary.BigEndian.PutUint64(buf[64:], 0)   // left
	binary.BigEndian.PutUint64(buf[72:], 0)   // uploaded
	binary.BigEndian.PutUint32(buf[80:], 0)   // event: none
	binary.BigEndian.PutUint32(buf[84:], 0)   // ip: default
	binary.BigEndian.PutUint32(buf[88:], tid) // key
	binary.BigEndian.PutUint32(buf[92:], uint32(announceNumWant))
	binary.BigEndian.PutUint16(buf[96:], 6881) // port

	response := make([]byte, announceResponseHead+announcePeerSize*int(announceNumWant))

	var n, retries int
	for {
		retries++

		if err := conn.SetWriteDeadline(time.Now().Add(g.timeout)); err != nil {
			return nil, err
		}

		n, err = conn.Write(buf)
		if err != nil {
			return nil, err
		}
		if n != len(buf) {
			return nil, ErrRequest
		}

		if err := conn.SetReadDeadline(time.Now().Add(g.timeout)); err != nil {
			return nil, err
		}

		n, err = conn.Read(response)
		if err, ok := err.(net.Error); ok && err.Timeout() {
			if retries > g.retries {
				return nil, ErrRetryLimit
			}
			continue
		} else if err != nil {
			return nil, err
		}
		break
	}

	if n < announceResponseHead {
		return nil, ErrResponse
	}
	if transactionID := binary.BigEndian.Uint32(response[4:]); transactionID != tid {
		return nil, ErrInvalidTransactionID
	}

	switch action := binary.BigEndian.Uint32(response[0:]); action {
	case actionError:
		return nil, ErrRemote
	case actionAnnounce:
	default:
		return nil, ErrInvalidAction
	}

	result := &AnnounceResult{
		Interval: binary.BigEndian.Uint32(response[4+4:]),
		Leechers: binary.BigEndian.Uint32(response[12:]),
		Seeders:  binary.BigEndian.Uint32(response[16:]),
	}

	for offset := announceResponseHead; offset+announcePeerSize <= n; offset += announcePeerSize {
		addr, ok := netip.AddrFromSlice(response[offset : offset+4])
		if !ok {
			continue
		}

		port := binary.BigEndian.Uint16(response[offset+4:])
		if port == 0 || !addr.IsValid() || addr.IsUnspecified() {
			continue
		}

		result.Peers = append(result.Peers, netip.AddrPortFrom(addr, port))
	}

	if len(result.Peers) == 0 {
		return result, ErrNoPeers
	}

	return result, nil
}

// GetPeers announces the infohash to the given trackers plus the additional
// tracker list and returns up to limit unique peers, giving up after timeout.
func GetPeers(ctx context.Context, r *cache.Redis, infoHash string, trackers []string, limit int, timeout time.Duration) []netip.AddrPort {
	additionalTrackers := getAdditionalTrackers(ctx, r)
	allTrackers := make([]string, 0, len(trackers)+len(additionalTrackers))
	allTrackers = append(allTrackers, trackers...)
	allTrackers = append(allTrackers, additionalTrackers...)
	allTrackers = utils.StableUniq(allTrackers)

	// Buffered so a tracker answering after the deadline does not leak its
	// goroutine waiting for a receiver that is already gone.
	peerChan := make(chan []netip.AddrPort, len(allTrackers))
	for _, tracker := range allTrackers {
		go func(tracker string) {
			announcer, err := New(tracker)
			if err != nil {
				peerChan <- nil
				return
			}

			announcer.SetTimeout(timeout)
			announcer.SetRetryLimit(1)

			result, err := announcer.Announce([]byte(infoHash))
			if err != nil {
				peerChan <- nil
				return
			}

			peerChan <- result.Peers
		}(tracker)
	}

	deadline := time.After(timeout)
	seen := make(map[netip.AddrPort]bool)
	peers := make([]netip.AddrPort, 0, limit)

	for responses := 0; responses < len(allTrackers) && len(peers) < limit; {
		select {
		case <-ctx.Done():
			return peers
		case <-deadline:
			logging.Debug().Str("info_hash", infoHash).Int("peers", len(peers)).Msg("Peer discovery deadline reached")
			return peers
		case trackerPeers := <-peerChan:
			responses++
			for _, peer := range trackerPeers {
				if seen[peer] {
					continue
				}

				seen[peer] = true
				peers = append(peers, peer)
				if len(peers) >= limit {
					break
				}
			}
		}
	}

	logging.Debug().Str("info_hash", infoHash).Int("peers", len(peers)).Msg("Discovered peers from trackers")

	return peers
}
