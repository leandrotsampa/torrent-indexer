package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
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
)

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
	targetURL := buildDarkmahouURL(metadata, q, page)

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

// resolveDarkmahouLink turns a download-area anchor into a usable magnet link.
// It accepts raw magnet links (pre-obfuscation HTML) and ad-gateway links whose
// "id" holds the AES-GCM-encrypted real URL. Cloud-storage links (jottacloud,
// drive, etc.) are not usable as torrents and are dropped (returns "").
func resolveDarkmahouLink(href, key string) string {
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "magnet:") {
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
	if strings.HasPrefix(decoded, "magnet:") {
		return decoded
	}

	// decoded link is a direct/cloud-storage URL, not a torrent
	return ""
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
			parsedMagnet, err := magnet.ParseMagnetUri(magnetLink.URL)
			if err != nil {
				logging.Error().Err(err).Str("magnet_link", magnetLink.URL).Msg("Failed to parse magnet URI")
			}
			releaseTitle := strings.TrimSpace(parsedMagnet.DisplayName)
			if releaseTitle == "" {
				releaseTitle = magnetLink.Label
			}
			infoHash := parsedMagnet.InfoHash.String()
			trackers := parsedMagnet.Trackers
			magnetAudio := getAudioFromTitle(releaseTitle, audio)

			peer, seed, err := goscrape.GetLeechsAndSeeds(ctx, i.redis, i.metrics, infoHash, trackers)
			if err != nil {
				logging.Error().Err(err).Str("info_hash", infoHash).Msg("Failed to get leechers and seeders")
			}

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
