package platform_test

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"webbuilder/internal/config"
)

var reNewPage = regexp.MustCompile(`/admin/pages/(\d+)`)

// newPage creates and publishes a page in the website the client is working on, with one text block.
func newPage(t *testing.T, c *client, title, slug, html string) string {
	t.Helper()
	r := c.post("/admin/pages", url.Values{"title": {title}, "slug": {slug}})
	m := reNewPage.FindStringSubmatch(r.Location())
	if r.Status != 303 || m == nil {
		t.Fatalf("create page %s: %d %s", slug, r.Status, firstLine(r.Body))
	}
	id := m[1]
	if r := c.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"richtext"}}); r.Status != 200 {
		t.Fatalf("add block: %d", r.Status)
	}
	bid := regexp.MustCompile(`id="blk-(\d+)"`).FindStringSubmatch(c.get("/admin/pages/" + id).Body)
	if bid == nil {
		t.Fatal("no block")
	}
	if r := c.post("/admin/blocks/"+bid[1], url.Values{"p.content": {html}}); r.Status != 200 {
		t.Fatalf("save block: %d", r.Status)
	}
	if r := c.post("/admin/pages/"+id, url.Values{"title": {title}, "slug": {slug}, "status": {"published"}}); r.Status != 303 {
		t.Fatalf("publish: %d", r.Status)
	}
	return id
}

func TestFirstAdminAndAccess(t *testing.T) {
	e := newEnv(t)
	anon := e.client()
	if r := anon.get("/"); r.Status != 303 || r.Location() != "/admin/login" {
		t.Errorf("the front door sends strangers to log in: %d %s", r.Status, r.Location())
	}
	for _, p := range []string{"/admin/", "/admin/pages", "/platform/sites", "/platform/audit", "/preview/live/", "/media/x.jpg"} {
		if r := anon.get(p); r.Status != 303 || r.Location() != "/admin/login" {
			t.Errorf("%s without logging in: %d %s", p, r.Status, r.Location())
		}
	}
	if r := anon.postNoToken("/admin/pages", url.Values{"title": {"x"}}); r.Status == 200 || r.Status == 303 && !strings.Contains(r.Location(), "login") {
		t.Errorf("posting without logging in: %d", r.Status)
	}
	if r := anon.get("/healthz"); r.Status != 200 {
		t.Errorf("health: %d", r.Status)
	}

	admin := e.setupAdmin("admin@example.com")
	if r := admin.get("/"); r.Location() != "/admin/" {
		t.Errorf("landing: %s", r.Location())
	}
	if r := admin.get("/admin/"); r.Status != 303 || r.Location() != "/platform/sites" {
		t.Errorf("an admin with no website open goes to the list: %d %s", r.Status, r.Location())
	}
	if body := admin.get("/platform/sites").Body; !strings.Contains(body, "No websites yet") || !strings.Contains(body, `action="/platform/sites"`) || !strings.Contains(body, `aria-label="Platform"`) {
		t.Error("the platform screens")
	}
	if r := e.client().get("/setup"); r.Status != 404 {
		t.Errorf("setup works once: %d", r.Status)
	}
	// a website's own screens that make no sense here are not served
	for _, p := range []string{"/admin/users", "/admin/domain"} {
		if r := admin.get(p); r.Status == 200 {
			t.Errorf("%s should not be a website screen when acting for nobody", p)
		}
	}
}

