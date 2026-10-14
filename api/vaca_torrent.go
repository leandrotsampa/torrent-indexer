package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
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

var vacaTorrent = IndexerMeta{
	Label: "vaca_torrent",
	URL:   utils.GetIndexerURLFromEnv("INDEXER_VACA_TORRENT_URL", "https://vaqueirofilmes1.com/pt/"),
	// Search results are loaded via WordPress admin-ajax (action=filtrar_busca);
	// the endpoint lives at the root of the host of URL.
	SearchURL:   "/wp-admin/admin-ajax.php",
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

	var (
		links     []string
		targetURL string
		err       error
	)
	if q == "" {
		targetURL = buildVacaTorrentURL(metadata, page)
		logging.InfoWithRequest(r).Str("target_url", targetURL).Msg("Processing indexer request")
		links, err = getVacaTorrentListingLinks(ctx, i, targetURL)
	} else {
		var form url.Values
		targetURL, form, err = buildVacaTorrentSearchRequest(metadata, q, page)
		if err == nil {
			logging.InfoWithRequest(r).Str("target_url", targetURL).Str("query", q).Msg("Processing indexer request")
			links, err = getVacaTorrentSearchLinks(ctx, i, targetURL, form, page, metadata.URL)
		}
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		err = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		if err != nil {
			logging.ErrorWithRequest(r).Err(err).Msg("Failed to encode error response")
		}
		i.metrics.IndexerErrors.WithLabelValues(metadata.Label).Inc()
		return
	}

	referer := metadata.URL
	if q == "" {
		referer = targetURL
	}

	// extract each torrent link
	indexedTorrents := utils.ParallelFlatMap(links, func(link string) ([]schema.IndexedTorrent, error) {
		return getTorrentsVacaTorrent(ctx, i, link, referer)
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

// buildVacaTorrentURL builds the listing (home/page N) URL.
func buildVacaTorrentURL(metadata IndexerMeta, page string) string {
	if page == "" || page == "1" {
		return metadata.URL
	}
	return fmt.Sprintf("%s%s", metadata.URL, fmt.Sprintf(metadata.PagePattern, page))
}

// buildVacaTorrentSearchRequest builds the admin-ajax search request. The
// classic `?s=` page renders an empty shell and loads results through
// admin-ajax (action=filtrar_busca), whose endpoint is at the root of the host
// of metadata.URL (so an INDEXER_VACA_TORRENT_URL override is respected).
func buildVacaTorrentSearchRequest(metadata IndexerMeta, query, page string) (string, url.Values, error) {
	if page == "" {
		page = "1"
	}
	base, err := url.Parse(metadata.URL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", nil, fmt.Errorf("invalid VacaTorrent URL %q", metadata.URL)
	}
	searchPath := metadata.SearchURL
	if searchPath == "" {
		searchPath = vacaTorrent.SearchURL
	}
	ajaxURL := (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: searchPath}).String()

	form := url.Values{}
	form.Set("action", "filtrar_busca")
	form.Set("s", query)
	form.Set("tipo", "todos")
	form.Set("paged", page)
	form.Set("lang", "pt-BR")
	return ajaxURL, form, nil
}

type vacaTorrentSearchResponse struct {
	HTML  string `json:"html"`
	Total int    `json:"total"`
	Pages int    `json:"pages"`
}

// parseVacaTorrentSearchResponse extracts post links from the admin-ajax
// search JSON. Pages past the last one reported by the endpoint yield nothing
// (the endpoint otherwise keeps returning results for out-of-range pages).
func parseVacaTorrentSearchResponse(body []byte, page string) ([]string, error) {
	var resp vacaTorrentSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("invalid VacaTorrent search response: %w", err)
	}
	if pageNum, err := strconv.Atoi(page); err == nil && pageNum > 1 && pageNum > resp.Pages {
		return nil, nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(resp.HTML))
	if err != nil {
		return nil, fmt.Errorf("invalid VacaTorrent search HTML: %w", err)
	}
	return extractVacaTorrentLinks(doc), nil
}

// extractVacaTorrentLinks returns the post links from a listing/search
// document: the `.i-tem_ht` cards, or, if there are none, any anchor that
// points to a movie/tv-show post (never category or pagination pages).
func extractVacaTorrentLinks(doc *goquery.Document) []string {
	var links []string
	doc.Find(".i-tem_ht").Each(func(_ int, s *goquery.Selection) {
		if link, exists := s.Find("a[href]").Attr("href"); exists && link != "" {
			links = append(links, link)
		}
	})
	if len(links) == 0 {
		doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
			if link, exists := s.Attr("href"); exists && isVacaTorrentPostURL(link) {
				links = append(links, link)
			}
		})
	}
	return utils.StableUniq(links)
}

