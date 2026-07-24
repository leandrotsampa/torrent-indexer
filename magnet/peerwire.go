package magnet

/*
	Minimal BitTorrent peer wire client, just enough to download the torrent info
	dict from a peer: handshake, extension protocol (BEP-0010) and ut_metadata
	(BEP-0009). Nothing else of the protocol is implemented — no pieces are ever
	requested, so this never downloads content.
	See http://www.bittorrent.org/beps/bep_0010.html
	and http://www.bittorrent.org/beps/bep_0009.html
*/

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/zeebo/bencode"

	goscrape "github.com/leandrotsampa/torrent-indexer/scrape"
)

const (
	peerProtocol = "BitTorrent protocol"
	// handshakeSize is 1 (pstrlen) + 19 (pstr) + 8 (reserved) + 20 (infohash) + 20 (peer id).
	handshakeSize = 68
	// extensionReservedByte/Bit mark support for the extension protocol in the
	// reserved bytes of the handshake.
	extensionReservedByte = 5
	extensionReservedBit  = 0x10
	// messageIDExtended is the peer message carrying extension protocol payloads.
	messageIDExtended = 20
	// extensionIDHandshake is the extension message id of the handshake itself.
	extensionIDHandshake = 0
	// extensionIDUTMetadata is the id we advertise for ut_metadata; peers echo it
	// back on their data messages.
	extensionIDUTMetadata = 1

	metadataPieceSize = 16 * 1024
	// maxMetadataSize caps what a peer can make us allocate.
	maxMetadataSize = 8 * 1024 * 1024
	// maxPeerMessageSize caps a single peer message, likewise.
	maxPeerMessageSize = 1 << 20

	utMetadataMsgTypeRequest = 0
	utMetadataMsgTypeData    = 1
	utMetadataMsgTypeReject  = 2
)

var (
	// ErrPeerNoExtensions is returned by peers that do not speak BEP-0010, which
	// therefore cannot serve the metadata.
	ErrPeerNoExtensions = errors.New("peer does not support the extension protocol")
	// ErrPeerNoUTMetadata is returned by peers that speak BEP-0010 but not BEP-0009.
	ErrPeerNoUTMetadata = errors.New("peer does not support ut_metadata")
	// ErrMetadataRejected is returned when the peer refuses to serve a piece.
	ErrMetadataRejected = errors.New("peer rejected the metadata request")
	// ErrInfoHashMismatch is returned when the assembled metadata does not hash to
	// the requested infohash, meaning the peer sent something else.
	ErrInfoHashMismatch = errors.New("metadata does not match the requested infohash")
)

type extensionHandshake struct {
	M            map[string]int `bencode:"m"`
	MetadataSize int            `bencode:"metadata_size"`
}

type utMetadataHeader struct {
	MsgType   int `bencode:"msg_type"`
	Piece     int `bencode:"piece"`
	TotalSize int `bencode:"total_size"`
}

// fetchInfoDictFromPeer downloads the info dict of infoHash from a single peer.
// The result is verified against infoHash before being returned, so a peer that
// answers with unrelated bytes is rejected instead of poisoning the cache.
func fetchInfoDictFromPeer(ctx context.Context, addr netip.AddrPort, infoHash T, timeout time.Duration) ([]byte, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr.String())
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// One deadline for the whole exchange: the metadata is a few dozen KiB, so
	// there is no reason to allow a peer to trickle it.
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	if err := writePeerHandshake(conn, infoHash); err != nil {
		return nil, err
	}
	if err := readPeerHandshake(conn, infoHash); err != nil {
		return nil, err
	}

	localHandshake, err := bencode.EncodeBytes(map[string]interface{}{
		"m": map[string]interface{}{"ut_metadata": extensionIDUTMetadata},
		"v": "torrent-indexer",
	})
	if err != nil {
		return nil, err
	}
	if err := writeExtensionMessage(conn, extensionIDHandshake, localHandshake); err != nil {
		return nil, err
	}

	var (
		metadata       []byte
		received       = map[int]bool{}
		pieces         int
		peerMetadataID int
		requested      bool
	)

	for {
		messageID, payload, err := readPeerMessage(conn)
		if err != nil {
			return nil, err
		}
		if messageID != messageIDExtended || len(payload) < 1 {
			// Anything else (choke, bitfield, have, ...) is irrelevant here.
			continue
		}

		switch payload[0] {
		case extensionIDHandshake:
			var handshake extensionHandshake
			if err := bencode.DecodeBytes(payload[1:], &handshake); err != nil {
				return nil, fmt.Errorf("failed to decode extension handshake: %w", err)
			}

			peerMetadataID = handshake.M["ut_metadata"]
			if peerMetadataID == 0 {
				return nil, ErrPeerNoUTMetadata
			}
			if handshake.MetadataSize <= 0 || handshake.MetadataSize > maxMetadataSize {
				return nil, fmt.Errorf("peer advertised unusable metadata size: %d", handshake.MetadataSize)
			}

			metadata = make([]byte, handshake.MetadataSize)
			pieces = (handshake.MetadataSize + metadataPieceSize - 1) / metadataPieceSize
			if requested {
				continue
			}

			if err := requestMetadataPieces(conn, peerMetadataID, pieces); err != nil {
				return nil, err
			}
			requested = true

		case extensionIDUTMetadata:
			if metadata == nil {
				// Data before the handshake: we do not know the total size yet.
				continue
			}

			header, data, err := decodeUTMetadataMessage(payload[1:])
			if err != nil {
				return nil, err
			}

			switch header.MsgType {
			case utMetadataMsgTypeReject:
				return nil, ErrMetadataRejected
			case utMetadataMsgTypeData:
			default:
				continue
			}

			offset := header.Piece * metadataPieceSize
			if header.Piece < 0 || offset+len(data) > len(metadata) {
				return nil, fmt.Errorf("peer sent metadata piece %d out of range", header.Piece)
			}

			copy(metadata[offset:], data)
			received[header.Piece] = true
			if len(received) < pieces {
				continue
			}

			if HashBytes(metadata) != infoHash {
				return nil, ErrInfoHashMismatch
			}

			return metadata, nil
		}
	}
}

