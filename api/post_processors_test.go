package handler

import (
	"net/http"
	"testing"

	"github.com/leandrotsampa/torrent-indexer/schema"
)

func TestFallbackPostTitleUsesOriginalTitleByDefault(t *testing.T) {
	indexer := &Indexer{}
	request := &http.Request{}
	torrents := []schema.IndexedTorrent{
		{OriginalTitle: "Rick e Morty 9a Temporada Torrent"},
	}

	got := FallbackPostTitle(indexer, request, torrents)

	if got[0].Title != "Rick e Morty 9a Temporada Torrent" {
		t.Fatalf("Title = %q", got[0].Title)
	}
}

func TestFallbackPostTitleCanMarkFallbackAsUnsafe(t *testing.T) {
	indexer := &Indexer{config: IndexersConfig{FallbackTitleEnabled: true}}
	request := &http.Request{}
	torrents := []schema.IndexedTorrent{
		{OriginalTitle: "Rick e Morty 9a Temporada Torrent"},
	}

	got := FallbackPostTitle(indexer, request, torrents)

	if got[0].Title != "[UNSAFE] Rick e Morty 9a Temporada Torrent" {
		t.Fatalf("Title = %q", got[0].Title)
	}
}
