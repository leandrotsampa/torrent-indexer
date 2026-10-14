package handler

import (
	"slices"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"github.com/leandrotsampa/torrent-indexer/schema"
)

const redeTorrentListingHTML = `<html><body>
<div class="row">
<div class="col">
    <a href="https://redestorrents.com/duna-a-profecia-1a-temporada-torrent-dublado-dual-audio-legendado-download/"
       class="text-decoration-none cover-link"
       title="Duna - A Profecia - 1ª Temporada Torrent">
        <article class="custom-card" data-title="Duna - A Profecia - 1ª Temporada" data-tipo="Séries"></article>
    </a>
</div>
<div class="col">
    <a href="https://redestorrents.com/duna-parte-2-torrent-dublado-dual-audio-legendado-download/"
       class="text-decoration-none cover-link"
       title="Duna - Parte 2 Torrent">
        <article class="custom-card" data-title="Duna - Parte 2" data-tipo="Filmes"></article>
    </a>
</div>
<a href="https://redestorrents.com/filmes/genero/acao/" class="genre-balloon-link">Ação</a>
</div>
</body></html>`

const redeTorrentMoviePostHTML = `<!DOCTYPE html>
<html lang="pt-BR"><head>
<meta charset="UTF-8">
<title>Duna - Parte 2 Torrent (2024) Dual Áudio 5.1 Download</title>
<script type="application/ld+json">
{
    "@context": "https://schema.org",
    "@type": "Movie",
    "name": "Duna - Parte 2",
    "alternateName": "Dune: Part Two",
    "sameAs": "https://www.imdb.com/title/tt15239678/",
    "datePublished": "2024-04-15T11:08:07-03:00"
}
</script>
<script type="application/ld+json">
{"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": []}
</script>
</head><body>
<article itemscope itemtype="https://schema.org/Movie">
<section class="hero">
    <h1 class="movie-title" itemprop="name">
        Duna - Parte 2 Torrent (2024) Dual Áudio 5.1 Download
    </h1>
    <p class="original-title" itemprop="alternativeHeadline">
        Dune: Part Two                    </p>
    <div class="info-pills">
        <div class="info-pill" aria-label="Ano de lançamento">
            <time itemprop="datePublished" datetime="2024-01-01">
                2024                            </time>
        </div>
    </div>
</section>
<main>
<section class="content-spacing download-section">
    <div class="download-list">
        <div class="download-row">
            <div class="download-name"> DOWNLOAD TORRENT DUAL ÁUDIO 5.1 MKV 1080P</div>
            <a href="magnet:?xt=urn:btih:4DBTGVDMM5JRT5IIGGFJEZZ5E37GCRPR&amp;dn=SITEDETORRENTS.COM..MKV.Duna%20-%20Parte%202%202024%20WEB-DL%201080p%20x264%20DUAL%205.1&amp;tr=udp%3A%2F%2Ftracker.openbittorrent.com%3A80%2Fannounce"
               rel="nofollow" class="download-btn download-btn-green">Download</a>
        </div>
        <div class="download-row">
            <div class="download-name"> DOWNLOAD TORRENT LEGENDADO 5.1 MKV 1080P</div>
            <a href="magnet:?xt=urn:btih:WHFPFKOFZK6HAWYFNQDNYU3F6HVPJQEY&amp;dn=SITEDETORRENTS.COM..MKV.-LEGENDADO-..Dune.Part.Two.2024.1080p.WEBRip.1600MB.DD5.1.x264-GalaxyRG%5BTGx%5D&amp;tr=udp%3A%2F%2Fopen.stealth.si%3A80%2Fannounce"
               rel="nofollow" class="download-btn download-btn-green">Download</a>
        </div>
    </div>
</section>
<section class="content-spacing">
    <div class="specs-grid-premium">
        <div class="spec-card-glass">
            <div class="spec-text">
                <small>Idioma / Áudio</small>
                <strong>Inglês / Português</strong>
            </div>
        </div>
        <div class="spec-card-glass">
            <div class="spec-text">
                <small>Qualidade de Áudio</small>
                <strong>10 / 10</strong>
            </div>
        </div>
        <div class="spec-card-glass">
            <div class="spec-text">
                <small>Tamanho do Arquivo</small>
                <strong> 1.49 GB | 3.50 GB</strong>
            </div>
        </div>
    </div>
</section>
</main>
</article>
</body></html>`

