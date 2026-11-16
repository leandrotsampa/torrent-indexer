package handler

import (
	"html"
	"regexp"
	"sort"
	"strings"

	"github.com/leandrotsampa/torrent-indexer/schema"
)

// nyaaLanguages holds the audio and subtitle languages stated in a release
// description.
type nyaaLanguages struct {
	Audio     []schema.Audio
	Subtitles []schema.Audio
}

// nyaaLanguagePatterns maps the ways uploaders write each language — English
// and Portuguese names, ISO codes, Crunchyroll locales — to schema.Audio.
// Cantonese comes before Chinese so "Chinese — Cantonese" keeps both.
var nyaaLanguagePatterns = []struct {
	re    *regexp.Regexp
	audio schema.Audio
}{
	{regexp.MustCompile(`(?i)portugu[eê]s[ea]?|brazilian|brasileir[oa]|\bpt[\s-]?\(?br\b\)?|\bptbr\b|\bpor\b`), schema.AudioPortuguese},
	{regexp.MustCompile(`(?i)japanese|japon[eê]s|\bjpn\b|\bjap\b`), schema.AudioJapanese},
	{regexp.MustCompile(`(?i)english|ingl[eê]s|\beng\b`), schema.AudioEnglish},
	{regexp.MustCompile(`(?i)spanish|espanhol|castilian|\bspa\b|\bes\s*\((?:la|eu)\)`), schema.AudioSpanish},
	{regexp.MustCompile(`(?i)french|franc[eê]s|\bfre\b|\bfra\b`), schema.AudioFrench},
	{regexp.MustCompile(`(?i)german|alem[aã]o|\bger\b|\bdeu\b`), schema.AudioGerman},
	{regexp.MustCompile(`(?i)italian|italiano|\bita\b`), schema.AudioItalian},
	{regexp.MustCompile(`(?i)russian|\brusso\b|\brus\b`), schema.AudioRussian},
	{regexp.MustCompile(`(?i)arabic|[áa]rabe\b|\bara\b`), schema.AudioArabic},
	{regexp.MustCompile(`(?i)cantonese|canton[eê]s`), schema.AudioCantonese},
	{regexp.MustCompile(`(?i)chinese|mandarin|chin[eê]s|\bchi\b|\bzho\b|\bzh(?:-[a-z]+)?\b`), schema.AudioMandarin},
	{regexp.MustCompile(`(?i)korean|coreano|\bkor\b`), schema.AudioKorean},
	{regexp.MustCompile(`(?i)\bthai\b|tailand[eê]s|\btha\b`), schema.AudioThai},
	{regexp.MustCompile(`(?i)polish|polon[eê]s|\bpol\b`), schema.AudioPolish},
	{regexp.MustCompile(`(?i)\bhindi\b`), schema.AudioHindi},
	{regexp.MustCompile(`(?i)hungarian|h[úu]ngaro`), schema.AudioHungarian},
	{regexp.MustCompile(`(?i)indonesian|indon[ée]sio`), schema.AudioIndonesian},
	{regexp.MustCompile(`(?i)\bmalay\b|malaio`), schema.AudioMalay},
	{regexp.MustCompile(`(?i)vietnamese|vietnamita`), schema.AudioVietnamese},
	{regexp.MustCompile(`(?i)filipino|tagalog`), schema.AudioFilipino},
	{regexp.MustCompile(`(?i)turkish|\bturco\b`), schema.AudioTurkish},
	{regexp.MustCompile(`(?i)\bdutch\b|holand[eê]s`), schema.AudioDutch},
}

var (
	// nyaaLabelRE splits a "Label: value" line, e.g. "Audio: Japanese",
	// "Audios (12): Japanese, ..." or "Subtitles Info:" (value on next lines).
	nyaaLabelRE = regexp.MustCompile(`^[\s\-*+•>]*([A-Za-zÀ-ÿ][A-Za-zÀ-ÿ /]{0,30}?)\s*(?:\(\d+\))?\s*:\s*(.*)$`)
	// nyaaMarkdownLinkRE keeps only the text of [text](url) links.
	nyaaMarkdownLinkRE = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// nyaaSeparatorCellRE matches the "---"/":---:" cells of a markdown table.
	nyaaSeparatorCellRE = regexp.MustCompile(`^:?-{3,}:?$`)

	nyaaHTMLRowRE      = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	nyaaHTMLCellRE     = regexp.MustCompile(`(?is)<t[dh]\b[^>]*>(.*?)</t[dh]>`)
	nyaaHTMLBreakRE    = regexp.MustCompile(`(?i)<br\s*/?>|<hr\s*/?>|</(?:p|li|div|h[1-6]|ul|ol|table|thead|tbody|blockquote)>`)
	nyaaHTMLTagRE      = regexp.MustCompile(`(?s)<[^>]+>`)
	nyaaHTMLDetectorRE = regexp.MustCompile(`(?i)<(?:p|li|br|table|div|strong|h[1-6])\b`)

	// nyaaDualAudioRE: on Nyaa "Dual Audio" is Japanese + English.
	nyaaDualAudioRE = regexp.MustCompile(`(?i)\bdual[\s._-]?audio\b|\bdual\b`)
	// nyaaDubbedTitleRE: a Brazilian dub announced in the title.
	nyaaDubbedTitleRE = regexp.MustCompile(`(?i)\bdublad[oa]\b|\bdub(?:bed)?[\s._-]*(?:pt[\s._-]?br|portugu)`)
	// nyaaBrazilianTitleRE: the tag Brazilian fansubs put on their releases.
	nyaaBrazilianTitleRE = regexp.MustCompile(`(?i)\bpt[\s._-]?br\b|\bptbr\b|\blegendad[oa]\b`)
)

