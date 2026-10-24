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

var starck_filmes = IndexerMeta{
	Label:       "starck_filmes",
	URL:         utils.GetIndexerURLFromEnv("INDEXER_STARCK_FILMES_URL", "https://starckfilmes-v27.com/"),
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
	// legacy layout (starckfilmes-vNN.com): .item > div.sub-item > a
	doc.Find(".item").Each(func(i int, s *goquery.Selection) {
		if link, _ := s.Find("div.sub-item > a").Attr("href"); link != "" {
			links = append(links, link)
		}
	})
	// new layout (starckfilmesnet.com): a.o-card
	doc.Find("a.o-card").Each(func(i int, s *goquery.Selection) {
		if link, _ := s.Attr("href"); link != "" {
			links = append(links, link)
		}
	})
	links = utils.StableUniq(links)

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
	if date.IsZero() {
		date = getPublishedDateFromMeta(doc)
	}

	var title, year string
	var magnets []starckMagnet
	if doc.Find("section.o-lista").Length() > 0 {
		title, year, magnets = parseStarckFilmesListPost(doc)
	} else {
		title, year, magnets = parseStarckFilmesLegacyPost(doc)
	}

	// TODO: find any link from imdb
	imdbLink := ""

	var chanIndexedTorrent = make(chan schema.IndexedTorrent)

	// for each magnet link, create a new indexed torrent
	for _, m := range magnets {
		go func(m starckMagnet) {
			magnetLink := m.link
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
			magnetAudio := getAudioFromTitle(releaseTitle, m.audio)

			peer, seed, err := goscrape.GetLeechsAndSeeds(ctx, i.redis, i.metrics, infoHash, trackers)
			if err != nil {
				logging.Error().Err(err).Str("info_hash", infoHash).Msg("Failed to get leechers and seeders")
			}

			title := processTitle(title, magnetAudio)

			mySize := m.size
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
		}(m)
	}

	for i := 0; i < len(magnets); i++ {
		it := <-chanIndexedTorrent
		indexedTorrents = append(indexedTorrents, it)
	}

	return indexedTorrents, nil
}

type starckMagnet struct {
	link  string
	size  string
	audio []schema.Audio
}

// parseStarckFilmesLegacyPost parses the legacy layout (starckfilmes-vNN.com):
// title and details in .post .capa, magnets (plain or data-u encoded) in
// .post-buttons.
func parseStarckFilmesLegacyPost(doc *goquery.Document) (title, year string, magnets []starckMagnet) {
	post := doc.Find(".post")
	capa := post.Find(".capa")
	title = capa.Find(".post-description > h2").Text()
	post_buttons := post.Find(".post-buttons")
	var magnetLinks []string
	post_buttons.Find("a[href^=\"magnet\"]").Each(func(i int, s *goquery.Selection) {
		magnetLink, _ := s.Attr("href")
		magnetLinks = append(magnetLinks, magnetLink)
	})
	post_buttons.Find("a[data-u]").Each(func(i int, s *goquery.Selection) {
		dataU, _ := s.Attr("data-u")
		magnetLink, err := utils.DecodeStarckDataU(dataU)
		if err != nil {
			logging.Warn().Err(err).Str("data_u", dataU).Msg("Failed to decode data-u attribute, skipping")
			return
		}
		magnetLinks = append(magnetLinks, magnetLink)
	})

	var audio []schema.Audio
	var size []string
	capa.Find(".post-description p").Each(func(i int, s *goquery.Selection) {
		// pattern:
		// Nome Original: 28 Weeks Later
		// Lançamento: 2007
		// Duração: 1h 40 min
		// Gênero: Terror, Suspense, Ficção
		// Formato: MKV
		// Tamanho: 2.45 GB
		// Qualidade de Video: 10
		// Qualidade do Audio: 10
		// Idioma: Português | Inglês
		// Legenda: Português, Inglês, Espanhol
		var text strings.Builder
		s.Find("span").Each(func(i int, span *goquery.Selection) {
			text.WriteString(span.Text())
			text.WriteString(" ")
		})
		audio = append(audio, findAudioFromText(text.String())...)
		y := findYearFromText(text.String(), title)
		if y != "" {
			year = y
		}
		size = append(size, findSizesFromText(text.String())...)
	})
	size = utils.StableUniq(size)

	for it, magnetLink := range magnetLinks {
		m := starckMagnet{link: magnetLink, audio: audio}
		// if the number of sizes is equal to the number of magnets, then assign the size to each magnet in order
		if len(size) == len(magnetLinks) {
			m.size = size[it]
		}
		magnets = append(magnets, m)
	}
	return title, year, magnets
}

var starckTitleRE = regexp.MustCompile(`^(.*?)(?:\s+Torrent)?\s*\((\d{4})\)`)

// parseStarckFilmesListPost parses the new layout (starckfilmesnet.com):
// h1.o-titulo "Título (ano)" and the downloads grouped by audio in
// section.o-lista (header .o-grupo-nome, one div.o-arquivo per file with its
// size in .o-arquivo-info > b).
func parseStarckFilmesListPost(doc *goquery.Document) (title, year string, magnets []starckMagnet) {
	h1 := strings.TrimSpace(doc.Find("h1").First().Text())
	title = h1
	if match := starckTitleRE.FindStringSubmatch(h1); match != nil {
		title = strings.TrimSpace(match[1])
		year = match[2]
	}

	doc.Find("section.o-lista").Each(func(_ int, group *goquery.Selection) {
		audio := starckGroupAudio(group.Find(".o-grupo-nome").First().Text())
		group.Find(".o-arquivo").Each(func(_ int, file *goquery.Selection) {
			magnetLink, _ := file.Find("a.o-baixar[href^=\"magnet\"]").Attr("href")
			if magnetLink == "" {
				return
			}
			magnets = append(magnets, starckMagnet{
				link:  magnetLink,
				size:  strings.TrimSpace(file.Find(".o-arquivo-info b").First().Text()),
				audio: audio,
			})
		})
	})
	return title, year, magnets
}

// starckGroupAudio maps an o-lista group name ("DUAL ÁUDIO", "DUBLADO",
// "NACIONAL", "LEGENDADO") to the audio it implies; subtitled groups carry
// only the original audio, which the page does not name.
func starckGroupAudio(name string) []schema.Audio {
	name = strings.ToLower(name)
	for _, token := range []string{"dual", "dublad", "nacional"} {
		if strings.Contains(name, token) {
			return []schema.Audio{schema.AudioPortuguese}
		}
	}
	return nil
}
