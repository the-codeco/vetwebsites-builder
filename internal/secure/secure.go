// Package secure is the outermost HTTP middleware: security headers, request
// body limits and panic recovery.
package secure

import (
	"log"
	"net/http"
	"runtime/debug"
	"strings"
)

const (
	// MaxBodyBytes caps ordinary form posts (the biggest legitimate one is a 200 KB blog post).
	MaxBodyBytes = 2 << 20
	// UploadPath is exempt from MaxBodyBytes: it enforces its own, larger limit.
	UploadPath = "/admin/media"

	// adminCSP is strict: only our own scripts and styles, no inline anything, no
	// framing, forms and requests only to ourselves. (The admin shows the public site
	// in a same-origin iframe for live preview, hence frame-src 'self'.)
	adminCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; " +
		"connect-src 'self'; frame-src 'self'; form-action 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'"

	permissions = "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()"
	hstsValue   = "max-age=15552000" // 180 days; no includeSubDomains/preload, which are hard to undo
)

// IsAdminArea reports whether path belongs to the logged-in/account screens.
func IsAdminArea(path string) bool {
	return path == "/admin" || strings.HasPrefix(path, "/admin/") || path == "/setup" || strings.HasPrefix(path, "/invite/")
}

// Middleware wraps next. trustProxy says whether X-Forwarded-Proto can be
// believed when deciding to send HSTS.
func Middleware(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					log.Printf("panic serving %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
					http.Error(w, "Internal server error", http.StatusInternalServerError)
				}
			}()

			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Permissions-Policy", permissions)
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			if r.TLS != nil || (trustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
				h.Set("Strict-Transport-Security", hstsValue)
			}
			if IsAdminArea(r.URL.Path) {
				h.Set("Content-Security-Policy", adminCSP)
				h.Set("X-Frame-Options", "DENY")
				// invite links carry a secret in the path: never leak it in a Referer
				h.Set("Referrer-Policy", "no-referrer")
			} else {
				// public pages may be framed by the admin's live preview, nothing else.
				// Their own CSP is set by the site handlers.
				h.Set("X-Frame-Options", "SAMEORIGIN")
				h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			}

			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				if r.URL.Path != UploadPath {
					r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
