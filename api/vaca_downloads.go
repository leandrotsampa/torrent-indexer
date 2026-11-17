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
)

// VacaTorrentMagnetPath is the endpoint that resolves a vaca download button
// (enc/go.php) to its magnet when a client grabs the release.
//
// Every download button of the site goes through enc/go.php, a 3-4 request
// chain behind a Cloudflare challenge, and a single search can list hundreds
// of them (one per episode and quality). Resolving them all at search time
// flooded FlareSolverr/Byparr and never finished, so the search lists each
// button from what the page states next to it (episode, audio, quality,
// size, subtitles) and the magnet is resolved only for the release the
// client actually downloads.
const VacaTorrentMagnetPath = "/indexers/vaca_torrent/magnet"

// vacaDownload is one download button of a vaca page.
type vacaDownload struct {
	link      string // enc/go.php resolver link
	season    int    // 0 on a movie
	episode   int    // 0 on a movie or a season pack
	audio     []schema.Audio
	subtitles []schema.Audio
	quality   string // e.g. "1080p", "1080p HDCAM"
	size      string // e.g. "418.6 MB"
	// name is the release name of a legacy button (an image whose alt text is
	// the full post title, quality included), used instead of the post title
	name string
}

var (
	// vacaSeasonNumberRE reads the season of a season page, from its URL
	// (/tv/<show>/season/temporada-2/) or its heading ("Temporada 2").
	vacaSeasonNumberRE = regexp.MustCompile(`(?i)temporada[\s-]*(\d+)`)
	// vacaSizeRE matches the size part of a button label ("418.6 MiB").
	vacaSizeRE = regexp.MustCompile(`(?i)^(\d+(?:[.,]\d+)?)\s*([KMGT])i?B$`)
	// vacaLegacyNoiseRE is what a legacy button's alt text adds to the name.
	vacaLegacyNoiseRE = regexp.MustCompile(`(?i)\s*-?\s*\btorrent\b|\bdownload\b|\(\s*\d{4}\s*\)`)
	// vacaQuerySeasonRE finds the "temporada N" the Prowlarr definition turns
	// S02/S02E01 into; the site's search finds nothing with it.
	vacaQuerySeasonRE = regexp.MustCompile(`(?i)\s*\btemporada\s+0*(\d+)\b`)
)

// splitVacaQuery separates the season asked for from the words the site's
// search understands: "aoashi temporada 2" searches "aoashi", season 2.
func splitVacaQuery(q string) (siteQuery string, season int) {
	if m := vacaQuerySeasonRE.FindStringSubmatch(q); m != nil {
		season, _ = strconv.Atoi(m[1])
		q = vacaQuerySeasonRE.ReplaceAllString(q, " ")
	}
	return strings.Join(strings.Fields(q), " "), season
}

// vacaSeasonOf returns the season number in text (a URL or a heading), or 0.
func vacaSeasonOf(text string) int {
	if m := vacaSeasonNumberRE.FindStringSubmatch(text); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// parseVacaDownloads lists the download buttons of a movie-links or season
// page. Each button is labelled "<audio> • <quality> • <size>", e.g.
// "Japonês • 1080p • 418.6 MiB" or "Português | Inglês • 1080p HDCAM • 4.90
// GB", and sits next to a "Subs" pill listing its subtitles.
func parseVacaDownloads(doc *goquery.Document, pageURL string) []vacaDownload {
	season := vacaSeasonOf(pageURL)
	if season == 0 {
		season = vacaSeasonOf(doc.Find(".ss-hero-title").First().Text())
	}

	var downloads []vacaDownload
	seen := map[string]bool{}
	add := func(s *goquery.Selection, episode int) {
		href, _ := s.Attr("href")
		link := resolveVacaHref(strings.TrimSpace(href), pageURL)
		if !isVacaResolverLink(link) || seen[link] {
			return
		}
		seen[link] = true

		d := parseVacaButtonLabel(s.Text())
		if strings.TrimSpace(s.Text()) == "" {
			alt, _ := s.Find("img[alt]").First().Attr("alt")
			d.name = strings.Join(strings.Fields(vacaLegacyNoiseRE.ReplaceAllString(alt, " ")), " ")
			d.name = strings.TrimRight(d.name, " -")
		}
		d.link = link
		d.season = season
		d.episode = episode
		if subs, exists := s.Closest(".dl-btn-wrap").Find("[data-subs]").First().Attr("data-subs"); exists {
			d.subtitles = parseVacaSubtitles(subs)
		}
		downloads = append(downloads, d)
	}

	episodes := doc.Find(".ss-ep")
	episodes.Each(func(_ int, ep *goquery.Selection) {
		episode, _ := strconv.Atoi(strings.TrimSpace(ep.Find(".ss-ep-num").First().Text()))
		ep.Find(`a[href*="/enc/go.php"]`).Each(func(_ int, s *goquery.Selection) { add(s, episode) })
	})
	if episodes.Length() == 0 {
		doc.Find(`a[href*="/enc/go.php"]`).Each(func(_ int, s *goquery.Selection) { add(s, 0) })
	}
	return downloads
}

// parseVacaButtonLabel reads "<audio> • <quality> • <size>".
func parseVacaButtonLabel(label string) vacaDownload {
	var d vacaDownload
	var quality []string
	for idx, part := range strings.Split(label, "•") {
		part = strings.Join(strings.Fields(part), " ")
		if part == "" {
			continue
		}
		if m := vacaSizeRE.FindStringSubmatch(part); m != nil {
			d.size = strings.ReplaceAll(m[1], ",", ".") + " " + strings.ToUpper(m[2]) + "B"
			continue
		}
		if idx == 0 {
			if audio := findNyaaLanguages(part); len(audio) > 0 {
				d.audio = audio
				continue
			}
		}
		quality = append(quality, part)
	}
	d.quality = strings.Join(quality, " ")
	return d
}

// parseVacaSubtitles reads a data-subs attribute: a JSON list such as
// ["English","(CR_English)","ASS","Portuguese",...] or ["PT-BR"].
func parseVacaSubtitles(raw string) []schema.Audio {
	var entries []string
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil
	}
	return findNyaaLanguages(strings.Join(entries, " | "))
}

