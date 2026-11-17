package requester

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leandrotsampa/torrent-indexer/cache"
)

func TestClearanceStoreLookup(t *testing.T) {
	s := newClearanceStore()
	s.save("bludvfilmes2.xyz", "cf_clearance=abc", "UA/1", time.Now().Add(time.Minute))

	for _, host := range []string{"bludvfilmes2.xyz", "www.bludvfilmes2.xyz"} {
		c, ok := s.lookup(host)
		if !ok || c.cookies != "cf_clearance=abc" || c.userAgent != "UA/1" {
			t.Errorf("lookup(%q) = %+v, %v", host, c, ok)
		}
	}
	for _, host := range []string{"evilbludvfilmes2.xyz", "bludvfilmes2.xyz.evil.com", "example.com"} {
		if _, ok := s.lookup(host); ok {
			t.Errorf("lookup(%q) matched another site's clearance", host)
		}
	}

	s.drop("www.bludvfilmes2.xyz")
	if _, ok := s.lookup("bludvfilmes2.xyz"); ok {
		t.Error("clearance still there after drop")
	}

	s.save("expired.example", "cf_clearance=old", "UA/1", time.Now().Add(-time.Second))
	if _, ok := s.lookup("expired.example"); ok {
		t.Error("expired clearance returned")
	}
}

// Only Cloudflare's cookies are replayed: the site's own session belongs to
// the solver's browser (replaying it made every vaca link resolve to the
// magnet of the first one).
func TestClearanceStoreKeepsOnlyCloudflareCookies(t *testing.T) {
	s := newClearanceStore()
	s.saveSolved("https://vaqueirofilmes.com/enc/go.php?id=1", "UA/1", []solvedCookie{
		{Name: "PHPSESSID", Value: "solver", Domain: "vaqueirofilmes.com"},
		{Name: "cf_clearance", Value: "ok", Domain: ".vaqueirofilmes.com"},
		{Name: "__cf_bm", Value: "bm", Domain: ".vaqueirofilmes.com"},
		{Name: "ads", Value: "x", Domain: ".adnetwork.example"},
	})
	c, ok := s.lookup("vaqueirofilmes.com")
	if !ok || c.cookies != "cf_clearance=ok; __cf_bm=bm" {
		t.Errorf("cookies = %q, %v; want only the Cloudflare ones", c.cookies, ok)
	}
}

// fakeCloudflareSite answers like a Cloudflare-protected site: a challenge
// unless the request carries the clearance cookie and the user agent it was
// issued to.
type fakeCloudflareSite struct {
	server  *httptest.Server
	hits    sync.Map // path -> *int32
	revoked atomic.Bool
}

const (
	fakeClearanceUA     = "Mozilla/5.0 (Test; rv:151.0) Gecko/20100101 Firefox/151.0"
	fakeClearanceCookie = "cf_clearance=ok"
)

func newFakeCloudflareSite(t *testing.T) *fakeCloudflareSite {
	t.Helper()
	site := &fakeCloudflareSite{}
	site.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter, _ := site.hits.LoadOrStore(r.URL.Path, new(int32))
		atomic.AddInt32(counter.(*int32), 1)

		cleared := !site.revoked.Load() &&
			strings.Contains(r.Header.Get("Cookie"), fakeClearanceCookie) &&
			r.Header.Get("User-Agent") == fakeClearanceUA
		if !cleared {
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "<html><head><title>Just a moment...</title></head><body></body></html>")
			return
		}
		_, _ = io.WriteString(w, "<html><body>page "+r.URL.Path+"</body></html>")
	}))
	t.Cleanup(site.server.Close)
	return site
}

func (s *fakeCloudflareSite) hitsOf(path string) int32 {
	if counter, ok := s.hits.Load(path); ok {
		return atomic.LoadInt32(counter.(*int32))
	}
	return 0
}

