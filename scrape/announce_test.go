package goscrape

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"
)

// startFakeTracker answers connect and announce requests (BEP-0015) on localhost
// with the given peers and counters, and returns its udp:// URL.
func startFakeTracker(t *testing.T, peers []netip.AddrPort, seeders, leechers uint32) string {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n < 16 {
				continue
			}

			action := binary.BigEndian.Uint32(buf[8:])
			transactionID := binary.BigEndian.Uint32(buf[12:])

			switch action {
			case actionConnect:
				response := make([]byte, 16)
				binary.BigEndian.PutUint32(response[0:], actionConnect)
				binary.BigEndian.PutUint32(response[4:], transactionID)
				binary.BigEndian.PutUint64(response[8:], 0x1122334455667788)
				_, _ = conn.WriteToUDP(response, addr)

			case actionAnnounce:
				response := make([]byte, announceResponseHead+announcePeerSize*len(peers))
				binary.BigEndian.PutUint32(response[0:], actionAnnounce)
				binary.BigEndian.PutUint32(response[4:], transactionID)
				binary.BigEndian.PutUint32(response[8:], 1800)
				binary.BigEndian.PutUint32(response[12:], leechers)
				binary.BigEndian.PutUint32(response[16:], seeders)

				offset := announceResponseHead
				for _, peer := range peers {
					ipv4 := peer.Addr().As4()
					copy(response[offset:], ipv4[:])
					binary.BigEndian.PutUint16(response[offset+4:], peer.Port())
					offset += announcePeerSize
				}
				_, _ = conn.WriteToUDP(response, addr)
			}
		}
	}()

	return fmt.Sprintf("udp://%s/announce", conn.LocalAddr().String())
}

func TestAnnounceReturnsPeersAndCounters(t *testing.T) {
	want := []netip.AddrPort{
		netip.MustParseAddrPort("192.168.1.10:51413"),
		netip.MustParseAddrPort("203.0.113.7:6881"),
	}
	trackerURL := startFakeTracker(t, want, 12, 34)

	announcer, err := New(trackerURL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	announcer.SetTimeout(2 * time.Second)

	result, err := announcer.Announce([]byte("0123456789abcdef0123456789abcdef01234567"))
	if err != nil {
		t.Fatalf("Announce() error = %v", err)
	}
	if result.Seeders != 12 || result.Leechers != 34 {
		t.Fatalf("seeders = %d, leechers = %d", result.Seeders, result.Leechers)
	}
	if len(result.Peers) != len(want) {
		t.Fatalf("got %d peers, want %d", len(result.Peers), len(want))
	}
	for i, peer := range result.Peers {
		if peer != want[i] {
			t.Fatalf("peer[%d] = %s, want %s", i, peer, want[i])
		}
	}
}

func TestAnnounceReportsEmptySwarm(t *testing.T) {
	trackerURL := startFakeTracker(t, nil, 0, 0)

	announcer, err := New(trackerURL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	announcer.SetTimeout(2 * time.Second)

	_, err = announcer.Announce([]byte("0123456789abcdef0123456789abcdef01234567"))
	if !errors.Is(err, ErrNoPeers) {
		t.Fatalf("error = %v, want ErrNoPeers", err)
	}
}

func TestAnnounceRejectsInvalidInfohash(t *testing.T) {
	trackerURL := startFakeTracker(t, nil, 0, 0)

	announcer, err := New(trackerURL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := announcer.Announce([]byte("not-hex")); err == nil {
		t.Fatal("expected an error for a non hex-encoded infohash")
	}
}

func TestPeerIDIsStableAndPrefixed(t *testing.T) {
	first := PeerID()
	if string(first[:8]) != "-TI0001-" {
		t.Fatalf("peer id prefix = %q", first[:8])
	}
	if PeerID() != first {
		t.Fatal("peer id changed between calls")
	}
}
