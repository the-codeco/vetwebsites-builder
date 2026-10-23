package app_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func saveMenuItems(t *testing.T, admin *client, section string, items []map[string]any) response {
	t.Helper()
	b, _ := json.Marshal(items)
	return admin.post("/admin/menus/"+section, url.Values{"structure": {string(b)}})
}

// The top bar can hold items on its left and its right, the social icons and the search icon; the search icon can
// also go in the main menu. Clicking it opens a popup, and without JavaScript it is a link to the search page.
func TestTopBarSidesSocialIconsAndSearchIcon(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()

	if r := admin.post("/admin/details", url.Values{
		"phone": {"011 646 4210"}, "social_facebook": {"https://www.facebook.com/example"}, "social_instagram": {"https://www.instagram.com/example"},
	}); r.Status != 303 {
		t.Fatalf("details: %d", r.Status)
	}

	// before anything is placed, the search icon sits in the header, and there is no top bar
	home := anon.get("/").Body
	if !strings.Contains(home, `<div class="site-search">`) || strings.Contains(home, `class="menu-topbar`) {
		t.Errorf("the default header:\n%s", home)
	}
	if !strings.Contains(home, `<dialog class="search-dialog" id="search-dialog"`) {
		t.Error("the popup should be on every page")
	}

	r := saveMenuItems(t, admin, "topbar", []map[string]any{
		{"kind": "link", "label": "Call us", "url": "tel:+27116464210", "side": "left"},
		{"kind": "social", "label": "", "side": "right"},
		{"kind": "search", "label": "", "side": "right"},
		{"kind": "link", "label": "Login", "url": "/login-info"},
	})
	if r.Status != 303 {
		t.Fatalf("save top bar: %d %s", r.Status, r.Body)
	}
	body := anon.get("/").Body
	left := between(body, `<ul class="menu-topbar-left">`, `</ul>`)
	right := between(body, `<ul class="menu-topbar-right">`, `</ul>`)
	if !strings.Contains(left, "Call us") || strings.Contains(left, "social-icons") || strings.Contains(left, "Login") {
		t.Errorf("left side: %s", left)
	}
	for _, want := range []string{`class="social-icons"`, `href="https://www.facebook.com/example"`, `aria-label="Facebook"`, `href="https://www.instagram.com/example"`, `aria-label="Instagram"`, `data-search-open`, "Login"} {
		if !strings.Contains(right, want) {
			t.Errorf("right side lacks %s: %s", want, right)
		}
	}
	if strings.Contains(right, "Call us") || strings.Contains(right, `rel="noopener noreferrer"`) == false {
		t.Errorf("right side: %s", right)
	}
	if strings.Contains(body, `<div class="site-search">`) {
		t.Error("the header icon must go once the owner has placed the search icon")
	}
	if !strings.Contains(body, `<a class="menu-search-btn" href="/search"`) {
		t.Error("the icon links to the search page for visitors without JavaScript")
	}

	// the search icon in the main menu instead
	saveMenuItems(t, admin, "topbar", []map[string]any{{"kind": "link", "label": "Call us", "url": "tel:+27116464210"}})
	if r := saveMenuItems(t, admin, "main", []map[string]any{{"kind": "link", "label": "Home", "url": "/"}, {"kind": "search", "label": ""}}); r.Status != 303 {
		t.Fatalf("save main: %d %s", r.Status, r.Body)
	}
	body = anon.get("/").Body
	if main := between(body, `<ul class="menu-list"`, `</ul>`); !strings.Contains(main, `menu-item menu-item-icon`) || !strings.Contains(main, `data-search-open`) {
		t.Errorf("main menu: %s", main)
	}
	if strings.Contains(body, `<div class="site-search">`) {
		t.Error("a search icon in the main menu replaces the header icon")
	}

	// the rules
	if r := saveMenuItems(t, admin, "footer", []map[string]any{{"kind": "search", "label": "x"}}); r.Status != 400 {
		t.Errorf("the footer takes links only: %d", r.Status)
	}
	if r := saveMenuItems(t, admin, "topbar", []map[string]any{{"kind": "dropdown", "label": "x", "children": []map[string]any{{"label": "a", "url": "/a"}}}}); r.Status != 400 {
		t.Errorf("no dropdowns in the top bar: %d", r.Status)
	}
	// sides are only kept where they mean something
	saveMenuItems(t, admin, "main", []map[string]any{{"kind": "link", "label": "Home", "url": "/", "side": "left"}})
	if got := e.sql(`SELECT items::text FROM menus WHERE location = 'main'`); strings.Contains(got, `"side"`) {
		t.Errorf("a side was stored in the main menu: %s", got)
	}

	// search switched off: no icon, no popup, whatever the menus say
	saveMenuItems(t, admin, "main", []map[string]any{{"kind": "search", "label": ""}})
	admin.post("/admin/appearance/search", url.Values{})
	off := anon.get("/").Body
	if strings.Contains(off, "data-search-open") || strings.Contains(off, `id="search-dialog"`) {
		t.Error("search is off: no icon and no popup")
	}

	// social icons with nothing to show are left out
	admin.post("/admin/details", url.Values{"phone": {"011 646 4210"}})
	saveMenuItems(t, admin, "topbar", []map[string]any{{"kind": "social", "label": ""}})
	if strings.Contains(anon.get("/").Body, "social-icons") {
		t.Error("no social links: no icons")
	}
}

func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	s = s[i+len(from):]
	if j := strings.Index(s, to); j >= 0 {
		return s[:j]
	}
	return s
}