const redeTorrentSeriesPostHTML = `<!DOCTYPE html>
<html lang="pt-BR"><head>
<script type="application/ld+json">
{
    "@context": "https://schema.org",
    "@type": "TVSeries",
    "name": "Duna - A Profecia - 1ª Temporada",
    "sameAs": "https://www.imdb.com/title/tt10466872/"
}
</script>
</head><body>
<article itemscope itemtype="https://schema.org/TVSeries">
    <h1 class="movie-title" itemprop="name">
        Duna - A Profecia - 1ª Temporada Torrent (2024) Dual Áudio 5.1 Download
    </h1>
    <p class="original-title" itemprop="alternativeHeadline">Dune: Prophecy S01</p>
    <div class="info-pill" aria-label="Ano de lançamento"><time datetime="2024-01-01">2024</time></div>
    <div class="download-list">
        <div class="download-row">
            <div class="download-name">01º EPISÓDIO DOWNLOAD TORRENT DUAL ÁUDIO 5.1 MKV 1080P</div>
            <a href="magnet:?xt=urn:btih:21d0d3bbf3c89f2c8b25ee8c90143ca1ded23612&amp;dn=HIDRATORRENTS.ORG.-DUBLADO-DUAL-AUDIO-.Duna.A.Profecia.S01E01.1080p.WEB-DL.DUAL.5.1&amp;tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce" class="download-btn">Download</a>
        </div>
        <div class="download-row">
            <div class="download-name">02º EPISÓDIO DOWNLOAD TORRENT DUAL ÁUDIO 5.1 MKV 1080P</div>
            <a href="magnet:?xt=urn:btih:a41706ad15ea08c818d79c657308dc30da5f6f65&amp;dn=HIDRATORRENTS.ORG.-DUBLADO-DUAL-AUDIO-.Duna.A.Profecia.S01E02.1080p.WEB-DL.DUAL.5.1&amp;tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce" class="download-btn">Download</a>
        </div>
    </div>
    <div class="spec-card-glass"><small>Idioma / Áudio</small><strong>Inglês / Português</strong></div>
</article>
</body></html>`

func mustRedeTorrentDoc(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	return doc
}

func TestBuildRedeTorrentURL(t *testing.T) {
	meta := IndexerMeta{URL: "https://redestorrents.com/", SearchURL: "index.php?busca=", PagePattern: "pagina/%s/"}
	cases := []struct {
		q, page, want string
	}{
		{"", "", "https://redestorrents.com/"},
		{"", "2", "https://redestorrents.com/pagina/2/"},
		{"the boys", "", "https://redestorrents.com/index.php?busca=the+boys"},
		{"the boys", "2", "https://redestorrents.com/index.php?busca=the+boys&pagina=2"},
	}
	for _, c := range cases {
		if got := buildRedeTorrentURL(meta, c.q, c.page); got != c.want {
			t.Errorf("buildRedeTorrentURL(%q, %q) = %q, want %q", c.q, c.page, got, c.want)
		}
	}
}

func TestExtractRedeTorrentLinksReadsCoverLinks(t *testing.T) {
	got := extractRedeTorrentLinks(mustRedeTorrentDoc(t, redeTorrentListingHTML))
	want := []string{
		"https://redestorrents.com/duna-a-profecia-1a-temporada-torrent-dublado-dual-audio-legendado-download/",
		"https://redestorrents.com/duna-parte-2-torrent-dublado-dual-audio-legendado-download/",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("extractRedeTorrentLinks() = %q, want %q", got, want)
	}
}

func TestParseRedeTorrentPostMovie(t *testing.T) {
	post, err := parseRedeTorrentPost(mustRedeTorrentDoc(t, redeTorrentMoviePostHTML), "https://redestorrents.com/duna-parte-2/")
	if err != nil {
		t.Fatalf("parseRedeTorrentPost() error = %v", err)
	}

	if post.Title != "Duna" {
		t.Errorf("Title = %q, want %q", post.Title, "Duna")
	}
	if post.Year != "2024" {
		t.Errorf("Year = %q, want %q", post.Year, "2024")
	}
	if post.OriginalTitle != "Dune: Part Two" {
		t.Errorf("OriginalTitle = %q, want %q", post.OriginalTitle, "Dune: Part Two")
	}
	if post.IMDB != "https://www.imdb.com/title/tt15239678/" {
		t.Errorf("IMDB = %q", post.IMDB)
	}
	if !slices.Equal(post.Audio, []schema.Audio{schema.AudioEnglish, schema.AudioPortuguese}) {
		t.Errorf("Audio = %v, want [eng por]", post.Audio)
	}
	if !slices.Equal(post.Sizes, []string{"1.49 GB", "3.50 GB"}) {
		t.Errorf("Sizes = %q", post.Sizes)
	}

	if len(post.Magnets) != 2 {
		t.Fatalf("got %d magnets, want 2", len(post.Magnets))
	}
	if !strings.HasPrefix(post.Magnets[0].Link, "magnet:?xt=urn:btih:4DBTGVDMM5JRT5IIGGFJEZZ5E37GCRPR&dn=") {
		t.Errorf("first magnet = %q", post.Magnets[0].Link)
	}
	if post.Magnets[0].Label != "DOWNLOAD TORRENT DUAL ÁUDIO 5.1 MKV 1080P" {
		t.Errorf("first label = %q", post.Magnets[0].Label)
	}
	if post.Magnets[1].Label != "DOWNLOAD TORRENT LEGENDADO 5.1 MKV 1080P" {
		t.Errorf("second label = %q", post.Magnets[1].Label)
	}
}

