package app_test

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const (
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	phoneUA   = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1"
)

// hitCount flushes pending page views and counts the rows matching where.
func (e *testEnv) hitCount(where string, args ...any) string {
	e.t.Helper()
	if err := e.app.FlushAnalytics(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	return e.sql(`SELECT count(*) FROM hits WHERE `+where, args...)
}

func TestAnalyticsCountsOnlyRealVisits(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	other := e.client()

	// real visits: one person twice on the home page (from a search engine), another on About from a phone
	anon.get("/", "User-Agent", browserUA, "Referer", "https://www.Google.com/search?q=secret+words")
	anon.get("/", "User-Agent", browserUA)
	other.get("/about", "User-Agent", phoneUA, "Referer", e.srv.URL+"/") // from this very site

	if got := e.hitCount(`true`); got != "3" {
		t.Fatalf("expected 3 recorded views, got %s", got)
	}
	if got := e.hitCount(`path = '/' AND referrer = 'google.com'`); got != "1" {
		t.Errorf("referrer should be stored as a bare host, got %s rows", got)
	}
	if got := e.hitCount(`path = '/about' AND referrer = '' AND device = 'mobile' AND browser = 'Safari'`); got != "1" {
		t.Errorf("a visit from this site is direct, and device/browser are classified: %s rows", got)
	}
	if got := e.hitCount(`path = '/' AND visitor <> ''`); got != "2" {
		t.Errorf("visitor ids missing: %s", got)
	}
	if got := e.sql(`SELECT count(DISTINCT visitor) FROM hits WHERE path = '/'`); got != "1" {
		t.Errorf("the same browser twice on the same day is one visitor, got %s", got)
	}
	// nothing identifying is kept
	if got := e.sql(`SELECT count(*) FROM hits WHERE visitor LIKE '%127.0.0.1%' OR referrer LIKE '%secret%' OR path LIKE '%?%'`); got != "0" {
		t.Errorf("IP address, search terms or query strings were stored: %s rows", got)
	}

	before := e.hitCount(`true`)
	uncounted := map[string]func(){
		"no user agent (a script)":   func() { anon.get("/", "User-Agent", "") },
		"a bot":                      func() { anon.get("/about", "User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1)") },
		"Do Not Track":               func() { anon.get("/about", "User-Agent", browserUA, "DNT", "1") },
		"Global Privacy Control":     func() { anon.get("/about", "User-Agent", browserUA, "Sec-GPC", "1") },
		"a missing page":             func() { anon.get("/nope", "User-Agent", browserUA) },
		"the stylesheet":             func() { anon.get("/site.css", "User-Agent", browserUA) },
		"the feed":                   func() { anon.get("/rss.xml", "User-Agent", browserUA) },
		"the sitemap":                func() { anon.get("/sitemap.xml", "User-Agent", browserUA) },
		"admin screens (logged out)": func() { anon.get("/admin/login", "User-Agent", browserUA) },
		"a logged-in user":           func() { admin.get("/about", "User-Agent", browserUA) },
		"a theme preview":            func() { admin.get("/about?theme=journal", "User-Agent", browserUA) },
		"a logged-in admin screen":   func() { admin.get("/admin/pages", "User-Agent", browserUA) },
	}
	for name, visit := range uncounted {
		visit()
		if got := e.hitCount(`true`); got != before {
			t.Errorf("%s was counted (%s rows, expected %s)", name, got, before)
		}
	}

	// drafts are not counted even when someone sees them
	id := newPage(t, admin, "Secret Draft")
	savePage(t, admin, id, url.Values{"title": {"Secret Draft"}, "slug": {"secret-draft"}, "status": {"draft"}})
	anon.get("/secret-draft", "User-Agent", browserUA) // 404 for the public
	if got := e.hitCount(`path = '/secret-draft'`); got != "0" {
		t.Errorf("draft counted: %s", got)
	}

	// posts and the blog count too
	r := admin.post("/admin/posts", url.Values{"title": {"Hello"}})
	pid := idFromLocation(t, r.Location())
	admin.post("/admin/posts/"+pid, url.Values{"title": {"Hello"}, "slug": {"hello"}, "status": {"published"}, "body": {"Hi"}})
	anon.get("/blog/hello", "User-Agent", browserUA)
	anon.get("/blog?page=1", "User-Agent", browserUA)
	if got := e.hitCount(`path IN ('/blog/hello', '/blog')`); got != "2" {
		t.Errorf("blog views: %s", got)
	}
}

func TestAnalyticsScreen(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	if r := e.client().get("/admin/analytics"); r.Status == 200 {
		t.Error("analytics are open to visitors")
	}

	// an empty site says so rather than drawing nothing
	if body := admin.get("/admin/analytics").Body; !strings.Contains(body, "No visits recorded") {
		t.Error("empty state missing")
	}

	for i := 0; i < 3; i++ {
		anon.get("/about", "User-Agent", browserUA, "Referer", "https://news.example.org/story")
	}
	e.client().get("/", "User-Agent", phoneUA)
	e.hitCount(`true`) // flush

	body := admin.get("/admin/analytics").Body
	for _, want := range []string{
		`<span class="stat-num">4</span><span class="stat-label">Page views</span>`,
		`<span class="stat-num">2</span><span class="stat-label">Visitors</span>`,
		`<span class="top-name">/about</span>`, `<span class="top-name">news.example.org</span>`,
		`<span class="top-name">mobile</span>`, `<span class="top-name">Chrome</span>`, `class="chart"`, "30 days", `aria-current="page"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("screen lacks %s", want)
		}
	}
	if strings.Count(body, `<rect class="bar-views"`) != 30 {
		t.Errorf("a 30-day chart needs 30 bars, got %d", strings.Count(body, `<rect class="bar-views"`))
	}
	if !strings.Contains(admin.get("/admin/analytics?days=7").Body, `<rect class="bar-views"`) || strings.Count(admin.get("/admin/analytics?days=7").Body, `<rect class="bar-views"`) != 7 {
		t.Error("7-day range")
	}
	if got := strings.Count(admin.get("/admin/analytics?days=9999").Body, `<rect class="bar-views"`); got != 30 {
		t.Errorf("an unknown range must fall back to 30 days, got %d bars", got)
	}
	if inline := regexp.MustCompile(`(?i)sstyles*=|<style|son[a-z]+s*=`).FindString(body); inline != "" {
		t.Errorf("inline style in the analytics screen (blocked by the CSP): %s", inline)
	}

	// editors may read, but not change what is collected
	if r := editor.get("/admin/analytics"); r.Status != 200 || strings.Contains(r.Body, "Delete all analytics data") {
		t.Errorf("editor view: %d", r.Status)
	}
	if r := editor.post("/admin/analytics/settings", url.Values{}); r.Status != 403 {
		t.Errorf("editor changed settings: %d", r.Status)
	}
	if r := editor.post("/admin/analytics/delete", url.Values{}); r.Status != 403 {
		t.Errorf("editor deleted data: %d", r.Status)
	}
	if got := e.sql(`SELECT count(*) FROM hits`); got != "4" {
		t.Errorf("an editor's request changed the data: %s rows", got)
	}

	// turning it off stops counting but keeps history
	if r := admin.post("/admin/analytics/settings", url.Values{}); r.Status != 303 {
		t.Fatalf("disable: %d", r.Status)
	}
	anon.get("/about", "User-Agent", browserUA)
	if got := e.hitCount(`true`); got != "4" {
		t.Errorf("counted while disabled: %s", got)
	}
	if !strings.Contains(admin.get("/admin/analytics").Body, "Analytics is turned off") {
		t.Error("the screen should say counting is off")
	}
	admin.post("/admin/analytics/settings", url.Values{"enabled": {"on"}})
	anon.get("/about", "User-Agent", browserUA)
	if got := e.hitCount(`true`); got != "5" {
		t.Errorf("not counting after re-enabling: %s", got)
	}

	// deleting wipes everything, including views not written yet
	anon.get("/about", "User-Agent", browserUA)
	if r := admin.post("/admin/analytics/delete", url.Values{}); r.Status != 303 {
		t.Fatalf("delete: %d", r.Status)
	}
	if got := e.hitCount(`true`); got != "0" {
		t.Errorf("data left after deletion: %s", got)
	}
}

func TestAnalyticsRetention(t *testing.T) {
	e := newEnv(t)
	e.newAdmin("admin@example.com")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO hits (day, path, visitor, device, browser) VALUES
		(current_date - 120, '/old', 'a', 'desktop', 'Chrome'),
		(current_date - 89,  '/recent', 'b', 'desktop', 'Chrome')`); err != nil {
		t.Fatal(err)
	}
	e.app.Housekeeping(ctx)
	if got := e.sql(`SELECT string_agg(path, ',') FROM hits`); got != "/recent" {
		t.Errorf("retention should drop only rows older than 90 days, left: %s", got)
	}
}

func TestAnalyticsNotInExistingSecretsOrHeaders(t *testing.T) {
	e := newEnv(t)
	anon := e.client()
	r := anon.get("/", "User-Agent", browserUA)
	for _, h := range []string{"Set-Cookie"} {
		if r.Header.Get(h) != "" {
			t.Errorf("a public page set %s; analytics must be cookieless", h)
		}
	}
	if strings.Contains(r.Body, "analytics") {
		t.Error("the public page should carry no analytics script or reference")
	}
	if got := e.sql(`SELECT length(analytics_secret) >= 64 FROM site_settings`); got != "true" {
		t.Errorf("the visitor-hash secret should be long and random: %s", got)
	}
}
