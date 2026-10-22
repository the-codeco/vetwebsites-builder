package auth

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

const (
	SessionCookie = "wb_session"
	preCSRFCookie = "wb_csrf"
)

type ctxKey struct{}

type Auth struct {
	Store         *Store
	SecureCookies bool
	TrustProxy    bool
	Logins        *Limiter
	// Resets limits "forgot password" requests, by address and by visitor, so the form can't be used to flood
	// someone's inbox or to find out which addresses have accounts.
	Resets *Limiter
}

func New(store *Store, secureCookies, trustProxy bool) *Auth {
	return &Auth{
		Store:         store,
		SecureCookies: secureCookies,
		TrustProxy:    trustProxy,
		Logins:        NewLimiter(8, 15*time.Minute),
		Resets:        NewLimiter(5, time.Hour),
	}
}

// NormalizeEmail lowercases and validates; returns "" if invalid.
func NormalizeEmail(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s || len(s) > 254 {
		return ""
	}
	return s
}

// ClientIP returns the caller's IP; behind a trusted proxy it uses the
// rightmost X-Forwarded-For entry (the one the proxy appended).
func (a *Auth) ClientIP(r *http.Request) string {
	if a.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IsHTTPS reports whether the visitor reached us over HTTPS: directly, or via a
// trusted reverse proxy that says so.
func (a *Auth) IsHTTPS(r *http.Request) bool {
	return r.TLS != nil || (a.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

// setCookie marks cookies Secure whenever the request was HTTPS (or always, if
// SecureCookies forces it). Deciding per request means a fresh server can still
// be set up over plain HTTP, and the same cookies become Secure once HTTPS works.
func (a *Auth) setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: a.SecureCookies || a.IsHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}

// StartSession replaces any existing session (prevents fixation) and sets the cookie.
func (a *Auth) StartSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	if c, err := r.Cookie(SessionCookie); err == nil {
		_ = a.Store.DeleteSession(r.Context(), c.Value)
	}
	token, err := a.Store.CreateSession(r.Context(), userID)
	if err != nil {
		return err
	}
	a.setCookie(w, r, SessionCookie, token, int(SessionTTL.Seconds()))
	return nil
}

func (a *Auth) EndSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		_ = a.Store.DeleteSession(r.Context(), c.Value)
	}
	a.setCookie(w, r, SessionCookie, "", -1)
}

// LoadSession attaches the identity (if any) to the request context.
func (a *Auth) LoadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
			if id, err := a.Store.LookupSession(r.Context(), c.Value); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, id))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// WithIdentity attaches an identity to a context. The platform uses it to tell a website's admin screens who is
// logged in, since the platform, not the website, does the logging in.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func CurrentIdentity(r *http.Request) *Identity {
	id, _ := r.Context().Value(ctxKey{}).(*Identity)
	return id
}

func (a *Auth) RequireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if CurrentIdentity(r) == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Auth) RequireAdmin(next http.Handler) http.Handler {
	return a.RequireLogin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if CurrentIdentity(r).Role != RoleAdmin {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func tokenMatches(r *http.Request, want string) bool {
	got := r.Header.Get("X-CSRF-Token")
	if got == "" {
		got = r.PostFormValue("csrf")
	}
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// CSRF protects state-changing requests from logged-in users with the
// per-session token (form field "csrf" or header X-CSRF-Token for htmx).
func (a *Auth) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) {
			id := CurrentIdentity(r)
			if id == nil || !tokenMatches(r, id.CSRF) {
				http.Error(w, "Invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// PreCSRF issues (or reuses) the double-submit token for forms shown before login.
func (a *Auth) PreCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(preCSRFCookie); err == nil && len(c.Value) >= 32 {
		return c.Value
	}
	token, _, err := newToken()
	if err != nil {
		return ""
	}
	a.setCookie(w, r, preCSRFCookie, token, int((2 * time.Hour).Seconds()))
	return token
}

func (a *Auth) CheckPreCSRF(r *http.Request) bool {
	c, err := r.Cookie(preCSRFCookie)
	return err == nil && len(c.Value) >= 32 && tokenMatches(r, c.Value)
}
