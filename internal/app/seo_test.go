package app_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var reLDJSONBody = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

// newPage creates a page through the admin and returns its id.
func newPage(t *testing.T, admin *client, title string) string {
	t.Helper()
	r := admin.post("/admin/pages", url.Values{"title": {title}})
	if r.Status != 303 {
		t.Fatalf("create page %q: %d", title, r.Status)
	}
	return idFromLocation(t, r.Location())
}

func savePage(t *testing.T, admin *client, id string, v url.Values) response {
	t.Helper()
	if v.Get("status") == "" {
		v.Set("status", "published")
	}
	return admin.post("/admin/pages/"+id, v)
}

type sitemapDoc struct {
	URLs []struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod"`
	} `xml:"url"`
}

func (s sitemapDoc) has(loc string) bool {
	for _, u := range s.URLs {
		if u.Loc == loc {
			return true
		}
	}
	return false
}

func getSitemap(t *testing.T, c *client) sitemapDoc {
	t.Helper()
	r := c.get("/sitemap.xml")
	if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/xml") {
		t.Fatalf("sitemap: %d %q", r.Status, r.Header.Get("Content-Type"))
	}
	var doc sitemapDoc
	if err := xml.Unmarshal([]byte(r.Body), &doc); err != nil {
		t.Fatalf("sitemap isn't valid XML: %v\n%s", err, r.Body)
	}
	return doc
}