// isVacaTorrentPostURL reports whether link is a single post, i.e.
// /pt/movie/<slug>/ or /pt/tv-shows/<slug>/ (not the category root, its
// pagination or a feed).
func isVacaTorrentPostURL(link string) bool {
	u, err := url.Parse(link)
	if err != nil {
		return false
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) != 3 || segments[0] != "pt" {
		return false
	}
	if segments[1] != "movie" && segments[1] != "tv-shows" {
		return false
	}
	return segments[2] != "" && segments[2] != "page" && segments[2] != "feed"
}

func getVacaTorrentListingLinks(ctx context.Context, i *Indexer, targetURL string) ([]string, error) {
	resp, err := i.requester.GetDocument(ctx, targetURL)
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	doc, err := goquery.NewDocumentFromReader(resp)
	if err != nil {
		return nil, err
	}

	links := extractVacaTorrentLinks(doc)
	// if no links were indexed, expire the document in cache
	if len(links) == 0 {
		_ = i.requester.ExpireDocument(ctx, targetURL)
	}
	return links, nil
}

func getVacaTorrentSearchLinks(ctx context.Context, i *Indexer, ajaxURL string, form url.Values, page, referer string) ([]string, error) {
	body, err := i.requester.PostForm(ctx, ajaxURL, form, referer)
	if err != nil {
		return nil, err
	}
	links, err := parseVacaTorrentSearchResponse(body, page)
	if err != nil || len(links) == 0 {
		// don't keep an unexpected/empty answer in the short-lived cache
		_ = i.requester.ExpirePostForm(ctx, ajaxURL, form)
	}
	return links, err
}

// cleanVacaMagnetTrackers drops `tr` parameters that are not URLs (the
// resolver chain emits e.g. "udptracker.openbittorrent.com80announce", with
// the "://", ":" and "/" stripped). The rest of the magnet is kept verbatim.
func cleanVacaMagnetTrackers(magnetLink string) string {
	prefix, query, found := strings.Cut(magnetLink, "?")
	if !found {
		return magnetLink
	}
	params := strings.Split(query, "&")
	kept := params[:0]
	changed := false
	for _, param := range params {
		if value, isTracker := strings.CutPrefix(param, "tr="); isTracker {
			decoded, err := url.QueryUnescape(value)
			if err != nil || !strings.Contains(decoded, "://") {
				changed = true
				continue
			}
		}
		kept = append(kept, param)
	}
	if !changed {
		return magnetLink
	}
	return prefix + "?" + strings.Join(kept, "&")
}

var commentRegex = regexp.MustCompile(`<!--(.*?)-->`)

var (
	vacaSystemTechLinkRE   = regexp.MustCompile(`^https?://systemtech\.space/enc/go\.php`)
	vacaSystemTechNextRE   = regexp.MustCompile(`(?i)(?:const|let|var)\s+next\s*=\s*"([^"]+)"`)
	vacaLocationReplaceRE  = regexp.MustCompile(`(?i)(?:location\.replace|window\.location\.replace)\(\s*"([^"]+)"\s*\)|(?:window\.)?location(?:\.href)?\s*=\s*"([^"]+)"`)
	vacaSecondStageURLRE   = regexp.MustCompile(`(?i)var\s+URL_ETAPA2\s*=\s*"([^"]+)"`)
	vacaSystemTechRelayURL = "https://systemtech.space/enc/relay.php"
)

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
		magnetLinks = append(magnetLinks, getVacaDataLinkMagnets(linkedDoc)...)
		magnetLinks = append(magnetLinks, getVacaSystemTechDocMagnets(ctx, i, linkedDoc, linkPage)...)
	}

	// legacy posts (root-level slugs, still returned by the search) carry the
	// SystemTech links directly in the post page
	magnetLinks = append(magnetLinks, getVacaSystemTechDocMagnets(ctx, i, doc, referer)...)

	return utils.StableUniq(magnetLinks)
}

