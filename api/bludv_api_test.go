package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/leandrotsampa/torrent-indexer/cache"
	"github.com/leandrotsampa/torrent-indexer/requester"
)

// bludvGatePost is a post page as bludv serves it now: every download link goes
// through the /gate/go/ link locker.
const bludvGatePost = `<html><head>%s</head><body>
<div class="post"><div class="title"><h1>Ranma1/2 3ª Temporada Torrent (2026) WEB-DL 1080p Dual Áudio</h1></div>
<div class="content">
<p>Título Original: Ranma1/2<br />Ano de Lançamento: 2026</p>
<p><center><strong>EPISÓDIO 01:</strong> <a href="https://bludvfilmes2.xyz/gate/go/21317f4385748b2e?p=80919">1080p</a></center><br />
<center><strong>EPISÓDIO 02:</strong> <a href="https://bludvfilmes2.xyz/gate/go/645d07a18150c5af?p=80919">1080p</a></center></p>
</div></div></body></html>`

// bludvAPIContent is the same post content as the WordPress REST API serves it,
// with the plain magnets.
const bludvAPIContent = `<p>Título Original: Ranma1/2</p>
<p><center><strong>EPIS&Oacute;DIO 01:</strong> <a href="magnet:?xt=urn:btih:d3902325a6a276d02240283194b6d78b4e61b856&#038;dn=Ranma1-2%20S03E01%20WEB-DL" rel="noopener">1080p</a></center><br />
<center><strong>EPIS&Oacute;DIO 02:</strong> <a href="magnet:?xt=urn:btih:7182b90777a2ec227a3d2c94b5bb6694df147eb8&#038;dn=Ranma1-2%20S03E02%20WEB-DL" rel="noopener">1080p</a></center></p>`

func parseBluDVTestDoc(t *testing.T, head string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(strings.Replace(bludvGatePost, "%s", head, 1)))
	if err != nil {
		t.Fatalf("failed to parse document: %v", err)
	}
	return doc
}

func TestHasBluDVGateLinks(t *testing.T) {
	doc := parseBluDVTestDoc(t, "")
	if !hasBluDVGateLinks(doc.Find(".post div.content")) {
		t.Fatal("hasBluDVGateLinks() = false for a post with gate links")
	}

	plain, _ := goquery.NewDocumentFromReader(strings.NewReader(`<div class="content"><a href="magnet:?xt=urn:btih:aaa">1080p</a></div>`))
	if hasBluDVGateLinks(plain.Find("div.content")) {
		t.Fatal("hasBluDVGateLinks() = true for a post with plain magnets")
	}
}

func TestBluDVPostAPIURL(t *testing.T) {
	cases := []struct {
		name, head, want string
	}{
		{
			name: "from the page's REST alternate link",
			head: `<link rel="alternate" type="application/json" href="https://bludvfilmes2.xyz/wp-json/wp/v2/posts/80919" />`,
			want: "https://bludvfilmes2.xyz/wp-json/wp/v2/posts/80919?_fields=content",
		},
		{
			name: "from the post id the gate links carry",
			head: "",
			want: bludv.URL + "wp-json/wp/v2/posts/80919?_fields=content",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := parseBluDVTestDoc(t, c.head)
			if got := bludvPostAPIURL(doc, doc.Find(".post div.content")); got != c.want {
				t.Fatalf("bludvPostAPIURL() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestExtractBluDVMagnetLinksFromAPIReadsThePlainMagnets(t *testing.T) {
	var apiHits int32
	var gotFields string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&apiHits, 1)
		gotFields = r.URL.Query().Get("_fields")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"rendered": bludvAPIContent},
		})
	}))
	t.Cleanup(api.Close)

	// no Redis listening: every cache read is a miss
	t.Setenv("REDIS_HOST", "127.0.0.1")
	i := &Indexer{requester: requester.NewRequester(requester.NewFlareSolverr("", 5000, 1), cache.NewRedis(), 5*time.Second)}

	head := `<link rel="alternate" type="application/json" href="` + api.URL + `/wp-json/wp/v2/posts/80919" />`
	doc := parseBluDVTestDoc(t, head)
	content := doc.Find(".post div.content")

	// the page itself yields nothing: every link is a gate
	if got := extractBluDVMagnetLinks(t.Context(), i, content, ""); len(got) != 0 {
		t.Fatalf("page links = %+v, want none (gate links are never followed)", got)
	}

	got := extractBluDVMagnetLinksFromAPI(t.Context(), i, doc, content, "https://bludvfilmes2.xyz/ranma1-2-3a-temporada/")
	if len(got) != 2 {
		t.Fatalf("got %d magnet links, want 2: %+v", len(got), got)
	}
	if !strings.Contains(got[0].URL, "d3902325a6a276d02240283194b6d78b4e61b856") || !strings.Contains(got[1].URL, "7182b90777a2ec227a3d2c94b5bb6694df147eb8") {
		t.Fatalf("magnets = %q, %q", got[0].URL, got[1].URL)
	}
	if got[0].Episode != "1" || got[1].Episode != "2" {
		t.Fatalf("episodes = %q, %q, want 1, 2", got[0].Episode, got[1].Episode)
	}
	if got[0].Label != "1080p" {
		t.Fatalf("label = %q, want 1080p", got[0].Label)
	}
	if apiHits != 1 || gotFields != "content" {
		t.Fatalf("API hits = %d, _fields = %q, want one request limited to content", apiHits, gotFields)
	}
}

