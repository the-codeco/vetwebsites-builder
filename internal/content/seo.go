package content

import (
	"context"
	"strconv"
	"time"

	"webbuilder/internal/blocks"
)

// SEO is what an owner can say about how one page, post or the whole site
// appears in search results and when shared. Every field is optional.
type SEO struct {
	MetaTitle   string // replaces the <title>; "" means "page title · site title"
	Description string
	Image       string // social sharing image
	NoIndex     bool   // ask search engines to leave it out
}

// asJSON stores only what is set, so an empty SEO is {}.
func (s SEO) asJSON() map[string]any {
	m := map[string]any{}
	if s.MetaTitle != "" {
		m["title"] = s.MetaTitle
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if s.Image != "" {
		m["image"] = s.Image
	}
	if s.NoIndex {
		m["noindex"] = true
	}
	return m
}

// SEOFrom reads the JSON stored in a seo column.
func SEOFrom(m map[string]any) SEO {
	var s SEO
	s.MetaTitle, _ = m["title"].(string)
	s.Description, _ = m["description"].(string)
	s.Image, _ = m["image"].(string)
	s.NoIndex, _ = m["noindex"].(bool)
	return s
}

// SetSEO saves the site-wide defaults: a fallback description and sharing image,
// and whether to ask search engines to skip the whole site.
func (s *Store) SetSEO(ctx context.Context, seo SEO) error {
	seo.MetaTitle = "" // the site has no title override; its title is the site title
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET seo = $1 WHERE id = 1`, seo.asJSON())
	return err
}

// SEOItem is one published-or-draft page or post, as the SEO screen audits it.
type SEOItem struct {
	Kind    string // "page" or "post"
	ID      int64
	Title   string
	Path    string // public path: "/", "/about", "/blog/hello"
	Status  string
	EditURL string
	SEO     SEO
	// Fallback and Cover are what a post uses when its own search settings are empty:
	// the start of its text (HTML) and its cover image.
	Fallback, Cover string
	UpdatedAt       time.Time
}

// SEOItems lists every page and post with its search settings.
func (s *Store) SEOItems(ctx context.Context) ([]SEOItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT 'page', id, title, CASE WHEN slug = 'home' THEN '/' ELSE '/' || slug END, status, seo, ''::text, ''::text, updated_at
		FROM pages
		UNION ALL
		SELECT 'post', id, title, '/blog/' || slug, status, seo,
		       CASE WHEN excerpt <> '' THEN excerpt ELSE left(body, 2000) END, cover_image, updated_at FROM posts
		ORDER BY 1, 3`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SEOItem
	for rows.Next() {
		var it SEOItem
		var raw map[string]any
		if err := rows.Scan(&it.Kind, &it.ID, &it.Title, &it.Path, &it.Status, &raw, &it.Fallback, &it.Cover, &it.UpdatedAt); err != nil {
			return nil, err
		}
		it.SEO = SEOFrom(raw)
		it.EditURL = "/admin/pages/"
		if it.Kind == "post" {
			it.EditURL = "/admin/posts/"
		}
		it.EditURL += strconv.FormatInt(it.ID, 10)
		out = append(out, it)
	}
	return out, rows.Err()
}

// SitemapEntry is one public URL for sitemap.xml.
type SitemapEntry struct {
	Path    string
	LastMod time.Time
}

// MaxSitemapURLs is the protocol's limit for one sitemap file.
const MaxSitemapURLs = 50000

// Sitemap lists the public, indexable URLs: published pages and posts that
// haven't opted out, the blog index, and categories with at least one post.
func (s *Store) Sitemap(ctx context.Context) ([]SitemapEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT path, lastmod FROM (
			SELECT CASE WHEN slug = 'home' THEN '/' ELSE '/' || slug END AS path, updated_at AS lastmod, 0 AS ord
			FROM pages WHERE status = 'published' AND NOT COALESCE((seo->>'noindex')::boolean, false)
			UNION ALL
			SELECT '/blog', max(updated_at), 1 FROM posts
			WHERE status = 'published' HAVING count(*) > 0
			UNION ALL
			SELECT '/blog/category/' || c.slug, max(p.updated_at), 2
			FROM categories c
			JOIN post_categories pc ON pc.category_id = c.id
			JOIN posts p ON p.id = pc.post_id AND p.status = 'published'
			GROUP BY c.slug
			UNION ALL
			SELECT '/blog/' || slug, updated_at, 3
			FROM posts WHERE status = 'published' AND NOT COALESCE((seo->>'noindex')::boolean, false)
		) u ORDER BY ord, path LIMIT $1`, MaxSitemapURLs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SitemapEntry
	for rows.Next() {
		var e SitemapEntry
		if err := rows.Scan(&e.Path, &e.LastMod); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CleanSEO makes any stored or imported search/sharing JSON safe: only the known fields survive, text is
// kept to a sensible length and the sharing image must be an address that is safe to use.
func CleanSEO(m map[string]any) map[string]any {
	s := SEOFrom(m)
	s.MetaTitle = truncateRunes(s.MetaTitle, 120)
	s.Description = truncateRunes(s.Description, 300)
	s.Image = blocks.SafeImageURL(truncateRunes(s.Image, 2000))
	return s.asJSON()
}
