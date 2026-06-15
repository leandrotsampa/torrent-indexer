package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/Erickfb/torrent-indexer/logging"
	"github.com/Erickfb/torrent-indexer/magnet"
	"github.com/Erickfb/torrent-indexer/schema"
	goscrape "github.com/Erickfb/torrent-indexer/scrape"
	"github.com/Erickfb/torrent-indexer/utils"
)

var eraiRaws = IndexerMeta{
	Label:       "erai_raws",
	URL:         utils.GetIndexerURLFromEnv("INDEXER_ERAI_RAWS_URL", "https://www.erai-raws.info/"),
	SearchURL:   "?s=",
	PagePattern: "page/%s/",
}

var eraiRawsMagnetRE = regexp.MustCompile(`magnet:\?[^"'<>\s]+`)

type eraiRawsRSS struct {
	Channel eraiRawsRSSChannel `xml:"channel"`
}

type eraiRawsRSSChannel struct {
	Items []eraiRawsFeedItem `xml:"item"`
}

type eraiRawsAtom struct {
	Entries []eraiRawsAtomEntry `xml:"entry"`
}

type eraiRawsAtomEntry struct {
	Title   string              `xml:"title"`
	ID      string              `xml:"id"`
	Updated string              `xml:"updated"`
	Summary string              `xml:"summary"`
	Content string              `xml:"content"`
	Links   []eraiRawsAtomLink  `xml:"link"`
}

type eraiRawsAtomLink struct {
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr"`
	Rel  string `xml:"rel,attr"`
}

type eraiRawsFeedItem struct {
	Title       string                   `xml:"title"`
	Link        string                   `xml:"link"`
	GUID        string                   `xml:"guid"`
	PubDate     string                   `xml:"pubDate"`
	Description string                   `xml:"description"`
	Content     string                   `xml:"encoded"`
	Enclosures  []eraiRawsRSSEnclosure   `xml:"enclosure"`
}

type eraiRawsRSSEnclosure struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

