package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/leandrotsampa/torrent-indexer/logging"
	"github.com/leandrotsampa/torrent-indexer/magnet"
	"github.com/leandrotsampa/torrent-indexer/schema"
	goscrape "github.com/leandrotsampa/torrent-indexer/scrape"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

var darkmahou = IndexerMeta{
	Label:       "darkmahou",
	URL:         utils.GetIndexerURLFromEnv("INDEXER_DARKMAHOU_URL", "https://darkmahou.io/"),
	SearchURL:   "?s=",
	PagePattern: "page/%s/",
}

var (
	// darkmahouKeyRE extracts the AES-GCM KEY_STRING from the page's obfuscation
	// script, so key rotation on the site does not break decoding.
	darkmahouKeyRE = regexp.MustCompile(`KEY_STRING\s*=\s*["']([^"']+)["']`)
	// darkmahouSystemAdsRE matches the ad-gateway anchors that wrap the real link
	// (e.g. https://systemads1.com/go.php?id=...). The numeric suffix may change.
	darkmahouSystemAdsRE = regexp.MustCompile(`(?i)^https?://systemads\d*\.[^/]+/go\.php`)

	// darkmahouSeasonEpisodeRE strips season/episode qualifiers from the search
	// query. darkmahou.io indexes only by anime name; the season and per-episode
	// downloads live inside the anime page, so searching "<name> 09" or
	// "<name> temporada 1" returns nothing.
	darkmahouSeasonEpisodeRE = regexp.MustCompile(`(?i)\s*\b(` +
		`s\d{1,2}e\d{1,4}|` + // S01E09
		`\d+\s*ª?\s*temporada|temporada\s*\d+|` + // 1ª temporada / temporada 1
		`season\s*\d+|s\d{1,2}|` + // season 1 / S01
		`epis[oó]dio\s*\d{1,4}|episode\s*\d{1,4}|ep\s*\d{1,4}` + // episódio 09
		`)\b`)
	// darkmahouTrailingNumberRE strips a trailing bare episode number ("<name> 09").
	darkmahouTrailingNumberRE = regexp.MustCompile(`\s+\d{1,4}\s*$`)
	// darkmahouWhitespaceRE collapses whitespace left behind after stripping.
	darkmahouWhitespaceRE = regexp.MustCompile(`\s+`)
)

// sanitizeDarkmahouQuery reduces a search query to the bare anime name, dropping
// season/episode qualifiers that darkmahou.io does not index. If stripping would
// empty the query, the trimmed original is returned unchanged.
func sanitizeDarkmahouQuery(q string) string {
	if q == "" {
		return q
	}
	out := darkmahouSeasonEpisodeRE.ReplaceAllString(q, "")
	out = darkmahouTrailingNumberRE.ReplaceAllString(out, "")
	out = strings.TrimSpace(darkmahouWhitespaceRE.ReplaceAllString(out, " "))
	if out == "" {
		return strings.TrimSpace(q)
	}
	return out
}

type darkmahouMagnetLink struct {
	URL   string
	Label string
}

func (i *Indexer) HandlerDarkmahouIndexer(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metadata := darkmahou

	defer func() {
		i.metrics.IndexerDuration.WithLabelValues(metadata.Label).Observe(time.Since(start).Seconds())
		i.metrics.IndexerRequests.WithLabelValues(metadata.Label).Inc()
	}()

	ctx := r.Context()
	// supported query params: q, page, filter_results
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := r.URL.Query().Get("page")
	// darkmahou.io indexes only by anime name; season/episode downloads live
	// inside the anime page, so the site search must use the bare name. The
	// original q (with episode) is kept on the request for similarity ranking.
	targetURL := buildDarkmahouURL(metadata, sanitizeDarkmahouQuery(q), page)

	logging.InfoWithRequest(r).Str("target_url", targetURL).Msg("Processing indexer request")
	resp, err := i.requester.GetDocument(ctx, targetURL)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if err = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); err != nil {
			logging.ErrorWithRequest(r).Err(err).Msg("Failed to encode error response")
		}
		i.metrics.IndexerErrors.WithLabelValues(metadata.Label).Inc()
		return
	}
	defer resp.Close()

	doc, err := goquery.NewDocumentFromReader(resp)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if err = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); err != nil {
			logging.ErrorWithRequest(r).Err(err).Msg("Failed to encode error response")
		}
		i.metrics.IndexerErrors.WithLabelValues(metadata.Label).Inc()
		return
	}

	links := extractDarkmahouSearchLinks(doc)

	// if no links were indexed, expire the document in cache
	if len(links) == 0 {
		_ = i.requester.ExpireDocument(ctx, targetURL)
	}

	// extract each torrent link
	indexedTorrents := utils.ParallelFlatMap(links, func(link string) ([]schema.IndexedTorrent, error) {
		return getTorrentsDarkmahou(ctx, i, link, targetURL)
	})

	// Apply post-processors
	postProcessedTorrents := indexedTorrents
	for _, processor := range i.postProcessors {
		postProcessedTorrents = processor(i, r, postProcessedTorrents)
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(Response{
		Results:      postProcessedTorrents,
		Count:        len(postProcessedTorrents),
		IndexedCount: len(indexedTorrents),
	})
	if err != nil {
		logging.Error().Err(err).Msg("Failed to encode response")
	}
}

