package schema

import "time"

type IndexedTorrent struct {
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Details       string  `json:"details"`
	Year          string  `json:"year"`
	IMDB          string  `json:"imdb"`
	Audio         []Audio `json:"audio"`
	// Subtitles lists the subtitle languages, when the source states them.
	Subtitles []Audio `json:"subtitles,omitempty"`
	// LanguagesFromSource marks Audio/Subtitles as read from the release page
	// itself (e.g. nyaa's description): they are final, so the title heuristics
	// of the Brazilian sites (where "Dual" means Portuguese) are not applied.
	LanguagesFromSource bool      `json:"languages_from_source,omitempty"`
	MagnetLink          string    `json:"magnet_link"`
	Date                time.Time `json:"date"`
	InfoHash            string    `json:"info_hash"`
	Trackers            []string  `json:"trackers"`
	Size                string    `json:"size"`
	Files               []File    `json:"files,omitempty"`
	LeechCount          int       `json:"leech_count"`
	SeedCount           int       `json:"seed_count"`
	Similarity          float32   `json:"similarity"`
}

type File struct {
	Path string `json:"path"`
	Size string `json:"size"`
}
