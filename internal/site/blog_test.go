package site

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/content"
)

func TestExcerptOf(t *testing.T) {
	if got := excerptOf(content.Post{Excerpt: "Hand written", Body: "<p>ignored</p>"}); got != "Hand written" {
		t.Errorf("author excerpt: %q", got)
	}
	if got := excerptOf(content.Post{Body: "<p>Fish &amp; <b>chips</b></p>"}); got != "Fish & chips" {
		t.Errorf("derived excerpt must be real text (templates escape it): %q", got)
	}
	long := excerptOf(content.Post{Body: "<p>" + strings.Repeat("word ", 100) + "</p>"})
	if r := []rune(long); len(r) != excerptLength+1 || !strings.HasSuffix(long, "…") {
		t.Errorf("long excerpt: %d runes", len(r))
	}
}

func TestSummariseSanitisesCover(t *testing.T) {
	pub := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	s := summarise(content.Post{Title: "T", Slug: "t", Cover: "javascript:alert(1)", PublishedAt: &pub,
		Categories: []content.Category{{Slug: "news", Name: "News"}}})
	if s.Cover != "" || s.URL != "/blog/t" || s.Date != "5 March 2026" || s.Categories[0].URL != "/blog/category/news" {
		t.Errorf("%+v", s)
	}
	draft := summarise(content.Post{Title: "D", Slug: "d", UpdatedAt: pub})
	if draft.Date != "5 March 2026" {
		t.Errorf("unpublished posts fall back to the updated date: %q", draft.Date)
	}
}

func TestBaseURL(t *testing.T) {
	req := func(host, proto string) *http.Request {
		r := httptest.NewRequest("GET", "/rss.xml", nil)
		r.Host = host
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		return r
	}
	cases := []struct {
		r    *http.Request
		st   content.Settings
		want string
	}{
		{req("localhost:8080", ""), content.Settings{}, "http://localhost:8080"},
		{req("example.com", "https"), content.Settings{}, "https://example.com"},
		{req("example.com", ""), content.Settings{Domain: "mysite.org"}, "https://mysite.org"}, // configured domain wins
		{req("evil.com/\"><script>", ""), content.Settings{}, "http://localhost"},              // junk host never reaches the feed
		{req("example.com", ""), content.Settings{Domain: "bad domain!"}, "http://example.com"},
	}
	for _, c := range cases {
		if got := baseURL(c.r, c.st); got != c.want {
			t.Errorf("baseURL(%q, %+v) = %q, want %q", c.r.Host, c.st, got, c.want)
		}
	}
}

func TestRSSIsValidXMLAndEscapes(t *testing.T) {
	feed := rssFeed{Version: "2.0", Channel: rssChannel{
		Title: `A & B <site>`, Link: "http://x/", Description: "d",
		Items: []rssItem{{Title: `<script>alert(1)</script> & more`, Link: "http://x/blog/a",
			GUID: rssGUID{true, "http://x/blog/a"}, PubDate: "Thu, 05 Mar 2026 10:00:00 +0000", Description: "e", Categories: []string{"News & Views"}}},
	}}
	out, err := xml.Marshal(feed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "<script>") {
		t.Errorf("unescaped markup in feed: %s", out)
	}
	var back rssFeed
	if err := xml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.Channel.Title != `A & B <site>` || back.Channel.Items[0].Title != `<script>alert(1)</script> & more` ||
		!back.Channel.Items[0].GUID.IsPermaLink || back.Channel.Items[0].Categories[0] != "News & Views" {
		t.Errorf("round trip lost data: %+v", back)
	}
}
