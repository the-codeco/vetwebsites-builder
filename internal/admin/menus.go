package admin

import (
	"context"
	"encoding/json"
	"net/http"

	"webbuilder/internal/auth"
	"webbuilder/internal/menus"
)

func (h *Handler) registerMenus(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/menus", authed(h.menusPage))
	mux.Handle("POST /admin/menus/{section}", authed(h.menuSave))
}

// sectionView is one menu card in the builder.
type sectionView struct {
	menus.Section
	Items  []menus.Item
	Sticky bool
	Style  menus.Style
}

type menusData struct {
	Sections []sectionView
	PageURLs []string
	// Destinations is the JSON list behind the "Find" search: every page and post
	// with its address, so picking one fills in a menu row's label and URL.
	Destinations string
}

// destination is one search result in the menu builder.
type destination struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Type  string `json:"type"` // "Page", "Post" or "Blog"
	Draft bool   `json:"draft,omitempty"`
}

// menuOverride re-shows what someone typed after a validation error.
type menuOverride struct {
	Key    string
	Items  []menus.Item
	Sticky bool
	Style  menus.Style
}

// normalise gives every item a kind so the editor can show the right controls.
func normalise(items []menus.Item) []menus.Item {
	out := make([]menus.Item, len(items))
	copy(out, items)
	for i := range out {
		if out[i].Kind == "" {
			out[i].Kind = menus.KindLink
		}
	}
	return out
}

func (h *Handler) renderMenus(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string, over *menuOverride) {
	saved, err := h.Content.Menus(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	var views []sectionView
	for _, sec := range menus.Sections {
		v := sectionView{Section: sec, Items: normalise(saved[sec.Key].Items), Sticky: saved[sec.Key].Sticky, Style: saved[sec.Key].Style}
		if over != nil && over.Key == sec.Key {
			v.Items, v.Sticky, v.Style = normalise(over.Items), over.Sticky, over.Style
		}
		views = append(views, v)
	}
	urls, dests, err := h.linkTargets(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "menus", pageData{
		Title: "Menus", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Wide: true,
		Data: menusData{Sections: views, PageURLs: urls, Destinations: dests},
	})
}

// linkTargets are the site's own addresses for link fields: the plain list behind an address box's
// suggestions, and the JSON behind "Find…" (every page and post with its title).
func (h *Handler) linkTargets(ctx context.Context) (urls []string, destJSON string, err error) {
	pages, err := h.Content.ListPages(ctx)
	if err != nil {
		return nil, "", err
	}
	posts, err := h.Content.ListPosts(ctx)
	if err != nil {
		return nil, "", err
	}
	urls = []string{"/blog"}
	var dests []destination
	for _, p := range pages {
		urls = append(urls, pagePath(p.Slug))
		dests = append(dests, destination{Title: p.Title, URL: pagePath(p.Slug), Type: "Page", Draft: p.Status != "published"})
	}
	dests = append(dests, destination{Title: "Blog", URL: "/blog", Type: "Blog"})
	for _, p := range posts {
		dests = append(dests, destination{Title: p.Title, URL: "/blog/" + p.Slug, Type: "Post", Draft: p.Status != "published"})
	}
	data, err := json.Marshal(dests)
	if err != nil {
		return nil, "", err
	}
	return urls, string(data), nil
}

func (h *Handler) menusPage(w http.ResponseWriter, r *http.Request) {
	notice := ""
	if sec, ok := menus.SectionByKey(r.URL.Query().Get("saved")); ok {
		notice = sec.Label + " saved."
	}
	h.renderMenus(w, r, 200, notice, "", nil)
}

func (h *Handler) menuSave(w http.ResponseWriter, r *http.Request) {
	sec, ok := menus.SectionByKey(r.PathValue("section"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	sticky := r.PostFormValue("sticky") == "on"
	var style menus.Style
	if sec.Styled {
		style = menus.Style{Background: r.PostFormValue("style_background"), Text: r.PostFormValue("style_text"), Size: r.PostFormValue("style_size")}
	}

	items, err := menus.Decode([]byte(r.PostFormValue("structure")))
	if err != nil {
		h.renderMenus(w, r, http.StatusBadRequest, "", err.Error(), nil)
		return
	}
	clean, err := menus.Validate(sec, items)
	if err != nil {
		h.renderMenus(w, r, http.StatusBadRequest, "", err.Error(), &menuOverride{Key: sec.Key, Items: items, Sticky: sticky, Style: style})
		return
	}
	if sec.Styled {
		typed := style
		if style, err = menus.ValidateStyle(style); err != nil {
			h.renderMenus(w, r, http.StatusBadRequest, "", err.Error(), &menuOverride{Key: sec.Key, Items: items, Sticky: sticky, Style: typed})
			return
		}
	}
	if err := h.Content.SaveMenu(r.Context(), sec.Key, clean, sticky, style); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/menus?saved="+sec.Key, http.StatusSeeOther)
}
