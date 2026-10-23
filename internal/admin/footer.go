package admin

import (
	"net/http"

	"webbuilder/internal/auth"
	"webbuilder/internal/footer"
)

// The footer is admin-only, like the rest of the site's look (it was part of Appearance).
func (h *Handler) registerFooter(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/footer", admin(h.footerPage))
	mux.Handle("POST /admin/footer", admin(h.footerSave))
}

// The builder renders the same row, column and widget templates for saved content and for the
// blank "add" templates, so each is drawn from a view that carries what its controls need.
type (
	fbPart struct {
		Key, Label string
		On         bool
	}
	fbWidget struct {
		footer.Widget
		Label string
		Parts []fbPart // contact widgets: every detail, ticked when shown
	}
	fbColumn struct{ Widgets []fbWidget }
	fbRow    struct {
		Ratio, Align string
		Columns      []fbColumn
	}
)

type footerData struct {
	Rows         []fbRow
	Bottom       string
	BottomHint   string
	Style        footer.Style
	BlankRow     fbRow
	BlankColumn  fbColumn
	BlankWidgets []fbWidget // one of each type, in the order the "add" list offers them
	BlankLink    footer.Link
	PageURLs     []string
	Destinations string
}

func widgetView(w footer.Widget) fbWidget {
	v := fbWidget{Widget: w}
	for _, t := range footer.Types {
		if t.Name == w.Type {
			v.Label = t.Label
		}
	}
	if w.Type == footer.TypeContact {
		on := map[string]bool{}
		for _, p := range w.Parts {
			on[p] = true
		}
		for _, p := range footer.ContactParts {
			v.Parts = append(v.Parts, fbPart{p.Key, p.Label, on[p.Key]})
		}
	}
	return v
}

// blankWidget is what a newly added widget starts with.
func blankWidget(typ string) footer.Widget {
	w := footer.Widget{Type: typ}
	switch typ {
	case footer.TypeText:
		w.Heading = "About us"
	case footer.TypeLinks:
		w.Heading = "Links"
	case footer.TypeContact:
		w.Heading = "Contact"
		for _, p := range footer.ContactParts {
			w.Parts = append(w.Parts, p.Key)
		}
	case footer.TypeSocial:
		w.Heading = "Follow us"
	case footer.TypeNewsletter:
		w.Heading, w.Text, w.Button, w.Success = "Stay in the loop", "Get our updates by email.", "Subscribe", "Thanks for subscribing!"
	case footer.TypeSearch:
		w.Heading = "Search the site"
	}
	return w
}

func rowViews(rows []footer.Row) []fbRow {
	var out []fbRow
	for _, r := range rows {
		v := fbRow{Ratio: r.Ratio, Align: r.Align}
		for _, c := range r.Columns {
			var cv fbColumn
			for _, w := range c.Widgets {
				cv.Widgets = append(cv.Widgets, widgetView(w))
			}
			v.Columns = append(v.Columns, cv)
		}
		out = append(out, v)
	}
	return out
}

func (h *Handler) renderFooter(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string, typed *footer.Footer) {
	f := footer.Empty()
	if typed != nil {
		f = *typed
	} else {
		st, err := h.Content.Settings(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		f = st.Footer
	}
	urls, dests, err := h.linkTargets(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	d := footerData{
		Rows: rowViews(f.Rows), Bottom: f.Bottom, BottomHint: footer.DefaultBottom, Style: f.Style,
		BlankRow:    fbRow{Ratio: "equal", Align: "left", Columns: make([]fbColumn, 3)},
		BlankColumn: fbColumn{}, PageURLs: urls, Destinations: dests,
	}
	for _, t := range footer.Types {
		d.BlankWidgets = append(d.BlankWidgets, widgetView(blankWidget(t.Name)))
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "footer", pageData{
		Title: "Footer", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Wide: true, Data: d,
	})
}

func (h *Handler) footerPage(w http.ResponseWriter, r *http.Request) {
	notice := ""
	if r.URL.Query().Get("saved") == "1" {
		notice = "Footer saved."
	}
	h.renderFooter(w, r, http.StatusOK, notice, "", nil)
}

// footerSave stores the whole footer from the builder's JSON. Text widgets are basic HTML, cleaned the
// same way as a block's text: scripts, event handlers and unsafe links are removed on save and again
// when the page is drawn.
func (h *Handler) footerSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	in, err := footer.Decode([]byte(r.PostFormValue("structure")))
	if err != nil {
		h.renderFooter(w, r, http.StatusBadRequest, "", err.Error(), nil)
		return
	}
	clean, err := footer.Validate(in)
	if err != nil {
		h.renderFooter(w, r, http.StatusBadRequest, "", err.Error(), &in) // what they built comes back with the error
		return
	}
	if err := h.Content.SetFooter(r.Context(), clean); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/footer?saved=1", http.StatusSeeOther)
}
