// Package site serves the public website: pages, theme assets, site CSS and media.
package site

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/auth"
	"webbuilder/internal/blocks"
	"webbuilder/internal/contact"
	"webbuilder/internal/content"
	"webbuilder/internal/media"
	"webbuilder/internal/newsletter"
	"webbuilder/internal/search"
	"webbuilder/internal/themes"
	"webbuilder/web"
)

var errNotFound = errors.New("not found")

const (
	publicCSP = "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; " +
		"object-src 'none'; base-uri 'self'; frame-ancestors 'self'; frame-src " + blocks.FrameSources
	// Uploaded files must never execute: no scripts, sandboxed if opened directly.
	mediaCSP = "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox"
)

type Service struct {
	mediaRoot *os.Root
	pool      *pgxpool.Pool
	content   *content.Store
	engine    *themes.Engine
	media     http.Handler
	// Contact receives contact-form submissions. Set by the app after New; nil disables the form.
	Contact *contact.Service
	// Newsletter receives newsletter signups. Set by the app after New; nil disables them.
	Newsletter *newsletter.Service
	// Search answers the site's search box. Set by the app after New; nil disables it.
	Search *search.Service
}

func New(pool *pgxpool.Pool, engine *themes.Engine, mediaDir string) (*Service, error) {
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(mediaDir) // confines all access to mediaDir
	if err != nil {
		return nil, err
	}
	files := http.StripPrefix("/media/", http.FileServerFS(web.NoDir(root.FS())))
	variants := media.NewVariants(root)
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", mediaCSP)
		h.Set("Cache-Control", "public, max-age=31536000, immutable") // names are random and never reused
		// "?w=960" asks for a copy that fits a smaller screen; anything else is sent as stored
		if ws := r.URL.Query().Get("w"); ws != "" {
			if width, err := strconv.Atoi(ws); err == nil {
				name := variants.Resolve(strings.TrimPrefix(r.URL.Path, "/media/"), width)
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/media/" + name
				r2.URL.RawQuery = ""
				r = r2
			}
		}
		files.ServeHTTP(w, r)
	})
	return &Service{mediaRoot: root, pool: pool, content: content.NewStore(pool), engine: engine, media: serve}, nil
}

// Close releases the handle on the media directory.
func (s *Service) Close() error { return s.mediaRoot.Close() }

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { s.servePage(w, r, "home") })
	mux.HandleFunc("GET /{slug}", s.page)
	mux.HandleFunc("GET /site.css", s.siteCSS)
	mux.HandleFunc("GET /theme/{name}/{file...}", s.themeAsset)
	mux.HandleFunc("GET /fonts/{file}", s.fontFile)
	mux.Handle("GET /media/", s.media)
	mux.HandleFunc("POST /contact", s.contactSubmit)
	s.registerNewsletter(mux)
	mux.HandleFunc("GET /search", s.searchPage)
	mux.HandleFunc("GET /sitemap.xml", s.sitemap)
	mux.HandleFunc("GET /robots.txt", s.robots)
	s.registerBlog(mux)
	mux.HandleFunc("GET /", s.fallback) // every other address: a redirect if there is one, otherwise the not-found page
}

// fallback answers addresses nothing else matches.
func (s *Service) fallback(w http.ResponseWriter, r *http.Request) {
	st, err := s.settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	theme, _ := s.themeFor(r, st)
	s.notFound(w, r, st, theme)
}

func (s *Service) page(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "home" {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return
	}
	s.servePage(w, r, slug)
}

// --- data ---

func (s *Service) settings(ctx context.Context) (content.Settings, error) {
	return s.content.Settings(ctx)
}

// themeFor picks the theme to render with. Logged-in users can view any theme
// without activating it via ?theme=name; the second result reports that case.
func (s *Service) themeFor(r *http.Request, st content.Settings) (*themes.Theme, bool) {
	if name := r.URL.Query().Get("theme"); name != "" && auth.CurrentIdentity(r) != nil {
		if t, ok := s.engine.Lookup(name); ok {
			return t, true
		}
	}
	return s.engine.Get(st.ActiveTheme), false
}

type Page struct {
	ID                  int64
	Slug, Title, Status string
	SEO                 content.SEO
	Blocks              []blocks.Block
}

