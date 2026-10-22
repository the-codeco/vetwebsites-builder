package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"webbuilder/internal/auth"
)

// UserError is a problem the person using the screen can fix; its text is shown to them as it is.
type UserError string

func (e UserError) Error() string { return string(e) }

// SiteRow is one website in the platform's list.
type SiteRow struct {
	ID            int64
	Name          string
	Owner         string
	Active        bool
	CreatedAt     time.Time
	LastPublished *time.Time
	Unpublished   int // changes made since the last publish
	PublishState  string
}

// AuditRow is one line of the audit log.
type AuditRow struct {
	At     time.Time
	Actor  string
	Site   string
	Action string
	Detail string
}

// PlatformService is what the platform's own screens need from the platform. The platform package implements it.
type PlatformService interface {
	Sites(ctx context.Context) ([]SiteRow, error)
	// CreateCustomer makes an account and its website, and returns the path of a one-time link where the customer
	// chooses a password.
	CreateCustomer(ctx context.Context, actor *auth.Identity, email, siteName string) (linkPath string, err error)
	// Act starts (siteID > 0) or stops (0) a platform admin working on a customer's website.
	Act(ctx context.Context, sessionToken string, actor *auth.Identity, siteID int64) error
	SetActive(ctx context.Context, actor *auth.Identity, siteID int64, active bool) error
	DeleteSite(ctx context.Context, actor *auth.Identity, siteID int64, typedName string) error
	// ResetLink makes a one-time password link for a customer.
	ResetLink(ctx context.Context, actor *auth.Identity, siteID int64) (email, linkPath string, err error)
	Audit(ctx context.Context, limit int) ([]AuditRow, error)
}

// RegisterPlatform serves the platform's own screens: login and password resets, the list of websites and customers,
// and the audit log. Platform admins only (customers are sent straight to their own website).
func (h *Handler) RegisterPlatform(mux *http.ServeMux) {
	a := h.Auth
	authed := func(f http.HandlerFunc) http.Handler { return a.RequireLogin(a.CSRF(f)) }
	admin := func(f http.HandlerFunc) http.Handler { return a.RequireAdmin(a.CSRF(f)) }

	h.registerLogin(mux, authed, admin)
	h.registerStatic(mux)
	h.registerAccount(mux, authed, admin)
	mux.Handle("GET /platform/sites", admin(h.platformSites))
	mux.Handle("POST /platform/sites", admin(h.platformCreate))
	mux.Handle("POST /platform/sites/{id}/act", admin(h.platformAct))
	mux.Handle("POST /platform/sites/{id}/status", admin(h.platformStatus))
	mux.Handle("POST /platform/sites/{id}/delete", admin(h.platformDelete))
	mux.Handle("POST /platform/sites/{id}/reset", admin(h.platformReset))
	mux.Handle("GET /platform/audit", admin(h.platformAudit))
	// leaving a customer's website is allowed whenever one is open
	mux.Handle("POST /platform/exit", admin(h.platformExit))
}

type platformSitesData struct {
	Sites     []SiteRow
	NewEmail  string
	NewName   string
	Link      string // a one-time link just made, shown once
	LinkFor   string
	LinkKind  string
	PublicURL string
}

