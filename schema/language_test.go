package schema

import "testing"

func TestLanguageName(t *testing.T) {
	tests := []struct {
		audio Audio
		want  string
	}{
		// Every Portuguese synonym of these Brazilian sites is pt-BR
		{AudioPortuguese, "Portuguese (Brazil)"},
		{AudioPortuguese3, "Portuguese (Brazil)"},
		{AudioPortuguese4, "Portuguese (Brazil)"},
		{AudioPortuguese5, "Portuguese (Brazil)"},
		{AudioEnglish, "English"},
		{AudioEnglish2, "English"},
		{AudioJapanese, "Japanese"},
		{AudioSpanish, "Spanish"},
		{AudioMandarin2, "Chinese"},
		// Sonarr/Radarr have no Cantonese entry
		{AudioCantonese, "Chinese"},
		{AudioFarsi3, "Persian"},
	}

	for _, tt := range tests {
		if got := tt.audio.LanguageName(); got != tt.want {
			t.Errorf("Audio(%q).LanguageName() = %q, want %q", string(tt.audio), got, tt.want)
		}
	}
}

func TestLanguageNamesDropsDuplicatesAndUnknown(t *testing.T) {
	got := LanguageNames([]Audio{AudioPortuguese4, AudioPortuguese, AudioEnglish, Audio("Klingon")})
	want := []string{"Portuguese (Brazil)", "English"}

	if len(got) != len(want) {
		t.Fatalf("LanguageNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("LanguageNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLanguageNamesCoversEveryKnownAudio(t *testing.T) {
	for _, audio := range AudioList {
		if audio.LanguageName() == "" {
			t.Errorf("Audio(%q) (tag %q) has no Sonarr/Radarr language name", string(audio), audio.String())
		}
	}
}
