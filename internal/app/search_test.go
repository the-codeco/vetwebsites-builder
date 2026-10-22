package app_test

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// textPage creates a page with one rich-text block and returns the page and block ids.
func textPage(t *testing.T, e *testEnv, admin *client, title, slug, text string, extra url.Values) (pageID, blockID string) {
	t.Helper()
	pageID = newPage(t, admin, title)
	if r := admin.post("/admin/pages/"+pageID+"/blocks", url.Values{"type": {"richtext"}}); r.Status != 200 {
		t.Fatalf("add block: %d", r.Status)
	}
	blockID = e.blockID(pageID, "richtext")
	if r := admin.post("/admin/blocks/"+blockID, url.Values{"p.content": {text}}); r.Status != 200 {
		t.Fatalf("save block: %d", r.Status)
	}
	v := url.Values{"title": {title}, "slug": {slug}}
	for k, vals := range extra {
		v[k] = vals
	}
	if r := savePage(t, admin, pageID, v); r.Status != 303 {
		t.Fatalf("save page: %d %s", r.Status, firstLine(r.Body))
	}
	return pageID, blockID
}

func publishedPost(t *testing.T, admin *client, title, slug, body string, extra url.Values) {
	t.Helper()
	r := admin.post("/admin/posts", url.Values{"title": {title}})
	id := idFromLocation(t, r.Location())
	v := url.Values{"title": {title}, "slug": {slug}, "status": {"published"}, "body": {body}}
	for k, vals := range extra {
		v[k] = vals
	}
	if r := admin.post("/admin/posts/"+id, v); r.Status != 303 {
		t.Fatalf("save post %s: %d", slug, r.Status)
	}
}

var reResultLinks = regexp.MustCompile(`class="search-title" href="([^"]+)"`)