func writePeerHandshake(w io.Writer, infoHash T) error {
	peerID := goscrape.PeerID()

	buf := make([]byte, 0, handshakeSize)
	buf = append(buf, byte(len(peerProtocol)))
	buf = append(buf, peerProtocol...)
	reserved := make([]byte, 8)
	reserved[extensionReservedByte] |= extensionReservedBit
	buf = append(buf, reserved...)
	buf = append(buf, infoHash[:]...)
	buf = append(buf, peerID[:]...)

	_, err := w.Write(buf)

	return err
}

func readPeerHandshake(r io.Reader, infoHash T) error {
	buf := make([]byte, handshakeSize)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}

	if int(buf[0]) != len(peerProtocol) || string(buf[1:1+len(peerProtocol)]) != peerProtocol {
		return errors.New("peer sent an unexpected protocol header")
	}

	reserved := buf[20:28]
	if reserved[extensionReservedByte]&extensionReservedBit == 0 {
		return ErrPeerNoExtensions
	}

	if !bytes.Equal(buf[28:48], infoHash[:]) {
		return ErrInfoHashMismatch
	}

	return nil
}

func writeExtensionMessage(w io.Writer, extensionID byte, payload []byte) error {
	buf := make([]byte, 4+2+len(payload))
	binary.BigEndian.PutUint32(buf, uint32(2+len(payload)))
	buf[4] = messageIDExtended
	buf[5] = extensionID
	copy(buf[6:], payload)

	_, err := w.Write(buf)

	return err
}

// readPeerMessage reads one length-prefixed peer message. Keep-alives (length 0)
// return messageID -1 and no payload.
func readPeerMessage(r io.Reader) (int, []byte, error) {
	var lengthBuf [4]byte
	if _, err := io.ReadFull(r, lengthBuf[:]); err != nil {
		return 0, nil, err
	}

	length := binary.BigEndian.Uint32(lengthBuf[:])
	if length == 0 {
		return -1, nil, nil
	}
	if length > maxPeerMessageSize {
		return 0, nil, fmt.Errorf("peer sent an oversized message: %d bytes", length)
	}

	message := make([]byte, length)
	if _, err := io.ReadFull(r, message); err != nil {
		return 0, nil, err
	}

	return int(message[0]), message[1:], nil
}

func requestMetadataPieces(w io.Writer, peerMetadataID, pieces int) error {
	for piece := 0; piece < pieces; piece++ {
		request, err := bencode.EncodeBytes(map[string]interface{}{
			"msg_type": utMetadataMsgTypeRequest,
			"piece":    piece,
		})
		if err != nil {
			return err
		}

		if err := writeExtensionMessage(w, byte(peerMetadataID), request); err != nil {
			return err
		}
	}

	return nil
}

// decodeUTMetadataMessage splits a ut_metadata message into its bencoded header
// and the raw piece bytes that follow it.
func decodeUTMetadataMessage(payload []byte) (utMetadataHeader, []byte, error) {
	var header utMetadataHeader

	decoder := bencode.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&header); err != nil {
		return header, nil, fmt.Errorf("failed to decode ut_metadata header: %w", err)
	}

	parsed := decoder.BytesParsed()
	if parsed > len(payload) {
		return header, nil, errors.New("ut_metadata header longer than the message")
	}

	return header, payload[parsed:], nil
}