func (h *Handler) renderPlatformSites(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string, d platformSitesData) {
	id := auth.CurrentIdentity(r)
	sites, err := h.Platform.Sites(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	d.Sites = sites
	h.render(w, r, status, "platform_sites", pageData{Title: "Websites", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Data: d})
}

func (h *Handler) platformSites(w http.ResponseWriter, r *http.Request) {
	notice := map[string]string{
		"created": "The website and its account are ready.", "suspended": "Website suspended: its owner can no longer log in or publish.",
		"resumed": "Website resumed.", "deleted": "Website deleted.", "exited": "You have left the website.",
	}[r.URL.Query().Get("done")]
	h.renderPlatformSites(w, r, 200, notice, "", platformSitesData{})
}

// linkFor builds the full address of a one-time link, from the address this request came to (the person asking is a
// logged-in platform admin, who is shown the link and passes it on).
func (h *Handler) linkFor(r *http.Request, path string) string {
	if h.BaseURL != "" {
		return h.BaseURL + path
	}
	scheme := "http"
	if h.Auth.SecureCookies || h.Auth.IsHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

func (h *Handler) platformCreate(w http.ResponseWriter, r *http.Request) {
	d := platformSitesData{NewEmail: strings.TrimSpace(r.PostFormValue("email")), NewName: strings.TrimSpace(r.PostFormValue("name"))}
	path, err := h.Platform.CreateCustomer(r.Context(), auth.CurrentIdentity(r), d.NewEmail, d.NewName)
	var ue UserError
	switch {
	case errors.As(err, &ue):
		h.renderPlatformSites(w, r, 400, "", ue.Error(), d)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	d.Link, d.LinkFor, d.LinkKind = h.linkFor(r, path), d.NewEmail, "welcome"
	d.NewEmail, d.NewName = "", ""
	h.renderPlatformSites(w, r, 200, "The website and its account are ready.", "", d)
}

func siteIDFrom(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (h *Handler) platformAct(w http.ResponseWriter, r *http.Request) {
	id, ok := siteIDFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	c, err := r.Cookie(auth.SessionCookie)
	if err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	var ue UserError
	switch err := h.Platform.Act(r.Context(), c.Value, auth.CurrentIdentity(r), id); {
	case errors.As(err, &ue):
		h.renderPlatformSites(w, r, 400, "", ue.Error(), platformSitesData{})
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (h *Handler) platformExit(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(auth.SessionCookie)
	if err == nil {
		if err := h.Platform.Act(r.Context(), c.Value, auth.CurrentIdentity(r), 0); err != nil {
			serverError(w, err)
			return
		}
	}
	http.Redirect(w, r, "/platform/sites?done=exited", http.StatusSeeOther)
}

func (h *Handler) platformStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := siteIDFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	active := r.PostFormValue("active") == "1"
	var ue UserError
	switch err := h.Platform.SetActive(r.Context(), auth.CurrentIdentity(r), id, active); {
	case errors.As(err, &ue):
		h.renderPlatformSites(w, r, 400, "", ue.Error(), platformSitesData{})
		return
	case err != nil:
		serverError(w, err)
		return
	}
	done := "suspended"
	if active {
		done = "resumed"
	}
	http.Redirect(w, r, "/platform/sites?done="+done, http.StatusSeeOther)
}

func (h *Handler) platformDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := siteIDFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var ue UserError
	switch err := h.Platform.DeleteSite(r.Context(), auth.CurrentIdentity(r), id, strings.TrimSpace(r.PostFormValue("confirm"))); {
	case errors.As(err, &ue):
		h.renderPlatformSites(w, r, 400, "", ue.Error(), platformSitesData{})
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/platform/sites?done=deleted", http.StatusSeeOther)
}

func (h *Handler) platformReset(w http.ResponseWriter, r *http.Request) {
	id, ok := siteIDFrom(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	email, path, err := h.Platform.ResetLink(r.Context(), auth.CurrentIdentity(r), id)
	var ue UserError
	switch {
	case errors.As(err, &ue):
		h.renderPlatformSites(w, r, 400, "", ue.Error(), platformSitesData{})
		return
	case err != nil:
		serverError(w, err)
		return
	}
	h.renderPlatformSites(w, r, 200, "", "", platformSitesData{Link: h.linkFor(r, path), LinkFor: email, LinkKind: "reset"})
}

func (h *Handler) platformAudit(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	rows, err := h.Platform.Audit(r.Context(), 200)
	if err != nil {
		serverError(w, err)
		return
	}
	h.render(w, r, 200, "platform_audit", pageData{Title: "Audit log", User: id, CSRF: id.CSRF, Data: rows})
}