func (i *Indexer) HandlerEraiRawsIndexer(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	metadata := eraiRaws

	defer func() {
		i.metrics.IndexerDuration.WithLabelValues(metadata.Label).Observe(time.Since(start).Seconds())
		i.metrics.IndexerRequests.WithLabelValues(metadata.Label).Inc()
	}()

	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	targetURL := buildEraiRawsFeedURL(metadata, q)

	logging.InfoWithRequest(r).Str("target_url", targetURL).Msg("Processing indexer request")
	torrents, err := getEraiRawsTorrents(ctx, i, targetURL, metadata.URL)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		err = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		if err != nil {
			logging.ErrorWithRequest(r).Err(err).Msg("Failed to encode error response")
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

func buildEraiRawsFeedURL(metadata IndexerMeta, q string) string {
	rssURL := os.Getenv("INDEXER_ERAI_RAWS_RSS_URL")
	if rssURL != "" && q == "" {
		return withEraiRawsToken(rssURL)
	}

	baseURL := metadata.URL
	if rssURL != "" {
		baseURL = rssURL
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return withEraiRawsToken(baseURL)
	}

	base.Path = "/feed/"
	if q != "" {
		base.Path = "/"
		values := base.Query()
		values.Set("s", q)
		values.Set("feed", "rss2")
		base.RawQuery = values.Encode()
	}

	return withEraiRawsToken(base.String())
}

func withEraiRawsToken(rawURL string) string {
	token := os.Getenv("ERAI_RAWS_AUTH_TOKEN")
	if token == "" {
		return rawURL
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	queryParam := utils.GetEnvOrDefault("ERAI_RAWS_AUTH_QUERY_PARAM", "token")
	values := parsedURL.Query()
	values.Set(queryParam, token)
	parsedURL.RawQuery = values.Encode()
	return parsedURL.String()
}

func getEraiRawsTorrents(ctx context.Context, i *Indexer, feedURL, referer string) ([]schema.IndexedTorrent, error) {
	body, err := getEraiRawsBody(ctx, feedURL, referer)
	if err != nil {
		return nil, err
	}

	items, err := parseEraiRawsFeed(body)
	if err != nil {
		return nil, err
	}

	indexedTorrents := utils.ParallelFlatMap(items, func(item eraiRawsFeedItem) ([]schema.IndexedTorrent, error) {
		magnetLinks := extractEraiRawsMagnets(strings.Join([]string{
			item.Link,
			item.GUID,
			item.Description,
			item.Content,
			eraiRawsEnclosuresText(item.Enclosures),
		}, " "))

		if len(magnetLinks) == 0 && item.Link != "" {
			linkedMagnets, err := getEraiRawsPageMagnets(ctx, item.Link, feedURL)
			if err != nil {
				logging.Warn().Err(err).Str("url", item.Link).Msg("Failed to fetch Erai-raws item page")
			}
			magnetLinks = append(magnetLinks, linkedMagnets...)
		}

		magnetLinks = utils.StableUniq(magnetLinks)
		return buildEraiRawsIndexedTorrents(ctx, i, item, magnetLinks), nil
	})

	return indexedTorrents, nil
}

func getEraiRawsBody(ctx context.Context, targetURL, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", utils.SpoofedUserAgent)
	req.Header.Set("Accept", "application/rss+xml,application/xml,text/xml,text/html;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	username := os.Getenv("ERAI_RAWS_USERNAME")
	password := os.Getenv("ERAI_RAWS_PASSWORD")
	if username != "" && password != "" {
		req.SetBasicAuth(username, password)
	}
	if cookie := os.Getenv("ERAI_RAWS_COOKIE"); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if header := os.Getenv("ERAI_RAWS_AUTH_HEADER"); header != "" {
		if token := os.Getenv("ERAI_RAWS_AUTH_TOKEN"); token != "" {
			req.Header.Set(header, token)
		}
	}

	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("erai-raws request failed with status %d: %s", resp.StatusCode, summarizeEraiRawsBody(body))
	}
	if isEraiRawsAuthBlocked(body) {
		return nil, fmt.Errorf("erai-raws requires authentication; set INDEXER_ERAI_RAWS_RSS_URL or ERAI_RAWS_AUTH_TOKEN/ERAI_RAWS_USERNAME/ERAI_RAWS_PASSWORD")
	}

	return body, nil
}

func parseEraiRawsFeed(body []byte) ([]eraiRawsFeedItem, error) {
	var rss eraiRawsRSS
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		return rss.Channel.Items, nil
	}

	var atom eraiRawsAtom
	if err := xml.Unmarshal(body, &atom); err == nil && len(atom.Entries) > 0 {
		items := make([]eraiRawsFeedItem, 0, len(atom.Entries))
		for _, entry := range atom.Entries {
			link := entry.ID
			var enclosures []eraiRawsRSSEnclosure
			for _, atomLink := range entry.Links {
				if atomLink.Href == "" {
					continue
				}
				if atomLink.Rel == "" || atomLink.Rel == "alternate" {
					link = atomLink.Href
				}
				enclosures = append(enclosures, eraiRawsRSSEnclosure{
					URL:  atomLink.Href,
					Type: atomLink.Type,
				})
			}
			items = append(items, eraiRawsFeedItem{
				Title:       entry.Title,
				Link:        link,
				GUID:        entry.ID,
				PubDate:     entry.Updated,
				Description: strings.Join([]string{entry.Summary, entry.Content}, " "),
				Enclosures:  enclosures,
			})
		}
		return items, nil
	}

	return nil, fmt.Errorf("failed to parse erai-raws feed")
}

func getEraiRawsPageMagnets(ctx context.Context, link, referer string) ([]string, error) {
	body, err := getEraiRawsBody(ctx, link, referer)
	if err != nil {
		return nil, err
	}

	magnetLinks := extractEraiRawsMagnets(string(body))
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return magnetLinks, nil
	}

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if exists {
			magnetLinks = append(magnetLinks, extractEraiRawsMagnets(href)...)
		}
	})

	return utils.StableUniq(magnetLinks), nil
}

