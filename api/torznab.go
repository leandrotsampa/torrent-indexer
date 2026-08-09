package handler

// Torznab feed for Sonarr/Radarr.
//
// Sonarr and Radarr read the release languages from the
// <torznab:attr name="language"> attributes and map them, by name, through their
// own language lists. The JSON path cannot carry that: the Cardigann parser used
// by the Prowlarr YML definitions has no "languages" field (Prowlarr#2550), so
// the audio metadata this indexer already knows dies in the translation.
//
// Speaking Torznab directly fixes it. Point Prowlarr's "Generic Torznab" indexer
// at http://torrent-indexer:7006/torznab/<indexer> and it forwards the language
// attributes to Sonarr/Radarr untouched — or add the URL straight into
// Sonarr/Radarr as a Torznab indexer, skipping Prowlarr altogether.

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/leandrotsampa/torrent-indexer/consts"
	"github.com/leandrotsampa/torrent-indexer/logging"
	"github.com/leandrotsampa/torrent-indexer/schema"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

const (
	// TorznabPathPrefix is the route prefix serving the Torznab feeds, one
	// indexer per path segment (/torznab/bludv). Prowlarr appends its configured
	// API path ("/api") to the base URL, which is accepted as well.
	TorznabPathPrefix = "/torznab/"

	torznabDefaultLimit = 100
	torznabMaxLimit     = 300
)

// Newznab standard categories understood by Sonarr/Radarr.
const (
	torznabCatMovies    = 2000
	torznabCatMoviesSD  = 2030
	torznabCatMoviesHD  = 2040
	torznabCatMoviesUHD = 2045
	torznabCatTV        = 5000
	torznabCatTVSD      = 5030
	torznabCatTVHD      = 5040
	torznabCatTVUHD     = 5045
	torznabCatTVAnime   = 5070
)

// Torznab error codes, as defined by the spec.
const (
	torznabErrMissingParameter = 200
	torznabErrNoSuchFunction   = 202
	torznabErrUnknownError     = 900
)

// torznabIndexer exposes one of the indexers over Torznab.
type torznabIndexer struct {
	ID          string
	Name        string
	Description string
	Handler     http.HandlerFunc
	// Anime indexers publish under TV/Anime and index whole animes per page, so
	// they take the query without season/episode markers.
	Anime bool
}

// torznabIndexers lists the indexers reachable over Torznab, keyed by the path
// segment — the same names used by the /indexers/<name> JSON routes.
func (i *Indexer) torznabIndexers() map[string]torznabIndexer {
	return map[string]torznabIndexer{
		"bludv": {
			ID:          "bludv",
			Name:        "BluDV (torrent-indexer)",
			Description: "BluDV — filmes e séries via torrent, servido pelo torrent-indexer",
			Handler:     i.HandlerBluDVIndexer,
		},
		"comando_torrents": {
			ID:          "comando_torrents",
			Name:        "Comando Torrents (torrent-indexer)",
			Description: "Comando Torrents — filmes e séries via torrent, servido pelo torrent-indexer",
			Handler:     i.HandlerComandoIndexer,
		},
		"rede_torrent": {
			ID:          "rede_torrent",
			Name:        "Rede Torrent (torrent-indexer)",
			Description: "Rede Torrent — filmes e séries via torrent, servido pelo torrent-indexer",
			Handler:     i.HandlerRedeTorrentIndexer,
		},
		"starck-filmes": {
			ID:          "starck-filmes",
			Name:        "Starck Filmes (torrent-indexer)",
			Description: "Starck Filmes — filmes e séries via torrent, servido pelo torrent-indexer",
			Handler:     i.HandlerStarckFilmesIndexer,
		},
		"torrent-dos-filmes": {
			ID:          "torrent-dos-filmes",
			Name:        "Torrent dos Filmes (torrent-indexer)",
			Description: "Torrent dos Filmes — filmes e séries via torrent, servido pelo torrent-indexer",
			Handler:     i.HandlerTorrentDosFilmesIndexer,
		},
		"vaca_torrent": {
			ID:          "vaca_torrent",
			Name:        "Vaca Torrent (torrent-indexer)",
			Description: "Vaca Torrent — filmes e séries via torrent, servido pelo torrent-indexer",
			Handler:     i.HandlerVacaTorrentIndexer,
		},
		"darkmahou": {
			ID:          "darkmahou",
			Name:        "Darkmahou (torrent-indexer)",
			Description: "Dark Animes (darkmahou.io) — animes via torrent, servido pelo torrent-indexer",
			Handler:     i.HandlerDarkmahouIndexer,
			Anime:       true,
		},
		"erai_raws": {
			ID:          "erai_raws",
			Name:        "Erai-raws (torrent-indexer)",
			Description: "Erai-raws — animes via torrent, servido pelo torrent-indexer",
			Handler:     i.HandlerEraiRawsIndexer,
			Anime:       true,
		},
	}
}

