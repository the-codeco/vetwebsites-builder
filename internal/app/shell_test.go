package app_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var (
	reSidebar     = regexp.MustCompile(`(?s)<aside class="sidebar".*?</aside>`)
	reNavCurrent  = regexp.MustCompile(`<a class="(?:nav-link|me-link)" href="([^"]+)"[^>]*aria-current="page"`)
	reNavLinks    = regexp.MustCompile(`<a class="nav-link" href="([^"]+)"`)
	reIconUse     = regexp.MustCompile(`<use href="#(i-[a-z-]+)"`)
	reIconSymbol  = regexp.MustCompile(`<symbol id="(i-[a-z-]+)"`)
	reStat        = regexp.MustCompile(`<span class="stat-num">(\d+)</span>\s*<span class="stat-label">([^<]+)</span>\s*<span class="stat-sub">([^<]*)</span>`)
	reRecentTitle = regexp.MustCompile(`(?s)<ul class="recent">(.*?)</ul>`)
)

func currentNav(t *testing.T, body string) []string {
	t.Helper()
	side := reSidebar.FindString(body)
	if side == "" {
		t.Fatal("no sidebar on the page")
	}
	var out []string
	for _, m := range reNavCurrent.FindAllStringSubmatch(side, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestSidebarHighlightsTheCurrentSection(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	pageID := idFromLocation(t, admin.post("/admin/pages", url.Values{"title": {"Highlight Me"}}).Location())
	postID := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {"A Post"}}).Location())

	cases := map[string]string{
		"/admin/":                "/admin/",
		"/admin/pages":           "/admin/pages",
		"/admin/pages/" + pageID: "/admin/pages", // editing a page is still "Pages"
		"/admin/posts":           "/admin/posts",
		"/admin/posts/" + postID: "/admin/posts",
		"/admin/categories":      "/admin/posts", // categories belong to the blog
		"/admin/media":           "/admin/media",
		"/admin/menus":           "/admin/menus",
		"/admin/appearance":      "/admin/appearance",
		"/admin/domain":          "/admin/domain",
		"/admin/seo":             "/admin/seo",
		"/admin/analytics":       "/admin/analytics",
		"/admin/messages":        "/admin/messages",
		"/admin/subscribers":     "/admin/subscribers",
		"/admin/users":           "/admin/users",
		"/admin/account":         "/admin/account",
	}
	for path, want := range cases {
		r := admin.get(path)
		if r.Status != 200 {
			t.Errorf("%s: %d", path, r.Status)
			continue
		}
		got := currentNav(t, r.Body)
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s: highlighted %v, want exactly [%s]", path, got, want)
		}
	}

	// error screens re-render the same section, so the highlight survives a failed save
	r := admin.post("/admin/domain", url.Values{"domain": {"bad domain"}})
	if got := currentNav(t, r.Body); r.Status != 400 || len(got) != 1 || got[0] != "/admin/domain" {
		t.Errorf("after a failed save: %d %v", r.Status, got)
	}
}

func TestSidebarShowsOnlyWhatTheRoleMayUse(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "editor@example.com", "editor")

	links := func(body string) map[string]bool {
		m := map[string]bool{}
		for _, l := range reNavLinks.FindAllStringSubmatch(reSidebar.FindString(body), -1) {
			m[l[1]] = true
		}
		return m
	}
	a, ed := links(admin.get("/admin/").Body), links(editor.get("/admin/").Body)
	for _, p := range []string{"/admin/", "/admin/pages", "/admin/posts", "/admin/media", "/admin/menus", "/admin/appearance", "/admin/seo", "/admin/subscribers", "/admin/domain", "/admin/users"} {
		if !a[p] {
			t.Errorf("admin sidebar lacks %s", p)
		}
	}
	for _, p := range []string{"/admin/", "/admin/analytics", "/admin/messages", "/admin/pages", "/admin/posts", "/admin/media", "/admin/menus"} {
		if !ed[p] {
			t.Errorf("editor sidebar lacks %s", p)
		}
	}
	for _, p := range []string{"/admin/appearance", "/admin/seo", "/admin/subscribers", "/admin/domain", "/admin/users"} {
		if ed[p] {
			t.Errorf("editor sees %s, which they may not open", p)
		}
	}
	if body := editor.get("/admin/").Body; strings.Contains(body, `class="nav-group">Site`) || strings.Contains(body, `href="/admin/users"`) {
		t.Error("the Site group and the team stat must be hidden from editors")
	}

	// the account block shows who is signed in
	side := reSidebar.FindString(admin.get("/admin/").Body)
	for _, want := range []string{`<span class="avatar" aria-hidden="true">A</span>`, "admin@example.com", "<small>admin</small>", `action="/admin/logout"`, `aria-label="Log out"`, `target="_blank" rel="noopener"`} {
		if !strings.Contains(side, want) {
			t.Errorf("account block lacks %q", want)
		}
	}
	if !strings.Contains(reSidebar.FindString(editor.get("/admin/").Body), "<small>editor</small>") {
		t.Error("an editor's role should be shown")
	}
}

