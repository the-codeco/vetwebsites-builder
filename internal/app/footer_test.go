package app_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/content"
)

// saveFooter posts a footer as the builder does: the whole thing as JSON.
func saveFooter(c *client, structure string) response {
	return c.post("/admin/footer", url.Values{"structure": {structure}})
}

const builtFooter = `{"version":2,"bottom":"Copyright {year} {title} <b>all</b> rights",
 "style":{"background":"#102030","text":"#F0F0F0","padding":"lg"},
 "rows":[
  {"ratio":"wide-left","align":"left","columns":[
    {"widgets":[{"type":"text","heading":"Find  us","text":"<p>1 High Street <a href=\"/contact\" onclick=\"x()\">Contact</a> <a href=\"javascript:alert(1)\">bad</a></p><script>alert(2)</script>"}]},
    {"widgets":[{"type":"links","heading":"Explore","links":[{"label":"About","url":"/about"},{"label":"Blog","url":"/blog","new_tab":true},{"label":"","url":""}]}]},
    {"widgets":[{"type":"contact","heading":"Call","parts":["phone","email"]},{"type":"social","heading":"Follow"}]}
  ]},
  {"align":"center","columns":[{"widgets":[
    {"type":"logo","text":"Caring for pets since 1990"},
    {"type":"newsletter","heading":"News","text":"Monthly tips.","button":"Join","success":"Welcome aboard!"}
  ]}]}
 ]}`

