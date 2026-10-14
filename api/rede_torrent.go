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

var rede_torrent = IndexerMeta{
	Label:       "rede_torrent",
	URL:         utils.GetIndexerURLFromEnv("INDEXER_REDE_TORRENT_URL", "https://redestorrents.com/"),
	SearchURL:   "index.php?busca=",
	PagePattern: "pagina/%s/",
}

// title pattern: "Something - optional balbla (dddd) some shit" - extract "Something" and "dddd"
var redeTorrentTitleRe = regexp.MustCompile(`^(.*?)(?: - (.*?))? \((\d{4})\)`)

// redeTorrentMagnet is a magnet link found in the post along with the label
// the page shows next to it (ex: "01º EPISÓDIO DOWNLOAD TORRENT DUAL ÁUDIO 5.1 MKV 1080P").
type redeTorrentMagnet struct {
	Link  string
	Label string
}

// redeTorrentPost holds everything parsed from a post page.
type redeTorrentPost struct {
	Title         string
	OriginalTitle string
	Year          string
	IMDB          string
	Audio         []schema.Audio
	Sizes         []string
	Date          time.Time
	Magnets       []redeTorrentMagnet
}

func (i *Indexer) HandlerRedeTorrentIndexer(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metadata := rede_torrent

	defer func() {
		i.metrics.IndexerDuration.WithLabelValues(metadata.Label).Observe(time.Since(start).Seconds())
		i.metrics.IndexerRequests.WithLabelValues(metadata.Label).Inc()
	}()

	ctx := r.Context()
	// supported query params: q, season, episode, page, filter_results
	q := r.URL.Query().Get("q")
	page := r.URL.Query().Get("page")

	url := buildRedeTorrentURL(metadata, q, page)

	logging.InfoWithRequest(r).Str("target_url", url).Msg("Processing indexer request")
	resp, err := i.requester.GetDocument(ctx, url)
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

	links := extractRedeTorrentLinks(doc)

	// if no links were indexed, expire the document in cache
	if len(links) == 0 {
		_ = i.requester.ExpireDocument(ctx, url)
		logging.DebugWithRequest(r).Str("url", url).Msg("No links found, expiring cache")
	}

	// extract each torrent link
	indexedTorrents := utils.ParallelFlatMap(links, func(link string) ([]schema.IndexedTorrent, error) {
		return getTorrentsRedeTorrent(ctx, i, link, url)
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

// buildRedeTorrentURL builds the listing URL. Searches go through
// "index.php?busca=<q>" (paginated with "&pagina=N"); the plain listing is
// paginated with "pagina/N/".
func buildRedeTorrentURL(metadata IndexerMeta, q, page string) string {
	if q != "" {
		target := fmt.Sprintf("%s%s%s", metadata.URL, metadata.SearchURL, url.QueryEscape(q))
		if page != "" {
			target = fmt.Sprintf("%s&pagina=%s", target, url.QueryEscape(page))
		}
		return target
	}

	if page != "" {
		return fmt.Sprintf(fmt.Sprintf("%s%s", metadata.URL, metadata.PagePattern), page)
	}

	return metadata.URL
}

// extractRedeTorrentLinks returns the post links of a listing page (home, pagination or search).
func extractRedeTorrentLinks(doc *goquery.Document) []string {
	var links []string
	doc.Find("a.cover-link").Each(func(_ int, s *goquery.Selection) {
		link := strings.TrimSpace(s.AttrOr("href", ""))
		if link != "" {
			links = append(links, link)
		}
	})
	return utils.StableUniq(links)
}

// parseRedeTorrentPost extracts the metadata and magnet links of a post page.
func parseRedeTorrentPost(doc *goquery.Document, link string) (redeTorrentPost, error) {
	var post redeTorrentPost

	article := doc.Find(`article[itemtype*="schema.org"]`).First()
	if article.Length() == 0 {
		article = doc.Selection
	}

	// the h1 starts with a line break and indentation, which breaks the anchored regex
	h1 := strings.TrimSpace(article.Find("h1.movie-title").First().Text())
	if h1 == "" {
		return post, fmt.Errorf("could not extract title from %s", link)
	}
	if titleP := redeTorrentTitleRe.FindStringSubmatch(h1); len(titleP) > 3 {
		post.Title = strings.TrimSpace(titleP[1])
		post.Year = strings.TrimSpace(titleP[3])
	} else {
		// no year in the title: keep everything before the " Torrent" suffix
		title := h1
		if idx := strings.Index(title, " Torrent"); idx > 0 {
			title = title[:idx]
		}
		post.Title = strings.TrimSpace(title)
	}
	if post.Year == "" {
		post.Year = strings.TrimSpace(article.Find(`[aria-label="Ano de lançamento"] time`).First().Text())
	}

	post.OriginalTitle = strings.TrimSpace(article.Find("p.original-title").First().Text())
	post.Date = getPublishedDateFromMeta(doc)

	article.Find(`.download-list a[href^="magnet"]`).Each(func(_ int, s *goquery.Selection) {
		label := s.Closest(".download-row").Find(".download-name").First().Text()
		post.Magnets = append(post.Magnets, redeTorrentMagnet{
			Link:  s.AttrOr("href", ""),
			Label: strings.TrimSpace(label),
		})
	})

	// pattern of each spec card:
	// <small>Idioma / Áudio</small> <strong>Inglês / Português</strong>
	// <small>Tamanho do Arquivo</small> <strong> 1.49 GB | 3.50 GB</strong>
	article.Find(".spec-card-glass").Each(func(_ int, s *goquery.Selection) {
		label := strings.TrimSpace(s.Find("small").First().Text())
		value := strings.TrimSpace(s.Find("strong").First().Text())
		if label == "" || value == "" {
			return
		}
		text := label + ": " + value
		lowerLabel := strings.ToLower(label)

		// "Qualidade de Áudio: 10 / 10" is a score, not a language
		if strings.Contains(lowerLabel, "idioma") || strings.HasPrefix(lowerLabel, "áudio") {
			post.Audio = append(post.Audio, findAudioFromText(text)...)
		}
		if strings.Contains(lowerLabel, "tamanho") {
			post.Sizes = append(post.Sizes, findSizesFromText(text)...)
		}
		if y := findYearFromText(text, ""); y != "" {
			post.Year = y
		}
	})
	post.Sizes = utils.StableUniq(post.Sizes)

	// the IMDB link lives in the "sameAs" of the schema.org JSON-LD
	doc.Find(`script[type="application/ld+json"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if imdbLink, err := getIMDBLink(s.Text()); err == nil {
			post.IMDB = imdbLink
			return false
		}
		return true
	})
	if post.IMDB == "" {
		article.Find("a").Each(func(_ int, s *goquery.Selection) {
			if imdbLink, err := getIMDBLink(s.AttrOr("href", "")); err == nil {
				post.IMDB = imdbLink
			}
		})
	}

	return post, nil
}

// redeTorrentMagnetAudio picks the audio of a release using both its name and
// the label shown next to the magnet ("DUAL ÁUDIO", "LEGENDADO"...), since the
// release name does not always announce the dub.
func redeTorrentMagnetAudio(releaseTitle, label string, audio []schema.Audio) []schema.Audio {
	return getAudioFromTitle(strings.TrimSpace(releaseTitle+" "+label), audio)
}

func getTorrentsRedeTorrent(ctx context.Context, i *Indexer, link, referer string) ([]schema.IndexedTorrent, error) {
	var indexedTorrents []schema.IndexedTorrent
	doc, err := getDocument(ctx, i, link, referer)
	if err != nil {
		return nil, err
	}

	post, err := parseRedeTorrentPost(doc, link)
	if err != nil {
		return nil, err
	}

	var chanIndexedTorrent = make(chan schema.IndexedTorrent)

	// for each magnet link, create a new indexed torrent
	for it, m := range post.Magnets {
		go func(it int, m redeTorrentMagnet) {
			magnetLink := m.Link
			magnet, err := magnet.ParseMagnetUri(magnetLink)
			if err != nil {
				logging.Error().Err(err).Str("magnet_link", magnetLink).Msg("Failed to parse magnet URI")
			}
			releaseTitle := magnet.DisplayName
			infoHash := magnet.InfoHash.String()
			trackers := magnet.Trackers
			magnetAudio := redeTorrentMagnetAudio(releaseTitle, m.Label, post.Audio)

			peer, seed, err := goscrape.GetLeechsAndSeeds(ctx, i.redis, i.metrics, infoHash, trackers)
			if err != nil {
				logging.Error().Err(err).Str("info_hash", infoHash).Msg("Failed to get leechers and seeders")
			}

			title := processTitle(post.Title, magnetAudio)

			// if the number of sizes is equal to the number of magnets, then assign the size to each indexed torrent in order
			var mySize string
			if len(post.Sizes) == len(post.Magnets) {
				mySize = post.Sizes[it]
			}
			if mySize == "" {
				warmMagnetMetadata(ctx, i, magnetLink)
			}

			ixt := schema.IndexedTorrent{
				Title:         releaseTitle,
				OriginalTitle: title,
				Details:       link,
				Year:          post.Year,
				IMDB:          post.IMDB,
				Audio:         magnetAudio,
				MagnetLink:    magnetLink,
				Date:          post.Date,
				InfoHash:      infoHash,
				Trackers:      trackers,
				LeechCount:    peer,
				SeedCount:     seed,
				Size:          mySize,
			}
			chanIndexedTorrent <- ixt
		}(it, m)
	}

	for i := 0; i < len(post.Magnets); i++ {
		it := <-chanIndexedTorrent
		indexedTorrents = append(indexedTorrents, it)
	}

	return indexedTorrents, nil
}
