package app_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestLogoAndTabIcon(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	home := anon.get("/").Body
	if !strings.Contains(home, `<a class="site-title" href="/">My Site</a>`) || strings.Contains(home, "site-logo") || strings.Contains(home, `rel="icon"`) {
		t.Error("without a logo the header is the plain title")
	}
	if card := admin.get("/admin/appearance").Body; !strings.Contains(card, `action="/admin/appearance/brand"`) || !strings.Contains(card, `name="logo_height"`) || !strings.Contains(card, `id="media-picker"`) {
		t.Error("the logo card, with the media picker")
	}

	save := func(c *client, v url.Values) response { return c.post("/admin/appearance/brand", v) }
	if r := save(admin, url.Values{"logo": {"/media/logo.png"}, "logo_alt": {"  Acme   Ltd "}, "logo_height": {"lg"}, "icon": {"/media/icon.png"}}); r.Status != 303 || !strings.HasSuffix(r.Header.Get("Location"), "done=brand") {
		t.Fatalf("save: %d", r.Status)
	}
	for _, theme := range []string{"", "?theme=journal", "?theme=midnight"} {
		body := admin.get("/" + theme).Body
		for _, want := range []string{
			`<a class="site-title site-brand" href="/"><img class="site-logo" src="/media/logo.png" alt="" height="64"><span>My Site</span></a>`,
			`<link rel="icon" href="/media/icon.png">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("theme %q lacks %s", theme, want)
			}
		}
	}
	// logo only: the picture carries the name, so it needs a description
	save(admin, url.Values{"logo": {"/media/logo.png"}, "logo_alt": {`Acme "Ltd"`}, "hide_title": {"on"}})
	body := anon.get("/").Body
	if !strings.Contains(body, `alt="Acme &#34;Ltd&#34;" height="44"></a>`) || strings.Contains(body, "<span>My Site</span>") {
		t.Errorf("logo only:\n%s", body)
	}
	save(admin, url.Values{"logo": {"/media/logo.png"}, "hide_title": {"on"}})
	if body := anon.get("/").Body; !strings.Contains(body, `alt="My Site"`) {
		t.Error("the description defaults to the site title")
	}

	// refusals: nothing is saved
	for name, v := range map[string]url.Values{
		"script logo": {"logo": {"javascript:alert(1)"}}, "data logo": {"logo": {"data:image/svg+xml,<svg>"}}, "mailto logo": {"logo": {"mailto:a@b.com"}}, "tel icon": {"icon": {"tel:0821234567"}},
		"relative": {"logo": {"logo.png"}}, "long alt": {"logo": {"/media/a.png"}, "logo_alt": {strings.Repeat("a", 121)}},
	} {
		if r := save(admin, v); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if r := save(editor, url.Values{"logo": {"/media/x.png"}}); r.Status != 403 {
		t.Errorf("an editor cannot change the logo: %d", r.Status)
	}
	if r := admin.postNoToken("/admin/appearance/brand", url.Values{"logo": {"/media/x.png"}}); r.Status != 403 {
		t.Errorf("without the CSRF token: %d", r.Status)
	}
	if got := e.sql(`SELECT brand->>'logo' FROM site_settings`); got != "/media/logo.png" {
		t.Errorf("refused requests changed nothing: %q", got)
	}
	// clearing the logo brings the title back; an icon can stay on its own
	save(admin, url.Values{"icon": {"/media/icon.png"}})
	if body := anon.get("/").Body; !strings.Contains(body, `<a class="site-title" href="/">My Site</a>`) || !strings.Contains(body, `rel="icon"`) {
		t.Error("no logo: the title; the icon stays")
	}
	// a logo put in the database by hand is still checked when shown
	e.pool.Exec(t.Context(), `UPDATE site_settings SET brand = '{"logo":"javascript:alert(1)","icon":"data:x","height":"huge","logo_alt":"x"}'`)
	if body := anon.get("/").Body; strings.Contains(body, "javascript:") || strings.Contains(body, "data:x") || strings.Contains(body, "site-logo") {
		t.Error("stored junk is not shown")
	}
}

func TestMenuItemsCanBeButtons(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	items := `[{"kind":"link","label":"Home","url":"/"},{"kind":"link","label":"Book now","url":"/contact-us","button":true},` +
		`{"kind":"dropdown","label":"More","button":true,"children":[{"label":"A","url":"/a"}]}]`
	if r := saveMenu(admin, "main", items, false); r.Status != 303 {
		t.Fatalf("save menu: %d", r.Status)
	}
	if r := saveMenu(admin, "topbar", `[{"kind":"link","label":"Call us","url":"tel:+27 21 555 0123","button":true}]`, false); r.Status != 303 {
		t.Fatalf("a phone link in the top bar: %d", r.Status)
	}
	body := anon.get("/").Body
	for _, want := range []string{`<a href="/contact-us" class="btn btn-primary">Book now</a>`, `<a href="tel:&#43;27%2021%20555%200123" class="btn btn-primary">Call us</a>`, `<a href="/"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if strings.Contains(body, `class="btn btn-primary">More`) || strings.Contains(body, `class="btn btn-primary">Home`) {
		t.Error("only links marked as buttons are buttons")
	}
	if strings.Contains(body, `<a href="/" class="btn`) {
		t.Error("an ordinary link stays a link")
	}
	if got := e.sql(`SELECT items::text FROM menus WHERE location = 'main'`); strings.Count(got, `"button": true`) != 1 {
		t.Errorf("a dropdown cannot be a button: %s", got)
	}
	if editor := admin.get("/admin/menus").Body; !strings.Contains(editor, `data-f="button"`) {
		t.Error("the menu editor has the Button checkbox")
	}
}

func TestSiteDetailsAndTheDetailsBlock(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	year := "©"

	if body := admin.get("/admin/details").Body; !strings.Contains(body, `name="phone"`) || !strings.Contains(body, `name="social_instagram"`) || !strings.Contains(body, `name="in_footer"`) {
		t.Error("the site details screen")
	}
	if !strings.Contains(admin.get("/admin/").Body, `href="/admin/details"`) {
		t.Error("the sidebar links to it")
	}
	form := url.Values{
		"phone": {"+27 (21) 555-0123"}, "whatsapp": {"+27 82 555 0123"}, "email": {"hello@example.com"},
		"address": {"1 High Street  \n\n  Cape <Town>\r\n8001"}, "hours": {"Mon to Fri 8 to 5\nSat 9 to 1"},
		"social_facebook": {"https://www.facebook.com/acme"}, "social_x": {"https://twitter.com/acme"}, "social_youtube": {"https://youtu.be/abc"},
	}
	if r := admin.post("/admin/details", form); r.Status != 303 || !strings.HasSuffix(r.Header.Get("Location"), "done=saved") {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	if got := e.sql(`SELECT details->>'address' FROM site_settings`); got != "1 High Street\nCape <Town>\n8001" {
		t.Errorf("lines are tidied: %q", got)
	}

	id := newPage(t, admin, "Contact")
	publishPage(t, admin, id, "Contact", "find-us")
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"details"}})
	block := e.blockID(id, "details")
	page := anon.get("/find-us").Body
	for _, want := range []string{
		`<h2>Get in touch</h2>`, `<a href="tel:+2721555 0123"`, // placeholder replaced below
	} {
		_ = want
	}
	for _, want := range []string{
		`<h2>Get in touch</h2>`, `href="https://wa.me/27825550123"`, `href="mailto:hello@example.com">hello@example.com</a>`,
		`1 High Street`, `Cape &lt;Town&gt;`, `Mon to Fri 8 to 5`, `href="https://www.facebook.com/acme"`, `>Facebook</a>`, `>X (Twitter)</a>`, `>YouTube</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the block lacks %s", want)
		}
	}
	if !regexp.MustCompile(`href="tel:(\+|&#43;)27215550123">(\+|&#43;)27 \(21\) 555-0123</a>`).MatchString(page) {
		t.Error("the phone number is a tap-to-call link")
	}
	if strings.Contains(page, "Instagram") || strings.Contains(page, "<Town>") {
		t.Error("only what is set is shown, escaped")
	}
	// choose what the block shows
	admin.post("/admin/blocks/"+block, url.Values{"p.heading": {"Call us"}, "p.show_phone": {"on"}})
	page = anon.get("/find-us").Body
	if !strings.Contains(page, "27215550123") || strings.Contains(page, "mailto:") || strings.Contains(page, "High Street") || strings.Contains(page, "Facebook") {
		t.Errorf("only the ticked parts:\n%s", page)
	}
	if !strings.Contains(page, "<h2>Call us</h2>") {
		t.Error("its own heading")
	}

	// the footer, only when asked
	if strings.Contains(anon.get("/").Body, "footer-details") {
		t.Error("not in the footer by default")
	}
	form.Set("in_footer", "on")
	admin.post("/admin/details", form)
	for _, theme := range []string{"", "?theme=journal", "?theme=midnight"} {
		body := admin.get("/" + theme).Body
		if !strings.Contains(body, `footer-details`) || !strings.Contains(body, `27215550123`) || !strings.Contains(body, "mailto:hello@example.com") {
			t.Errorf("theme %q: the footer shows the details", theme)
		}
		if !strings.Contains(body, year) {
			t.Errorf("theme %q: the bottom line is still there", theme)
		}
	}
	// the editable footer columns and the details sit together
	admin.post("/admin/appearance/footer", url.Values{"footer_heading_1": {"Hours"}, "footer_text_1": {"Open"}})
	if body := anon.get("/").Body; !strings.Contains(body, `footer-cols footer-cols-1`) || !strings.Contains(body, "footer-details") {
		t.Error("columns and details together")
	}

	// bad values are reported against their field, and nothing is saved
	before := e.sql(`SELECT details::text FROM site_settings`)
	for name, v := range map[string]url.Values{
		"short phone": {"phone": {"123"}}, "letters phone": {"phone": {"call me"}}, "bad whatsapp": {"whatsapp": {"12"}}, "local whatsapp": {"whatsapp": {"082 555 0123"}},
		"bad email": {"email": {"not an email"}}, "script email": {"email": {"a@b.co<script>"}},
		"wrong site": {"social_facebook": {"https://evil.example/facebook.com"}}, "http only": {"social_instagram": {"http://instagram.com/x"}},
		"javascript": {"social_tiktok": {"javascript:alert(1)"}}, "lookalike": {"social_linkedin": {"https://notlinkedin.com/x"}}, "credentials": {"social_x": {"https://user:pw@x.com/a"}},
	} {
		r := admin.post("/admin/details", v)
		if r.Status != 400 || !strings.Contains(r.Body, "Nothing was saved") || !strings.Contains(r.Body, "field-error") {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if e.sql(`SELECT details::text FROM site_settings`) != before {
		t.Error("refused requests changed nothing")
	}
	if r := editor.post("/admin/details", form); r.Status != 403 {
		t.Errorf("an editor cannot change the details: %d", r.Status)
	}
	if r := admin.postNoToken("/admin/details", url.Values{"phone": {"+27 21 555 0123"}}); r.Status != 403 {
		t.Errorf("without the CSRF token: %d", r.Status)
	}
	// details written by hand are cleaned when shown
	e.pool.Exec(t.Context(), `UPDATE site_settings SET details = '{"phone":"<b>1</b>","email":"x\"onmouseover=\"y","social":{"facebook":"javascript:alert(1)","x":"https://evil.example"},"address":"fine","in_footer":true}'`)
	body := anon.get("/").Body
	if strings.Contains(body, "javascript:") || strings.Contains(body, "onmouseover") || strings.Contains(body, "evil.example") || strings.Contains(body, "<b>1</b>") || !strings.Contains(body, "fine") {
		t.Error("stored junk is cleaned, the good part stays")
	}
	// clearing everything removes the block's content and the footer column
	admin.post("/admin/details", url.Values{})
	if body := anon.get("/find-us").Body; strings.Contains(body, `class="details"`) {
		t.Error("nothing to show, nothing drawn")
	}
	_ = regexp.MustCompile
}

func TestAnalyticsServices(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	// nothing set: no outside scripts, and the usual policy
	basePolicy := anon.get("/").Header.Get("Content-Security-Policy")
	home := anon.get("/")
	if strings.Contains(home.Body, "track.js") || strings.Contains(basePolicy, "googletagmanager") || strings.Contains(basePolicy, "script-src") {
		t.Error("no services, no loader, strict policy")
	}
	if card := admin.get("/admin/analytics").Body; !strings.Contains(card, `action="/admin/analytics/tracking"`) || !strings.Contains(card, `name="ga4"`) {
		t.Error("the services card (admins only)")
	}
	if strings.Contains(editor.get("/admin/analytics").Body, `name="ga4"`) {
		t.Error("an editor does not see the services card")
	}

	save := func(c *client, v url.Values) response { return c.post("/admin/analytics/tracking", v) }
	if r := save(admin, url.Values{"ga4": {" g-abc123xyz "}, "plausible": {"https://www.Example.com/"}, "wait": {"on"}}); r.Status != 303 || !strings.HasSuffix(r.Header.Get("Location"), "done=tracking") {
		t.Fatalf("save: %d", r.Status)
	}
	if got := e.sql(`SELECT tracking::text FROM site_settings`); !strings.Contains(got, `"G-ABC123XYZ"`) || !strings.Contains(got, `"example.com"`) || strings.Contains(got, "no_wait") {
		t.Errorf("stored cleaned: %s", got)
	}
	page := anon.get("/")
	tag := regexp.MustCompile(`<script src="(/theme/default/assets/track\.js\?v=[0-9a-f]+)" defer data-wait="1" data-ga4="G-ABC123XYZ" data-plausible="example.com"></script>`).FindStringSubmatch(page.Body)
	if tag == nil {
		t.Fatalf("the loader tag is not on the page:\n%s", page.Body)
	}
	if js := anon.get(tag[1]); js.Status != 200 || !strings.Contains(js.Header.Get("Content-Type"), "javascript") || !strings.Contains(js.Body, "wb:consent") {
		t.Errorf("the loader script: %d", js.Status)
	}
	csp := page.Header.Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self' https://www.googletagmanager.com https://plausible.io;", "connect-src 'self' https://www.googletagmanager.com https://*.google-analytics.com https://*.analytics.google.com https://plausible.io;", "default-src 'self'", "frame-ancestors 'self'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("policy lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src *") || regexp.MustCompile(`script-src[^;]*unsafe-inline`).MatchString(csp) {
		t.Errorf("no inline or eval scripts: %s", csp)
	}
	if extra := foreignScripts(page.Body); len(extra) != 0 && !(len(extra) == 1 && strings.Contains(extra[0], "track.js")) {
		t.Errorf("unexpected scripts: %v", extra)
	}
	// every kind of public page has it, and the owner's own visits do not
	for _, p := range []string{"/about", "/blog", "/search?q=x", "/nope"} {
		if !strings.Contains(anon.get(p).Body, "track.js") {
			t.Errorf("%s lacks the loader", p)
		}
	}
	if strings.Contains(admin.get("/").Body, "track.js") || strings.Contains(admin.get("/").Header.Get("Content-Security-Policy"), "googletagmanager") {
		t.Error("logged-in users are not tracked")
	}
	// only GA: only its hosts; no waiting
	save(admin, url.Values{"ga4": {"G-ABC123XYZ"}})
	page = anon.get("/")
	if !strings.Contains(page.Body, `data-wait="0" data-ga4="G-ABC123XYZ"></script>`) || strings.Contains(page.Body, "data-plausible") {
		t.Error("loads at once when not told to wait; no Plausible tag")
	}
	if csp := page.Header.Get("Content-Security-Policy"); strings.Contains(csp, "plausible") || !strings.Contains(csp, "googletagmanager") {
		t.Errorf("only the hosts of the services in use: %s", csp)
	}

	// refusals
	for name, v := range map[string]url.Values{
		"ga4 script": {"ga4": {`"><script>alert(1)</script>`}}, "ua id": {"ga4": {"UA-123-1"}}, "gtm id": {"ga4": {"GTM-ABCDEF"}}, "long": {"ga4": {"G-" + strings.Repeat("A", 30)}},
		"plausible script": {"plausible": {`x.com"><script>`}}, "plausible no dot": {"plausible": {"localhost"}}, "plausible space": {"plausible": {"a b.com"}},
	} {
		if r := save(admin, v); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if r := save(editor, url.Values{"ga4": {"G-ZZZZ9999"}}); r.Status != 403 {
		t.Errorf("an editor cannot set services: %d", r.Status)
	}
	if r := admin.postNoToken("/admin/analytics/tracking", url.Values{"ga4": {"G-ZZZZ9999"}}); r.Status != 403 {
		t.Errorf("without the CSRF token: %d", r.Status)
	}
	if got := e.sql(`SELECT tracking->>'ga4' FROM site_settings`); got != "G-ABC123XYZ" {
		t.Errorf("refused requests changed nothing: %s", got)
	}
	// IDs put in the database by hand are checked again before they reach a page
	e.pool.Exec(t.Context(), `UPDATE site_settings SET tracking = '{"ga4":"\"><script>alert(1)</script>","plausible":"evil.com\" onload=\"x"}'`)
	if body := anon.get("/").Body; strings.Contains(body, "<script>alert") || strings.Contains(body, "onload") || strings.Contains(body, "track.js") {
		t.Error("stored junk never reaches the page")
	}
	// a warning when the services wait for a choice the cookie notice does not offer
	save(admin, url.Values{"ga4": {"G-ABC123XYZ"}, "wait": {"on"}})
	if !strings.Contains(admin.get("/admin/analytics").Body, "will never load") {
		t.Error("warn about waiting for a notice that is not set up")
	}
	admin.post("/admin/appearance/consent", url.Values{"consent_enabled": {"on"}, "consent_mode": {"choice"}})
	if strings.Contains(admin.get("/admin/analytics").Body, "will never load") {
		t.Error("no warning once Accept and Decline is on")
	}
}

func TestBrandDetailsAndTrackingSurviveABackupRestore(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	admin.post("/admin/appearance/brand", url.Values{"logo": {"/media/logo.png"}, "logo_alt": {"Acme"}, "logo_height": {"sm"}, "icon": {"/media/i.png"}})
	admin.post("/admin/details", url.Values{"phone": {"+27 21 555 0123"}, "email": {"a@example.com"}, "in_footer": {"on"}, "social_facebook": {"https://facebook.com/a"}})
	admin.post("/admin/analytics/tracking", url.Values{"ga4": {"G-ABC123XYZ"}})
	read := func() string {
		return e.sql(`SELECT brand::text||'|'||details::text||'|'||tracking::text FROM site_settings`)
	}
	if r := admin.restoreUpload(t, []byte(admin.downloadBackup(false).Body), nil); r.Status != 200 {
		t.Fatalf("settle: %d", r.Status)
	}
	before := read()
	raw := []byte(admin.downloadBackup(false).Body)
	site := string(unzip(t, raw)["site.json"])
	for _, want := range []string{`"logo": "/media/logo.png"`, `"phone": "+27 21 555 0123"`, `"ga4": "G-ABC123XYZ"`} {
		if !strings.Contains(site, want) {
			t.Errorf("site.json lacks %s", want)
		}
	}
	e.pool.Exec(t.Context(), `UPDATE site_settings SET brand = '{}', details = '{}', tracking = '{}'`)
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if after := read(); after != before {
		t.Errorf("settings changed:\n%s\n%s", before, after)
	}
	if body := anon.get("/").Body; !strings.Contains(body, "site-logo") || !strings.Contains(body, "footer-details") || !strings.Contains(body, "track.js") {
		t.Error("the restored site shows them")
	}
}
