package app_test

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"webbuilder/internal/config"
)

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	anon := e.client()

	// admin-area pages (setup/login are reachable before any account exists)
	for _, path := range []string{"/setup", "/admin/login"} {
		r := anon.get(path)
		if r.Status != 200 && r.Status != 303 {
			t.Fatalf("%s: %d", path, r.Status)
		}
		h := r.Header
		csp := h.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "unsafe") {
			t.Errorf("%s CSP: %q", path, csp)
		}
		if h.Get("X-Frame-Options") != "DENY" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s headers: %v", path, h)
		}
		if r.Status == 200 && h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s must not be cached: %q", path, h.Get("Cache-Control"))
		}
	}

	// public pages: their own CSP, frameable only by the admin's same-origin preview
	pub := anon.get("/")
	csp := pub.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'self'") || !strings.Contains(csp, "object-src 'none'") {
		t.Errorf("public CSP: %q", csp)
	}
	if strings.Contains(csp, "script-src") && strings.Contains(csp, "unsafe-inline'; ") && strings.Contains(csp, "script-src 'unsafe") {
		t.Errorf("public pages must not allow inline scripts: %q", csp)
	}
	if pub.Header.Get("X-Frame-Options") != "SAMEORIGIN" || pub.Header.Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
		t.Errorf("public framing/referrer: %v", pub.Header)
	}
	for _, h := range []string{"Permissions-Policy", "Cross-Origin-Opener-Policy", "X-Content-Type-Options"} {
		if pub.Header.Get(h) == "" {
			t.Errorf("public page lacks %s", h)
		}
	}
	if pub.Header.Get("Strict-Transport-Security") != "" {
		t.Error("plain HTTP must not send HSTS")
	}

	// static assets: served, never listed
	if r := anon.get("/static/admin.css"); r.Status != 200 || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("static css: %d", r.Status)
	}
	for _, p := range []string{"/static/", "/static", "/static/nope.js"} {
		if r := anon.get(p); r.Status == 200 {
			t.Errorf("GET %s: 200 (directory listing?)", p)
		}
	}

	// errors don't leak details
	for _, p := range []string{"/admin/pages/abc", "/admin/pages/99999999999999999999", "/blog/%00", "/%ff%fe", "/admin/blocks/-1"} {
		r := anon.get(p)
		if r.Status == 500 || strings.Contains(r.Body, "goroutine") || strings.Contains(r.Body, "pgx") || strings.Contains(r.Body, ".go:") {
			t.Errorf("GET %s: %d %s", p, r.Status, firstLine(r.Body))
		}
	}
}

func TestTrustedProxyHTTPS(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.TrustProxy = true })
	e.newAdmin("admin@example.com")
	anon := e.client()

	r := anon.get("/", "X-Forwarded-Proto", "https")
	if got := r.Header.Get("Strict-Transport-Security"); !strings.HasPrefix(got, "max-age=") || strings.Contains(got, "includeSubDomains") || strings.Contains(got, "preload") {
		t.Errorf("HSTS behind a trusted proxy: %q", got)
	}
	if anon.get("/", "X-Forwarded-Proto", "http").Header.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS over plain HTTP")
	}

	// cookies are Secure exactly when the visitor used HTTPS
	login := func(proto string) string {
		c := e.client()
		tok := c.formToken("/admin/login")
		r := c.post("/admin/login", url.Values{"csrf": {tok}, "email": {"admin@example.com"}, "password": {testPassword}}, "X-Forwarded-Proto", proto)
		if r.Status != 303 {
			t.Fatalf("login via %s: %d", proto, r.Status)
		}
		return strings.Join(r.Header.Values("Set-Cookie"), "\n")
	}
	if cookie := login("https"); !strings.Contains(cookie, "Secure") || !strings.Contains(cookie, "HttpOnly") {
		t.Errorf("https login cookie: %s", cookie)
	}
	if cookie := login("http"); strings.Contains(cookie, "Secure") {
		t.Errorf("a plain-HTTP login must not set Secure cookies: %s", cookie)
	}
}

