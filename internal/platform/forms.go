package platform

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// FormsBase is where a published website's forms and search talk to the builder: /f/<the website's public token>/…
const FormsBase = "/f/"

func (p *Platform) siteByToken(ctx context.Context, token string) (Site, error) {
	return scanSite(p.pool.QueryRow(ctx, `SELECT `+siteCols+` FROM sites s LEFT JOIN users u ON u.id = s.owner_id WHERE s.public_token = $1`, token))
}

// originOf is "https://host" for an address, or "" when it has none.
func originOf(addr string) string {
	u, err := url.Parse(addr)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// remoteForms answers the published copy of a website: its contact and newsletter forms. These are the only public
// endpoints that change anything, so they are as narrow as can be: a website is found by its public token (which is
// in every published page anyway), only that website's own address may call them from a browser, and they run the
// website's own form checks (timed token, hidden trap field, hourly limits).
func (p *Platform) remoteForms(w http.ResponseWriter, r *http.Request) {
	s, err := p.siteByToken(r.Context(), r.PathValue("token"))
	if err != nil || !s.Active {
		http.NotFound(w, r)
		return
	}
	// a browser says where the page it is sending from lives; only the website's published address (and this
	// server's own, which shows built previews) may do that
	if origin := r.Header.Get("Origin"); origin != "" {
		if origin != originOf(s.PublicURL) && origin != originOf(p.cfg.BaseURL) {
			http.Error(w, "This address may not use this website's forms.", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Accept, Content-Type")
			h.Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	rt, release, err := p.mgr.Acquire(r.Context(), s.ID, s.Schema)
	if err != nil {
		serverError(w, err)
		return
	}
	defer release()

	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + strings.TrimPrefix(r.PathValue("rest"), "/")
	r2.URL.RawPath = ""
	rt.App.FormAPI().ServeHTTP(w, r2)
}