func (s *Service) loadPage(ctx context.Context, slug string) (*Page, error) {
	if !slugRe.MatchString(slug) {
		return nil, errNotFound
	}
	p := &Page{}
	var seo map[string]any
	err := s.pool.QueryRow(ctx, `SELECT id, slug, title, status, seo FROM pages WHERE slug = $1`, slug).
		Scan(&p.ID, &p.Slug, &p.Title, &p.Status, &seo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	p.SEO = content.SEOFrom(seo)

	rows, err := s.pool.Query(ctx,
		`SELECT id, COALESCE(parent_id, 0), slot, type, props, style FROM blocks WHERE page_id = $1
		 ORDER BY (parent_id IS NOT NULL), slot, position, id`, p.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nested := map[int64][]blocks.Block{} // the blocks in each layout, in column order
	slots := map[int64][]int{}
	for rows.Next() {
		var b blocks.Block
		var parent int64
		var slot int
		var props, style map[string]any
		if err := rows.Scan(&b.ID, &parent, &slot, &b.Type, &props, &style); err != nil {
			return nil, err
		}
		bt, ok := blocks.Get(b.Type)
		if !ok {
			continue // type removed from the registry; skip rather than fail the page
		}
		b.Props = bt.Clean(props)
		b.Style = blocks.CleanStyle(style)
		if parent == 0 {
			p.Blocks = append(p.Blocks, b)
		} else if blocks.Nestable(b.Type) {
			nested[parent] = append(nested[parent], b)
			slots[parent] = append(slots[parent], slot)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range p.Blocks {
		if p.Blocks[i].Type != blocks.LayoutType {
			continue
		}
		cols := blocks.LayoutColumns(p.Blocks[i].Props)
		p.Blocks[i].Children = make([][]blocks.Block, cols)
		for j, c := range nested[p.Blocks[i].ID] {
			col := min(slots[p.Blocks[i].ID][j], cols-1) // a block left in a column that no longer exists shows in the last one
			p.Blocks[i].Children[col] = append(p.Blocks[i].Children[col], c)
		}
	}
	if err := s.resolveShared(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// resolveShared replaces each shared block on the page with what it stands for: the library block's type,
// settings and style, under the page block's own id. A shared block that no longer exists (or whose kind is gone)
// is left out of the page.
func (s *Service) resolveShared(ctx context.Context, p *Page) error {
	need := map[int64]bool{}
	visit := func(f func(b *blocks.Block)) {
		for i := range p.Blocks {
			f(&p.Blocks[i])
			for c := range p.Blocks[i].Children {
				for j := range p.Blocks[i].Children[c] {
					f(&p.Blocks[i].Children[c][j])
				}
			}
		}
	}
	visit(func(b *blocks.Block) {
		if b.Type == blocks.ReusableType {
			if id := blocks.RefOf(b.Props); id > 0 {
				need[id] = true
			}
		}
	})
	if len(need) == 0 {
		p.Blocks = dropShared(p.Blocks)
		return nil
	}
	ids := make([]int64, 0, len(need))
	for id := range need {
		ids = append(ids, id)
	}
	rows, err := s.pool.Query(ctx, `SELECT id, type, props, style FROM reusable_blocks WHERE id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	lib := map[int64]blocks.Block{}
	for rows.Next() {
		var id int64
		var b blocks.Block
		var props, style map[string]any
		if err := rows.Scan(&id, &b.Type, &props, &style); err != nil {
			return err
		}
		if bt, ok := blocks.Get(b.Type); ok && blocks.CanBeShared(b.Type) {
			b.Props, b.Style = bt.Clean(props), blocks.CleanStyle(style)
			lib[id] = b
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	visit(func(b *blocks.Block) {
		if b.Type != blocks.ReusableType {
			return
		}
		if shared, ok := lib[blocks.RefOf(b.Props)]; ok {
			b.Type, b.Props, b.Style = shared.Type, shared.Props, shared.Style
		}
	})
	p.Blocks = dropShared(p.Blocks)
	return nil
}

// dropShared removes shared blocks that could not be resolved, at the top level and inside layouts.
func dropShared(list []blocks.Block) []blocks.Block {
	out := list[:0]
	for _, b := range list {
		if b.Type == blocks.ReusableType {
			continue
		}
		for c := range b.Children {
			b.Children[c] = dropShared(b.Children[c])
		}
		out = append(out, b)
	}
	return out
}

// --- handlers ---

func serverError(w http.ResponseWriter, err error) {
	log.Printf("site: %v", err)
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}

func (s *Service) servePage(w http.ResponseWriter, r *http.Request, slug string) {
	ctx := r.Context()
	st, err := s.settings(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	theme, themePreview := s.themeFor(r, st)

	page, err := s.loadPage(ctx, slug)
	preview := false
	if err == nil && page.Status != "published" {
		// drafts are visible only to logged-in users
		if auth.CurrentIdentity(r) == nil {
			err = errNotFound
		} else {
			preview = true
		}
	}
	if errors.Is(err, errNotFound) {
		s.notFound(w, r, st, theme)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	var body strings.Builder
	// data a block needs beyond its own settings: the latest posts, a form's spam token
	extraFor := func(b blocks.Block) any {
		switch b.Type {
		case "postlist":
			n, _ := b.Props["count"].(float64)
			return s.latestPosts(ctx, int(n))
		case "contactform":
			return s.contactFormData(r, b.ID)
		case "newsletter":
			return s.newsletterFormData(r, b.ID)
		case "details":
			return detailsInfo(st.Details)
		}
		return nil
	}
	for _, b := range page.Blocks {
		html, err := theme.RenderBlockTree(b, extraFor)
		if err != nil {
			log.Printf("site: %v", err) // one broken block shouldn't take the page down
			continue
		}
		body.WriteString(string(html))
	}
	nav, err := s.nav(ctx, r.URL.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	title := page.Title
	path := "/" + slug
	if slug == "home" {
		title, path = "", "/"
	}
	seoTags := buildSEO(seoInput{
		st: st, base: baseURL(r, st), path: path, pageTitle: title, item: page.SEO,
		hidden: preview || themePreview,
	})
	s.write(w, theme, http.StatusOK, preview || themePreview, themes.PageData{
		Site:        siteInfo(r, theme, st, themePreview),
		PageTitle:   title,
		Description: seoTags.Description,
		SEO:         seoTags,
		Nav:         nav,
		Content:     template.HTML(body.String()),
		Year:        time.Now().Year(),
		Preview:     preview,
	})
}

func (s *Service) notFound(w http.ResponseWriter, r *http.Request, st content.Settings, theme *themes.Theme) {
	// an old address that was moved: send the visitor (and search engines) to the new one
	if to, permanent, ok := s.content.RedirectFor(r.Context(), r.URL.Path); ok {
		code := http.StatusFound
		if permanent {
			code = http.StatusMovedPermanently
		}
		http.Redirect(w, r, to, code)
		return
	}
	nav, _ := s.nav(r.Context(), r.URL.Path)
	_, themePreview := s.themeFor(r, st)
	s.write(w, theme, http.StatusNotFound, themePreview, themes.PageData{
		Site:      siteInfo(r, theme, st, themePreview),
		PageTitle: "Page not found",
		SEO:       buildSEO(seoInput{st: st, base: baseURL(r, st), pageTitle: "Page not found", hidden: true}),
		Nav:       nav,
		Content: template.HTML(`<section class="block pad-md align-center width-normal"><div class="block-inner">` +
			`<h1>Page not found</h1><p>The page you were looking for doesn't exist.</p>` +
			`<p><a href="/">Back to the home page</a></p></div></section>`),
		Year: time.Now().Year(),
	})
}

// write renders to a buffer first so a template error yields a clean 500, not a half page.
func (s *Service) write(w http.ResponseWriter, theme *themes.Theme, status int, private bool, d themes.PageData) {
	var buf bytes.Buffer
	if err := theme.RenderPage(&buf, d); err != nil {
		serverError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", publicCSPFor(d.Site.Tracking))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if private {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (s *Service) siteCSS(w http.ResponseWriter, r *http.Request) {
	st, err := s.settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	theme, themePreview := s.themeFor(r, st)
	fonts, err := s.content.Fonts(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	faces := make([]themes.FontFace, 0, len(fonts))
	for _, f := range fonts {
		faces = append(faces, themes.FontFace{Family: f.Family, Slug: content.FontToken(f.Family), Weight: f.Weight, URL: "/fonts/" + f.FileName()})
	}
	css := theme.CSS(st.TokensFor(theme.Name), st.CustomCSS, st.ColorMode, faces)
	sum := sha256.Sum256([]byte(css))
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("ETag", etag)
	h.Set("Cache-Control", "no-cache") // revalidate so edits show immediately
	if themePreview {
		h.Set("Cache-Control", "no-store")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write([]byte(css))
}

// themeAsset serves a theme's stylesheets and assets/ folder — never its templates or theme.json.
func (s *Service) themeAsset(w http.ResponseWriter, r *http.Request) {
	theme, ok := s.engine.Lookup(r.PathValue("name"))
	file := r.PathValue("file")
	if !ok || !fs.ValidPath(file) || !(path.Ext(file) == ".css" && !strings.Contains(file, "/") || strings.HasPrefix(file, "assets/")) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFileFS(w, r, theme.FS, file)
}
