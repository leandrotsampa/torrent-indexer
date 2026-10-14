package requester

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"github.com/fereidani/httpdecompressor"
	"github.com/leandrotsampa/torrent-indexer/cache"
	"github.com/leandrotsampa/torrent-indexer/logging"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

const (
	cacheKey = "shortLivedCache"
)

// Markers of a real Cloudflare interstitial/challenge. Note: "challenge-platform"
// is intentionally NOT here — Cloudflare injects /cdn-cgi/challenge-platform/
// scripts into normal 200 pages too, so matching it produces false positives
// that discard valid content (e.g. ad-gateway pages that carry the magnet).
var challangeRegex = regexp.MustCompile(`(?i)(just a moment|cf-chl-bypass|under attack|cf-mitigated)`)

type Requster struct {
	fs                        *FlareSolverr
	c                         *cache.Redis
	httpClient                *http.Client
	shortLivedCacheExpiration time.Duration
}

func NewRequester(fs *FlareSolverr, c *cache.Redis, timeout time.Duration) *Requster {
	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableCompression:  false,
			MaxIdleConns:        100,              // Increase connection pool
			MaxIdleConnsPerHost: 10,               // More connections per host
			IdleConnTimeout:     90 * time.Second, // Keep connections alive longer
			DisableKeepAlives:   false,            // Enable keep-alive
			ForceAttemptHTTP2:   true,             // Use HTTP/2 when possible
		},
	}

	return &Requster{fs: fs, httpClient: httpClient, c: c, shortLivedCacheExpiration: 30 * time.Minute}
}

func (i *Requster) SetShortLivedCacheExpiration(expiration time.Duration) {
	i.shortLivedCacheExpiration = expiration
}

func (i *Requster) GetDocument(ctx context.Context, url string, referer ...string) (io.ReadCloser, error) {
	var body io.ReadCloser

	// Extract referer if provided
	ref := ""
	if len(referer) > 0 {
		ref = referer[0]
	}

	// try request from short-lived cache
	key := fmt.Sprintf("%s:%s", cacheKey, url)
	bodyByte, err := i.c.Get(ctx, key)
	if err == nil {
		logging.Debug().Str("url", url).Msg("Returning from short-lived cache")
		body = io.NopCloser(bytes.NewReader(bodyByte))
		return body, nil
	}

	// try request with plain client
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for url %s: %w", url, err)
	}

	// Add browser-like headers to spoof a real browser
	spoofBrowserHeaders(req, ref)

	resp, err := i.httpClient.Do(req)
	if err != nil {
		// try request with flare solverr
		body, err = i.fs.Get(ctx, url, 3)
		if err != nil {
			return nil, fmt.Errorf("failed to do request for url %s: %w", url, err)
		}
	} else {
		defer resp.Body.Close()

		// Decompress response using httpdecompressor
		body, err = httpdecompressor.Reader(resp)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress response: %w", err)
		}
		defer body.Close()
	}

	// Pre-allocate buffer based on Content-Length if available
	var buf bytes.Buffer
	if resp != nil && resp.ContentLength > 0 {
		buf.Grow(int(resp.ContentLength))
	} else {
		buf.Grow(32 * 1024) // Default 32KB pre-allocation
	}

	// Use io.Copy instead of io.ReadAll for better performance
	_, err = io.Copy(&buf, body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	bodyByte = buf.Bytes()
	if hasChallange(bodyByte) {
		// try request with flare solverr
		body, err = i.fs.Get(ctx, url, 3)
		if err != nil {
			return nil, fmt.Errorf("failed to do request for url %s: %w", url, err)
		}
		bodyByte, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
		logging.Debug().Str("url", url).Msg("Request served from flaresolverr")
	} else {
		logging.Debug().Str("url", url).Msg("Request served from plain client")
	}

	// save response to cache if it's not a challange, body is not empty and is valid HTML
	if !hasChallange(bodyByte) && len(bodyByte) > 0 && utils.IsValidHTML(string(bodyByte)) {
		err = i.c.SetWithExpiration(ctx, key, bodyByte, i.shortLivedCacheExpiration)
		if err != nil {
			logging.Error().Err(err).Str("url", url).Msg("Failed to save response to cache")
		}
		logging.Debug().Str("url", url).Msg("Saved to cache")
	} else {
		return nil, fmt.Errorf("response is a challange")
	}

	return io.NopCloser(bytes.NewReader(bodyByte)), nil
}

// PostForm sends an application/x-www-form-urlencoded POST (e.g. a WordPress
// admin-ajax call) and returns the raw response body. It uses only the plain
// HTTP client: FlareSolverr's request.post returns the browser-rendered page,
// which mangles non-HTML (JSON) responses, so there is no fallback to it.
// Successful (2xx, non-challenge, non-empty) responses are kept in the
// short-lived cache, keyed by URL + encoded form; see ExpirePostForm.
func (i *Requster) PostForm(ctx context.Context, url string, form neturl.Values, referer string) ([]byte, error) {
	key := postFormCacheKey(url, form)
	if bodyByte, err := i.c.Get(ctx, key); err == nil {
		logging.Debug().Str("url", url).Msg("Returning POST from short-lived cache")
		return bodyByte, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create POST request for url %s: %w", url, err)
	}
	spoofBrowserHeaders(req, referer)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Del("Sec-Fetch-User")
	req.Header.Del("Upgrade-Insecure-Requests")

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do POST request for url %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := httpdecompressor.Reader(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress response: %w", err)
	}
	defer body.Close()

	bodyByte, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("unexpected status %d for POST %s", resp.StatusCode, url)
	}
	if hasChallange(bodyByte) {
		return nil, fmt.Errorf("response is a challange")
	}

	if len(bodyByte) > 0 {
		if err := i.c.SetWithExpiration(ctx, key, bodyByte, i.shortLivedCacheExpiration); err != nil {
			logging.Error().Err(err).Str("url", url).Msg("Failed to save POST response to cache")
		}
	}
	return bodyByte, nil
}

// ExpirePostForm removes a cached PostForm response.
func (i *Requster) ExpirePostForm(ctx context.Context, url string, form neturl.Values) error {
	return i.c.Del(ctx, postFormCacheKey(url, form))
}

func postFormCacheKey(url string, form neturl.Values) string {
	return fmt.Sprintf("%s:POST:%s:%s", cacheKey, url, form.Encode())
}

func (i *Requster) ExpireDocument(ctx context.Context, url string) error {
	key := fmt.Sprintf("%s:%s", cacheKey, url)
	return i.c.Del(ctx, key)
}

// hasChallange checks if the body contains a challange by regex matching
func hasChallange(body []byte) bool {
	return challangeRegex.Match(body)
}
