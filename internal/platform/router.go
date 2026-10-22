package platform

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"webbuilder/internal/auth"
	"webbuilder/internal/rewrite"
)

// PreviewBase is where a website's live preview is mounted.
const PreviewBase = "/preview/live"

// loadSession reads the platform session cookie and attaches the person's identity to the request.
func (p *Platform) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(auth.SessionCookie); err == nil && c.Value != "" {
			if id, err := p.store.LookupPlatformSession(r.Context(), c.Value); err == nil {
				r = r.WithContext(auth.WithIdentity(r.Context(), id))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Platform) routes() http.Handler {
	top := http.NewServeMux()
	p.adm.RegisterPlatform(top)
	top.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := p.pool.Ping(r.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	top.HandleFunc("GET /{$}", p.landing)

	// a website's editor, its media (the editor shows pictures from /media/), and its live preview
	pass := func(w http.ResponseWriter, r *http.Request, rt *Runtime) { rt.App.Handler.ServeHTTP(w, r) }
	top.Handle("POST /admin/build", p.authn.CSRF(p.site(p.handleBuild)))
	top.Handle(PreviewBuildBase+"/", p.site(p.previewBuild))
	top.Handle("GET "+PreviewBuildBase, http.RedirectHandler(PreviewBuildBase+"/", http.StatusMovedPermanently))
	top.Handle("/admin", p.site(pass))
	top.Handle("/admin/", p.site(pass))
	top.Handle("/media/", p.site(pass))
	top.Handle("GET "+PreviewBase, http.RedirectHandler(PreviewBase+"/", http.StatusMovedPermanently))
	top.Handle(PreviewBase+"/", p.site(p.preview))
	return top
}

// landing sends people where they belong: a customer to their website's editor, an admin to the list of websites.
func (p *Platform) landing(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	switch {
	case id == nil:
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
	}
}

// siteFor finds the website a person is working on: a customer's own, or the one a platform admin has opened.
func (p *Platform) siteFor(ctx context.Context, id *auth.Identity) (Site, error) {
	if id.Role == auth.RoleAdmin {
		if id.ActingSiteID == 0 {
			return Site{}, errNoSite
		}
		return p.siteByID(ctx, id.ActingSiteID)
	}
	return p.siteByOwner(ctx, id.UserID)
}

var errNoSite = errors.New("no website open")

// site wraps a handler that serves a website: it requires a login, finds the website, refuses a suspended one, loads the
// website's program, and gives it the identity of whoever is working (a customer is the admin of their own website; a
// platform admin is too, with a note of who they really are, for the banner and the log).
func (p *Platform) site(h func(http.ResponseWriter, *http.Request, *Runtime)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := auth.CurrentIdentity(r)
		if id == nil {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			} else {
				http.Error(w, "Please log in again", http.StatusUnauthorized)
			}
			return
		}
		s, err := p.siteFor(r.Context(), id)
		switch {
		case errors.Is(err, errNoSite):
			http.Redirect(w, r, "/platform/sites", http.StatusSeeOther)
			return
		case err != nil: // no such website (a customer without one, or one deleted while an admin was in it)
			if id.Role == auth.RoleAdmin {
				http.Redirect(w, r, "/platform/sites", http.StatusSeeOther)
				return
			}
			http.Error(w, "No website is set up for this account", http.StatusForbidden)
			return
		case !s.Active:
			http.Error(w, "This website is suspended. Contact the administrator.", http.StatusForbidden)
			return
		}
		rt, release, err := p.mgr.Acquire(r.Context(), s.ID, s.Schema)
		if err != nil {
			serverError(w, err)
			return
		}
		defer release()
		site := &auth.Identity{UserID: id.UserID, Email: id.Email, Role: auth.RoleAdmin, CSRF: id.CSRF, SiteName: s.Name}
		if id.Role == auth.RoleAdmin {
			site.ImpersonatedBy = id.Email
		}
		if err := rt.ensureUser(r.Context(), site.UserID, site.Email); err != nil {
			serverError(w, err)
			return
		}
		h(w, r.WithContext(auth.WithIdentity(r.Context(), site)), rt)
	})
}

// preview serves the website's public pages as they are being edited (drafts included), under /preview/live. The
// website itself knows nothing of the prefix, so it is taken off the request and put back on every address in the answer.
func (p *Platform) preview(w http.ResponseWriter, r *http.Request, rt *Runtime) {
	rest := strings.TrimPrefix(r.URL.Path, PreviewBase)
	if rest == "" {
		rest = "/"
	}
	// the editor and its scripts are not part of the preview
	for _, no := range []string{"/admin", "/static/", "/setup", "/invite/", "/reset/", "/healthz"} {
		if rest == no || strings.HasPrefix(rest, no) && (strings.HasSuffix(no, "/") || strings.HasPrefix(rest, no+"/")) {
			http.NotFound(w, r)
			return
		}
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = rest
	if r.URL.RawPath != "" {
		r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, PreviewBase)
	}
	r2.RequestURI = ""
	rewrite.Base(PreviewBase, rt.App.Handler).ServeHTTP(w, r2)
}

func serverError(w http.ResponseWriter, err error) {
	http.Error(w, "Internal server error", http.StatusInternalServerError)
	logf("platform: %v", err)
}

var logf = log.Printf
