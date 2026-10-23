package app_test

import (
	"net/url"
	"strings"
	"testing"
)

func TestPageLayoutAndHeaderStyles(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	// nothing chosen: the theme's own header, and no layout rules
	home := anon.get("/").Body
	if !strings.Contains(home, `<header class="site-header">`) || strings.Contains(home, "header-bar") {
		t.Error("the theme's own header by default")
	}
	if css := anon.get("/site.css").Body; strings.Contains(css, "page layout (Appearance)") {
		t.Error("no layout rules by default")
	}
	page := admin.get("/admin/appearance").Body
	for _, want := range []string{`action="/admin/appearance/layout"`, `action="/admin/appearance/header"`, `name="banner_6"`, `value="stacked"`, `value="centered"`} {
		if !strings.Contains(page, want) {
			t.Errorf("Appearance lacks %s", want)
		}
	}

	// a boxed page on grey, with the logo-and-banner header and a teal menu bar (animalhandling.co.za)
	if r := admin.post("/admin/appearance/layout", url.Values{"boxed": {"on"}, "width": {"980"}, "background": {"#BBBBBB"}, "image_mode": {"cover"}}); r.Status != 303 {
		t.Fatalf("save layout: %d", r.Status)
	}
	if r := admin.post("/admin/appearance/header", url.Values{"header_style": {"stacked"}, "tagline": {"Support systems"},
		"banner_1": {"/media/2026/10/a.jpg"}, "banner_2": {"/media/2026/10/b.jpg"}, "banner_place": {"beside"}, "banner_height": {"sm"},
		"bar_background": {"#003a40"}, "bar_text": {"#ffffff"}, "bar_active": {"#222222"}}); r.Status != 303 {
		t.Fatalf("save header: %d", r.Status)
	}
	css := anon.get("/site.css").Body
	for _, want := range []string{"html { background-color: #bbbbbb; }", "body { max-width: 980px;", "--bar-bg: #003a40", "--bar-fg: #ffffff", "--bar-active: #222222"} {
		if !strings.Contains(css, want) {
			t.Errorf("site.css lacks %s", want)
		}
	}
	for _, theme := range []string{"", "?theme=journal", "?theme=midnight"} {
		body := anon.get("/about" + theme).Body
		for _, want := range []string{
			`<header class="site-header header-stacked">`, `class="header-top banner-beside has-banner" style="--banner-h: 120px"`,
			`<p class="header-tagline">Support systems</p>`, `<div class="header-banner" data-rotate aria-hidden="true"><img src="/media/2026/10/a.jpg"`,
			`class="is-on"`, `<div class="header-bar"><div class="header-bar-inner">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("theme %q: header lacks %s", theme, want)
			}
		}
		if bar, menu := strings.Index(body, `<div class="header-bar-inner">`), strings.Index(body, `<nav class="menu-main"`); menu < bar {
			t.Errorf("theme %q: the menu is in the bar", theme)
		}
		if strings.Contains(body, "masthead") || strings.Count(body, `class="menu-main"`) != 1 {
			t.Errorf("theme %q: the theme's own header is replaced, not doubled", theme)
		}
	}

	// the centred style: the menu bar on top, the title below
	admin.post("/admin/appearance/header", url.Values{"header_style": {"centered"}, "tagline": {"Estd. 1964"}})
	body := anon.get("/").Body
	bar, brand := strings.Index(body, `<div class="header-bar">`), strings.Index(body, `<div class="header-brand header-brand-center">`)
	if bar < 0 || brand < bar || !strings.Contains(body, `<p class="header-tagline">Estd. 1964</p>`) || strings.Contains(body, "header-banner") {
		t.Error("centred: the bar, then the title and tagline, no banner")
	}

	// bad values are refused and change nothing
	before := e.sql(`SELECT layout::text FROM site_settings`)
	for name, c := range map[string]struct {
		path string
		v    url.Values
	}{
		"bad colour":   {"/admin/appearance/layout", url.Values{"background": {"red;}"}}},
		"bad picture":  {"/admin/appearance/layout", url.Values{"image": {"javascript:alert(1)"}}},
		"narrow box":   {"/admin/appearance/layout", url.Values{"boxed": {"on"}, "width": {"100"}}},
		"bad banner":   {"/admin/appearance/header", url.Values{"banner_1": {"javascript:alert(1)"}}},
		"bad bar":      {"/admin/appearance/header", url.Values{"bar_text": {"blue"}}},
		"long tagline": {"/admin/appearance/header", url.Values{"tagline": {strings.Repeat("t", 121)}}},
	} {
		if r := admin.post(c.path, c.v); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if r := editor.post("/admin/appearance/layout", url.Values{"boxed": {"on"}}); r.Status != 403 {
		t.Errorf("an editor cannot change the layout: %d", r.Status)
	}
	if e.sql(`SELECT layout::text FROM site_settings`) != before {
		t.Error("refused saves changed nothing")
	}

	// part of backups
	raw := []byte(admin.downloadBackup(false).Body)
	e.pool.Exec(t.Context(), `UPDATE site_settings SET layout = '{}'`)
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if after := e.sql(`SELECT layout::text FROM site_settings`); after != before {
		t.Errorf("restored layout:\n%s\n%s", before, after)
	}
}
