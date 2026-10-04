package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/leandrotsampa/torrent-indexer/logging"
	"github.com/leandrotsampa/torrent-indexer/magnet"
	"github.com/leandrotsampa/torrent-indexer/schema"
	goscrape "github.com/leandrotsampa/torrent-indexer/scrape"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

var starck_filmes = IndexerMeta{
	Label:       "starck_filmes",
	URL:         utils.GetIndexerURLFromEnv("INDEXER_STARCK_FILMES_URL", "https://starckfilmesnet.com/"),
	SearchURL:   "?s=",
	PagePattern: "page/%s",
}

func (i *Indexer) HandlerStarckFilmesIndexer(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metadata := starck_filmes

	defer func() {
		i.metrics.IndexerDuration.WithLabelValues(metadata.Label).Observe(time.Since(start).Seconds())
		i.metrics.IndexerRequests.WithLabelValues(metadata.Label).Inc()
	}()

	ctx := r.Context()
	// supported query params: q, page, filter_results
	q := r.URL.Query().Get("q")
	page := r.URL.Query().Get("page")

	// URL encode query param
	q = url.QueryEscape(q)
	url := metadata.URL
	if q != "" {
		url = fmt.Sprintf("%s%s%s", url, metadata.SearchURL, q)
	} else if page != "" {
		url = fmt.Sprintf(fmt.Sprintf("%s%s", url, metadata.PagePattern), page)
	}

	// if search is empty, redirect to page 1
	if q == "" {
		url = fmt.Sprintf(fmt.Sprintf("%s%s", url, metadata.PagePattern), "1")
	}

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

	var links []string
	doc.Find("a.o-card").Each(func(i int, s *goquery.Selection) {
		link, _ := s.Attr("href")
		if link != "" {
			links = append(links, link)
		}
	})

	// if no links were indexed, expire the document in cache
	if len(links) == 0 {
		_ = i.requester.ExpireDocument(ctx, url)
		logging.DebugWithRequest(r).Str("url", url).Msg("No links found, expiring cache")
	}

	// extract each torrent link
	indexedTorrents := utils.ParallelFlatMap(links, func(link string) ([]schema.IndexedTorrent, error) {
		return getTorrentStarckFilmes(ctx, i, link, url)
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

func getTorrentStarckFilmes(ctx context.Context, i *Indexer, link, referer string) ([]schema.IndexedTorrent, error) {
	var indexedTorrents []schema.IndexedTorrent
	doc, err := getDocument(ctx, i, link, referer)
	if err != nil {
		return nil, err
	}

	date := getPublishedDateFromRawString(link)

	// layout novo (starckfilmesnet.com): h1 "Título (ano)", downloads agrupados
	// por áudio em sections .o-lista > header .o-grupo-nome + divs .o-arquivo
	titleRe := regexp.MustCompile(`^(.*?)(?: Torrent)?\s*\((\d{4})\)`)
	titleP := titleRe.FindStringSubmatch(doc.Find("h1").First().Text())
	if len(titleP) < 2 {
		return nil, fmt.Errorf("could not extract title from %s", link)
	}
	title := strings.TrimSpace(titleP[1])
	year := ""
	if len(titleP) >= 3 {
		year = strings.TrimSpace(titleP[2])
	}

	var magnetLinks []string
	var audio [][]schema.Audio
	doc.Find("section.o-lista").Each(func(_ int, grupo *goquery.Selection) {
		grupoNome := strings.TrimSpace(grupo.Find(".o-grupo-nome").First().Text())
		grupo.Find(".o-arquivo").Each(func(_ int, arquivo *goquery.Selection) {
			magnetLink, ok := arquivo.Find("a.o-baixar[href^='magnet']").Attr("href")
			if !ok {
				return
			}
			// magnets vêm URL-encoded
			if decoded, err := url.QueryUnescape(magnetLink); err == nil {
				magnetLink = decoded
			}
			magnetLinks = append(magnetLinks, magnetLink)
			label := grupoNome + " " + strings.TrimSpace(arquivo.Find(".o-arquivo-info").Text())
			audio = append(audio, findAudioFromText(label))
		})
	})

	// fallback: magnets soltos, sem agrupamento
	if len(magnetLinks) == 0 {
		doc.Find("a[href^='magnet']").Each(func(_ int, s *goquery.Selection) {
			magnetLink, _ := s.Attr("href")
			magnetLinks = append(magnetLinks, magnetLink)
			audio = append(audio, nil)
		})
	}

	// TODO: find any link from imdb
	imdbLink := ""

	var chanIndexedTorrent = make(chan schema.IndexedTorrent)

	// for each magnet link, create a new indexed torrent
	for it, magnetLink := range magnetLinks {
		it := it
		go func(it int, magnetLink string) {
			magnet, err := magnet.ParseMagnetUri(magnetLink)
			if err != nil {
				logging.Error().Err(err).Str("magnet_link", magnetLink).Msg("Failed to parse magnet URI")
			}
			releaseTitle := strings.TrimSpace(magnet.DisplayName)
			// url decode the title
			releaseTitle, err = url.QueryUnescape(releaseTitle)
			if err != nil {
				logging.Error().Err(err).Str("title", releaseTitle).Msg("Failed to URL decode title")
				releaseTitle = strings.TrimSpace(magnet.DisplayName)
			}
			infoHash := magnet.InfoHash.String()
			trackers := magnet.Trackers
			for i, tracker := range trackers {
				unescapedTracker, err := url.QueryUnescape(tracker)
				if err != nil {
					logging.Error().Err(err).Str("tracker", tracker).Msg("Failed to URL decode tracker")
				}
				trackers[i] = strings.TrimSpace(unescapedTracker)
			}
			magnetAudio := audio[it]
			if len(magnetAudio) == 0 {
				magnetAudio = getAudioFromTitle(releaseTitle, magnetAudio)
			}

			peer, seed, err := goscrape.GetLeechsAndSeeds(ctx, i.redis, i.metrics, infoHash, trackers)
			if err != nil {
				logging.Error().Err(err).Str("info_hash", infoHash).Msg("Failed to get leechers and seeders")
			}

			title := processTitle(title, magnetAudio)

			var mySize string
			if mySize == "" {
				warmMagnetMetadata(ctx, i, magnetLink)
			}

			ixt := schema.IndexedTorrent{
				Title:         releaseTitle,
				OriginalTitle: title,
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
		indexedTorrents = append(indexedTorrents, it)
	}

	return indexedTorrents, nil
}
