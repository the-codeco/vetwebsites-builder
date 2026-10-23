package admin

import (
	"net/http"

	"webbuilder/internal/auth"
	"webbuilder/internal/footer"
)

// The sidebar is admin-only, like the footer.
func (h *Handler) registerSidebar(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/sidebar", admin(h.sidebarPage))
	mux.Handle("POST /admin/sidebar", admin(h.sidebarSave))
}

type sidebarData struct {
	Side, Width  string
	Widgets      []fbWidget
	BlankWidgets []fbWidget
	BlankLink    footer.Link
	PageURLs     []string
	Destinations string
}

func (h *Handler) renderSidebar(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string, typed *footer.Sidebar) {
	var sb footer.Sidebar
	if typed != nil {
		sb = *typed
	} else {
		st, err := h.Content.Settings(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		sb = st.Sidebar
	}
	urls, dests, err := h.linkTargets(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	d := sidebarData{Side: sb.Side, Width: sb.Width, PageURLs: urls, Destinations: dests}
	for _, wd := range sb.Widgets {
		d.Widgets = append(d.Widgets, widgetView(wd))
	}
	for _, t := range footer.Types {
		d.BlankWidgets = append(d.BlankWidgets, widgetView(blankWidget(t.Name)))
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "sidebar", pageData{Title: "Sidebar", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Wide: true, Data: d})
}

func (h *Handler) sidebarPage(w http.ResponseWriter, r *http.Request) {
	notice := ""
	if r.URL.Query().Get("saved") == "1" {
		notice = "Sidebar saved."
	}
	h.renderSidebar(w, r, http.StatusOK, notice, "", nil)
}

// sidebarSave stores the sidebar from the builder's JSON, checked like the footer's widgets.
func (h *Handler) sidebarSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	in, err := footer.DecodeSidebar([]byte(r.PostFormValue("structure")))
	if err != nil {
		h.renderSidebar(w, r, http.StatusBadRequest, "", err.Error(), nil)
		return
	}
	clean, err := footer.ValidateSidebar(in)
	if err != nil {
		h.renderSidebar(w, r, http.StatusBadRequest, "", err.Error(), &in)
		return
	}
	if err := h.Content.SetSidebar(r.Context(), clean); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/sidebar?saved=1", http.StatusSeeOther)
}
