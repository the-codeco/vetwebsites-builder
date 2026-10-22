package admin

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"webbuilder/internal/auth"
	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
	"webbuilder/internal/seo"
)

const maxMetaTitle = 120

// seoView is what the "seofields" form section and its search-result preview need.
type seoView struct {
	content.SEO
	SiteTitle string
	Host      string // shown in the preview's address line
	Path      string // the page's public path
	Hint      string // greyed-out text shown when the description is empty
	Heading   string // the page's own title, used by the preview until the form says otherwise
}

// seoForm reads the shared search fields from a page or post form. problem is a
// message for the user when something is invalid.
func seoForm(r *http.Request) (s content.SEO, problem string) {
	s.MetaTitle = strings.TrimSpace(r.PostFormValue("seo_title"))
	s.Description = strings.Join(strings.Fields(r.PostFormValue("seo_description")), " ")
	s.Image = strings.TrimSpace(r.PostFormValue("seo_image"))
	s.NoIndex = r.PostFormValue("seo_noindex") == "on"
	switch {
	case utf8.RuneCountInString(s.MetaTitle) > maxMetaTitle:
		problem = fmt.Sprintf("The search title must be at most %d characters.", maxMetaTitle)
	case utf8.RuneCountInString(s.Description) > maxDescription:
		problem = fmt.Sprintf("The description must be at most %d characters.", maxDescription)
	case s.Image != "" && blocks.SafeImageURL(s.Image) == "":
		problem = "The sharing image must be a path like /media/photo.jpg or an http(s):// address."
	}
	if problem == "" {
		s.Image = blocks.SafeImageURL(s.Image)
	}
	return s, problem
}

func (h *Handler) newSEOView(r *http.Request, s content.SEO, heading, path, hint string) (seoView, error) {
	st, err := h.Content.Settings(r.Context())
	if err != nil {
		return seoView{}, err
	}
	host := st.Domain
	if host == "" {
		host = r.Host
	}
	if hint == "" {
		hint = st.SEO.Description
	}
	return seoView{SEO: s, SiteTitle: st.Title, Host: host, Path: path, Hint: hint, Heading: heading}, nil
}

// --- the site-wide SEO screen ---

func (h *Handler) registerSEO(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/seo", admin(h.seoPage))
	mux.Handle("POST /admin/seo", admin(h.seoSave))
}

type seoRow struct {
	content.SEOItem
	Issues []seo.Issue
}

type seoScreen struct {
	SEO       seoView
	BaseURL   string
	HasDomain bool
	Rows      []seoRow // pages and posts with something to fix
	Checked   int      // published pages and posts that were checked
	Hidden    int      // published pages and posts that opted out of search
}

func (h *Handler) renderSEO(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string, override *content.SEO) {
	ctx := r.Context()
	st, err := h.Content.Settings(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	items, err := h.Content.SEOItems(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	screen := seoScreen{HasDomain: st.Domain != ""}
	screen.BaseURL = "http://" + r.Host
	if st.Domain != "" {
		screen.BaseURL = "https://" + st.Domain
	}
	for _, it := range items {
		if it.Status != "published" {
			continue
		}
		if it.SEO.NoIndex {
			screen.Hidden++
			continue
		}
		screen.Checked++
		desc, image := it.SEO.Description, it.SEO.Image
		if it.Kind == "post" {
			if desc == "" {
				desc = seo.Shorten(blocks.PlainText(it.Fallback), 300)
			}
			if image == "" {
				image = blocks.SafeImageURL(it.Cover)
			}
		}
		if image == "" {
			image = blocks.SafeImageURL(st.SEO.Image)
		}
		if desc == "" {
			desc = st.SEO.Description
		}
		title := it.Title
		if it.Path == "/" {
			title = ""
		}
		issues := seo.Audit(seo.Page{
			Title: title, SiteTitle: st.Title, MetaTitle: it.SEO.MetaTitle,
			Description: desc, Image: image, Published: true,
		})
		if len(issues) > 0 {
			screen.Rows = append(screen.Rows, seoRow{it, issues})
		}
	}
	current := st.SEO
	if override != nil {
		current = *override
	}
	screen.SEO = seoView{SEO: current, SiteTitle: st.Title}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "seo", pageData{
		Title: "SEO", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Data: screen,
	})
}

func (h *Handler) seoPage(w http.ResponseWriter, r *http.Request) {
	notice := ""
	if r.URL.Query().Get("saved") == "1" {
		notice = "SEO settings saved."
	}
	h.renderSEO(w, r, 200, notice, "", nil)
}

func (h *Handler) seoSave(w http.ResponseWriter, r *http.Request) {
	s, problem := seoForm(r)
	s.MetaTitle = "" // the site has no search-title override
	if problem != "" {
		h.renderSEO(w, r, 400, "", problem, &s)
		return
	}
	if err := h.Content.SetSEO(r.Context(), s); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/seo?saved=1", http.StatusSeeOther)
}
