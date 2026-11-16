package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// nyaaSiListing mirrors nyaa.si's search table: relative links, a comments link
// before the title and a raw (1_4) release that must be dropped.
const nyaaSiListing = `<table class="table table-bordered table-hover table-striped torrent-list"><tbody>
<tr class="default">
	<td><a href="/?c=1_2" title="Anime - English-translated"><img src="/static/img/icons/nyaa/1_2.png" alt="Anime - English-translated" class="category-icon"></a></td>
	<td colspan="2">
		<a href="/view/2163683#comments" class="comments" title="3 comments"><i class="fa fa-comments-o"></i>3</a>
		<a href="/view/2163683" title="[DKB] Black Torch - S01E12 [1080p][Dual Audio][Multi-Subs]">[DKB] Black Torch - S01E12 [1080p][Dual Audio][Multi-Subs]</a>
	</td>
	<td class="text-center">
		<a href="/download/2163683.torrent"><i class="fa fa-fw fa-download"></i></a>
		<a href="magnet:?xt=urn:btih:43ba97a2c8f0b1d2e3f405162738495a6b7c8d9e&amp;dn=Black%20Torch&amp;tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce"><i class="fa fa-fw fa-magnet"></i></a>
	</td>
	<td class="text-center">1.3 GiB</td>
	<td class="text-center" data-timestamp="1794620407">2026-11-14 01:40</td>
	<td class="text-center">240</td>
	<td class="text-center">25</td>
	<td class="text-center">1279</td>
</tr>
<tr class="default">
	<td><a href="/?c=1_4" title="Anime - Raw"><img src="/static/img/icons/nyaa/1_4.png" alt="Anime - Raw" class="category-icon"></a></td>
	<td colspan="2"><a href="/view/2185858" title="[NanakoRaws] One Piece - 996 (BS8 TV 1080p HEVC AAC)">[NanakoRaws] One Piece - 996</a></td>
	<td class="text-center"><a href="magnet:?xt=urn:btih:208185277cc055fedd2d7568ac214cc0fbdfa200"><i class="fa fa-fw fa-magnet"></i></a></td>
	<td class="text-center">468.2 MiB</td>
	<td class="text-center" data-timestamp="1794620407">2026-11-14 01:40</td>
	<td class="text-center">240</td>
	<td class="text-center">25</td>
	<td class="text-center">1279</td>
</tr>
</tbody></table>`

// nyaaWikiListing mirrors nyaa.wiki: absolute links and the size repeated in a
// span next to the title.
const nyaaWikiListing = `<table class="table torrent-list"><tbody>
<tr class="success">
	<td><a href="https://nyaa.wiki?f=0&amp;q=psyren&amp;c=1_2" title="Category: Anime - English-translated"><img src="https://nyaa.wiki/img/icons/1_2.png" alt="Anime - English-translated" class="category-icon"></a></td>
	<td colspan="2"><a href="https://nyaa.wiki/view/2186974" title="[ToonsHub] PSYREN S01E04 1080p CR WEB-DL MULTi">[ToonsHub] PSYREN S01E04 1080p CR WEB-DL MULTi</a> <span class="torrent-meta">1.4 GiB</span></td>
	<td class="text-center">
		<a href="https://nyaa.si/download/2186974.torrent" rel="nofollow noopener noreferrer" title="Download Torrent"><i class="fa fa-fw fa-download"></i></a>
		<a href="magnet:?xt=urn:btih:0f1e2d3c4b5a69788796a5b4c3d2e1f0a1b2c3d4&amp;dn=PSYREN" rel="nofollow noopener noreferrer" title="Magnet Link"><i class="fa fa-fw fa-magnet"></i></a>
	</td>
	<td class="text-center">1.4 GiB</td>
	<td class="text-center" data-timestamp="1794700000">2026-11-15 08:40</td>
	<td class="text-center">546</td>
	<td class="text-center">53</td>
	<td class="text-center">1059</td>
</tr>
</tbody></table>`

func parseNyaaFixture(t *testing.T, page, base string) []nyaaRow {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
	if err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	return parseNyaaListing(doc, base)
}

func TestParseNyaaListingNyaaSi(t *testing.T) {
	rows := parseNyaaFixture(t, nyaaSiListing, "https://nyaa.si/")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (the raw release is dropped): %+v", len(rows), rows)
	}
	row := rows[0]
	if row.ID != "2163683" || row.ViewURL != "https://nyaa.si/view/2163683" {
		t.Errorf("id/view = %q/%q", row.ID, row.ViewURL)
	}
	if row.Title != "[DKB] Black Torch - S01E12 [1080p][Dual Audio][Multi-Subs]" {
		t.Errorf("title = %q", row.Title)
	}
	if row.Category != nyaaCategoryEnglish {
		t.Errorf("category = %q", row.Category)
	}
	if !strings.HasPrefix(row.Magnet, "magnet:?xt=urn:btih:43ba97a2") || strings.Contains(row.Magnet, "&amp;") {
		t.Errorf("magnet = %q", row.Magnet)
	}
	if row.Size != "1.3 GB" {
		t.Errorf("size = %q, want 1.3 GB (MiB/GiB units normalized)", row.Size)
	}
	if !row.Date.Equal(time.Unix(1794620407, 0)) {
		t.Errorf("date = %v", row.Date)
	}
	if row.Seeders != 240 || row.Leechers != 25 {
		t.Errorf("seeders/leechers = %d/%d", row.Seeders, row.Leechers)
	}
}

func TestParseNyaaListingNyaaWiki(t *testing.T) {
	rows := parseNyaaFixture(t, nyaaWikiListing, "https://nyaa.wiki/")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.ID != "2186974" || row.ViewURL != "https://nyaa.wiki/view/2186974" {
		t.Errorf("id/view = %q/%q", row.ID, row.ViewURL)
	}
	if row.Category != nyaaCategoryEnglish || row.Size != "1.4 GB" || row.Seeders != 546 || row.Leechers != 53 {
		t.Errorf("row = %+v", row)
	}
}

func TestBuildNyaaSearchURL(t *testing.T) {
	tests := []struct {
		q, page, want string
	}{
		{"", "", "https://nyaa.si/?c=1_0&f=0&q="},
		{"black torch", "", "https://nyaa.si/?c=1_0&f=0&q=black+torch"},
		// nyaa.si pages with "p", nyaa.wiki with "page": send both
		{"black torch", "2", "https://nyaa.si/?c=1_0&f=0&p=2&page=2&q=black+torch"},
	}
	for _, tt := range tests {
		if got := buildNyaaSearchURL("https://nyaa.si/", tt.q, tt.page); got != tt.want {
			t.Errorf("buildNyaaSearchURL(%q, %q) = %q, want %q", tt.q, tt.page, got, tt.want)
		}
	}
}

func TestNyaaMirrors(t *testing.T) {
	t.Setenv("INDEXER_NYAA_MIRRORS", " https://nyaa.wiki , https://nyaa.si/ ,,https://nyaa.iss.ink")
	got := strings.Join(nyaaMirrors("https://nyaa.si/"), ",")
	if got != "https://nyaa.si/,https://nyaa.wiki/,https://nyaa.iss.ink/" {
		t.Errorf("nyaaMirrors() = %s", got)
	}
}
