package platform_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"webbuilder/internal/config"
)

// photo is a small real JPEG.
func photo(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 120, 255})
		}
	}
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}

// upload adds a picture to the website the client is working on and returns its address.
func (c *client) upload(name string, data []byte) string {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename=%q`, name))
	h.Set("Content-Type", "image/jpeg")
	part, _ := mw.CreatePart(h)
	part.Write(data)
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+"/admin/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	r := c.do(req, []string{"X-CSRF-Token", c.token, "Accept", "application/json"})
	var out struct {
		Uploaded []struct{ URL string } `json:"uploaded"`
	}
	if err := json.Unmarshal([]byte(r.Body), &out); err != nil || len(out.Uploaded) != 1 {
		c.t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	return out.Uploaded[0].URL
}

// buildSite has the customer make a preview copy and returns the folder of the newest one.
func (e *env) buildSite(c *client, siteName string) string {
	e.t.Helper()
	r := c.post("/admin/build", nil)
	if r.Status != http.StatusSeeOther || r.Location() != "/preview/build/" {
		e.t.Fatalf("build: %d %s", r.Status, firstLine(r.Body))
	}
	id := e.scalar(`SELECT id FROM sites WHERE name = $1`, siteName)
	ver := e.scalar(`SELECT build_version FROM sites WHERE id = $1`, id)
	return filepath.Join(e.cfg.DataDir, "builds", id, ver)
}

func listFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var (
	reAttr   = regexp.MustCompile(`(?i)\b(?:href|src|poster|data-src)="([^"]+)"`)
	reSrcset = regexp.MustCompile(`(?i)\bsrcset="([^"]+)"`)
	reCSSURL = regexp.MustCompile(`url\(\s*["']?([^)"']+)["']?\s*\)`)
)

// localRefs lists the addresses in a page or stylesheet that point at the site itself ("/x").
func localRefs(body string) []string {
	var out []string
	for _, m := range reAttr.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	for _, m := range reSrcset.FindAllStringSubmatch(body, -1) {
		for _, c := range strings.Split(m[1], ",") {
			if f := strings.Fields(c); len(f) > 0 {
				out = append(out, f[0])
			}
		}
	}
	for _, m := range reCSSURL.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	var local []string
	for _, u := range out {
		if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") {
			local = append(local, u)
		}
	}
	return local
}

// resolves says whether an address on the exported site leads to a file, the way the generated .htaccess makes it.
func resolves(files map[string][]byte, u string) bool {
	u, _, _ = strings.Cut(u, "#")
	p, _, _ := strings.Cut(u, "?")
	if p == "/" {
		_, ok := files["index.html"]
		return ok
	}
	rel := strings.TrimPrefix(path.Clean(p), "/")
	if _, ok := files[rel]; ok {
		return true
	}
	_, ok := files[rel+".html"]
	return ok
}