// HandlerTorznab serves the Torznab endpoints of a single indexer:
// /torznab/<indexer> (and /torznab/<indexer>/api, the path Prowlarr appends).
func (i *Indexer) HandlerTorznab(w http.ResponseWriter, r *http.Request) {
	name := torznabIndexerName(r.URL.Path)
	indexers := i.torznabIndexers()

	def, ok := indexers[name]
	if !ok {
		writeTorznabError(w, r, http.StatusNotFound, torznabErrMissingParameter,
			fmt.Sprintf("Unknown indexer %q. Available: %s", name, strings.Join(sortedKeys(indexers), ", ")))
		return
	}

	switch strings.ToLower(r.URL.Query().Get("t")) {
	case "caps":
		writeTorznabCaps(w, r, def)
	case "", "search", "tvsearch", "tv-search", "movie", "movie-search":
		i.torznabSearch(w, r, def)
	default:
		writeTorznabError(w, r, http.StatusOK, torznabErrNoSuchFunction, "No such function")
	}
}

// torznabIndexerName extracts the indexer name from the request path, tolerating
// the "/api" suffix Prowlarr appends to the configured base URL.
func torznabIndexerName(path string) string {
	name := strings.TrimPrefix(path, TorznabPathPrefix)
	name = strings.Trim(name, "/")
	name = strings.TrimSuffix(name, "/api")

	if idx := strings.Index(name, "/"); idx >= 0 {
		name = name[:idx]
	}

	return name
}

func sortedKeys(m map[string]torznabIndexer) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// map iteration order is random; keep the error message stable
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	return keys
}

// torznabSearch runs the indexer and renders its results as a Torznab feed.
func (i *Indexer) torznabSearch(w http.ResponseWriter, r *http.Request, def torznabIndexer) {
	query := r.URL.Query()

	q := strings.TrimSpace(query.Get("q"))
	if q == "" {
		// Radarr searches by IMDB ID when it has one; the indexers take it
		// through the same "q" parameter.
		if imdbID := strings.TrimSpace(query.Get("imdbid")); imdbID != "" {
			q = imdbID
		}
	}

	keywords := torznabKeywords(def, q, query.Get("season"), query.Get("ep"))

	response, err := i.runIndexer(r, def, keywords, torznabLimit(query.Get("limit")))
	if err != nil {
		logging.ErrorWithRequest(r).Err(err).Str("indexer", def.ID).Msg("Failed to run indexer for torznab feed")
		writeTorznabError(w, r, http.StatusOK, torznabErrUnknownError, err.Error())
		return
	}

	writeTorznabFeed(w, r, def, response.Results)
}