func resultPaths(body string) []string {
	var out []string
	for _, m := range reResultLinks.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestSearchFindsPagesAndPosts(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()

	textPage(t, e, admin, "Pricing", "pricing", "Our wholesale rates include free delivery for large orders.", nil)
	textPage(t, e, admin, "Orchids", "orchids", "A guide to the care of orchids.", nil)
	textPage(t, e, admin, "Notes", "notes", "We sometimes mention orchids in passing here.", nil)
	publishedPost(t, admin, "Gardening tips", "gardening-tips", "<p>Tomatoes love <b>sunshine</b> and regular watering.</p>", nil)
	// things that must never appear
	textPage(t, e, admin, "Secret plans", "secret-plans", "The zebraword launch is confidential.", url.Values{"status": {"draft"}})
	textPage(t, e, admin, "Hidden", "hidden", "A zebraword page that asks not to be listed.", url.Values{"seo_noindex": {"on"}})
	publishedPost(t, admin, "Hidden post", "hidden-post", "<p>zebraword here too</p>", url.Values{"seo_noindex": {"on"}})
	publishedPost(t, admin, "Draft post", "draft-post", "<p>zebraword draft</p>", url.Values{"status": {"draft"}})

	// words inside a block are found, with the match highlighted in a snippet
	r := anon.get("/search?q=wholesale")
	paths := resultPaths(r.Body)
	if r.Status != 200 || len(paths) != 1 || paths[0] != "/pricing" {
		t.Fatalf("wholesale: %d %v", r.Status, paths)
	}
	if !strings.Contains(r.Body, "<mark>wholesale</mark>") || !strings.Contains(r.Body, `<span class="search-kind">Page</span>`) || !strings.Contains(r.Body, "1 result for") {
		t.Errorf("snippet or count missing:\n%s", r.Body)
	}

	// stemming: "tomato" finds "Tomatoes"; post bodies are searched without their HTML tags
	r = anon.get("/search?q=tomato")
	if paths := resultPaths(r.Body); len(paths) != 1 || paths[0] != "/blog/gardening-tips" || !strings.Contains(r.Body, `<span class="search-kind">Post</span>`) {
		t.Errorf("tomato: %v", paths)
	}
	if got := resultPaths(anon.get("/search?q=sunshine+watering").Body); len(got) != 1 {
		t.Errorf("several words: %v", got)
	}
	if got := resultPaths(anon.get("/search?q=b").Body); len(got) != 0 {
		t.Errorf("a post's <b> tag must not be searchable: %v", got)
	}

	// a title match outranks a passing mention
	paths = resultPaths(anon.get("/search?q=orchids").Body)
	if len(paths) != 2 || paths[0] != "/orchids" {
		t.Errorf("ranking: %v", paths)
	}

	// drafts, pages marked "don't list" and unpublished posts stay out
	r = anon.get("/search?q=zebraword")
	if got := resultPaths(r.Body); len(got) != 0 || !strings.Contains(r.Body, "Nothing found") {
		t.Errorf("hidden content leaked into search: %v", got)
	}

	// starter pages are searchable straight after startup ("admin area" is in the About page)
	if got := resultPaths(anon.get("/search?q=admin").Body); !has(got, "/about") {
		t.Errorf("starter content not indexed: %v", got)
	}

	// results are never cached or indexed
	r = anon.get("/search?q=wholesale")
	if !strings.Contains(r.Header.Get("Cache-Control"), "no-store") || !strings.Contains(r.Body, `content="noindex, nofollow"`) || strings.Contains(r.Body, `rel="canonical"`) {
		t.Error("search results must be no-store and noindex")
	}
	if !strings.Contains(anon.get("/robots.txt").Body, "Disallow: /search") {
		t.Error("robots.txt should keep crawlers off /search")
	}
}

func TestSearchFollowsContentChanges(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	count := func(q string) int { return len(resultPaths(anon.get("/search?q=" + url.QueryEscape(q)).Body)) }

	pageID, blk := textPage(t, e, admin, "Moving", "moving", "Originalword lives here.", nil)
	if count("originalword") != 1 {
		t.Fatal("new page not searchable")
	}
	admin.post("/admin/blocks/"+blk, url.Values{"p.content": {"Replacementword lives here."}})
	if count("originalword") != 0 || count("replacementword") != 1 {
		t.Error("editing a block must update the index")
	}
	// a second block is searched too, and deleting a block removes its words
	admin.post("/admin/pages/"+pageID+"/blocks", url.Values{"type": {"hero"}})
	hero := e.blockID(pageID, "hero")
	admin.post("/admin/blocks/"+hero, url.Values{"p.heading": {"Heroword banner"}})
	if count("heroword") != 1 {
		t.Error("a hero heading should be searchable")
	}
	admin.post("/admin/blocks/"+hero+"/delete", nil)
	if count("heroword") != 0 {
		t.Error("deleting a block must remove its words")
	}
	// unpublishing and renaming
	savePage(t, admin, pageID, url.Values{"title": {"Moving"}, "slug": {"moving"}, "status": {"draft"}})
	if count("replacementword") != 0 {
		t.Error("an unpublished page is still searchable")
	}
	savePage(t, admin, pageID, url.Values{"title": {"Titleword here"}, "slug": {"moving"}})
	if count("titleword") != 1 {
		t.Error("a page's title is searchable")
	}
	admin.post("/admin/pages/"+pageID+"/delete", nil)
	if count("titleword") != 0 {
		t.Error("a deleted page is still searchable")
	}
}

func TestSearchEscapesEverything(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	pid := newPage(t, admin, "Markup")
	admin.post("/admin/pages/"+pid+"/blocks", url.Values{"type": {"hero"}})
	admin.post("/admin/blocks/"+e.blockID(pid, "hero"), url.Values{"p.heading": {`Evilword <script>alert(1)</script> <mark>fake</mark> <img src=x onerror=alert(2)>`}})
	savePage(t, admin, pid, url.Values{"title": {"Markup"}, "slug": {"markup"}})

	r := anon.get("/search?q=evilword")
	if len(resultPaths(r.Body)) != 1 {
		t.Fatalf("not found: %v", resultPaths(r.Body))
	}
	for _, bad := range []string{"<script>alert", "<img src=x", "<mark>fake</mark>"} {
		if strings.Contains(r.Body, bad) {
			t.Errorf("page text reached the results unescaped: %s", bad)
		}
	}
	// the database drops well-formed tags from a snippet and anything left is escaped; either way the
	// only markup in it is the highlight on the real match
	snippet := regexp.MustCompile(`(?s)<p class="search-snippet">(.*?)</p>`).FindStringSubmatch(r.Body)
	if snippet == nil || !strings.Contains(snippet[1], "<mark>Evilword</mark>") || strings.Count(snippet[1], "<mark>") != 1 || strings.Contains(snippet[1], "<script") || strings.Contains(snippet[1], "<img") {
		t.Errorf("snippet: %v", snippet)
	}

	// the query is shown back to the visitor, so it must be escaped too
	q := `"><script>alert(1)</script>`
	r = anon.get("/search?q=" + url.QueryEscape(q))
	if strings.Contains(r.Body, "<script>alert") || strings.Contains(r.Body, `value=""><script`) {
		t.Error("the query was reflected unescaped")
	}
	if extra := foreignScripts(r.Body); len(extra) != 0 {
		t.Errorf("scripts on the results page: %v", extra)
	}
}

func TestSearchOddInput(t *testing.T) {
	e := newEnv(t)
	e.newAdmin("admin@example.com")
	anon := e.client()
	for name, q := range map[string]string{
		"empty": "", "spaces": "   ", "stop words only": "the and of", "unbalanced quote": `"half open`, "dash": "-", "operators": "or or and",
		"symbols": "!@#$%^&*()", "control characters": "a\x00b\x07c", "very long": strings.Repeat("word ", 200), "unicode": "naïve café 日本語",
		"sql-ish": "'; DROP TABLE pages; --",
	} {
		r := anon.get("/search?q=" + url.QueryEscape(q))
		if r.Status != 200 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if r := anon.get("/search"); r.Status != 200 || !strings.Contains(r.Body, "Type a word or phrase") {
		t.Errorf("empty search: %d", r.Status)
	}
	if e.sql(`SELECT count(*) FROM pages`) == "0" {
		t.Error("pages vanished")
	}
	// the typed query is capped
	r := anon.get("/search?q=" + strings.Repeat("a", 500))
	if m := regexp.MustCompile(`id="search-q" type="search" name="q" value="(a+)"`).FindStringSubmatch(r.Body); m == nil || len(m[1]) != 100 {
		t.Errorf("long query not capped at 100 characters")
	}
}

func TestSearchPaging(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	for i := 1; i <= 12; i++ {
		publishedPost(t, admin, fmt.Sprintf("Entry %02d", i), fmt.Sprintf("entry-%02d", i), "<p>Pagewordx content.</p>", nil)
	}
	p1 := anon.get("/search?q=pagewordx")
	if got := resultPaths(p1.Body); len(got) != 10 || !strings.Contains(p1.Body, "12 results for") || !strings.Contains(p1.Body, "Page 1 of 2") {
		t.Fatalf("page 1: %d results", len(resultPaths(p1.Body)))
	}
	if !strings.Contains(p1.Body, `href="/search?page=2&amp;q=pagewordx" rel="next"`) || strings.Contains(p1.Body, `rel="prev"`) {
		t.Error("page 1 links")
	}
	p2 := anon.get("/search?q=pagewordx&page=2")
	if got := resultPaths(p2.Body); len(got) != 2 || !strings.Contains(p2.Body, `rel="prev"`) || strings.Contains(p2.Body, `rel="next"`) {
		t.Errorf("page 2: %v", got)
	}
	seen := map[string]bool{}
	for _, p := range append(resultPaths(p1.Body), resultPaths(p2.Body)...) {
		if seen[p] {
			t.Errorf("%s appears on both pages", p)
		}
		seen[p] = true
	}
	if r := anon.get("/search?q=pagewordx&page=3"); r.Status != 404 {
		t.Errorf("past the end: %d", r.Status)
	}
	if r := anon.get("/search?q=pagewordx&page=abc"); r.Status != 200 {
		t.Errorf("junk page number: %d", r.Status)
	}
}

func TestSearchRateLimit(t *testing.T) {
	e := newEnv(t)
	e.newAdmin("admin@example.com")
	anon := e.client()
	for i := 1; i <= 30; i++ {
		if r := anon.get(fmt.Sprintf("/search?q=term%d", i)); r.Status != 200 {
			t.Fatalf("search %d: %d", i, r.Status)
		}
	}
	r := anon.get("/search?q=onemore")
	if r.Status != 429 || !strings.Contains(r.Body, "searched a lot") {
		t.Errorf("the 31st search in a minute: %d", r.Status)
	}
	if anon.get("/").Status != 200 || anon.get("/search").Status != 200 {
		t.Error("the limit must only affect running searches")
	}
}

func TestSearchBoxInHeader(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	for _, theme := range []string{"default", "journal", "midnight"} {
		admin.post("/admin/appearance/theme", url.Values{"theme": {theme}})
		for _, p := range []string{"/", "/about", "/blog", "/search?q=x"} {
			body := anon.get(p).Body
			for _, want := range []string{`<form class="site-search" role="search" action="/search" method="get">`, `name="q"`, `aria-label="Search"`} {
				if !strings.Contains(body, want) {
					t.Errorf("%s %s: lacks %s", theme, p, want)
				}
			}
			if extra := foreignScripts(body); len(extra) != 0 {
				t.Errorf("%s %s: scripts: %v", theme, p, extra)
			}
		}
	}

	if r := editor.post("/admin/appearance/search", url.Values{}); r.Status != 403 {
		t.Errorf("an editor changed the search setting: %d", r.Status)
	}
	if !strings.Contains(admin.get("/admin/appearance").Body, `name="show_search" checked`) {
		t.Error("the setting should start on")
	}
	if r := admin.post("/admin/appearance/search", url.Values{}); r.Status != 303 {
		t.Fatalf("turn off: %d", r.Status)
	}
	for _, p := range []string{"/", "/blog", "/search"} {
		if strings.Contains(anon.get(p).Body, `class="site-search"`) {
			t.Errorf("%s: the header box should be gone", p)
		}
	}
	if r := anon.get("/search?q=admin"); r.Status != 200 || len(resultPaths(r.Body)) == 0 {
		t.Error("searching still works when the box is hidden")
	}
	admin.post("/admin/appearance/search", url.Values{"show_search": {"on"}})
	if !strings.Contains(anon.get("/").Body, `class="site-search"`) {
		t.Error("turning it back on")
	}
}

func TestSearchSlugIsReserved(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	if r := admin.post("/admin/pages", url.Values{"title": {"Search"}}); r.Status != 400 {
		t.Errorf("a page called /search would be hidden by the search route: %d", r.Status)
	}
	_ = e
}