func getVacaSystemTechDocMagnets(ctx context.Context, i *Indexer, doc *goquery.Document, pageURL string) []string {
	var hrefs []string
	doc.Find("a[href*=\"systemtech.space/enc/go.php\"]").Each(func(_ int, s *goquery.Selection) {
		if href, exists := s.Attr("href"); exists && href != "" {
			hrefs = append(hrefs, resolveVacaHref(href, pageURL))
		}
	})

	var magnetLinks []string
	for _, href := range utils.StableUniq(hrefs) {
		resolvedMagnets, err := getVacaSystemTechMagnets(ctx, i, href, pageURL)
		if err != nil {
			logging.Warn().Err(err).Str("href", href).Msg("Failed to resolve VacaTorrent SystemTech link")
			continue
		}
		magnetLinks = append(magnetLinks, resolvedMagnets...)
	}
	return magnetLinks
}

func getVacaSystemTechMagnets(ctx context.Context, i *Indexer, link, referer string) ([]string, error) {
	if !vacaSystemTechLinkRE.MatchString(link) {
		return nil, fmt.Errorf("unsupported VacaTorrent resolver link: %s", link)
	}

	cacheKey := fmt.Sprintf("vaca_torrent:systemtech:%s", link)
	cached, err := i.redis.Get(ctx, cacheKey)
	if err == nil {
		return splitCachedVacaMagnets(cached), nil
	}

	client, err := newVacaResolverClient()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	body, finalURL, err := fetchVacaResolverURL(ctx, client, link, referer)
	if err != nil {
		return nil, err
	}
	if magnets := getVacaDataLinkMagnetsFromHTML(body); len(magnets) > 0 {
		cacheVacaMagnets(ctx, i, cacheKey, magnets)
		return magnets, nil
	}

	nextURL := getVacaJSString(body, vacaSystemTechNextRE)
	relayURL := vacaSystemTechRelayURL
	if nextURL != "" {
		nextBody, nextFinalURL, err := fetchVacaResolverURL(ctx, client, nextURL, finalURL)
		if err != nil {
			logging.Debug().Err(err).Str("url", nextURL).Msg("Failed to fetch VacaTorrent t.co interstitial")
		} else {
			finalURL = nextFinalURL
			if parsedRelayURL := getVacaJSString(nextBody, vacaLocationReplaceRE); parsedRelayURL != "" {
				relayURL = parsedRelayURL
			}
		}
	}

	body, finalURL, err = fetchVacaResolverURL(ctx, client, relayURL, finalURL)
	if err != nil {
		return nil, err
	}
	if magnets := getVacaDataLinkMagnetsFromHTML(body); len(magnets) > 0 {
		cacheVacaMagnets(ctx, i, cacheKey, magnets)
		return magnets, nil
	}

	secondStageURL := getVacaJSString(body, vacaSecondStageURLRE)
	if secondStageURL == "" {
		return nil, fmt.Errorf("VacaTorrent second stage URL not found")
	}

	body, _, err = fetchVacaResolverURL(ctx, client, secondStageURL, finalURL)
	if err != nil {
		return nil, err
	}
	magnets := getVacaDataLinkMagnetsFromHTML(body)
	if len(magnets) == 0 {
		return nil, fmt.Errorf("VacaTorrent data-link magnet not found")
	}

	cacheVacaMagnets(ctx, i, cacheKey, magnets)
	return magnets, nil
}

func newVacaResolverClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create VacaTorrent cookie jar: %w", err)
	}

	return &http.Client{
		Timeout: 30 * time.Second,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			referer := ""
			if len(via) > 0 {
				referer = via[len(via)-1].URL.String()
			}
			setVacaResolverHeaders(req, referer)
			return nil
		},
	}, nil
}