// runIndexer calls the JSON handler of the indexer in-process and decodes its
// response, so the Torznab layer is only a different output format over the very
// same search path (cache, post-processors and filters included).
func (i *Indexer) runIndexer(r *http.Request, def torznabIndexer, keywords string, limit int) (Response, error) {
	params := url.Values{}
	if keywords != "" {
		params.Set("q", keywords)
		// Same as the Cardigann definitions: drop the results that have nothing
		// to do with the query.
		params.Set("filter_results", "true")
	}
	params.Set("limit", strconv.Itoa(limit))

	// Forward the filters the JSON API already supports, so they stay usable
	// from the Torznab URL as well.
	for _, name := range []string{"audio", "year", "imdb", "page", "sortBy", "sortDirection"} {
		if value := strings.TrimSpace(r.URL.Query().Get(name)); value != "" {
			params.Set(name, value)
		}
	}

	inner := r.Clone(r.Context())
	inner.Method = http.MethodGet
	inner.URL = &url.URL{Path: "/indexers/" + def.ID, RawQuery: params.Encode()}
	inner.RequestURI = inner.URL.RequestURI()
	inner.Body = http.NoBody

	recorder := newResponseRecorder()
	def.Handler(recorder, inner)

	if recorder.status >= http.StatusBadRequest {
		return Response{}, fmt.Errorf("indexer %s failed with status %d: %s", def.ID, recorder.status, strings.TrimSpace(recorder.body.String()))
	}

	var response Response
	if err := json.Unmarshal(recorder.body.Bytes(), &response); err != nil {
		return Response{}, fmt.Errorf("failed to decode %s response: %w", def.ID, err)
	}

	return response, nil
}

// responseRecorder captures the response of an indexer handler invoked
// in-process by the Torznab layer.
type responseRecorder struct {
	headers http.Header
	status  int
	body    bytes.Buffer
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{headers: http.Header{}, status: http.StatusOK}
}

func (rec *responseRecorder) Header() http.Header { return rec.headers }

func (rec *responseRecorder) Write(b []byte) (int, error) { return rec.body.Write(b) }

func (rec *responseRecorder) WriteHeader(status int) { rec.status = status }

func torznabLimit(raw string) int {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return torznabDefaultLimit
	}
	if limit > torznabMaxLimit {
		return torznabMaxLimit
	}

	return limit
}

var (
	// torznabConjunctionRE mirrors the "and|es|y|et -> e" keywords filter of the
	// Cardigann definitions, which is what makes Sonarr's "Rick and Morty" match
	// the localized "Rick e Morty" of the Brazilian sites.
	torznabConjunctionRE = regexp.MustCompile(`(?i)\b(and|es|y|et)\b`)
	// The Brazilian sites index a whole season per post and title it
	// "Temporada N", never SxxEyy.
	torznabSeasonEpisodeRE = regexp.MustCompile(`(?i)\bs0*(\d{1,3})[\s._-]*e0*(\d{1,3})\b`)
	torznabSeasonOnlyRE    = regexp.MustCompile(`(?i)\bs0*(\d{1,3})$`)
	// torznabSeasonMarkersRE strips the season/episode markers for the anime
	// indexers, whose pages list every episode of the anime.
	torznabSeasonMarkersRE = regexp.MustCompile(`(?i)\bs\d{1,4}(e\d{1,4})?\b|\b(temporadas?|epis[oó]dios?|seasons?|episodes?)\s*\d+\b`)

	torznabTVTitleRE    = regexp.MustCompile(`(?i)\bs\d{1,3}(e\d{1,3})?\b|\btemporada\b|\bepis.dio`)
	torznabAnimeMovieRE = regexp.MustCompile(`(?i)\b(BDRip Completo|Movie|Filme|1080p BD)\b`)
	torznabUHDRE        = regexp.MustCompile(`(?i)\b(2160p|4k)\b`)
	torznabHDRE         = regexp.MustCompile(`(?i)\b(720p|1080p)\b`)
	torznabSDRE         = regexp.MustCompile(`(?i)\b(480p|360p|dvdrip|sdtv)\b`)
)

// torznabKeywords translates the Torznab query into what the indexer expects,
// replicating the keywordsfilters of the Cardigann definitions — Prowlarr does
// not apply them to a Generic Torznab indexer.
func torznabKeywords(def torznabIndexer, q, season, ep string) string {
	if def.Anime {
		// The anime pages carry every episode, so the markers would only narrow
		// the search wrongly; Sonarr picks the right episode from the title.
		return normalizeSpaces(torznabSeasonMarkersRE.ReplaceAllString(q, " "))
	}

	keywords := strings.ToLower(strings.TrimSpace(q))
	if season != "" {
		// The episode is left out on purpose: the posts are per season, and
		// Sonarr filters the episode from the release title.
		keywords = strings.TrimSpace(fmt.Sprintf("%s temporada %s", keywords, strings.TrimLeft(season, "0")))
	}

	keywords = torznabConjunctionRE.ReplaceAllString(keywords, "e")
	keywords = torznabSeasonEpisodeRE.ReplaceAllString(keywords, "temporada $1")
	keywords = torznabSeasonOnlyRE.ReplaceAllString(keywords, "temporada $1")

	return normalizeSpaces(keywords)
}

func normalizeSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// torznabCategories maps a result to the newznab categories Sonarr/Radarr filter
// on: the generic one plus the resolution-specific subcategory.
func torznabCategories(def torznabIndexer, it schema.IndexedTorrent) []int {
	title := fmt.Sprintf("%s %s", it.Title, it.OriginalTitle)

	if def.Anime {
		if torznabAnimeMovieRE.MatchString(title) {
			return append([]int{torznabCatMovies}, torznabResolutionCategory(title, false)...)
		}

		return []int{torznabCatTV, torznabCatTVAnime}
	}

	if torznabTVTitleRE.MatchString(title) {
		return append([]int{torznabCatTV}, torznabResolutionCategory(title, true)...)
	}

	return append([]int{torznabCatMovies}, torznabResolutionCategory(title, false)...)
}

func torznabResolutionCategory(title string, tv bool) []int {
	switch {
	case torznabUHDRE.MatchString(title):
		if tv {
			return []int{torznabCatTVUHD}
		}
		return []int{torznabCatMoviesUHD}
	case torznabHDRE.MatchString(title):
		if tv {
			return []int{torznabCatTVHD}
		}
		return []int{torznabCatMoviesHD}
	case torznabSDRE.MatchString(title):
		if tv {
			return []int{torznabCatTVSD}
		}
		return []int{torznabCatMoviesSD}
	default:
		return nil
	}
}

var imdbIDRE = regexp.MustCompile(`tt(\d+)`)

// writeTorznabFeed renders the results as a Torznab RSS feed.
func writeTorznabFeed(w http.ResponseWriter, r *http.Request, def torznabIndexer, results []schema.IndexedTorrent) {
	items := make([]torznabItem, 0, len(results))
	for _, it := range results {
		items = append(items, newTorznabItem(def, it))
	}

	feed := torznabFeed{
		Version:   "2.0",
		TorznabNS: "http://torznab.com/schemas/2015/feed",
		Channel: torznabChannel{
			Title:       def.Name,
			Description: def.Description,
			Link:        torznabFeedLink(r),
			Language:    "pt-BR",
			Response: torznabResponse{
				XMLName: xml.Name{Local: "torznab:response"},
				Offset:  0,
				Total:   len(results),
			},
			Items: items,
		},
	}

	writeXML(w, r, http.StatusOK, feed)
}

func newTorznabItem(def torznabIndexer, it schema.IndexedTorrent) torznabItem {
	size := utils.ParseSize(it.Size)
	categories := torznabCategories(def, it)

	attrs := make([]torznabAttr, 0, 16)
	for _, category := range categories {
		attrs = append(attrs, newTorznabAttr("category", strconv.Itoa(category)))
	}
	attrs = append(attrs,
		newTorznabAttr("seeders", strconv.Itoa(it.SeedCount)),
		newTorznabAttr("leechers", strconv.Itoa(it.LeechCount)),
		newTorznabAttr("peers", strconv.Itoa(it.SeedCount+it.LeechCount)),
		newTorznabAttr("size", strconv.FormatInt(size, 10)),
		// Public trackers: nothing counts against a ratio.
		newTorznabAttr("downloadvolumefactor", "1"),
		newTorznabAttr("uploadvolumefactor", "1"),
	)

	if it.InfoHash != "" {
		attrs = append(attrs, newTorznabAttr("infohash", it.InfoHash))
	}
	if it.MagnetLink != "" {
		attrs = append(attrs, newTorznabAttr("magneturl", it.MagnetLink))
	}
	if len(it.Files) > 0 {
		attrs = append(attrs, newTorznabAttr("files", strconv.Itoa(len(it.Files))))
	}
	if matches := imdbIDRE.FindStringSubmatch(it.IMDB); len(matches) > 1 {
		// Sonarr/Radarr read the bare number from "imdb" and the tt-prefixed id
		// from "imdbid"; emit both so either parser finds it.
		attrs = append(attrs,
			newTorznabAttr("imdb", matches[1]),
			newTorznabAttr("imdbid", matches[0]),
		)
	}
	if it.Year != "" {
		attrs = append(attrs, newTorznabAttr("year", it.Year))
	}

	// The whole point of this feed: hand Sonarr/Radarr the languages by name, so
	// their language filters and custom formats can act on them.
	languages := schema.LanguageNames(it.Audio)
	for _, language := range languages {
		attrs = append(attrs, newTorznabAttr("language", language))
	}

	guid := it.InfoHash
	if guid == "" {
		guid = it.MagnetLink
	}

	item := torznabItem{
		Title:       it.Title,
		GUID:        guid,
		Link:        it.MagnetLink,
		Comments:    it.Details,
		Description: it.OriginalTitle,
		Categories:  categories,
		Size:        size,
		// Both parsers fall back to a comma separated <language> element when a
		// feed carries no language attribute, so keep it as a safety net for the
		// readers that only speak plain newznab.
		Language: strings.Join(languages, ", "),
		Attrs:    attrs,
	}

	if !it.Date.IsZero() {
		item.PubDate = it.Date.UTC().Format(torznabDateLayout)
	}
	if it.MagnetLink != "" {
		item.Enclosure = &torznabEnclosure{
			URL:    it.MagnetLink,
			Length: size,
			Type:   "application/x-bittorrent",
		}
	}

	return item
}

