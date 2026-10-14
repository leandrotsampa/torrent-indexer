package handler

import (
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

// Deterministic AES-GCM payloads (default key, fixed nonce) produced offline.
// They mirror what darkmahou.io exposes as systemads1.com/go.php?id=<payload>.
const (
	darkmahouTestIDMagnet = "AAECAwQFBgcICQoLFLBEfa/kZx5Jbxo9M1/c1/gnovBoh7hqQTXzxxAGz07Z3XMZ5GPKDC6WDH1PFvVDSrqrl1k912G7z9iYRrSoLOQEdpOqjbpdrpvYqhtLkC5e7ZEuZpwBRlaScO3rwWoLuH84KgiB5CFGV+BGTyyvGFWItv+gaimNO5ys"
	darkmahouTestIDCloud  = "AAECAwQFBgcICQoLhtLji9UUCTahzRMvJSoYPfgnovBoh7hqQTDm1A4QgVvJwXVN53SKUSONAnkQWfdORfaumlE9nWPyp7HzVaq6PbJIMoSlm7VToJrZqhsTyHVB"
	darkmahouTestMagnet   = "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd&dn=Test+Anime+01"
)

// systemAdsHref builds a systemads gateway link the same way the site does,
// URL-encoding the base64 id (encodeURIComponent).
func systemAdsHref(id string) string {
	return "https://systemads1.com/go.php?id=" + url.QueryEscape(id) + "&rastrear=dark"
}

func TestExtractDarkmahouSearchLinks(t *testing.T) {
	html := `
	<div class="listupd">
		<article class="bs"><div class="bsx"><a href="https://darkmahou.io/anime-1/" class="tip">A1</a></div></article>
		<article class="bs"><div class="bsx"><a href="https://darkmahou.io/anime-2/" class="tip">A2</a></div></article>
		<article class="bs"><div class="bsx"><a href="https://darkmahou.io/anime-1/" class="tip">dup</a></div></article>
	</div>
	<div class="sidebar"><article class="bs"><a href="https://darkmahou.io/popular/" class="tip">nope</a></article></div>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}

	got := extractDarkmahouSearchLinks(doc)
	want := []string{"https://darkmahou.io/anime-1/", "https://darkmahou.io/anime-2/"}
	if len(got) != len(want) {
		t.Fatalf("got %d links %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveDarkmahouLink(t *testing.T) {
	tests := []struct {
		name string
		href string
		want string
	}{
		{"raw magnet", darkmahouTestMagnet, darkmahouTestMagnet},
		{"systemads to magnet", systemAdsHref(darkmahouTestIDMagnet), darkmahouTestMagnet},
		{"systemads to cloud dropped", systemAdsHref(darkmahouTestIDCloud), ""},
		{"direct cloud dropped", "https://jottacloud.com/s/abc", ""},
		{"raw nyaa torrent", "https://nyaa.si/download/1511470.torrent", "https://nyaa.si/download/1511470.torrent"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveDarkmahouLink(tt.href, utils.DarkmahouAdKey); got != tt.want {
				t.Errorf("resolveDarkmahouLink() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeDarkmahouQuery(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"bare name unchanged", "The All devouring Whale", "The All devouring Whale"},
		{"trailing episode", "The All devouring Whale 09", "The All devouring Whale"},
		{"colon name with episode", "The All-devouring Whale: Homecoming 09", "The All-devouring Whale: Homecoming"},
		{"temporada suffix", "the all devouring whale temporada 1", "the all devouring whale"},
		{"ordinal temporada", "some anime 2ª temporada", "some anime"},
		{"season suffix", "some anime season 3", "some anime"},
		{"s01e09 format", "some anime S01E09", "some anime"},
		{"season token", "some anime S02", "some anime"},
		{"episodio pt", "some anime episódio 12", "some anime"},
		{"episode en", "some anime episode 4", "some anime"},
		{"empty stays empty", "", ""},
		{"numeric-only title kept", "86", "86"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeDarkmahouQuery(tt.in); got != tt.want {
				t.Errorf("sanitizeDarkmahouQuery(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestChooseDarkmahouReleaseTitle(t *testing.T) {
	tests := []struct {
		name, displayName, pageTitle, label, want string
	}{
		{
			"rich display name kept",
			"[Erai-raws] Karakai Jouzu no Takagi-san - 01 ~ 12 [1080p][Multiple Subtitle]",
			"Karakai Jouzu no Takagi-san",
			"1ª Temporada Completo Legendado Torrent 1080p",
			"[Erai-raws] Karakai Jouzu no Takagi-san - 01 ~ 12 [1080p][Multiple Subtitle]",
		},
		{
			"generic display name rebuilt from label",
			"The All-devouring Whale Homecoming",
			"The All-devouring Whale: Homecoming",
			"1ª Temporada Completa Dublado Torrent [01-12] 1080p",
			"The All-devouring Whale: Homecoming S01 Dublado 1080p",
		},
		{
			"empty display name (nyaa torrent) uses label",
			"",
			"Some Anime",
			"Episódio 07 720p",
			"Some Anime - 07 [720p]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chooseDarkmahouReleaseTitle(tt.displayName, tt.pageTitle, tt.label); got != tt.want {
				t.Errorf("chooseDarkmahouReleaseTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildDarkmahouReleaseTitle(t *testing.T) {
	tests := []struct {
		name, page, label, want string
	}{
		{"episode with quality", "Gaikotsu Kishi-sama", "Episódio 05 720p", "Gaikotsu Kishi-sama - 05 [720p]"},
		{"episode only", "Some Anime", "Episódio 12", "Some Anime - 12"},
		{"season pack normalized to SNN", "Some Anime", "1ª Temporada Completo", "Some Anime S01"},
		{"season pack drops range and noise", "The All-devouring Whale: Homecoming", "1ª Temporada Completa Dublado Torrent [01-12] 1080p", "The All-devouring Whale: Homecoming S01 Dublado 1080p"},
		{"season pack legendado with paren range", "Gaikotsu", "1ª Temporada Completo Legendado Torrent (01-12) 1080p", "Gaikotsu S01 Legendado 1080p"},
		{"second season", "Some Anime", "2ª Temporada Completa 1080p", "Some Anime S02 1080p"},
		{"episode strips noise word", "Some Anime", "Episódio 03 Torrent 720p", "Some Anime - 03 [720p]"},
		{"empty label", "Some Anime", "", "Some Anime"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildDarkmahouReleaseTitle(tt.page, tt.label); got != tt.want {
				t.Errorf("buildDarkmahouReleaseTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractDarkmahouMagnetLinks(t *testing.T) {
	html := `
	<h1 class="entry-title">Test Anime</h1>
	<script>const KEY_STRING = "encodandotudoetodosagora";</script>
	<div class="soraddl dlone">
		<div class="sorattl"><h3>Episódio 01</h3></div>
		<div class="content"><table><tbody>
			<tr><td><div class="slink"><a href="magnet:?xt=urn:btih:1111111111111111111111111111111111111111&dn=Raw">1080p</a></div></td></tr>
			<tr><td><div class="slink"><a href="` + systemAdsHref(darkmahouTestIDMagnet) + `">720p</a></div></td></tr>
			<tr><td><div class="slink"><a href="` + systemAdsHref(darkmahouTestIDCloud) + `">cloud</a></div></td></tr>
		</tbody></table></div>
	</div>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}

	key := extractDarkmahouKey(doc)
	if key != "encodandotudoetodosagora" {
		t.Fatalf("extractDarkmahouKey() = %q, want default key", key)
	}

	links := extractDarkmahouMagnetLinks(doc, key)
	// expect: raw magnet + systemads-decoded magnet; cloud link dropped
	if len(links) != 2 {
		t.Fatalf("got %d magnet links, want 2: %+v", len(links), links)
	}
	for _, l := range links {
		if !strings.HasPrefix(l.URL, "magnet:") {
			t.Errorf("resolved link is not a magnet: %q", l.URL)
		}
		if l.Label == "" {
			t.Errorf("magnet link missing label: %+v", l)
		}
	}
	if links[1].URL != darkmahouTestMagnet {
		t.Errorf("second link = %q, want decoded %q", links[1].URL, darkmahouTestMagnet)
	}
}

