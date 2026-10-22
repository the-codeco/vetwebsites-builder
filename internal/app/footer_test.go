package app_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func footerForm(extra url.Values) url.Values { // a copy: the test client adds its token to forms it sends
	v := url.Values{}
	for k, vals := range extra {
		v[k] = vals
	}
	return v
}

func TestEditableFooter(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	year := strconv.Itoa(time.Now().Year())

	// nothing written yet: the plain line, no columns
	home := anon.get("/").Body
	if !strings.Contains(home, `<p class="footer-bottom">© `+year+` My Site</p>`) || strings.Contains(home, "footer-cols") {
		t.Errorf("default footer:\n%s", home)
	}
	if card := admin.get("/admin/appearance").Body; !strings.Contains(card, `action="/admin/appearance/footer"`) || !strings.Contains(card, `name="footer_text_4"`) || strings.Contains(card, `name="footer_text_5"`) {
		t.Error("the footer card offers four columns")
	}

	save := func(c *client, v url.Values) response { return c.post("/admin/appearance/footer", v) }
	form := footerForm(url.Values{
		"footer_heading_1": {"Opening  hours"}, "footer_text_1": {"Mon–Fri 9–5\n\nSat 9–12"},
		"footer_heading_2": {"Find us"}, "footer_text_2": {`<p>1 High Street <a href="/contact" onclick="x()">Contact</a> <a href="javascript:alert(1)">bad</a></p><script>alert(2)</script>`},
		"footer_heading_3": {}, "footer_text_3": {},
		"footer_heading_4": {"Just a heading"},
		"footer_bottom":    {"Copyright {year} {title} <b>all</b> rights"},
	})
	if r := save(admin, form); r.Status != 303 || !strings.HasSuffix(r.Header.Get("Location"), "done=footer") {
		t.Fatalf("save: %d", r.Status)
	}
	for _, theme := range []string{"", "?theme=journal", "?theme=midnight"} {
		body := admin.get("/" + theme).Body
		for _, want := range []string{
			`footer-cols footer-cols-3`, `<h2>Opening hours</h2>`, `<p>Mon–Fri 9–5</p>`, `<p>Sat 9–12</p>`, `<h2>Find us</h2>`, `href="/contact"`, `<h2>Just a heading</h2>`,
			`<p class="footer-bottom">Copyright ` + year + ` My Site &lt;b&gt;all&lt;/b&gt; rights</p>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("theme %q: footer lacks %s", theme, want)
			}
		}
		for _, not := range []string{"onclick", "javascript:", "<script>alert", "footer-col-4"} {
			if strings.Contains(body, not) {
				t.Errorf("theme %q: footer contains %q", theme, not)
			}
		}
		if strings.Count(body, `class="footer-col"`) != 3 {
			t.Errorf("theme %q: the empty column is left out", theme)
		}
	}
	// it is on every page, not only the home page
	for _, p := range []string{"/about", "/blog", "/search?q=x", "/nope"} {
		if body := anon.get(p).Body; !strings.Contains(body, `<h2>Find us</h2>`) {
			t.Errorf("%s has no footer content", p)
		}
	}
	// the form shows what was saved, and clearing everything goes back to the default
	card := admin.get("/admin/appearance").Body
	if !strings.Contains(card, `value="Opening hours"`) || !strings.Contains(card, `Copyright {year} {title}`) {
		t.Error("the form shows the saved footer")
	}
	if r := save(admin, url.Values{}); r.Status != 303 {
		t.Fatalf("clear: %d", r.Status)
	}
	if body := anon.get("/").Body; strings.Contains(body, "footer-cols") || !strings.Contains(body, `<p class="footer-bottom">© `+year+` My Site</p>`) {
		t.Error("cleared footer is the default again")
	}

	// limits and access
	long := func(k string, n int) url.Values { return url.Values{k: {strings.Repeat("x", n)}} }
	for name, v := range map[string]url.Values{"heading": long("footer_heading_1", 61), "text": long("footer_text_2", 5001), "bottom": long("footer_bottom", 301)} {
		if r := save(admin, v); r.Status != 400 {
			t.Errorf("too long %s: %d", name, r.Status)
		}
	}
	if r := save(editor, footerForm(form)); r.Status != 403 {
		t.Errorf("an editor cannot change the footer: %d", r.Status)
	}
	if r := save(anon, footerForm(form)); r.Status == 303 && strings.Contains(r.Header.Get("Location"), "done=footer") {
		t.Error("not logged in")
	}
	if r := admin.postNoToken("/admin/appearance/footer", url.Values{"footer_heading_1": {"No token"}}); r.Status != 403 {
		t.Errorf("without the CSRF token: %d", r.Status)
	}
	if got := e.sql(`SELECT footer::text FROM site_settings`); got != "{}" {
		t.Errorf("nothing was saved by the refused requests: %s", got)
	}
}

func TestFooterSurvivesABackupRestore(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	admin.post("/admin/appearance/footer", url.Values{"footer_heading_1": {"Hours"}, "footer_text_1": {"Quokka o'clock"}, "footer_bottom": {"Mine {year}"}})
	if r := admin.restoreUpload(t, []byte(admin.downloadBackup(false).Body), nil); r.Status != 200 {
		t.Fatalf("settle: %d", r.Status)
	}
	before := e.sql(`SELECT footer::text FROM site_settings`)
	raw := []byte(admin.downloadBackup(false).Body)
	if site := string(unzip(t, raw)["site.json"]); !strings.Contains(site, "Quokka o") || !strings.Contains(site, "Mine {year}") {
		t.Error("the backup holds the footer")
	}
	admin.post("/admin/appearance/footer", url.Values{})
	if strings.Contains(anon.get("/").Body, "Quokka") {
		t.Fatal("test setup")
	}
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if after := e.sql(`SELECT footer::text FROM site_settings`); after != before {
		t.Errorf("footer changed: %s vs %s", before, after)
	}
	if body := anon.get("/").Body; !strings.Contains(body, "Quokka o") || !strings.Contains(body, "Mine "+strconv.Itoa(time.Now().Year())) {
		t.Error("the restored footer is shown")
	}
}

func TestFooterStoredByHandIsStillCleanedWhenShown(t *testing.T) {
	e := newEnv(t)
	anon := e.client()
	e.pool.Exec(t.Context(), `UPDATE site_settings SET footer = '{"bottom":"B {year}","columns":[{"heading":"H","text":"<p onclick=\"x()\">Hi</p><script>alert(1)</script><a href=\"javascript:alert(2)\">x</a>"},"junk",{"heading":"2"},{"heading":"3"},{"heading":"4"},{"heading":"5th"}]}'`)
	body := anon.get("/").Body
	for _, not := range []string{"onclick", "<script>alert", "javascript:", "5th"} {
		if strings.Contains(body, not) {
			t.Errorf("a footer put in the database directly still shows %q", not)
		}
	}
	if !strings.Contains(body, "<p>Hi</p>") || strings.Count(body, `class="footer-col"`) != 4 {
		t.Error("the clean parts are shown")
	}
}
