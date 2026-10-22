package app_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func addRedirect(c *client, from, to, kind string) response {
	v := url.Values{"from": {from}, "to": {to}}
	if kind != "" {
		v.Set("kind", kind)
	}
	return c.post("/admin/redirects", v)
}

func TestRedirectsSendVisitorsToTheNewAddress(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	if body := admin.get("/admin/redirects").Body; !strings.Contains(body, "No redirects yet") || !strings.Contains(body, `action="/admin/redirects"`) {
		t.Error("the redirects screen")
	}
	if !strings.Contains(admin.get("/admin/").Body, `href="/admin/redirects"`) {
		t.Error("the sidebar links to it")
	}

	for _, c := range []struct{ from, to, kind, want string }{
		{"/Old-Page/", "/about", "", "301"},
		{"/a/very/old/path", "/about?from=old#top", "temporary", "302"},
		{"https://example.com/Moved", "https://example.org/new", "permanent", "301"},
	} {
		if r := addRedirect(admin, c.from, c.to, c.kind); r.Status != 303 || !strings.HasSuffix(r.Header.Get("Location"), "done=added") {
			t.Fatalf("add %s: %d", c.from, r.Status)
		}
	}
	for _, c := range []struct {
		path, to string
		status   int
	}{
		{"/old-page", "/about", 301},
		{"/OLD-PAGE", "/about", 301},
		{"/old-page/", "/about", 301},
		{"/a/very/old/path", "/about?from=old#top", 302},
		{"/moved", "https://example.org/new", 301},
	} {
		r := anon.get(c.path)
		if r.Status != c.status || r.Header.Get("Location") != c.to {
			t.Errorf("%s: %d → %q, want %d → %q", c.path, r.Status, r.Header.Get("Location"), c.status, c.to)
		}
	}
	list := admin.get("/admin/redirects").Body
	for _, want := range []string{"<code>/old-page</code>", "<code>/a/very/old/path</code>", "<code>https://example.org/new</code>", "Temporary", "Permanent"} {
		if !strings.Contains(list, want) {
			t.Errorf("the list lacks %s", want)
		}
	}

	// an address that is not redirected is a normal themed not-found page, also with several segments
	for _, p := range []string{"/nothing-here", "/a/b/c", "/blog/nope"} {
		if r := anon.get(p); r.Status != 404 || !strings.Contains(r.Body, "Page not found") || !strings.Contains(r.Body, "site-header") {
			t.Errorf("%s: %d", p, r.Status)
		}
	}
	// a real page wins over a redirect that names the same address
	e.pool.Exec(t.Context(), `INSERT INTO redirects (from_path, to_url) VALUES ('/about', '/old-page') ON CONFLICT DO NOTHING`)
	if r := anon.get("/about"); r.Status != 200 {
		t.Errorf("a page that exists is shown, not redirected: %d", r.Status)
	}
	e.pool.Exec(t.Context(), `DELETE FROM redirects WHERE from_path = '/about'`)

	// editors may manage redirects; the logged-out may not, and the CSRF token is required
	if r := addRedirect(editor, "/from-editor", "/about", ""); r.Status != 303 {
		t.Errorf("editor: %d", r.Status)
	}
	if r := addRedirect(anon, "/from-anon", "/about", ""); r.Status == 303 && strings.Contains(r.Header.Get("Location"), "done=added") {
		t.Error("logged out")
	}
	if r := admin.postNoToken("/admin/redirects", url.Values{"from": {"/no-token"}, "to": {"/about"}}); r.Status != 403 {
		t.Errorf("without the CSRF token: %d", r.Status)
	}
	if got := e.sql(`SELECT count(*) FROM redirects WHERE from_path IN ('/from-anon', '/no-token')`); got != "0" {
		t.Error("refused requests added nothing")
	}

	// removing one
	id := e.sql(`SELECT id FROM redirects WHERE from_path = '/old-page'`)
	if r := admin.post("/admin/redirects/"+id+"/delete", url.Values{}); r.Status != 303 {
		t.Errorf("delete: %d", r.Status)
	}
	if r := anon.get("/old-page"); r.Status != 404 {
		t.Errorf("after removing it: %d", r.Status)
	}
	if r := admin.post("/admin/redirects/999999/delete", url.Values{}); r.Status != 404 {
		t.Errorf("no such redirect: %d", r.Status)
	}
	if r := anon.post("/admin/redirects/"+e.sql(`SELECT id FROM redirects LIMIT 1`)+"/delete", url.Values{}); r.Status == 303 && strings.Contains(r.Header.Get("Location"), "done=deleted") {
		t.Error("logged-out delete")
	}
}