// darkmahouNewLayoutHome mirrors the current home/listing layout: a slideshow
// followed by several dm-latest sections, the first being "Últimos lançamentos".
const darkmahouNewLayoutHome = `
<section class="dm-slideshow"><article class="dm-slide"><a href="https://darkmahou.io/slide/">slide</a></article></section>
<section class="dm-latest dm-home-section"><header class="dm-section-heading"><h2>Últimos lançamentos</h2> <a class="dm-section-note" href="https://darkmahou.io/?dm_catalog=ongoing">Ver mais</a></header>
<div class="dm-grid">
	<article class="dm-card"> <a href="https://darkmahou.io/shiotaiou-no-satou-san/"><div class="dm-poster"><img src="x.jpg" alt="Shiotaiou"> <span class="dm-episode">EP 2</span></div><h3>Shiotaiou</h3><p>Em lançamento</p> </a></article>
	<article class="dm-card"> <a href="https://darkmahou.io/seitokai-ni-mo-ana-wa-aru/"><div class="dm-poster"></div><h3>Seitokai</h3></a></article>
	<article class="dm-card"> <a href="https://darkmahou.io/shiotaiou-no-satou-san/"><h3>dup</h3></a></article>
</div></section>
<section class="dm-latest dm-home-section"><header class="dm-section-heading"><h2>Recomendações</h2></header>
<div class="dm-grid"><article class="dm-card"> <a href="https://darkmahou.io/recomendado/"><h3>Rec</h3></a></article></div></section>`

