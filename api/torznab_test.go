package handler

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/leandrotsampa/torrent-indexer/schema"
)

func TestTorznabItemCarriesLanguages(t *testing.T) {
	def := torznabIndexer{ID: "bludv", Name: "BluDV"}
	torrent := schema.IndexedTorrent{
		Title:      "Superman 2025 1080p WEB-DL Dual Audio",
		Audio:      []schema.Audio{schema.AudioPortuguese4, schema.AudioEnglish},
		MagnetLink: "magnet:?xt=urn:btih:abc",
		InfoHash:   "abc",
		Size:       "2.45 GB",
		IMDB:       "https://www.imdb.com/title/tt1234567/",
		Year:       "2025",
		SeedCount:  10,
		LeechCount: 2,
		Date:       time.Date(2025, 9, 30, 13, 8, 58, 0, time.UTC),
	}

	item := newTorznabItem(def, torrent)
	attrs := map[string][]string{}
	for _, attr := range item.Attrs {
		attrs[attr.Name] = append(attrs[attr.Name], attr.Value)
	}

	languages := attrs["language"]
	if len(languages) != 2 || languages[0] != "Portuguese (Brazil)" || languages[1] != "English" {
		t.Errorf("language attributes = %v, want [Portuguese (Brazil) English]", languages)
	}

	if got := attrs["size"]; len(got) != 1 || got[0] != "2630667468" {
		t.Errorf("size attribute = %v, want [2630667468]", got)
	}
	if got := attrs["peers"]; len(got) != 1 || got[0] != "12" {
		t.Errorf("peers attribute = %v, want [12]", got)
	}
	if got := attrs["imdbid"]; len(got) != 1 || got[0] != "tt1234567" {
		t.Errorf("imdbid attribute = %v, want [tt1234567]", got)
	}
	if got := attrs["imdb"]; len(got) != 1 || got[0] != "1234567" {
		t.Errorf("imdb attribute = %v, want [1234567]", got)
	}
	if item.PubDate != "Tue, 30 Sep 2025 13:08:58 +0000" {
		t.Errorf("PubDate = %q", item.PubDate)
	}
	if item.Enclosure == nil || item.Enclosure.URL != torrent.MagnetLink {
		t.Errorf("Enclosure = %+v, want the magnet link", item.Enclosure)
	}
}

func TestTorznabItemSkipsUnknownAndEmptyMetadata(t *testing.T) {
	def := torznabIndexer{ID: "bludv", Name: "BluDV"}
	item := newTorznabItem(def, schema.IndexedTorrent{Title: "Some Movie 2024"})

	for _, attr := range item.Attrs {
		switch attr.Name {
		case "language", "infohash", "magneturl", "imdb", "imdbid", "files", "year":
			t.Errorf("unexpected attribute %q = %q for a result without metadata", attr.Name, attr.Value)
		}
	}
	if item.Enclosure != nil {
		t.Errorf("Enclosure = %+v, want nil without a magnet link", item.Enclosure)
	}
}

func TestTorznabFeedEmitsPrefixedAttributes(t *testing.T) {
	def := torznabIndexer{ID: "bludv", Name: "BluDV"}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/torznab/bludv?t=tvsearch&q=supergirl", nil)

	writeTorznabFeed(recorder, request, def, []schema.IndexedTorrent{{
		Title: "Supergirl S01E02 1080p",
		Audio: []schema.Audio{schema.AudioPortuguese},
	}})

	body := recorder.Body.String()
	for _, want := range []string{
		`xmlns:torznab="http://torznab.com/schemas/2015/feed"`,
		`<torznab:attr name="language" value="Portuguese (Brazil)">`,
		`<torznab:response offset="0" total="1">`,
		"<category>5000</category>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("feed does not contain %q\n%s", want, body)
		}
	}

	// The feed must stay parseable XML
	var parsed struct {
		XMLName xml.Name
		Items   []struct {
			Title string `xml:"title"`
		} `xml:"channel>item"`
	}
	if err := xml.Unmarshal(recorder.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to parse generated feed: %v\n%s", err, body)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].Title != "Supergirl S01E02 1080p" {
		t.Errorf("parsed items = %+v", parsed.Items)
	}
}