func TestNormalizeBluDVSearchQuerySimplifiesSeasonSearches(t *testing.T) {
	cases := map[string]string{
		// the site only finds "Ranma1/2 3ª Temporada (2026)" without the year
		// and with the fraction spelled as two numbers
		"Ranma½ (2024) S03E06":       "Ranma 1 2 temporada 3",
		"ranma 1_2 (2024) S03E06":    "ranma 1 2 temporada 3",
		"ranma 1-2 2024 temporada 3": "ranma 1 2 temporada 3",
		// Sonarr's anime searches: absolute episode, season named by an alias;
		// a season search returns the whole season, so the episode goes
		"ranma 1/2 (2024) 3rd season 06": "ranma 1 2 temporada 3",
		"Ranma 1/2 (2024) Season 3":      "Ranma 1 2 temporada 3",
		"ranma1 & 2 temporada 3":         "ranma1 2 temporada 3",
		"ranma1 e 2 temporada 3":         "ranma1 2 temporada 3",
		// without a season: the fraction and the parenthesized year still go,
		// a bare year stays (a movie search "Duna 2021" needs it)
		"ranma 1_2 (2024) 06": "ranma 1 2 06",
		"Ranma½ (2024) 06":    "Ranma 1 2 06",
		"ranma 1_2 06":        "ranma 1 2 06",
		"ranma 1-2 2024 06":   "ranma 1 2 06",
		"ranma 2024 06":       "ranma 06",
		"Duna 2021":           "Duna 2021",
		"Duna (2021)":         "Duna",
	}
	for input, want := range cases {
		if got := normalizeBluDVSearchQuery(input); got != want {
			t.Errorf("normalizeBluDVSearchQuery(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMatchesBluDVRequestedTitleRanma(t *testing.T) {
	cases := []struct {
		q, title string
		want     bool
	}{
		{"Ranma½ (2024) S03E06", "Ranma1/2", true},
		{"Ranma½ (2024) S03E06", "Ranma1/2 3ª Temporada Torrent (2026) WEB-DL 1080p Dual Áudio", true},
		{"ranma 1-2 2024 temporada 3", "Ranma1/2 3ª Temporada Torrent (2026)", true},
		{"ranma1 2 temporada 3", "Ranma 1/2 3ª Temporada", true},
		// the year is only dropped from the end of the request, never ignored
		// inside it, and a different show stays rejected
		{"ranma 2024 temporada 3", "Ranma1/2", false},
		{"rick e morty 2013 s01e01", "Rick e Morty Multiverso 1a Temporada", false},
		{"rick e morty 2013 s01e01", "Rick e Morty 1a Temporada", true},
	}
	for _, c := range cases {
		if got := matchesBluDVRequestedTitle(c.q, c.title); got != c.want {
			t.Errorf("matchesBluDVRequestedTitle(%q, %q) = %v, want %v", c.q, c.title, got, c.want)
		}
	}
}

func TestExtractBluDVRequestedSeason(t *testing.T) {
	cases := map[string]string{
		// the "2" of the title must not be read as "2 temporada"
		"ranma1 2 temporada 3":     "3",
		"Ranma½ (2024) S03E06":     "3",
		"ranma 1 2 temporada 03":   "3",
		"rick and morty s01":       "1",
		"the boys 2ª temporada":    "2",
		"rick e morty temporada 1": "1",
		"ranma 1_2 (2024) 06":      "",
		"vingadores ultimato":      "",
	}
	for q, want := range cases {
		if got := extractBluDVRequestedSeason(q); got != want {
			t.Errorf("extractBluDVRequestedSeason(%q) = %q, want %q", q, got, want)
		}
	}
}

func TestNormalizeBluDVSearchQueryConvertsSeasonOnly(t *testing.T) {
	// the site names seasons "4ª Temporada": "S04" alone finds nothing
	cases := map[string]string{
		"the boys S04":      "the boys temporada 4",
		"rick and morty s8": "rick e morty temporada 8",
		// an "s" inside a word is not a season
		"mrs3 doubtfire": "mrs3 doubtfire",
	}
	for input, want := range cases {
		if got := normalizeBluDVSearchQuery(input); got != want {
			t.Errorf("normalizeBluDVSearchQuery(%q) = %q, want %q", input, got, want)
		}
	}
}