func TestExtractDarkmahouSearchLinksNewLayout(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(darkmahouNewLayoutHome))
	if err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}

	got := extractDarkmahouSearchLinks(doc)
	want := []string{"https://darkmahou.io/shiotaiou-no-satou-san/", "https://darkmahou.io/seitokai-ni-mo-ana-wa-aru/"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v (only the latest-releases section)", got, want)
	}
}

// darkmahouNewLayoutPost mirrors the current anime page: facts list with the
// premiere date and the #dm-downloads area made of details groups, each with
// .dm-resolution rows labelled by .dm-quality and direct magnet anchors.
const darkmahouNewLayoutPost = `
<h1 class="entry-title" itemprop="name">Sousou no Frieren 2ª Temporada</h1>
<section class="dm-facts"><dl class="dm-facts-grid">
	<div class="dm-fact dm-fact-status"><dt>Status</dt><dd>Concluído</dd></div>
	<div class="dm-fact "><dt>Episódios</dt><dd>10</dd></div>
	<div class="dm-fact "><dt>Estreia</dt><dd>Jan 16, 2026</dd></div>
</dl></section>
<section id="dm-downloads" aria-label="Downloads"><div class="dm-download-box"><div class="dm-download-list">
<details class="dm-download-group">
	<summary><span class="dm-download-icon" aria-hidden="true">↓</span><span class="dm-download-title">Episódio 01</span><span class="dm-detail-toggle" aria-hidden="true">⌄</span></summary>
	<div class="dm-download-resolutions">
		<div class="dm-resolution"><span class="dm-quality">Baixar&gt;&gt;</span><div class="dm-hosts">
			<a href="magnet:?xt=urn:btih:2b429d6eb6538ffc5a5b873fc33ad06646ef46dd&#038;dn=%5BErai-raws%5D%20Sousou%20no%20Frieren%202nd%20Season%20-%2001%20%5B1080p%20CR%20WEB-DL%20AVC%20AAC%5D" target="_blank"><span>1080p-2</span><span aria-hidden="true">↗</span><span class="screen-reader-text"> (abre em nova aba)</span></a>
			<a href="magnet:?xt=urn:btih:b7fab54ee37b0793e56d845bdee8320f500c58c6&#038;dn=Sousou%20no%20Frieren" target="_blank"><span>1080p HEVC-2 CR</span><span aria-hidden="true">↗</span><span class="screen-reader-text"> (abre em nova aba)</span></a>
		</div></div>
		<div class="dm-resolution"><span class="dm-quality">Dublado.[Multi-Audio]&gt;&gt;</span><div class="dm-hosts">
			<a href="magnet:?xt=urn:btih:84f19ebc83be1c18fd3195a96d7ce84f21c6563a&#038;dn=Frieren" target="_blank"><span>1080p </span><span aria-hidden="true">↗</span><span class="screen-reader-text"> (abre em nova aba)</span></a>
		</div></div>
	</div>
</details>
<details class="dm-download-group">
	<summary><span class="dm-download-icon" aria-hidden="true">↓</span><span class="dm-download-title">2ª Temporada BD Completa Dublado/Legendada Torrent [01-10]</span></summary>
	<div class="dm-download-resolutions">
		<div class="dm-resolution"><span class="dm-quality">Dublado&gt;&gt;</span><div class="dm-hosts">
			<a href="magnet:?xt=urn:btih:1111111111111111111111111111111111111111&#038;dn=Frieren%20S2" target="_blank"><span>1080p</span><span aria-hidden="true">↗</span><span class="screen-reader-text"> (abre em nova aba)</span></a>
		</div></div>
	</div>
</details>
</div></div></section>`