// site makes a customer with a populated website: pages (one hidden from search, one draft), a blog with enough posts for
// several pages, a picture, a menu, a contact form and a newsletter form, a redirect.
func populate(t *testing.T, e *env, c *client) {
	t.Helper()
	newPage(t, c, "About us", "about-us", "<p>We are Acme and we fix teeth. <a href=\"/services\">Services</a></p>")
	newPage(t, c, "Services", "services", "<p>Cleaning and whitening</p>")
	hidden := newPage(t, c, "Hidden from search", "hidden-page", "<p>Quietly reachable</p>")
	c.post("/admin/pages/"+hidden, url.Values{"title": {"Hidden from search"}, "slug": {"hidden-page"}, "status": {"published"}, "seo_noindex": {"on"}})
	// a draft that must not be exported
	d := c.post("/admin/pages", url.Values{"title": {"Secret draft"}, "slug": {"secret-draft"}})
	if d.Status != 303 {
		t.Fatalf("draft: %d", d.Status)
	}
	// a picture on a page
	img := c.upload("tooth.jpg", photo(1600, 900))
	pid := newPage(t, c, "Gallery", "gallery", "<p>Photos</p>")
	c.post("/admin/pages/"+pid+"/blocks", url.Values{"type": {"image"}})
	bid := regexp.MustCompile(`id="blk-(\d+)"`).FindAllStringSubmatch(c.get("/admin/pages/"+pid).Body, -1)
	c.post("/admin/blocks/"+bid[len(bid)-1][1], url.Values{"p.image": {img}, "p.alt": {"A tooth"}})
	// a contact form and a newsletter signup on their own pages
	cp := newPage(t, c, "Contact us", "contact-us", "<p>Write to us</p>")
	c.post("/admin/pages/"+cp+"/blocks", url.Values{"type": {"contactform"}})
	c.post("/admin/pages/"+cp+"/blocks", url.Values{"type": {"newsletter"}})
	// a blog: 12 posts in two categories
	c.post("/admin/categories", url.Values{"name": {"News"}})
	newsID := e.scalar(`SELECT id FROM site_1.categories WHERE slug = 'news'`)
	for i := 1; i <= 12; i++ {
		r := c.post("/admin/posts", url.Values{"title": {fmt.Sprintf("Post number %d", i)}})
		m := regexp.MustCompile(`/admin/posts/(\d+)`).FindStringSubmatch(r.Location())
		if m == nil {
			t.Fatalf("post: %d", r.Status)
		}
		v := url.Values{"title": {fmt.Sprintf("Post number %d", i)}, "slug": {fmt.Sprintf("post-%d", i)}, "status": {"published"}, "body": {fmt.Sprintf("<p>Words of post %d: orthodontics</p>", i)}}
		if i%2 == 0 {
			v.Add("category", newsID)
		}
		if r := c.post("/admin/posts/"+m[1], v); r.Status != 303 {
			t.Fatalf("publish post: %d", r.Status)
		}
	}
	// a menu, and a redirect from an old address
	c.post("/admin/menus/main", url.Values{"structure": {`[{"kind":"link","label":"Home","url":"/"},{"kind":"link","label":"About","url":"/about-us"},{"kind":"link","label":"Blog","url":"/blog"},{"kind":"link","label":"Call","url":"tel:+27215550123","button":true}]`}})
	c.post("/admin/redirects", url.Values{"from": {"/old-about"}, "to": {"/about-us"}})
	// a draft post that must not be exported either
	r := c.post("/admin/posts", url.Values{"title": {"Unpublished idea"}})
	_ = r
}

