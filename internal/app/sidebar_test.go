package app_test

import (
	"net/url"
	"strings"
	"testing"
)

// newPublishedPage makes a published page and returns its id.
func newPublishedPage(t *testing.T, e *testEnv, admin *client, title, slug string) string {
	t.Helper()
	id := idFromLocation(t, admin.post("/admin/pages", url.Values{"title": {title}, "slug": {slug}}).Location())
	if r := admin.post("/admin/pages/"+id, url.Values{"title": {title}, "slug": {slug}, "status": {"published"}}); r.Status != 303 {
		t.Fatalf("publish %s: %d", slug, r.Status)
	}
	return id
}

// putUnder saves a page's section settings as the page settings form does.
func putUnder(admin *client, id, title, slug, parent, position string, sidebar bool) response {
	v := url.Values{"title": {title}, "slug": {slug}, "status": {"published"}, "section": {"1"}, "parent": {parent}, "position": {position}}
	if sidebar {
		v.Set("sidebar", "on")
	}
	return admin.post("/admin/pages/"+id, v)
}

func TestSidebarWithASectionMenu(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	// a section: Safety equipment, with three products in a set order
	top := newPublishedPage(t, e, admin, "Safety equipment", "safety-equipment")
	poles := newPublishedPage(t, e, admin, "Animal control poles", "poles")
	nets := newPublishedPage(t, e, admin, "Animal nets", "nets")
	muzzles := newPublishedPage(t, e, admin, "Safety muzzles", "muzzles")
	other := newPublishedPage(t, e, admin, "Contact us", "contact-us")
	for _, c := range []struct{ id, title, slug, pos string }{{poles, "Animal control poles", "poles", "1"}, {nets, "Animal nets", "nets", "3"}, {muzzles, "Safety muzzles", "muzzles", "2"}} {
		if r := putUnder(admin, c.id, c.title, c.slug, top, c.pos, true); r.Status != 303 {
			t.Fatalf("put %s under: %d", c.slug, r.Status)
		}
	}
	if got := e.sql(`SELECT string_agg(slug, ',' ORDER BY position) FROM pages WHERE parent_id = ` + top); got != "poles,muzzles,nets" {
		t.Fatalf("section: %s", got)
	}
	// the page editor offers the section settings; the pages list shows the section indented
	if body := admin.get("/admin/pages/" + poles).Body; !strings.Contains(body, `name="parent"`) || !strings.Contains(body, `<option value="`+top+`" selected>Safety equipment</option>`) || !strings.Contains(body, `name="sidebar" checked`) {
		t.Error("the page settings show the section")
	}
	if body := admin.get("/admin/pages/" + top).Body; !strings.Contains(body, "it is the top of a section") {
		t.Error("a section's top page says so instead of offering a parent")
	}
	list := admin.get("/admin/pages").Body
	if a, b := strings.Index(list, ">Safety equipment</a>"), strings.Index(list, `class="page-child"><span class="muted" aria-hidden="true">↳ </span><a href="/admin/pages/`+poles+`">`); a < 0 || b < a {
		t.Error("the pages list shows a section's pages under its top page")
	}

	// one level only, and never under itself; the home page never goes under a page
	home := e.sql(`SELECT id FROM pages WHERE slug = 'home'`)
	for name, r := range map[string]response{
		"under a page that is under a page": putUnder(admin, other, "Contact us", "contact-us", poles, "0", true),
		"a top page under another":          putUnder(admin, top, "Safety equipment", "safety-equipment", other, "0", true),
		"under itself":                      putUnder(admin, other, "Contact us", "contact-us", other, "0", true),
		"the home page":                     putUnder(admin, home, "Home", "home", other, "0", true),
		"a bad order":                       putUnder(admin, other, "Contact us", "contact-us", "0", "lots", true),
	} {
		if r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}

	// the sidebar: section menu, search, a linked picture, opening hours as text
	if body := admin.get("/admin/sidebar").Body; !strings.Contains(body, `data-sidebar-form`) || !strings.Contains(body, `id="tpl-fb-w-sectionmenu"`) || !strings.Contains(body, `<option value="search">Search box</option>`) {
		t.Error("the sidebar builder")
	}
	sidebar := `{"side":"left","width":"narrow","widgets":[
		{"type":"sectionmenu"},
		{"type":"search","heading":"Search the site"},
		{"type":"image","image":"/media/2026/10/dog.jpg","alt":"Our dog","url":"https://vetdirectory.example"},
		{"type":"text","heading":"Office hours","text":"<p>Mon–Thu 8:30–15:30</p>"}]}`
	if r := admin.post("/admin/sidebar", url.Values{"structure": {sidebar}}); r.Status != 303 {
		t.Fatalf("save sidebar: %d %s", r.Status, firstLine(r.Body))
	}
	for _, theme := range []string{"", "?theme=journal", "?theme=midnight"} {
		body := anon.get("/muzzles" + theme).Body
		for _, want := range []string{
			`<div class="with-sidebar sidebar-left sidebar-narrow"><main>`, `<aside class="site-sidebar" aria-label="Sidebar">`,
			`<h2><a href="/safety-equipment">Safety equipment</a></h2>`,
			`<li><a href="/poles">Animal control poles</a></li><li><a href="/muzzles" aria-current="page">Safety muzzles</a></li><li><a href="/nets">Animal nets</a></li>`,
			`<form class="sidebar-search search-form" role="search" action="/search" method="get">`,
			`<a href="https://vetdirectory.example" rel="noopener"><img src="/media/2026/10/dog.jpg"`, `alt="Our dog"`,
			`<h2>Office hours</h2><div class="prose"><p>Mon–Thu 8:30–15:30</p></div>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("theme %q: the sidebar lacks %s", theme, want)
			}
		}
		if strings.Index(body, "<main>") > strings.Index(body, `<aside class="site-sidebar"`) {
			t.Errorf("theme %q: the content comes before the sidebar in the page", theme)
		}
	}
	// the section's top page marks itself; a page in no section has no section menu, but the rest of the sidebar
	if body := anon.get("/safety-equipment").Body; !strings.Contains(body, `<h2><a href="/safety-equipment" aria-current="page">Safety equipment</a></h2>`) {
		t.Error("the top page is marked on its own page")
	}
	for _, p := range []string{"/contact-us", "/blog", "/search?q=x"} {
		body := anon.get(p).Body
		if !strings.Contains(body, `class="site-sidebar"`) || strings.Contains(body, "/poles") || !strings.Contains(body, "Office hours") {
			t.Errorf("%s: the sidebar without a section menu", p)
		}
	}
	// a page can turn it off
	if r := putUnder(admin, other, "Contact us", "contact-us", "0", "0", false); r.Status != 303 {
		t.Fatalf("turn off: %d", r.Status)
	}
	if body := anon.get("/contact-us").Body; strings.Contains(body, "site-sidebar") || !strings.Contains(body, "<main>") {
		t.Error("a page with the sidebar off has none")
	}
	// a form without the section fields leaves them alone
	admin.post("/admin/pages/"+poles, url.Values{"title": {"Animal control poles"}, "slug": {"poles"}, "status": {"published"}})
	if got := e.sql(`SELECT parent_id::text || '/' || sidebar::text FROM pages WHERE id = ` + poles); got != top+"/true" {
		t.Errorf("saving without the section fields changed them: %s", got)
	}
	// no widgets, no sidebar
	if r := admin.post("/admin/sidebar", url.Values{"structure": {`{"side":"right","widgets":[]}`}}); r.Status != 303 {
		t.Fatal("clear")
	}
	if body := anon.get("/poles").Body; strings.Contains(body, "with-sidebar") {
		t.Error("an empty sidebar is not drawn")
	}
	admin.post("/admin/sidebar", url.Values{"structure": {sidebar}})

	// limits and access
	before := e.sql(`SELECT sidebar::text FROM site_settings`)
	for name, s := range map[string]string{"not json": `x`, "unknown widget": `{"widgets":[{"type":"iframe"}]}`, "bad picture": `{"widgets":[{"type":"image","image":"javascript:alert(1)"}]}`} {
		if r := admin.post("/admin/sidebar", url.Values{"structure": {s}}); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if r := editor.post("/admin/sidebar", url.Values{"structure": {`{"widgets":[]}`}}); r.Status != 403 {
		t.Errorf("an editor cannot change the sidebar: %d", r.Status)
	}
	if e.sql(`SELECT sidebar::text FROM site_settings`) != before {
		t.Error("refused saves changed nothing")
	}

	// sections, the sidebar switch and the sidebar survive a backup restore
	snapshot := func() string {
		return e.sql(`SELECT string_agg(p.slug || ':' || COALESCE(t.slug, '-') || ':' || p.position || ':' || p.sidebar, ',' ORDER BY p.slug) FROM pages p LEFT JOIN pages t ON t.id = p.parent_id`) + "|" + e.sql(`SELECT sidebar::text FROM site_settings`)
	}
	want := snapshot()
	raw := []byte(admin.downloadBackup(false).Body)
	e.pool.Exec(t.Context(), `UPDATE pages SET parent_id = NULL, sidebar = true; UPDATE site_settings SET sidebar = '{}'`)
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if got := snapshot(); got != want {
		t.Errorf("after restore:\n%s\nwant\n%s", got, want)
	}
}
