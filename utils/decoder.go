package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
)

// DarkmahouAdKey is the default KEY_STRING embedded in darkmahou.io's link
// obfuscation script. It is used to derive the AES-GCM key when the key cannot
// be extracted dynamically from the page. See DecodeDarkmahouAdLink.
const DarkmahouAdKey = "encodandotudoetodosagora"

func DecodeAdLink(encodedStr string) (string, error) {
	if encodedStr == "" {
		return "", fmt.Errorf("empty string")
	}
	reversed := reverseString(encodedStr)

	decodedBytes, err := base64.StdEncoding.DecodeString(reversed)
	if err != nil {
		return "", err
	}

	htmlUnescaped := html.UnescapeString(string(decodedBytes))

	return htmlUnescaped, nil
}

func Base64Decode(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("empty string")
	}
	decodedBytes, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return "", err
	}
	return string(decodedBytes), nil
}

// Helper function to reverse a string
func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// unshuffleStringByStep reconstructs an original string from a shuffled version.
// The input was created by picking characters using a stepping cursor (advancing
// by step positions through the original, skipping already-used positions).
// This function inverts the process: it walks the same cursor sequence and places
// each shuffled[i] back into original[index].
func unshuffleStringByStep(shuffled string, step int) (string, error) {
	runes := []rune(shuffled)
	length := len(runes)
	if length == 0 {
		return "", fmt.Errorf("empty string")
	}
	if step <= 0 {
		return "", fmt.Errorf("step must be greater than 0")
	}

	original := make([]rune, length)
	used := make([]bool, length)
	index := 0

	for i := 0; i < length; i++ {
		for used[index] {
			index = (index + 1) % length
		}
		used[index] = true
		original[i] = runes[index]
		index = (index + step) % length
	}

	return string(original), nil
}

// DecodeStarckDataU decodes the obfuscated magnet link from the data-u attribute. This is indexer-specific.
func DecodeStarckDataU(dataU string) (string, error) {
	if dataU == "" {
		return "", fmt.Errorf("empty data-u value")
	}

	unshuffled, err := unshuffleStringByStep(dataU, 3)
	if err != nil {
		return "", fmt.Errorf("unshuffle failed: %w", err)
	}

	if !IsMagnetLink(unshuffled) {
		return "", fmt.Errorf("decoded string is not a valid magnet link: %s", unshuffled)
	}

	return unshuffled, nil
}

func IsMagnetLink(link string) bool {
	return len(link) > 8 && link[:8] == "magnet:?"
}

// DecodeDarkmahouAdLink reverts the AES-GCM obfuscation used by darkmahou.io.
// The site's client-side script encrypts each real link with AES-GCM, using
// SHA-256(keyString) as the key, and packs the result as
// base64(nonce[12] || tag[16] || ciphertext), exposing it through
// https://systemads1.com/go.php?id=<url-encoded base64>. The decrypted payload
// is a JSON object {"url": <real link>, "exp": <unix ts>}.
//
// encoded must already be URL-decoded (e.g. via url.Query().Get("id")).
// keyString is the KEY_STRING found in the page JS; pass an empty string to use
// the default DarkmahouAdKey. Expiration ("exp") is intentionally not enforced,
// since the returned link stays valid regardless of the ad-gateway timestamp.
func DecodeDarkmahouAdLink(encoded, keyString string) (string, error) {
	if encoded == "" {
		return "", fmt.Errorf("empty string")
	}
	if keyString == "" {
		keyString = DarkmahouAdKey
	}

	combined, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode failed: %w", err)
	}
	// Layout: nonce(12) + tag(16) + ciphertext(>=0)
	if len(combined) < 12+16 {
		return "", fmt.Errorf("payload too short: %d bytes", len(combined))
	}
	nonce := combined[:12]
	tag := combined[12:28]
	ciphertext := combined[28:]

	key := sha256.Sum256([]byte(keyString))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// Go's GCM expects the tag appended to the ciphertext; the site stores it
	// before the ciphertext, so reassemble as ciphertext || tag.
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)

	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("aes-gcm open failed: %w", err)
	}

	var payload struct {
		URL string `json:"url"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return "", fmt.Errorf("json unmarshal failed: %w", err)
	}
	if payload.URL == "" {
		return "", fmt.Errorf("decoded payload has empty url")
	}

	return payload.URL, nil
}
