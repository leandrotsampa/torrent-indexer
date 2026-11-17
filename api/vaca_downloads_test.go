package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/leandrotsampa/torrent-indexer/schema"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

// trimmed from https://vaqueirofilmes.com/pt/tv/aoashi/season/temporada-2/
const vacaSeasonPageHTML = `<div class="ss-wrap">
<div class="ss-hero-info"><h1 class="ss-hero-title">Temporada 2</h1></div>
<div class="ss-episodes">
  <div class="ss-ep">
    <div class="ss-ep-num">01</div>
    <div class="ss-ep-body">
      <p class="ss-ep-title"><span class="ss-ep-num-inline">01</span> Ponto de Partida</p>
      <div class="ss-ep-actions">
        <a href="https://vaqueirofilmes.com/episodes/ponto-de-partida/?video=0" class="ss-ep-btn ss-ep-btn-video">Veja Online</a>
        <div class="dl-btn-wrap">
          <a href="https://vaqueirofilmes.com/enc/go.php?id=ep1&amp;pub=PUB_x" class="ss-ep-btn ss-ep-btn-dl">
            Japonês &bull; 1080p &bull; 418.6 MiB </a>
          <button type="button" class="subs-pill"
            data-subs="[&quot;English&quot;,&quot;(CR_English)&quot;,&quot;ASS&quot;,&quot;Portuguese&quot;,&quot;(CR_Portuguese(Brazil))&quot;,&quot;ASS&quot;]">Subs</button>
        </div>
      </div>
    </div>
  </div>
  <div class="ss-ep">
    <div class="ss-ep-num">02</div>
    <div class="ss-ep-body">
      <div class="ss-ep-actions">
        <div class="dl-btn-wrap">
          <a href="https://vaqueirofilmes.com/enc/go.php?id=ep2&amp;pub=PUB_x" class="ss-ep-btn ss-ep-btn-dl">
            Japonês &bull; 1080p &bull; 1,2 GiB </a>
        </div>
      </div>
    </div>
  </div>
</div>
</div>`

// trimmed from https://vaqueirofilmes.com/pt/movie-links/60774/
const vacaMovieLinksHTML = `<div class="ml-links-box">
  <p class="ml-group-label">Download</p>
  <div class="ml-btns">
    <div class="dl-btn-wrap">
      <a href="https://vaqueirofilmes.com/enc/go.php?id=movie&amp;pub=PUB_x" class="ml-btn ml-btn-dl">
        Português | Inglês • 1080p HDCAM • 4.90 GB </a>
      <button type="button" class="subs-pill" data-subs="[&quot;PT-BR&quot;]">Subs</button>
    </div>
  </div>
</div>`

