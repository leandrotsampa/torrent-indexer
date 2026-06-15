package handler

import (
	"net/url"
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
