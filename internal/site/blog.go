package site

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"

	"webbuilder/internal/auth"
	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
	"webbuilder/internal/themes"
)

const (
	blogPerPage   = 10
	feedItems     = 20
	excerptLength = 200
)

func (s *Service) registerBlog(mux *http.ServeMux) {
	mux.HandleFunc("GET /blog", s.blogIndex)
	mux.HandleFunc("GET /blog/page/{n}", s.blogIndex)
	mux.HandleFunc("GET /blog/category/{slug}", s.blogCategory)
	mux.HandleFunc("GET /blog/category/{slug}/page/{n}", s.blogCategory)
	mux.HandleFunc("GET /blog/{slug}", s.blogPost)
	mux.HandleFunc("GET /rss.xml", s.rss)
}

// --- conversion ---

// excerptOf returns the author's excerpt, or the start of the body as plain text.
func excerptOf(p content.Post) string {
	text := p.Excerpt
	if text == "" {
		text = blocks.PlainText(p.Body)
	}
	if utf8.RuneCountInString(text) > excerptLength {
		text = string([]rune(text)[:excerptLength]) + "…"
	}
	return text
}

func categoryLinks(cs []content.Category) []themes.CategoryLink {
	out := make([]themes.CategoryLink, len(cs))
	for i, c := range cs {
		out[i] = themes.CategoryLink{Name: c.Name, URL: "/blog/category/" + c.Slug}
	}
	return out
}

func summarise(p content.Post) themes.PostSummary {
	d := p.Date()
	return themes.PostSummary{
		Title: p.Title, URL: "/blog/" + p.Slug, Excerpt: excerptOf(p),
		Cover: blocks.SafeImageURL(p.Cover), Date: d.Format("2 January 2006"), DateISO: d.Format(time.RFC3339),
		Categories: categoryLinks(p.Categories),
	}
}

func summaries(ps []content.Post) []themes.PostSummary {
	out := make([]themes.PostSummary, len(ps))
	for i, p := range ps {
		out[i] = summarise(p)
	}
	return out
}

// latestPosts feeds the "Latest posts" block.
func (s *Service) latestPosts(ctx context.Context, n int) []themes.PostSummary {
	posts, _, err := s.content.PublishedPosts(ctx, "", n, 0)
	if err != nil {
		return nil // a failing block shouldn't take the whole page down
	}
	return summaries(posts)
}

// --- listing ---

func (s *Service) blogIndex(w http.ResponseWriter, r *http.Request) { s.serveBlogList(w, r, "") }

func (s *Service) blogCategory(w http.ResponseWriter, r *http.Request) {
	s.serveBlogList(w, r, r.PathValue("slug"))
}

