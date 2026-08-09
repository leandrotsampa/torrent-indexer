package handler

import (
	"testing"

	"github.com/leandrotsampa/torrent-indexer/schema"
)

func Test_findAnimeAudioFromTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  []string // ISO 639-2 tags
	}{
		{
			name:  "japanese dub suffix",
			title: "[Erai-raws] Dogulwang - 02 (JA) [1080p CR WEB-DL AVC AAC][MultiSub][B2E2F2EF]",
			want:  []string{"jpn"},
		},
		{
			name:  "korean dub suffix",
			title: "[Erai-raws] Dogulwang - 02 (KA) [1080p CR WEB-DL AVC AAC][MultiSub][E6C1AC49].mkv",
			want:  []string{"kor"},
		},
		{
			name:  "spelled out dub",
			title: "[ToonsHub] Tomb Raider King S01E01 1080p CR WEB-DL AAC2.0 H.264 (Dogul Wang, Multi-Subs, Japanese Dub)",
			want:  []string{"jpn"},
		},
		{
			name:  "subtitle markers say nothing about the audio",
			title: "[Erai-raws] Sakamoto Days - 05 [720p][Multiple Subtitle]",
			want:  nil,
		},
		{
			name:  "MultiSub alone says nothing about the audio",
			title: "[DKB] Hanaori-san - S01E04 [1080p][HEVC x265 10bit][MultiSub]",
			want:  nil,
		},
		{
			name:  "plain release",
			title: "[Judas] Hanaori-san - S01E01.mkv",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findAnimeAudioFromTitle(tt.title)
			if len(got) != len(tt.want) {
				t.Fatalf("findAnimeAudioFromTitle() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i].String() != tt.want[i] {
					t.Errorf("findAnimeAudioFromTitle()[%d] = %q, want %q", i, got[i].String(), tt.want[i])
				}
			}
		})
	}
}

func Test_isMultiAudioTitle(t *testing.T) {
	multiAudio := []string{
		"[ToonsHub] Tomb Raider King S01E01 1080p CR WEB-DL MULTi AAC2.0 H.264 (Multi-Audio, Multi-Subs)",
		"Some Anime S01E01 1080p Multi Audio",
		"Some.Anime.S01E01.1080p.Multi.Audio.WEB-DL",
		"Some Anime S01E01 Multi-Audios",
	}
	for _, title := range multiAudio {
		if !isMultiAudioTitle(title) {
			t.Errorf("isMultiAudioTitle(%q) = false, want true", title)
		}
	}

	// "MULTi" on its own is ambiguous: the groups also use it for subtitles
	subtitlesOnly := []string{
		"[Erai-raws] Dogulwang - 02 (JA) [1080p CR WEB-DL AVC AAC][MultiSub][B2E2F2EF]",
		"[Erai-raws] Sakamoto Days - 05 [720p][Multiple Subtitle]",
		"[ToonsHub] Hanaori-san S01E01 1080p CR WEB-DL MULTi AAC2.0 H.264",
		"Tomb Raider King S01E01 1080p CR WEB-DL (Dogul Wang, Multi-Subs)",
	}
	for _, title := range subtitlesOnly {
		if isMultiAudioTitle(title) {
			t.Errorf("isMultiAudioTitle(%q) = true, want false", title)
		}
	}
}

func Test_getAudioFromTitleWithAnimeMarkers(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		content []schema.Audio
		want    []string // ISO 639-2 tags, sorted as the function returns them
	}{
		{
			name:    "korean audio stays korean",
			title:   "[Erai-raws] Dogulwang - 02 (KA) [1080p][MultiSub]",
			content: []schema.Audio{schema.AudioKorean},
			want:    []string{"kor"},
		},
		{
			name:    "multi-audio adds portuguese to the original track",
			title:   "[ToonsHub] Tomb Raider King S01E01 1080p (Multi-Audio, Multi-Subs)",
			content: []schema.Audio{schema.AudioJapanese},
			want:    []string{"brazilian", "jpn"},
		},
		{
			name:    "multisub does not add portuguese",
			title:   "[DKB] Hanaori-san - S01E04 [1080p][MultiSub]",
			content: []schema.Audio{schema.AudioJapanese},
			want:    []string{"jpn"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getAudioFromTitle(tt.title, tt.content)
			if len(got) != len(tt.want) {
				t.Fatalf("getAudioFromTitle() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i].String() != tt.want[i] {
					t.Errorf("getAudioFromTitle()[%d] = %q, want %q", i, got[i].String(), tt.want[i])
				}
			}
		})
	}
}
