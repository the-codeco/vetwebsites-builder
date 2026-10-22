package app_test

import (
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func cssVar(css, name string) string {
	m := regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `: ([^;]*);`).FindStringSubmatch(css)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestAppearance(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	css := func() string { return anon.get("/site.css").Body }

	if r := admin.get("/admin/appearance"); r.Status != 200 || !strings.Contains(r.Body, "Midnight") || !strings.Contains(r.Body, "Journal") {
		t.Fatalf("appearance page: %d", r.Status)
	}

	// hostile or malformed values are rejected, and nothing is saved
	bad := []url.Values{
		{"t.color-primary": {"red"}}, {"t.color-primary": {"#fff"}}, {"t.color-bg": {"#ffffff;}body{display:none"}},
		{"t.color-text": {"url(//evil.example/x)"}}, {"t.radius": {"12"}}, {"t.radius": {"expression(alert(1))"}},
		{"t.space": {"1rem;color:red"}}, {"t.content-width": {"99999px"}}, {"t.font-body": {"comic-sans"}},
		{"t.font-heading": {"sans, url(//evil.example)"}},
	}
	for _, v := range bad {
		if r := admin.post("/admin/appearance/tokens", v); r.Status != 400 {
			t.Errorf("%v accepted: %d", v, r.Status)
		}
	}
	if got := e.sql(`SELECT tokens::text FROM site_settings`); got != "{}" {
		t.Fatalf("a rejected submission saved something: %s", got)
	}

	// valid overrides: normalised, defaults not stored, blank ignored
	r := admin.post("/admin/appearance/tokens", url.Values{
		"t.color-primary": {"#E8590C"}, "t.font-body": {"serif"}, "t.radius": {"  20px "},
		"t.color-bg": {"#ffffff"}, "t.space": {""},
	})
	if r.Status != 303 {
		t.Fatalf("save tokens: %d", r.Status)
	}
	if got := e.sql(`SELECT tokens->'default'->>'color-primary' FROM site_settings`); got != "#e8590c" {
		t.Errorf("colour not normalised: %q", got)
	}
	if got := e.sql(`SELECT (tokens->'default' ? 'color-bg')::text FROM site_settings`); got != "false" {
		t.Error("a value equal to the theme default must not be stored")
	}
	c := css()
	if cssVar(c, "color-primary") != "#e8590c" || cssVar(c, "radius") != "20px" || !strings.HasPrefix(cssVar(c, "font-body"), "Georgia") || cssVar(c, "space") != "1rem" {
		t.Errorf("site.css does not reflect the settings:\n%s", c)
	}

	// each theme keeps its own settings
	if r := admin.post("/admin/appearance/theme", url.Values{"theme": {"nope"}}); r.Status != 400 {
		t.Errorf("unknown theme: %d", r.Status)
	}
	if r := admin.post("/admin/appearance/theme", url.Values{"theme": {"midnight"}}); r.Status != 303 {
		t.Fatalf("activate midnight: %d", r.Status)
	}
	c = css()
	if cssVar(c, "color-bg") != "#0f1218" || cssVar(c, "color-primary") != "#2dd4bf" || cssVar(c, "radius") != "12px" {
		t.Errorf("midnight must show its own defaults, not default's overrides:\n%s", c)
	}
	if home := anon.get("/"); !strings.Contains(home.Body, "/theme/default/theme.css") || !strings.Contains(home.Body, "/theme/midnight/theme.css") {
		t.Error("a child theme loads its parent's stylesheet first, then its own")
	}
	admin.post("/admin/appearance/theme", url.Values{"theme": {"journal"}})
	if home := anon.get("/"); !strings.Contains(home.Body, `site-header masthead`) {
		t.Error("journal overrides the layout")
	}
	admin.post("/admin/appearance/theme", url.Values{"theme": {"default"}})
	if cssVar(css(), "color-primary") != "#e8590c" {
		t.Error("switching back must restore default's own overrides")
	}

	// previewing a theme without activating it is for logged-in users only
	if r := anon.get("/?theme=midnight"); !strings.Contains(r.Body, `href="/site.css"`) || strings.Contains(r.Body, "theme=midnight") {
		t.Error("anonymous visitors must not be able to preview unpublished themes")
	}
	if r := anon.get("/site.css?theme=midnight"); cssVar(r.Body, "color-bg") != "#ffffff" {
		t.Error("anonymous ?theme= on site.css must be ignored")
	}
	pv := admin.get("/?theme=midnight")
	if !strings.Contains(pv.Body, `href="/site.css?theme=midnight"`) || pv.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("admin preview: cache=%q", pv.Header.Get("Cache-Control"))
	}
	if r := admin.get("/site.css?theme=midnight"); cssVar(r.Body, "color-bg") != "#0f1218" || r.Header.Get("Cache-Control") != "no-store" {
		t.Error("admin site.css preview")
	}
	if r := admin.get("/?theme=nope"); !strings.Contains(r.Body, `href="/site.css"`) {
		t.Error("an unknown preview theme falls back to the active one")
	}

	// custom CSS
	if r := admin.post("/admin/appearance/css", url.Values{"css": {strings.Repeat("a", 20_001)}}); r.Status != 400 {
		t.Errorf("huge css: %d", r.Status)
	}
	if r := admin.post("/admin/appearance/css", url.Values{"css": {"a\x00b"}}); r.Status != 400 {
		t.Errorf("null byte: %d", r.Status)
	}
	if r := admin.post("/admin/appearance/css", url.Values{"css": {".site-title { letter-spacing: .1em; }"}}); r.Status != 303 {
		t.Fatalf("save css: %d", r.Status)
	}
	if c := css(); !strings.HasSuffix(strings.TrimSpace(c), ".site-title { letter-spacing: .1em; }") {
		t.Errorf("custom CSS must come after the tokens:\n%s", c)
	}
	if r := anon.get("/site.css"); r.Header.Get("Content-Type") != "text/css; charset=utf-8" || r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("ETag") == "" {
		t.Errorf("site.css headers: %v", r.Header)
	}
	etag := anon.get("/site.css").Header.Get("ETag")
	if r := anon.get("/site.css", "If-None-Match", etag); r.Status != 304 {
		t.Errorf("conditional request: %d", r.Status)
	}

	// title (always escaped)
	if r := admin.post("/admin/appearance/title", url.Values{"title": {""}}); r.Status != 400 {
		t.Errorf("empty title: %d", r.Status)
	}
	admin.post("/admin/appearance/title", url.Values{"title": {"Test <Studio>"}})
	if !strings.Contains(anon.get("/").Body, "<title>Test &lt;Studio&gt;</title>") {
		t.Error("title must be escaped")
	}

	// reset
	if r := admin.post("/admin/appearance/tokens/reset", nil); r.Status != 303 {
		t.Errorf("reset: %d", r.Status)
	}
	if got := e.sql(`SELECT tokens::text FROM site_settings`); got != "{}" {
		t.Errorf("reset left %s", got)
	}
	if cssVar(css(), "color-primary") != "#3b5bdb" {
		t.Error("reset must restore the theme default")
	}

	// theme files: stylesheets are public; templates and metadata are not
	for path, want := range map[string]int{
		"/theme/default/theme.css": 200, "/theme/midnight/theme.css": 200,
		"/theme/default/theme.json": 404, "/theme/default/layout.html": 404, "/theme/default/blocks/hero.html": 404,
		"/theme/nope/theme.css": 404, "/theme/default/": 404,
	} {
		if r := anon.get(path); r.Status != want {
			t.Errorf("GET %s: %d, want %d", path, r.Status, want)
		}
	}
	if r := anon.get("/theme/default/../../go.mod"); r.Status == 200 {
		t.Error("path traversal through /theme/")
	}
}