// vacaReleaseTitle builds the release name Sonarr/Radarr parse:
// "Aoashi S02E01 1080p" or "A Odisseia 2026 1080p HDCAM".
func vacaReleaseTitle(name, year string, d vacaDownload) string {
	// a legacy alt text is only a name when it carries the title (some read
	// just "MAGNET LINK")
	if d.name != "" && strings.Contains(strings.ToLower(d.name), strings.ToLower(name)) {
		name = d.name
	}
	parts := []string{name}
	switch {
	case d.season > 0 && d.episode > 0:
		parts = append(parts, fmt.Sprintf("S%02dE%02d", d.season, d.episode))
	case d.season > 0:
		parts = append(parts, fmt.Sprintf("S%02d", d.season))
	case year != "" && !strings.Contains(name, year):
		parts = append(parts, year)
	}
	if d.quality != "" {
		parts = append(parts, d.quality)
	}
	return strings.Join(parts, " ")
}

// vacaMagnetDownloadLink is the link a client follows to grab d: this
// service's resolver endpoint, reached at base.
func vacaMagnetDownloadLink(base, resolverLink string) string {
	return base + VacaTorrentMagnetPath + "?link=" + url.QueryEscape(resolverLink)
}

// vacaPost is what a post page says about all its releases.
type vacaPost struct {
	link    string
	title   string
	year    string
	imdb    string
	audio   []schema.Audio
	date    time.Time
	baseURL string
}

// vacaDownloadTorrent turns a download button into a release. The magnet is
// filled in when a previous grab already resolved it (cached); otherwise the
// release carries the on-demand DownloadLink and no peer counts.
func vacaDownloadTorrent(ctx context.Context, i *Indexer, post vacaPost, d vacaDownload) schema.IndexedTorrent {
	audio := d.audio
	if len(audio) == 0 {
		audio = post.audio
	}
	season := ""
	if d.season > 0 {
		season = strconv.Itoa(d.season)
	}

	it := schema.IndexedTorrent{
		Title:               vacaReleaseTitle(post.title, post.year, d),
		OriginalTitle:       processVacaTorrentTitle(post.title, audio, season),
		Details:             post.link,
		Year:                post.year,
		IMDB:                post.imdb,
		Audio:               audio,
		Subtitles:           d.subtitles,
		LanguagesFromSource: len(d.audio) > 0,
		Date:                post.date,
		Size:                d.size,
		DownloadLink:        vacaMagnetDownloadLink(post.baseURL, d.link),
		PeersUnknown:        true,
	}

	cached, err := i.redis.Get(ctx, vacaResolverCacheKey(d.link))
	if err != nil {
		return it
	}
	magnets := splitCachedVacaMagnets(cached)
	if len(magnets) == 0 {
		return it
	}
	magnetLink := cleanVacaMagnetTrackers(magnets[0])
	parsed, err := magnet.ParseMagnetUri(magnetLink)
	if err != nil {
		return it
	}
	it.MagnetLink = magnetLink
	it.InfoHash = parsed.InfoHash.String()
	it.Trackers = parsed.Trackers
	if peer, seed, err := goscrape.GetLeechsAndSeeds(ctx, i.redis, i.metrics, it.InfoHash, it.Trackers); err == nil {
		it.LeechCount, it.SeedCount = peer, seed
		it.PeersUnknown = false
	}
	return it
}

// isVacaOwnResolverLink reports whether link is an enc/go.php link of the vaca
// site (or of the resolver host it used before), so the endpoint never
// fetches arbitrary URLs.
func isVacaOwnResolverLink(link string) bool {
	if !isVacaResolverLink(link) {
		return false
	}
	parsed, err := url.Parse(link)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "systemtech.space" {
		return true
	}
	site, err := url.Parse(vacaTorrent.URL)
	return err == nil && host == strings.ToLower(site.Hostname())
}

// HandlerVacaTorrentMagnet resolves a download button and redirects to its
// magnet, which is how Sonarr/Radarr and Prowlarr take a magnet from a
// download link.
func (i *Indexer) HandlerVacaTorrentMagnet(w http.ResponseWriter, r *http.Request) {
	link := r.URL.Query().Get("link")
	if !isVacaOwnResolverLink(link) {
		writeVacaMagnetError(w, http.StatusBadRequest, fmt.Errorf("not a VacaTorrent download link: %q", link))
		return
	}

	magnets, err := getVacaSystemTechMagnets(r.Context(), i, link, vacaTorrent.URL)
	if err == nil && len(magnets) == 0 {
		err = fmt.Errorf("no magnet behind %s", link)
	}
	if err != nil {
		logging.ErrorWithRequest(r).Err(err).Str("link", link).Msg("Failed to resolve VacaTorrent download link")
		i.metrics.IndexerErrors.WithLabelValues(vacaTorrent.Label).Inc()
		writeVacaMagnetError(w, http.StatusBadGateway, err)
		return
	}

	http.Redirect(w, r, cleanVacaMagnetTrackers(magnets[0]), http.StatusFound)
}

func writeVacaMagnetError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
