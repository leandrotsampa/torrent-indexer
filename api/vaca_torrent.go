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
	"github.com/felipemarinho97/torrent-indexer/logging"
	"github.com/felipemarinho97/torrent-indexer/magnet"
	"github.com/felipemarinho97/torrent-indexer/schema"
	goscrape "github.com/felipemarinho97/torrent-indexer/scrape"
	"github.com/felipemarinho97/torrent-indexer/utils"
)

var vacaTorrent = IndexerMeta{
	Label:       "vaca_torrent",
	URL:         utils.GetIndexerURLFromEnv("INDEXER_VACA_TORRENT_URL", "https://vaqueirofilmes.com/pt/"),
	SearchURL:   "?s=",
	PagePattern: "page/%s/",
}

func (i *Indexer) HandlerVacaTorrentIndexer(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metadata := vacaTorrent

	defer func() {
		i.metrics.IndexerDuration.WithLabelValues(metadata.Label).Observe(time.Since(start).Seconds())
		i.metrics.IndexerRequests.WithLabelValues(metadata.Label).Inc()
	}()

	ctx := r.Context()
	// supported query params: q, season, episode, page, filter_results
	q := r.URL.Query().Get("q")
	page := r.URL.Query().Get("page")

	if page == "" {
		page = "1"
	}

	targetURL := buildVacaTorrentURL(metadata, q, page)

	logging.InfoWithRequest(r).Str("target_url", targetURL).Msg("Processing indexer request")
	resp, err := i.requester.GetDocument(ctx, targetURL)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		err = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		if err != nil {
			logging.ErrorWithRequest(r).Err(err).Msg("Failed to encode error response")
		}
		i.metrics.IndexerErrors.WithLabelValues(metadata.Label).Inc()
		return
	}
	defer resp.Close()

	doc, err := goquery.NewDocumentFromReader(resp)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		err = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		if err != nil {
			logging.ErrorWithRequest(r).Err(err).Msg("Failed to encode error response")
		}
		i.metrics.IndexerErrors.WithLabelValues(metadata.Label).Inc()
		return
	}

	var links []string

	// For home page: find article links in .grid-home
	selector := ".i-tem_ht"
	doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
		link, exists := s.Find("a").Attr("href")
		if exists {
			links = append(links, link)
		}
	})
	if len(links) == 0 {
		doc.Find("a[href], link[href]").Each(func(_ int, s *goquery.Selection) {
			link, exists := s.Attr("href")
			if exists && (strings.Contains(link, "/pt/movie/") || strings.Contains(link, "/pt/tv-shows/")) {
				links = append(links, link)
			}
		})
	}
	links = utils.StableUniq(links)

	// if no links were indexed, expire the document in cache
	if len(links) == 0 {
		_ = i.requester.ExpireDocument(ctx, targetURL)
	}

	soraFetcher, err := utils.NewSoraLinkFetcher("https://vacadb.org", i.redis)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		err = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		if err != nil {
			logging.ErrorWithRequest(r).Err(err).Msg("Failed to encode error response")
		}
		i.metrics.IndexerErrors.WithLabelValues(metadata.Label).Inc()
		return
	}

	// extract each torrent link
	indexedTorrents := utils.ParallelFlatMap(links, func(link string) ([]schema.IndexedTorrent, error) {
		return getTorrentsVacaTorrent(ctx, i, link, targetURL, soraFetcher)
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

func buildVacaTorrentURL(metadata IndexerMeta, query, page string) string {
	if page == "" {
		page = "1"
	}

	baseURL := metadata.URL
	if page != "1" {
		baseURL = fmt.Sprintf("%s%s", baseURL, fmt.Sprintf(metadata.PagePattern, page))
	}

	if query == "" {
		return baseURL
	}

	return fmt.Sprintf("%s%s%s", baseURL, metadata.SearchURL, url.QueryEscape(query))
}

var commentRegex = regexp.MustCompile(`<!--(.*?)-->`)

func getVacaTorrentLinkedMagnets(ctx context.Context, i *Indexer, doc *goquery.Document, referer string) []string {
	var linkPages []string
	var magnetLinks []string

	doc.Find(".streaming-container a[href], .mf-top-wrap a[href]").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists || href == "" {
			return
		}
		if strings.HasPrefix(href, "magnet:") {
			magnetLinks = append(magnetLinks, href)
			return
		}
		if strings.Contains(href, "/movie-links/") ||
			strings.Contains(href, "/season/") ||
			strings.Contains(href, "/season-internal/") {
			linkPages = append(linkPages, href)
		}
	})

	for _, linkPage := range utils.StableUniq(linkPages) {
		linkedDoc, err := getDocument(ctx, i, linkPage, referer)
		if err != nil {
			logging.Warn().Err(err).Str("url", linkPage).Msg("Failed to fetch VacaTorrent linked download page")
			continue
		}
		linkedDoc.Find("a[href^=\"magnet\"]").Each(func(_ int, s *goquery.Selection) {
			magnetLink, _ := s.Attr("href")
			magnetLinks = append(magnetLinks, magnetLink)
		})
	}

	return utils.StableUniq(magnetLinks)
}