func TestEveryThemeRendersEverything(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()

	pageID := idFromLocation(t, admin.post("/admin/pages", url.Values{"title": {"Showcase"}}).Location())
	for _, typ := range []string{"hero", "richtext", "image", "gallery", "columns", "button", "postlist", "spacer"} {
		admin.post("/admin/pages/"+pageID+"/blocks", url.Values{"type": {typ}})
	}
	admin.post("/admin/pages/"+pageID, url.Values{"title": {"Showcase"}, "slug": {"showcase"}, "status": {"published"}})
	postID := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {"A Post"}}).Location())
	publish(t, admin, postID, "A Post", "a-post", "Hello\n\nWorld")

	for _, theme := range []string{"default", "journal", "midnight"} {
		if r := admin.post("/admin/appearance/theme", url.Values{"theme": {theme}}); r.Status != 303 {
			t.Fatalf("activate %s: %d", theme, r.Status)
		}
		for path, want := range map[string]int{"/": 200, "/about": 200, "/showcase": 200, "/blog": 200, "/blog/a-post": 200, "/nope": 404, "/blog/nope": 404} {
			r := anon.get(path)
			if r.Status != want || !strings.Contains(r.Body, "<footer") {
				t.Errorf("%s %s: %d (want %d)", theme, path, r.Status, want)
			}
		}
		if n := strings.Count(anon.get("/showcase").Body, `class="block block-`); n != 8 {
			t.Errorf("%s: %d blocks rendered, want 8", theme, n)
		}
	}
}

