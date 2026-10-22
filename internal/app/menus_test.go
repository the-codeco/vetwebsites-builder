package app_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const richMain = `[
  {"kind":"link","label":"Home","url":"/"},
  {"kind":"link","label":"Docs","url":"https://example.com/docs","new_tab":true},
  {"kind":"link","label":"Secret","url":"/secret","hidden":true},
  {"kind":"dropdown","label":"Company","children":[
     {"label":"About","url":"/about"},{"label":"Old page","url":"/old","hidden":true},{"label":"Team","url":"/team"}]},
  {"kind":"mega","label":"Products","columns":[
     {"heading":"Apps","links":[{"label":"Alpha","url":"/alpha","description":"The first one"},{"label":"Beta","url":"/beta"}]},
     {"heading":"Tools","links":[{"label":"Delta","url":"/delta","new_tab":true}]}]},
  {"kind":"dropdown","label":"Nothing to show","children":[{"label":"x","url":"/x","hidden":true}]}
]`

func saveMenu(c *client, section, structure string, sticky bool) response {
	v := url.Values{"structure": {structure}}
	if sticky {
		v.Set("sticky", "on")
	}
	return c.post("/admin/menus/"+section, v)
}

func TestMenuBuilder(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "editor@example.com", "editor")
	anon := e.client()

	// the three sections, with the starter links seeded into the main navigation
	page := admin.get("/admin/menus")
	if page.Status != 200 {
		t.Fatalf("menus page: %d", page.Status)
	}
	for _, want := range []string{"Top bar", "Main navigation", "Footer", `data-menu-form`, `tpl-item-main`, `tpl-item-topbar`, `tpl-item-footer`,
		`value="Home"`, `value="About"`, `<option value="mega"`, "Freeze", `id="preview"`} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("builder page lacks %q", want)
		}
	}
	// only the main navigation offers dropdowns and mega menus
	if n := strings.Count(page.Body, `<option value="mega"`); n != 3 { // two saved rows + the blank "add" template
		t.Errorf("mega option appears %d times, want 3 (main only)", n)
	}
	if r := editor.get("/admin/menus"); r.Status != 200 {
		t.Errorf("editors manage menus too: %d", r.Status)
	}
	if r := anon.get("/admin/menus"); r.Status != 303 {
		t.Errorf("anonymous: %d", r.Status)
	}
	if nav := anon.get("/").Body; !strings.Contains(nav, `href="/about"`) || !strings.Contains(nav, `class="menu-main"`) {
		t.Error("the seeded main navigation should be public")
	}

	// bad submissions never save and say what is wrong
	bad := map[string]struct {
		section, structure string
		want               int
	}{
		"not JSON":                {"main", "nope", 400},
		"unknown field":           {"main", `[{"kind":"link","label":"x","url":"/x","evil":true}]`, 400},
		"javascript link":         {"main", `[{"kind":"link","label":"Evil","url":"javascript:alert(1)"}]`, 400},
		"javascript in dropdown":  {"main", `[{"kind":"dropdown","label":"D","children":[{"label":"x","url":"javascript:alert(1)"}]}]`, 400},
		"data url in mega":        {"main", `[{"kind":"mega","label":"M","columns":[{"links":[{"label":"x","url":"data:text/html,x"}]}]}]`, 400},
		"link without a label":    {"main", `[{"kind":"link","label":"","url":"/x"}]`, 400},
		"five columns":            {"main", `[{"kind":"mega","label":"M","columns":[` + strings.TrimSuffix(strings.Repeat(`{"heading":"h","links":[{"label":"a","url":"/a"}]},`, 5), ",") + `]}]`, 400},
		"dropdown in the top bar": {"topbar", `[{"kind":"dropdown","label":"D","children":[{"label":"a","url":"/a"}]}]`, 400},
		"mega in the footer":      {"footer", `[{"kind":"mega","label":"M","columns":[{"links":[{"label":"a","url":"/a"}]}]}]`, 400},
		"unknown section":         {"sidebar", `[]`, 404},
		"too large":               {"main", `[` + strings.Repeat(`{"kind":"link","label":"x","url":"/x"},`, 6000) + `{"kind":"link","label":"y","url":"/y"}]`, 400},
	}
	for name, c := range bad {
		if r := saveMenu(admin, c.section, c.structure, false); r.Status != c.want {
			t.Errorf("%s: %d, want %d", name, r.Status, c.want)
		}
	}
	if got := e.sql(`SELECT jsonb_array_length(items) FROM menus WHERE location = 'main'`); got != "2" {
		t.Errorf("a rejected save changed the menu: %s items", got)
	}
	// what the person typed comes back with the error, so nothing is lost
	r := saveMenu(admin, "main", `[{"kind":"link","label":"My precious label","url":"javascript:alert(1)"}]`, true)
	if r.Status != 400 || !strings.Contains(r.Body, "My precious label") || !strings.Contains(r.Body, "valid address") {
		t.Errorf("error page should keep the input: %d", r.Status)
	}
	if !regexp.MustCompile(`name="sticky" checked`).MatchString(r.Body) {
		t.Error("the freeze tick must also survive a failed save")
	}

	// a rich main menu, a frozen top bar and header, an unfrozen footer
	if r := saveMenu(admin, "main", richMain, true); r.Status != 303 {
		t.Fatalf("save main: %d %s", r.Status, firstLine(r.Body))
	}
	if r := saveMenu(admin, "topbar", `[{"kind":"link","label":"Call us","url":"mailto:hi@example.com"},{"kind":"link","label":"Log in","url":"/admin/login","new_tab":true}]`, true); r.Status != 303 {
		t.Fatalf("save topbar: %d", r.Status)
	}
	if r := saveMenu(editor, "footer", `[{"kind":"link","label":"Privacy","url":"/privacy"}]`, false); r.Status != 303 {
		t.Fatalf("an editor saving the footer: %d", r.Status)
	}
	if got := e.sql(`SELECT items->3->>'kind' FROM menus WHERE location = 'main'`); got != "dropdown" {
		t.Errorf("stored structure: %q", got)
	}
	if got := e.sql(`SELECT jsonb_array_length(items) FROM menus WHERE location = 'main'`); got != "6" {
		t.Errorf("hidden items are kept in the saved data so hiding is reversible: %s items", got)
	}

	home := anon.get("/").Body
	for _, want := range []string{
		`<nav class="menu-topbar is-sticky"`, `href="mailto:hi@example.com"`, `href="/admin/login" target="_blank" rel="noopener noreferrer">Log in`,
		`header class="site-header is-sticky"`,
		`href="https://example.com/docs" target="_blank" rel="noopener noreferrer">Docs`,
		`aria-controls="menu-panel-2">Company`, `aria-controls="menu-panel-3">Products`,
		`class="menu-mega-grid mega-cols-2"`, `<p class="menu-col-heading">Apps</p>`, `<span class="menu-desc">The first one</span>`,
		`<nav class="menu-footer" aria-label="Footer">`, `href="/privacy"`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("home page lacks %q", want)
		}
	}
	for _, gone := range []string{"Secret", "Old page", "Nothing to show", `menu-footer is-sticky`} {
		if strings.Contains(home, gone) {
			t.Errorf("home page shows %q", gone)
		}
	}
	if n := strings.Count(home, `class="menu-toggle"`); n != 2 {
		t.Errorf("%d panel buttons, want 2 (the dropdown with nothing visible is dropped)", n)
	}

	// the page you're on is marked, including the dropdown that contains it
	about := anon.get("/about").Body
	if !strings.Contains(about, `href="/about" aria-current="page">About`) || !strings.Contains(about, `menu-item has-panel is-current`) {
		t.Error("the current page is not marked inside its dropdown")
	}

	// unfreezing, and showing a hidden item again
	shown := strings.Replace(richMain, `"url":"/secret","hidden":true`, `"url":"/secret","hidden":false`, 1)
	if shown == richMain {
		t.Fatal("test setup: nothing to un-hide")
	}
	saveMenu(admin, "main", shown, false)
	home = anon.get("/").Body
	if strings.Contains(home, `site-header is-sticky`) || !strings.Contains(home, `href="/secret"`) {
		t.Error("unfreezing, or un-hiding, did not take effect")
	}
	if !strings.Contains(home, `menu-topbar is-sticky`) {
		t.Error("freezing one section must not affect another")
	}

	// the builder shows saved dropdown/mega rows and the hidden state when reopened
	again := admin.get("/admin/menus").Body
	for _, want := range []string{`value="Company"`, `value="Products"`, `value="Apps"`, `value="Alpha"`, `value="The first one"`, `data-kind="mega"`, `data-kind="dropdown"`} {
		if !strings.Contains(again, want) {
			t.Errorf("reopened builder lacks %q", want)
		}
	}
	saveMenu(admin, "main", richMain, true) // hide "Secret" again
	if !strings.Contains(admin.get("/admin/menus").Body, `data-f="hidden" checked`) {
		t.Error("a hidden item must show as hidden when the builder is reopened")
	}

	// clearing a section removes it from the page
	if r := saveMenu(admin, "topbar", `[]`, false); r.Status != 303 {
		t.Fatal("clear topbar")
	}
	if strings.Contains(anon.get("/").Body, "menu-topbar") {
		t.Error("an empty top bar must render nothing")
	}
}