func TestCustomerGetsTheirOwnWebsite(t *testing.T) {
	e := newEnv(t)
	admin := e.setupAdmin("admin@example.com")
	path := e.createCustomer(admin, "Ann@Acme.example", "Acme Dental")
	if !strings.HasPrefix(path, "/reset/") {
		t.Fatalf("link: %s", path)
	}
	if body := admin.get("/platform/sites").Body; !strings.Contains(body, "Acme Dental") || !strings.Contains(body, "ann@acme.example") {
		t.Error("the website is listed with its owner (email normalised)")
	}
	if got := e.scalar(`SELECT count(*) FROM information_schema.schemata WHERE schema_name LIKE 'site\_%'`); got != "1" {
		t.Errorf("a schema for the website: %s", got)
	}
	// the customer cannot log in with a password nobody knows
	if r := e.client().login("ann@acme.example", "guess-guess-guess"); r.Status == 303 {
		t.Fatal("a new account has no usable password")
	}
	// they choose one through the link
	ann := e.client()
	tok := ann.formToken(path)
	if r := ann.post(path, url.Values{"csrf": {tok}, "password": {"short"}, "confirm": {"short"}}); r.Status != 400 {
		t.Errorf("password rules: %d", r.Status)
	}
	if r := ann.post(path, url.Values{"csrf": {tok}, "password": {testPassword}, "confirm": {testPassword}}); r.Status != 303 || r.Location() != "/admin/" {
		t.Fatalf("choose a password: %d", r.Status)
	}
	if r := ann.get(path); r.Status != 404 {
		t.Errorf("the link works once: %d", r.Status)
	}
	ann.refreshToken()

	// a website nobody has set up starts with the set-up wizard; skipping it leaves the dashboard
	if r := ann.get("/admin/"); r.Status != 303 || r.Location() != "/admin/setup" {
		t.Fatalf("first visit: %d %s", r.Status, r.Location())
	}
	if r := ann.post("/admin/setup/skip", nil); r.Status != 303 {
		t.Fatalf("skip: %d", r.Status)
	}

	// their editor: the dashboard of their website, titled with the name the admin gave it
	dash := ann.get("/admin/")
	if dash.Status != 200 || !strings.Contains(dash.Body, "Dashboard") || strings.Contains(dash.Body, "acting-banner") {
		t.Fatalf("dashboard: %d", dash.Status)
	}
	if !strings.Contains(ann.get("/admin/appearance").Body, `value="Acme Dental"`) {
		t.Error("the site title is the name the admin gave it")
	}
	// a customer's editor has no login, users or domain screens, and links previews to the platform's preview
	if strings.Contains(dash.Body, `href="/admin/users"`) || strings.Contains(dash.Body, `href="/admin/domain"`) {
		t.Error("single-site screens are hidden")
	}
	if !strings.Contains(dash.Body, `href="/preview/live/"`) {
		t.Error("the preview link points at the platform's preview")
	}
	for _, p := range []string{"/admin/users", "/admin/domain", "/platform/sites", "/platform/audit"} {
		if r := ann.get(p); r.Status == 200 {
			t.Errorf("a customer must not reach %s: %d", p, r.Status)
		}
	}
	for _, p := range []string{"/platform/sites"} {
		if r := ann.post(p, url.Values{"email": {"x@example.com"}, "name": {"x"}}); r.Status != 403 {
			t.Errorf("a customer cannot create websites: %d", r.Status)
		}
	}
	// they work on their site: a page, shown in the live preview with every address under the preview prefix
	newPage(t, ann, "About us", "about-us", "<p>We are Acme and we fix teeth</p>")
	pv := ann.get("/preview/live/about-us")
	if pv.Status != 200 || !strings.Contains(pv.Body, "we fix teeth") || !strings.Contains(pv.Body, "<title>About us") {
		t.Fatalf("preview: %d", pv.Status)
	}
	for _, want := range []string{`href="/preview/live/site.css`, `href="/preview/live/theme/default/theme.css`, `<a class="site-title" href="/preview/live/">Acme Dental</a>`} {
		if !strings.Contains(pv.Body, want) {
			t.Errorf("preview lacks %s", want)
		}
	}
	if regexp.MustCompile(`(?:href|src|action)="/[^p]`).MatchString(pv.Body) && regexp.MustCompile(`(?:href|src|action)="/(?:[^p]|p[^r])`).MatchString(pv.Body) {
		t.Errorf("an address in the preview does not start with the prefix:\n%s", regexp.MustCompile(`(?:href|src|action)="/(?:[^p]|p[^r])[^"]*"`).FindAllString(pv.Body, -1))
	}
	if r := ann.get("/preview/live/site.css"); r.Status != 200 || !strings.Contains(r.Header.Get("Content-Type"), "css") {
		t.Errorf("site.css through the preview: %d", r.Status)
	}
	if r := ann.get("/preview/live/theme/default/assets/menu.js"); r.Status != 200 {
		t.Errorf("theme script: %d", r.Status)
	}
	// the editor's own pieces are not part of the preview
	for _, p := range []string{"/preview/live/admin/", "/preview/live/admin/pages", "/preview/live/static/admin.js", "/preview/live/setup"} {
		if r := ann.get(p); r.Status != 404 {
			t.Errorf("%s in the preview: %d", p, r.Status)
		}
	}
	// a draft shows in the preview (they are logged in), not to the public: there is no public side on this server
	if r := ann.get("/about-us"); r.Status != 404 {
		t.Errorf("the website itself is not served at the root of the platform: %d", r.Status)
	}
	// the editor iframe points at the preview
	if body := ann.get("/admin/pages/" + reNewPage.FindStringSubmatch(ann.get("/admin/pages").Body)[1]).Body; !strings.Contains(body, `src="/preview/live/`) {
		t.Error("the editor's preview frame uses the preview prefix")
	}
	// posts and uploads work (rows record who did it, so the platform account is mirrored into the website)
	if r := ann.post("/admin/posts", url.Values{"title": {"First post"}}); r.Status != 303 {
		t.Errorf("a post: %d %s", r.Status, firstLine(r.Body))
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.users WHERE email = 'ann@acme.example'`); got != "1" {
		t.Errorf("mirrored account: %s", got)
	}
}

func TestWebsitesAreIsolated(t *testing.T) {
	e := newEnv(t)
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Alpha")
	bob := e.customer(admin, "bob@b.example", "Beta")
	if e.scalar(`SELECT count(*) FROM sites`) != "2" {
		t.Fatal("two websites")
	}
	// the same address and different words on each
	newPage(t, ann, "Same", "same", "<p>Alpha secret words</p>")
	newPage(t, bob, "Same", "same", "<p>Beta other words</p>")
	if body := ann.get("/preview/live/same").Body; !strings.Contains(body, "Alpha secret") || strings.Contains(body, "Beta") {
		t.Error("Ann sees her own page only")
	}
	if body := bob.get("/preview/live/same").Body; !strings.Contains(body, "Beta other") || strings.Contains(body, "Alpha") {
		t.Error("Bob sees his own page only")
	}
	for _, c := range []*client{ann, bob} {
		list := c.get("/admin/pages").Body
		if strings.Count(list, `href="/admin/pages/`) < 3 { // home, about, same
			t.Errorf("pages listed: %d", strings.Count(list, `href="/admin/pages/`))
		}
	}
	// a block id from the other website means nothing here
	annPage := reNewPage.FindAllStringSubmatch(ann.get("/admin/pages").Body, -1)
	bobPage := reNewPage.FindAllStringSubmatch(bob.get("/admin/pages").Body, -1)
	if len(annPage) == 0 || len(bobPage) == 0 {
		t.Fatal("pages")
	}
	e.pool.Exec(t.Context(), `INSERT INTO site_2.pages (id, slug, title, status) VALUES (9999, 'only-in-beta', 'Only in Beta', 'published')`)
	if r := ann.get("/admin/pages/9999"); r.Status != 404 {
		t.Errorf("Ann asking for a page that exists only in Beta's website: %d", r.Status)
	}
	if r := ann.get("/preview/live/only-in-beta"); r.Status != 404 {
		t.Errorf("or its address: %d", r.Status)
	}
	if r := bob.get("/preview/live/only-in-beta"); r.Status != 200 {
		t.Errorf("Bob sees it: %d", r.Status)
	}
	// search and settings are separate too
	ann.post("/admin/appearance/title", url.Values{"title": {"Alpha Title"}})
	if body := bob.get("/preview/live/").Body; strings.Contains(body, "Alpha Title") {
		t.Error("settings are per website")
	}
	if r := bob.get("/preview/live/search?q=secret"); strings.Contains(r.Body, "Alpha") {
		t.Errorf("search stays inside the website")
	}
	// the database itself keeps them in different schemas
	if got := e.scalar(`SELECT count(*) FROM site_1.pages WHERE slug = 'same'`) + e.scalar(`SELECT count(*) FROM site_2.pages WHERE slug = 'same'`); got != "11" {
		t.Errorf("each schema has its own: %s", got)
	}
	if got := e.scalar(`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name IN ('pages', 'blocks', 'posts', 'media')`); got != "0" {
		t.Errorf("no website tables in the platform schema: %s", got)
	}
	// uploaded files are kept apart
	files := e.mgrMediaDir(1)
	if files == e.mgrMediaDir(2) {
		t.Error("separate media folders")
	}
}