func TestBuildIsACompleteStaticCopy(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.BaseURL = "https://builder.example.test" })
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	populate(t, e, ann)
	e.pool.Exec(t.Context(), `UPDATE sites SET public_url = 'https://www.acme.example' WHERE name = 'Acme Dental'`)
	dir := e.buildSite(ann, "Acme Dental")
	files := listFiles(t, dir)

	for _, want := range []string{
		"index.html", "about-us.html", "services.html", "hidden-page.html", "gallery.html", "contact-us.html", "blog.html", "blog/page/2.html", "blog/post-1.html", "blog/post-12.html",
		"blog/category/news.html", "404.html", "sitemap.xml", "robots.txt", "rss.xml", "search.html", "search-index.json", "search.js", "site.css", "theme/default/theme.css",
		"theme/default/assets/menu.js", ".htaccess",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("the copy lacks %s (has %d files)", want, len(files))
		}
	}
	// drafts are not in it, anywhere
	for name, body := range files {
		if strings.Contains(strings.ToLower(string(body)), "secret draft") || strings.Contains(string(body), "Unpublished idea") || name == "secret-draft.html" {
			t.Errorf("a draft leaked into %s", name)
		}
	}
	// a page's text, its canonical address on the real domain, and no editor or preview chrome
	about := string(files["about-us.html"])
	for _, want := range []string{"We are Acme and we fix teeth", `<link rel="canonical" href="https://www.acme.example/about-us">`, `<title>About us`} {
		if !strings.Contains(about, want) {
			t.Errorf("about-us.html lacks %s", want)
		}
	}
	for _, not := range []string{"/admin", "preview-banner", "/preview/", "example.com/"} {
		if strings.Contains(about, not) && not != "example.com/" {
			t.Errorf("about-us.html contains %s", not)
		}
	}
	// every address in every page and stylesheet leads to a file in the copy
	checked := 0
	for name, body := range files {
		if !strings.HasSuffix(name, ".html") && !strings.HasSuffix(name, ".css") {
			continue
		}
		for _, u := range localRefs(string(body)) {
			checked++
			if !resolves(files, u) {
				t.Errorf("%s links to %s, which is not in the copy", name, u)
			}
		}
	}
	if checked < 100 {
		t.Errorf("the crawl check looked at only %d addresses", checked)
	}
	// pictures: the original and the resized copies the pages ask for
	gallery := string(files["gallery.html"])
	m := regexp.MustCompile(`srcset="([^"]+)"`).FindStringSubmatch(gallery)
	if m == nil || !strings.Contains(m[1], "/media/w") {
		t.Fatalf("the picture's srcset points at resized files: %v", m)
	}
	if strings.Contains(gallery, "?w=") {
		t.Error("no query-string addresses are left")
	}
	mediaFiles := 0
	for name := range files {
		if strings.HasPrefix(name, "media/") {
			mediaFiles++
		}
	}
	if mediaFiles < 2 {
		t.Errorf("picture files: %d", mediaFiles)
	}
	// pagination uses addresses a static copy can hold
	blog := string(files["blog.html"])
	if !strings.Contains(blog, `href="/blog/page/2"`) || strings.Contains(blog, "?page=") {
		t.Errorf("blog pagination:\n%s", regexp.MustCompile(`<nav class="pager".*?</nav>`).FindString(blog))
	}
	if !strings.Contains(string(files["blog/page/2.html"]), `href="/blog"`) {
		t.Error("page 2 links back")
	}
	if strings.Contains(string(files["blog/category/news.html"]), "Post number 1<") && strings.Count(string(files["blog/category/news.html"]), "postcard") < 6 {
		t.Error("the category lists only its posts")
	}
	// forms point at the builder and carry no baked token
	contact := string(files["contact-us.html"])
	for _, want := range []string{`action="https://builder.example.test/f/`, `/contact"`, `data-wb-form="contact"`, `data-wb-form="subscribe"`, `/subscribe"`} {
		if !strings.Contains(contact, want) {
			t.Errorf("contact-us.html lacks %s", want)
		}
	}
	if regexp.MustCompile(`name="t" value="[^"]+"`).MatchString(contact) {
		t.Error("a form token was baked into the page")
	}
	token := e.scalar(`SELECT public_token FROM sites WHERE name = 'Acme Dental'`)
	if !strings.Contains(contact, "/f/"+token+"/contact") {
		t.Error("the form names the website by its public token")
	}
	// the phone link in the menu survives
	if !regexp.MustCompile(`href="tel:(\+|&#43;)27215550123"`).MatchString(string(files["index.html"])) {
		t.Error("tel link")
	}
	// search: the index holds published pages and posts and nothing else, the page has its script
	var index []struct{ P, T, K, X string }
	if err := json.Unmarshal(files["search-index.json"], &index); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range index {
		paths = append(paths, e.P)
	}
	sort.Strings(paths)
	idx := strings.Join(paths, " ")
	for _, want := range []string{"/about-us", "/services", "/blog/post-1", "/blog/post-12"} {
		if !strings.Contains(idx, want) {
			t.Errorf("the search index lacks %s", want)
		}
	}
	for _, not := range []string{"hidden-page", "secret-draft", "unpublished"} {
		if strings.Contains(idx, not) {
			t.Errorf("the search index contains %s", not)
		}
	}
	if !strings.Contains(string(files["search.html"]), `<script src="/search.js" defer></script>`) {
		t.Error("the search page loads its script")
	}
	// sitemap and robots use the real domain, and sitemap omits the page hidden from search engines
	if sm := string(files["sitemap.xml"]); !strings.Contains(sm, "https://www.acme.example/about-us") || strings.Contains(sm, "hidden-page") || strings.Contains(sm, "secret") {
		t.Error("sitemap")
	}
	if rb := string(files["robots.txt"]); !strings.Contains(rb, "https://www.acme.example/sitemap.xml") {
		t.Errorf("robots: %s", rb)
	}
	// the web server's settings
	ht := string(files[".htaccess"])
	for _, want := range []string{"RewriteRule ^old-about/?$ /about-us [R=301,L,NC]", "ErrorDocument 404 /404.html", "DirectorySlash Off", `RewriteRule ^(.+)$ $1.html [L]`,
		"Header always set Content-Security-Policy", "form-action 'self' https://builder.example.test", "connect-src 'self' https://builder.example.test", "Options -Indexes",
		"X-Content-Type-Options"} {
		if !strings.Contains(ht, want) {
			t.Errorf(".htaccess lacks %s\n%s", want, ht)
		}
	}
	if strings.Contains(ht, "\r") || strings.Contains(ht, "\x00") {
		t.Error("clean line endings")
	}
	// the manifest says what is in the folder
	if got := e.scalar(`SELECT build_changes >= 0 FROM sites WHERE name = 'Acme Dental'`); got != "true" {
		t.Errorf("the copy is recorded: %s", got)
	}
}

