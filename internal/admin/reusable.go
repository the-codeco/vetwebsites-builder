package admin

import (
	"errors"
	"fmt"
	"net/http"

	"webbuilder/internal/auth"
	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
)

func (h *Handler) registerReusable(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/shared", authed(h.sharedList))
	mux.Handle("POST /admin/shared", authed(h.sharedCreate))
	mux.Handle("GET /admin/shared/{id}", authed(h.sharedEdit))
	mux.Handle("POST /admin/shared/{id}", authed(h.sharedSave))
	mux.Handle("POST /admin/shared/{id}/delete", authed(h.sharedDelete))
}

type sharedRow struct {
	content.Reusable
	TypeLabel string
}

type sharedListData struct {
	Rows  []sharedRow
	Types []*blocks.Type // what a new shared block can be
	Name  string
}

type sharedEditData struct {
	R           sharedRow
	Fields      []fieldView
	StyleFields []fieldView
	Uses        []content.Use
	Saved       bool
}

func shareableTypes() []*blocks.Type {
	var out []*blocks.Type
	for _, t := range blocks.Pickable() {
		if blocks.CanBeShared(t.Name) {
			out = append(out, t)
		}
	}
	return out
}

func labelOf(typ string) string {
	if bt, ok := blocks.Get(typ); ok {
		return bt.Label
	}
	return typ + " (unknown type)"
}

func (h *Handler) renderSharedList(w http.ResponseWriter, r *http.Request, status int, errMsg, name string) {
	id := auth.CurrentIdentity(r)
	list, err := h.Content.Reusables(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	d := sharedListData{Types: shareableTypes(), Name: name}
	for _, x := range list {
		d.Rows = append(d.Rows, sharedRow{x, labelOf(x.Type)})
	}
	h.render(w, r, status, "shared", pageData{Title: "Shared blocks", User: id, CSRF: id.CSRF, Error: errMsg, Data: d})
}

func (h *Handler) sharedList(w http.ResponseWriter, r *http.Request) {
	h.renderSharedList(w, r, 200, "", "")
}

func (h *Handler) sharedCreate(w http.ResponseWriter, r *http.Request) {
	name := content.CleanReusableName(r.PostFormValue("name"))
	bt, ok := blocks.Get(r.PostFormValue("type"))
	switch {
	case name == "":
		h.renderSharedList(w, r, 400, "Give the shared block a name.", name)
		return
	case !ok || bt.Internal || !blocks.CanBeShared(bt.Name):
		h.renderSharedList(w, r, 400, "Choose what kind of block to share.", name)
		return
	}
	id, err := h.Content.CreateReusable(r.Context(), name, bt.Name, bt.Clean(nil), blocks.CleanStyleMap(nil))
	switch {
	case errors.Is(err, content.ErrLibraryFull):
		h.renderSharedList(w, r, 400, fmt.Sprintf("The library holds at most %d shared blocks. Delete one you no longer use first.", content.MaxReusable), name)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/shared/%d", id), http.StatusSeeOther)
}

func (h *Handler) renderSharedEdit(w http.ResponseWriter, r *http.Request, status int, rb content.Reusable, errMsg string, saved bool) {
	id := auth.CurrentIdentity(r)
	uses, err := h.Content.ReusableUses(r.Context(), rb.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	d := sharedEditData{R: sharedRow{rb, labelOf(rb.Type)}, Uses: uses, Saved: saved}
	if bt, ok := blocks.Get(rb.Type); ok {
		d.Fields = fieldViews("p.", bt.Fields, bt.Clean(rb.Props))
		d.StyleFields = fieldViews("s.", blocks.StyleFields(), blocks.CleanStyleMap(rb.Style))
	}
	h.render(w, r, status, "shared_edit", pageData{Title: rb.Name, User: id, CSRF: id.CSRF, Error: errMsg, Data: d})
}

func (h *Handler) sharedByPath(w http.ResponseWriter, r *http.Request) (content.Reusable, bool) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return content.Reusable{}, false
	}
	rb, err := h.Content.Reusable(r.Context(), id)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return rb, false
	}
	if err != nil {
		serverError(w, err)
		return rb, false
	}
	return rb, true
}

func (h *Handler) sharedEdit(w http.ResponseWriter, r *http.Request) {
	if rb, ok := h.sharedByPath(w, r); ok {
		h.renderSharedEdit(w, r, 200, rb, "", r.URL.Query().Get("saved") == "1")
	}
}

func (h *Handler) sharedSave(w http.ResponseWriter, r *http.Request) {
	rb, ok := h.sharedByPath(w, r)
	if !ok {
		return
	}
	bt, known := blocks.Get(rb.Type)
	if !known {
		http.Error(w, "Unknown block type", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	name := content.CleanReusableName(r.PostFormValue("name"))
	if name == "" {
		h.renderSharedEdit(w, r, 400, rb, "Give the shared block a name.", false)
		return
	}
	props := bt.Clean(formValues(bt.Fields, "p.", r.PostForm))
	style := blocks.CleanStyleMap(formValues(blocks.StyleFields(), "s.", r.PostForm))
	if err := h.Content.UpdateReusable(r.Context(), rb.ID, name, props, style); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/shared/%d?saved=1", rb.ID), http.StatusSeeOther)
}

func (h *Handler) sharedDelete(w http.ResponseWriter, r *http.Request) {
	rb, ok := h.sharedByPath(w, r)
	if !ok {
		return
	}
	err := h.Content.DeleteReusable(r.Context(), rb.ID)
	switch {
	case errors.Is(err, content.ErrInUse):
		h.renderSharedEdit(w, r, 409, rb, "It is still used on a page. Remove it from those pages first.", false)
		return
	case errors.Is(err, content.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/shared", http.StatusSeeOther)
}
