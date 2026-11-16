package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leandrotsampa/torrent-indexer/schema"
)

func readNyaaFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "nyaa", name))
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	return string(b)
}

func nyaaLanguageNames(list []schema.Audio) []string {
	names := schema.LanguageNames(list)
	slices.Sort(names)
	return names
}

func hasNyaaLanguage(list []schema.Audio, want schema.Audio) bool {
	return slices.Contains(schema.LanguageNames(list), want.LanguageName())
}

var (
	nyaaJA = schema.Audio(schema.AudioJapanese)
	nyaaEN = schema.Audio(schema.AudioEnglish)
	nyaaPT = schema.Audio(schema.AudioPortuguese)
)

// The examples come from real nyaa.si pages (descriptions kept verbatim, minus
// the image lines): every uploader writes the audio and subtitle tracks its own
// way, so each one pins a format the parser must understand.
func TestResolveNyaaLanguagesFromRealPages(t *testing.T) {
	tests := []struct {
		fixture  string
		title    string
		category string
		// exact audio set when set, otherwise only the dub/sub verdict is checked
		audio []schema.Audio
		// dubbed: Portuguese audio; subtitled: Portuguese subtitles, no Portuguese audio
		dubbed, subtitled bool
	}{
		{"1821065.md", "[ToonsHub] Black Butler S04E04 1080p CR WEB-DL AAC2.0 x264 (Dual-Audio, Multi-Subs)", "1_2", []schema.Audio{nyaaEN, nyaaJA}, false, true},
		{"2163683.md", "[DKB] Black Torch - S01E12 [1080p][END][HEVC x265 10bit][Dual Audio][Multi-Subs][weekly]", "1_2", []schema.Audio{nyaaJA, nyaaEN}, false, true},
		{"2163865.md", "[Anime Time] Black Torch - S01E12 [1080p][HEVC 10bit x265][AAC][Dual-Audio] [Multi Sub] [Weekly]", "1_2", []schema.Audio{nyaaJA, nyaaEN}, false, true},
		{"2169412.md", "[Judas] Black Torch (Season 01) [1080p][HEVC x265 10bit][Dual-Audio][Multi-Subs] (Batch)", "1_2", []schema.Audio{nyaaJA, nyaaEN}, false, true},
		{"2173712.md", "Black Torch - Season 1 [Multi-Audio][Multi-Subs][AV1 10bits]", "1_3", []schema.Audio{nyaaJA, nyaaPT}, true, false},
		{"2180987.md", "Ranma 1-2 2024 S03E05 The Way the Cookie Crumbles 1080p NF WEB-DL MULTi AAC2.0 H.264-VARYG (Ranma 1/2 (2024) 3rd Season, Multi-Audio, Multi-Subs)", "1_2", nil, true, false},
		{"2182451.md", "Magic Knight Rayearth 2026 S01E03 All in the Same Boat 1080p CR WEB-DL DUAL AAC2.0 H.264-VARYG (Magic Knight Rayearth (2026), Dual-Audio, Multi-Subs)", "1_2", []schema.Audio{nyaaJA, nyaaEN}, false, true},
		{"2184657.md", "[FênixFansub] Kyouran Reijou Nia Liston - 06 [PT-BR]", "1_3", []schema.Audio{nyaaJA}, false, true},
		{"2185424.md", "[Erai-raws] Tsuihou sareta Tensei Juukishi wa Game Chishiki de Musou suru - 20 [1080p CR WEBRip HEVC AAC][MultiSub][EC573C73]", "1_2", []schema.Audio{nyaaJA}, false, true},
		{"2186685.md", "Overgeared S01E08 Yatans High Priest 1080p CR WEB-DL MULTi AAC2.0 H.264-VARYG (Tempal: Item no Chikara, Multi-Audio, Multi-Subs)", "1_2", nil, true, false},
		{"2186974.md", "[ToonsHub] PSYREN S01E04 1080p CR WEB-DL MULTi AAC2.0 H.264 (PSYЯEN, Multi-Audio, Multi-Subs)", "1_2", nil, true, false},
		// nyaa.wiki serves the same description rendered as HTML
		{"2186974.wiki.html", "[ToonsHub] PSYREN S01E04 1080p CR WEB-DL MULTi AAC2.0 H.264 (PSYЯEN, Multi-Audio, Multi-Subs)", "1_2", nil, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			audio, subtitles := resolveNyaaLanguages(tt.title, tt.category, parseNyaaDescription(readNyaaFixture(t, tt.fixture)))

			if tt.audio != nil {
				if got, want := nyaaLanguageNames(audio), nyaaLanguageNames(tt.audio); !slices.Equal(got, want) {
					t.Errorf("audio = %v, want %v", got, want)
				}
			}
			if got := hasNyaaLanguage(audio, nyaaPT); got != tt.dubbed {
				t.Errorf("portuguese audio = %v, want %v (audio %v)", got, tt.dubbed, nyaaLanguageNames(audio))
			}
			if got := !hasNyaaLanguage(audio, nyaaPT) && hasNyaaLanguage(subtitles, nyaaPT); got != tt.subtitled {
				t.Errorf("subtitled = %v, want %v (subtitles %v)", got, tt.subtitled, nyaaLanguageNames(subtitles))
			}
		})
	}
}

