package requester

import (
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"
)

// clearanceDefaultTTL is how long a Cloudflare clearance is reused when the
// solver does not report the cookie expiry (Byparr does not). A clearance the
// site stops accepting is dropped on the first challenge anyway.
const clearanceDefaultTTL = 20 * time.Minute

// clearance is what a solved Cloudflare challenge leaves behind: the cookies
// (cf_clearance and friends) and the user agent they were issued to. Cloudflare
// accepts them from a plain HTTP client as long as both match, so the next
// pages of the site skip FlareSolverr/Byparr — a solve takes 15-40s, a plain
// request about 1s.
type clearance struct {
	cookies   string
	userAgent string
	expires   time.Time
}

// clearanceStore keeps one clearance per site, keyed by the cookie domain.
type clearanceStore struct {
	mu       sync.RWMutex
	byDomain map[string]clearance
}

func newClearanceStore() *clearanceStore {
	return &clearanceStore{byDomain: map[string]clearance{}}
}

func (s *clearanceStore) save(domain, cookies, userAgent string, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byDomain[strings.TrimPrefix(strings.ToLower(domain), ".")] = clearance{cookies: cookies, userAgent: userAgent, expires: expires}
}

// lookup returns the clearance of host or of a parent domain it belongs to.
func (s *clearanceStore) lookup(host string) (clearance, bool) {
	host = strings.ToLower(host)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for domain, c := range s.byDomain {
		if host != domain && !strings.HasSuffix(host, "."+domain) {
			continue
		}
		if time.Now().After(c.expires) {
			return clearance{}, false
		}
		return c, true
	}
	return clearance{}, false
}

// drop forgets the clearance covering host, after the site rejected it.
func (s *clearanceStore) drop(host string) {
	host = strings.ToLower(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	for domain := range s.byDomain {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			delete(s.byDomain, domain)
		}
	}
}

// solvedCookie is a cookie as FlareSolverr/Byparr report them.
type solvedCookie struct {
	Name   string
	Value  string
	Domain string
	Expiry int
}

// saveSolved records the clearance a solve left for rawURL's site. Only a
// solve that produced cf_clearance counts; the cookies kept are the ones of
// that site (a solved page also collects ad-network cookies).
func (s *clearanceStore) saveSolved(rawURL, userAgent string, cookies []solvedCookie) {
	parsed, err := neturl.Parse(rawURL)
	if err != nil || userAgent == "" {
		return
	}
	host := strings.ToLower(parsed.Hostname())

	domain := ""
	expires := time.Now().Add(clearanceDefaultTTL)
	for _, c := range cookies {
		cookieDomain := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
		if c.Name != "cf_clearance" || (cookieDomain != "" && host != cookieDomain && !strings.HasSuffix(host, "."+cookieDomain)) {
			continue
		}
		domain = cookieDomain
		if domain == "" {
			domain = host
		}
		if c.Expiry > 0 {
			expires = time.Unix(int64(c.Expiry), 0)
		}
	}
	if domain == "" {
		return
	}

	var pairs []string
	for _, c := range cookies {
		if !isCloudflareCookie(c.Name) {
			continue
		}
		cookieDomain := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
		if cookieDomain == "" || domain == cookieDomain || strings.HasSuffix(domain, "."+cookieDomain) || strings.HasSuffix(cookieDomain, "."+domain) {
			pairs = append(pairs, c.Name+"="+c.Value)
		}
	}
	s.save(domain, strings.Join(pairs, "; "), userAgent, expires)
}

// isCloudflareCookie reports whether name is one of Cloudflare's own cookies
// (cf_clearance, __cf_bm, _cfuvid...). Only those are replayed: the site's
// cookies (a PHP session, say) belong to the solver's browser, and replaying
// them on every request would mix the state of unrelated requests.
func isCloudflareCookie(name string) bool {
	return name == "cf_clearance" || strings.HasPrefix(name, "__cf") || strings.HasPrefix(name, "_cf")
}

// isCloudflareChallenge reports whether resp is a Cloudflare challenge: the
// plain client cannot get past it, so retrying is pointless.
func isCloudflareChallenge(resp *http.Response) bool {
	return resp != nil && strings.EqualFold(resp.Header.Get("Cf-Mitigated"), "challenge")
}
