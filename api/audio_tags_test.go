package handler

import (
	"net/http"
	"testing"

	"github.com/leandrotsampa/torrent-indexer/schema"
)

// A middleware may replace the title with the torrent name (see
// FullfilMissingMetadata), which usually does not repeat the dub marker the site
// published next to the link — the language must survive that swap.
func TestAppendAudioTagsKeepsKnownLanguagesWhenTheTitleChanges(t *testing.T) {
	torrents := []schema.IndexedTorrent{
		{
			// name resolved from the torrent metadata: no "Dublado"/"Multi-Audio"
			Title: "Tomb.Raider.King.S01E01.1080p.CR.WEB-DL.MULTi.AAC2.0.H.264.MSubs-ToonsHub",
			// what the page said: the link sat under the "Dublado>>" row
			Audio: []schema.Audio{schema.AudioPortuguese, schema.AudioJapanese},
		},
	}

	got := AppendAudioTags(&Indexer{}, &http.Request{}, torrents)

	names := schema.LanguageNames(got[0].Audio)
	if len(names) != 2 || names[0] != "Portuguese (Brazil)" || names[1] != "Japanese" {
		t.Errorf("Audio = %v, want Portuguese (Brazil) and Japanese", names)
	}
	if want := "(brazilian, jpn)"; got[0].Title[len(got[0].Title)-len(want):] != want {
		t.Errorf("Title = %q, want it tagged with %q", got[0].Title, want)
	}
}

// The pruning of the page-level audio still has to work: a subbed release of a
// post announcing "Português | Inglês" must not be marked as dubbed.
func TestAppendAudioTagsDoesNotInventLanguages(t *testing.T) {
	torrents := []schema.IndexedTorrent{
		{
			Title: "Superman 2025 1080p WEB-DL Legendado",
			Audio: []schema.Audio{schema.AudioEnglish},
		},
	}

	got := AppendAudioTags(&Indexer{}, &http.Request{}, torrents)

	names := schema.LanguageNames(got[0].Audio)
	if len(names) != 1 || names[0] != "English" {
		t.Errorf("Audio = %v, want only English", names)
	}
}

func TestAppendAudioTagsReadsTheDubMarkerFromTheTitle(t *testing.T) {
	torrents := []schema.IndexedTorrent{
		{
			Title: "Some Anime S01E01 1080p WEB-DL Multi-Audio",
			Audio: []schema.Audio{schema.AudioJapanese},
		},
	}

	got := AppendAudioTags(&Indexer{}, &http.Request{}, torrents)

	names := schema.LanguageNames(got[0].Audio)
	if len(names) != 2 {
		t.Fatalf("Audio = %v, want portuguese added to japanese", names)
	}
}

func Test_addAudio(t *testing.T) {
	got := addAudio(
		[]schema.Audio{schema.AudioJapanese},
		[]schema.Audio{schema.AudioPortuguese4, schema.AudioJapanese},
		[]schema.Audio{schema.AudioPortuguese},
	)

	// synonyms collapse by ISO code, ordered by it
	want := []string{"brazilian", "jpn"}
	if len(got) != len(want) {
		t.Fatalf("addAudio() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("addAudio()[%d] = %q, want %q", i, got[i].String(), want[i])
		}
	}
}

func TestAppendAudioTagsMarksSubtitledReleases(t *testing.T) {
	tests := []struct {
		name  string
		it    schema.IndexedTorrent
		title string
	}{
		{
			name:  "known audio without portuguese",
			it:    schema.IndexedTorrent{Title: "Superman 2025 1080p WEB-DL", Audio: []schema.Audio{schema.AudioEnglish}},
			title: "Superman 2025 1080p WEB-DL (eng, legendado)",
		},
		{
			name:  "explicit marker without known audio",
			it:    schema.IndexedTorrent{Title: "Superman 2025 1080p WEB-DL Legendado"},
			title: "Superman 2025 1080p WEB-DL Legendado (legendado)",
		},
		{
			name:  "LEG marker in a video file name",
			it:    schema.IndexedTorrent{Title: "Superman 2025 1080p", Files: []schema.File{{Path: "Superman.2025.1080p.WEB-DL.LEG.mkv"}}},
			title: "Superman 2025 1080p (legendado)",
		},
		{
			name:  "dual audio is not subtitled",
			it:    schema.IndexedTorrent{Title: "Superman 2025 1080p WEB-DL DUAL", Audio: []schema.Audio{schema.AudioEnglish}},
			title: "Superman 2025 1080p WEB-DL DUAL (brazilian, eng)",
		},
		{
			name:  "portuguese audio wins over a legendado marker",
			it:    schema.IndexedTorrent{Title: "Superman 2025 Dublado e Legendado", Audio: []schema.Audio{schema.AudioPortuguese}},
			title: "Superman 2025 Dublado e Legendado (brazilian)",
		},
		{
			name:  "unknown audio and no marker stays untouched",
			it:    schema.IndexedTorrent{Title: "Superman 2025 1080p WEB-DL"},
			title: "Superman 2025 1080p WEB-DL",
		},
		{
			name:  "leg inside a word is not a marker",
			it:    schema.IndexedTorrent{Title: "Legacy of Kain 2025 Legend"},
			title: "Legacy of Kain 2025 Legend",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AppendAudioTags(&Indexer{}, &http.Request{}, []schema.IndexedTorrent{tt.it})
			if got[0].Title != tt.title {
				t.Errorf("Title = %q, want %q", got[0].Title, tt.title)
			}
		})
	}
}
