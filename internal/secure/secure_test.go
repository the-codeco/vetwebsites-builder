package secure

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(trust bool, h http.HandlerFunc, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	Middleware(trust)(h).ServeHTTP(w, r)
	return w
}

var ok = func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }

func TestAdminAreaHeaders(t *testing.T) {
	for _, path := range []string{"/admin", "/admin/", "/admin/pages/3", "/setup", "/invite/abc"} {
		w := serve(false, ok, httptest.NewRequest("GET", path, nil))
		csp := w.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "frame-ancestors 'none'", "form-action 'self'", "base-uri 'none'", "object-src 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP missing %q: %s", path, want, csp)
			}
		}
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "*") {
			t.Errorf("%s: admin CSP must not weaken itself: %s", path, csp)
		}
		if w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: framing/referrer headers: %v", path, w.Header())
		}
	}
}

func TestPublicHeaders(t *testing.T) {
	for _, path := range []string{"/", "/about", "/blog/x", "/media/a.png", "/static/admin.css", "/adminfoo", "/administrator"} {
		w := serve(false, ok, httptest.NewRequest("GET", path, nil))
		if w.Header().Get("Content-Security-Policy") != "" {
			t.Errorf("%s: public pages set their own CSP, middleware must not: %q", path, w.Header().Get("Content-Security-Policy"))
		}
		if w.Header().Get("X-Frame-Options") != "SAMEORIGIN" || w.Header().Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
			t.Errorf("%s: %v", path, w.Header())
		}
	}
}

func TestBaselineHeadersEverywhere(t *testing.T) {
	for _, path := range []string{"/", "/admin/x", "/nope"} {
		h := serve(false, ok, httptest.NewRequest("GET", path, nil)).Header()
		for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Cross-Origin-Opener-Policy": "same-origin"} {
			if h.Get(k) != want {
				t.Errorf("%s: %s = %q", path, k, h.Get(k))
			}
		}
		if !strings.Contains(h.Get("Permissions-Policy"), "camera=()") {
			t.Errorf("%s: permissions policy %q", path, h.Get("Permissions-Policy"))
		}
	}
}

func TestHSTSOnlyOverTrustedHTTPS(t *testing.T) {
	proxied := httptest.NewRequest("GET", "/", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	direct := httptest.NewRequest("GET", "https://example.com/", nil) // r.TLS set

	if serve(true, ok, proxied).Header().Get("Strict-Transport-Security") == "" {
		t.Error("trusted proxy + https must send HSTS")
	}
	if serve(false, ok, proxied).Header().Get("Strict-Transport-Security") != "" {
		t.Error("an untrusted X-Forwarded-Proto must not trigger HSTS")
	}
	if serve(false, ok, direct).Header().Get("Strict-Transport-Security") == "" {
		t.Error("direct TLS must send HSTS")
	}
	if serve(true, ok, httptest.NewRequest("GET", "/", nil)).Header().Get("Strict-Transport-Security") != "" {
		t.Error("plain HTTP must never send HSTS")
	}
	hsts := serve(true, ok, proxied).Header().Get("Strict-Transport-Security")
	if strings.Contains(hsts, "includeSubDomains") || strings.Contains(hsts, "preload") {
		t.Errorf("HSTS must stay easy to undo: %q", hsts)
	}
}

func TestBodyLimit(t *testing.T) {
	read := func(path string, size int) error {
		var got error
		h := func(w http.ResponseWriter, r *http.Request) { _, got = io.Copy(io.Discard, r.Body) }
		serve(false, h, httptest.NewRequest("POST", path, strings.NewReader(strings.Repeat("a", size))))
		return got
	}
	if err := read("/admin/pages", MaxBodyBytes-10); err != nil {
		t.Errorf("body under the limit rejected: %v", err)
	}
	if err := read("/admin/pages", MaxBodyBytes+10); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("oversize body accepted: %v", err)
	}
	if err := read(UploadPath, MaxBodyBytes+1000); err != nil {
		t.Errorf("uploads are exempt from the form limit: %v", err)
	}
}

func TestPanicBecomes500(t *testing.T) {
	boom := func(w http.ResponseWriter, r *http.Request) { panic("boom") }
	w := serve(false, boom, httptest.NewRequest("GET", "/", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "boom") {
		t.Errorf("panic must give a generic 500 without details: %d %q", w.Code, w.Body.String())
	}
	// ErrAbortHandler keeps its meaning (used by net/http to abort a response quietly)
	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Errorf("ErrAbortHandler must propagate, got %v", rec)
		}
	}()
	serve(false, func(w http.ResponseWriter, r *http.Request) { panic(http.ErrAbortHandler) }, httptest.NewRequest("GET", "/", nil))
}
