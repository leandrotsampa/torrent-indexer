package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/leandrotsampa/torrent-indexer/logging"
	"github.com/leandrotsampa/torrent-indexer/schema"
	"github.com/leandrotsampa/torrent-indexer/utils"
)

// writeIndexerError answers an indexer request that failed with a 500 carrying
// the error. The error is also logged: the response only reaches the client
// (Prowlarr), so without this a failing indexer leaves no trace of the cause.
func writeIndexerError(w http.ResponseWriter, r *http.Request, i *Indexer, label string, err error) {
	logging.ErrorWithRequest(r).Err(err).Str("indexer", label).Msg("Indexer request failed")

	w.WriteHeader(http.StatusInternalServerError)
	if encodeErr := json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); encodeErr != nil {
		logging.ErrorWithRequest(r).Err(encodeErr).Msg("Failed to encode error response")
	}
	i.metrics.IndexerErrors.WithLabelValues(label).Inc()
}

// getDocument retrieves a document from the cache or makes a request to get it.
// It first checks the Redis cache for the document body.
func getDocument(ctx context.Context, i *Indexer, link, referer string) (*goquery.Document, error) {
	// try to get from redis first
	docCache, err := i.redis.Get(ctx, link)
	if err == nil {
		i.metrics.CacheHits.WithLabelValues("document_body").Inc()
		logging.Debug().Str("url", link).Msg("Returning document from long-lived cache")
		return goquery.NewDocumentFromReader(io.NopCloser(bytes.NewReader(docCache)))
	}
	defer i.metrics.CacheMisses.WithLabelValues("document_body").Inc()

	resp, err := i.requester.GetDocument(ctx, link, referer)
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	body, err := io.ReadAll(resp)
	if err != nil {
		return nil, err
	}

	// set cache
	err = i.redis.Set(ctx, link, body)
	if err != nil {
		logging.Error().Err(err).Str("url", link).Msg("Failed to set document body in redis cache")
	}

	doc, err := goquery.NewDocumentFromReader(io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return nil, err
	}

	return doc, nil
}

// magnetMetadataInlineTimeoutDefault bounds how long a search waits for the
// metadata lookup (size/name), which resolves the torrent through the swarm and
// is therefore unpredictable. The lookup itself keeps running after the search
// gives up on it (see magnet.Resolver.FetchMetadata), so the next search is
// served from cache.
const magnetMetadataInlineTimeoutDefault = 3 * time.Second

func magnetMetadataInlineTimeout() time.Duration {
	seconds, err := strconv.Atoi(utils.GetEnvOrDefault("MAGNET_METADATA_API_INLINE_TIMEOUT_SECONDS", "0"))
	if err != nil || seconds <= 0 {
		return magnetMetadataInlineTimeoutDefault
	}

	return time.Duration(seconds) * time.Second
}

// warmMagnetMetadata starts resolving the magnet metadata while the indexer is
// still parsing, so FullfilMissingMetadata finds it cached or joins the same
// lookup instead of starting another. The resolver logs the outcome once per
// lookup, so failures are not logged here — every caller sharing a lookup
// would repeat it.
func warmMagnetMetadata(ctx context.Context, i *Indexer, magnetLink string) {
	if !i.magnetMetadataAPI.IsEnabled() {
		return
	}

	detachedCtx := context.WithoutCancel(ctx)
	go func() {
		_, _ = i.magnetMetadataAPI.FetchMetadata(detachedCtx, magnetLink)
	}()
}

func getPublishedDateFromMeta(document *goquery.Document) time.Time {
	var date time.Time
	//<meta property="article:published_time" content="2019-08-23T13:20:57+00:00">
	datePublished := strings.TrimSpace(document.Find("meta[property=\"article:published_time\"]").AttrOr("content", ""))
	if datePublished == "" {
		// <meta property="og:updated_time" content="2025-09-30T13:08:58-03:00">
		datePublished = strings.TrimSpace(document.Find("meta[property=\"og:updated_time\"]").AttrOr("content", ""))
	}

	if datePublished == "" {
		// type="application/ld+json" with "datePublished" attribute on json
		scriptTags := document.Find("script[type=\"application/ld+json\"]")
		scriptTags.EachWithBreak(func(i int, s *goquery.Selection) bool {
			scriptContent := s.Text()
			mapData := make(map[string]interface{})
			err := json.Unmarshal([]byte(scriptContent), &mapData)
			if err != nil {
				return true // continue
			}
			if dateVal, ok := mapData["datePublished"].(string); ok {
				datePublished = dateVal
				return false // break
			}
			return true // continue
		})
	}

	if datePublished != "" {
		date, _ = time.Parse(time.RFC3339, datePublished)
	}

	return date
}

type datePattern struct {
	regex  *regexp.Regexp
	layout string
}

