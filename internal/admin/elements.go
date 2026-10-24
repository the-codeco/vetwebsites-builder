package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"webbuilder/internal/auth"
	"webbuilder/internal/content"
	"webbuilder/internal/elements"
)

func (h *Handler) registerElements(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/elements", admin(h.elementsList))
	mux.Handle("GET /admin/elements/new", admin(h.elementNew))
	mux.Handle("POST /admin/elements", admin(h.elementCreate))
	mux.Handle("GET /admin/elements/{id}", admin(h.elementEdit))
	mux.Handle("POST /admin/elements/{id}", admin(h.elementUpdate))
	mux.Handle("POST /admin/elements/{id}/toggle", admin(h.elementToggle))
	mux.Handle("POST /admin/elements/{id}/move", admin(h.elementMove))
	mux.Handle("POST /admin/elements/{id}/delete", admin(h.elementDelete))
}

// elementRow is one element in the list.
type elementRow struct {
	elements.Element
	Status elements.Status
	Where  string
}

type elementsData struct {
	Rows  []elementRow
	Kinds []elements.Kind
	Room  bool
}

func (h *Handler) elementsList(w http.ResponseWriter, r *http.Request) {
	list, err := h.Content.Elements(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	now := time.Now()
	d := elementsData{Kinds: elements.Kinds, Room: len(list) < elements.MaxElements}
	for _, e := range list {
		where := "Every page"
		if len(e.Pages) > 0 {
			where = fmt.Sprintf("%d page%s", len(e.Pages), plural(len(e.Pages)))
		}
		d.Rows = append(d.Rows, elementRow{Element: e, Status: e.StatusAt(now), Where: where})
	}
	notice := map[string]string{"saved": "Saved.", "deleted": "Removed.", "on": "Switched on.", "off": "Switched off."}[r.URL.Query().Get("done")]
	id := auth.CurrentIdentity(r)
	h.render(w, r, http.StatusOK, "elements", pageData{Title: "Site-wide elements", User: id, CSRF: id.CSRF, Notice: notice, Data: d})
}

// pageOption is a page the owner can pick for an element to show on.
type pageOption struct {
	Path, Title string
	Chosen      bool
}

type elementForm struct {
	E          elements.Element
	KindLabel  string
	KindHelp   string
	Start, End *time.Time
	Some       bool // only on chosen pages
	Options    []pageOption
	Extra      string // other addresses, one per line
	Preview    string // an address to see it on, once saved
}

// elementFormFor prepares the form for an element (new or saved), listing the site's published pages.
func (h *Handler) elementFormFor(r *http.Request, e elements.Element) (elementForm, error) {
	f := elementForm{E: e, Start: e.Start, End: e.End, Some: len(e.Pages) > 0}
	for _, k := range elements.Kinds {
		if k.ID == e.Kind {
			f.KindLabel, f.KindHelp = k.Label, k.Help
		}
	}
	chosen := map[string]bool{}
	for _, p := range e.Pages {
		chosen[p] = true
	}
	pages, err := h.Content.ListPages(r.Context())
	if err != nil {
		return f, err
	}
	add := func(path, title string) {
		f.Options = append(f.Options, pageOption{Path: path, Title: title, Chosen: chosen[path]})
		delete(chosen, path)
	}
	add("/", "Home page")
	add("/blog/*", "The blog (index and every post)")
	for _, p := range pages {
		if p.Status != "published" || p.Slug == "home" {
			continue
		}
		add("/"+p.Slug, p.Title)
	}
	var extra []string
	for _, p := range e.Pages {
		if chosen[p] {
			extra = append(extra, p)
		}
	}
	f.Extra = strings.Join(extra, "\n")
	if e.ID != 0 {
		f.Preview = "/?wb-element=" + strconv.FormatInt(e.ID, 10)
	}
	return f, nil
}

func (h *Handler) renderElementForm(w http.ResponseWriter, r *http.Request, status int, e elements.Element, errMsg string) {
	f, err := h.elementFormFor(r, e)
	if err != nil {
		serverError(w, err)
		return
	}
	id := auth.CurrentIdentity(r)
	title := "New " + strings.ToLower(f.KindLabel)
	if e.ID != 0 {
		title = "Edit " + strings.ToLower(f.KindLabel)
	}
	h.render(w, r, status, "element_edit", pageData{Title: title, User: id, CSRF: id.CSRF, Error: errMsg, Data: f})
}

func (h *Handler) elementNew(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if !elements.ValidKind(kind) {
		http.Redirect(w, r, "/admin/elements", http.StatusSeeOther)
		return
	}
	e := elements.Element{Kind: kind, Enabled: true}
	switch kind { // sensible starting points
	case elements.KindBar:
		e.Config = elements.Config{Style: elements.StyleInfo, Dismissible: true, DismissDays: 7}
	case elements.KindAlert:
		e.Config = elements.Config{DismissDays: 1}
	case elements.KindPopup:
		e.Config = elements.Config{Mode: elements.ModeInfo, Trigger: elements.TriggerDelay, Delay: 5, DismissDays: 14}
	case elements.KindContact:
		e.Config = elements.Config{Action: elements.ActionCall, Side: elements.SideRight}
	}
	h.renderElementForm(w, r, http.StatusOK, e, "")
}

func elementID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (h *Handler) elementEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := elementID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	e, err := h.Content.Element(r.Context(), id)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	h.renderElementForm(w, r, http.StatusOK, e, "")
}

