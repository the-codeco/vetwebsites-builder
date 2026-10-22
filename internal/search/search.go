// Package search finds published pages and posts for the site's search box, using the
// database's full-text search (English word stemming, ranking, highlighted snippets).
package search

import (
	"context"
	"html"
	"html/template"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/auth"
)

const (
	PerPage  = 10
	MaxQuery = 100

	// Each search reads an index but also builds highlighted snippets, so one visitor
	// can't run an unlimited number of them.
	perMinute = 30

	// Private-use characters mark the matched words inside a snippet. They can't appear in
	// real text, so after the rest is escaped they can safely become <mark> tags.
	markStart = ""
	markEnd   = ""
)

type Result struct {
	Kind    string // "page" or "post"
	Title   string
	Path    string
	Snippet template.HTML // escaped, with <mark> around matched words
}

type Results struct {
	Query       string
	Items       []Result
	Total       int
	Page, Pages int
}

type Service struct {
	pool     *pgxpool.Pool
	limiter  *auth.Limiter
	ClientIP func(*http.Request) string
}

func New(pool *pgxpool.Pool, clientIP func(*http.Request) string) *Service {
	return &Service{pool: pool, limiter: auth.NewLimiter(perMinute, time.Minute), ClientIP: clientIP}
}

// Allow counts one search against the visitor's per-minute limit.
func (s *Service) Allow(r *http.Request) bool {
	ip := s.ClientIP(r)
	if s.limiter.Blocked(ip) {
		return false
	}
	s.limiter.Fail(ip)
	return true
}

// Clean makes a typed query safe to use and show: no control characters, single spaces, at most
// MaxQuery characters.
func Clean(q string) string {
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, q)
	q = strings.Join(strings.Fields(q), " ")
	if utf8.RuneCountInString(q) > MaxQuery {
		q = strings.TrimSpace(string([]rune(q)[:MaxQuery]))
	}
	return q
}

// Highlight turns a database snippet into safe HTML: everything is escaped, then the markers
// around matched words become <mark>.
func Highlight(raw string) template.HTML {
	esc := html.EscapeString(raw)
	esc = strings.ReplaceAll(esc, markStart, "<mark>")
	esc = strings.ReplaceAll(esc, markEnd, "</mark>")
	return template.HTML(esc)
}

const query = `
WITH q AS (SELECT websearch_to_tsquery('english', $1) AS tsq),
hits AS (
	SELECT 'page'::text AS kind, p.id, p.title,
	       CASE WHEN p.slug = 'home' THEN '/' ELSE '/' || p.slug END AS path,
	       ts_rank(p.search_vec, q.tsq) AS rank
	FROM pages p, q
	WHERE p.status = 'published' AND NOT COALESCE((p.seo->>'noindex')::boolean, false) AND p.search_vec @@ q.tsq
	UNION ALL
	SELECT 'post', p.id, p.title, '/blog/' || p.slug, ts_rank(p.search_vec, q.tsq)
	FROM posts p, q
	WHERE p.status = 'published' AND NOT COALESCE((p.seo->>'noindex')::boolean, false) AND p.search_vec @@ q.tsq
),
page AS (
	SELECT *, count(*) OVER () AS total FROM hits ORDER BY rank DESC, title, id LIMIT $2 OFFSET $3
)
SELECT h.kind, h.title, h.path, h.total,
	ts_headline('english',
		CASE h.kind
			WHEN 'page' THEN (SELECT search_text FROM pages WHERE id = h.id)
			ELSE (SELECT regexp_replace(excerpt || ' ' || body, '<[^>]*>', ' ', 'g') FROM posts WHERE id = h.id)
		END,
		q.tsq, $4)
FROM page h, q
ORDER BY h.rank DESC, h.title, h.id`

const headlineOptions = "StartSel=" + markStart + ",StopSel=" + markEnd + ",MaxWords=34,MinWords=14,ShortWord=2,MaxFragments=1"

// Run searches for q (already cleaned) and returns one page of results.
func (s *Service) Run(ctx context.Context, q string, page int) (Results, error) {
	res := Results{Query: q, Page: max(page, 1)}
	if q == "" {
		return res, nil
	}
	rows, err := s.pool.Query(ctx, query, q, PerPage, (res.Page-1)*PerPage, headlineOptions)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Result
		var snippet string
		if err := rows.Scan(&r.Kind, &r.Title, &r.Path, &res.Total, &snippet); err != nil {
			return res, err
		}
		r.Snippet = Highlight(snippet)
		res.Items = append(res.Items, r)
	}
	res.Pages = max((res.Total+PerPage-1)/PerPage, 1)
	return res, rows.Err()
}