var datePatterns = []datePattern{
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}`), "2006-01-02"},
	{regexp.MustCompile(`\d{2}-\d{2}-\d{4}`), "02-01-2006"},
	{regexp.MustCompile(`\d{2}/\d{2}/\d{4}`), "02/01/2006"},
	// Release Date: 4, October
	{regexp.MustCompile(`\d{1,2},? [A-Za-z]+`), "2, January"},
	// Release Date: October 4, 2020
	{regexp.MustCompile(`[A-Za-z]+ \d{1,2},? \d{4}`), "January 2, 2006"},
}

// getPublishedDateFromRawString extracts the date from a raw string using predefined patterns.
func getPublishedDateFromRawString(dateStr string) time.Time {
	for _, p := range datePatterns {
		match := p.regex.FindString(dateStr)

		if match != "" {
			date, err := time.Parse(p.layout, match)
			if err == nil {
				return date.UTC()
			}
		}
	}

	return time.Time{}
}

// getSeparator returns the separator used in the string.
// It checks for common separators like "|", ",", "/", and " e "
func getSeparator(s string) string {
	if strings.Contains(s, "|") {
		return "|"
	} else if strings.Contains(s, ",") {
		return ","
	} else if strings.Contains(s, "/") {
		return "/"
	} else if strings.Contains(s, " e ") {
		return " e "
	}
	return " "
}

// dualAudioTokens mark "dual audio" content, which on these Brazilian sites
// always includes Portuguese plus another language. The other language, when
// named, is captured separately; here we ensure Portuguese is detected.
var dualAudioTokens = map[string]struct{}{
	"dual":       {},
	"dual audio": {},
	"dual áudio": {},
}

// noiseTokens are leftovers of splitting the "Áudio"/"Idioma" field that carry
// no language information, so they should neither map to a language nor warn.
var noiseTokens = map[string]struct{}{
	"audio": {},
	"áudio": {},
	"–":     {},
	"-":     {},
}

// isDualAudioToken reports whether the token marks dual-audio (implies Portuguese).
func isDualAudioToken(lang string) bool {
	_, ok := dualAudioTokens[strings.ToLower(lang)]
	return ok
}

// isNoiseToken reports whether the token is a known non-language artifact.
func isNoiseToken(lang string) bool {
	_, ok := noiseTokens[strings.ToLower(lang)]
	return ok
}

// findAudioFromText extracts audio languages from a given text.
// It looks for patterns like "Áudio: Português, Inglês" or "Idioma: Português, Inglês"
func findAudioFromText(text string) []schema.Audio {
	var audio []schema.Audio
	re := regexp.MustCompile(`(.udio|Idioma|Languages):.?(.*)`)
	audioMatch := re.FindStringSubmatch(text)
	if len(audioMatch) > 0 {
		sep := getSeparator(audioMatch[2])
		langs_raw := strings.Split(audioMatch[2], sep)
		for _, lang := range langs_raw {
			lang = strings.TrimSpace(lang)
			a := schema.GetAudioFromString(lang)
			if a != nil {
				audio = append(audio, *a)
			} else if isDualAudioToken(lang) {
				// "Dual Áudio" implies Portuguese; the other language, if named,
				// is captured from its own token.
				audio = append(audio, schema.AudioPortuguese)
			} else if lang != "" && !isNoiseToken(lang) {
				logging.Warn().
					Str("language", lang).
					Msg("Unknown language detected")
				logging.Debug().Str("text", text).Msg("Unknown language detected from this text")
			}
		}
	}
	return audio
}

// findYearFromText extracts the year from a given text.
// It looks for patterns like "Lançamento: 2001" in the title.
func findYearFromText(text string, title string) (year string) {
	re := regexp.MustCompile(`(?:Lançamento|Year): (.*)`)
	yearMatch := re.FindStringSubmatch(text)
	if len(yearMatch) > 0 {
		lancamentoText := strings.TrimSpace(yearMatch[1])
		// Extract 4-digit year from the lançamento field
		yearRe := regexp.MustCompile(`\b(\d{4})\b`)
		if yearDigits := yearRe.FindStringSubmatch(lancamentoText); len(yearDigits) > 0 {
			year = yearDigits[1]
		}
	}

	if year == "" {
		re = regexp.MustCompile(`\((\d{4})\)`)
		yearMatch := re.FindStringSubmatch(title)
		if len(yearMatch) > 0 {
			year = yearMatch[1]
		}
	}

	return year
}

// findSizesFromText extracts sizes from a given text.
// It looks for patterns like "Tamanho: 1.26 GB" or "Tamanho: 700 MB".
func findSizesFromText(text string) []string {
	var sizes []string
	// everything that ends with GB or MB, using ',' or '.' as decimal separator
	re := regexp.MustCompile(`(\d+[\.,]?\d+) ?(GB|MB)`)
	sizesMatch := re.FindAllStringSubmatch(text, -1)
	if len(sizesMatch) > 0 {
		for _, size := range sizesMatch {
			sizes = append(sizes, size[0])
		}
	}
	return sizes
}

var imdbLinkRE = regexp.MustCompile(`https://www.imdb.com(/[a-z]{2})?/title/(tt\d+)/?`)
var subtitlesHintIMDBLinkRE = regexp.MustCompile(`imdbid-((tt)?\d+)`)