func TestShellStructureAndAccessibility(t *testing.T) {
	e := newEnv(t)
	anon := e.client()

	// the first-run screen has no sidebar: just a centred card under the brand
	setup := anon.get("/setup").Body
	for _, want := range []string{`<body class="auth"`, `class="auth-brand"`, `<main id="main" class="auth-main">`} {
		if !strings.Contains(setup, want) {
			t.Errorf("setup page lacks %q", want)
		}
	}
	for _, bad := range []string{`class="sidebar"`, `class="topbar"`, `data-sidebar-open`} {
		if strings.Contains(setup, bad) {
			t.Errorf("setup page has app chrome %q", bad)
		}
	}

	admin := e.newAdmin("admin@example.com")
	login := e.client().get("/admin/login").Body
	if strings.Contains(login, `class="sidebar"`) || !strings.Contains(login, `<body class="auth"`) {
		t.Error("the login page must use the plain centred layout")
	}

	page := admin.get("/admin/").Body
	for _, want := range []string{
		`<body class="app"`, `class="skip" href="#main"`, `<main id="main" tabindex="-1">`, `<aside class="sidebar" id="sidebar"`,
		`<nav class="side-nav" aria-label="Admin">`, `<header class="topbar">`, `class="topbar-title">Dashboard<`,
		// the drawer and collapse controls the script wires up, with accessible names
		`data-sidebar-open aria-controls="sidebar" aria-expanded="false" aria-label="Open menu"`, `data-sidebar-collapse aria-label="Collapse sidebar"`,
		`data-sidebar-close aria-label="Close menu"`, `class="scrim" data-sidebar-close`,
		`<meta name="color-scheme" content="light dark">`, `src="/static/admin.js"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("dashboard lacks %q", want)
		}
	}
	// pages that edit with a live preview use the full-width main
	pageID := idFromLocation(t, admin.post("/admin/pages", url.Values{"title": {"Wide"}}).Location())
	if !strings.Contains(admin.get("/admin/pages/"+pageID).Body, `<main id="main" tabindex="-1" class="wide">`) {
		t.Error("the page editor should use the wide layout")
	}

	// alerts: success and error, each with an icon and the right ARIA role
	if r := admin.post("/admin/domain", url.Values{"domain": {"bad domain"}}); !strings.Contains(r.Body, `class="alert error" role="alert"`) {
		t.Error("error alert markup")
	}
	if r := admin.get("/admin/menus?saved=main"); !strings.Contains(r.Body, `class="alert notice" role="status"`) {
		t.Error("success alert markup")
	}
}

// Every icon a page asks for must exist in the inline sprite, or it silently renders as nothing.
func TestEveryIconUsedExists(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	pageID := idFromLocation(t, admin.post("/admin/pages", url.Values{"title": {"Icons"}}).Location())
	postID := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {"Icons"}}).Location())

	pages := []string{"/admin/", "/admin/pages", "/admin/pages/" + pageID, "/admin/posts", "/admin/posts/" + postID, "/admin/categories",
		"/admin/media", "/admin/menus", "/admin/appearance", "/admin/seo", "/admin/analytics", "/admin/messages", "/admin/subscribers", "/admin/domain", "/admin/users", "/admin/account", "/admin/login"}
	for _, p := range pages {
		body := admin.get(p).Body
		if p == "/admin/login" { // not signed in: the plain layout
			body = e.client().get(p).Body
		}
		defined := map[string]bool{}
		for _, m := range reIconSymbol.FindAllStringSubmatch(body, -1) {
			defined[m[1]] = true
		}
		if len(defined) < 15 {
			t.Errorf("%s: only %d icons in the sprite", p, len(defined))
		}
		for _, m := range reIconUse.FindAllStringSubmatch(body, -1) {
			if !defined[m[1]] {
				t.Errorf("%s: icon #%s is used but not defined", p, m[1])
			}
		}
	}
}

func TestDashboardShowsRealNumbers(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "editor@example.com", "editor")

	// the starter content: two published pages, nothing else
	stats := func(body string) map[string][2]string {
		m := map[string][2]string{}
		for _, s := range reStat.FindAllStringSubmatch(body, -1) {
			m[s[2]] = [2]string{s[1], s[3]}
		}
		return m
	}
	got := stats(admin.get("/admin/").Body)
	if got["Published pages"] != [2]string{"2", "No drafts"} || got["Published posts"] != [2]string{"0", "No drafts"} ||
		got["Images"] != [2]string{"0", "In your media library"} || got["Team members"][0] != "2" {
		t.Errorf("fresh site: %v", got)
	}

	// add content of every kind
	admin.post("/admin/pages", url.Values{"title": {"Work in progress"}}) // a draft page
	pid := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {"Hello Dashboard"}}).Location())
	publish(t, admin, pid, "Hello Dashboard", "hello-dashboard", "Body") // a published post
	admin.post("/admin/posts", url.Values{"title": {"Unfinished post"}})
	admin.post("/admin/posts", url.Values{"title": {"Another draft"}}) // two drafts vs one published, so swapped counts show up // a draft post
	if res := decodeUpload(t, admin.upload(upFile{"a.png", "image/png", pngImage(20, 20)}).Body); len(res.Uploaded) != 1 {
		t.Fatal("upload failed")
	}

	body := admin.get("/admin/").Body
	got = stats(body)
	if got["Published pages"] != [2]string{"2", "1 draft waiting"} || got["Published post"] != [2]string{"1", "2 drafts waiting"} ||
		got["Image"] != [2]string{"1", "In your media library"} || got["Team members"][0] != "2" {
		t.Errorf("after adding content: %v", got)
	}

	// recent lists: newest first, with status badges, linking to the editors
	lists := reRecentTitle.FindAllStringSubmatch(body, -1)
	if len(lists) != 2 {
		t.Fatalf("%d recent lists", len(lists))
	}
	pagesList, postsList := lists[0][1], lists[1][1]
	if strings.Index(pagesList, "Work in progress") < 0 || strings.Index(pagesList, "Work in progress") > strings.Index(pagesList, "About") {
		t.Errorf("the newest page should come first:\n%s", pagesList)
	}
	for _, want := range []string{`<span class="badge draft">draft</span>`, `<span class="badge published">published</span>`} {
		if !strings.Contains(pagesList+postsList, want) {
			t.Errorf("recent lists lack %q", want)
		}
	}
	if !strings.Contains(postsList, "Hello Dashboard") || !strings.Contains(postsList, "Unfinished post") || !strings.Contains(postsList, `href="/admin/posts/`) {
		t.Errorf("recent posts:\n%s", postsList)
	}

	// quick actions land on elements that exist
	for _, link := range []string{`href="/admin/pages#new"`, `href="/admin/posts#new"`, `href="/admin/media#upload"`} {
		if !strings.Contains(body, link) {
			t.Errorf("dashboard lacks %s", link)
		}
	}
	for path, id := range map[string]string{"/admin/pages": `id="new"`, "/admin/posts": `id="new"`, "/admin/media": `id="upload"`} {
		if !strings.Contains(admin.get(path).Body, id) {
			t.Errorf("%s lacks the %s anchor the dashboard links to", path, id)
		}
	}
	// the list screens put their main action in the top bar
	for path, want := range map[string]string{"/admin/pages": "New page", "/admin/posts": "New post", "/admin/media": "Upload"} {
		top := regexp.MustCompile(`(?s)<header class="topbar">.*?</header>`).FindString(admin.get(path).Body)
		if !strings.Contains(top, want) {
			t.Errorf("%s: top bar lacks %q", path, want)
		}
	}

	// editors don't see the team number or the admin shortcuts
	ed := editor.get("/admin/").Body
	if _, has := stats(ed)["Team members"]; has {
		t.Error("the team stat is for admins")
	}
	if strings.Contains(ed, `href="/admin/appearance"`) || strings.Contains(ed, `href="/admin/seo"`) || strings.Contains(ed, `href="/admin/domain"`) {
		t.Error("admin shortcuts shown to an editor")
	}
	if !strings.Contains(ed, `href="/admin/menus"`) {
		t.Error("menus are for editors too")
	}
}