func fetchVacaResolverURL(ctx context.Context, client *http.Client, targetURL, referer string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create VacaTorrent resolver request: %w", err)
	}
	setVacaResolverHeaders(req, referer)

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch VacaTorrent resolver URL %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		return nil, "", fmt.Errorf("unexpected VacaTorrent resolver status %d for %s", resp.StatusCode, targetURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read VacaTorrent resolver response: %w", err)
	}

	return body, resp.Request.URL.String(), nil
}

func setVacaResolverHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", utils.SpoofedUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "pt-BR,pt;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("DNT", "1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Cache-Control", "max-age=0")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
}

func getVacaDataLinkMagnetsFromHTML(body []byte) []string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	return getVacaDataLinkMagnets(doc)
}

func getVacaDataLinkMagnets(doc *goquery.Document) []string {
	var magnetLinks []string
	doc.Find("[data-link]").Each(func(_ int, s *goquery.Selection) {
		dataLink, exists := s.Attr("data-link")
		if !exists || strings.TrimSpace(dataLink) == "" {
			return
		}
		magnetLink, err := decodeVacaDataLink(dataLink)
		if err != nil {
			logging.Debug().Err(err).Msg("Failed to decode VacaTorrent data-link")
			return
		}
		if strings.HasPrefix(magnetLink, "magnet:") {
			magnetLinks = append(magnetLinks, magnetLink)
		}
	})
	return utils.StableUniq(magnetLinks)
}

func decodeVacaDataLink(dataLink string) (string, error) {
	dataLink = strings.TrimSpace(dataLink)
	if decoded, err := utils.Base64Decode(dataLink); err == nil {
		return html.UnescapeString(decoded), nil
	}

	padded := dataLink
	if remainder := len(padded) % 4; remainder != 0 {
		padded += strings.Repeat("=", 4-remainder)
	}

	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		decoded, err := encoding.DecodeString(padded)
		if err == nil {
			return html.UnescapeString(string(decoded)), nil
		}
	}
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		decoded, err := encoding.DecodeString(dataLink)
		if err == nil {
			return html.UnescapeString(string(decoded)), nil
		}
	}

	return "", fmt.Errorf("invalid VacaTorrent data-link")
}

func getVacaJSString(body []byte, re *regexp.Regexp) string {
	matches := re.FindSubmatch(body)
	for i := 1; i < len(matches); i++ {
		if len(matches[i]) > 0 {
			return unescapeVacaJSString(string(matches[i]))
		}
	}
	return ""
}

func unescapeVacaJSString(value string) string {
	unquoted, err := strconv.Unquote(`"` + value + `"`)
	if err == nil {
		return unquoted
	}
	return strings.ReplaceAll(value, `\/`, `/`)
}

func resolveVacaHref(href, base string) string {
	parsedHref, err := url.Parse(href)
	if err != nil || parsedHref.IsAbs() {
		return href
	}
	parsedBase, err := url.Parse(base)
	if err != nil {
		return href
	}
	return parsedBase.ResolveReference(parsedHref).String()
}

func splitCachedVacaMagnets(cached []byte) []string {
	var magnetLinks []string
	for _, magnetLink := range strings.Split(strings.TrimSpace(string(cached)), "\n") {
		if magnetLink != "" {
			magnetLinks = append(magnetLinks, magnetLink)
		}
	}
	return magnetLinks
}

func cacheVacaMagnets(ctx context.Context, i *Indexer, cacheKey string, magnetLinks []string) {
	if len(magnetLinks) == 0 {
		return
	}
	if err := i.redis.Set(ctx, cacheKey, []byte(strings.Join(magnetLinks, "\n"))); err != nil {
		logging.Debug().Err(err).Str("cache_key", cacheKey).Msg("Failed to cache VacaTorrent resolved magnets")
	}
}

func getTorrentsVacaTorrent(ctx context.Context, i *Indexer, link, referer string) ([]schema.IndexedTorrent, error) {
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
	for idx, magnetLink := range magnetLinks {
		magnetLinks[idx] = cleanVacaMagnetTrackers(magnetLink)
	}
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
				warmMagnetMetadata(ctx, i, magnetLink)
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