// torznabDateLayout is the RFC 1123 date format RSS readers expect.
const torznabDateLayout = "Mon, 02 Jan 2006 15:04:05 -0700"

func torznabFeedLink(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}

	return fmt.Sprintf("%s://%s%s", scheme, r.Host, r.URL.Path)
}

// writeTorznabCaps advertises the search modes and categories of the indexer.
func writeTorznabCaps(w http.ResponseWriter, r *http.Request, def torznabIndexer) {
	categories := []torznabCapsCategory{
		{
			ID:   torznabCatMovies,
			Name: "Movies",
			Subcategories: []torznabCapsCategory{
				{ID: torznabCatMoviesSD, Name: "Movies/SD"},
				{ID: torznabCatMoviesHD, Name: "Movies/HD"},
				{ID: torznabCatMoviesUHD, Name: "Movies/UHD"},
			},
		},
		{
			ID:   torznabCatTV,
			Name: "TV",
			Subcategories: []torznabCapsCategory{
				{ID: torznabCatTVSD, Name: "TV/SD"},
				{ID: torznabCatTVHD, Name: "TV/HD"},
				{ID: torznabCatTVUHD, Name: "TV/UHD"},
				{ID: torznabCatTVAnime, Name: "TV/Anime"},
			},
		},
	}

	caps := torznabCaps{
		Server: torznabCapsServer{
			Version:   consts.GetBuildInfo()["version"],
			Title:     def.Name,
			Email:     "",
			URL:       torznabFeedLink(r),
			Image:     "",
			Type:      "public",
			Strapline: def.Description,
		},
		Limits: torznabCapsLimits{Max: torznabMaxLimit, Default: torznabDefaultLimit},
		Searching: torznabCapsSearching{
			Search:      torznabCapsSearch{Available: "yes", SupportedParams: "q"},
			TVSearch:    torznabCapsSearch{Available: "yes", SupportedParams: "q,season,ep"},
			MovieSearch: torznabCapsSearch{Available: "yes", SupportedParams: "q,imdbid"},
			MusicSearch: torznabCapsSearch{Available: "no", SupportedParams: "q"},
			AudioSearch: torznabCapsSearch{Available: "no", SupportedParams: "q"},
			BookSearch:  torznabCapsSearch{Available: "no", SupportedParams: "q"},
		},
		Categories: torznabCapsCategories{Categories: categories},
	}

	writeXML(w, r, http.StatusOK, caps)
}

func writeTorznabError(w http.ResponseWriter, r *http.Request, status, code int, description string) {
	writeXML(w, r, status, torznabError{Code: code, Description: description})
}

