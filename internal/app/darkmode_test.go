package app_test

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var reColorJS = regexp.MustCompile(`<script src="(/theme/default/assets/colormode\.js\?v=[0-9a-f]+)"></script>`)

func (c *client) setColorMode(t *testing.T, mode string) {
	t.Helper()
	if r := c.post("/admin/appearance/colormode", url.Values{"color_mode": {mode}}); r.Status != 303 {
		t.Fatalf("set %s: %d", mode, r.Status)
	}
}

func TestDarkModeSwitch(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	// out of the box: a switch, light by default
	home := anon.get("/").Body
	js := scrape(reColorJS, home)
	for _, want := range []string{`<html lang="en" data-color-mode="toggle">`, `data-color-toggle`, `aria-pressed="false"`, `aria-label="Switch to dark mode"`, `class="icon-moon"`, `class="icon-sun"`} {
		if !strings.Contains(home, want) {
			t.Errorf("home lacks %s", want)
		}
	}
	if js == "" {
		t.Fatal("the colour script is not loaded in the page")
	}
	if strings.Index(home, js) > strings.Index(home, "<body") {
		t.Error("the colour script must load in <head>, before the page paints")
	}
	if extra := foreignScripts(home); len(extra) != 0 {
		t.Errorf("unexpected scripts: %v", extra)
	}
	script := anon.get(js)
	if script.Status != 200 || !strings.Contains(script.Header.Get("Content-Type"), "javascript") || !strings.Contains(script.Body, "localStorage") {
		t.Errorf("script: %d %q", script.Status, script.Header.Get("Content-Type"))
	}
	cssToggle := anon.get("/site.css")
	if !strings.Contains(cssToggle.Body, `:root[data-theme="dark"]`) || strings.Contains(cssToggle.Body, "prefers-color-scheme") {
		t.Errorf("toggle mode CSS:\n%s", cssToggle.Body)
	}
	if got := anon.get("/").Header.Get("Set-Cookie"); got != "" {
		t.Errorf("dark mode must not set a cookie: %q", got)
	}

	// automatic: follows the system, so the stylesheet says so, and it is revalidated (new ETag)
	admin.setColorMode(t, "auto")
	cssAuto := anon.get("/site.css")
	if !strings.Contains(cssAuto.Body, "@media (prefers-color-scheme: dark)") || !strings.Contains(cssAuto.Body, `:root:not([data-theme="light"])`) {
		t.Errorf("auto mode CSS:\n%s", cssAuto.Body)
	}
	if cssAuto.Header.Get("ETag") == cssToggle.Header.Get("ETag") {
		t.Error("changing the mode must change the stylesheet's ETag, or visitors keep the old one")
	}
	if !strings.Contains(anon.get("/").Body, `data-color-mode="auto"`) {
		t.Error("auto attribute")
	}

	// off: nothing dark anywhere
	admin.setColorMode(t, "light")
	off := anon.get("/").Body
	for _, not := range []string{"data-color-mode", "colormode.js", "data-color-toggle"} {
		if strings.Contains(off, not) {
			t.Errorf("dark mode is off but the page contains %q", not)
		}
	}
	if css := anon.get("/site.css").Body; strings.Contains(css, "data-theme") || strings.Contains(css, "prefers-color-scheme") {
		t.Error("dark CSS served while off")
	}

	// every page type gets the switch (and the layouts of both light themes)
	admin.setColorMode(t, "toggle")
	for _, theme := range []string{"default", "journal"} {
		admin.post("/admin/appearance/theme", url.Values{"theme": {theme}})
		for _, p := range []string{"/", "/about", "/blog", "/search?q=x", "/nope"} {
			body := anon.get(p).Body
			if !strings.Contains(body, `data-color-toggle`) || !reColorJS.MatchString(body) || !strings.Contains(body, `data-color-mode="toggle"`) {
				t.Errorf("%s %s: no dark mode switch", theme, p)
			}
		}
	}
	if css := anon.get("/site.css").Body; !strings.Contains(css, "--color-bg: #1c1815;") {
		t.Errorf("journal's own dark palette isn't served:\n%s", css)
	}

	// a theme that is already dark has nothing to switch between
	admin.post("/admin/appearance/theme", url.Values{"theme": {"midnight"}})
	mid := anon.get("/").Body
	if strings.Contains(mid, "data-color-toggle") || strings.Contains(mid, "colormode.js") || strings.Contains(mid, "data-color-mode") {
		t.Error("midnight is dark already: no switch")
	}
	if css := anon.get("/site.css").Body; strings.Contains(css, "data-theme") {
		t.Error("midnight served dark-mode CSS")
	}
	if body := admin.get("/admin/appearance").Body; !strings.Contains(body, "already dark") || strings.Contains(body, `name="color_mode"`) {
		t.Error("the admin should explain why there is no dark mode option for Midnight")
	}
	admin.post("/admin/appearance/theme", url.Values{"theme": {"default"}})

	// who can change it, and what values are accepted
	for _, bad := range []string{"", "dark", "on", "toggle; drop", "AUTO"} {
		if r := admin.post("/admin/appearance/colormode", url.Values{"color_mode": {bad}}); r.Status != 400 {
			t.Errorf("%q accepted: %d", bad, r.Status)
		}
	}
	if r := editor.post("/admin/appearance/colormode", url.Values{"color_mode": {"auto"}}); r.Status != 403 {
		t.Errorf("an editor changed dark mode: %d", r.Status)
	}
	if r := anon.post("/admin/appearance/colormode", url.Values{"color_mode": {"auto"}}); !strings.Contains(r.Location(), "/admin/login") {
		t.Errorf("a visitor should be sent to the login page, got %d %q", r.Status, r.Location())
	}
	if got := e.sql(`SELECT color_mode FROM site_settings`); got != "toggle" {
		t.Errorf("stored mode %q", got)
	}
	form := admin.get("/admin/appearance").Body
	if !strings.Contains(form, `name="color_mode" value="toggle" checked`) || !strings.Contains(form, `name="color_mode" value="auto"`) {
		t.Error("the admin form should show the current choice")
	}
}

