package admin

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"webbuilder/internal/auth"
	"webbuilder/internal/content"
)

// history wraps a handler that changes a page's blocks so a restore point is saved first. The point is the page
// as it is before the change, taken at most once per editing session (see content.RevisionGap). byBlock says the
// route names a block, not a page. A failure to save the point is logged and never stops the edit.
func (h *Handler) history(next http.HandlerFunc, byBlock bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if id, ok := pathID(r); ok {
			pageID := id
			if byBlock {
				if b, err := h.Content.Block(r.Context(), id); err == nil {
					pageID = b.PageID
				} else {
					pageID = 0
				}
			}
			if ident := auth.CurrentIdentity(r); ident != nil && pageID > 0 {
				if err := h.Content.SnapshotPage(r.Context(), pageID, ident.Email); err != nil {
					log.Printf("page history: %v", err)
				}
			}
		}
		next(w, r)
	}
}

// revisionRestore puts a page's blocks back as they were at a restore point.
func (h *Handler) revisionRestore(w http.ResponseWriter, r *http.Request) {
	pageID, ok := pathID(r)
	rev, err := strconv.ParseInt(r.PathValue("rev"), 10, 64)
	if !ok || err != nil || rev < 1 {
		http.NotFound(w, r)
		return
	}
	ident := auth.CurrentIdentity(r)
	switch err := h.Content.RestoreRevision(r.Context(), pageID, rev, ident.Email); {
	case errors.Is(err, content.ErrNoRevision):
		http.NotFound(w, r)
		return
	case errors.Is(err, content.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/pages/%d?restored=1", pageID), http.StatusSeeOther)
}