func TestExtractDarkmahouMagnetLinksNewLayout(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(darkmahouNewLayoutPost))
	if err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}

	links := extractDarkmahouMagnetLinks(doc, extractDarkmahouKey(doc))
	want := []darkmahouMagnetLink{
		{
			URL:   "magnet:?xt=urn:btih:2b429d6eb6538ffc5a5b873fc33ad06646ef46dd&dn=%5BErai-raws%5D%20Sousou%20no%20Frieren%202nd%20Season%20-%2001%20%5B1080p%20CR%20WEB-DL%20AVC%20AAC%5D",
			Label: "Episódio 01 Baixar 1080p",
		},
		{
			URL:   "magnet:?xt=urn:btih:b7fab54ee37b0793e56d845bdee8320f500c58c6&dn=Sousou%20no%20Frieren",
			Label: "Episódio 01 Baixar 1080p HEVC CR",
		},
		{
			URL:   "magnet:?xt=urn:btih:84f19ebc83be1c18fd3195a96d7ce84f21c6563a&dn=Frieren",
			Label: "Episódio 01 Dublado [Multi-Audio] 1080p",
		},
		{
			URL:   "magnet:?xt=urn:btih:1111111111111111111111111111111111111111&dn=Frieren%20S2",
			Label: "2ª Temporada BD Completa Dublado/Legendada Torrent [01-10] Dublado 1080p",
		},
	}
	if len(links) != len(want) {
		t.Fatalf("got %d links, want %d: %+v", len(links), len(want), links)
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("link[%d] = %+v, want %+v", i, links[i], want[i])
		}
	}
}

func TestDarkmahouNewLayoutReleaseTitles(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(darkmahouNewLayoutPost))
	if err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}

	title := strings.TrimSpace(doc.Find("h1.entry-title").First().Text())
	links := extractDarkmahouMagnetLinks(doc, extractDarkmahouKey(doc))
	if len(links) != 4 {
		t.Fatalf("got %d links, want 4: %+v", len(links), links)
	}

	tests := []struct {
		displayName string
		link        darkmahouMagnetLink
		want        string
	}{
		{"[Erai-raws] Sousou no Frieren 2nd Season - 01 [1080p CR WEB-DL AVC AAC]", links[0], "[Erai-raws] Sousou no Frieren 2nd Season - 01 [1080p CR WEB-DL AVC AAC]"},
		{"Sousou no Frieren", links[1], "Sousou no Frieren 2ª Temporada - 01 [1080p HEVC CR]"},
		{"Frieren", links[2], "Sousou no Frieren 2ª Temporada - 01 [Dublado [Multi-Audio] 1080p]"},
		{"Frieren S2", links[3], "Sousou no Frieren 2ª Temporada S02 BD Dublado/Legendada Dublado 1080p"},
	}
	for _, tt := range tests {
		if got := chooseDarkmahouReleaseTitle(tt.displayName, title, tt.link.Label); got != tt.want {
			t.Errorf("title for %q = %q, want %q", tt.link.Label, got, tt.want)
		}
	}
}

func TestBuildDarkmahouReleaseTitleNewLabels(t *testing.T) {
	tests := []struct {
		name, page, label, want string
	}{
		{"BD complete season keeps BD and drops range", "Sousou no Frieren", "1ª Temporada BD Completo Dublado [Dual-Audio][Multi-Subs] Torrent 1080p", "Sousou no Frieren S01 BD Dublado [Dual-Audio][Multi-Subs] 1080p"},
		{"episode range", "One Piece", "Episódio 1000~1100 OPEX Baixar 1080p", "One Piece - 1000 ~ 1100 [OPEX 1080p]"},
		{"episode range repeating the page title", "One Piece", "One Piece Episódio 0001~1163 Baixar 720p", "One Piece - 0001 ~ 1163 [720p]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildDarkmahouReleaseTitle(tt.page, tt.label); got != tt.want {
				t.Errorf("buildDarkmahouReleaseTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractDarkmahouYear(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(darkmahouNewLayoutPost))
	if err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	if got := extractDarkmahouYear(doc, "", "Sousou no Frieren 2ª Temporada"); got != "2026" {
		t.Errorf("extractDarkmahouYear() = %q, want %q", got, "2026")
	}

	empty, err := goquery.NewDocumentFromReader(strings.NewReader(`<h1>x</h1>`))
	if err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	if got := extractDarkmahouYear(empty, "Lançamento: 2019", "x"); got != "2019" {
		t.Errorf("extractDarkmahouYear() fallback = %q, want %q", got, "2019")
	}
}
