package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func Test_buildEraiRawsFeedURL_UsesStaticRSSURLForRecentFeed(t *testing.T) {
	t.Setenv("INDEXER_ERAI_RAWS_RSS_URL", "https://www.erai-raws.info/feed/?auth=abc")

	got := buildEraiRawsFeedURL(IndexerMeta{URL: "https://www.erai-raws.info/"}, "")
	if got != "https://www.erai-raws.info/feed/?auth=abc" {
		t.Fatalf("buildEraiRawsFeedURL() = %q", got)
	}
}

func Test_buildEraiRawsFeedURL_UsesSearchFeedWhenQueryIsProvided(t *testing.T) {
	t.Setenv("INDEXER_ERAI_RAWS_RSS_URL", "https://www.erai-raws.info/feed/?auth=abc")

	got := buildEraiRawsFeedURL(IndexerMeta{URL: "https://www.erai-raws.info/"}, "one piece")
	parsedURL, err := url.Parse(got)
	if err != nil {
		t.Fatalf("failed to parse URL: %v", err)
	}

	if parsedURL.Path != "/" {
		t.Fatalf("path = %q, want /", parsedURL.Path)
	}
	values := parsedURL.Query()
	if values.Get("s") != "one piece" {
		t.Fatalf("s = %q, want one piece", values.Get("s"))
	}
	if values.Get("feed") != "rss2" {
		t.Fatalf("feed = %q, want rss2", values.Get("feed"))
	}
	if values.Get("auth") != "abc" {
		t.Fatalf("auth = %q, want abc", values.Get("auth"))
	}
}

func Test_getEraiRawsBody_MissingTokenPointsToConfig(t *testing.T) {
	// since 2026-11 the feed answers 403 + wp_die "Invalid or missing
	// authentication token" when no token is sent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<error><code>wp_die</code><title><![CDATA[Authentication Required]]></title><message><![CDATA[Invalid or missing authentication token.]]></message><data><status>403</status></data></error>`))
	}))
	defer srv.Close()

	_, err := getEraiRawsBody(context.Background(), srv.URL+"/feed/", "")
	if err == nil || !strings.Contains(err.Error(), "ERAI_RAWS_AUTH_TOKEN") {
		t.Fatalf("getEraiRawsBody() error = %v, want a hint about ERAI_RAWS_AUTH_TOKEN", err)
	}
}
