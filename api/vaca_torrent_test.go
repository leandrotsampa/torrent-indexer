package handler

import (
	"sort"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/leandrotsampa/torrent-indexer/magnet"
)

func Test_decodeVacaDataLink(t *testing.T) {
	want := "magnet:?xt=urn:btih:CF76LOCCN4KECVLBWM4D5TKIIRJKYZMM&tr=udp://tracker.example/announce"
	got, err := decodeVacaDataLink("bWFnbmV0Oj94dD11cm46YnRpaDpDRjc2TE9DQ040S0VDVkxCV000RDVUS0lJUkpLWVpNTSZ0cj11ZHA6Ly90cmFja2VyLmV4YW1wbGUvYW5ub3VuY2U=")
	if err != nil {
		t.Fatalf("decodeVacaDataLink() failed: %v", err)
	}
	if got != want {
		t.Fatalf("decodeVacaDataLink() = %q, want %q", got, want)
	}
}

func Test_getVacaJSString(t *testing.T) {
	body := []byte(`const next = "https:\/\/t.co\/abc123"; var URL_ETAPA2 = "https:\/\/vacaflix.xyz\/enc\/receber.php?enc=abc&pub=PUB";`)

	if got := getVacaJSString(body, vacaSystemTechNextRE); got != "https://t.co/abc123" {
		t.Fatalf("next URL = %q", got)
	}
	if got := getVacaJSString(body, vacaSecondStageURLRE); got != "https://vacaflix.xyz/enc/receber.php?enc=abc&pub=PUB" {
		t.Fatalf("second stage URL = %q", got)
	}
}

func Test_getVacaDataLinkMagnetsFromHTML(t *testing.T) {
	body := []byte(`<html><body data-link="bWFnbmV0Oj94dD11cm46YnRpaDpDRjc2TE9DQ040S0VDVkxCV000RDVUS0lJUkpLWVpNTQ=="></body></html>`)

	got := getVacaDataLinkMagnetsFromHTML(body)
	if len(got) != 1 {
		t.Fatalf("got %d magnets, want 1", len(got))
	}
	if got[0] != "magnet:?xt=urn:btih:CF76LOCCN4KECVLBWM4D5TKIIRJKYZMM" {
		t.Fatalf("magnet = %q", got[0])
	}
}

func Test_buildVacaTorrentSearchRequest(t *testing.T) {
	meta := IndexerMeta{URL: "https://vaqueirofilmes1.com/pt/", PagePattern: "page/%s/"}

	ajaxURL, form, err := buildVacaTorrentSearchRequest(meta, "duna", "2")
	if err != nil {
		t.Fatalf("buildVacaTorrentSearchRequest() failed: %v", err)
	}
	if ajaxURL != "https://vaqueirofilmes1.com/wp-admin/admin-ajax.php" {
		t.Fatalf("ajaxURL = %q", ajaxURL)
	}
	want := map[string]string{"action": "filtrar_busca", "s": "duna", "tipo": "todos", "paged": "2", "lang": "pt-BR"}
	for k, v := range want {
		if got := form.Get(k); got != v {
			t.Fatalf("form[%q] = %q, want %q", k, got, v)
		}
	}

	// host override via env (other domain, no /pt/ path) is respected
	ajaxURL, form, err = buildVacaTorrentSearchRequest(IndexerMeta{URL: "http://mirror.example:8080/"}, "x", "")
	if err != nil {
		t.Fatalf("buildVacaTorrentSearchRequest() failed: %v", err)
	}
	if ajaxURL != "http://mirror.example:8080/wp-admin/admin-ajax.php" {
		t.Fatalf("ajaxURL = %q", ajaxURL)
	}
	if form.Get("paged") != "1" {
		t.Fatalf("paged = %q, want 1", form.Get("paged"))
	}

	if _, _, err := buildVacaTorrentSearchRequest(IndexerMeta{URL: "not a url"}, "x", "1"); err == nil {
		t.Fatalf("expected error for URL without host")
	}
}

func Test_buildVacaTorrentURL_listing(t *testing.T) {
	meta := IndexerMeta{URL: "https://vaqueirofilmes1.com/pt/", PagePattern: "page/%s/"}
	if got := buildVacaTorrentURL(meta, "1"); got != "https://vaqueirofilmes1.com/pt/" {
		t.Fatalf("page 1 = %q", got)
	}
	if got := buildVacaTorrentURL(meta, "3"); got != "https://vaqueirofilmes1.com/pt/page/3/" {
		t.Fatalf("page 3 = %q", got)
	}
}

const vacaSearchJSON = `{"html":"\n<div class=\"movies-list\">\n<div class=\"lo-col-m\"><div class=\"i-tem_ht\"><div class=\"image\"><a href=\"https:\/\/vaqueirofilmes1.com\/pt\/movie\/duna-parte-dois\/\" class=\"tooltip-container\"><span class=\"audio\">DUAL ÁUDIO<\/span><\/a><\/div><div class=\"custom-main-title\"><a href=\"https:\/\/vaqueirofilmes1.com\/pt\/movie\/duna-parte-dois\/\"><h3>Duna: Parte Dois<\/h3><\/a><\/div><\/div><\/div>\n<div class=\"lo-col-m\"><div class=\"i-tem_ht\"><div class=\"image\"><a href=\"https:\/\/vaqueirofilmes1.com\/pt\/tv-shows\/frieren\/\"><\/a><\/div><\/div><\/div>\n<\/div>","total":2,"pages":1,"count_todos":2,"count_filme":1,"count_serie":1}`

