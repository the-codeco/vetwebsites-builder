package admin

import (
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
}

type menusData struct {
	Sections []sectionView
	PageURLs []string
}

// menuOverride re-shows what someone typed after a validation error.
type menuOverride struct {
	Key    string
	Items  []menus.Item
	Sticky bool
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
		v := sectionView{Section: sec, Items: normalise(saved[sec.Key].Items), Sticky: saved[sec.Key].Sticky}
		if over != nil && over.Key == sec.Key {
			v.Items, v.Sticky = normalise(over.Items), over.Sticky
		}
		views = append(views, v)
	}
	pages, err := h.Content.ListPages(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	urls := []string{"/blog"}
	for _, p := range pages {
		urls = append(urls, pagePath(p.Slug))
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "menus", pageData{
		Title: "Menus", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Wide: true,
		Data: menusData{Sections: views, PageURLs: urls},
	})
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

	items, err := menus.Decode([]byte(r.PostFormValue("structure")))
	if err != nil {
		h.renderMenus(w, r, http.StatusBadRequest, "", err.Error(), nil)
		return
	}
	clean, err := menus.Validate(sec, items)
	if err != nil {
		h.renderMenus(w, r, http.StatusBadRequest, "", err.Error(), &menuOverride{Key: sec.Key, Items: items, Sticky: sticky})
		return
	}
	if err := h.Content.SaveMenu(r.Context(), sec.Key, clean, sticky); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/menus?saved="+sec.Key, http.StatusSeeOther)
}
