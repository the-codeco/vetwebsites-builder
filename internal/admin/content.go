package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"webbuilder/internal/auth"
	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
	"webbuilder/internal/site"
)

const (
	maxTitle       = 200
	maxDescription = 300
	homeSlug       = "home"
)

func (h *Handler) registerContent(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/pages", authed(h.pagesList))
	mux.Handle("POST /admin/pages", authed(h.pageCreate))
	mux.Handle("GET /admin/pages/{id}", authed(h.pageEdit))
	mux.Handle("POST /admin/pages/{id}", authed(h.pageSave))
	mux.Handle("POST /admin/pages/{id}/delete", authed(h.pageDelete))
	// every change to a page's blocks first saves a restore point (see history)
	mux.Handle("POST /admin/pages/{id}/blocks", authed(h.history(h.blockAdd, false)))
	mux.Handle("POST /admin/blocks/{id}", authed(h.history(h.blockSave, true)))
	mux.Handle("POST /admin/blocks/{id}/move", authed(h.history(h.blockMove, true)))
	mux.Handle("POST /admin/blocks/{id}/children", authed(h.history(h.blockAddChild, true)))
	mux.Handle("POST /admin/blocks/{id}/slot", authed(h.history(h.blockSlot, true)))
	mux.Handle("POST /admin/blocks/{id}/share", authed(h.history(h.blockShare, true)))
	mux.Handle("POST /admin/blocks/{id}/delete", authed(h.history(h.blockDelete, true)))
	mux.Handle("POST /admin/pages/{id}/history/{rev}/restore", authed(h.revisionRestore))
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func pagePath(slug string) string {
	if slug == homeSlug {
		return "/"
	}
	return "/" + slug
}

// --- pages list / create ---

type pagesData struct {
	Pages             []content.Page
	NewTitle, NewSlug string
}

func (h *Handler) renderPages(w http.ResponseWriter, r *http.Request, status int, errMsg, title, slug string) {
	id := auth.CurrentIdentity(r)
	pages, err := h.Content.ListPages(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	h.render(w, r, status, "pages", pageData{
		Title: "Pages", User: id, CSRF: id.CSRF, Error: errMsg, Data: pagesData{pages, title, slug},
	})
}

func (h *Handler) pagesList(w http.ResponseWriter, r *http.Request) {
	h.renderPages(w, r, 200, "", "", "")
}

func (h *Handler) pageCreate(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.PostFormValue("title"))
	slug := strings.ToLower(strings.TrimSpace(r.PostFormValue("slug")))
	auto := slug == ""
	if auto {
		slug = site.Slugify(title)
	}
	switch {
	case title == "":
		h.renderPages(w, r, 400, "Title is required.", title, slug)
		return
	case utf8.RuneCountInString(title) > maxTitle:
		h.renderPages(w, r, 400, "Title is too long.", title, slug)
		return
	}
	if msg := site.CheckSlug(slug); msg != "" {
		h.renderPages(w, r, 400, msg, title, slug)
		return
	}
	candidate := slug
	var id int64
	var err error
	for i := 2; ; i++ {
		id, err = h.Content.CreatePage(r.Context(), candidate, title)
		if !errors.Is(err, content.ErrSlugTaken) || !auto || i > 20 {
			break
		}
		candidate = fmt.Sprintf("%s-%d", slug, i)
	}
	if errors.Is(err, content.ErrSlugTaken) {
		h.renderPages(w, r, 409, "A page with that slug already exists.", title, slug)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d", id), http.StatusSeeOther)
}

// --- page editor ---

type fieldView struct {
	Name, Label string
	Kind        string
	Str         string
	Checked     bool
	Options     []optionView

	// a repeating list: its items, a blank one to copy for "Add", and what one is called
	Items     []repeatItem
	Blank     repeatItem
	ItemLabel string
	Max       int
}

type repeatItem struct {
	N      int
	Fields []fieldView
}

type optionView struct {
	Value, Label string
	Selected     bool
}

type blockView struct {
	ID                  int64
	Type, Label         string
	Summary             string
	Fields, StyleFields []fieldView
	Open                bool
	Flash               string
	Nested              bool        // inside a layout
	CanShare            bool        // can be moved into the shared library
	Shared              *sharedView // set when this block is a shared block

	// a layout block also shows its columns, each a list of blocks with a way to add more
	IsLayout      bool
	Columns       []columnView
	NestTypes     []*blocks.Type
	SharedChoices []content.Reusable // library blocks that can be added to a column
}

// columnView is one column of a layout in the editor.
type columnView struct {
	Index, Number     int
	Blocks            []blockView
	CanLeft, CanRight bool
}

type listView struct {
	PageID int64
	Blocks []blockView
	Types  []*blocks.Type
	Shared []content.Reusable // the library, for "add a shared block"
}

type editData struct {
	History    []content.Revision
	Page       content.Page
	List       listView
	PreviewURL string
	IsHome     bool
	SEO        seoView
}

func fieldViews(prefix string, fields []blocks.Field, values map[string]any) []fieldView {
	out := make([]fieldView, 0, len(fields))
	for _, f := range fields {
		fv := fieldView{Name: prefix + f.Key, Label: f.Label, Kind: string(f.Kind)}
		if f.Kind == blocks.KindRepeater {
			fv.ItemLabel, fv.Max = f.ItemLabel, repeaterMax(f)
			items, _ := values[f.Key].([]map[string]any)
			for i, item := range items {
				fv.Items = append(fv.Items, repeatItem{N: i + 1, Fields: fieldViews(fv.Name+"."+strconv.Itoa(i)+".", f.Item, item)})
			}
			fv.Blank = repeatItem{Fields: fieldViews(fv.Name+".__N__.", f.Item, nil)}
			out = append(out, fv)
			continue
		}
		switch v := values[f.Key].(type) {
		case string:
			fv.Str = v
		case float64:
			fv.Str = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			fv.Checked = v
		case []string:
			fv.Str = strings.Join(v, "\n")
		}
		for _, o := range f.Options {
			fv.Options = append(fv.Options, optionView{o.Value, o.Label, o.Value == fv.Str})
		}
		out = append(out, fv)
	}
	return out
}

// summarize returns a short preview of a block's content for its collapsed header.
func summarize(bt *blocks.Type, props map[string]any) string {
	for _, f := range bt.Fields {
		s, _ := props[f.Key].(string)
		if s == "" || (f.Kind != blocks.KindText && f.Kind != blocks.KindTextarea && f.Kind != blocks.KindRichText) {
			continue
		}
		if f.Kind == blocks.KindRichText {
			s = blocks.PlainText(s)
		}
		if r := []rune(s); len(r) > 60 {
			s = string(r[:60]) + "…"
		}
		if s != "" {
			return s
		}
	}
	return ""
}

// newBlockView prepares a block for the editor. It is shown open if it is the block just worked on (openID), or
// holds that block.
func newBlockView(b content.Block, openID int64, flash string, lib library) blockView {
	bt, ok := blocks.Get(b.Type)
	open := b.ID == openID
	if !ok {
		return blockView{ID: b.ID, Type: b.Type, Label: b.Type + " (unknown type)", Open: open}
	}
	props := bt.Clean(b.Props)
	styleFields := blocks.StyleFields()
	if b.ParentID != 0 { // a block in a column fills the column, so "content width" would do nothing
		kept := styleFields[:0:0]
		for _, f := range styleFields {
			if f.Key != "width" {
				kept = append(kept, f)
			}
		}
		styleFields = kept
	}
	v := blockView{
		ID: b.ID, Type: b.Type, Label: bt.Label, Summary: summarize(bt, props),
		Fields:      fieldViews("p.", bt.Fields, props),
		StyleFields: fieldViews("s.", styleFields, blocks.CleanStyleMap(b.Style)),
		Open:        open, Flash: flash, Nested: b.ParentID != 0,
		CanShare: blocks.CanBeShared(b.Type),
	}
	if b.Type == blocks.ReusableType {
		// a shared block is edited once, in the library; here it only says which one it is
		sv := &sharedView{ID: blocks.RefOf(props)}
		if r, ok := lib.byID[sv.ID]; ok {
			sv.Name = r.Name
			if rt, ok := blocks.Get(r.Type); ok {
				sv.TypeLabel = rt.Label
			}
		} else {
			sv.Missing = true
		}
		v.Shared, v.Summary = sv, sv.Name
	}
	if b.Type == blocks.LayoutType {
		v.IsLayout = true
		v.SharedChoices = lib.list
		for _, t := range blocks.Pickable() {
			if blocks.Nestable(t.Name) {
				v.NestTypes = append(v.NestTypes, t)
			}
		}
		cols := blocks.LayoutColumns(props)
		for i := 0; i < cols; i++ {
			c := columnView{Index: i, Number: i + 1, CanLeft: i > 0, CanRight: i < cols-1}
			if i < len(b.Children) {
				for _, child := range b.Children[i] {
					cv := newBlockView(child, openID, "", lib)
					v.Open = v.Open || cv.Open
					c.Blocks = append(c.Blocks, cv)
				}
			}
			v.Columns = append(v.Columns, c)
		}
	}
	return v
}

func (h *Handler) listFor(r *http.Request, pageID, openID int64) (listView, error) {
	rows, err := h.Content.Blocks(r.Context(), pageID)
	if err != nil {
		return listView{}, err
	}
	lib, err := h.library(r.Context())
	if err != nil {
		return listView{}, err
	}
	lv := listView{PageID: pageID, Types: blocks.Pickable(), Shared: lib.list}
	for _, b := range rows {
		lv.Blocks = append(lv.Blocks, newBlockView(b, openID, "", lib))
	}
	return lv, nil
}

// renderEdit shows the editor; override (if set) holds unsaved settings after a validation error.
func (h *Handler) renderEdit(w http.ResponseWriter, r *http.Request, status int, id int64, notice, errMsg string, override *content.Page) {
	page, err := h.Content.Page(r.Context(), id)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	list, err := h.listFor(r, id, 0)
	if err != nil {
		serverError(w, err)
		return
	}
	history, err := h.Content.Revisions(r.Context(), id)
	if err != nil {
		serverError(w, err)
		return
	}
	data := editData{History: history, Page: page, List: list, PreviewURL: pagePath(page.Slug), IsHome: page.Slug == homeSlug}
	if override != nil {
		data.Page = *override
	}
	heading := data.Page.Title
	if data.IsHome {
		heading = "" // the home page's tab title is just the site name
	}
	data.SEO, err = h.newSEOView(r, data.Page.SEO, heading, data.PreviewURL, "")
	if err != nil {
		serverError(w, err)
		return
	}
	ident := auth.CurrentIdentity(r)
	h.render(w, r, status, "page_edit", pageData{
		Title: "Edit " + page.Title, User: ident, CSRF: ident.CSRF, Notice: notice, Error: errMsg, Wide: true, Data: data,
	})
}

func (h *Handler) pageEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	notice := ""
	switch {
	case r.URL.Query().Get("saved") == "1":
		notice = "Page settings saved."
	case r.URL.Query().Get("restored") == "1":
		notice = "The earlier version is back. The version you had before is saved in the history below, in case you change your mind."
	}
	h.renderEdit(w, r, 200, id, notice, "", nil)
}

func (h *Handler) pageSave(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	page, err := h.Content.Page(r.Context(), id)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	in := page
	in.Title = strings.TrimSpace(r.PostFormValue("title"))
	in.Status = r.PostFormValue("status")
	var seoProblem string
	in.SEO, seoProblem = seoForm(r)
	var scheduleProblem string
	in.PublishAt, scheduleProblem = publishAtFrom(r, in.Status, time.Now())
	in.Slug = strings.ToLower(strings.TrimSpace(r.PostFormValue("slug")))
	if page.Slug == homeSlug {
		in.Slug = homeSlug // the home page's URL is fixed
	}

	fail := func(status int, msg string) { h.renderEdit(w, r, status, id, "", msg, &in) }
	switch {
	case in.Title == "":
		fail(400, "Title is required.")
		return
	case utf8.RuneCountInString(in.Title) > maxTitle:
		fail(400, "Title is too long.")
		return
	case in.Status != "draft" && in.Status != "published":
		fail(400, "Choose a valid status.")
		return
	case seoProblem != "":
		fail(400, seoProblem)
		return
	case scheduleProblem != "":
		fail(400, scheduleProblem)
		return
	}
	if msg := site.CheckSlug(in.Slug); msg != "" {
		fail(400, msg)
		return
	}
	err = h.Content.UpdatePage(r.Context(), id, in.Slug, in.Title, in.Status, in.SEO, in.PublishAt)
	if errors.Is(err, content.ErrSlugTaken) {
		fail(409, "Another page already uses that slug.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d?saved=1", id), http.StatusSeeOther)
}

func (h *Handler) pageDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	page, err := h.Content.Page(r.Context(), id)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if page.Slug == homeSlug {
		h.renderEdit(w, r, 400, id, "", "The home page can't be deleted.", nil)
		return
	}
	if err := h.Content.DeletePage(r.Context(), id); err != nil && !errors.Is(err, content.ErrNotFound) {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/pages", http.StatusSeeOther)
}

// --- blocks (htmx fragments) ---

func (h *Handler) previewRefresh(w http.ResponseWriter) {
	w.Header().Set("HX-Trigger", "preview-refresh")
}

func (h *Handler) partial(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := h.pages["page_edit"].ExecuteTemplate(w, name, data); err != nil {
		serverError(w, err)
	}
}

func (h *Handler) respondList(w http.ResponseWriter, r *http.Request, pageID, openID int64) {
	lv, err := h.listFor(r, pageID, openID)
	if err != nil {
		serverError(w, err)
		return
	}
	h.previewRefresh(w)
	h.partial(w, 200, "blocklist", lv)
}

func (h *Handler) blockAdd(w http.ResponseWriter, r *http.Request) {
	pageID, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	bt, ok := blocks.Get(r.PostFormValue("type"))
	if !ok {
		http.Error(w, "Unknown block type", http.StatusBadRequest)
		return
	}
	props, ok := h.newProps(w, r, bt)
	if !ok {
		return
	}
	b, err := h.Content.AddBlock(r.Context(), pageID, bt.Name, props, blocks.CleanStyleMap(nil))
	switch {
	case errors.Is(err, content.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, content.ErrTooMany):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	h.respondList(w, r, pageID, b.ID)
}

// formValues extracts a schema's fields from a submitted form (keys are prefix+field key).
func formValues(fields []blocks.Field, prefix string, form url.Values) map[string]any {
	raw := make(map[string]any, len(fields))
	for _, f := range fields {
		if f.Kind == blocks.KindRepeater {
			raw[f.Key] = f.RepeaterFormValues(prefix, form)
			continue
		}
		vals, present := form[prefix+f.Key]
		switch {
		case f.Kind == blocks.KindBool:
			raw[f.Key] = present && vals[0] == "on" // unchecked boxes aren't submitted
		case present && len(vals) > 0:
			raw[f.Key] = vals[0]
		}
	}
	return raw
}

func (h *Handler) blockSave(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := h.Content.Block(r.Context(), id)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	bt, known := blocks.Get(b.Type)
	if !known {
		http.Error(w, "Unknown block type", http.StatusBadRequest)
		return
	}
	if b.Type == blocks.ReusableType { // a shared block is edited in the library, not on the page
		http.Error(w, "Edit a shared block in the shared block library", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	b.Props = bt.Clean(formValues(bt.Fields, "p.", r.PostForm))
	b.Style = blocks.CleanStyleMap(formValues(blocks.StyleFields(), "s.", r.PostForm))
	if err := h.Content.UpdateBlock(r.Context(), id, b.Props, b.Style); err != nil {
		serverError(w, err)
		return
	}
	if b.Type == blocks.LayoutType { // its columns may have changed, and blocks may have moved between them
		h.respondList(w, r, b.PageID, b.ID)
		return
	}
	h.previewRefresh(w)
	h.partial(w, 200, "blockcard", newBlockView(b, b.ID, "Saved", library{}))
}

// blockAddChild adds a block to one column of a layout. New blocks in a column start with no padding of their
// own, so the columns line up; the layout's spacing does the rest.
func (h *Handler) blockAddChild(w http.ResponseWriter, r *http.Request) {
	parentID, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	slot, err := strconv.Atoi(r.PostFormValue("slot"))
	bt, known := blocks.Get(r.PostFormValue("type"))
	if err != nil || !known || !blocks.Nestable(bt.Name) {
		http.Error(w, "Unknown block type", http.StatusBadRequest)
		return
	}
	props, ok := h.newProps(w, r, bt)
	if !ok {
		return
	}
	b, err := h.Content.AddChildBlock(r.Context(), parentID, slot, bt.Name, props, blocks.CleanStyleMap(map[string]any{"padding": "none"}))
	switch {
	case errors.Is(err, content.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, content.ErrTooMany), errors.Is(err, content.ErrNotNestable):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	h.respondList(w, r, b.PageID, b.ID)
}

// blockSlot moves a block in a layout to the next column to the left or right.
func (h *Handler) blockSlot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	dir := r.PostFormValue("dir")
	if dir != "left" && dir != "right" {
		http.Error(w, "Bad direction", http.StatusBadRequest)
		return
	}
	pageID, err := h.Content.MoveBlockToColumn(r.Context(), id, dir == "right")
	switch {
	case errors.Is(err, content.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, content.ErrTooMany):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	h.respondList(w, r, pageID, id)
}

func (h *Handler) blockMove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	dir := r.PostFormValue("dir")
	if dir != "up" && dir != "down" {
		http.Error(w, "Bad direction", http.StatusBadRequest)
		return
	}
	pageID, err := h.Content.MoveBlock(r.Context(), id, dir == "up")
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	h.respondList(w, r, pageID, 0)
}

func (h *Handler) blockDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	pageID, err := h.Content.DeleteBlock(r.Context(), id)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	h.respondList(w, r, pageID, 0)
}

// repeaterMax is how many items the editor lets a list grow to.
func repeaterMax(f blocks.Field) int {
	if f.MaxItems > 0 && f.MaxItems < blocks.MaxRepeaterItems {
		return f.MaxItems
	}
	return blocks.MaxRepeaterItems
}

// library is the shared block library as the page editor needs it: to name the shared blocks a page holds, and to
// offer them in the "add a shared block" lists.
type library struct {
	byID map[int64]content.Reusable
	list []content.Reusable
}

func (h *Handler) library(ctx context.Context) (library, error) {
	list, err := h.Content.Reusables(ctx)
	if err != nil {
		return library{}, err
	}
	lib := library{byID: make(map[int64]content.Reusable, len(list)), list: list}
	for _, r := range list {
		lib.byID[r.ID] = r
	}
	return lib, nil
}

// sharedView says which library block a shared block on a page shows.
type sharedView struct {
	ID              int64
	Name, TypeLabel string
	Missing         bool // the library block was deleted behind the page's back; the block shows nothing
}

// newProps is the settings a block starts with. A shared block starts pointing at the library block the form
// named; it can't be added without one, and the library block must exist.
func (h *Handler) newProps(w http.ResponseWriter, r *http.Request, bt *blocks.Type) (map[string]any, bool) {
	if bt.Name != blocks.ReusableType {
		return bt.Clean(nil), true
	}
	id, err := strconv.ParseInt(r.PostFormValue("ref"), 10, 64)
	if err == nil && id > 0 {
		if _, err = h.Content.Reusable(r.Context(), id); err == nil {
			return bt.Clean(map[string]any{"ref": float64(id)}), true
		}
		if !errors.Is(err, content.ErrNotFound) {
			serverError(w, err)
			return nil, false
		}
	}
	http.Error(w, "Unknown shared block", http.StatusBadRequest)
	return nil, false
}

// blockShare turns a block on the page into a shared one: it moves into the library and a shared block takes its place.
func (h *Handler) blockShare(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	name := content.CleanReusableName(r.PostFormValue("name"))
	if name == "" {
		http.Error(w, "Give the shared block a name", http.StatusBadRequest)
		return
	}
	_, pageID, err := h.Content.ShareBlock(r.Context(), id, name)
	switch {
	case errors.Is(err, content.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, content.ErrNotShareable), errors.Is(err, content.ErrLibraryFull):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	h.respondList(w, r, pageID, id)
}