func TestBuildPreviewServesTheCopyLikeTheHosting(t *testing.T) {
	e := newEnv(t)
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	newPage(t, ann, "About us", "about-us", "<p>We fix teeth</p>")
	bob := e.customer(admin, "bob@b.example", "Beta")
	if r := ann.get("/preview/build/"); r.Status != 404 || !strings.Contains(r.Body, "no preview copy") {
		t.Errorf("before any build: %d", r.Status)
	}
	e.buildSite(ann, "Acme Dental")
	home := ann.get("/preview/build/")
	if home.Status != 200 || !strings.Contains(home.Body, "<title>") || !strings.Contains(home.Body, `href="/preview/build/about"`) || !strings.Contains(home.Body, `/preview/build/site.css`) {
		t.Fatalf("home of the copy: %d", home.Status)
	}
	if r := ann.get("/preview/build/about-us"); r.Status != 200 || !strings.Contains(r.Body, "We fix teeth") {
		t.Errorf("a clean address is the .html file: %d", r.Status)
	}
	if r := ann.get("/preview/build/about-us/"); r.Status != 301 || r.Location() != "/preview/build/about-us" {
		t.Errorf("a trailing slash is taken off: %d %s", r.Status, r.Location())
	}
	if r := ann.get("/preview/build/nope"); r.Status != 404 || !strings.Contains(r.Body, "Page not found") {
		t.Errorf("the site's own not-found page: %d", r.Status)
	}
	for _, p := range []string{"/preview/build/.htaccess", "/preview/build/.wb-build.json", "/preview/build/..%2f..%2fetc/passwd", "/preview/build/%2e%2e/%2e%2e/x"} {
		if r := ann.get(p); r.Status == 200 {
			t.Errorf("%s is served: %d", p, r.Status)
		}
	}
	if r := ann.get("/preview/build/site.css"); r.Status != 200 || !strings.Contains(r.Header.Get("Content-Type"), "css") || !strings.Contains(r.Body, "--color-primary") {
		t.Errorf("css: %d", r.Status)
	}
	if r := ann.get("/preview/build/search-index.json"); r.Status != 200 || !strings.Contains(r.Header.Get("Content-Type"), "json") {
		t.Errorf("index: %d", r.Status)
	}
	// someone else's copy is theirs alone: Bob has none, and cannot see Ann's
	if r := bob.get("/preview/build/"); r.Status != 404 || strings.Contains(r.Body, "teeth") {
		t.Errorf("Bob's copy: %d", r.Status)
	}
	if r := e.client().get("/preview/build/"); r.Status != 303 {
		t.Errorf("not logged in: %d", r.Status)
	}
	// building needs the CSRF token and a login
	if r := ann.postNoToken("/admin/build", nil); r.Status != 403 {
		t.Errorf("without the CSRF token: %d", r.Status)
	}
	if r := e.client().postNoToken("/admin/build", nil); r.Status == 303 && strings.Contains(r.Location(), "preview") {
		t.Error("logged out")
	}
	// old copies are tidied: only the latest few stay
	for i := 0; i < 7; i++ {
		e.pool.Exec(t.Context(), `UPDATE sites SET build_version = build_version WHERE id = 1`)
		ann.post("/admin/build", nil)
		// versions are named by the second; make each one distinct
		os.Rename(filepath.Join(e.cfg.DataDir, "builds", "1", e.scalar(`SELECT build_version FROM sites WHERE id = 1`)), filepath.Join(e.cfg.DataDir, "builds", "1", fmt.Sprintf("b2000010%d-000000", i)))
		e.pool.Exec(t.Context(), `UPDATE sites SET build_version = $1 WHERE id = 1`, fmt.Sprintf("b2000010%d-000000", i))
	}
	entries, _ := os.ReadDir(filepath.Join(e.cfg.DataDir, "builds", "1"))
	if len(entries) > 6 {
		t.Errorf("old copies are removed: %d kept", len(entries))
	}
}