func TestTorznabCategories(t *testing.T) {
	movies := torznabIndexer{ID: "bludv"}
	anime := torznabIndexer{ID: "darkmahou", Anime: true}

	tests := []struct {
		name string
		def  torznabIndexer
		it   schema.IndexedTorrent
		want []int
	}{
		{
			name: "series with resolution",
			def:  movies,
			it:   schema.IndexedTorrent{Title: "Supergirl S01E02 1080p WEB-DL"},
			want: []int{torznabCatTV, torznabCatTVHD},
		},
		{
			name: "movie in 4k",
			def:  movies,
			it:   schema.IndexedTorrent{Title: "Superman 2025 2160p BluRay"},
			want: []int{torznabCatMovies, torznabCatMoviesUHD},
		},
		{
			name: "movie without resolution",
			def:  movies,
			it:   schema.IndexedTorrent{Title: "Superman 2025 BluRay"},
			want: []int{torznabCatMovies},
		},
		{
			name: "anime episode",
			def:  anime,
			it:   schema.IndexedTorrent{Title: "[Erai-raws] Some Anime - 01 [1080p]"},
			want: []int{torznabCatTV, torznabCatTVAnime},
		},
		{
			name: "anime movie",
			def:  anime,
			it:   schema.IndexedTorrent{Title: "Some Anime Movie 1080p BD"},
			want: []int{torznabCatMovies, torznabCatMoviesHD},
		},
		{
			name: "season pack titled in portuguese",
			def:  movies,
			it:   schema.IndexedTorrent{Title: "Fundação 2a Temporada 720p", OriginalTitle: "Foundation Temporada 2"},
			want: []int{torznabCatTV, torznabCatTVHD},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := torznabCategories(tt.def, tt.it)
			if len(got) != len(tt.want) {
				t.Fatalf("torznabCategories() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("torznabCategories()[%d] = %d, want %d", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestTorznabKeywordsMirrorsCardigannFilters(t *testing.T) {
	movies := torznabIndexer{ID: "bludv"}
	anime := torznabIndexer{ID: "darkmahou", Anime: true}

	tests := []struct {
		name   string
		def    torznabIndexer
		q      string
		season string
		ep     string
		want   string
	}{
		{name: "season and episode become temporada", def: movies, q: "Supergirl", season: "1", ep: "2", want: "supergirl temporada 1"},
		{name: "padded season", def: movies, q: "Fundação", season: "02", want: "fundação temporada 2"},
		{name: "conjunction is localized", def: movies, q: "Rick and Morty", season: "9", want: "rick e morty temporada 9"},
		{name: "raw SxxEyy in the query", def: movies, q: "Supergirl S01E02", want: "supergirl temporada 1"},
		{name: "raw season only", def: movies, q: "Supergirl S03", want: "supergirl temporada 3"},
		{name: "movie query untouched", def: movies, q: "Superman", want: "superman"},
		{name: "anime drops the markers", def: anime, q: "Sakamoto Days S01E13", want: "Sakamoto Days"},
		{name: "anime drops temporada", def: anime, q: "Sakamoto Days temporada 2", want: "Sakamoto Days"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := torznabKeywords(tt.def, tt.q, tt.season, tt.ep); got != tt.want {
				t.Errorf("torznabKeywords() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTorznabIndexerName(t *testing.T) {
	tests := map[string]string{
		"/torznab/bludv":              "bludv",
		"/torznab/bludv/":             "bludv",
		"/torznab/bludv/api":          "bludv",
		"/torznab/torrent-dos-filmes": "torrent-dos-filmes",
		"/torznab/":                   "",
	}

	for path, want := range tests {
		if got := torznabIndexerName(path); got != want {
			t.Errorf("torznabIndexerName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestHandlerTorznabCaps(t *testing.T) {
	indexer := &Indexer{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/torznab/bludv?t=caps", nil)

	indexer.HandlerTorznab(recorder, request)

	if recorder.Code != 200 {
		t.Fatalf("status = %d", recorder.Code)
	}

	body := recorder.Body.String()
	for _, want := range []string{
		`<tv-search available="yes" supportedParams="q,season,ep">`,
		`<movie-search available="yes" supportedParams="q,imdbid">`,
		`<category id="5000" name="TV">`,
		`<subcat id="5070" name="TV/Anime">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("caps do not contain %q\n%s", want, body)
		}
	}
}

func TestHandlerTorznabUnknownIndexer(t *testing.T) {
	indexer := &Indexer{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/torznab/nope?t=caps", nil)

	indexer.HandlerTorznab(recorder, request)

	if recorder.Code != 404 {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "bludv") {
		t.Errorf("error does not list the available indexers: %s", recorder.Body.String())
	}
}

func TestHandlerTorznabUnknownFunction(t *testing.T) {
	indexer := &Indexer{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/torznab/bludv?t=music", nil)

	indexer.HandlerTorznab(recorder, request)

	if !strings.Contains(recorder.Body.String(), `code="202"`) {
		t.Errorf("body = %s, want a 202 torznab error", recorder.Body.String())
	}
}

func TestRunIndexerForwardsTheQueryAndDecodesTheResponse(t *testing.T) {
	var got url.Values
	def := torznabIndexer{
		ID: "bludv",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Query()
			if err := json.NewEncoder(w).Encode(Response{
				Results: []schema.IndexedTorrent{{Title: "Supergirl S01E02"}},
				Count:   1,
			}); err != nil {
				t.Errorf("failed to encode fake response: %v", err)
			}
		},
	}

	request := httptest.NewRequest("GET", "/torznab/bludv?t=tvsearch&q=supergirl&season=1&audio=brazilian", nil)
	response, err := (&Indexer{}).runIndexer(request, def, "supergirl temporada 1", 50)
	if err != nil {
		t.Fatalf("runIndexer() error = %v", err)
	}

	if len(response.Results) != 1 || response.Results[0].Title != "Supergirl S01E02" {
		t.Errorf("Results = %+v", response.Results)
	}
	if got.Get("q") != "supergirl temporada 1" {
		t.Errorf("q = %q", got.Get("q"))
	}
	if got.Get("limit") != "50" {
		t.Errorf("limit = %q", got.Get("limit"))
	}
	if got.Get("filter_results") != "true" {
		t.Errorf("filter_results = %q, want it enabled as the Cardigann definitions do", got.Get("filter_results"))
	}
	// filters supported by the JSON API stay usable from the torznab URL
	if got.Get("audio") != "brazilian" {
		t.Errorf("audio = %q", got.Get("audio"))
	}
	// the torznab-only parameters must not leak into the indexer
	if got.Get("t") != "" || got.Get("season") != "" {
		t.Errorf("torznab parameters leaked into the indexer request: %v", got)
	}
}

func TestRunIndexerFailsWhenTheIndexerErrors(t *testing.T) {
	def := torznabIndexer{
		ID: "bludv",
		Handler: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		},
	}

	request := httptest.NewRequest("GET", "/torznab/bludv?t=search&q=x", nil)
	if _, err := (&Indexer{}).runIndexer(request, def, "x", 100); err == nil {
		t.Fatal("runIndexer() error = nil, want the indexer failure to surface")
	}
}

func TestTorznabLimit(t *testing.T) {
	tests := map[string]int{
		"":     torznabDefaultLimit,
		"0":    torznabDefaultLimit,
		"abc":  torznabDefaultLimit,
		"-5":   torznabDefaultLimit,
		"50":   50,
		"9999": torznabMaxLimit,
	}

	for raw, want := range tests {
		if got := torznabLimit(raw); got != want {
			t.Errorf("torznabLimit(%q) = %d, want %d", raw, got, want)
		}
	}
}
