package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/leandrotsampa/torrent-indexer/logging"
	"github.com/leandrotsampa/torrent-indexer/magnet"
	"github.com/leandrotsampa/torrent-indexer/schema"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

// nyaa indexes the anime category of Nyaa. The audio and subtitle languages
// come from each release page (see parseNyaaDescription): on Nyaa "Dual Audio"
// means Japanese + English, so the title alone cannot tell dubbed from
// subtitled for a Brazilian user.
var nyaa = IndexerMeta{
	Label: "nyaa",
	URL:   utils.GetIndexerURLFromEnv("INDEXER_NYAA_URL", "https://nyaa.si/"),
}

const (
	nyaaCategoryAnime      = "1_0"
	nyaaCategoryEnglish    = "1_2"
	nyaaCategoryNonEnglish = "1_3"
	nyaaCategoryRaw        = "1_4"

	// nyaaDefaultMirrors are tried, in order, after INDEXER_NYAA_URL fails.
	// nyaa.mom redirects to nyaa.wiki, a mirror of the same database.
	nyaaDefaultMirrors = "https://nyaa.wiki/"

	// A release page does not change after upload, so the languages read from
	// it are kept for long.
	nyaaLanguagesCacheTTL = 30 * 24 * time.Hour
	nyaaLanguagesCacheKey = "nyaa:languages:"
	// nyaaViewConcurrency bounds the release pages fetched at once, so a search
	// does not hammer the site (nyaa.si sits behind ddos-guard).
	nyaaViewConcurrency = 6
)

var (
	nyaaViewIDRE   = regexp.MustCompile(`/view/(\d+)`)
	nyaaCategoryRE = regexp.MustCompile(`[?&]c=(\d+_\d+)`)
	nyaaSizeUnits  = strings.NewReplacer("KiB", "KB", "MiB", "MB", "GiB", "GB", "TiB", "TB")
)

// nyaaRow is one release of a Nyaa search page.
type nyaaRow struct {
	ID       string
	Title    string
	Category string
	Magnet   string
	Size     string
	ViewURL  string
	Date     time.Time
	Seeders  int
	Leechers int
}

func (i *Indexer) HandlerNyaaIndexer(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metadata := nyaa

	defer func() {
		i.metrics.IndexerDuration.WithLabelValues(metadata.Label).Observe(time.Since(start).Seconds())
		i.metrics.IndexerRequests.WithLabelValues(metadata.Label).Inc()
	}()

	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := strings.TrimSpace(r.URL.Query().Get("page"))

	logging.InfoWithRequest(r).Str("query", q).Str("page", page).Msg("Processing indexer request")
	torrents, err := getNyaaTorrents(ctx, i, nyaaMirrors(metadata.URL), q, page)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if encodeErr := json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); encodeErr != nil {
			logging.ErrorWithRequest(r).Err(encodeErr).Msg("Failed to encode error response")
		}
		i.metrics.IndexerErrors.WithLabelValues(metadata.Label).Inc()
		return
	}

	postProcessedTorrents := torrents
	for _, processor := range i.postProcessors {
		postProcessedTorrents = processor(i, r, postProcessedTorrents)
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(Response{
		Results:      postProcessedTorrents,
		Count:        len(postProcessedTorrents),
		IndexedCount: len(torrents),
	})
	if err != nil {
		logging.Error().Err(err).Msg("Failed to encode response")
	}
}

// nyaaMirrors returns the primary URL followed by INDEXER_NYAA_MIRRORS (comma
// separated, default nyaa.wiki), normalized and without duplicates.
func nyaaMirrors(primary string) []string {
	mirrors := []string{primary}
	for _, mirror := range strings.Split(utils.GetEnvOrDefault("INDEXER_NYAA_MIRRORS", nyaaDefaultMirrors), ",") {
		mirror = strings.TrimSpace(mirror)
		if mirror == "" {
			continue
		}
		if !strings.HasSuffix(mirror, "/") {
			mirror += "/"
		}
		mirrors = append(mirrors, mirror)
	}

	seen := map[string]bool{}
	unique := mirrors[:0]
	for _, mirror := range mirrors {
		if !seen[mirror] {
			seen[mirror] = true
			unique = append(unique, mirror)
		}
	}
	return unique
}

// buildNyaaSearchURL builds the anime search of a mirror. nyaa.si pages with
// "p" and nyaa.wiki with "page"; each ignores the other's parameter.
func buildNyaaSearchURL(base, q, page string) string {
	values := url.Values{
		"f": {"0"},
		"c": {nyaaCategoryAnime},
		"q": {q},
	}
	if page != "" && page != "1" {
		values.Set("p", page)
		values.Set("page", page)
	}
	return base + "?" + values.Encode()
}

// getNyaaTorrents searches the first mirror that answers and reads the
// languages of every release from its page.
func getNyaaTorrents(ctx context.Context, i *Indexer, mirrors []string, q, page string) ([]schema.IndexedTorrent, error) {
	var rows []nyaaRow
	var lastErr error
	for _, base := range mirrors {
		searchURL := buildNyaaSearchURL(base, q, page)
		resp, err := i.requester.GetDocument(ctx, searchURL)
		if err != nil {
			logging.Warn().Err(err).Str("target_url", searchURL).Msg("Nyaa mirror failed, trying the next one")
			lastErr = err
			continue
		}
		doc, err := goquery.NewDocumentFromReader(resp)
		resp.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to parse %s: %w", searchURL, err)
			continue
		}
		rows = parseNyaaListing(doc, base)
		lastErr = nil
		break
	}
	if lastErr != nil {
		return nil, lastErr
	}

	sem := make(chan struct{}, nyaaViewConcurrency)
	torrents := utils.ParallelFlatMap(rows, func(row nyaaRow) ([]schema.IndexedTorrent, error) {
		sem <- struct{}{}
		defer func() { <-sem }()
		return []schema.IndexedTorrent{buildNyaaTorrent(ctx, i, row)}, nil
	})
	// ParallelFlatMap does not keep the order: restore the site's, newest first
	sort.SliceStable(torrents, func(a, b int) bool { return torrents[a].Date.After(torrents[b].Date) })

	return torrents, nil
}

