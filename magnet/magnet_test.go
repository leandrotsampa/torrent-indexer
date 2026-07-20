package magnet

import "testing"

// zeroHash is the all-zeros infohash produced when parsing fails.
const zeroHash = "0000000000000000000000000000000000000000"

func TestParseMagnetUri_Base32Infohash(t *testing.T) {
	// lowercase base32 infohash (as emitted by torrentdosfilmes, comando1,
	// redetorrent, darkmahou) — previously failed with "illegal base32 data".
	lower, err := ParseMagnetUri("magnet:?xt=urn:btih:2cdllhkfhg3lfsib2rrcdvjejwrekb2e")
	if err != nil {
		t.Fatalf("lowercase base32 returned error: %v", err)
	}
	if lower.InfoHash.HexString() == zeroHash {
		t.Fatal("lowercase base32 decoded to the zero infohash")
	}

	// uppercase base32 must decode to the same infohash.
	upper, err := ParseMagnetUri("magnet:?xt=urn:btih:2CDLLHKFHG3LFSIB2RRCDVJEJWREKB2E")
	if err != nil {
		t.Fatalf("uppercase base32 returned error: %v", err)
	}
	if lower.InfoHash != upper.InfoHash {
		t.Errorf("lowercase (%s) and uppercase (%s) base32 decoded to different infohashes",
			lower.InfoHash.HexString(), upper.InfoHash.HexString())
	}
}

func TestParseMagnetUri_HexInfohash(t *testing.T) {
	const hexHash = "d08cb5d453b6cad903a3628a2648b122d1428e84"
	m, err := ParseMagnetUri("magnet:?xt=urn:btih:" + hexHash)
	if err != nil {
		t.Fatalf("hex infohash returned error: %v", err)
	}
	if got := m.InfoHash.HexString(); got != hexHash {
		t.Errorf("infohash = %q, want %q", got, hexHash)
	}
}

func TestParseMagnetUri_InvalidLengthDoesNotPanic(t *testing.T) {
	// A validly-decoding but wrong-length payload must return an error, not panic.
	if _, err := ParseMagnetUri("magnet:?xt=urn:btih:aaaa"); err == nil {
		t.Fatal("expected error for malformed infohash, got nil")
	}
}
