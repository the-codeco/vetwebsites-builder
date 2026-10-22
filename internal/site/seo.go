package site

import (
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
	"webbuilder/internal/seo"
	"webbuilder/internal/themes"
)

// seoInput is what buildSEO needs to know about one rendered page.
type seoInput struct {
	st        content.Settings
	base      string // https://example.com, no trailing slash
	path      string // canonical path, including ?page=N when paginated
	pageTitle string // "" on the home page
	item      content.SEO
	fallback  string // description to use when the item has none (a post's excerpt)
	image     string // image to use when the item has none (a post's cover)
	article   bool
	published time.Time
	modified  time.Time
	// hidden pages (drafts, theme previews, errors) are never indexed, whatever their settings
	hidden bool
}

// buildSEO turns a page's settings into the tags the layout prints.
func buildSEO(in seoInput) themes.SEO {
	site := in.st.Title
	out := themes.SEO{Type: "website", SiteName: site}

	switch {
	case in.item.MetaTitle != "":
		out.Title = in.item.MetaTitle
	case in.pageTitle == "":
		out.Title = site
	default:
		out.Title = in.pageTitle + " · " + site
	}

	out.Description = in.item.Description
	if out.Description == "" {
		out.Description = in.fallback
	}
	if out.Description == "" {
		out.Description = in.st.SEO.Description
	}
	out.Description = seo.Shorten(out.Description, 300)

	for _, ref := range []string{in.item.Image, in.image, in.st.SEO.Image} {
		if u := seo.Absolute(in.base, blocks.SafeImageURL(ref)); u != "" {
			out.Image = u
			break
		}
	}

	switch {
	case in.hidden || in.st.SEO.NoIndex:
		out.Robots = "noindex, nofollow"
	case in.item.NoIndex:
		out.Robots = "noindex, follow"
	default:
		out.Canonical = in.base + in.path
	}
	if in.hidden {
		out.Canonical = ""
	}

	if in.article {
		out.Type = "article"
		out.Published = in.published.Format(time.RFC3339)
		out.Modified = in.modified.Format(time.RFC3339)
	}
	out.JSONLD = jsonLD(in, out)
	return out
}

// jsonLD is schema.org structured data: a WebSite on the home page and a
// BlogPosting on posts. json.Marshal escapes <, > and &, so it can't close the script tag.
func jsonLD(in seoInput, out themes.SEO) template.JS {
	if in.hidden || out.Robots != "" {
		return "" // nothing worth advertising on a page that asks not to be indexed
	}
	var doc map[string]any
	switch {
	case in.article:
		doc = map[string]any{
			"@context": "https://schema.org", "@type": "BlogPosting",
			"headline": in.pageTitle, "url": out.Canonical, "mainEntityOfPage": out.Canonical,
			"datePublished": out.Published, "dateModified": out.Modified,
			"publisher": map[string]any{"@type": "Organization", "name": in.st.Title},
		}
		if out.Description != "" {
			doc["description"] = out.Description
		}
		if out.Image != "" {
			doc["image"] = []string{out.Image}
		}
	case in.path == "/":
		doc = map[string]any{"@context": "https://schema.org", "@type": "WebSite", "name": in.st.Title, "url": in.base + "/"}
		if out.Description != "" {
			doc["description"] = out.Description
		}
	default:
		return ""
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return template.JS(b)
}

// --- /sitemap.xml and /robots.txt ---

func (s *Service) sitemap(w http.ResponseWriter, r *http.Request) {
	st, err := s.settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	if st.SEO.NoIndex {
		http.NotFound(w, r) // a hidden site publishes no map of itself
		return
	}
	entries, err := s.content.Sitemap(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	list := make([]seo.Entry, len(entries))
	for i, e := range entries {
		list[i] = seo.Entry{Path: e.Path, LastMod: e.LastMod}
	}
	out, err := seo.Sitemap(baseURL(r, st), list)
	if err != nil {
		serverError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/xml; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "public, max-age=300")
	w.Write(out)
}

func (s *Service) robots(w http.ResponseWriter, r *http.Request) {
	st, err := s.settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "public, max-age=300")
	w.Write([]byte(seo.Robots(baseURL(r, st), st.SEO.NoIndex)))
}
