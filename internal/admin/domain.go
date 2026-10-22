package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"webbuilder/internal/auth"
	"webbuilder/internal/domain"
)

// The domain decides where certificates are issued and which address the site
// is canonical at, so it is admin-only.
func (h *Handler) registerDomain(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/domain", admin(h.domainPage))
	mux.Handle("POST /admin/domain", admin(h.domainSave))
	mux.Handle("POST /admin/domain/remove", admin(h.domainRemove))
	mux.Handle("POST /admin/domain/check", admin(h.domainCheck))
}

type domainData struct {
	Current string // saved domain, "" if none
	Input   string // what to show in the form field
	WWW     bool   // www.<domain> is also served (and redirects)
	Report  *domain.Report
}

func (h *Handler) renderDomain(w http.ResponseWriter, r *http.Request, status int, notice, errMsg, input string, rep *domain.Report) {
	current, err := h.Domain.Current(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	if input == "" {
		input = current
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "domain", pageData{
		Title: "Domain", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg,
		Data: domainData{Current: current, Input: input, WWW: current != "" && !strings.HasPrefix(current, "www."), Report: rep},
	})
}

func (h *Handler) domainPage(w http.ResponseWriter, r *http.Request) {
	notice := map[string]string{"saved": "Domain saved.", "removed": "Domain removed."}[r.URL.Query().Get("done")]
	h.renderDomain(w, r, 200, notice, "", "", nil)
}

func (h *Handler) domainSave(w http.ResponseWriter, r *http.Request) {
	raw := r.PostFormValue("domain")
	d, err := domain.Normalize(raw)
	if err != nil {
		h.renderDomain(w, r, 400, "", err.Error(), raw, nil)
		return
	}
	if err := h.Domain.Set(r.Context(), d); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/domain?done=saved", http.StatusSeeOther)
}

func (h *Handler) domainRemove(w http.ResponseWriter, r *http.Request) {
	if err := h.Domain.Set(r.Context(), ""); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/domain?done=removed", http.StatusSeeOther)
}

// domainCheck runs the connection check. It makes outbound requests, so only
// one runs at a time.
func (h *Handler) domainCheck(w http.ResponseWriter, r *http.Request) {
	current, err := h.Domain.Current(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	if current == "" {
		h.renderDomain(w, r, 400, "", "Save a domain first, then check it.", "", nil)
		return
	}
	if !h.checkMu.TryLock() {
		h.renderDomain(w, r, http.StatusTooManyRequests, "", "A check is already running. Wait a moment and try again.", "", nil)
		return
	}
	defer h.checkMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	rep := h.Checker.Run(ctx, current)
	h.renderDomain(w, r, 200, "", "", "", &rep)
}
