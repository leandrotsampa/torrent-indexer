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
	"sync"
	"time"

	"github.com/fereidani/httpdecompressor"
	"github.com/leandrotsampa/torrent-indexer/cache"
	"github.com/leandrotsampa/torrent-indexer/logging"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

const (
	cacheKey = "shortLivedCache"
	// maxPlainRetries is how many extra attempts the plain client gets on a
	// retryable status before falling back to FlareSolverr
	maxPlainRetries = 2
)

// Markers of a real Cloudflare interstitial/challenge. Note: "challenge-platform"
// is intentionally NOT here — Cloudflare injects /cdn-cgi/challenge-platform/
// scripts into normal 200 pages too, so matching it produces false positives
// that discard valid content (e.g. ad-gateway pages that carry the magnet).
var challangeRegex = regexp.MustCompile(`(?i)(just a moment|cf-chl-bypass|under attack|cf-mitigated)`)

type Requster struct {
	fs         *FlareSolverr
	c          *cache.Redis
	httpClient *http.Client
	// postClient is httpClient with room for the slow search endpoints sites
	// answer through POST (vaca's admin-ajax takes 4-15s).
	postClient                *http.Client
	shortLivedCacheExpiration time.Duration
	// siteLocks serializes the FlareSolverr solves of each site (host ->
	// chan struct{}): one request solves, the others reuse its clearance.
	siteLocks sync.Map
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

	postClient := &http.Client{Timeout: max(timeout, minPostFormTimeout), Transport: httpClient.Transport}

	return &Requster{fs: fs, httpClient: httpClient, postClient: postClient, c: c, shortLivedCacheExpiration: 30 * time.Minute}
}

// minPostFormTimeout is the least PostForm waits: a POST is a site's search
// endpoint, and those run a database query instead of serving a cached page.
const minPostFormTimeout = 30 * time.Second

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
	cleared := i.applyClearance(req, url)

	// some CDNs (e.g. Cloudflare on redestorrents.com) intermittently answer
	// 403/429/5xx to the same client; a new attempt usually goes through. A
	// Cloudflare challenge is final, though: it goes straight to FlareSolverr.
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		resp, err = i.httpClient.Do(req)
		if err == nil && isCloudflareChallenge(resp) {
			if cleared {
				// the site no longer accepts this clearance: solve again
				i.fs.dropClearance(url)
			}
			break
		}
		if err != nil || !isRetryableStatus(resp.StatusCode) || attempt == maxPlainRetries {
			break
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request for url %s: %w", url, err)
		}
		spoofBrowserHeaders(req, ref)
		i.applyClearance(req, url)
	}
	if err != nil {
		// try request with flare solverr
		body, err = i.solve(ctx, url, ref)
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
	// A 5xx without challenge markers is the site's own server failing (the
	// redestorrents search answers 500 to some queries): the solver cannot fix
	// it and would only hold the request until its own timeout.
	if resp != nil && resp.StatusCode >= http.StatusInternalServerError && !hasChallange(bodyByte) && !isCloudflareChallenge(resp) {
		return nil, fmt.Errorf("site answered HTTP %d for url %s", resp.StatusCode, url)
	}
	// also fall back on a still-blocked answer (e.g. a 403 without challenge markers)
	if hasChallange(bodyByte) || (resp != nil && isRetryableStatus(resp.StatusCode)) {
		// try request with flare solverr
		body, err = i.solve(ctx, url, ref)
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
	cleared := i.applyClearance(req, url)

	resp, err := i.postClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do POST request for url %s: %w", url, err)
	}
	defer resp.Body.Close()
	if cleared && isCloudflareChallenge(resp) {
		i.fs.dropClearance(url)
	}

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
// isRetryableStatus reports whether status is a transient block/failure worth
// retrying: 403, 429 and 5xx. Other 4xx (e.g. a legit 404) are final. After the
// retries, 403/429 go to FlareSolverr, but a 5xx only when it carries a
// Cloudflare challenge.
func isRetryableStatus(status int) bool {
	return status == http.StatusForbidden || status == http.StatusTooManyRequests || status >= 500
}

func hasChallange(body []byte) bool {
	return challangeRegex.Match(body)
}

// applyClearance sends the Cloudflare clearance a previous solve left for the
// site, with the user agent it was issued to; it reports whether it did.
func (i *Requster) applyClearance(req *http.Request, url string) bool {
	c, ok := i.fs.clearanceFor(url)
	if !ok {
		return false
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Cookie", c.cookies)
	return true
}

// ApplyClearance adds to req (built by a caller with its own HTTP client) the
// Cloudflare clearance a previous solve left for the site; it reports whether
// it did.
func (i *Requster) ApplyClearance(req *http.Request) bool {
	return i.applyClearance(req, req.URL.String())
}

// solve fetches url through FlareSolverr, one solve per site at a time. A
// request that waited for another one's solve first retries the plain client
// with the clearance it left, so a burst of pages costs a single solve.
func (i *Requster) solve(ctx context.Context, url, ref string) (io.ReadCloser, error) {
	release, err := i.lockSite(ctx, url)
	if err != nil {
		return nil, err
	}
	defer release()

	if body, ok := i.getWithClearance(ctx, url, ref); ok {
		return body, nil
	}
	return i.fs.Get(ctx, url, 3)
}

// lockSite takes the solve lock of url's site, giving up with the context.
func (i *Requster) lockSite(ctx context.Context, rawURL string) (func(), error) {
	host := rawURL
	if parsed, err := neturl.Parse(rawURL); err == nil {
		host = strings.ToLower(parsed.Hostname())
	}
	lock, _ := i.siteLocks.LoadOrStore(host, make(chan struct{}, 1))
	ch := lock.(chan struct{})
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// getWithClearance fetches url with the plain client and the site's clearance,
// if there is one; a clearance the site rejects is dropped.
func (i *Requster) getWithClearance(ctx context.Context, url, ref string) (io.ReadCloser, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	spoofBrowserHeaders(req, ref)
	if !i.applyClearance(req, url) {
		return nil, false
	}

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if isCloudflareChallenge(resp) {
		i.fs.dropClearance(url)
		return nil, false
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, false
	}

	decompressed, err := httpdecompressor.Reader(resp)
	if err != nil {
		return nil, false
	}
	defer decompressed.Close()
	bodyByte, err := io.ReadAll(decompressed)
	if err != nil || hasChallange(bodyByte) {
		return nil, false
	}
	logging.Debug().Str("url", url).Msg("Request served from plain client with a fresh clearance")
	return io.NopCloser(bytes.NewReader(bodyByte)), true
}