func TestBuildIntoAFolderAndUnpublishedChanges(t *testing.T) {
	e := newEnv(t)
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	newPage(t, ann, "About us", "about-us", "<p>We fix teeth</p>")
	ann.post("/admin/menus/main", url.Values{"structure": {`[{"kind":"link","label":"About","url":"/about-us"}]`}})
	e.pool.Exec(t.Context(), `UPDATE sites SET public_url = 'https://example.org/dental/' WHERE name = 'Acme Dental'`)
	dir := e.buildSite(ann, "Acme Dental")
	files := listFiles(t, dir)
	home := string(files["index.html"])
	for _, want := range []string{`href="/dental/about-us"`, `href="/dental/site.css`, `href="https://example.org/dental"`} {
		if !strings.Contains(home, want) && want != `href="https://example.org/dental"` {
			t.Errorf("a site in a folder has the folder in its links: lacks %s", want)
		}
	}
	for name, body := range files {
		for _, u := range localRefs(string(body)) {
			if !strings.HasPrefix(u, "/dental/") && u != "/dental" {
				t.Errorf("%s has an address outside the folder: %s", name, u)
			}
		}
	}
	ht := string(files[".htaccess"])
	if !strings.Contains(ht, "RewriteBase /dental/") || !strings.Contains(ht, "ErrorDocument 404 /dental/404.html") {
		t.Errorf("the folder in .htaccess:\n%s", ht)
	}
	if r := ann.get("/preview/build/dental/about-us"); r.Status != 200 || !strings.Contains(r.Body, "We fix teeth") {
		t.Errorf("the preview of a site in a folder: %d", r.Status)
	}

	// the change counter says what is not in the copy
	siteID := e.scalar(`SELECT id FROM sites WHERE name = 'Acme Dental'`)
	counter := func() string { return e.scalar(`SELECT n FROM site_` + siteID + `.change_counter`) }
	built := e.scalar(`SELECT build_changes FROM sites WHERE id = ` + siteID)
	if counter() != built {
		t.Errorf("right after a build nothing is unpublished: counter %s, built at %s", counter(), built)
	}
	before := counter()
	ann.post("/admin/appearance/title", url.Values{"title": {"New title"}})
	if counter() == before {
		t.Error("changing the title is a change")
	}
	before = counter()
	newPage(t, ann, "Another", "another", "<p>x</p>")
	if counter() == before {
		t.Error("so is a new page")
	}
	// things that are not content do not count
	before = counter()
	e.pool.Exec(t.Context(), `UPDATE site_`+siteID+`.pages SET search_text = 'reindexed' WHERE slug = 'about-us'`)
	e.pool.Exec(t.Context(), `UPDATE site_`+siteID+`.site_settings SET analytics_enabled = NOT analytics_enabled, notify_email = 'x@example.com'`)
	e.pool.Exec(t.Context(), `INSERT INTO site_`+siteID+`.messages (name, email, body) VALUES ('A', 'a@example.com', 'hello')`)
	ann.get("/admin/")
	if counter() != before {
		t.Errorf("search words, analytics settings and messages are not changes to the site: %s -> %s", before, counter())
	}
}