func Test_parseVacaTorrentSearchResponse(t *testing.T) {
	links, err := parseVacaTorrentSearchResponse([]byte(vacaSearchJSON), "1")
	if err != nil {
		t.Fatalf("parseVacaTorrentSearchResponse() failed: %v", err)
	}
	want := []string{
		"https://vaqueirofilmes1.com/pt/movie/duna-parte-dois/",
		"https://vaqueirofilmes1.com/pt/tv-shows/frieren/",
	}
	if strings.Join(links, ",") != strings.Join(want, ",") {
		t.Fatalf("links = %v, want %v", links, want)
	}

	// page beyond the last page reported by the endpoint returns nothing
	links, err = parseVacaTorrentSearchResponse([]byte(vacaSearchJSON), "2")
	if err != nil {
		t.Fatalf("parseVacaTorrentSearchResponse() failed: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("links for page 2 = %v, want none", links)
	}

	// empty search
	links, err = parseVacaTorrentSearchResponse([]byte(`{"html":"\n<p>Nenhum resultado<\/p>","total":0,"pages":0}`), "1")
	if err != nil || len(links) != 0 {
		t.Fatalf("empty search: links=%v err=%v", links, err)
	}

	if _, err := parseVacaTorrentSearchResponse([]byte(`<html>not json</html>`), "1"); err == nil {
		t.Fatalf("expected error for non-JSON body")
	}
}

func Test_extractVacaTorrentLinks_fallbackOnlyPosts(t *testing.T) {
	body := `<html><head>
<link rel="alternate" href="https://vaqueirofilmes1.com/pt/movie/feed-like-link/">
</head><body>
<a href="https://vaqueirofilmes1.com/pt/movie/">Filmes</a>
<a href="https://vaqueirofilmes1.com/pt/tv-shows/">Séries</a>
<a href="https://vaqueirofilmes1.com/pt/movie/page/2/">2</a>
<a href="https://vaqueirofilmes1.com/pt/movie/duna-parte-dois/">Duna</a>
<a href="https://vaqueirofilmes1.com/pt/tv-shows/frieren/">Frieren</a>
<a href="https://vaqueirofilmes1.com/pt/movie/duna-parte-dois/">Duna (dup)</a>
</body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got := extractVacaTorrentLinks(doc)
	sort.Strings(got) // StableUniq orders by last occurrence
	want := []string{
		"https://vaqueirofilmes1.com/pt/movie/duna-parte-dois/",
		"https://vaqueirofilmes1.com/pt/tv-shows/frieren/",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("links = %v, want %v", got, want)
	}
}

func Test_extractVacaTorrentLinks_itemCards(t *testing.T) {
	body := `<div class="i-tem_ht"><a href="https://vaqueirofilmes1.com/pt/movie/a/">A</a></div>
<div class="i-tem_ht"><a href="https://vaqueirofilmes1.com/pt/tv-shows/b/">B</a></div>
<a href="https://vaqueirofilmes1.com/pt/movie/c/">sidebar</a>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got := extractVacaTorrentLinks(doc)
	if strings.Join(got, ",") != "https://vaqueirofilmes1.com/pt/movie/a/,https://vaqueirofilmes1.com/pt/tv-shows/b/" {
		t.Fatalf("links = %v", got)
	}
}

func Test_isVacaTorrentPostURL(t *testing.T) {
	cases := map[string]bool{
		"https://vaqueirofilmes1.com/pt/movie/duna-parte-dois/": true,
		"https://vaqueirofilmes1.com/pt/tv-shows/frieren":       true,
		"/pt/movie/duna/":                                 true,
		"https://vaqueirofilmes1.com/pt/movie/":           false,
		"https://vaqueirofilmes1.com/pt/tv-shows/":        false,
		"https://vaqueirofilmes1.com/pt/movie/page/2/":    false,
		"https://vaqueirofilmes1.com/pt/movie/duna/feed/": false,
		"https://vaqueirofilmes1.com/pt/genero/acao/":     false,
	}
	for link, want := range cases {
		if got := isVacaTorrentPostURL(link); got != want {
			t.Errorf("isVacaTorrentPostURL(%q) = %v, want %v", link, got, want)
		}
	}
}

func Test_cleanVacaMagnetTrackers(t *testing.T) {
	in := "magnet:?xt=urn:btih:CF76LOCCN4KECVLBWM4D5TKIIRJKYZMM&dn=Duna.2024&tr=udptracker.openbittorrent.com80announce&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce&tr=udp://open.stealth.si:80/announce"
	want := "magnet:?xt=urn:btih:CF76LOCCN4KECVLBWM4D5TKIIRJKYZMM&dn=Duna.2024&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce&tr=udp://open.stealth.si:80/announce"
	if got := cleanVacaMagnetTrackers(in); got != want {
		t.Fatalf("cleanVacaMagnetTrackers() = %q, want %q", got, want)
	}

	m, err := magnet.ParseMagnetUri(cleanVacaMagnetTrackers(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Trackers) != 2 || m.DisplayName != "Duna.2024" {
		t.Fatalf("parsed magnet = %+v", m)
	}

	// magnet without malformed trackers is unchanged
	ok := "magnet:?xt=urn:btih:CF76LOCCN4KECVLBWM4D5TKIIRJKYZMM&tr=udp://tracker.example/announce"
	if got := cleanVacaMagnetTrackers(ok); got != ok {
		t.Fatalf("cleanVacaMagnetTrackers() changed a valid magnet: %q", got)
	}
}