func TestProxyHeadersIgnoredWhenNotTrusted(t *testing.T) {
	e := newEnv(t) // TrustProxy is off
	e.newAdmin("admin@example.com")
	c := e.client()
	if c.get("/", "X-Forwarded-Proto", "https").Header.Get("Strict-Transport-Security") != "" {
		t.Error("a client-supplied X-Forwarded-Proto must not trigger HSTS")
	}
	tok := c.formToken("/admin/login")
	r := c.post("/admin/login", url.Values{"csrf": {tok}, "email": {"admin@example.com"}, "password": {testPassword}}, "X-Forwarded-Proto", "https")
	if strings.Contains(strings.Join(r.Header.Values("Set-Cookie"), ""), "Secure") {
		t.Error("an untrusted X-Forwarded-Proto must not change cookie flags")
	}
	// spoofed forwarding headers can't dodge the login rate limit either
	for i := 0; i < 8; i++ {
		c.post("/admin/login", url.Values{"csrf": {tok}, "email": {"admin@example.com"}, "password": {"wrong-wrong-wrong"}}, "X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i))
	}
	if r := c.post("/admin/login", url.Values{"csrf": {tok}, "email": {"admin@example.com"}, "password": {testPassword}}, "X-Forwarded-For", "10.9.9.9"); r.Status != 429 {
		t.Errorf("rotating X-Forwarded-For must not reset the rate limit: %d", r.Status)
	}
}

func TestOversizeFormIsRejected(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	before := e.sql(`SELECT count(*) FROM pages`)
	r := admin.post("/admin/pages", url.Values{"title": {"Big"}, "slug": {strings.Repeat("a", 3<<20)}})
	if r.Status == 303 || r.Status == 200 {
		t.Errorf("a 3 MB form was accepted: %d", r.Status)
	}
	if e.sql(`SELECT count(*) FROM pages`) != before {
		t.Error("an oversize request still changed data")
	}
}

var (
	reInlineScript = regexp.MustCompile(`(?i)<script(\s[^>]*)?>`)
	reScriptSrc    = regexp.MustCompile(`(?i)\ssrc\s*=`)
	reHandler      = regexp.MustCompile(`(?i)<[a-z][^>]*\son[a-z]+\s*=`)
	reStyleAttr    = regexp.MustCompile(`(?i)<[a-z][^>]*\sstyle\s*=`)
	reStyleTag     = regexp.MustCompile(`(?i)<style[\s>]`)
	reJSURL        = regexp.MustCompile(`(?i)(href|src|action)\s*=\s*["']?\s*javascript:`)
)

// TestAdminPagesWorkUnderStrictCSP guards the strict admin Content-Security-Policy:
// no page may contain inline scripts, event-handler attributes or style attributes,
// because the browser would silently refuse them and the feature would break.
func TestAdminPagesWorkUnderStrictCSP(t *testing.T) {
	e := newEnv(t)
	anon := e.client()
	pages := map[string]response{"/setup": anon.get("/setup"), "/admin/login": {}}
	delete(pages, "/admin/login") // redirects to /setup until an account exists

	admin := e.newAdmin("admin@example.com")

	// content that makes every admin screen render its richest form
	pageID := idFromLocation(t, admin.post("/admin/pages", url.Values{"title": {"Everything"}}).Location())
	for _, typ := range []string{"hero", "richtext", "image", "gallery", "columns", "button", "postlist", "spacer"} {
		admin.post("/admin/pages/"+pageID+"/blocks", url.Values{"type": {typ}})
	}
	catID := ""
	admin.post("/admin/categories", url.Values{"name": {"News"}})
	catID = e.sql(`SELECT id FROM categories`)
	postID := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {"A Post"}}).Location())
	publish(t, admin, postID, "A Post", "a-post", "Hello", catID)
	up := decodeUpload(t, admin.upload(upFile{"a.jpg", "image/jpeg", jpegImage(800, 400)}).Body)
	if len(up.Uploaded) != 1 {
		t.Fatalf("upload: %v", up)
	}
	e.invite(admin, "pending@example.com", "editor")
	invitePath := e.invite(admin, "other@example.com", "editor")

	paths := []string{
		"/admin/", "/admin/pages", "/admin/pages/" + pageID, "/admin/posts", "/admin/posts/" + postID, "/admin/categories",
		"/admin/media", "/admin/media/picker", "/admin/menus", "/admin/appearance", "/admin/domain", "/admin/users", "/admin/account",
	}
	for _, p := range paths {
		pages[p] = admin.get(p)
	}
	pages[invitePath] = e.client().get(invitePath)

	// a failed check result and an error banner exercise more templates
	pages["domain+error"] = admin.post("/admin/domain", url.Values{"domain": {"bad domain"}})
	pages["appearance+error"] = admin.post("/admin/appearance/tokens", url.Values{"t.color-primary": {"red"}})

	for name, r := range pages {
		if r.Status != 200 && r.Status != 400 {
			t.Errorf("%s: status %d", name, r.Status)
			continue
		}
		body := r.Body
		for _, m := range reInlineScript.FindAllString(body, -1) {
			if !reScriptSrc.MatchString(m) {
				t.Errorf("%s: inline <script> would be blocked by the CSP: %s", name, m)
			}
		}
		if m := reHandler.FindString(body); m != "" {
			t.Errorf("%s: inline event handler would be blocked by the CSP: %.80s", name, m)
		}
		if m := reStyleAttr.FindString(body); m != "" {
			t.Errorf("%s: inline style attribute would be blocked by the CSP: %.80s", name, m)
		}
		if reStyleTag.MatchString(body) {
			t.Errorf("%s: inline <style> would be blocked by the CSP", name)
		}
		if reJSURL.MatchString(body) {
			t.Errorf("%s: javascript: URL", name)
		}
		isFragment := name == "/admin/media/picker" // an htmx fragment, not a whole page
		if !isFragment && !strings.Contains(body, `src="/static/admin.js"`) {
			t.Errorf("%s: admin script missing", name)
		}
		if r.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: no CSP header", name)
		}
	}
}
