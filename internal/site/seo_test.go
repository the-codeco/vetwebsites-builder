package site

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/content"
)

func baseInput() seoInput {
	return seoInput{
		st:   content.Settings{Title: "Acme"},
		base: "https://acme.test", path: "/about", pageTitle: "About",
	}
}

func TestBuildSEOTitleAndCanonical(t *testing.T) {
	in := baseInput()
	got := buildSEO(in)
	if got.Title != "About · Acme" || got.Canonical != "https://acme.test/about" || got.Robots != "" || got.Type != "website" {
		t.Errorf("defaults: %+v", got)
	}
	in.item.MetaTitle = "Custom"
	if got := buildSEO(in); got.Title != "Custom" {
		t.Errorf("override: %q", got.Title)
	}
	home := baseInput()
	home.pageTitle, home.path = "", "/"
	if got := buildSEO(home); got.Title != "Acme" || got.Canonical != "https://acme.test/" {
		t.Errorf("home: %+v", got)
	}
}

func TestBuildSEODescriptionAndImageFallbacks(t *testing.T) {
	in := baseInput()
	in.st.SEO = content.SEO{Description: "site wide", Image: "/media/site.jpg"}
	in.fallback, in.image = "excerpt", "/media/cover.jpg"
	in.item = content.SEO{Description: "own", Image: "/media/own.jpg"}

	if got := buildSEO(in); got.Description != "own" || got.Image != "https://acme.test/media/own.jpg" {
		t.Errorf("own values should win: %+v", got)
	}
	in.item = content.SEO{}
	if got := buildSEO(in); got.Description != "excerpt" || got.Image != "https://acme.test/media/cover.jpg" {
		t.Errorf("item fallbacks: %+v", got)
	}
	in.fallback, in.image = "", ""
	if got := buildSEO(in); got.Description != "site wide" || got.Image != "https://acme.test/media/site.jpg" {
		t.Errorf("site defaults: %+v", got)
	}
	// an unusable image is skipped, not printed
	in.item.Image = "javascript:alert(1)"
	in.st.SEO.Image = ""
	if got := buildSEO(in); got.Image != "" {
		t.Errorf("bad image used: %q", got.Image)
	}
}

func TestBuildSEORobots(t *testing.T) {
	in := baseInput()
	in.item.NoIndex = true
	if got := buildSEO(in); got.Robots != "noindex, follow" || got.Canonical != "" || got.JSONLD != "" {
		t.Errorf("page noindex: %+v", got)
	}
	in = baseInput()
	in.st.SEO.NoIndex = true
	if got := buildSEO(in); got.Robots != "noindex, nofollow" || got.Canonical != "" {
		t.Errorf("site noindex: %+v", got)
	}
	in = baseInput()
	in.hidden = true // drafts, theme previews and error pages
	if got := buildSEO(in); got.Robots != "noindex, nofollow" || got.Canonical != "" || got.JSONLD != "" {
		t.Errorf("hidden: %+v", got)
	}
}

func TestBuildSEOArticleAndStructuredData(t *testing.T) {
	in := baseInput()
	in.path, in.article = "/blog/x", true
	in.pageTitle = `Tricky </script><script>alert(1)</script> & "quotes"`
	in.published = time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	in.modified = in.published.Add(time.Hour)
	in.fallback = "A summary"
	got := buildSEO(in)
	if got.Type != "article" || got.Published != "2026-03-05T10:00:00Z" || got.Modified != "2026-03-05T11:00:00Z" {
		t.Errorf("article fields: %+v", got)
	}
	raw := string(got.JSONLD)
	if strings.Contains(raw, "</script>") || strings.Contains(raw, "<script") {
		t.Fatalf("structured data can close its own script tag: %s", raw)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, raw)
	}
	if doc["@type"] != "BlogPosting" || doc["headline"] != in.pageTitle || doc["url"] != "https://acme.test/blog/x" || doc["description"] != "A summary" {
		t.Errorf("document: %v", doc)
	}

	home := baseInput()
	home.path, home.pageTitle = "/", ""
	var site map[string]any
	if err := json.Unmarshal([]byte(buildSEO(home).JSONLD), &site); err != nil || site["@type"] != "WebSite" || site["url"] != "https://acme.test/" {
		t.Errorf("home structured data: %v %v", site, err)
	}
	if got := buildSEO(baseInput()).JSONLD; got != "" {
		t.Errorf("ordinary pages carry no structured data, got %s", got)
	}
}

func TestBuildSEOShortensLongDescriptions(t *testing.T) {
	in := baseInput()
	in.item.Description = strings.Repeat("word ", 200)
	if got := buildSEO(in).Description; len([]rune(got)) > 300 {
		t.Errorf("description not capped: %d characters", len([]rune(got)))
	}
}

func TestBuildSEOPaginatedCanonical(t *testing.T) {
	in := baseInput()
	in.path, in.pageTitle = "/blog?page=2", "Blog"
	if got := buildSEO(in).Canonical; got != "https://acme.test/blog?page=2" {
		t.Errorf("each page of a list is its own canonical address: %q", got)
	}
}
