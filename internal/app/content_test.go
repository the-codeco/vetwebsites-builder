package app_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// blockIDs returns the ids of a page's blocks by type.
func (e *testEnv) blockID(pageID, typ string) string {
	return e.sql(`SELECT id FROM blocks WHERE page_id = $1 AND type = $2 ORDER BY id LIMIT 1`, pageID, typ)
}

func (e *testEnv) blockOrder(pageID string) string {
	return e.sql(`SELECT COALESCE(string_agg(type, ',' ORDER BY position), '') FROM blocks WHERE page_id = $1`, pageID)
}

func TestPagesAndBlocks(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()

	// creating pages
	r := admin.post("/admin/pages", url.Values{"title": {"Our Services"}})
	if r.Status != 303 {
		t.Fatalf("create: %d", r.Status)
	}
	id := idFromLocation(t, r.Location())
	if got := e.sql(`SELECT slug FROM pages WHERE id = $1`, id); got != "our-services" {
		t.Errorf("slug from title: %q", got)
	}
	r2 := admin.post("/admin/pages", url.Values{"title": {"Our Services"}})
	if got := e.sql(`SELECT slug FROM pages WHERE id = $1`, idFromLocation(t, r2.Location())); got != "our-services-2" {
		t.Errorf("duplicate title must get a new slug: %q", got)
	}
	for name, v := range map[string]url.Values{
		"reserved slug":    {"title": {"X"}, "slug": {"admin"}},
		"another reserved": {"title": {"X"}, "slug": {"media"}},
		"bad characters":   {"title": {"X"}, "slug": {"Bad Slug!"}},
		"empty title":      {"title": {""}},
		"explicit dup":     {"title": {"X"}, "slug": {"about"}},
	} {
		if r := admin.post("/admin/pages", v); r.Status != 400 && r.Status != 409 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}

	// every block type can be added; htmx asks the preview to refresh
	for _, typ := range []string{"hero", "richtext", "image", "gallery", "columns", "button", "postlist", "spacer"} {
		r := admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {typ}})
		if r.Status != 200 || !strings.Contains(r.Body, `id="blk-`) || r.Header.Get("HX-Trigger") != "preview-refresh" {
			t.Errorf("add %s: %d trigger=%q", typ, r.Status, r.Header.Get("HX-Trigger"))
		}
	}
	if r := admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"evil"}}); r.Status != 400 {
		t.Errorf("unknown block type: %d", r.Status)
	}
	if r := admin.post("/admin/pages/999999/blocks", url.Values{"type": {"hero"}}); r.Status != 404 {
		t.Errorf("block on a missing page: %d", r.Status)
	}

	// drafts are private
	if r := anon.get("/our-services"); r.Status != 404 {
		t.Errorf("draft visible to the public: %d", r.Status)
	}
	if r := admin.get("/our-services"); r.Status != 200 || !strings.Contains(r.Body, "Draft preview") {
		t.Errorf("draft preview for admin: %d", r.Status)
	}

	// hostile values are neutralised on save and on render
	hero := e.blockID(id, "hero")
	r = admin.post("/admin/blocks/"+hero, url.Values{
		"p.heading": {"Hi <b>there</b>"}, "p.button_label": {"Go"}, "p.button_url": {"javascript:alert(1)"},
		"p.image": {"javascript:alert(2)"}, "p.size": {"huge"},
		"s.background": {"red;}body{display:none"}, "s.text": {"#112233"}, "s.align": {"diagonal"},
	})
	if r.Status != 200 || !strings.Contains(r.Body, "Saved") {
		t.Fatalf("save hero: %d", r.Status)
	}
	text := e.blockID(id, "richtext")
	admin.post("/admin/blocks/"+text, url.Values{"p.content": {`<p onclick="x()">Para</p><script>alert(1)</script><a href="javascript:alert(2)">bad</a> <a href="/about">good</a>`}})
	if r := admin.post("/admin/blocks/999999", url.Values{"p.heading": {"x"}}); r.Status != 404 {
		t.Errorf("save missing block: %d", r.Status)
	}

	if r := admin.post("/admin/pages/"+id, url.Values{"title": {"Our Services"}, "slug": {"our-services"}, "status": {"published"}, "seo_description": {"What we do"}}); r.Status != 303 {
		t.Fatalf("publish: %d", r.Status)
	}
	page := anon.get("/our-services")
	if page.Status != 200 {
		t.Fatalf("published page: %d", page.Status)
	}
	for _, want := range []string{"<h1>Hi &lt;b&gt;there&lt;/b&gt;</h1>", "--fg:#112233", `content="What we do"`, "<p>Para</p>", `href="/about"`, "block-hero", "block-spacer"} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("public page lacks %q", want)
		}
	}
	if extra := foreignScripts(page.Body); len(extra) != 0 {
		t.Errorf("public page has script tags besides the menu script: %v", extra)
	}
	for _, bad := range []string{"onclick", "javascript:", "display:none", "Draft preview"} {
		if strings.Contains(page.Body, bad) {
			t.Errorf("public page contains %q", bad)
		}
	}
	if !strings.Contains(page.Body, "align-left") || strings.Contains(page.Body, "diagonal") {
		t.Error("an invalid alignment must fall back to the default")
	}

	// ordering
	before := e.blockOrder(id)
	spacer := e.blockID(id, "spacer")
	if r := admin.post("/admin/blocks/"+spacer+"/move", url.Values{"dir": {"up"}}); r.Status != 200 || r.Header.Get("HX-Trigger") == "" {
		t.Errorf("move: %d", r.Status)
	}
	after := e.blockOrder(id)
	if before == after || !strings.HasSuffix(before, ",spacer") || !strings.Contains(after, "postlist,spacer,button") && !strings.Contains(after, "spacer,postlist") {
		t.Errorf("order before %q after %q", before, after)
	}
	if r := admin.post("/admin/blocks/"+spacer+"/move", url.Values{"dir": {"sideways"}}); r.Status != 400 {
		t.Errorf("bad direction: %d", r.Status)
	}
	first := e.blockID(id, "hero")
	for i := 0; i < 3; i++ { // moving the first block up is a no-op, not an error
		if r := admin.post("/admin/blocks/"+first+"/move", url.Values{"dir": {"up"}}); r.Status != 200 {
			t.Errorf("move first up: %d", r.Status)
		}
	}
	if !strings.HasPrefix(e.blockOrder(id), "hero,") {
		t.Errorf("hero should stay first: %s", e.blockOrder(id))
	}
	if r := admin.post("/admin/blocks/"+spacer+"/delete", nil); r.Status != 200 {
		t.Errorf("delete block: %d", r.Status)
	}
	if got := e.sql(`SELECT string_agg(position::text, ',' ORDER BY position) FROM blocks WHERE page_id = $1`, id); got != "0,1,2,3,4,5,6" {
		t.Errorf("positions must stay gapless after a delete: %s", got)
	}

	// the home page is special
	homeID := e.sql(`SELECT id FROM pages WHERE slug = 'home'`)
	if r := admin.post("/admin/pages/"+homeID+"/delete", nil); r.Status != 400 {
		t.Errorf("deleting home: %d", r.Status)
	}
	admin.post("/admin/pages/"+homeID, url.Values{"title": {"Home"}, "slug": {"renamed"}, "status": {"published"}})
	if got := e.sql(`SELECT slug FROM pages WHERE id = $1`, homeID); got != "home" {
		t.Errorf("the home page's slug must be locked, got %q", got)
	}
	if r := anon.get("/home"); r.Status != 301 || r.Location() != "/" {
		t.Errorf("/home should redirect to /: %d %q", r.Status, r.Location())
	}

	// deleting a page removes it and its blocks
	if r := admin.post("/admin/pages/"+id+"/delete", nil); r.Status != 303 {
		t.Errorf("delete page: %d", r.Status)
	}
	if r := anon.get("/our-services"); r.Status != 404 {
		t.Errorf("deleted page still served: %d", r.Status)
	}
	if n := e.sql(`SELECT count(*) FROM blocks WHERE page_id = $1`, id); n != "0" {
		t.Errorf("orphaned blocks: %s", n)
	}
}