// timeFrom reads a start or end time from a form: the browser sends it as UTC in "<name>_at" (a small script converts what
// the person typed in their own time zone); without the script the typed "<name>_local" is read as UTC.
func timeFrom(r *http.Request, name string) (*time.Time, error) {
	raw, layout := strings.TrimSpace(r.PostFormValue(name+"_at")), time.RFC3339
	if raw == "" {
		raw, layout = strings.TrimSpace(r.PostFormValue(name+"_local")), "2006-01-02T15:04"
	}
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(layout, raw)
	if err != nil {
		return nil, errors.New("Choose a valid date and time.")
	}
	t = t.UTC()
	return &t, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// elementFromForm reads an element from the form. Checking it is elements.Clean's job, when it is saved.
func elementFromForm(r *http.Request, kind string, id int64) (elements.Element, error) {
	e := elements.Element{ID: id, Kind: kind, Name: r.PostFormValue("name"), Enabled: r.PostFormValue("enabled") == "on"}
	e.Config = elements.Config{
		Text: r.PostFormValue("text"), Heading: r.PostFormValue("heading"), Link: r.PostFormValue("link"), LinkLabel: r.PostFormValue("link_label"),
		Style: r.PostFormValue("style"), Dismissible: r.PostFormValue("dismissible") == "on", DismissDays: atoi(r.PostFormValue("dismiss_days")),
		Mode: r.PostFormValue("mode"), Trigger: r.PostFormValue("trigger"), Delay: atoi(r.PostFormValue("delay")), Scroll: atoi(r.PostFormValue("scroll")),
		Button: r.PostFormValue("button"), Success: r.PostFormValue("success"),
		Action: r.PostFormValue("action"), Label: r.PostFormValue("label"), Side: r.PostFormValue("side"),
	}
	if r.PostFormValue("pages_mode") == "some" {
		e.Pages = append(e.Pages, r.PostForm["pages"]...)
		e.Pages = append(e.Pages, strings.Split(r.PostFormValue("pages_extra"), "\n")...)
	}
	var err error
	if e.Start, err = timeFrom(r, "start"); err != nil {
		return e, errors.New("The start: " + err.Error())
	}
	if e.End, err = timeFrom(r, "end"); err != nil {
		return e, errors.New("The end: " + err.Error())
	}
	return e, nil
}

func (h *Handler) elementCreate(w http.ResponseWriter, r *http.Request) { h.saveElement(w, r, 0) }

func (h *Handler) elementUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := elementID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.saveElement(w, r, id)
}

func (h *Handler) saveElement(w http.ResponseWriter, r *http.Request, id int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	kind := r.PostFormValue("kind")
	if id != 0 { // an element keeps its kind
		cur, err := h.Content.Element(r.Context(), id)
		if errors.Is(err, content.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		kind = cur.Kind
	}
	if !elements.ValidKind(kind) {
		http.Error(w, "Choose what kind of element to add.", http.StatusBadRequest)
		return
	}
	e, err := elementFromForm(r, kind, id)
	if err == nil {
		_, err = h.Content.SaveElement(r.Context(), e)
	}
	switch {
	case errors.Is(err, content.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, content.ErrElementsFull):
		h.renderElementForm(w, r, http.StatusBadRequest, e, fmt.Sprintf("The site can hold at most %d elements. Remove one you no longer use.", elements.MaxElements))
	case err != nil:
		h.renderElementForm(w, r, http.StatusBadRequest, e, err.Error())
	default:
		http.Redirect(w, r, "/admin/elements?done=saved", http.StatusSeeOther)
	}
}

func (h *Handler) elementToggle(w http.ResponseWriter, r *http.Request) {
	id, ok := elementID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	on := r.PostFormValue("on") == "1"
	if err := h.Content.SetElementEnabled(r.Context(), id, on); err != nil {
		if errors.Is(err, content.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		serverError(w, err)
		return
	}
	done := "off"
	if on {
		done = "on"
	}
	http.Redirect(w, r, "/admin/elements?done="+done, http.StatusSeeOther)
}

func (h *Handler) elementMove(w http.ResponseWriter, r *http.Request) {
	id, ok := elementID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	dir := 1
	if r.PostFormValue("dir") == "up" {
		dir = -1
	}
	if err := h.Content.MoveElement(r.Context(), id, dir); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/elements", http.StatusSeeOther)
}

func (h *Handler) elementDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := elementID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := h.Content.DeleteElement(r.Context(), id); err != nil && !errors.Is(err, content.ErrNotFound) {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/elements?done=deleted", http.StatusSeeOther)
}
