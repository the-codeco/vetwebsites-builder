package content

import (
	"context"
	"strconv"
	"strings"

	"webbuilder/internal/blocks"
)

// ChangeCount is the number of changes made to everything a visitor could see (see migration 00024). It only goes up.
func (s *Store) ChangeCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT n FROM change_counter WHERE id = 1`).Scan(&n)
	return n, err
}

// ExportPaths lists the address of every public page of the website: the published pages (also those hidden from search
// engines, which are still reachable), the blog index and each page of it, each category and each of its pages, and every
// published post. Drafts are never listed.
func (s *Store) ExportPaths(ctx context.Context, perPage int) ([]string, error) {
	var paths []string
	rows, err := s.pool.Query(ctx, `SELECT slug FROM pages WHERE status = 'published' ORDER BY (slug = 'home') DESC, slug`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			rows.Close()
			return nil, err
		}
		if slug == "home" {
			paths = append(paths, "/")
		} else {
			paths = append(paths, "/"+slug)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE status = 'published'`).Scan(&total); err != nil {
		return nil, err
	}
	if total > 0 {
		paths = append(paths, listPaths("/blog", total, perPage)...)
	}
	cats, err := s.pool.Query(ctx, `
		SELECT c.slug, count(p.id) FROM categories c
		JOIN post_categories pc ON pc.category_id = c.id
		JOIN posts p ON p.id = pc.post_id AND p.status = 'published'
		GROUP BY c.slug ORDER BY c.slug`)
	if err != nil {
		return nil, err
	}
	for cats.Next() {
		var slug string
		var n int
		if err := cats.Scan(&slug, &n); err != nil {
			cats.Close()
			return nil, err
		}
		paths = append(paths, listPaths("/blog/category/"+slug, n, perPage)...)
	}
	cats.Close()
	if err := cats.Err(); err != nil {
		return nil, err
	}
	posts, err := s.pool.Query(ctx, `SELECT slug FROM posts WHERE status = 'published' ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer posts.Close()
	for posts.Next() {
		var slug string
		if err := posts.Scan(&slug); err != nil {
			return nil, err
		}
		paths = append(paths, "/blog/"+slug)
	}
	return paths, posts.Err()
}

// listPaths is a list's first page and each page after it ("/blog", "/blog/page/2", ...).
func listPaths(base string, total, perPage int) []string {
	pages := max((total+perPage-1)/perPage, 1)
	out := []string{base}
	for p := 2; p <= pages; p++ {
		out = append(out, base+"/page/"+strconv.Itoa(p))
	}
	return out
}

// IndexEntry is one page or post in a site's search index.
type IndexEntry struct {
	Path, Title, Kind, Text string
}

// SearchIndex lists the words of every published page and post, for a search that runs in the visitor's browser. Text is
// plain words (no markup), limited in size so the index stays small.
func (s *Store) SearchIndex(ctx context.Context, maxText int) ([]IndexEntry, error) {
	var out []IndexEntry
	rows, err := s.pool.Query(ctx, `SELECT slug, title, search_text FROM pages WHERE status = 'published' AND NOT COALESCE((seo->>'noindex')::boolean, false) ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var slug, title, text string
		if err := rows.Scan(&slug, &title, &text); err != nil {
			rows.Close()
			return nil, err
		}
		path := "/" + slug
		if slug == "home" {
			path = "/"
		}
		out = append(out, IndexEntry{Path: path, Title: title, Kind: "page", Text: truncateRunes(text, maxText)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	posts, err := s.pool.Query(ctx, `SELECT slug, title, excerpt, body FROM posts WHERE status = 'published' AND NOT COALESCE((seo->>'noindex')::boolean, false) ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer posts.Close()
	for posts.Next() {
		var slug, title, excerpt, body string
		if err := posts.Scan(&slug, &title, &excerpt, &body); err != nil {
			return nil, err
		}
		text := strings.TrimSpace(excerpt + " " + blocks.WordsOf(body))
		out = append(out, IndexEntry{Path: "/blog/" + slug, Title: title, Kind: "post", Text: truncateRunes(text, maxText)})
	}
	return out, posts.Err()
}

// RedirectList is every redirect, for the static copy of a website to answer itself.
func (s *Store) RedirectList(ctx context.Context) ([]Redirect, error) { return s.Redirects(ctx) }
