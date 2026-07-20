package handler

import (
	"testing"

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

	got := filterBluDVSeasonResults("rick and morty temporada 1 episodio 2", torrents)

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

	got := filterBluDVSeasonResults("rick e morty s01e01", torrents)

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

	got := filterBluDVSeasonResults("georgie e mandy temporada 2", torrents)

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

	got := filterBluDVSeasonResults("rick and morty s01e02", torrents)

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

	got := filterBluDVSearchLinks("rick e morty s01e01", "1", links)

	if len(got) != 1 {
		t.Fatalf("got %d links, want 1", len(got))
	}
	if got[0].URL != "https://bludv2.xyz/rick-and-morty-1a-temporada/" {
		t.Fatalf("URL = %q", got[0].URL)
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