func buildDarkmahouURL(metadata IndexerMeta, q, page string) string {
	if page != "" {
		if q != "" {
			return fmt.Sprintf("%spage/%s/?s=%s", metadata.URL, page, url.QueryEscape(q))
		}

		return fmt.Sprintf("%s%s", metadata.URL, fmt.Sprintf(metadata.PagePattern, page))
	}

	return fmt.Sprintf("%s%s%s", metadata.URL, metadata.SearchURL, url.QueryEscape(q))
}

// extractDarkmahouSearchLinks collects the anime page URLs from a search/listing
// page. Results live in div.listupd as article.bs cards linking via a.tip.
func extractDarkmahouSearchLinks(doc *goquery.Document) []string {
	var links []string
	seen := map[string]bool{}
	doc.Find("div.listupd article.bs a.tip").Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok {
			return
		}
		href = strings.TrimSpace(href)
		if href == "" || seen[href] {
			return
		}
		seen[href] = true
		links = append(links, href)
	})

	return links
}

// extractDarkmahouKey returns the site's obfuscation key, read from the page's
// JS when present, falling back to the known default.
func extractDarkmahouKey(doc *goquery.Document) string {
	htmlStr, err := doc.Html()
	if err != nil {
		return utils.DarkmahouAdKey
	}
	if m := darkmahouKeyRE.FindStringSubmatch(htmlStr); len(m) > 1 {
		return m[1]
	}

	return utils.DarkmahouAdKey
}

// isGrabbableDarkmahouLink reports whether a resolved link is something the
// *arr apps can grab: a magnet, or a .torrent file URL (darkmahou also serves
// links to nyaa.si .torrent files). Cloud-storage links (jottacloud, drive,
// yandex, etc.) are not grabbable and are rejected.
func isGrabbableDarkmahouLink(u string) bool {
	if strings.HasPrefix(u, "magnet:") {
		return true
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return false
	}
	path := u
	if idx := strings.IndexAny(path, "?#"); idx >= 0 {
		path = path[:idx]
	}
	return strings.HasSuffix(strings.ToLower(path), ".torrent")
}

// resolveDarkmahouLink turns a download-area anchor into a grabbable link.
// It accepts raw magnet/.torrent links (pre-obfuscation HTML) and ad-gateway
// links whose "id" holds the AES-GCM-encrypted real URL. Cloud-storage links
// (jottacloud, drive, etc.) are not usable and are dropped (returns "").
func resolveDarkmahouLink(href, key string) string {
	if href == "" {
		return ""
	}
	if isGrabbableDarkmahouLink(href) {
		return href
	}
	if !darkmahouSystemAdsRE.MatchString(href) {
		return ""
	}

	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	// Query().Get already URL-decodes the id into raw base64.
	id := parsed.Query().Get("id")
	if id == "" {
		return ""
	}

	decoded, err := utils.DecodeDarkmahouAdLink(id, key)
	if err != nil {
		logging.Warn().Err(err).Str("href", href).Msg("Failed to decode darkmahou ad link")
		return ""
	}
	if isGrabbableDarkmahouLink(decoded) {
		return decoded
	}

	// decoded link is a cloud-storage URL (jottacloud/drive/etc.), not grabbable
	return ""
}