func TestFooterBuilder(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	year := strconv.Itoa(time.Now().Year())

	// nothing built yet: the plain line, no rows
	home := anon.get("/").Body
	if !strings.Contains(home, `<p class="footer-bottom">© `+year+` My Site</p>`) || strings.Contains(home, "footer-row") {
		t.Errorf("default footer:\n%s", home)
	}
	page := admin.get("/admin/footer").Body
	for _, want := range []string{`href="/admin/footer" title="Footer" aria-current="page"`, `data-footer-builder`, `data-destinations=`, `id="tpl-fb-row"`, `id="tpl-fb-w-newsletter"`, `id="tpl-fb-w-contact"`, `data-part="hours"`} {
		if !strings.Contains(page, want) {
			t.Errorf("builder page lacks %s", want)
		}
	}
	if strings.Contains(admin.get("/admin/appearance").Body, `name="footer_text_1"`) {
		t.Error("the old footer form is gone from Appearance")
	}

	admin.post("/admin/details", url.Values{"phone": {"021 555 0101"}, "email": {"hello@example.com"}, "social_facebook": {"https://facebook.com/vets"}})
	if r := saveFooter(admin, builtFooter); r.Status != 303 || !strings.HasSuffix(r.Location(), "/admin/footer?saved=1") {
		t.Fatalf("save: %d\n%s", r.Status, r.Body)
	}
	for _, theme := range []string{"", "?theme=journal", "?theme=midnight"} {
		body := anon.get("/about" + theme).Body
		for _, want := range []string{
			`<footer class="site-footer footer-pad-lg has-bg has-fg" id="site-footer" style="--footer-bg:#102030;--footer-fg:#f0f0f0;">`,
			`footer-row footer-cols-3 footer-ratio-wide-left footer-align-left`, `footer-row footer-cols-1 footer-ratio-equal footer-align-center`,
			`<h2>Find us</h2>`, `href="/contact"`, `<h2>Explore</h2>`,
			`<a href="/about" aria-current="page">About</a>`, `<a href="/blog" target="_blank" rel="noopener noreferrer">Blog</a>`,
			`<h2>Call</h2>`, `021 555 0101`, `hello@example.com`, `<h2>Follow</h2>`, `href="https://facebook.com/vets"`,
			`<span class="footer-logo-title">My Site</span>`, `<p class="footer-tagline">Caring for pets since 1990</p>`,
			`<form class="newsletter-form" id="newsletter-0" method="post" action="/subscribe"`, `<input type="hidden" name="block" value="0">`, `>Join</button>`,
			`<p class="footer-bottom">Copyright ` + year + ` My Site &lt;b&gt;all&lt;/b&gt; rights</p>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("theme %q: footer lacks %s", theme, want)
			}
		}
		for _, not := range []string{"onclick", "javascript:", "<script>alert", "menu-footer"} {
			if strings.Contains(body, not) {
				t.Errorf("theme %q: footer contains %q", theme, not)
			}
		}
	}
	// it is on every page, and the newsletter widget signs people up like a signup block
	for _, p := range []string{"/", "/blog", "/search?q=x", "/nope"} {
		if body := anon.get(p).Body; !strings.Contains(body, `<h2>Find us</h2>`) {
			t.Errorf("%s has no footer content", p)
		}
	}
	// the builder shows what was saved
	page = admin.get("/admin/footer").Body
	for _, want := range []string{`value="Find us"`, `value="Caring for pets since 1990"`, `value="#102030"`, `Copyright {year} {title}`, `data-part="phone" checked`} {
		if !strings.Contains(page, want) {
			t.Errorf("builder lacks saved %s", want)
		}
	}
	if !strings.Contains(page, `data-part="hours">`) {
		t.Error("details left out of a contact widget are unticked")
	}
	// clearing everything goes back to the default
	if r := saveFooter(admin, `{"version":2,"rows":[],"style":{}}`); r.Status != 303 {
		t.Fatalf("clear: %d", r.Status)
	}
	if body := anon.get("/").Body; strings.Contains(body, "footer-row") || !strings.Contains(body, `<p class="footer-bottom">© `+year+` My Site</p>`) {
		t.Error("a cleared footer is the default again")
	}
}

func TestFooterBuilderLimitsAndAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	initial := e.sql(`SELECT footer::text FROM site_settings`)

	five := strings.TrimSuffix(strings.Repeat(`{"widgets":[]},`, 5), ",")
	for name, structure := range map[string]string{
		"not json":       `nope`,
		"unknown field":  `{"rows":[],"evil":1}`,
		"five columns":   `{"rows":[{"columns":[` + five + `]}]}`,
		"unknown widget": `{"rows":[{"columns":[{"widgets":[{"type":"iframe"}]}]}]}`,
		"script link":    `{"rows":[{"columns":[{"widgets":[{"type":"links","links":[{"label":"x","url":"javascript:alert(1)"}]}]}]}]}`,
		"bad colour":     `{"rows":[],"style":{"background":"red;}body{display:none"}}`,
		"long bottom":    `{"rows":[],"bottom":"` + strings.Repeat("b", 301) + `"}`,
	} {
		if r := saveFooter(admin, structure); r.Status != 400 {
			t.Errorf("%s: %d, want 400", name, r.Status)
		}
	}
	// what was typed comes back with the error, so nothing is lost
	r := saveFooter(admin, `{"rows":[{"columns":[{"widgets":[{"type":"links","heading":"My precious heading","links":[{"label":"x","url":"javascript:alert(1)"}]}]}]}]}`)
	if r.Status != 400 || !strings.Contains(r.Body, `value="My precious heading"`) {
		t.Errorf("a rejected save shows what was typed: %d", r.Status)
	}
	if r := editor.get("/admin/footer"); r.Status != 403 {
		t.Errorf("an editor cannot open the footer builder: %d", r.Status)
	}
	if r := saveFooter(editor, builtFooter); r.Status != 403 {
		t.Errorf("an editor cannot change the footer: %d", r.Status)
	}
	if strings.Contains(editor.get("/admin/").Body, `href="/admin/footer"`) {
		t.Error("editors don't see the footer in the sidebar")
	}
	saveFooter(anon, builtFooter)
	if r := admin.postNoToken("/admin/footer", url.Values{"structure": {builtFooter}}); r.Status != 403 {
		t.Errorf("without the CSRF token: %d", r.Status)
	}
	if got := e.sql(`SELECT footer::text FROM site_settings`); got != initial {
		t.Errorf("nothing was saved by the refused requests: %s", got)
	}
	// the footer menu left the Menus screen
	if body := admin.get("/admin/menus").Body; strings.Contains(body, `id="menu-footer"`) {
		t.Error("the menus screen still has a footer menu")
	}
	if r := admin.post("/admin/menus/footer", url.Values{"structure": {`[]`}}); r.Status != 404 {
		t.Errorf("saving the old footer menu: %d", r.Status)
	}
}

// A site from before the builder: its footer columns, its footer menu and "contact details in the
// footer" all move into the builder's rows, once.
func TestOldFooterIsUpgraded(t *testing.T) {
	e := newEnv(t)
	anon := e.client()
	e.pool.Exec(t.Context(), `UPDATE site_settings SET footer = '{"bottom":"Old {year}","columns":[{"heading":"Hours","text":"Quokka o''clock"},{"heading":"Find us","text":"<p onclick=\"x()\">1 Road</p><script>alert(1)</script>"}]}',
		details = '{"phone":"021 555 0101","in_footer":true}'`)
	e.pool.Exec(t.Context(), `UPDATE menus SET items = '[{"kind":"link","label":"Privacy","url":"/privacy"},{"kind":"link","label":"Secret","url":"/s","hidden":true}]', sticky = true WHERE location = 'footer'`)

	// before the upgrade has run (it runs at startup), the old footer is already shown in the new shape
	if body := anon.get("/").Body; !strings.Contains(body, "Quokka o") || strings.Contains(body, "onclick") {
		t.Error("an old footer is shown, cleaned, before it is upgraded")
	}
	if err := content.NewStore(e.pool).UpgradeFooter(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := e.sql(`SELECT items::text FROM menus WHERE location = 'footer'`); got != "[]" {
		t.Errorf("the footer menu is emptied: %s", got)
	}
	body := anon.get("/").Body
	for _, want := range []string{
		`footer-row footer-cols-1 footer-ratio-equal footer-align-center`, `<ul class="footer-links is-inline"><li><a href="/privacy">Privacy</a></li></ul>`,
		`footer-row footer-cols-3`, `<h2>Hours</h2>`, `Quokka o`, `<h2>Contact</h2>`, `021 555 0101`, `Old ` + strconv.Itoa(time.Now().Year()),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("upgraded footer lacks %s", want)
		}
	}
	for _, not := range []string{"Secret", "onclick", "<script>alert", "menu-footer"} {
		if strings.Contains(body, not) {
			t.Errorf("upgraded footer shows %q", not)
		}
	}
	// it happens once: running it again changes nothing, even with a footer menu back in the table
	before := e.sql(`SELECT footer::text FROM site_settings`)
	e.pool.Exec(t.Context(), `UPDATE menus SET items = '[{"kind":"link","label":"Again","url":"/again"}]' WHERE location = 'footer'`)
	if err := content.NewStore(e.pool).UpgradeFooter(t.Context()); err != nil {
		t.Fatal(err)
	}
	if after := e.sql(`SELECT footer::text FROM site_settings`); after != before {
		t.Errorf("a second upgrade changed the footer:\n%s\n%s", before, after)
	}
}

func TestFooterSurvivesABackupRestore(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	saveFooter(admin, `{"version":2,"bottom":"Mine {year}","style":{"padding":"sm"},"rows":[{"columns":[{"widgets":[{"type":"text","heading":"Hours","text":"Quokka o'clock"}]},{"widgets":[{"type":"newsletter","heading":"News"}]}]}]}`)
	if r := admin.restoreUpload(t, []byte(admin.downloadBackup(false).Body), nil); r.Status != 200 {
		t.Fatalf("settle: %d", r.Status)
	}
	before := e.sql(`SELECT footer::text FROM site_settings`)
	raw := []byte(admin.downloadBackup(false).Body)
	if site := string(unzip(t, raw)["site.json"]); !strings.Contains(site, "Quokka o") || !strings.Contains(site, "Mine {year}") || !strings.Contains(site, `"newsletter"`) {
		t.Error("the backup holds the footer")
	}
	saveFooter(admin, `{"rows":[]}`)
	if strings.Contains(anon.get("/").Body, "Quokka") {
		t.Fatal("test setup")
	}
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if after := e.sql(`SELECT footer::text FROM site_settings`); after != before {
		t.Errorf("footer changed: %s vs %s", before, after)
	}
	if body := anon.get("/").Body; !strings.Contains(body, "Quokka o") || !strings.Contains(body, "Mine "+strconv.Itoa(time.Now().Year())) || !strings.Contains(body, `id="newsletter-0"`) {
		t.Error("the restored footer is shown")
	}
}

func TestFooterStoredByHandIsStillCleanedWhenShown(t *testing.T) {
	e := newEnv(t)
	anon := e.client()
	e.pool.Exec(t.Context(), `UPDATE site_settings SET footer = '{"version":2,"bottom":"B {year}","style":{"background":"red;}body{x"},"rows":[{"columns":[{"widgets":[
		{"type":"text","heading":"H","text":"<p onclick=\"x()\">Hi</p><script>alert(1)</script><a href=\"javascript:alert(2)\">x</a>"},
		{"type":"iframe","text":"<iframe src=evil>"},
		{"type":"links","links":[{"label":"Ok","url":"/ok"},{"label":"Bad","url":"javascript:alert(3)"}]}]},
		{"widgets":[]},{"widgets":[]},{"widgets":[]},{"widgets":[{"type":"text","heading":"5th column"}]}]}]}'`)
	body := anon.get("/").Body
	for _, not := range []string{"onclick", "<script>alert", "javascript:", "5th column", "iframe", "red;}"} {
		if strings.Contains(body, not) {
			t.Errorf("a footer put in the database directly still shows %q", not)
		}
	}
	if !strings.Contains(body, "<p>Hi</p>") || !strings.Contains(body, `<a href="/ok">Ok</a>`) || strings.Count(body, `class="footer-col"`) != 4 {
		t.Error("the clean parts are shown")
	}
}