func buildNyaaTorrent(ctx context.Context, i *Indexer, row nyaaRow) schema.IndexedTorrent {
	audio, subtitles := resolveNyaaLanguages(row.Title, row.Category, getNyaaPageLanguages(ctx, i, row))

	torrent := schema.IndexedTorrent{
		Title:               row.Title,
		OriginalTitle:       row.Title,
		Details:             row.ViewURL,
		Audio:               audio,
		Subtitles:           subtitles,
		LanguagesFromSource: true,
		MagnetLink:          row.Magnet,
		Date:                row.Date,
		Size:                row.Size,
		SeedCount:           row.Seeders,
		LeechCount:          row.Leechers,
	}
	if parsed, err := magnet.ParseMagnetUri(row.Magnet); err == nil {
		torrent.InfoHash = parsed.InfoHash.String()
		torrent.Trackers = parsed.Trackers
	} else {
		logging.Warn().Err(err).Str("magnet_link", row.Magnet).Msg("Failed to parse Nyaa magnet URI")
	}
	return torrent
}

// getNyaaPageLanguages reads the languages stated in the release description,
// cached by release id. A page that cannot be fetched yields no languages (and
// is not cached), leaving resolveNyaaLanguages to fall back on the title.
func getNyaaPageLanguages(ctx context.Context, i *Indexer, row nyaaRow) nyaaLanguages {
	key := nyaaLanguagesCacheKey + row.ID
	if cached, err := i.redis.Get(ctx, key); err == nil {
		var languages nyaaLanguages
		if json.Unmarshal(cached, &languages) == nil {
			return languages
		}
	}

	resp, err := i.requester.GetDocument(ctx, row.ViewURL)
	if err != nil {
		logging.Warn().Err(err).Str("url", row.ViewURL).Msg("Failed to fetch Nyaa release page")
		return nyaaLanguages{}
	}
	defer resp.Close()
	doc, err := goquery.NewDocumentFromReader(resp)
	if err != nil {
		return nyaaLanguages{}
	}

	languages := parseNyaaDescription(nyaaDescription(doc))
	if encoded, err := json.Marshal(languages); err == nil {
		if err := i.redis.SetWithExpiration(ctx, key, encoded, nyaaLanguagesCacheTTL); err != nil {
			logging.Error().Err(err).Str("key", key).Msg("Failed to cache Nyaa languages")
		}
	}
	return languages
}

// nyaaDescription returns the release description: raw markdown on nyaa.si,
// rendered HTML on nyaa.wiki (kept as HTML so the table rows survive).
func nyaaDescription(doc *goquery.Document) string {
	description := doc.Find("#torrent-description").First()
	if description.Children().Length() > 0 {
		if rendered, err := description.Html(); err == nil {
			return rendered
		}
	}
	return description.Text()
}

// parseNyaaListing reads the releases of a search page — the same table on
// nyaa.si (relative links) and nyaa.wiki (absolute links) — dropping the raws,
// which carry no subtitles at all.
func parseNyaaListing(doc *goquery.Document, base string) []nyaaRow {
	baseURL, _ := url.Parse(base)

	var rows []nyaaRow
	doc.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		link := tr.Find(`a[href*="/view/"]`).FilterFunction(func(_ int, a *goquery.Selection) bool {
			href, _ := a.Attr("href")
			return !strings.Contains(href, "#")
		}).First()
		href, ok := link.Attr("href")
		if !ok {
			return
		}
		id := nyaaViewIDRE.FindStringSubmatch(href)
		magnetLink, _ := tr.Find(`a[href^="magnet:"]`).First().Attr("href")
		if id == nil || magnetLink == "" {
			return
		}

		row := nyaaRow{ID: id[1], Magnet: magnetLink, ViewURL: href}
		if baseURL != nil {
			if ref, err := url.Parse(href); err == nil {
				row.ViewURL = baseURL.ResolveReference(ref).String()
			}
		}
		row.Title = strings.TrimSpace(link.AttrOr("title", ""))
		if row.Title == "" {
			row.Title = strings.TrimSpace(link.Text())
		}
		if categoryHref, ok := tr.Find(`a[href*="c="]`).First().Attr("href"); ok {
			if m := nyaaCategoryRE.FindStringSubmatch(categoryHref); m != nil {
				row.Category = m[1]
			}
		}
		if row.Category == nyaaCategoryRaw {
			return
		}

		// size, date, seeders and leechers sit around the timestamp cell
		cells := tr.Find("td")
		cells.EachWithBreak(func(idx int, td *goquery.Selection) bool {
			timestamp, ok := td.Attr("data-timestamp")
			if !ok {
				return true
			}
			if unix, err := strconv.ParseInt(timestamp, 10, 64); err == nil {
				row.Date = time.Unix(unix, 0)
			}
			row.Size = nyaaSizeUnits.Replace(strings.TrimSpace(cells.Eq(idx - 1).Text()))
			row.Seeders, _ = strconv.Atoi(strings.TrimSpace(cells.Eq(idx + 1).Text()))
			row.Leechers, _ = strconv.Atoi(strings.TrimSpace(cells.Eq(idx + 2).Text()))
			return false
		})

		rows = append(rows, row)
	})
	return rows
}