var (
	darkmahouEpisodeLabelRE = regexp.MustCompile(`(?i)epis[oó]dio\s*(\d+)`)
	// darkmahouLabelNoiseRE drops filler words darkmahou puts in download
	// headings that are not release info ("Torrent", "Download", "Baixar", ...).
	darkmahouLabelNoiseRE = regexp.MustCompile(`(?i)\b(torrent|download|baixar|assistir|online)\b`)
	// darkmahouResolutionRE detects a resolution token (480p/720p/1080p/2160p/4K),
	// used to tell a complete magnet display name from a generic series-only one.
	darkmahouResolutionRE = regexp.MustCompile(`(?i)(\b\d{3,4}p\b|\b4k\b)`)
	// darkmahouSeasonLabelRE matches a Portuguese season heading ("1ª Temporada",
	// "2 Temporada Completa"), capturing the season number and whether it is a
	// complete-season pack.
	darkmahouSeasonLabelRE = regexp.MustCompile(`(?i)(\d+)\s*ª?\s*temporada(\s+(completa|completo))?`)
	// darkmahouRangeRE matches a bracketed episode range ("[01-12]", "(01~12)").
	darkmahouRangeRE = regexp.MustCompile(`[\[\(]\s*\d{1,4}\s*[-~]\s*\d{1,4}\s*[\]\)]`)
)

// chooseDarkmahouReleaseTitle picks the best release title for a download.
// darkmahou magnet display names are inconsistent: some are complete release
// names carrying group/episode-range/quality (e.g.
// "[Erai-raws] Anime - 01 ~ 12 [1080p]"), others are just the series title with
// no quality. When the display name already has a resolution it is kept as-is;
// otherwise the title is rebuilt from the page title + block label, which holds
// the quality and episode/season info that *arr needs (per-episode, season
// packs and each quality).
func chooseDarkmahouReleaseTitle(displayName, pageTitle, label string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName != "" && darkmahouResolutionRE.MatchString(displayName) {
		return displayName
	}
	return buildDarkmahouReleaseTitle(pageTitle, label)
}

// buildDarkmahouReleaseTitle builds a parseable release title from the anime
// page title plus the download-block label. The label is darkmahou's own
// organization of the download and carries the episode/season and quality
// (e.g. "Episódio 01 1080p HEVC" or "1ª Temporada Completa Torrent [01-12] 1080p")
// that magnet display names omit, so it is the authoritative source for *arr
// parsing (per-episode releases, season packs and each quality).
func buildDarkmahouReleaseTitle(pageTitle, label string) string {
	label = strings.TrimSpace(darkmahouWhitespaceRE.ReplaceAllString(
		darkmahouLabelNoiseRE.ReplaceAllString(label, ""), " "))

	// Per-episode: "Episódio 05 720p" -> "<page> - 05 [720p]". Anime episodes use
	// absolute numbering, which *arr parses well.
	if m := darkmahouEpisodeLabelRE.FindStringSubmatch(label); len(m) > 1 {
		quality := strings.TrimSpace(darkmahouEpisodeLabelRE.ReplaceAllString(label, ""))
		title := fmt.Sprintf("%s - %s", pageTitle, m[1])
		if quality != "" {
			title = fmt.Sprintf("%s [%s]", title, quality)
		}
		return title
	}

	// Season pack: normalize "1ª Temporada Completa" -> "S01". Sonarr reads "S01"
	// as a full-season pack (Season 1 -> 1x1-N); a bare "[01-12]" is read as
	// absolute episode numbers and fails to match the series, so for a complete
	// season the now-redundant range is dropped.
	if m := darkmahouSeasonLabelRE.FindStringSubmatch(label); len(m) > 1 {
		season, _ := strconv.Atoi(m[1])
		complete := m[3] != ""
		label = darkmahouSeasonLabelRE.ReplaceAllString(label, fmt.Sprintf("S%02d", season))
		if complete {
			label = darkmahouRangeRE.ReplaceAllString(label, "")
		}
		label = strings.TrimSpace(darkmahouWhitespaceRE.ReplaceAllString(label, " "))
	}

	if label != "" {
		return strings.TrimSpace(fmt.Sprintf("%s %s", pageTitle, label))
	}
	return pageTitle
}