func TestDarkModeKeepsBrandColourNotBackground(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	// the owner picks a cream background and a red brand colour in the token editor
	r := admin.post("/admin/appearance/tokens", url.Values{"t.color-bg": {"#fff4e0"}, "t.color-primary": {"#c0392b"}})
	if r.Status != 303 {
		t.Fatalf("tokens: %d", r.Status)
	}
	css := e.client().get("/site.css").Body
	i := strings.Index(css, `:root[data-theme="dark"]`)
	if i < 0 {
		t.Fatalf("no dark block:\n%s", css)
	}
	light, dark := css[:i], css[i:]
	dark = dark[:strings.Index(dark, "}")+1] // the dark palette itself; hand-coloured blocks are covered separately
	if !strings.Contains(light, "--color-bg: #fff4e0;") || !strings.Contains(dark, "--color-primary: #c0392b;") {
		t.Errorf("owner colours:\n%s", css)
	}
	if strings.Contains(dark, "#fff4e0") || !strings.Contains(dark, "--color-bg: #12151c;") {
		t.Errorf("a cream background must not carry into dark mode:\n%s", dark)
	}
}

// Blocks the owner coloured by hand must stay readable when the page around them goes dark.
func TestDarkModeHandColouredBlocks(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()

	id := newPage(t, admin, "Colours")
	for _, typ := range []string{"richtext", "richtext", "richtext"} {
		admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {typ}})
	}
	rows, err := e.pool.Query(t.Context(), `SELECT id FROM blocks WHERE page_id = $1 ORDER BY position`, id)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var n int64
		rows.Scan(&n)
		ids = append(ids, fmt.Sprint(n))
	}
	rows.Close()
	admin.post("/admin/blocks/"+ids[0], url.Values{"p.content": {"plain"}})
	admin.post("/admin/blocks/"+ids[1], url.Values{"p.content": {"cream"}, "s.background": {"#fff4e0"}})
	admin.post("/admin/blocks/"+ids[2], url.Values{"p.content": {"red text"}, "s.text": {"#aa0000"}})
	savePage(t, admin, id, url.Values{"title": {"Colours"}, "slug": {"colours"}})

	body := anon.get("/colours").Body
	section := func(n string) string {
		return regexp.MustCompile(`<section id="block-` + n + `" class="([^"]*)"`).FindStringSubmatch(body)[1]
	}
	if c := section(ids[0]); strings.Contains(c, "pinned") || strings.Contains(c, "text-only") {
		t.Errorf("an uncoloured block must follow the theme: %q", c)
	}
	if c := section(ids[1]); !strings.Contains(c, "pinned") {
		t.Errorf("a block with its own background is pinned: %q", c)
	}
	if c := section(ids[2]); !strings.Contains(c, "text-only") {
		t.Errorf("a block with only a text colour is marked: %q", c)
	}
}