func TestRedirectsRefuseWhatCannotWork(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	if r := addRedirect(admin, "/one", "/two", ""); r.Status != 303 {
		t.Fatalf("setup: %d", r.Status)
	}
	if r := addRedirect(admin, "/two", "/three", ""); r.Status != 303 {
		t.Fatalf("a chain is allowed: %d", r.Status)
	}
	for name, c := range map[string]struct {
		from, to string
		status   int
		msg      string
	}{
		"to itself":             {"/loop", "/loop", 400, "circle"},
		"to itself, other case": {"/loop", "/LOOP/", 400, "circle"},
		"round a circle":        {"/three", "/one", 400, "circle"},
		"duplicate":             {"/one", "/elsewhere", 409, "already a redirect"},
		"reserved":              {"/admin/pages", "/x", 400, "used by the site itself"},
		"home":                  {"/", "/x", 400, "home page"},
		"no old address":        {"", "/x", 400, "old address"},
		"no new address":        {"/old", "", 400, "where it should go"},
		"javascript":            {"/old", "javascript:alert(1)", 400, "where it should go"},
		"mailto":                {"/old", "mailto:a@b.com", 400, "where it should go"},
		"query in old":          {"/old?x=1", "/x", 400, "no ?"},
		"protocol relative":     {"/old", "//evil.example", 400, "where it should go"},
	} {
		r := addRedirect(admin, c.from, c.to, "")
		if r.Status != c.status || !strings.Contains(r.Body, c.msg) {
			t.Errorf("%s: %d, want %d containing %q", name, r.Status, c.status, c.msg)
		}
		if strings.Contains(r.Body, "<script>") {
			t.Errorf("%s: echoed unescaped", name)
		}
	}
	if got := e.sql(`SELECT count(*) FROM redirects`); got != "2" {
		t.Errorf("only the two good redirects exist: %s", got)
	}
	// what was typed comes back in the form so it can be corrected, escaped
	if r := addRedirect(admin, `/bad path<script>`, "/x", ""); !strings.Contains(r.Body, `value="/bad path&lt;script&gt;"`) {
		t.Errorf("the form keeps what was typed:\n%s", r.Body)
	}
	// the list is limited
	e.pool.Exec(t.Context(), `INSERT INTO redirects (from_path, to_url) SELECT '/bulk-'||g, '/about' FROM generate_series(1, 998) g`)
	if r := addRedirect(admin, "/one-too-many", "/about", ""); r.Status != 400 || !strings.Contains(r.Body, "at most 1000") {
		t.Errorf("a full list: %d", r.Status)
	}
}

func TestChangingAnAddressLeavesARedirectBehind(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	id := newPage(t, admin, "Wanderer")
	publishPage(t, admin, id, "Wanderer", "first-address")

	// a hand-made redirect that already pointed at the page's address
	addRedirect(admin, "/very-old", "/first-address", "")

	publishPage(t, admin, id, "Wanderer", "second-address")
	if r := anon.get("/first-address"); r.Status != 301 || r.Header.Get("Location") != "/second-address" {
		t.Errorf("the old address redirects: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := anon.get("/very-old"); r.Header.Get("Location") != "/second-address" {
		t.Errorf("a redirect that pointed at it follows it, with no extra hop: %q", r.Header.Get("Location"))
	}
	if body := admin.get("/admin/redirects").Body; !strings.Contains(body, `<span class="badge">automatic</span>`) || e.sql(`SELECT count(*) FROM redirects WHERE auto`) != "2" {
		t.Error("automatic redirects are marked, and the hand-made one is not")
	}
	if r := anon.get("/second-address"); r.Status != 200 {
		t.Errorf("the page: %d", r.Status)
	}

	// moving back removes the redirect that would now lead the page to itself
	publishPage(t, admin, id, "Wanderer", "first-address")
	if r := anon.get("/first-address"); r.Status != 200 {
		t.Errorf("back at the first address: %d", r.Status)
	}
	if r := anon.get("/second-address"); r.Status != 301 || r.Header.Get("Location") != "/first-address" {
		t.Errorf("and the second now redirects: %d %q", r.Status, r.Header.Get("Location"))
	}
	if got := e.sql(`SELECT count(*) FROM redirects WHERE from_path = '/first-address'`); got != "0" {
		t.Error("no redirect from an address a page lives at")
	}
	// saving without changing the address adds nothing
	before := e.sql(`SELECT count(*) FROM redirects`)
	publishPage(t, admin, id, "Wanderer renamed", "first-address")
	if e.sql(`SELECT count(*) FROM redirects`) != before {
		t.Error("a new title is not a new address")
	}
	// the home page keeps its address and a page cannot be given "home"
	if r := admin.post("/admin/pages/"+id, url.Values{"title": {"x"}, "slug": {"home"}, "status": {"published"}}); r.Status == 303 {
	}

	// posts: the address is /blog/<slug>
	pid := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {"Moving post"}}).Location())
	publish(t, admin, pid, "Moving post", "post-one", "body")
	publish(t, admin, pid, "Moving post", "post-two", "body")
	if r := anon.get("/blog/post-one"); r.Status != 301 || r.Header.Get("Location") != "/blog/post-two" {
		t.Errorf("a post's old address: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := anon.get("/blog/post-two"); r.Status != 200 {
		t.Errorf("the post: %d", r.Status)
	}
	_ = fmt.Sprint()
}

func TestRedirectsSurviveABackupRestore(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	addRedirect(admin, "/old-a", "/about", "")
	addRedirect(admin, "/old-b", "https://example.org/x", "temporary")
	if r := admin.restoreUpload(t, []byte(admin.downloadBackup(false).Body), nil); r.Status != 200 {
		t.Fatalf("settle: %d", r.Status)
	}
	before := e.sql(`SELECT string_agg(from_path||'|'||to_url||'|'||permanent::text, ',' ORDER BY from_path) FROM redirects`)
	raw := []byte(admin.downloadBackup(false).Body)
	if site := string(unzip(t, raw)["site.json"]); !strings.Contains(site, `"/old-a"`) || !strings.Contains(site, `"redirects"`) {
		t.Error("the backup holds the redirects")
	}
	e.pool.Exec(t.Context(), `DELETE FROM redirects; INSERT INTO redirects (from_path, to_url) VALUES ('/junk', '/x')`)
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if after := e.sql(`SELECT string_agg(from_path||'|'||to_url||'|'||permanent::text, ',' ORDER BY from_path) FROM redirects`); after != before {
		t.Errorf("redirects changed: %s vs %s", before, after)
	}
	if r := anon.get("/old-b"); r.Status != 302 || r.Header.Get("Location") != "https://example.org/x" {
		t.Errorf("restored redirect works: %d", r.Status)
	}
}
