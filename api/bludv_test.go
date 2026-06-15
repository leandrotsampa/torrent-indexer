package handler

import "testing"

func TestNormalizeBluDVSearchQueryConvertsSeasonEpisode(t *testing.T) {
	got := normalizeBluDVSearchQuery("rick and morty s01e02")
	want := "rick and morty temporada 1 episodio 2"

	if got != want {
		t.Fatalf("normalizeBluDVSearchQuery() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVSearchQueryConvertsSeasonEpisodeWithSeparators(t *testing.T) {
	got := normalizeBluDVSearchQuery("Rick.And.Morty.S01.E02.1080p")
	want := "Rick.And.Morty.temporada 1 episodio 2.1080p"

	if got != want {
		t.Fatalf("normalizeBluDVSearchQuery() = %q, want %q", got, want)
	}
}

func TestNormalizeBluDVSearchQueryLeavesOtherQueriesUnchanged(t *testing.T) {
	got := normalizeBluDVSearchQuery("vingadores ultimato")
	want := "vingadores ultimato"

	if got != want {
		t.Fatalf("normalizeBluDVSearchQuery() = %q, want %q", got, want)
	}
}