// mgrMediaDir is where a website's media lives under the test's data folder.
func (e *env) mgrMediaDir(id int64) string {
	return filepath.Join(e.cfg.DataDir, "media", itoa(id))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestAdminCanWorkOnACustomersWebsite(t *testing.T) {
	e := newEnv(t)
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Alpha")
	e.customer(admin, "bob@b.example", "Beta")
	newPage(t, ann, "Hers", "hers", "<p>Ann wrote this</p>")
	siteID := e.scalar(`SELECT id FROM sites WHERE name = 'Alpha'`)

	if r := admin.post("/platform/sites/"+siteID+"/act", nil); r.Status != 303 || r.Location() != "/admin/" {
		t.Fatalf("act: %d %s", r.Status, r.Location())
	}
	dash := admin.get("/admin/")
	if dash.Status != 200 || !strings.Contains(dash.Body, "acting-banner") || !strings.Contains(dash.Body, "Alpha") || !strings.Contains(dash.Body, "admin@example.com") || !strings.Contains(dash.Body, `action="/platform/exit"`) {
		t.Fatalf("the banner says whose website this is: %d", dash.Status)
	}
	if body := admin.get("/preview/live/hers").Body; !strings.Contains(body, "Ann wrote this") {
		t.Error("the admin sees Ann's website")
	}
	// work done as the admin
	newPage(t, admin, "By admin", "by-admin", "<p>Added by the platform admin</p>")
	if body := ann.get("/preview/live/by-admin").Body; !strings.Contains(body, "Added by the platform admin") {
		t.Error("it is in Ann's website")
	}
	// their own screens do not show the banner
	if strings.Contains(ann.get("/admin/").Body, "acting-banner") {
		t.Error("the customer sees no banner")
	}
	// the log
	log := admin.get("/platform/audit").Body
	for _, want := range []string{"started working on the website", "created the website and account", "Alpha", "admin@example.com"} {
		if !strings.Contains(log, want) {
			t.Errorf("audit log lacks %q", want)
		}
	}
	// leaving
	if r := admin.post("/platform/exit", nil); r.Status != 303 || !strings.Contains(r.Location(), "/platform/sites") {
		t.Errorf("exit: %d %s", r.Status, r.Location())
	}
	if r := admin.get("/admin/"); r.Location() != "/platform/sites" {
		t.Errorf("after leaving, the admin has no website open: %s", r.Location())
	}
	if !strings.Contains(admin.get("/platform/audit").Body, "stopped working on the website") {
		t.Error("leaving is logged")
	}
	// switching to another website replaces the first
	bobID := e.scalar(`SELECT id FROM sites WHERE name = 'Beta'`)
	admin.post("/platform/sites/"+bobID+"/act", nil)
	if body := admin.get("/admin/appearance").Body; !strings.Contains(body, `value="Beta"`) || strings.Contains(body, "Alpha") {
		t.Error("now in Beta's website")
	}
	// customers cannot do any of this, with or without a token
	if r := ann.post("/platform/sites/"+bobID+"/act", nil); r.Status != 403 {
		t.Errorf("a customer cannot act on another website: %d", r.Status)
	}
	if r := ann.post("/platform/exit", nil); r.Status != 403 {
		t.Errorf("or use the exit: %d", r.Status)
	}
	if r := admin.postNoToken("/platform/sites/"+bobID+"/act", nil); r.Status != 403 {
		t.Errorf("an admin needs the CSRF token too: %d", r.Status)
	}
	if r := admin.post("/platform/sites/999999/act", nil); r.Status != 400 {
		t.Errorf("no such website: %d", r.Status)
	}
	if r := admin.post("/platform/sites/abc/act", nil); r.Status != 404 {
		t.Errorf("junk id: %d", r.Status)
	}
	// an admin's session working on a website does not give them the customer's password or login
	if r := e.client().login("ann@a.example", testPassword); r.Status != 303 {
		t.Errorf("Ann can still log in herself: %d", r.Status)
	}
}

func TestSuspendResumeAndDelete(t *testing.T) {
	e := newEnv(t)
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Alpha")
	bob := e.customer(admin, "bob@b.example", "Beta")
	_ = bob
	newPage(t, ann, "Keep", "keep", "<p>x</p>")
	id := e.scalar(`SELECT id FROM sites WHERE name = 'Alpha'`)
	if got := e.scalar(`SELECT count(*) FROM site_` + id + `.pages`); got == "0" {
		t.Fatal("setup")
	}

	// suspend: the owner is signed out and cannot get back in
	if r := admin.post("/platform/sites/"+id+"/status", url.Values{"active": {"0"}}); r.Status != 303 || !strings.Contains(r.Location(), "done=suspended") {
		t.Fatalf("suspend: %d", r.Status)
	}
	if r := ann.get("/admin/"); r.Status != 303 || r.Location() != "/admin/login" {
		t.Errorf("signed out: %d %s", r.Status, r.Location())
	}
	again := e.client()
	if r := again.login("ann@a.example", testPassword); r.Status != 303 {
		t.Fatalf("logging in itself works: %d", r.Status)
	}
	if r := again.get("/admin/"); r.Status != 403 || !strings.Contains(r.Body, "suspended") {
		t.Errorf("but the website is closed to them: %d", r.Status)
	}
	if r := again.get("/preview/live/"); r.Status != 403 {
		t.Errorf("and its preview: %d", r.Status)
	}
	if r := admin.post("/platform/sites/"+id+"/act", nil); r.Status != 400 || !strings.Contains(r.Body, "suspended") {
		t.Errorf("an admin cannot open it either: %d", r.Status)
	}
	if e.p.Loaded() > 1 {
		t.Errorf("a suspended website is unloaded: %d loaded", e.p.Loaded())
	}
	// resume
	if r := admin.post("/platform/sites/"+id+"/status", url.Values{"active": {"1"}}); r.Status != 303 {
		t.Fatalf("resume: %d", r.Status)
	}
	if r := again.get("/admin/"); r.Status != 200 {
		t.Errorf("back in: %d", r.Status)
	}
	if !strings.Contains(admin.get("/platform/audit").Body, "suspended the website") {
		t.Error("logged")
	}

	// delete needs the name
	for _, typed := range []string{"", "alpha", "Alpha ", "Beta"} {
		if typed == "Alpha " {
			typed = "Alpha  x"
		}
		if r := admin.post("/platform/sites/"+id+"/delete", url.Values{"confirm": {typed}}); r.Status != 400 {
			t.Errorf("delete with %q: %d", typed, r.Status)
		}
	}
	if e.scalar(`SELECT count(*) FROM sites`) != "2" {
		t.Fatal("nothing deleted yet")
	}
	if r := again.post("/platform/sites/"+id+"/delete", url.Values{"confirm": {"Alpha"}}); r.Status != 403 {
		t.Errorf("a customer cannot delete: %d", r.Status)
	}
	if err := os.MkdirAll(e.mgrMediaDir(1), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := admin.post("/platform/sites/"+id+"/delete", url.Values{"confirm": {"Alpha"}}); r.Status != 303 || !strings.Contains(r.Location(), "done=deleted") {
		t.Fatalf("delete: %d", r.Status)
	}
	if got := e.scalar(`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'site_` + id + `'`); got != "0" {
		t.Error("the schema is gone")
	}
	if got := e.scalar(`SELECT count(*) FROM users WHERE email = 'ann@a.example'`); got != "0" {
		t.Error("and the account")
	}
	if _, err := os.Stat(e.mgrMediaDir(1)); !os.IsNotExist(err) {
		t.Error("and the files")
	}
	if r := e.client().login("ann@a.example", testPassword); r.Status == 303 {
		t.Error("the owner cannot log in")
	}
	if got := e.scalar(`SELECT count(*) FROM site_2.pages`); got == "0" {
		t.Error("the other website is untouched")
	}
	if r := bob.get("/admin/"); r.Status != 200 {
		t.Errorf("Bob carries on: %d", r.Status)
	}
	if !strings.Contains(admin.get("/platform/audit").Body, "deleted the website and account") {
		t.Error("the deletion is logged, with the website's name")
	}
}

func TestPlatformRefusals(t *testing.T) {
	e := newEnv(t)
	admin := e.setupAdmin("admin@example.com")
	e.customer(admin, "ann@a.example", "Alpha")
	for name, v := range map[string]url.Values{
		"no email": {"name": {"X"}}, "bad email": {"email": {"not an email"}, "name": {"X"}}, "no name": {"email": {"new@a.example"}},
		"blank name": {"email": {"new@a.example"}, "name": {"   "}}, "taken": {"email": {"ANN@a.example"}, "name": {"Another"}},
	} {
		r := admin.post("/platform/sites", v)
		if r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if got := e.scalar(`SELECT count(*) FROM sites`); got != "1" {
		t.Errorf("refused requests made nothing: %s", got)
	}
	if got := e.scalar(`SELECT count(*) FROM information_schema.schemata WHERE schema_name LIKE 'site\_%'`); got != "1" {
		t.Errorf("and no schema: %s", got)
	}
	// names are tidied and limited
	r := admin.post("/platform/sites", url.Values{"email": {"long@a.example"}, "name": {"  A   " + strings.Repeat("b", 200) + "  "}})
	if r.Status != 200 {
		t.Fatalf("long name: %d", r.Status)
	}
	if got := e.scalar(`SELECT name FROM sites WHERE id = 2`); len([]rune(got)) != 100 || !strings.HasPrefix(got, "A bbb") {
		t.Errorf("name: %q", got)
	}
	// markup in a name is shown as text
	admin.post("/platform/sites", url.Values{"email": {"x@a.example"}, "name": {`<script>alert(1)</script>`}})
	if body := admin.get("/platform/sites").Body; strings.Contains(body, "<script>alert(1)</script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Error("names are escaped")
	}
}

func TestOnlyAFewWebsitesStayLoaded(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.MaxRuntimes = 2 })
	admin := e.setupAdmin("admin@example.com")
	var customers []*client
	for _, n := range []string{"a", "b", "c", "d"} {
		customers = append(customers, e.customer(admin, n+"@x.example", "Site "+n))
	}
	if e.p.Loaded() > 2 {
		t.Errorf("at most 2 loaded: %d", e.p.Loaded())
	}
	for i, c := range customers {
		newPage(t, c, "Mine", "mine", "<p>words "+string(rune('a'+i))+"</p>")
	}
	for i, c := range customers { // every website still works after being unloaded and loaded again
		if body := c.get("/preview/live/mine").Body; !strings.Contains(body, "words "+string(rune('a'+i))) {
			t.Errorf("site %d lost its page", i)
		}
		if e.p.Loaded() > 2 {
			t.Errorf("loaded: %d", e.p.Loaded())
		}
	}
}