const (
	nyaaSectionAudio     = "audio"
	nyaaSectionSubtitles = "subtitles"
	// nyaaSectionLanguage is a bare "Idioma"/"Language" column: in a table that
	// also has a subtitle column (Brazilian fansubs) it names the subtitles.
	nyaaSectionLanguage = "language"
)

// parseNyaaDescription reads the audio and subtitle languages from a release
// description — nyaa.si serves it as markdown, nyaa.wiki as rendered HTML. It
// only looks inside the sections labelled as audio or subtitles, in the forms
// the uploaders use:
//
//   - **Audio:** Japanese, English                      (one line)
//     `Audios (12):` **Japanese** │ **English** │ ...     (counted)
//   - `Subtitles Info:` + one language per line         (block)
//     | Audio | Japanese, English(US) AAC |               (key/value table)
//     | Audio Language (2) | Subtitle Language (15) |     (column table)
//
// so a subtitle list never leaks into the audio.
func parseNyaaDescription(description string) nyaaLanguages {
	var audio, subtitles strings.Builder
	add := func(section, text string) {
		switch section {
		case nyaaSectionAudio:
			audio.WriteString(text + "\n")
		case nyaaSectionSubtitles:
			subtitles.WriteString(text + "\n")
		}
	}

	section := ""
	var columns map[int]string
	for _, raw := range nyaaDescriptionLines(description) {
		line := cleanNyaaLine(raw)
		if strings.TrimSpace(line) == "" {
			section, columns = "", nil
			continue
		}

		if strings.Count(line, "|") >= 1 {
			cells := splitNyaaCells(line)
			if len(cells) == 0 || isNyaaSeparatorRow(cells) {
				continue
			}
			kinds := make([]string, len(cells))
			labels := 0
			for idx, cell := range cells {
				kinds[idx] = nyaaLabelKind(cell)
				if kinds[idx] == nyaaSectionAudio || kinds[idx] == nyaaSectionSubtitles {
					labels++
				}
			}

			switch {
			case len(cells) > 1 && labels == 1 && (kinds[0] == nyaaSectionAudio || kinds[0] == nyaaSectionSubtitles):
				// key/value row: | Audio | Japanese, English |
				add(kinds[0], strings.Join(cells[1:], " "))
				section = ""
			case labels >= 1:
				// header row: | Audio Language | Subtitle Language |
				columns = nyaaColumns(kinds)
				section = ""
			case columns != nil:
				for idx, kind := range columns {
					if idx < len(cells) {
						add(kind, cells[idx])
					}
				}
			case section != "":
				// a block line that happens to use pipes:
				// **English** (CR_English) | ASS
				add(section, line)
			}
			continue
		}

		columns = nil
		if m := nyaaLabelRE.FindStringSubmatch(line); m != nil {
			section = nyaaLabelKind(m[1])
			if section == nyaaSectionLanguage {
				section = ""
			}
			add(section, m[2])
			continue
		}
		add(section, line)
	}

	return nyaaLanguages{
		Audio:     findNyaaLanguages(audio.String()),
		Subtitles: findNyaaLanguages(subtitles.String()),
	}
}

// resolveNyaaLanguages completes what the description states with the title and
// the category: the audio defaults to Japanese ("Dual Audio" adds English), an
// English-translated release (1_2) is subtitled in English, and a Brazilian
// fansub tag ("[PT-BR]") means Portuguese subtitles.
func resolveNyaaLanguages(title, category string, described nyaaLanguages) (audio, subtitles []schema.Audio) {
	audio = described.Audio
	subtitles = described.Subtitles

	if len(audio) == 0 {
		audio = findAnimeAudioFromTitle(title)
		if nyaaDubbedTitleRE.MatchString(title) {
			audio = addAudio(audio, []schema.Audio{schema.AudioPortuguese})
		}
		if len(audio) == 0 {
			audio = []schema.Audio{schema.AudioJapanese}
			if nyaaDualAudioRE.MatchString(title) {
				audio = append(audio, schema.AudioEnglish)
			}
		}
	}

	if len(subtitles) == 0 && category == nyaaCategoryEnglish {
		subtitles = []schema.Audio{schema.AudioEnglish}
	}
	if !hasPortugueseAudio(audio) && !hasPortugueseAudio(subtitles) && nyaaBrazilianTitleRE.MatchString(title) {
		subtitles = addAudio(subtitles, []schema.Audio{schema.AudioPortuguese})
	}

	return audio, subtitles
}

