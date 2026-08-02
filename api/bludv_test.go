package handler

import (
	"slices"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"github.com/leandrotsampa/torrent-indexer/schema"
)

func TestNormalizeBluDVSearchQueryConvertsSeasonEpisode(t *testing.T) {
	got := normalizeBluDVSearchQuery("rick and morty s01e02")
	want := "rick e morty temporada 1"

	if got != want {
		t.Fatalf("normalizeBluDVSearchQuery() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVSearchQueryDropsEpisodeFromTemporadaQuery(t *testing.T) {
	got := normalizeBluDVSearchQuery("rick and morty temporada 1 episodio 2")
	want := "rick e morty temporada 1"

	if got != want {
		t.Fatalf("normalizeBluDVSearchQuery() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVSearchQueryConvertsSeasonEpisodeWithSeparators(t *testing.T) {
	got := normalizeBluDVSearchQuery("Rick.And.Morty.S01.E02.1080p")
	want := "Rick.e.Morty.temporada 1.1080p"

	if got != want {
		t.Fatalf("normalizeBluDVSearchQuery() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVSearchQueryConvertsSonarrConjunctionsToPortuguese(t *testing.T) {
	cases := map[string]string{
		"rick and morty s01e01": "rick e morty temporada 1",
		"rick y morty s01e01":   "rick e morty temporada 1",
		"rick et morty s01e01":  "rick e morty temporada 1",
		"rick es morty s01e01":  "rick e morty temporada 1",
	}

	for input, want := range cases {
		if got := normalizeBluDVSearchQuery(input); got != want {
			t.Fatalf("normalizeBluDVSearchQuery(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeBluDVSearchQueryLeavesOtherQueriesUnchanged(t *testing.T) {
	got := normalizeBluDVSearchQuery("vingadores ultimato")
	want := "vingadores ultimato"

	if got != want {
		t.Fatalf("normalizeBluDVSearchQuery() = %q, want %q", got, want)
	}
}

func TestFilterBluDVSeasonResultsKeepsRequestedSeason(t *testing.T) {
	torrents := []schema.IndexedTorrent{
		{OriginalTitle: "Rick and Morty 7a Temporada Torrent"},
		{OriginalTitle: "Rick and Morty 1a Temporada Torrent"},
		{Title: "Rick and Morty S01 WEB-DL"},
	}

	got := filterBluDVSeasonResults("rick and morty temporada 1 episodio 2", torrents, nil)

	if len(got) != 2 {
		t.Fatalf("got %d torrents, want 2", len(got))
	}
	if got[0].OriginalTitle != "Rick and Morty 1a Temporada Torrent" {
		t.Fatalf("first result = %q", got[0].OriginalTitle)
	}
	if got[1].Title != "Rick and Morty S01 WEB-DL" {
		t.Fatalf("second result = %q", got[1].Title)
	}
}

func TestFilterBluDVSeasonResultsRejectsAppendedTitleWithoutSeparator(t *testing.T) {
	// Extra words appended without a subtitle separator indicate a different
	// title and must be rejected (only the exact show is kept).
	torrents := []schema.IndexedTorrent{
		{OriginalTitle: "Rick and Morty Multiverso 1a Temporada Torrent"},
		{OriginalTitle: "Rick and Morty 1a Temporada Torrent"},
	}

	got := filterBluDVSeasonResults("rick e morty s01e01", torrents, nil)

	if len(got) != 1 {
		t.Fatalf("got %d torrents, want 1", len(got))
	}
	if got[0].OriginalTitle != "Rick and Morty 1a Temporada Torrent" {
		t.Fatalf("result = %q", got[0].OriginalTitle)
	}
}

func TestFilterBluDVSeasonResultsKeepsLocalizedSubtitle(t *testing.T) {
	// The show name is followed by a localized subtitle after a separator, as
	// bludv titles this spin-off ("Georgie e Mandy: Seu Primeiro Casamento").
	torrents := []schema.IndexedTorrent{
		{OriginalTitle: "Georgie e Mandy: Seu Primeiro Casamento 2a Temporada (2025)"},
		{OriginalTitle: "Georgie e Mandy Seu Primeiro Casamento 1a Temporada (2024)"},
	}

	got := filterBluDVSeasonResults("georgie e mandy temporada 2", torrents, nil)

	if len(got) != 1 {
		t.Fatalf("got %d torrents, want 1", len(got))
	}
	if got[0].OriginalTitle != "Georgie e Mandy: Seu Primeiro Casamento 2a Temporada (2025)" {
		t.Fatalf("result = %q", got[0].OriginalTitle)
	}
}

func TestFilterBluDVSeasonResultsReturnsEmptyWhenSeasonDoesNotMatch(t *testing.T) {
	torrents := []schema.IndexedTorrent{
		{OriginalTitle: "Rick and Morty 7a Temporada Torrent"},
		{OriginalTitle: "Rick and Morty 5a Temporada Torrent"},
	}

	got := filterBluDVSeasonResults("rick and morty s01e02", torrents, nil)

	if len(got) != 0 {
		t.Fatalf("got %d torrents, want 0", len(got))
	}
}

func TestBuildBluDVURLUsesSearchPagination(t *testing.T) {
	got := buildBluDVURL(IndexerMeta{URL: "https://bludv2.xyz/", SearchURL: "?s="}, "rick and morty temporada 1", "2")
	want := "https://bludv2.xyz/page/2/?s=rick+and+morty+temporada+1"

	if got != want {
		t.Fatalf("buildBluDVURL() = %q, want %q", got, want)
	}
}

func TestFilterBluDVSearchLinksKeepsRequestedTitleAndSeason(t *testing.T) {
	links := []bludvSearchLink{
		{URL: "https://bludv2.xyz/rick-and-morty-multiverso-1a-temporada/", Title: "Rick and Morty Multiverso 1a Temporada"},
		{URL: "https://bludv2.xyz/rick-and-morty-7a-temporada/", Title: "Rick and Morty 7a Temporada"},
		{URL: "https://bludv2.xyz/rick-and-morty-1a-temporada/", Title: "Rick and Morty 1a Temporada"},
	}

	got, pending := filterBluDVSearchLinks("rick e morty s01e01", "1", links)

	if len(got) != 1 {
		t.Fatalf("got %d links, want 1", len(got))
	}
	if got[0].URL != "https://bludv2.xyz/rick-and-morty-1a-temporada/" {
		t.Fatalf("URL = %q", got[0].URL)
	}
	// The spin-off shares the requested season, so it is only pending: it stays
	// out of the results unless one of its title fields matches the request.
	if len(pending) != 1 || pending[0].URL != "https://bludv2.xyz/rick-and-morty-multiverso-1a-temporada/" {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestFilterBluDVSearchLinksReportsLocalizedTitleAsPending(t *testing.T) {
	// Sonarr searches in the original language while bludv titles the post in
	// Portuguese, so the link can only be confirmed by reading the post.
	links := []bludvSearchLink{
		{
			URL:   "https://bludv2.xyz/stuart-nao-consegue-salvar-o-universo-1a-temporada/",
			Title: "Stuart Não Consegue Salvar o Universo 1ª Temporada Torrent (2026) WEB-DL 1080p Dual Áudio",
		},
	}

	matched, pending := filterBluDVSearchLinks("stuart fails to save the universe temporada 1", "1", links)

	if len(matched) != 0 {
		t.Fatalf("got %d matched links, want 0", len(matched))
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending links, want 1", len(pending))
	}
}

func TestFilterBluDVSearchLinksIgnoresOtherSeasonsWhenPending(t *testing.T) {
	links := []bludvSearchLink{
		{URL: "https://bludv2.xyz/stuart-1a-temporada/", Title: "Stuart Não Consegue Salvar o Universo 1ª Temporada"},
		{URL: "https://bludv2.xyz/stuart-2a-temporada/", Title: "Stuart Não Consegue Salvar o Universo 2ª Temporada"},
	}

	matched, pending := filterBluDVSearchLinks("stuart fails to save the universe temporada 2", "2", links)

	if len(matched) != 0 {
		t.Fatalf("got %d matched links, want 0", len(matched))
	}
	if len(pending) != 1 || pending[0].URL != "https://bludv2.xyz/stuart-2a-temporada/" {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestExtractBluDVPostTitlesReadsOriginalTitleFirst(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="post"><div class="content">
<p><span><strong><em>T&iacute;tulo Traduzido:</em></strong> Stuart N&atilde;o Consegue Salvar o Universo</span><br />
<span><strong><em>T&iacute;tulo Original:</em></strong>&nbsp;Stuart Fails to Save the Universe</span><br />
<span><strong><em>G&ecirc;nero:</em></strong> Com&eacute;dia | Fantasia | Fic&ccedil;&atilde;o</span></p>
</div></div>`))
	if err != nil {
		t.Fatalf("failed to parse document: %v", err)
	}

	got := extractBluDVPostTitles(doc.Find(".post div.content"))
	want := []string{"Stuart Fails to Save the Universe", "Stuart Não Consegue Salvar o Universo"}

	if !slices.Equal(got, want) {
		t.Fatalf("extractBluDVPostTitles() = %#v, want %#v", got, want)
	}
}

func TestExtractBluDVPostTitlesFallsBackToOtherTitleFields(t *testing.T) {
	// Posts that omit "Título Original" still name the show in another field.
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="post"><div class="content">
<p><strong>Nome Original:</strong> Stuart Fails to Save the Universe<br />
<strong>Ano de Lan&ccedil;amento:</strong> 2026</p>
</div></div>`))
	if err != nil {
		t.Fatalf("failed to parse document: %v", err)
	}

	got := extractBluDVPostTitles(doc.Find(".post div.content"))
	want := []string{"Stuart Fails to Save the Universe"}

	if !slices.Equal(got, want) {
		t.Fatalf("extractBluDVPostTitles() = %#v, want %#v", got, want)
	}
}

func TestMatchesAnyBluDVPostTitleAcceptsOriginalTitle(t *testing.T) {
	titles := []string{"Stuart Fails to Save the Universe", "Stuart Não Consegue Salvar o Universo"}

	if !matchesAnyBluDVPostTitle("stuart fails to save the universe temporada 1", titles) {
		t.Fatal("expected the post to match the requested original title")
	}
}

func TestMatchesAnyBluDVPostTitleRejectsSimilarShow(t *testing.T) {
	// A franchise sharing words with the request must stay rejected: the post
	// title fields are compared as strictly as the post title itself.
	titles := []string{"Rick and Morty Multiverse", "Rick and Morty Multiverso"}

	if matchesAnyBluDVPostTitle("rick e morty temporada 1", titles) {
		t.Fatal("expected the spin-off post to be rejected")
	}
}

func TestMatchesAnyBluDVPostTitleRejectsPostWithoutTitleFields(t *testing.T) {
	if matchesAnyBluDVPostTitle("stuart fails to save the universe temporada 1", nil) {
		t.Fatal("expected a post without title fields to be rejected")
	}
}

func TestFilterBluDVSeasonResultsKeepsTitleConfirmedPost(t *testing.T) {
	confirmed := map[string]bool{"https://bludv2.xyz/stuart-1a-temporada/": true}
	torrents := []schema.IndexedTorrent{
		{
			OriginalTitle: "Stuart Não Consegue Salvar o Universo S01E01 1ª Temporada",
			Details:       "https://bludv2.xyz/stuart-1a-temporada/",
		},
		{
			OriginalTitle: "Stuart Não Consegue Salvar o Universo S02E01 2ª Temporada",
			Details:       "https://bludv2.xyz/stuart-1a-temporada/",
		},
		{
			OriginalTitle: "Outra Série S01E01 1ª Temporada",
			Details:       "https://bludv2.xyz/outra-serie-1a-temporada/",
		},
	}

	got := filterBluDVSeasonResults("stuart fails to save the universe temporada 1", torrents, confirmed)

	if len(got) != 1 {
		t.Fatalf("got %d torrents, want 1", len(got))
	}
	if got[0].OriginalTitle != "Stuart Não Consegue Salvar o Universo S01E01 1ª Temporada" {
		t.Fatalf("result = %q", got[0].OriginalTitle)
	}
}

func TestMatchesBluDVRequestedTitleTreatsEAndAndAsEquivalent(t *testing.T) {
	for _, query := range []string{
		"rick e morty s01e01",
		"rick and morty s01e01",
		"rick y morty s01e01",
		"rick et morty s01e01",
		"rick es morty s01e01",
	} {
		if !matchesBluDVRequestedTitle(query, "Rick and Morty 1a Temporada Torrent") {
			t.Fatalf("expected title to match query %q", query)
		}
	}
}

func TestMatchesBluDVRequestedTitleRejectsAppendedWithoutSeparator(t *testing.T) {
	// Extra words with no subtitle separator mean a different title.
	if matchesBluDVRequestedTitle("rick e morty s01e01", "Rick e Morty Multiverso 1a Temporada") {
		t.Fatal("expected appended title not to match")
	}
}

func TestMatchesBluDVRequestedTitleAcceptsLocalizedSubtitle(t *testing.T) {
	// A localized subtitle after a separator is tolerated, so bludv posts like
	// "Georgie e Mandy: Seu Primeiro Casamento" match the short query. Spin-offs
	// that share the base name (e.g. "Rick e Morty: O Anime") are also accepted
	// here on purpose; Sonarr/Prowlarr do the final title/episode matching.
	if !matchesBluDVRequestedTitle("georgie e mandy temporada 2", "Georgie e Mandy: Seu Primeiro Casamento 2a Temporada") {
		t.Fatal("expected localized subtitle title to match")
	}
}

func TestNormalizeBluDVReleaseTitleForSonarrAddsSeasonTag(t *testing.T) {
	got := normalizeBluDVReleaseTitleForSonarr("Rick and Morty 1a Temporada Torrent - BluRay 720p/1080p Dual Audio (eng)")
	want := "Rick and Morty S01 1a Temporada Torrent - BluRay 720p/1080p Dual Audio (eng)"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseTitleForSonarr() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVReleaseTitleForSonarrAddsEpisodeTag(t *testing.T) {
	got := normalizeBluDVReleaseTitleForSonarr("Rick and Morty 1a Temporada Episodio 1 Torrent - WEB-DL 1080p")
	want := "Rick and Morty S01E01 1a Temporada Episodio 1 Torrent - WEB-DL 1080p"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseTitleForSonarr() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVReleaseTitleForSonarrAddsEpisodeToExistingSeasonTag(t *testing.T) {
	got := normalizeBluDVReleaseTitleForSonarr("Rick and Morty S01 Episodio 1 Torrent - WEB-DL 1080p")
	want := "Rick and Morty S01E01 Episodio 1 Torrent - WEB-DL 1080p"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseTitleForSonarr() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVReleaseTitleForSonarrDoesNotDuplicateEpisodeTag(t *testing.T) {
	got := normalizeBluDVReleaseTitleForSonarr("Rick and Morty S01E01 Torrent - WEB-DL 1080p")
	want := "Rick and Morty S01E01 Torrent - WEB-DL 1080p"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseTitleForSonarr() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVReleaseTitleForSonarrDoesNotDuplicateSeasonTag(t *testing.T) {
	got := normalizeBluDVReleaseTitleForSonarr("Rick and Morty S01 1a Temporada Torrent")
	want := "Rick and Morty S01 1a Temporada Torrent"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseTitleForSonarr() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVReleaseQualityForSonarrSelectsQualityBySize(t *testing.T) {
	title := "Rick and Morty S01 1a Temporada Torrent - BluRay 720p/1080p Dual Audio (eng)"
	sizes := []string{"5.4 GB", "8.8 GB"}

	got720p := normalizeBluDVReleaseQualityForSonarr(title, "", 0, 2, sizes)
	want720p := "Rick and Morty S01 1a Temporada Torrent - BluRay 720p Dual Audio (eng)"
	if got720p != want720p {
		t.Fatalf("normalizeBluDVReleaseQualityForSonarr() = %q, want %q", got720p, want720p)
	}

	got1080p := normalizeBluDVReleaseQualityForSonarr(title, "", 1, 2, sizes)
	want1080p := "Rick and Morty S01 1a Temporada Torrent - BluRay 1080p Dual Audio (eng)"
	if got1080p != want1080p {
		t.Fatalf("normalizeBluDVReleaseQualityForSonarr() = %q, want %q", got1080p, want1080p)
	}
}

func TestNormalizeBluDVReleaseQualityForSonarrUsesMagnetDisplayNameQuality(t *testing.T) {
	title := "Rick and Morty S01 1a Temporada Torrent - BluRay 720p/1080p Dual Audio (eng)"

	got := normalizeBluDVReleaseQualityForSonarr(title, "Rick and Morty S01 1080p BluRay", 0, 1, nil)
	want := "Rick and Morty S01 1a Temporada Torrent - BluRay 1080p Dual Audio (eng)"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseQualityForSonarr() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVReleaseQualityFromLabelForSonarr(t *testing.T) {
	title := "Rick and Morty S04 4a Temporada Torrent - WEB-DL 720p/1080p Dual Audio (eng)"

	got := normalizeBluDVReleaseQualityFromLabelForSonarr(title, "1080p")
	want := "Rick and Morty S04 4a Temporada Torrent - WEB-DL 1080p Dual Audio (eng)"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseQualityFromLabelForSonarr() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVReleaseQualityFromLabelForSonarrUsesContextBeforeButton(t *testing.T) {
	title := "Rick and Morty S04 4a Temporada Torrent - WEB-DL 720p/1080p Dual Audio (eng)"

	got := normalizeBluDVReleaseQualityFromLabelForSonarr(title, "WEB-DL 1080p (9.79 GB) Magnet-Link")
	want := "Rick and Morty S04 4a Temporada Torrent - WEB-DL 1080p Dual Audio (eng)"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseQualityFromLabelForSonarr() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVReleaseEpisodeForSonarrAddsEpisodeToSeasonTag(t *testing.T) {
	got := normalizeBluDVReleaseEpisodeForSonarr("Rick and Morty S04 4a Temporada Torrent - WEB-DL 1080p", "1")
	want := "Rick and Morty S04E01 4a Temporada Torrent - WEB-DL 1080p"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseEpisodeForSonarr() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVReleaseEpisodeForSonarrDoesNotDuplicateEpisodeTag(t *testing.T) {
	got := normalizeBluDVReleaseEpisodeForSonarr("Rick and Morty S04E01 4a Temporada Torrent - WEB-DL 1080p", "1")
	want := "Rick and Morty S04E01 4a Temporada Torrent - WEB-DL 1080p"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseEpisodeForSonarr() = %q, want %q", got, want)
	}
}

func TestExtractBluDVMagnetLinksTagsPlainMagnetsWithTheirEpisode(t *testing.T) {
	// bludv publishes one plain magnet per episode, announced by the text right
	// before the anchor. Without reading it every episode would be indexed as a
	// full-season pack, since the post title only names the season.
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="post"><div class="content">
<p>T&iacute;tulo Original: Stuart Fails to Save the Universe<br />
IMDb: <a href="https://www.imdb.com/pt/title/tt27497393/">7,0</a></p>
<p><center><strong>EPIS&Oacute;DIO 01:</strong> <a href="magnet:?xt=urn:btih:aaa&#038;dn=Stuart%20S01E01" rel="noopener">1080p</a></center><br />
<center><strong>EPIS&Oacute;DIO 02:</strong> <a href="magnet:?xt=urn:btih:bbb&#038;dn=Stuart%20S01E02" rel="noopener">1080p</a></center></p>
<p><a href="https://watch.brplayer.xyz/stuart">ASSISTIR ONLINE</a></p>
</div></div>`))
	if err != nil {
		t.Fatalf("failed to parse document: %v", err)
	}

	got := extractBluDVMagnetLinks(t.Context(), nil, doc.Find(".post div.content"), "")

	if len(got) != 2 {
		t.Fatalf("got %d magnet links, want 2: %+v", len(got), got)
	}
	if got[0].URL != "magnet:?xt=urn:btih:aaa&dn=Stuart%20S01E01" {
		t.Fatalf("first URL = %q", got[0].URL)
	}
	if got[0].Episode != "1" || got[1].Episode != "2" {
		t.Fatalf("episodes = %q, %q, want 1, 2", got[0].Episode, got[1].Episode)
	}
	if got[0].Label != "1080p" || got[1].Label != "1080p" {
		t.Fatalf("labels = %q, %q", got[0].Label, got[1].Label)
	}
}

func TestExtractBluDVMagnetLinksLeavesSeasonPackWithoutEpisode(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="post"><div class="content">
<p>Qualidade: WEB-DL<br />
<a href="magnet:?xt=urn:btih:aaa&#038;dn=Rick.and.Morty.S01.720p">720p</a><br />
<a href="magnet:?xt=urn:btih:bbb&#038;dn=Rick.and.Morty.S01.1080p">1080p</a></p>
</div></div>`))
	if err != nil {
		t.Fatalf("failed to parse document: %v", err)
	}

	got := extractBluDVMagnetLinks(t.Context(), nil, doc.Find(".post div.content"), "")

	if len(got) != 2 {
		t.Fatalf("got %d magnet links, want 2", len(got))
	}
	for _, link := range got {
		if link.Episode != "" {
			t.Fatalf("season pack link tagged with episode %q", link.Episode)
		}
	}
}

func TestExtractBluDVMagnetLinksIgnoresUnrelatedAnchors(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="post"><div class="content">
<p><a href="https://bludvfilmes.xyz/series/">S&eacute;ries</a>
<a href="https://www.youtube.com/watch?v=abc">Trailer</a>
<a href="magnet:?xt=urn:btih:aaa">Magnet-Link</a></p>
</div></div>`))
	if err != nil {
		t.Fatalf("failed to parse document: %v", err)
	}

	got := extractBluDVMagnetLinks(t.Context(), nil, doc.Find(".post div.content"), "")

	if len(got) != 1 {
		t.Fatalf("got %d magnet links, want 1: %+v", len(got), got)
	}
	if got[0].URL != "magnet:?xt=urn:btih:aaa" {
		t.Fatalf("URL = %q", got[0].URL)
	}
}

func TestExtractBluDVMagnetLinksClearsEpisodeOnSeasonPack(t *testing.T) {
	// A whole-season download listed after the episodes must not inherit the
	// episode above it.
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="post"><div class="content">
<p><strong>EPIS&Oacute;DIO 01:</strong> <a href="magnet:?xt=urn:btih:aaa">1080p</a><br />
<strong>EPIS&Oacute;DIO 02:</strong> <a href="magnet:?xt=urn:btih:bbb">1080p</a><br />
<strong>TEMPORADA COMPLETA:</strong> <a href="magnet:?xt=urn:btih:ccc">1080p</a></p>
</div></div>`))
	if err != nil {
		t.Fatalf("failed to parse document: %v", err)
	}

	got := extractBluDVMagnetLinks(t.Context(), nil, doc.Find(".post div.content"), "")

	if len(got) != 3 {
		t.Fatalf("got %d magnet links, want 3", len(got))
	}
	if got[2].Episode != "" {
		t.Fatalf("season pack tagged with episode %q", got[2].Episode)
	}
}

func TestExtractBluDVMagnetLinksTagsRangeOfEpisodes(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<div class="post"><div class="content">
<p><strong>EPIS&Oacute;DIOS 01 E 02:</strong> <a href="magnet:?xt=urn:btih:aaa">1080p</a><br />
<strong>EPIS&Oacute;DIO 03:</strong> <a href="magnet:?xt=urn:btih:bbb">1080p</a></p>
</div></div>`))
	if err != nil {
		t.Fatalf("failed to parse document: %v", err)
	}

	got := extractBluDVMagnetLinks(t.Context(), nil, doc.Find(".post div.content"), "")

	if len(got) != 2 {
		t.Fatalf("got %d magnet links, want 2", len(got))
	}
	if got[0].Episode != "1-2" || got[1].Episode != "3" {
		t.Fatalf("episodes = %q, %q, want 1-2, 3", got[0].Episode, got[1].Episode)
	}
}

func TestExtractBluDVEpisodeReadsRangeFromMagnetDisplayName(t *testing.T) {
	// bludv ships a single torrent for two episodes, named "S01E01-02".
	if got := extractBluDVEpisode("Cabo do Medo S01E01-02 WEB-DL 1080p x264 DUAL 5.1"); got != "1-2" {
		t.Fatalf("extractBluDVEpisode() = %q, want %q", got, "1-2")
	}
}

func TestNormalizeBluDVReleaseEpisodeForSonarrRendersRange(t *testing.T) {
	got := normalizeBluDVReleaseEpisodeForSonarr("Cabo do Medo S01 1a Temporada Torrent - WEB-DL 1080p", "1-2")
	want := "Cabo do Medo S01E01-E02 1a Temporada Torrent - WEB-DL 1080p"

	if got != want {
		t.Fatalf("normalizeBluDVReleaseEpisodeForSonarr() = %q, want %q", got, want)
	}
}

func TestBluDVEpisodeFromContextUsesClosestLabel(t *testing.T) {
	// The text between two anchors ends with the label of the next link, so the
	// last occurrence is the one that belongs to it.
	if got := bludvEpisodeFromContext("Episódio 01: 1080p Episódio 02:", "1"); got != "2" {
		t.Fatalf("bludvEpisodeFromContext() = %q, want %q", got, "2")
	}
}

func TestBluDVEpisodeFromContextCarriesEpisodeOver(t *testing.T) {
	// The resolutions of one episode are listed side by side, separated by text
	// that names no episode.
	if got := bludvEpisodeFromContext(" | ", "3"); got != "3" {
		t.Fatalf("bludvEpisodeFromContext() = %q, want %q", got, "3")
	}
}

func TestBluDVEpisodeFromContextKeepsEpisodeAnnouncedAfterMarker(t *testing.T) {
	// The season heading opens the list, so the episode label that follows it
	// still wins.
	if got := bludvEpisodeFromContext("TEMPORADA COMPLETA: 1080p EPISÓDIO 04:", ""); got != "4" {
		t.Fatalf("bludvEpisodeFromContext() = %q, want %q", got, "4")
	}
}

func TestExtractBluDVEpisodeReadsMagnetDisplayName(t *testing.T) {
	// Fallback for posts that do not label the link: the magnet display name
	// carries the episode.
	got := extractBluDVEpisode("Stuart Não Consegue Salvar o Universo S01E02 WEB-DL 1080p x264 DUAL 5.1")
	if got != "2" {
		t.Fatalf("extractBluDVEpisode() = %q, want %q", got, "2")
	}
}
