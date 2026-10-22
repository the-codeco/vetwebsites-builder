// Package seo holds the pure parts of search-engine support: sitemap and
// robots.txt text, absolute URLs, and the checks behind the SEO audit screen.
// It knows nothing about the database or HTTP.
package seo

import (
	"encoding/xml"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Lengths that search results show without cutting the text off. They are
// guidance, not hard limits: the audit warns, it never blocks a save.
const (
	TitleMin = 15
	TitleMax = 60
	DescMin  = 70
	DescMax  = 160
)

// Absolute turns a site path ("/media/a.jpg") into a full URL. Full http(s)
// URLs are returned unchanged and anything else (javascript:, data:, empty) becomes "".
func Absolute(base, ref string) string {
	switch {
	case strings.HasPrefix(ref, "//"): // protocol-relative: ambiguous, so refuse
		return ""
	case strings.HasPrefix(ref, "/"):
		return strings.TrimRight(base, "/") + ref
	case strings.HasPrefix(ref, "https://"), strings.HasPrefix(ref, "http://"):
		return ref
	}
	return ""
}

// Shorten cuts text to at most max characters on a word boundary, adding an
// ellipsis when it had to cut.
func Shorten(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)[:max-1]
	if i := strings.LastIndex(string(r), " "); i > max/2 {
		return strings.TrimRight(string(r[:i]), " ,.;:-") + "…"
	}
	return strings.TrimRight(string(r), " ,.;:-") + "…"
}

// --- sitemap.xml and robots.txt ---

type Entry struct {
	Path    string
	LastMod time.Time
}

type urlset struct {
	XMLName xml.Name     `xml:"urlset"`
	Xmlns   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// Sitemap renders sitemap.xml for the given entries at base (e.g. "https://example.com").
func Sitemap(base string, entries []Entry) ([]byte, error) {
	set := urlset{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, e := range entries {
		u := sitemapURL{Loc: Absolute(base, e.Path)}
		if !e.LastMod.IsZero() {
			u.LastMod = e.LastMod.UTC().Format("2006-01-02")
		}
		set.URLs = append(set.URLs, u)
	}
	out, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

// Robots renders robots.txt. A hidden site asks every crawler to stay away; a
// normal one keeps them out of the admin and points them at the sitemap.
func Robots(base string, hidden bool) string {
	if hidden {
		return "User-agent: *\nDisallow: /\n"
	}
	return "User-agent: *\nDisallow: /admin/\nDisallow: /search\n\nSitemap: " + strings.TrimRight(base, "/") + "/sitemap.xml\n"
}

// --- audit ---

// Issue is one thing worth fixing about a page's search listing.
type Issue struct {
	Code string // stable, for tests and styling
	Text string
}

// Page is what the audit looks at.
type Page struct {
	Title       string // the page's own title
	SiteTitle   string
	MetaTitle   string
	Description string // the description search engines will actually use
	Image       string // the sharing image that will actually be used
	NoIndex     bool
	Published   bool
}

// EffectiveTitle is what appears in the browser tab and search results.
func (p Page) EffectiveTitle() string {
	switch {
	case p.MetaTitle != "":
		return p.MetaTitle
	case p.Title == "":
		return p.SiteTitle
	}
	return p.Title + " · " + p.SiteTitle
}

// Audit lists problems with how a page will look in search results. A page
// that is hidden on purpose or not published yet has nothing to fix.
func Audit(p Page) []Issue {
	if p.NoIndex || !p.Published {
		return nil
	}
	var out []Issue
	add := func(code, text string) { out = append(out, Issue{code, text}) }

	if n := utf8.RuneCountInString(p.EffectiveTitle()); n > TitleMax {
		add("title-long", "Title is long ("+strconv.Itoa(n)+" characters); search results cut it off after about "+strconv.Itoa(TitleMax)+".")
	} else if n < TitleMin && (p.Title != "" || p.MetaTitle != "") { // a home page titled just with the site name is normal
		add("title-short", "Title is very short ("+strconv.Itoa(n)+" characters).")
	}
	switch n := utf8.RuneCountInString(p.Description); {
	case n == 0:
		add("desc-missing", "No description. Search engines will pick a snippet themselves.")
	case n < DescMin:
		add("desc-short", "Description is short ("+strconv.Itoa(n)+" characters); aim for "+strconv.Itoa(DescMin)+"–"+strconv.Itoa(DescMax)+".")
	case n > DescMax:
		add("desc-long", "Description is long ("+strconv.Itoa(n)+" characters); search results cut it off after about "+strconv.Itoa(DescMax)+".")
	}
	if p.Image == "" {
		add("image-missing", "No sharing image, so links to this page show without a picture.")
	}
	return out
}