func TestParseRedeTorrentPostSeries(t *testing.T) {
	post, err := parseRedeTorrentPost(mustRedeTorrentDoc(t, redeTorrentSeriesPostHTML), "https://redestorrents.com/duna-a-profecia/")
	if err != nil {
		t.Fatalf("parseRedeTorrentPost() error = %v", err)
	}

	if post.Title != "Duna" || post.Year != "2024" {
		t.Errorf("Title/Year = %q/%q, want Duna/2024", post.Title, post.Year)
	}
	if post.IMDB != "https://www.imdb.com/title/tt10466872/" {
		t.Errorf("IMDB = %q", post.IMDB)
	}
	if len(post.Magnets) != 2 {
		t.Fatalf("got %d magnets, want 2", len(post.Magnets))
	}
	if post.Magnets[1].Label != "02º EPISÓDIO DOWNLOAD TORRENT DUAL ÁUDIO 5.1 MKV 1080P" {
		t.Errorf("second label = %q", post.Magnets[1].Label)
	}
	if len(post.Sizes) != 0 {
		t.Errorf("Sizes = %q, want none", post.Sizes)
	}
}

func TestParseRedeTorrentPostFallsBackToYearPill(t *testing.T) {
	html := strings.Replace(redeTorrentMoviePostHTML,
		"Duna - Parte 2 Torrent (2024) Dual Áudio 5.1 Download\n    </h1>",
		"Duna - Parte 2 Torrent Dual Áudio 5.1 Download\n    </h1>", 1)
	post, err := parseRedeTorrentPost(mustRedeTorrentDoc(t, html), "https://redestorrents.com/duna-parte-2/")
	if err != nil {
		t.Fatalf("parseRedeTorrentPost() error = %v", err)
	}
	if post.Year != "2024" {
		t.Errorf("Year = %q, want 2024", post.Year)
	}
	if post.Title != "Duna - Parte 2" {
		t.Errorf("Title = %q, want %q", post.Title, "Duna - Parte 2")
	}
}

func TestParseRedeTorrentPostFailsWithoutTitle(t *testing.T) {
	if _, err := parseRedeTorrentPost(mustRedeTorrentDoc(t, redeTorrentListingHTML), "https://redestorrents.com/x/"); err == nil {
		t.Fatal("expected error for page without h1.movie-title")
	}
}

func TestRedeTorrentMagnetAudio(t *testing.T) {
	content := []schema.Audio{schema.AudioEnglish, schema.AudioPortuguese}

	dual := redeTorrentMagnetAudio("Duna - Parte 2 2024 WEB-DL 1080p x264 DUAL 5.1", "DOWNLOAD TORRENT DUAL ÁUDIO 5.1 MKV 1080P", content)
	if !slices.Equal(dual, []schema.Audio{schema.AudioPortuguese, schema.AudioEnglish}) {
		t.Errorf("dual audio = %v", dual)
	}

	// the release name does not announce the dub, but the page label does
	labelOnly := redeTorrentMagnetAudio("Duna.Parte.2.2024.1080p.WEB-DL", "DOWNLOAD TORRENT DUAL ÁUDIO 5.1 MKV 1080P", content)
	if !slices.Equal(labelOnly, []schema.Audio{schema.AudioPortuguese, schema.AudioEnglish}) {
		t.Errorf("label-only dual audio = %v", labelOnly)
	}

	subbed := redeTorrentMagnetAudio("-LEGENDADO-..Dune.Part.Two.2024.1080p.WEBRip", "DOWNLOAD TORRENT LEGENDADO 5.1 MKV 1080P", content)
	if !slices.Equal(subbed, []schema.Audio{schema.AudioEnglish}) {
		t.Errorf("subbed audio = %v", subbed)
	}
}