func TestSEOTagsOnPages(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	base := e.srv.URL

	// the seeded home page already carries the basics
	home := anon.get("/")
	for _, want := range []string{
		`<link rel="canonical" href="` + base + `/">`, `<meta property="og:type" content="website">`,
		`<meta property="og:url" content="` + base + `/">`, `<meta name="twitter:card" content="summary">`, `"@type":"WebSite"`,
	} {
		if !strings.Contains(home.Body, want) {
			t.Errorf("home lacks %s", want)
		}
	}
	if strings.Contains(home.Body, `name="robots"`) {
		t.Error("an ordinary page must not carry a robots restriction")
	}

	// a page with its own settings, and hostile text in every field
	id := newPage(t, admin, "About Us")
	r := savePage(t, admin, id, url.Values{
		"title": {"About Us"}, "slug": {"about-us"},
		"seo_title":       {`Custom <b>Title</b> & "more"`},
		"seo_description": {`Desc </script><script>alert(1)</script> "quoted"`},
		"seo_image":       {"/media/2026/10/share.jpg"},
	})
	if r.Status != 303 {
		t.Fatalf("save: %d %s", r.Status, firstLine(r.Body))
	}
	page := anon.get("/about-us")
	for _, want := range []string{
		`<title>Custom &lt;b&gt;Title&lt;/b&gt; &amp; &#34;more&#34;</title>`,
		`<meta property="og:image" content="` + base + `/media/2026/10/share.jpg">`,
		`<meta name="twitter:image" content="` + base + `/media/2026/10/share.jpg">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<link rel="canonical" href="` + base + `/about-us">`,
		`<meta property="og:title" content="About Us">`,
	} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(page.Body, "<script>alert(1)") || strings.Contains(page.Body, "<b>Title") {
		t.Error("search text was not escaped")
	}
	if extra := foreignScripts(page.Body); len(extra) != 0 {
		t.Errorf("unexpected scripts: %v", extra)
	}

	// what was saved comes back in the editor, and the preview data is there
	ed := admin.get("/admin/pages/" + id).Body
	for _, want := range []string{`name="seo_title" value="Custom &lt;b&gt;Title&lt;/b&gt; &amp; &#34;more&#34;"`, `value="/media/2026/10/share.jpg"`, `data-seo`, `data-path="/about-us"`} {
		if !strings.Contains(ed, want) {
			t.Errorf("editor lacks %s", want)
		}
	}

	// validation: nothing invalid is stored, and the typed values survive the error
	for name, v := range map[string]url.Values{
		"image scheme":     {"seo_image": {"javascript:alert(1)"}},
		"long description": {"seo_description": {strings.Repeat("d", 301)}},
		"long title":       {"seo_title": {strings.Repeat("t", 121)}},
	} {
		v.Set("title", "About Us")
		v.Set("slug", "about-us")
		if r := savePage(t, admin, id, v); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if got := e.sql(`SELECT seo->>'image' FROM pages WHERE id = $1`, id); got != "/media/2026/10/share.jpg" {
		t.Errorf("a rejected save changed the stored image: %q", got)
	}
	if r := savePage(t, admin, id, url.Values{"title": {"About Us"}, "slug": {"about-us"}, "seo_title": {"Kept"}, "seo_image": {"nonsense"}}); !strings.Contains(r.Body, `name="seo_title" value="Kept"`) {
		t.Error("a failed save lost what was typed")
	}

	// clearing a field really clears it
	savePage(t, admin, id, url.Values{"title": {"About Us"}, "slug": {"about-us"}})
	if got := e.sql(`SELECT seo::text FROM pages WHERE id = $1`, id); got != "{}" {
		t.Errorf("empty settings should store nothing, got %s", got)
	}
	if strings.Contains(anon.get("/about-us").Body, "og:image") {
		t.Error("cleared image is still printed")
	}
}

func TestSEOHiddenPages(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	base := e.srv.URL

	secret := newPage(t, admin, "Secret")
	savePage(t, admin, secret, url.Values{"title": {"Secret"}, "slug": {"secret"}, "seo_noindex": {"on"}})
	draft := newPage(t, admin, "Draft Page")
	savePage(t, admin, draft, url.Values{"title": {"Draft Page"}, "slug": {"draft-page"}, "status": {"draft"}})
	open := newPage(t, admin, "Open Page")
	savePage(t, admin, open, url.Values{"title": {"Open Page"}, "slug": {"open-page"}})

	body := anon.get("/secret").Body
	if !strings.Contains(body, `<meta name="robots" content="noindex, follow">`) || strings.Contains(body, `rel="canonical"`) || strings.Contains(body, "ld+json") {
		t.Error("a noindex page must say so and must not advertise a canonical address or structured data")
	}
	if got := admin.get("/draft-page").Body; !strings.Contains(got, `content="noindex, nofollow"`) {
		t.Error("draft previews must never be indexed")
	}
	if got := admin.get("/open-page?theme=journal").Body; !strings.Contains(got, `content="noindex, nofollow"`) {
		t.Error("theme previews must never be indexed")
	}
	if got := anon.get("/does-not-exist"); got.Status != 404 || !strings.Contains(got.Body, `content="noindex, nofollow"`) {
		t.Errorf("404 pages must not be indexed: %d", got.Status)
	}

	sm := getSitemap(t, anon)
	for _, want := range []string{base + "/", base + "/open-page"} {
		if !sm.has(want) {
			t.Errorf("sitemap lacks %s", want)
		}
	}
	for _, not := range []string{base + "/secret", base + "/draft-page", base + "/home", base + "/blog"} {
		if sm.has(not) {
			t.Errorf("sitemap wrongly lists %s", not)
		}
	}
	if !strings.Contains(anon.get("/sitemap.xml").Header.Get("Cache-Control"), "max-age") {
		t.Error("sitemap should be cacheable briefly")
	}
}

func TestSEOPostsAndSitemap(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	base := e.srv.URL

	admin.post("/admin/categories", url.Values{"name": {"News"}})
	news := e.sql(`SELECT id FROM categories`)

	mk := func(title, slug string, extra url.Values) string {
		r := admin.post("/admin/posts", url.Values{"title": {title}})
		if r.Status != 303 {
			t.Fatalf("create %s: %d", title, r.Status)
		}
		id := idFromLocation(t, r.Location())
		v := url.Values{"title": {title}, "slug": {slug}, "status": {"published"}, "body": {"Some words for the post body."}}
		for k, vals := range extra {
			v[k] = vals
		}
		if r := admin.post("/admin/posts/"+id, v); r.Status != 303 {
			t.Fatalf("save %s: %d %s", title, r.Status, firstLine(r.Body))
		}
		return id
	}

	mk("Plain </script> & Post", "plain", url.Values{
		"excerpt": {"The excerpt"}, "cover": {"/media/2026/10/cover.jpg"}, "category": {news},
	})
	mk("Described", "described", url.Values{"seo_description": {"Own description"}, "seo_image": {"https://cdn.example.com/own.png"}})
	mk("Hidden Post", "hidden-post", url.Values{"seo_noindex": {"on"}})
	mk("Draft Post", "draft-post", url.Values{"status": {"draft"}})

	post := anon.get("/blog/plain").Body
	for _, want := range []string{
		`<meta property="og:type" content="article">`, `<meta property="article:published_time" content="`,
		`<meta name="description" content="The excerpt">`, `<meta property="og:image" content="` + base + `/media/2026/10/cover.jpg">`,
		`<link rel="canonical" href="` + base + `/blog/plain">`,
	} {
		if !strings.Contains(post, want) {
			t.Errorf("post lacks %s", want)
		}
	}
	m := reLDJSONBody.FindStringSubmatch(post)
	if m == nil {
		t.Fatal("post has no structured data")
	}
	if strings.Contains(m[1], "</script") {
		t.Fatalf("structured data breaks out of its script tag: %s", m[1])
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil || doc["@type"] != "BlogPosting" || doc["headline"] != "Plain </script> & Post" {
		t.Errorf("structured data: %v %v", doc, err)
	}

	desc := anon.get("/blog/described").Body
	if !strings.Contains(desc, `<meta name="description" content="Own description">`) || !strings.Contains(desc, `og:image" content="https://cdn.example.com/own.png"`) {
		t.Error("a post's own description and image must win")
	}
	if got := anon.get("/blog/hidden-post").Body; !strings.Contains(got, `noindex, follow`) {
		t.Error("hidden post not marked noindex")
	}

	sm := getSitemap(t, anon)
	for _, want := range []string{base + "/blog", base + "/blog/plain", base + "/blog/described", base + "/blog/category/news"} {
		if !sm.has(want) {
			t.Errorf("sitemap lacks %s", want)
		}
	}
	for _, not := range []string{base + "/blog/hidden-post", base + "/blog/draft-post"} {
		if sm.has(not) {
			t.Errorf("sitemap wrongly lists %s", not)
		}
	}
	for _, u := range sm.URLs {
		if u.LastMod == "" {
			t.Errorf("%s has no lastmod", u.Loc)
		}
	}

	// the blog list and a category carry their own tags
	if list := anon.get("/blog").Body; !strings.Contains(list, `<link rel="canonical" href="`+base+`/blog">`) || !strings.Contains(list, "<title>Blog · ") {
		t.Error("blog index tags")
	}
	if cat := anon.get("/blog/category/news").Body; !strings.Contains(cat, `href="`+base+`/blog/category/news"`) {
		t.Error("category canonical")
	}
}

func TestSEOSiteWideSettings(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	base := e.srv.URL

	if got := anon.get("/robots.txt"); got.Status != 200 || !strings.HasPrefix(got.Header.Get("Content-Type"), "text/plain") ||
		!strings.Contains(got.Body, "Disallow: /admin/") || !strings.Contains(got.Body, "Sitemap: "+base+"/sitemap.xml") {
		t.Errorf("robots.txt: %d %q", got.Status, got.Body)
	}

	// site defaults reach pages that have nothing of their own
	id := newPage(t, admin, "Plain Page")
	savePage(t, admin, id, url.Values{"title": {"Plain Page"}, "slug": {"plain-page"}})
	if r := admin.post("/admin/seo", url.Values{"seo_description": {"Site-wide words"}, "seo_image": {"/media/2026/10/site.jpg"}}); r.Status != 303 {
		t.Fatalf("save: %d", r.Status)
	}
	got := anon.get("/plain-page").Body
	if !strings.Contains(got, `<meta name="description" content="Site-wide words">`) || !strings.Contains(got, `og:image" content="`+base+`/media/2026/10/site.jpg"`) {
		t.Error("site defaults not applied")
	}
	if form := admin.get("/admin/seo").Body; !strings.Contains(form, "Site-wide words") || !strings.Contains(form, "/media/2026/10/site.jpg") {
		t.Error("saved defaults not shown")
	}

	// validation
	for name, v := range map[string]url.Values{
		"bad image":        {"seo_image": {"javascript:alert(1)"}},
		"long description": {"seo_description": {strings.Repeat("d", 301)}},
	} {
		if r := admin.post("/admin/seo", v); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}

	// hiding the whole site
	if r := admin.post("/admin/seo", url.Values{"seo_noindex": {"on"}}); r.Status != 303 {
		t.Fatalf("hide: %d", r.Status)
	}
	if got := anon.get("/robots.txt").Body; got != "User-agent: *\nDisallow: /\n" {
		t.Errorf("hidden robots.txt: %q", got)
	}
	if got := anon.get("/sitemap.xml"); got.Status != 404 {
		t.Errorf("a hidden site must not publish a sitemap: %d", got.Status)
	}
	for _, p := range []string{"/", "/plain-page", "/blog"} {
		body := anon.get(p).Body
		if !strings.Contains(body, `<meta name="robots" content="noindex, nofollow">`) || strings.Contains(body, `rel="canonical"`) {
			t.Errorf("%s: hidden site must be noindex without a canonical link", p)
		}
	}
	if !strings.Contains(admin.get("/admin/seo").Body, "no sitemap") {
		t.Error("the screen should explain that the sitemap is off")
	}
	admin.post("/admin/seo", url.Values{})
	if anon.get("/sitemap.xml").Status != 200 || strings.Contains(anon.get("/").Body, `name="robots"`) {
		t.Error("turning the setting off must restore normal behaviour")
	}
}

func TestSEOScreenAccessAndAudit(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	if r := anon.get("/admin/seo"); r.Status == 200 {
		t.Error("the SEO screen is open to visitors")
	}
	if r := editor.get("/admin/seo"); r.Status != 403 {
		t.Errorf("editors may not open the site-wide SEO screen: %d", r.Status)
	}
	if r := editor.post("/admin/seo", url.Values{"seo_noindex": {"on"}}); r.Status != 403 {
		t.Errorf("an editor hid the whole site: %d", r.Status)
	}
	if got := e.sql(`SELECT seo::text FROM site_settings`); got != "{}" {
		t.Errorf("settings changed by an editor: %s", got)
	}
	// ...but editors can do search settings on their own pages and posts
	pid := newPage(t, editor, "Editor Page")
	if r := savePage(t, editor, pid, url.Values{"title": {"Editor Page"}, "slug": {"editor-page"}, "seo_description": {"Mine"}, "seo_noindex": {"on"}}); r.Status != 303 {
		t.Errorf("editor page SEO: %d", r.Status)
	}

	// the audit lists what needs work and drops it once fixed
	id := newPage(t, admin, "Needs Work")
	savePage(t, admin, id, url.Values{"title": {"Needs Work"}, "slug": {"needs-work"}})
	screen := admin.get("/admin/seo").Body
	for _, want := range []string{"Needs Work", `data-issue="desc-missing"`, `data-issue="image-missing"`, `href="/admin/pages/` + id + `"`} {
		if !strings.Contains(screen, want) {
			t.Errorf("audit lacks %s", want)
		}
	}
	savePage(t, admin, id, url.Values{
		"title": {"Needs Work"}, "slug": {"needs-work"}, "seo_image": {"/media/2026/10/a.jpg"},
		"seo_description": {strings.Repeat("A good description of the page. ", 3)},
	})
	// the seeded pages have short descriptions, which the audit rightly points out
	if _, err := e.pool.Exec(context.Background(), `UPDATE pages SET seo = jsonb_build_object('description', repeat('A fine description. ', 5)) WHERE seo->>'noindex' IS NULL AND slug IN ('home', 'about')`); err != nil {
		t.Fatal(err)
	}
	admin.post("/admin/seo", url.Values{"seo_description": {strings.Repeat("A good site description. ", 4)}, "seo_image": {"/media/2026/10/a.jpg"}})
	after := admin.get("/admin/seo").Body
	if strings.Contains(after, "Needs Work") {
		t.Error("a fixed page is still listed")
	}
	if !strings.Contains(after, "have a good title") {
		t.Errorf("the all-clear message is missing once everything is fixed; rows left: %s", strings.Join(regexp.MustCompile(`data-issue="[^"]*"|<strong>[^<]*</strong>`).FindAllString(after, -1), " "))
	}

	// hidden pages are skipped, and counted
	savePage(t, admin, id, url.Values{"title": {"Needs Work"}, "slug": {"needs-work"}, "seo_noindex": {"on"}})
	if !strings.Contains(admin.get("/admin/seo").Body, "hidden on purpose") {
		t.Error("hidden pages should be mentioned")
	}
}