// newFakeByparr answers request.get with a solved page plus the clearance
// cookie for host, like Byparr/FlareSolverr; it implements no sessions.
func newFakeByparr(t *testing.T, host string, solves *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["cmd"] != "request.get" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		atomic.AddInt32(solves, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"solution": map[string]interface{}{
				"status":    200,
				"url":       req["url"],
				"userAgent": fakeClearanceUA,
				"response":  "<html><body>solved</body></html>",
				"cookies": []map[string]interface{}{
					{"name": "cf_clearance", "value": "ok", "domain": "." + host, "path": "/"},
					{"name": "ads", "value": "x", "domain": ".adnetwork.example", "path": "/"},
					{"name": "PHPSESSID", "value": "solver-session", "domain": host, "path": "/"},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestRequester(t *testing.T, fsURL string) *Requster {
	t.Helper()
	// no Redis listening: every cache read is a miss
	t.Setenv("REDIS_HOST", "127.0.0.1")
	return NewRequester(NewFlareSolverr(fsURL, 5000, 1), cache.NewRedis(), 5*time.Second)
}

func readDocument(t *testing.T, r *Requster, target string) string {
	t.Helper()
	body, err := r.GetDocument(t.Context(), target)
	if err != nil {
		t.Fatalf("GetDocument(%s) error = %v", target, err)
	}
	defer body.Close()
	b, _ := io.ReadAll(body)
	return string(b)
}

func TestGetDocumentReusesCloudflareClearance(t *testing.T) {
	site := newFakeCloudflareSite(t)
	host := strings.Split(strings.TrimPrefix(site.server.URL, "http://"), ":")[0]
	var solves int32
	r := newTestRequester(t, newFakeByparr(t, host, &solves).URL)

	// first page: challenged, solved by Byparr, which hands out the clearance
	if got := readDocument(t, r, site.server.URL+"/list"); !strings.Contains(got, "solved") {
		t.Fatalf("first page = %q, want Byparr's solved page", got)
	}
	if solves != 1 {
		t.Fatalf("solves = %d, want 1", solves)
	}
	// a challenge is final for the plain client: no retries before Byparr
	if hits := site.hitsOf("/list"); hits != 1 {
		t.Errorf("plain attempts on a challenge = %d, want 1", hits)
	}

	// next pages of the same site: plain client with the clearance, no Byparr
	for _, path := range []string{"/post-1", "/post-2"} {
		if got := readDocument(t, r, site.server.URL+path); !strings.Contains(got, "page "+path) {
			t.Errorf("%s = %q, want the real page", path, got)
		}
	}
	if solves != 1 {
		t.Errorf("solves = %d after reusing the clearance, want 1", solves)
	}

	// clearance revoked by the site: dropped, solved again, then reused again
	site.revoked.Store(true)
	if got := readDocument(t, r, site.server.URL+"/post-3"); !strings.Contains(got, "solved") {
		t.Errorf("post-3 = %q, want Byparr's solved page", got)
	}
	if solves != 2 {
		t.Errorf("solves = %d after the clearance was revoked, want 2", solves)
	}
	site.revoked.Store(false)
	if got := readDocument(t, r, site.server.URL+"/post-4"); !strings.Contains(got, "page /post-4") {
		t.Errorf("post-4 = %q, want the real page with the new clearance", got)
	}
	if solves != 2 {
		t.Errorf("solves = %d, want 2", solves)
	}
}

func TestPostFormUsesCloudflareClearance(t *testing.T) {
	site := newFakeCloudflareSite(t)
	host := strings.Split(strings.TrimPrefix(site.server.URL, "http://"), ":")[0]
	var solves int32
	r := newTestRequester(t, newFakeByparr(t, host, &solves).URL)

	if _, err := r.PostForm(t.Context(), site.server.URL+"/wp-admin/admin-ajax.php", url.Values{"action": {"x"}}, ""); err == nil {
		t.Fatal("PostForm before any clearance = nil error, want the challenge")
	}

	_ = readDocument(t, r, site.server.URL+"/list") // Byparr hands out the clearance
	body, err := r.PostForm(t.Context(), site.server.URL+"/wp-admin/admin-ajax.php", url.Values{"action": {"y"}}, "")
	if err != nil {
		t.Fatalf("PostForm with the clearance error = %v", err)
	}
	if !strings.Contains(string(body), "page /wp-admin/admin-ajax.php") {
		t.Errorf("PostForm body = %q", body)
	}
}

// Parallel requests to a site with no clearance yet must not each solve the
// challenge: one solves, the others wait and reuse its clearance (this is what
// keeps a burst of pages from flooding Byparr).
func TestGetDocumentSolvesOncePerSiteUnderConcurrency(t *testing.T) {
	site := newFakeCloudflareSite(t)
	host := strings.Split(strings.TrimPrefix(site.server.URL, "http://"), ":")[0]
	var solves int32
	byparr := newFakeByparr(t, host, &solves)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond) // a solve takes a while
		byparr.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(slow.Close)
	r := newTestRequester(t, slow.URL)

	var wg sync.WaitGroup
	for n := 0; n < 5; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			path := "/post-" + string(rune('a'+n))
			body, err := r.GetDocument(t.Context(), site.server.URL+path)
			if err != nil {
				t.Errorf("GetDocument(%s) error = %v", path, err)
				return
			}
			body.Close()
		}(n)
	}
	wg.Wait()

	if solves != 1 {
		t.Errorf("solves = %d for 5 parallel pages of one site, want 1", solves)
	}
}

func TestApplyClearanceOnForeignRequests(t *testing.T) {
	site := newFakeCloudflareSite(t)
	host := strings.Split(strings.TrimPrefix(site.server.URL, "http://"), ":")[0]
	var solves int32
	r := newTestRequester(t, newFakeByparr(t, host, &solves).URL)

	req, _ := http.NewRequest(http.MethodGet, site.server.URL+"/enc/go.php", nil)
	if r.ApplyClearance(req) {
		t.Fatal("ApplyClearance() = true before any solve")
	}

	_ = readDocument(t, r, site.server.URL+"/list") // Byparr hands out the clearance
	req, _ = http.NewRequest(http.MethodGet, site.server.URL+"/enc/go.php", nil)
	if !r.ApplyClearance(req) {
		t.Fatal("ApplyClearance() = false after a solve")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("foreign request with the clearance: status %d, want 200", resp.StatusCode)
	}
}

func TestGetDocumentDoesNotSolveServerErrors(t *testing.T) {
	var hits int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(site.Close)

	var solves int32
	r := newTestRequester(t, newFakeByparr(t, "127.0.0.1", &solves).URL)

	_, err := r.GetDocument(t.Context(), site.URL+"/index.php?busca=ranma+(2024)")
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("GetDocument() error = %v, want the site's HTTP 500", err)
	}
	if solves != 0 {
		t.Errorf("solves = %d, want 0: a plain 5xx is not a Cloudflare block", solves)
	}
	// still retried with the plain client, as 5xx can be transient
	if want := int32(maxPlainRetries + 1); hits != want {
		t.Errorf("plain attempts = %d, want %d", hits, want)
	}
}

func TestGetDocumentSolvesChallengedServerErrors(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "<html><head><title>Just a moment...</title></head></html>")
	}))
	t.Cleanup(site.Close)

	var solves int32
	r := newTestRequester(t, newFakeByparr(t, "127.0.0.1", &solves).URL)

	if got := readDocument(t, r, site.URL+"/page"); !strings.Contains(got, "solved") {
		t.Fatalf("page = %q, want the solver's page", got)
	}
	if solves != 1 {
		t.Errorf("solves = %d, want 1", solves)
	}
}
