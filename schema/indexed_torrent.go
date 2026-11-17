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
	LanguagesFromSource bool   `json:"languages_from_source,omitempty"`
	MagnetLink          string `json:"magnet_link"`
	// DownloadLink is where a client fetches the release when the magnet is
	// not known at search time: an endpoint of this service that resolves it
	// on demand and redirects to the magnet (see vaca_torrent).
	DownloadLink string `json:"download_link,omitempty"`
	// PeersUnknown marks SeedCount/LeechCount as not measured (no info hash
	// yet), so feeds leave them out instead of reporting zero seeders.
	PeersUnknown bool      `json:"peers_unknown,omitempty"`
	Date         time.Time `json:"date"`
	InfoHash     string    `json:"info_hash"`
	Trackers     []string  `json:"trackers"`
	Size         string    `json:"size"`
	Files        []File    `json:"files,omitempty"`
	LeechCount   int       `json:"leech_count"`
	SeedCount    int       `json:"seed_count"`
	Similarity   float32   `json:"similarity"`
}

type File struct {
	Path string `json:"path"`
	Size string `json:"size"`
}
