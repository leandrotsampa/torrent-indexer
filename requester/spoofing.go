package requester

import (
	"net/http"
	"os"

	"github.com/fereidani/httpdecompressor"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

// spoofBrowserHeaders adds browser-like headers to spoof a real browser.
// If referer is empty, it defaults to "https://google.com/".
// The User-Agent can be overridden with the SPOOFED_USER_AGENT env var (e.g.
// for sites that block a browser UA whose TLS fingerprint does not match).
func spoofBrowserHeaders(req *http.Request, referer string) {
	if ua := os.Getenv("SPOOFED_USER_AGENT"); ua != "" {
		req.Header.Set("User-Agent", ua)
	} else {
		req.Header.Set("User-Agent", utils.SpoofedUserAgent)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Accept-Encoding", httpdecompressor.ACCEPT_ENCODING)

	// Use provided referer or default to Google
	if referer != "" {
		req.Header.Set("Referer", referer)
	} else {
		req.Header.Set("Referer", "https://google.com/")
	}

	req.Header.Set("DNT", "1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Cache-Control", "max-age=0")
}
