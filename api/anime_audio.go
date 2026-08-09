package handler

import (
	"regexp"
	"slices"
	"strings"

	"github.com/leandrotsampa/torrent-indexer/schema"
)

var (
	// animeDubSuffixRE matches the audio-track suffix the anime groups append to
	// the episode number. Erai-raws marks which dub the file carries — "(JA)"
	// japanese, "(KA)" korean —, which is the only thing telling apart the
	// japanese and the korean audio of a korean production (e.g. Dogulwang).
	animeDubSuffixRE = regexp.MustCompile(`(?i)\((JA|KA|CH|EN)\)`)

	// animeSpelledDubRE matches the same information spelled out, as the other
	// groups write it ("Japanese Dub", "Korean Audio").
	animeSpelledDubRE = regexp.MustCompile(`(?i)\b(japanese|korean|chinese|mandarin|english|portuguese)[\s._-]+(?:dub(?:bed)?|audio)\b`)

	// multiAudioTitleRE matches an explicit multi-audio marker, which on these
	// releases includes Portuguese.
	//
	// The bare "MULTi" is deliberately left out: the anime groups also use
	// "MultiSub"/"Multi-Subs"/"Multiple Subtitle" for the subtitle tracks, and a
	// release can be "MULTi" for subtitles while carrying a single audio track.
	multiAudioTitleRE = regexp.MustCompile(`(?i)\bmulti[\s._-]?audios?\b`)
)

// animeDubTokens maps the audio-track markers to the language they announce.
var animeDubTokens = map[string]schema.Audio{
	"ja":         schema.AudioJapanese,
	"ka":         schema.AudioKorean,
	"ch":         schema.AudioMandarin,
	"en":         schema.AudioEnglish,
	"japanese":   schema.AudioJapanese,
	"korean":     schema.AudioKorean,
	"chinese":    schema.AudioMandarin,
	"mandarin":   schema.AudioMandarin,
	"english":    schema.AudioEnglish,
	"portuguese": schema.AudioPortuguese,
}

// findAnimeAudioFromTitle extracts the audio track an anime release announces in
// its own name, which is more reliable than the anime page: a single page lists
// every dub of the same episode. Returns nil when the name says nothing about it.
func findAnimeAudioFromTitle(releaseTitle string) []schema.Audio {
	var audio []schema.Audio

	for _, re := range []*regexp.Regexp{animeDubSuffixRE, animeSpelledDubRE} {
		for _, match := range re.FindAllStringSubmatch(releaseTitle, -1) {
			if a, ok := animeDubTokens[strings.ToLower(match[1])]; ok {
				audio = append(audio, a)
			}
		}
	}

	return audio
}

// isMultiAudioTitle reports whether the release name explicitly announces more
// than one audio track.
func isMultiAudioTitle(releaseTitle string) bool {
	return multiAudioTitleRE.MatchString(releaseTitle)
}

// addAudio merges the languages into the ones already known, ordered by their
// ISO 639-2 code and without duplicates, comparing by code so the synonyms
// ("Dublado", "Nacional") collapse into a single language.
func addAudio(known []schema.Audio, found ...[]schema.Audio) []schema.Audio {
	merged := slices.Clone(known)
	for _, list := range found {
		merged = append(merged, list...)
	}

	slices.SortFunc(merged, func(a, b schema.Audio) int {
		return strings.Compare(a.String(), b.String())
	})

	return slices.CompactFunc(merged, func(a, b schema.Audio) bool {
		return a.String() == b.String()
	})
}