// --- media ---

func jpegImage(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

func pngImage(w, h int) []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

type uploadResult struct {
	Uploaded []struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		URL   string `json:"url"`
		Thumb string `json:"thumb"`
	} `json:"uploaded"`
	Errors []string `json:"errors"`
}

func decodeUpload(t *testing.T, body string) uploadResult {
	t.Helper()
	var out uploadResult
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("upload response is not JSON: %v: %s", err, body)
	}
	return out
}

func TestMediaLifecycle(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()

	if r := e.client().upload(upFile{"a.png", "image/png", pngImage(10, 10)}); r.Status != 303 && r.Status != 403 {
		t.Errorf("anonymous upload: %d", r.Status)
	}
	noToken := e.newAdmin2(t)
	noToken.token = ""
	if r := noToken.upload(upFile{"a.png", "image/png", pngImage(10, 10)}); r.Status != 403 {
		t.Errorf("upload without a CSRF token: %d", r.Status)
	}

	// valid uploads: names come from the content, not the client
	r := admin.upload(
		upFile{"../../evil name.php.jpg", "image/jpeg", jpegImage(1200, 600)}, // big: gets a thumbnail
		upFile{"logo.png", "text/plain", pngImage(60, 40)},                    // wrong content type is ignored
	)
	res := decodeUpload(t, r.Body)
	if r.Status != 200 || len(res.Uploaded) != 2 || len(res.Errors) != 0 {
		t.Fatalf("valid upload: %d %s", r.Status, r.Body)
	}
	big, small := res.Uploaded[0], res.Uploaded[1]
	if strings.Contains(big.URL, "evil") || strings.Contains(big.URL, "php") || !strings.HasSuffix(big.URL, ".jpg") || !strings.HasPrefix(big.URL, "/media/") {
		t.Errorf("stored name must not come from the client: %s", big.URL)
	}
	if big.Name != "evil name.php.jpg" {
		t.Errorf("display name should be the cleaned base name: %q", big.Name)
	}
	if big.Thumb == big.URL || !strings.Contains(big.Thumb, "_thumb") {
		t.Errorf("large images get a thumbnail: %+v", big)
	}
	if small.Thumb != small.URL {
		t.Errorf("small images are their own thumbnail: %+v", small)
	}
	for _, u := range []string{big.URL, big.Thumb, small.URL} {
		if _, err := os.Stat(filepath.Join(e.mediaDir, filepath.FromSlash(strings.TrimPrefix(u, "/media/")))); err != nil {
			t.Errorf("file for %s not on disk: %v", u, err)
		}
	}

	// everything else is refused, with a clear reason, and stores nothing
	attacks := []struct {
		f    upFile
		want string
	}{
		{upFile{"x.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)}, "not a supported image"},
		{upFile{"x.png", "image/png", []byte(`<html><script>alert(document.cookie)</script></html>`)}, "not a supported image"},
		{upFile{"x.jpg", "image/jpeg", []byte("plain text pretending to be a photo")}, "not a supported image"},
		{upFile{"x.gif", "image/gif", append([]byte("GIF89a\x01\x00\x01\x00"), []byte("garbage-not-a-real-gif")...)}, "couldn't be read"},
		{upFile{"bomb.png", "image/png", pngImage(6000, 6000)}, "too many pixels"},
		{upFile{"huge.jpg", "image/jpeg", append(jpegImage(10, 10), make([]byte, 11<<20)...)}, "larger than"},
	}
	filesBefore := countFiles(t, e.mediaDir)
	for _, a := range attacks {
		r := admin.upload(a.f)
		res := decodeUpload(t, r.Body)
		if r.Status != 400 || len(res.Uploaded) != 0 || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], a.want) {
			t.Errorf("%s: %d %s", a.f.Name, r.Status, r.Body)
		}
	}
	if got := countFiles(t, e.mediaDir); got != filesBefore {
		t.Errorf("rejected uploads left files behind: %d -> %d", filesBefore, got)
	}
	mixed := decodeUpload(t, admin.upload(upFile{"ok.png", "image/png", pngImage(20, 20)}, upFile{"bad.svg", "image/svg+xml", []byte("<svg/>")}).Body)
	if len(mixed.Uploaded) != 1 || len(mixed.Errors) != 1 {
		t.Errorf("a batch keeps its good files: %+v", mixed)
	}
	if r := admin.upload(); r.Status != 400 {
		t.Errorf("empty upload: %d", r.Status)
	}
	many := make([]upFile, 11)
	for i := range many {
		many[i] = upFile{"n.png", "image/png", pngImage(5, 5)}
	}
	if r := admin.upload(many...); r.Status != 400 {
		t.Errorf("too many files: %d", r.Status)
	}

	// serving: safe headers, no directory listing, no escaping the folder
	img := anon.get(big.URL)
	if img.Status != 200 || img.Header.Get("Content-Type") != "image/jpeg" || img.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(img.Header.Get("Content-Security-Policy"), "sandbox") || !strings.Contains(img.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("serving headers: %d %v", img.Status, img.Header)
	}
	for _, p := range []string{"/media/", "/media/2026/", "/media/nope.jpg", "/media/../go.mod", "/media/%2e%2e/go.mod", "/media/..%2fgo.mod"} {
		if r := anon.get(p); r.Status == 200 {
			t.Errorf("GET %s returned 200", p)
		}
	}

	// library, alt text, usage, deletion
	if r := admin.get("/admin/media"); r.Status != 200 || !strings.Contains(r.Body, "evil name.php.jpg") || !strings.Contains(r.Body, "1200×600") {
		t.Errorf("library: %d", r.Status)
	}
	if r := admin.get("/admin/media/picker"); r.Status != 200 || strings.Count(r.Body, "data-media-url=") != 3 {
		t.Errorf("picker: %d (%d items)", r.Status, strings.Count(r.Body, "data-media-url="))
	}
	bigID := e.sql(`SELECT id FROM media WHERE original_name = 'evil name.php.jpg'`)
	if r := admin.post("/admin/media/"+bigID, url.Values{"alt": {strings.Repeat("a", 301)}}); r.Status != 400 {
		t.Errorf("long alt: %d", r.Status)
	}
	if r := admin.post("/admin/media/"+bigID, url.Values{"alt": {"A gradient"}}); r.Status != 303 {
		t.Errorf("alt: %d", r.Status)
	}
	if !strings.Contains(admin.get("/admin/media/picker").Body, `data-media-alt="A gradient"`) {
		t.Error("alt text should reach the picker")
	}

	pageID := idFromLocation(t, admin.post("/admin/pages", url.Values{"title": {"Gallery Page"}}).Location())
	admin.post("/admin/pages/"+pageID+"/blocks", url.Values{"type": {"image"}})
	admin.post("/admin/blocks/"+e.blockID(pageID, "image"), url.Values{"p.image": {big.URL}, "p.alt": {"x"}})
	if r := admin.get("/admin/media"); !strings.Contains(r.Body, "used on 1 page") {
		t.Error("the library should show where an image is used")
	}

	if r := admin.post("/admin/media/"+bigID+"/delete", nil); r.Status != 303 {
		t.Errorf("delete: %d", r.Status)
	}
	for _, u := range []string{big.URL, big.Thumb} {
		if _, err := os.Stat(filepath.Join(e.mediaDir, filepath.FromSlash(strings.TrimPrefix(u, "/media/")))); !os.IsNotExist(err) {
			t.Errorf("%s should be gone from disk: %v", u, err)
		}
		if r := anon.get(u); r.Status != 404 {
			t.Errorf("%s still served: %d", u, r.Status)
		}
	}
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// newAdmin2 returns a second logged-in client for the already-created admin.
func (e *testEnv) newAdmin2(t *testing.T) *client {
	t.Helper()
	c := e.client()
	if r := c.login("admin@example.com", testPassword); r.Status != 303 {
		t.Fatalf("second login: %d", r.Status)
	}
	return c
}