func buildEraiRawsIndexedTorrents(ctx context.Context, i *Indexer, item eraiRawsFeedItem, magnetLinks []string) []schema.IndexedTorrent {
	var indexedTorrents []schema.IndexedTorrent
	date := parseEraiRawsDate(item.PubDate)
	size := ""
	sizes := findSizesFromText(strings.Join([]string{item.Title, item.Description}, " "))
	if len(sizes) > 0 {
		size = sizes[0]
	}

	for _, magnetLink := range magnetLinks {
		parsedMagnet, err := magnet.ParseMagnetUri(magnetLink)
		if err != nil {
			logging.Warn().Err(err).Str("magnet_link", magnetLink).Msg("Failed to parse Erai-raws magnet URI")
			continue
		}

		releaseTitle := strings.TrimSpace(parsedMagnet.DisplayName)
		if releaseTitle == "" {
			releaseTitle = strings.TrimSpace(item.Title)
		}
		audio := getAudioFromTitle(releaseTitle, append(findAudioFromText(releaseTitle), schema.AudioJapanese))
		infoHash := parsedMagnet.InfoHash.String()
		peer, seed, err := goscrape.GetLeechsAndSeeds(ctx, i.redis, i.metrics, infoHash, parsedMagnet.Trackers)
		if err != nil {
			logging.Error().Err(err).Str("info_hash", infoHash).Msg("Failed to get leechers and seeders")
		}

		indexedTorrents = append(indexedTorrents, schema.IndexedTorrent{
			Title:         releaseTitle,
			OriginalTitle: strings.TrimSpace(item.Title),
			Details:       firstNonEmpty(item.Link, item.GUID),
			Audio:         audio,
			MagnetLink:    magnetLink,
			Date:          date,
			InfoHash:      infoHash,
			Trackers:      parsedMagnet.Trackers,
			LeechCount:    peer,
			SeedCount:     seed,
			Size:          size,
		})
	}

	return indexedTorrents
}

func extractEraiRawsMagnets(text string) []string {
	candidates := []string{html.UnescapeString(text)}
	if unescaped, err := url.QueryUnescape(text); err == nil {
		candidates = append(candidates, html.UnescapeString(unescaped))
	}

	var magnetLinks []string
	for _, candidate := range candidates {
		matches := eraiRawsMagnetRE.FindAllString(candidate, -1)
		for _, match := range matches {
			magnetLinks = append(magnetLinks, strings.TrimRight(match, ".,;)]}"))
		}
	}

	return utils.StableUniq(magnetLinks)
}

func eraiRawsEnclosuresText(enclosures []eraiRawsRSSEnclosure) string {
	var parts []string
	for _, enclosure := range enclosures {
		parts = append(parts, enclosure.URL, enclosure.Type)
	}
	return strings.Join(parts, " ")
}

func parseEraiRawsDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}

	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		time.RFC3339,
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		date, err := time.Parse(layout, raw)
		if err == nil {
			return date
		}
	}
	return time.Time{}
}

func isEraiRawsAuthBlocked(body []byte) bool {
	content := strings.ToLower(string(body))
	blockedMessages := []string{
		"authentication required",
		"invalid or missing authentication token",
		"access denied",
		"you have been locked out",
		"unlock me",
	}
	for _, message := range blockedMessages {
		if strings.Contains(content, message) {
			return true
		}
	}
	return false
}

func summarizeEraiRawsBody(body []byte) string {
	summary := strings.TrimSpace(html.UnescapeString(string(body)))
	summary = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(summary, " ")
	summary = regexp.MustCompile(`\s+`).ReplaceAllString(summary, " ")
	if len(summary) > 180 {
		return summary[:180]
	}
	return summary
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