func TestParseNyaaDescriptionKeepsSectionsApart(t *testing.T) {
	got := parseNyaaDescription("**Audio:** Japanese\n**Subtitles:** English, Portuguese (Brazil)\n**Duration:** 24 min\nPortuguese fansub notes")
	if names := nyaaLanguageNames(got.Audio); !slices.Equal(names, []string{"Japanese"}) {
		t.Errorf("audio = %v, want only Japanese", names)
	}
	if names := nyaaLanguageNames(got.Subtitles); !slices.Equal(names, []string{"English", "Portuguese (Brazil)"}) {
		t.Errorf("subtitles = %v, want English and Portuguese (Brazil)", names)
	}
}

func TestResolveNyaaLanguagesWithoutDescription(t *testing.T) {
	tests := []struct {
		name, title, category string
		audio                 []string
		subtitles             []string
	}{
		{
			// on Nyaa "Dual-Audio" means Japanese + English, never Portuguese
			name: "dual audio", title: "[Group] Show - 01 [1080p][Dual-Audio]", category: "1_2",
			audio: []string{"English", "Japanese"}, subtitles: []string{"English"},
		},
		{
			name: "english-translated", title: "[Group] Show - 01 [1080p]", category: "1_2",
			audio: []string{"Japanese"}, subtitles: []string{"English"},
		},
		{
			name: "brazilian fansub", title: "[Fansub] Show - 01 [PT-BR]", category: "1_3",
			audio: []string{"Japanese"}, subtitles: []string{"Portuguese (Brazil)"},
		},
		{
			name: "brazilian dub", title: "[Fansub] Show - 01 [Dublado]", category: "1_3",
			audio: []string{"Portuguese (Brazil)"}, subtitles: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			audio, subtitles := resolveNyaaLanguages(tt.title, tt.category, nyaaLanguages{})
			if got := nyaaLanguageNames(audio); !slices.Equal(got, tt.audio) {
				t.Errorf("audio = %v, want %v", got, tt.audio)
			}
			if got := nyaaLanguageNames(subtitles); !slices.Equal(got, tt.subtitles) {
				t.Errorf("subtitles = %v, want %v", got, tt.subtitles)
			}
		})
	}
}

// Languages read from the release page are final: the title heuristics of the
// Brazilian sites (where "Dual" means Portuguese) must not touch them.
func TestAppendAudioTagsTrustsLanguagesFromSource(t *testing.T) {
	tests := []struct {
		name string
		it   schema.IndexedTorrent
		want string
	}{
		{
			name: "dual audio with portuguese subtitles",
			it: schema.IndexedTorrent{
				Title: "[DKB] Black Torch - S01E12 [1080p][Dual Audio][Multi-Subs]", LanguagesFromSource: true,
				Audio: []schema.Audio{nyaaJA, nyaaEN}, Subtitles: []schema.Audio{nyaaEN, nyaaPT},
			},
			want: "[DKB] Black Torch - S01E12 [1080p][Dual Audio][Multi-Subs] (jpn, eng, legendado)",
		},
		{
			name: "english subtitles only",
			it: schema.IndexedTorrent{
				Title: "[Group] Show - 01 [Dual-Audio]", LanguagesFromSource: true,
				Audio: []schema.Audio{nyaaJA, nyaaEN}, Subtitles: []schema.Audio{nyaaEN},
			},
			want: "[Group] Show - 01 [Dual-Audio] (jpn, eng)",
		},
		{
			name: "portuguese audio",
			it: schema.IndexedTorrent{
				Title: "Show S01E01 MULTi", LanguagesFromSource: true,
				Audio: []schema.Audio{nyaaJA, nyaaPT}, Subtitles: []schema.Audio{nyaaPT},
			},
			want: "Show S01E01 MULTi (jpn, brazilian)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AppendAudioTags(&Indexer{}, &http.Request{}, []schema.IndexedTorrent{tt.it})
			if got[0].Title != tt.want {
				t.Errorf("Title = %q, want %q", got[0].Title, tt.want)
			}
			if strings.Contains(got[0].Title, "brazilian") != hasNyaaLanguage(got[0].Audio, nyaaPT) {
				t.Errorf("Audio = %v does not match the title tags", nyaaLanguageNames(got[0].Audio))
			}
		})
	}
}
