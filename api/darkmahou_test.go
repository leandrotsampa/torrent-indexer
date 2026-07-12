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