func (s *Service) serveBlogList(w http.ResponseWriter, r *http.Request, categorySlug string) {
	ctx := r.Context()
	st, err := s.settings(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	theme, themePreview := s.themeFor(r, st)

	list := themes.BlogList{Heading: "Blog"}
	base := "/blog"
	if categorySlug != "" {
		if !slugRe.MatchString(categorySlug) {
			s.notFound(w, r, st, theme)
			return
		}
		cat, err := s.content.CategoryBySlug(ctx, categorySlug)
		if errors.Is(err, content.ErrNotFound) {
			s.notFound(w, r, st, theme)
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		base = "/blog/category/" + cat.Slug
		list.Heading = cat.Name
		list.Category = &themes.CategoryLink{Name: cat.Name, URL: base}
	}

	// the page number is in the address (/blog/page/2, which a static copy of the site can hold) or, as it used to be,
	// in the query (?page=2)
	n := r.PathValue("n")
	if n == "" {
		n = r.URL.Query().Get("page")
	}
	page, _ := strconv.Atoi(n)
	page = max(page, 1)
	posts, total, err := s.content.PublishedPosts(ctx, categorySlug, blogPerPage, (page-1)*blogPerPage)
	if err != nil {
		serverError(w, err)
		return
	}
	pages := max((total+blogPerPage-1)/blogPerPage, 1)
	if page > pages {
		s.notFound(w, r, st, theme)
		return
	}
	list.Posts, list.Page, list.Pages = summaries(posts), page, pages
	if page > 1 {
		list.PrevURL = base
		if page > 2 {
			list.PrevURL = fmt.Sprintf("%s/page/%d", base, page-1)
		}
	}
	if page < pages {
		list.NextURL = fmt.Sprintf("%s/page/%d", base, page+1)
	}

	canonical := base
	if page > 1 {
		canonical = fmt.Sprintf("%s/page/%d", base, page)
	}
	listSEO := buildSEO(seoInput{
		st: st, base: baseURL(r, st), path: canonical, pageTitle: list.Heading, hidden: themePreview,
	})
	body, err := theme.RenderSection("blog/list", list)
	if err != nil {
		serverError(w, err)
		return
	}
	nav, err := s.nav(ctx, r.URL.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	s.write(w, theme, http.StatusOK, themePreview, themes.PageData{
		Site:      siteInfo(r, theme, st, themePreview),
		PageTitle: list.Heading, Nav: nav, Content: body, Year: time.Now().Year(),
		SEO:         listSEO,
		Description: listSEO.Description,
	})
}

// --- single post ---

func (s *Service) blogPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.settings(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	theme, themePreview := s.themeFor(r, st)

	slug := r.PathValue("slug")
	var post content.Post
	err = errNotFound
	if slugRe.MatchString(slug) {
		post, err = s.content.PostBySlug(ctx, slug)
	}
	preview := false
	if err == nil && post.Status != "published" {
		if auth.CurrentIdentity(r) == nil { // drafts are visible only when logged in
			err = content.ErrNotFound
		} else {
			preview = true
		}
	}
	if errors.Is(err, content.ErrNotFound) || errors.Is(err, errNotFound) {
		s.notFound(w, r, st, theme)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	d := post.Date()
	body, err := theme.RenderSection("blog/post", themes.BlogPost{
		Title: post.Title, Cover: blocks.SafeImageURL(post.Cover),
		Date: d.Format("2 January 2006"), DateISO: d.Format(time.RFC3339), BackURL: "/blog",
		Categories: categoryLinks(post.Categories),
		Body:       template.HTML(blocks.SanitizeHTML(post.Body)), // sanitised again in case it predates validation
	})
	if err != nil {
		serverError(w, err)
		return
	}
	nav, err := s.nav(ctx, r.URL.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	postSEO := buildSEO(seoInput{
		st: st, base: baseURL(r, st), path: "/blog/" + post.Slug, pageTitle: post.Title, item: post.SEO,
		fallback: excerptOf(post), image: post.Cover, article: true, published: d, modified: post.UpdatedAt,
		hidden: preview || themePreview,
	})
	s.write(w, theme, http.StatusOK, preview || themePreview, themes.PageData{
		Site:      siteInfo(r, theme, st, themePreview),
		PageTitle: post.Title, Description: postSEO.Description, SEO: postSEO, Nav: nav, Content: body,
		Year: time.Now().Year(), Preview: preview,
	})
}

// --- RSS ---

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	LastBuildDate string    `xml:"lastBuildDate,omitempty"`
	Items         []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	GUID        rssGUID  `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
	Description string   `xml:"description"`
	Categories  []string `xml:"category"`
}

type rssGUID struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:\d{1,5})?$`)

// baseURL builds absolute URLs for the feed. The configured domain wins; the
// request's Host is only used as a fallback, and only if it looks like a host.
func baseURL(r *http.Request, st content.Settings) string {
	if st.Domain != "" && hostRe.MatchString(st.Domain) {
		return "https://" + st.Domain
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if !hostRe.MatchString(host) {
		host = "localhost"
	}
	return scheme + "://" + host
}

func (s *Service) rss(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.settings(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	posts, _, err := s.content.PublishedPosts(ctx, "", feedItems, 0)
	if err != nil {
		serverError(w, err)
		return
	}
	base := baseURL(r, st)
	ch := rssChannel{Title: st.Title, Link: base + "/", Description: "Latest posts from " + st.Title}
	for i, p := range posts {
		d := p.Date()
		if i == 0 {
			ch.LastBuildDate = d.Format(time.RFC1123Z)
		}
		item := rssItem{
			Title: p.Title, Link: base + "/blog/" + p.Slug, PubDate: d.Format(time.RFC1123Z),
			Description: excerptOf(p),
		}
		item.GUID = rssGUID{IsPermaLink: true, Value: item.Link}
		for _, c := range p.Categories {
			item.Categories = append(item.Categories, c.Name)
		}
		ch.Items = append(ch.Items, item)
	}
	out, err := xml.MarshalIndent(rssFeed{Version: "2.0", Channel: ch}, "", "  ")
	if err != nil {
		serverError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/rss+xml; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "public, max-age=300")
	w.Write([]byte(xml.Header))
	w.Write(out)
}