func mustVacaDoc(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestParseVacaDownloadsSeasonPage(t *testing.T) {
	downloads := parseVacaDownloads(mustVacaDoc(t, vacaSeasonPageHTML), "https://vaqueirofilmes.com/pt/tv/aoashi/season/temporada-2/")
	if len(downloads) != 2 {
		t.Fatalf("downloads = %+v, want 2", downloads)
	}

	ep1 := downloads[0]
	if ep1.link != "https://vaqueirofilmes.com/enc/go.php?id=ep1&pub=PUB_x" || ep1.season != 2 || ep1.episode != 1 {
		t.Errorf("ep1 = %+v", ep1)
	}
	if ep1.quality != "1080p" || ep1.size != "418.6 MB" {
		t.Errorf("ep1 quality/size = %q/%q", ep1.quality, ep1.size)
	}
	if len(ep1.audio) != 1 || ep1.audio[0] != schema.AudioJapanese {
		t.Errorf("ep1 audio = %v, want Japanese", ep1.audio)
	}
	if !hasPortugueseAudio(ep1.subtitles) {
		t.Errorf("ep1 subtitles = %v, want Portuguese among them", ep1.subtitles)
	}
	if got := vacaReleaseTitle("Aoashi", "2022", ep1); got != "Aoashi S02E01 1080p" {
		t.Errorf("title = %q", got)
	}

	ep2 := downloads[1]
	if ep2.episode != 2 || ep2.size != "1.2 GB" || len(ep2.subtitles) != 0 {
		t.Errorf("ep2 = %+v", ep2)
	}
	if utils.ParseSize(ep2.size) == 0 {
		t.Errorf("size %q does not parse", ep2.size)
	}
}

func TestParseVacaDownloadsMovieLinksPage(t *testing.T) {
	downloads := parseVacaDownloads(mustVacaDoc(t, vacaMovieLinksHTML), "https://vaqueirofilmes.com/pt/movie-links/60774/")
	if len(downloads) != 1 {
		t.Fatalf("downloads = %+v, want 1", downloads)
	}
	d := downloads[0]
	if d.season != 0 || d.episode != 0 || d.quality != "1080p HDCAM" || d.size != "4.90 GB" {
		t.Errorf("movie = %+v", d)
	}
	if len(d.audio) != 2 || d.audio[0] != schema.AudioPortuguese || d.audio[1] != schema.AudioEnglish {
		t.Errorf("audio = %v, want Portuguese and English", d.audio)
	}
	if got := vacaReleaseTitle("A Odisseia", "2026", d); got != "A Odisseia 2026 1080p HDCAM" {
		t.Errorf("title = %q", got)
	}
}

// trimmed from a legacy post (root-level slug): the buttons are images whose
// alt text is the full post title
const vacaLegacyPostHTML = `<h3>Download</h3>
<p><a href="https://vaqueirofilmes.com/enc/go.php?id=legacy1&amp;pub=PUB_x" target="_blank"><img alt="2001 Uma Odisseia no Espaço - BluRay 720p - 1080p 5.1 Dual Áudio Torrent Download (1961)" src="https://torrentdosfilmes-v2.xyz/botao.png"/></a></p>`

func TestParseVacaDownloadsLegacyPost(t *testing.T) {
	downloads := parseVacaDownloads(mustVacaDoc(t, vacaLegacyPostHTML), "https://vaqueirofilmes.com/2001-uma-odisseia-no-espa-o-bluray-720p-1080p-5-1-dual-udio-torrent-download-1961/")
	if len(downloads) != 1 {
		t.Fatalf("downloads = %+v, want 1", downloads)
	}
	if got := vacaReleaseTitle("2001 Uma Odisseia no Espaço", "1961", downloads[0]); got != "2001 Uma Odisseia no Espaço - BluRay 720p - 1080p 5.1 Dual Áudio 1961" {
		t.Errorf("title = %q", got)
	}
}

func TestVacaReleaseTitleDoesNotRepeatTheYear(t *testing.T) {
	if got := vacaReleaseTitle("A Odisséia 2018", "2018", vacaDownload{}); got != "A Odisséia 2018" {
		t.Errorf("title = %q", got)
	}
	// an alt text that is not a name is ignored
	if got := vacaReleaseTitle("A Odisséia 2018", "2018", vacaDownload{name: "MAGNET LINK"}); got != "A Odisséia 2018" {
		t.Errorf("title = %q", got)
	}
}

func TestSplitVacaQuery(t *testing.T) {
	tests := []struct {
		q      string
		query  string
		season int
	}{
		{"aoashi temporada 2", "aoashi", 2},
		{"aoashi temporada 02", "aoashi", 2},
		{"one piece temporada 21 extra", "one piece extra", 21},
		{"a odisseia", "a odisseia", 0},
		{"", "", 0},
	}
	for _, tt := range tests {
		query, season := splitVacaQuery(tt.q)
		if query != tt.query || season != tt.season {
			t.Errorf("splitVacaQuery(%q) = %q, %d; want %q, %d", tt.q, query, season, tt.query, tt.season)
		}
	}
}

func TestIsVacaOwnResolverLink(t *testing.T) {
	for link, want := range map[string]bool{
		"https://vaqueirofilmes.com/enc/go.php?id=x&pub=y": true,
		"https://systemtech.space/enc/go.php?id=x":         true,
		"https://evil.example/enc/go.php?id=x":             false,
		"http://169.254.169.254/enc/go.php?id=x":           false,
		"https://vaqueirofilmes.com/pt/movie/x/":           false,
		"":                                                 false,
	} {
		if got := isVacaOwnResolverLink(link); got != want {
			t.Errorf("isVacaOwnResolverLink(%q) = %v, want %v", link, got, want)
		}
	}
}

func TestHandlerVacaTorrentMagnetRejectsForeignLinks(t *testing.T) {
	i := &Indexer{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, VacaTorrentMagnetPath+"?link=https%3A%2F%2Fevil.example%2Fenc%2Fgo.php%3Fid%3Dx", nil)
	i.HandlerVacaTorrentMagnet(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// A release whose magnet is resolved on demand goes out with the download
// link and no seeders (Sonarr would reject a reported zero).
func TestTorznabItemWithOnDemandDownloadLink(t *testing.T) {
	link := vacaMagnetDownloadLink("http://torrent-indexer:7006", "https://vaqueirofilmes.com/enc/go.php?id=ep1&pub=x")
	if link != "http://torrent-indexer:7006/indexers/vaca_torrent/magnet?link=https%3A%2F%2Fvaqueirofilmes.com%2Fenc%2Fgo.php%3Fid%3Dep1%26pub%3Dx" {
		t.Fatalf("download link = %q", link)
	}

	item := newTorznabItem(torznabIndexer{ID: "vaca_torrent"}, schema.IndexedTorrent{
		Title:        "Aoashi S02E01 1080p",
		DownloadLink: link,
		PeersUnknown: true,
		Size:         "418.6 MB",
	})
	if item.Link != link || item.GUID != link || item.Enclosure == nil || item.Enclosure.URL != link {
		t.Errorf("item link/guid/enclosure = %q/%q/%+v", item.Link, item.GUID, item.Enclosure)
	}
	for _, attr := range item.Attrs {
		if attr.Name == "seeders" || attr.Name == "leechers" || attr.Name == "peers" {
			t.Errorf("attr %s = %s, want it left out", attr.Name, attr.Value)
		}
	}
}