// getIMDBLink extracts the IMDB link from a given link.
// It looks for patterns like "https://www.imdb.com/title/tt1234567/".
// Returns an error if no valid IMDB link is found.
func getIMDBLink(link string) (string, error) {
	var imdbLink string

	matches := imdbLinkRE.FindStringSubmatch(link)
	if len(matches) > 0 {
		imdbLink = matches[0]
	} else if matches := subtitlesHintIMDBLinkRE.FindStringSubmatch(link); len(matches) > 0 {
		id := strings.TrimPrefix(matches[1], "tt")
		imdbLink = fmt.Sprintf("https://www.imdb.com/title/tt%s/", id)
	} else {
		return "", fmt.Errorf("no imdb link found")
	}
	return imdbLink, nil
}

var (
	redirectAdIDRE      = regexp.MustCompile(`redirect\s*=\s*"[^"]*[?&]id=([^"&]+)`)
	systemAdsMagnetRE   = regexp.MustCompile(`magnet:\?[^"'<>\s]+`)
	escapedURLAmpersand = strings.NewReplacer(`\u0026`, "&", `\x26`, "&")
)

func extractSystemAdsMagnet(bodyText string) string {
	magnetLink := systemAdsMagnetRE.FindString(bodyText)
	if magnetLink == "" {
		return ""
	}

	magnetLink = escapedURLAmpersand.Replace(magnetLink)
	magnetLink = html.UnescapeString(magnetLink)

	return strings.TrimSpace(magnetLink)
}

// adGatewayLinkRE matches ad-gateway "go/links.php?id=" URLs that hide the real
// magnet behind an ad redirect chain (e.g. systemads.net -> videosad.net, which
// serves the magnet in the final page body). Host-agnostic so it survives the
// frequent domain changes of these gateways.
var adGatewayLinkRE = regexp.MustCompile(`(?i)^https?://[^/]+/(?:go|links?|out|get)\.php\?[^"']*\bid=`)

// isAdGatewayLink reports whether href is an ad-gateway link resolvable to a
// magnet via getMagnetFromSystemAds.
func isAdGatewayLink(href string) bool {
	return adGatewayLinkRE.MatchString(strings.TrimSpace(href))
}

func getMagnetFromSystemAds(ctx context.Context, i *Indexer, link, referer string) (string, error) {
	// Fast path: some gateways (e.g. systemads1.com) embed the real link as an
	// AES-GCM payload in the "id" query param, decodable offline with no request.
	if u, err := url.Parse(link); err == nil {
		if id := u.Query().Get("id"); id != "" {
			if decoded, derr := utils.DecodeDarkmahouAdLink(id, ""); derr == nil && strings.HasPrefix(decoded, "magnet:") {
				return decoded, nil
			}
		}
	}

	resp, err := i.requester.GetDocument(ctx, link, referer)
	if err != nil {
		return "", err
	}
	defer resp.Close()

	body, err := io.ReadAll(resp)
	if err != nil {
		return "", err
	}

	bodyText := html.UnescapeString(string(body))
	if magnetLink := extractSystemAdsMagnet(bodyText); magnetLink != "" {
		return magnetLink, nil
	}

	matches := redirectAdIDRE.FindStringSubmatch(bodyText)
	if len(matches) < 2 {
		return "", fmt.Errorf("redirectad id not found")
	}

	id, err := url.QueryUnescape(matches[1])
	if err != nil {
		return "", err
	}

	magnetLink, err := utils.DecodeAdLink(id)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(magnetLink, "magnet:") {
		return "", fmt.Errorf("decoded redirectad link is not a magnet")
	}

	return magnetLink, nil
}

func appendTrackersToMagnetLink(magnetLink string, trackers []string) string {
	if len(trackers) == 0 || !strings.HasPrefix(magnetLink, "magnet:?") {
		return magnetLink
	}

	parsedURL, err := url.Parse(magnetLink)
	if err != nil || parsedURL.Scheme != "magnet" {
		return magnetLink
	}

	query := parsedURL.Query()
	existingTrackers := map[string]bool{}
	for _, tracker := range query["tr"] {
		existingTrackers[tracker] = true
	}

	var encodedTrackers []string
	for _, tracker := range trackers {
		tracker = strings.TrimSpace(tracker)
		if tracker == "" || existingTrackers[tracker] {
			continue
		}

		encodedTrackers = append(encodedTrackers, "tr="+url.QueryEscape(tracker))
		existingTrackers[tracker] = true
	}

	if len(encodedTrackers) == 0 {
		return magnetLink
	}

	separator := "&"
	if strings.HasSuffix(magnetLink, "?") || strings.HasSuffix(magnetLink, "&") {
		separator = ""
	}

	return magnetLink + separator + strings.Join(encodedTrackers, "&")
}