func writeXML(w http.ResponseWriter, r *http.Request, status int, payload any) {
	body, err := xml.MarshalIndent(payload, "", "  ")
	if err != nil {
		logging.ErrorWithRequest(r).Err(err).Msg("Failed to encode XML response")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(status)

	if _, err := w.Write([]byte(xml.Header)); err != nil {
		logging.ErrorWithRequest(r).Err(err).Msg("Failed to write XML header")
		return
	}
	if _, err := w.Write(body); err != nil {
		logging.ErrorWithRequest(r).Err(err).Msg("Failed to write XML response")
	}
}

// Torznab XML documents. The torznab: prefixed names are set through XMLName so
// encoding/xml emits them verbatim instead of treating the prefix as a namespace
// URL.

type torznabFeed struct {
	XMLName   xml.Name       `xml:"rss"`
	Version   string         `xml:"version,attr"`
	TorznabNS string         `xml:"xmlns:torznab,attr"`
	Channel   torznabChannel `xml:"channel"`
}

type torznabChannel struct {
	Title       string          `xml:"title"`
	Description string          `xml:"description,omitempty"`
	Link        string          `xml:"link,omitempty"`
	Language    string          `xml:"language,omitempty"`
	Response    torznabResponse `xml:""`
	Items       []torznabItem   `xml:"item"`
}

type torznabResponse struct {
	XMLName xml.Name
	Offset  int `xml:"offset,attr"`
	Total   int `xml:"total,attr"`
}

type torznabItem struct {
	XMLName     xml.Name          `xml:"item"`
	Title       string            `xml:"title"`
	GUID        string            `xml:"guid"`
	Link        string            `xml:"link,omitempty"`
	Comments    string            `xml:"comments,omitempty"`
	PubDate     string            `xml:"pubDate,omitempty"`
	Description string            `xml:"description,omitempty"`
	Categories  []int             `xml:"category"`
	Language    string            `xml:"language,omitempty"`
	Size        int64             `xml:"size,omitempty"`
	Enclosure   *torznabEnclosure `xml:"enclosure,omitempty"`
	Attrs       []torznabAttr
}

type torznabEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type torznabAttr struct {
	XMLName xml.Name
	Name    string `xml:"name,attr"`
	Value   string `xml:"value,attr"`
}

func newTorznabAttr(name, value string) torznabAttr {
	return torznabAttr{XMLName: xml.Name{Local: "torznab:attr"}, Name: name, Value: value}
}

type torznabCaps struct {
	XMLName    xml.Name              `xml:"caps"`
	Server     torznabCapsServer     `xml:"server"`
	Limits     torznabCapsLimits     `xml:"limits"`
	Searching  torznabCapsSearching  `xml:"searching"`
	Categories torznabCapsCategories `xml:"categories"`
}

type torznabCapsServer struct {
	Version   string `xml:"version,attr,omitempty"`
	Title     string `xml:"title,attr,omitempty"`
	Email     string `xml:"email,attr,omitempty"`
	URL       string `xml:"url,attr,omitempty"`
	Image     string `xml:"image,attr,omitempty"`
	Type      string `xml:"type,attr,omitempty"`
	Strapline string `xml:"strapline,attr,omitempty"`
}

type torznabCapsLimits struct {
	Max     int `xml:"max,attr"`
	Default int `xml:"default,attr"`
}

type torznabCapsSearching struct {
	Search      torznabCapsSearch `xml:"search"`
	TVSearch    torznabCapsSearch `xml:"tv-search"`
	MovieSearch torznabCapsSearch `xml:"movie-search"`
	MusicSearch torznabCapsSearch `xml:"music-search"`
	AudioSearch torznabCapsSearch `xml:"audio-search"`
	BookSearch  torznabCapsSearch `xml:"book-search"`
}

type torznabCapsSearch struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

type torznabCapsCategories struct {
	Categories []torznabCapsCategory `xml:"category"`
}

type torznabCapsCategory struct {
	ID            int                   `xml:"id,attr"`
	Name          string                `xml:"name,attr"`
	Subcategories []torznabCapsCategory `xml:"subcat,omitempty"`
}

type torznabError struct {
	XMLName     xml.Name `xml:"error"`
	Code        int      `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}