func TestMenuScriptAndThemes(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	saveMenu(admin, "main", richMain, true)
	saveMenu(admin, "footer", `[{"kind":"link","label":"Privacy","url":"/privacy"}]`, true)

	js := anon.get("/theme/default/assets/menu.js")
	if js.Status != 200 || !strings.Contains(js.Header.Get("Content-Type"), "javascript") || js.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(js.Body, "aria-expanded") {
		t.Errorf("menu.js: %d %s", js.Status, js.Header.Get("Content-Type"))
	}
	if r := anon.get("/theme/default/assets/../theme.json"); r.Status == 200 {
		t.Error("path traversal out of assets/")
	}

	// public pages may carry style attributes for block colours (their CSP allows that), so only
	// event handlers and inline scripts are banned page-wide; the menus themselves get no inline style
	inline := regexp.MustCompile(`(?i)<[a-z][^>]*\son[a-z]+\s*=|<script(\s[^>]*)?>[^<]`)
	navBlock := regexp.MustCompile(`(?s)<nav.*?</nav>`)
	styleAttr := regexp.MustCompile(`(?i)\sstyle\s*=`)
	for _, theme := range []string{"default", "journal", "midnight"} {
		if r := admin.post("/admin/appearance/theme", url.Values{"theme": {theme}}); r.Status != 303 {
			t.Fatal(theme)
		}
		for _, path := range []string{"/", "/about", "/blog", "/nope"} {
			body := anon.get(path).Body
			if !strings.Contains(body, `class="menu-main"`) || !strings.Contains(body, `mega-cols-2`) || !strings.Contains(body, `/theme/default/assets/menu.js`) {
				t.Errorf("%s %s: navigation or script missing", theme, path)
			}
			if m := inline.FindString(withoutStructuredData(body)); m != "" {
				t.Errorf("%s %s: inline code on a public page: %.80s", theme, path, m)
			}
			for _, nav := range navBlock.FindAllString(body, -1) {
				if styleAttr.MatchString(nav) {
					t.Errorf("%s %s: inline style inside a menu", theme, path)
				}
			}
			if !strings.Contains(body, `menu-footer is-sticky`) || !strings.Contains(body, `site-header`) || !strings.Contains(body, ` is-sticky"`) {
				t.Errorf("%s %s: frozen sections missing their class", theme, path)
			}
		}
	}
}