// extractDarkmahouMagnetLinks walks each download block (div.soraddl), using the
// block heading as label and resolving every .slink anchor into a magnet link.
func extractDarkmahouMagnetLinks(doc *goquery.Document, key string) []darkmahouMagnetLink {
	var magnetLinks []darkmahouMagnetLink
	seen := map[string]bool{}

	doc.Find("div.soraddl").Each(func(_ int, block *goquery.Selection) {
		label := strings.TrimSpace(block.Find(".sorattl h3").First().Text())
		block.Find(".slink a").Each(func(_ int, a *goquery.Selection) {
			href, ok := a.Attr("href")
			if !ok {
				return
			}

			resolved := resolveDarkmahouLink(strings.TrimSpace(href), key)
			if resolved == "" || seen[resolved] {
				return
			}
			seen[resolved] = true

			itemLabel := label
			if text := strings.TrimSpace(a.Text()); text != "" {
				itemLabel = strings.TrimSpace(fmt.Sprintf("%s %s", label, text))
			}

			magnetLinks = append(magnetLinks, darkmahouMagnetLink{URL: resolved, Label: itemLabel})
		})
	})

	return magnetLinks
}

func getTorrentsDarkmahou(ctx context.Context, i *Indexer, link, referer string) ([]schema.IndexedTorrent, error) {
	var indexedTorrents []schema.IndexedTorrent
	doc, err := getDocument(ctx, i, link, referer)
	if err != nil {
		return nil, err
	}

	title := strings.TrimSpace(doc.Find("h1.entry-title").First().Text())
	date := getPublishedDateFromMeta(doc)
	key := extractDarkmahouKey(doc)

	// best-effort metadata from the info/synopsis area
	infoText := doc.Find("div.entry-content, div.info, div.bigcontent .infox").Text()
	audio := findAudioFromText(infoText)
	year := findYearFromText(infoText, title)

	magnetLinks := extractDarkmahouMagnetLinks(doc, key)
	if len(magnetLinks) == 0 {
		return nil, nil
	}

	var chanIndexedTorrent = make(chan schema.IndexedTorrent)

	// for each magnet link, create a new indexed torrent
	for _, magnetLink := range magnetLinks {
		go func(magnetLink darkmahouMagnetLink) {
			var displayName, infoHash string
			var trackers []string
			var peer, seed int

			if strings.HasPrefix(magnetLink.URL, "magnet:") {
				parsedMagnet, err := magnet.ParseMagnetUri(magnetLink.URL)
				if err != nil {
					logging.Error().Err(err).Str("magnet_link", magnetLink.URL).Msg("Failed to parse magnet URI")
				}
				displayName = strings.TrimSpace(parsedMagnet.DisplayName)
				infoHash = parsedMagnet.InfoHash.String()
				trackers = parsedMagnet.Trackers

				peer, seed, err = goscrape.GetLeechsAndSeeds(ctx, i.redis, i.metrics, infoHash, trackers)
				if err != nil {
					logging.Error().Err(err).Str("info_hash", infoHash).Msg("Failed to get leechers and seeders")
				}
			}
			// Keep a complete magnet display name (group/range/quality) as-is;
			// otherwise rebuild from the page title + block label so episodes,
			// season packs and each quality are distinguishable. .torrent links
			// (e.g. nyaa) carry no display name and always use the label.
			releaseTitle := chooseDarkmahouReleaseTitle(displayName, title, magnetLink.Label)
			magnetAudio := getAudioFromTitle(releaseTitle, audio)

			originalTitle := processTitle(title, magnetAudio)

			ixt := schema.IndexedTorrent{
				Title:         releaseTitle,
				OriginalTitle: originalTitle,
				Details:       link,
				Year:          year,
				Audio:         magnetAudio,
				MagnetLink:    magnetLink.URL,
				Date:          date,
				InfoHash:      infoHash,
				Trackers:      trackers,
				LeechCount:    peer,
				SeedCount:     seed,
			}
			chanIndexedTorrent <- ixt
		}(magnetLink)
	}

	for range magnetLinks {
		indexedTorrents = append(indexedTorrents, <-chanIndexedTorrent)
	}

	return indexedTorrents, nil
}
