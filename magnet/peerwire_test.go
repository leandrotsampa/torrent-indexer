package magnet

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/zeebo/bencode"
)

// fakePeerOptions tweaks the fake peer to reproduce misbehaving peers.
type fakePeerOptions struct {
	// noExtensions answers the handshake without the extension protocol bit.
	noExtensions bool
	// noUTMetadata answers the extension handshake without ut_metadata.
	noUTMetadata bool
	// reject answers every metadata request with msg_type 2.
	reject bool
	// corruptInfoDict serves bytes that do not hash to the requested infohash.
	corruptInfoDict bool
	// utMetadataID is the extension id the peer advertises (0 uses 3).
	utMetadataID int
}

// startFakePeer serves infoDict over the peer wire protocol on localhost and
// returns its address.
func startFakePeer(t *testing.T, infoDict []byte, opts fakePeerOptions) netip.AddrPort {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	utMetadataID := opts.utMetadataID
	if utMetadataID == 0 {
		utMetadataID = 3
	}

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

		// Handshake: echo back the infohash the client asked for.
		clientHandshake := make([]byte, handshakeSize)
		if _, err := io.ReadFull(conn, clientHandshake); err != nil {
			return
		}

		response := make([]byte, handshakeSize)
		response[0] = byte(len(peerProtocol))
		copy(response[1:], peerProtocol)
		if !opts.noExtensions {
			response[1+len(peerProtocol)+extensionReservedByte] |= extensionReservedBit
		}
		copy(response[28:], clientHandshake[28:48])
		copy(response[48:], "-FAKE01-000000000000")
		if _, err := conn.Write(response); err != nil {
			return
		}

		served := infoDict
		if opts.corruptInfoDict {
			served = append([]byte("d4:name5:otheree"), infoDict...)
		}

		extensions := map[string]interface{}{}
		if !opts.noUTMetadata {
			extensions["ut_metadata"] = utMetadataID
		}
		handshakePayload, err := bencode.EncodeBytes(map[string]interface{}{
			"m":             extensions,
			"metadata_size": len(served),
		})
		if err != nil {
			return
		}
		if err := writeExtensionMessage(conn, extensionIDHandshake, handshakePayload); err != nil {
			return
		}

		for {
			messageID, payload, err := readPeerMessage(conn)
			if err != nil {
				return
			}
			if messageID != messageIDExtended || len(payload) < 1 || int(payload[0]) != utMetadataID {
				continue
			}

			var request utMetadataHeader
			if err := bencode.DecodeBytes(payload[1:], &request); err != nil {
				return
			}

			msgType := utMetadataMsgTypeData
			if opts.reject {
				msgType = utMetadataMsgTypeReject
			}

			header, err := bencode.EncodeBytes(map[string]interface{}{
				"msg_type":   msgType,
				"piece":      request.Piece,
				"total_size": len(served),
			})
			if err != nil {
				return
			}

			body := header
			if msgType == utMetadataMsgTypeData {
				start := request.Piece * metadataPieceSize
				end := min(start+metadataPieceSize, len(served))
				if start > len(served) {
					return
				}
				body = append(header, served[start:end]...)
			}

			if err := writeExtensionMessage(conn, byte(extensionIDUTMetadata), body); err != nil {
				return
			}
		}
	}()

	addr := listener.Addr().(*net.TCPAddr)

	return netip.MustParseAddrPort(addr.String())
}

// buildInfoDict returns a bencoded info dict and its infohash.
func buildInfoDict(t *testing.T, value map[string]interface{}) ([]byte, T) {
	t.Helper()

	raw, err := bencode.EncodeBytes(value)
	if err != nil {
		t.Fatalf("failed to encode info dict: %v", err)
	}

	return raw, HashBytes(raw)
}

func TestFetchInfoDictFromPeerReturnsVerifiedMetadata(t *testing.T) {
	infoDict, infoHash := buildInfoDict(t, map[string]interface{}{
		"name":         "Stuart.Fails.to.Save.the.Universe.S01E01.1080p.WEB-DL.DUAL",
		"length":       1_503_238_553,
		"piece length": 262_144,
	})
	addr := startFakePeer(t, infoDict, fakePeerOptions{})

	got, err := fetchInfoDictFromPeer(context.Background(), addr, infoHash, 5*time.Second)
	if err != nil {
		t.Fatalf("fetchInfoDictFromPeer() error = %v", err)
	}
	if HashBytes(got) != infoHash {
		t.Fatal("returned info dict does not match the requested infohash")
	}

	metadata, err := parseInfoDict(got, infoHash)
	if err != nil {
		t.Fatalf("parseInfoDict() error = %v", err)
	}
	if metadata.Name != "Stuart.Fails.to.Save.the.Universe.S01E01.1080p.WEB-DL.DUAL" {
		t.Fatalf("name = %q", metadata.Name)
	}
	if metadata.Size != 1_503_238_553 {
		t.Fatalf("size = %d", metadata.Size)
	}
}