func TestDomain(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()

	internal := func(domain string) int {
		w := httptest.NewRecorder()
		e.app.Internal.ServeHTTP(w, httptest.NewRequest("GET", "/tls-check?domain="+url.QueryEscape(domain), nil))
		return w.Code
	}

	// nothing configured: no certificates for anyone, and nothing redirects
	for _, d := range []string{"example.com", "www.example.com", "", "localhost"} {
		if internal(d) != 404 {
			t.Errorf("no domain set, %q was allowed a certificate", d)
		}
	}
	if r := anon.get("/", "Host", "example.com", "X-Forwarded-Proto", "http"); r.Status != 200 {
		t.Errorf("with no domain saved, nothing may redirect (first-time setup over plain HTTP): %d", r.Status)
	}
	if r := admin.post("/admin/domain/check", nil); r.Status != 400 {
		t.Errorf("check with no domain: %d", r.Status)
	}

	// validation
	for _, v := range []string{"localhost", "192.168.1.1", "8.8.8.8", "exa mple.com", "example.com:8080", "example.com/path", "printer.local",
		"javascript:alert(1)", "bücher.de", "-x.com", "a@b.com", "x.internal", "x.test"} {
		if r := admin.post("/admin/domain", url.Values{"domain": {v}}); r.Status != 400 {
			t.Errorf("domain %q accepted: %d", v, r.Status)
		}
	}
	if got := e.sql(`SELECT domain FROM site_settings`); got != "" {
		t.Fatalf("a rejected domain was saved: %q", got)
	}

	// saving normalises a pasted URL
	if r := admin.post("/admin/domain", url.Values{"domain": {"  HTTPS://Example.COM/ "}}); r.Status != 303 {
		t.Fatalf("save: %d", r.Status)
	}
	if got := e.sql(`SELECT domain FROM site_settings`); got != "example.com" {
		t.Errorf("stored %q", got)
	}
	if r := admin.get("/admin/domain"); !strings.Contains(r.Body, "https://example.com") || !strings.Contains(r.Body, "www.example.com redirects to it") {
		t.Error("the domain page should show the saved domain")
	}

	// the certificate gate (what Caddy asks): exactly the domain and its www
	for d, want := range map[string]int{
		"example.com": 200, "EXAMPLE.com.": 200, "www.example.com": 200,
		"evil.com": 404, "example.com.evil.com": 404, "sub.example.com": 404, "notexample.com": 404, "": 404, "www.www.example.com": 404,
	} {
		if got := internal(d); got != want {
			t.Errorf("tls-check %q: %d, want %d", d, got, want)
		}
	}
	// ...and that gate is not reachable through the public listener
	for _, p := range []string{"/tls-check?domain=example.com", "/internal/tls-check?domain=example.com"} {
		if r := anon.get(p); r.Status == 200 && r.Body == "ok" {
			t.Errorf("%s is exposed publicly", p)
		}
	}

	// canonical redirects
	if r := anon.get("/blog?page=2", "Host", "www.example.com"); r.Status != 301 || r.Location() != "https://example.com/blog?page=2" {
		t.Errorf("www: %d %q", r.Status, r.Location())
	}
	if r := anon.get("/about", "Host", "example.com", "X-Forwarded-Proto", "http"); r.Status != 301 || r.Location() != "https://example.com/about" {
		t.Errorf("http->https: %d %q", r.Status, r.Location())
	}
	if r := anon.get("/about", "Host", "example.com", "X-Forwarded-Proto", "https"); r.Status != 200 {
		t.Errorf("already https: %d", r.Status)
	}
	if r := anon.get("/", "Host", "203.0.113.7", "X-Forwarded-Proto", "http"); r.Status != 200 {
		t.Errorf("the server's own IP must keep working: %d", r.Status)
	}
	if r := anon.get("//evil.example/x", "Host", "www.example.com"); !strings.HasPrefix(r.Location(), "https://example.com/") {
		t.Errorf("open redirect: %q", r.Location())
	}
	if r := anon.get("/rss.xml"); !strings.Contains(r.Body, "<link>https://example.com/</link>") {
		t.Error("the feed should use the saved domain")
	}

	// the id endpoint the connection check relies on
	id := anon.get("/.well-known/site-id")
	if id.Status != 200 || !regexp.MustCompile(`^[0-9a-f]{24}$`).MatchString(id.Body) || id.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("site-id: %d %q", id.Status, id.Body)
	}

	// removing the domain closes the gate again
	if r := admin.post("/admin/domain/remove", nil); r.Status != 303 {
		t.Errorf("remove: %d", r.Status)
	}
	if internal("example.com") != 404 {
		t.Error("a removed domain must stop getting certificates")
	}
	if r := anon.get("/", "Host", "www.example.com"); r.Status != 200 {
		t.Errorf("redirects must stop once the domain is removed: %d", r.Status)
	}
}