// appendAudioISO639_2Code appends the audio languages to the title in ISO 639-2 code format.
// It formats the title to include the audio languages in parentheses.
// Example: "Movie Title (eng, por)"
func appendAudioISO639_2Code(title string, a []schema.Audio) string {
	return appendReleaseTags(title, a, false)
}

// subtitledTag marks releases without Portuguese audio, e.g. "(eng, legendado)",
// so they can be told apart (and filtered) from dubbed ones by the title alone.
const subtitledTag = "legendado"

// subtitledMarkerRE matches the "Legendado"/"Legendada" words and the uppercase
// "LEG" release tag (case-sensitive, so words like "Legacy" or "leg" don't match).
var subtitledMarkerRE = regexp.MustCompile(`(?i:\blegendad[oa]s?\b)|\bLEG\b`)

// appendReleaseTags appends the audio ISO 639-2 codes and, when subtitled, the
// subtitledTag to the title between parentheses.
func appendReleaseTags(title string, a []schema.Audio, subtitled bool) string {
	tags := []string{}
	for _, lang := range a {
		tags = append(tags, lang.String())
	}
	tags = slices.Compact(tags)
	if subtitled {
		tags = append(tags, subtitledTag)
	}
	if len(tags) > 0 {
		title = fmt.Sprintf("%s (%s)", title, strings.Join(tags, ", "))
	}
	return title
}

// isSubtitledRelease reports whether a release is subtitled rather than dubbed:
// never when it carries Portuguese audio; otherwise when the audio is known
// (so it is the original one) or a name says "Legendado"/"LEG".
func isSubtitledRelease(audio []schema.Audio, names ...string) bool {
	if hasPortugueseAudio(audio) {
		return false
	}
	if len(audio) > 0 {
		return true
	}
	for _, name := range names {
		if subtitledMarkerRE.MatchString(name) {
			return true
		}
	}
	return false
}

// isSubtitledFromSource is isSubtitledRelease for languages read from the
// release page: subtitled means Portuguese subtitles without Portuguese audio,
// so a release subtitled only in English is not tagged.
func isSubtitledFromSource(audio, subtitles []schema.Audio) bool {
	return !hasPortugueseAudio(audio) && hasPortugueseAudio(subtitles)
}

// hasPortugueseAudio reports whether the list carries Portuguese, under any of
// its synonyms.
func hasPortugueseAudio(list []schema.Audio) bool {
	portuguese := schema.Audio(schema.AudioPortuguese).LanguageName()
	for _, a := range list {
		if a.LanguageName() == portuguese {
			return true
		}
	}
	return false
}

// getAudioFromTitle extracts audio languages from the release title.
// It checks for common patterns like "nacional", "dual", or "dublado"
func getAudioFromTitle(releaseTitle string, audioFromContent []schema.Audio) []schema.Audio {
	magnetAudio := []schema.Audio{}
	isNacional := strings.Contains(strings.ToLower(releaseTitle), "nacional")
	if isNacional {
		magnetAudio = append(magnetAudio, schema.AudioPortuguese)
	}

	if strings.Contains(strings.ToLower(releaseTitle), "dual") || strings.Contains(strings.ToLower(releaseTitle), "dublado") || isMultiAudioTitle(releaseTitle) {
		magnetAudio = append(magnetAudio, audioFromContent...)
		// if Portuguese audio is not in the audio slice, append it
		if !slices.Contains(magnetAudio, schema.AudioPortuguese) {
			magnetAudio = append(magnetAudio, schema.AudioPortuguese)
		}
	} else if len(audioFromContent) > 1 {
		// remove portuguese audio, and append to magnetAudio
		for _, a := range audioFromContent {
			if a != schema.AudioPortuguese {
				magnetAudio = append(magnetAudio, a)
			}
		}
	} else {
		magnetAudio = append(magnetAudio, audioFromContent...)
	}

	// order and uniq the audio slice
	slices.SortFunc(magnetAudio, func(a, b schema.Audio) int {
		return strings.Compare(a.String(), b.String())
	})
	magnetAudio = slices.CompactFunc(magnetAudio, func(a, b schema.Audio) bool {
		return a.String() == b.String()
	})

	return magnetAudio
}