// nyaaDescriptionLines turns the description into lines, rendering nyaa.wiki's
// HTML back into the markdown-like shape of nyaa.si (table rows as "| a | b |").
func nyaaDescriptionLines(description string) []string {
	if nyaaHTMLDetectorRE.MatchString(description) {
		description = nyaaHTMLRowRE.ReplaceAllStringFunc(description, func(row string) string {
			var cells []string
			for _, m := range nyaaHTMLCellRE.FindAllStringSubmatch(row, -1) {
				cells = append(cells, strings.TrimSpace(nyaaHTMLTagRE.ReplaceAllString(m[1], "")))
			}
			return "\n| " + strings.Join(cells, " | ") + " |\n"
		})
		description = nyaaHTMLBreakRE.ReplaceAllString(description, "\n")
		description = nyaaHTMLTagRE.ReplaceAllString(description, "")
	}
	return strings.Split(html.UnescapeString(description), "\n")
}

// cleanNyaaLine drops the markdown decoration that would get in the way of the
// label and language matching.
func cleanNyaaLine(line string) string {
	line = nyaaMarkdownLinkRE.ReplaceAllString(line, "$1")
	line = strings.NewReplacer("*", "", "`", "", "_", " ", "&nbsp;", " ", " ", " ").Replace(line)
	return strings.TrimLeft(strings.TrimSpace(line), "#> ")
}

func splitNyaaCells(line string) []string {
	parts := strings.Split(strings.TrimSpace(line), "|")
	// "| a | b |" has empty edges; "a | b" (no outer pipes) does not
	if len(parts) > 0 && strings.TrimSpace(parts[0]) == "" {
		parts = parts[1:]
	}
	if len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	cells := make([]string, len(parts))
	for idx, part := range parts {
		cells[idx] = strings.TrimSpace(part)
	}
	return cells
}

func isNyaaSeparatorRow(cells []string) bool {
	for _, cell := range cells {
		if !nyaaSeparatorCellRE.MatchString(strings.ReplaceAll(cell, " ", "")) {
			return false
		}
	}
	return true
}

// nyaaLabelKind classifies a label ("Audio", "Audios", "Audio Info", "Áudio",
// "Subtitles", "Subtittle Language", "Legenda", "Idioma"); text carrying
// languages or too long to be a label is not one.
func nyaaLabelKind(text string) string {
	text = strings.ToLower(strings.TrimSpace(strings.TrimRight(strings.TrimSpace(text), ":")))
	if text == "" || len(text) > 40 || len(findNyaaLanguages(text)) > 0 {
		return ""
	}
	switch {
	case strings.Contains(text, "sub") || strings.Contains(text, "legend"):
		return nyaaSectionSubtitles
	case strings.Contains(text, "audio") || strings.Contains(text, "áudio"):
		return nyaaSectionAudio
	case text == "idioma" || text == "idiomas" || text == "language" || text == "languages":
		return nyaaSectionLanguage
	}
	return ""
}

// nyaaColumns maps the header cells to the section their column feeds; a bare
// language column counts as subtitles when the table has a subtitle column.
func nyaaColumns(kinds []string) map[int]string {
	hasSubtitles := false
	for _, kind := range kinds {
		hasSubtitles = hasSubtitles || kind == nyaaSectionSubtitles
	}
	columns := map[int]string{}
	for idx, kind := range kinds {
		switch {
		case kind == nyaaSectionAudio || kind == nyaaSectionSubtitles:
			columns[idx] = kind
		case kind == nyaaSectionLanguage && hasSubtitles:
			columns[idx] = nyaaSectionSubtitles
		}
	}
	return columns
}

// findNyaaLanguages returns the languages named in text, in order of appearance
// and without duplicates.
func findNyaaLanguages(text string) []schema.Audio {
	type found struct {
		at    int
		audio schema.Audio
	}
	var hits []found
	for _, pattern := range nyaaLanguagePatterns {
		if loc := pattern.re.FindStringIndex(text); loc != nil {
			hits = append(hits, found{at: loc[0], audio: pattern.audio})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].at < hits[b].at })

	languages := make([]schema.Audio, 0, len(hits))
	for _, hit := range hits {
		languages = append(languages, hit.audio)
	}
	return languages
}
