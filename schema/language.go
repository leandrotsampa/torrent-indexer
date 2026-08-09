package schema

import "slices"

// Sonarr and Radarr resolve the torznab "language"/"subs" attributes by NAME:
// their parser maps each value through the app's own language list
// (IsoLanguages.FindByName, case-insensitive) and silently drops whatever it
// does not recognize. So the feed must carry "Portuguese (Brazil)", never "por"
// nor "pt-BR".
var languageNameByTag = map[string]string{
	"brazilian": "Portuguese (Brazil)",
	"eng":       "English",
	"spa":       "Spanish",
	"fra":       "French",
	"deu":       "German",
	"ita":       "Italian",
	"jpn":       "Japanese",
	"kor":       "Korean",
	"chi":       "Chinese",
	// Neither app has a Cantonese entry; Chinese is the closest they know.
	"yue": "Chinese",
	"rus": "Russian",
	"swe": "Swedish",
	"srb": "Serbian",
	"ukr": "Ukrainian",
	"pol": "Polish",
	"tha": "Thai",
	"tur": "Turkish",
	"hin": "Hindi",
	"hun": "Hungarian",
	"fas": "Persian",
	"msa": "Malay",
	"nld": "Dutch",
	"fin": "Finnish",
	"dan": "Danish",
	"nor": "Norwegian",
	"isl": "Icelandic",
	"ell": "Greek",
	"ara": "Arabic",
	"heb": "Hebrew",
	"vie": "Vietnamese",
	"ind": "Indonesian",
	"fil": "Filipino",
	"ben": "Bengali",
	"tam": "Tamil",
	"tel": "Telugu",
	"guj": "Gujarati",
	"mar": "Marathi",
	"slk": "Slovak",
	"slv": "Slovenian",
}

// LanguageName returns the language name Sonarr/Radarr understand, or an empty
// string when the language has no counterpart in their lists.
func (a Audio) LanguageName() string {
	return languageNameByTag[a.toTag()]
}

// LanguageNames maps the languages to the names Sonarr/Radarr understand,
// dropping the unknown ones and the duplicates while keeping the original order.
func LanguageNames(list []Audio) []string {
	names := make([]string, 0, len(list))
	for _, a := range list {
		name := a.LanguageName()
		if name == "" || slices.Contains(names, name) {
			continue
		}
		names = append(names, name)
	}

	return names
}