func TestFetchInfoDictFromPeerHandlesMultiPieceMetadata(t *testing.T) {
	// A file list long enough to span more than one 16 KiB piece, which is what a
	// full season post looks like.
	files := make([]interface{}, 0, 400)
	for i := 0; i < 400; i++ {
		files = append(files, map[string]interface{}{
			"length": int64(100_000_000 + i),
			"path":   []interface{}{"Season 01", fmt.Sprintf("Episode.%03d.1080p.WEB-DL.DUAL.mkv", i)},
		})
	}
	infoDict, infoHash := buildInfoDict(t, map[string]interface{}{
		"name":         "Stuart.Fails.to.Save.the.Universe.S01.1080p.WEB-DL.DUAL",
		"piece length": 262_144,
		"files":        files,
	})
	if len(infoDict) <= metadataPieceSize {
		t.Fatalf("info dict has %d bytes, expected more than one piece", len(infoDict))
	}

	addr := startFakePeer(t, infoDict, fakePeerOptions{})

	got, err := fetchInfoDictFromPeer(context.Background(), addr, infoHash, 5*time.Second)
	if err != nil {
		t.Fatalf("fetchInfoDictFromPeer() error = %v", err)
	}

	metadata, err := parseInfoDict(got, infoHash)
	if err != nil {
		t.Fatalf("parseInfoDict() error = %v", err)
	}
	if len(metadata.Files) != 400 {
		t.Fatalf("files = %d, want 400", len(metadata.Files))
	}
	if metadata.Files[0].Path != "Season 01/Episode.000.1080p.WEB-DL.DUAL.mkv" {
		t.Fatalf("first file path = %q", metadata.Files[0].Path)
	}

	var want int64
	for i := 0; i < 400; i++ {
		want += int64(100_000_000 + i)
	}
	if metadata.Size != want {
		t.Fatalf("size = %d, want %d", metadata.Size, want)
	}
}

func TestFetchInfoDictFromPeerRejectsMismatchedInfoHash(t *testing.T) {
	infoDict, infoHash := buildInfoDict(t, map[string]interface{}{"name": "whatever", "length": 1})
	addr := startFakePeer(t, infoDict, fakePeerOptions{corruptInfoDict: true})

	_, err := fetchInfoDictFromPeer(context.Background(), addr, infoHash, 5*time.Second)
	if !errors.Is(err, ErrInfoHashMismatch) {
		t.Fatalf("error = %v, want ErrInfoHashMismatch", err)
	}
}

func TestFetchInfoDictFromPeerReportsUnsupportedPeers(t *testing.T) {
	infoDict, infoHash := buildInfoDict(t, map[string]interface{}{"name": "whatever", "length": 1})

	tests := []struct {
		name string
		opts fakePeerOptions
		want error
	}{
		{name: "no extension protocol", opts: fakePeerOptions{noExtensions: true}, want: ErrPeerNoExtensions},
		{name: "no ut_metadata", opts: fakePeerOptions{noUTMetadata: true}, want: ErrPeerNoUTMetadata},
		{name: "rejected request", opts: fakePeerOptions{reject: true}, want: ErrMetadataRejected},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := startFakePeer(t, infoDict, tt.opts)

			_, err := fetchInfoDictFromPeer(context.Background(), addr, infoHash, 5*time.Second)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDecodeUTMetadataMessageSplitsHeaderAndData(t *testing.T) {
	header, err := bencode.EncodeBytes(map[string]interface{}{
		"msg_type":   utMetadataMsgTypeData,
		"piece":      1,
		"total_size": 40,
	})
	if err != nil {
		t.Fatalf("failed to encode header: %v", err)
	}

	payload := append(header, []byte("raw-info-dict-bytes")...)

	got, data, err := decodeUTMetadataMessage(payload)
	if err != nil {
		t.Fatalf("decodeUTMetadataMessage() error = %v", err)
	}
	if got.MsgType != utMetadataMsgTypeData || got.Piece != 1 || got.TotalSize != 40 {
		t.Fatalf("header = %+v", got)
	}
	if string(data) != "raw-info-dict-bytes" {
		t.Fatalf("data = %q", data)
	}
}

func TestReadPeerMessageReportsKeepAlive(t *testing.T) {
	var keepAlive [4]byte
	binary.BigEndian.PutUint32(keepAlive[:], 0)

	messageID, payload, err := readPeerMessage(newByteReader(keepAlive[:]))
	if err != nil {
		t.Fatalf("readPeerMessage() error = %v", err)
	}
	if messageID != -1 || payload != nil {
		t.Fatalf("messageID = %d, payload = %v", messageID, payload)
	}
}

func newByteReader(b []byte) io.Reader {
	return &sliceReader{data: b}
}

type sliceReader struct {
	data []byte
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}

	n := copy(p, r.data)
	r.data = r.data[n:]

	return n, nil
}