func getTorrentsVacaTorrent(ctx context.Context, i *Indexer, link, referer string, soraFetcher *utils.SoraLinkFetcher) ([]schema.IndexedTorrent, error) {
	var indexedTorrents []schema.IndexedTorrent
	doc, err := getDocument(ctx, i, link, referer)
	if err != nil {
		return nil, err
	}

	// Extract title from .custom-main-title or h1
	title := strings.TrimSpace(doc.Find(".custom-main-title").First().Text())
	if title == "" {
		title = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	// Remove release date from title if present
	title = strings.TrimSpace(strings.Split(title, "(")[0])

	// Extract metadata from the list items
	var year string
	var imdbLink string
	var audio []schema.Audio
	var size []string
	var season string
	var date time.Time

	date = getPublishedDateFromMeta(doc)

	doc.Find(".col-left ul li, .content p").Each(func(_ int, s *goquery.Selection) {
		text := s.Text()
		html, _ := s.Html()

		// Extract Year
		if year == "" {
			year = findYearFromText(text, title)
		}

		// Extract link
		if imdbLink == "" {
			s.Find("a").Each(func(_ int, link *goquery.Selection) {
				href, exists := link.Attr("href")
				if exists && strings.Contains(href, "imdb.com") {
					_imdbLink, err := getIMDBLink(href)
					if err == nil {
						imdbLink = _imdbLink
					}
				}
			})
		}

		// Extract Audio/Languages
		if len(audio) == 0 {
			audio = append(audio, findAudioFromText(text)...)
		}

		// Extract Season
		if strings.Contains(text, "Season:") || strings.Contains(text, "Temporada:") {
			seasonMatch := regexp.MustCompile(`(\d+)`).FindStringSubmatch(text)
			if len(seasonMatch) > 0 {
				season = seasonMatch[1]
			}
		}

		// Extract Release Date
		if date.IsZero() {
			date = getPublishedDateFromRawString(text)
		}

		// Extract sizes from text
		size = append(size, findSizesFromText(html)...)
	})

	if date.Year() == 0 {
		yearInt, _ := strconv.Atoi(year)
		date = time.Date(yearInt, date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	}

	// Extract magnet links
	var magnetLinks []string
	doc.Find("a[href^=\"magnet\"]").Each(func(_ int, s *goquery.Selection) {
		magnetLink, _ := s.Attr("href")
		magnetLinks = append(magnetLinks, magnetLink)
	})

	doc.Find(".area-links-download a").Each(func(_ int, s *goquery.Selection) {
		// check for vacadb.org
		href, exists := s.Attr("href")
		if exists && strings.Contains(href, "vacadb.org") {
			// extract the first query param value
			u, err := url.Parse(href)
			if err == nil {
				queryID := u.Query().Get("id")
				if queryID != "" {
					magnetLink, err := soraFetcher.FetchLink(ctx, queryID)
					if err == nil && magnetLink != "" {
						magnetLinks = append(magnetLinks, magnetLink)
					}
				}
			}
		}
	})

	// get the comment content inside streaming-container, parse it as HTML and extract magnet links from there
	doc.Find(".streaming-container").Each(func(_ int, s *goquery.Selection) {
		html, err := s.Html()
		// extract html comments and parse them as HTML to find magnet links
		html = strings.ReplaceAll(html, "\n", "")
		if err == nil {
			matches := commentRegex.FindAllStringSubmatch(html, -1)
			for _, match := range matches {
				commentContent := match[1]
				// check if it is a valid HTML before parsing
				if !strings.HasPrefix(strings.TrimSpace(commentContent), "<") {
					continue
				}
				commentDoc, err := goquery.NewDocumentFromReader(strings.NewReader(commentContent))
				if err == nil {
					commentDoc.Find("a[href^=\"magnet\"]").Each(func(_ int, s *goquery.Selection) {
						magnetLink, _ := s.Attr("href")
						magnetLinks = append(magnetLinks, magnetLink)
					})
				}
			}
		}
	})

	magnetLinks = append(magnetLinks, getVacaTorrentLinkedMagnets(ctx, i, doc, link)...)
	magnetLinks = utils.StableUniq(magnetLinks)
	size = utils.StableUniq(size)

	var chanIndexedTorrent = make(chan schema.IndexedTorrent)

	// for each magnet link, create a new indexed torrent
	for it, magnetLink := range magnetLinks {
		it := it
		go func(it int, magnetLink string) {
			magnet, err := magnet.ParseMagnetUri(magnetLink)
			if err != nil {
				logging.Error().Err(err).Str("magnet_link", magnetLink).Msg("Failed to parse magnet URI")
				chanIndexedTorrent <- schema.IndexedTorrent{}
				return
			}
			releaseTitle := magnet.DisplayName
			infoHash := magnet.InfoHash.String()
			trackers := magnet.Trackers
			magnetAudio := getAudioFromTitle(releaseTitle, audio)

			peer, seed, err := goscrape.GetLeechsAndSeeds(ctx, i.redis, i.metrics, infoHash, trackers)
			if err != nil {
				logging.Error().Err(err).Str("info_hash", infoHash).Msg("Failed to get leechers and seeders")
			}

			processedTitle := processVacaTorrentTitle(title, magnetAudio, season)

			// if the number of sizes is equal to the number of magnets, then assign the size to each indexed torrent in order
			var mySize string
			if len(size) == len(magnetLinks) {
				mySize = size[it]
			}
			if mySize == "" {
				go func() {
					_, _ = i.magnetMetadataAPI.FetchMetadata(ctx, magnetLink)
				}()
			}

			ixt := schema.IndexedTorrent{
				Title:         releaseTitle,
				OriginalTitle: processedTitle,
				Details:       link,
				Year:          year,
				IMDB:          imdbLink,
				Audio:         magnetAudio,
				MagnetLink:    magnetLink,
				Date:          date,
				InfoHash:      infoHash,
				Trackers:      trackers,
				LeechCount:    peer,
				SeedCount:     seed,
				Size:          mySize,
			}
			chanIndexedTorrent <- ixt
		}(it, magnetLink)
	}

	for i := 0; i < len(magnetLinks); i++ {
		it := <-chanIndexedTorrent
		// Skip empty torrents (failed to parse)
		if it.InfoHash != "" {
			indexedTorrents = append(indexedTorrents, it)
		}
	}

	return indexedTorrents, nil
}

func processVacaTorrentTitle(title string, audio []schema.Audio, season string) string {
	// Remove common suffixes
	title = strings.Replace(title, " – Download", "", -1)
	title = strings.Replace(title, " - Download", "", -1)
	title = strings.TrimSpace(title)

	// Add season if present
	if season != "" {
		title = fmt.Sprintf("%s S%s - %sª Temporada", title, fmt.Sprintf("%02s", season), season)
	}

	// Add audio ISO 639-2 code to title between ()
	title = appendAudioISO639_2Code(title, audio)

	return title
}
