package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/backup"
	"webbuilder/internal/config"
)

// --- helpers ---

func unzip(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = data
	}
	return out
}

func rezip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, _ := zw.Create(n)
		w.Write(files[n])
	}
	zw.Close()
	return buf.Bytes()
}

// editBackup changes a backup file's contents and fixes its manifest, so the change itself is what is tested.
func editBackup(t *testing.T, raw []byte, edit func(files map[string][]byte)) []byte {
	t.Helper()
	files := unzip(t, raw)
	edit(files)
	var man map[string]any
	if err := json.Unmarshal(files[backup.ManifestName], &man); err != nil {
		t.Fatal(err)
	}
	sums := map[string]any{}
	for n, data := range files {
		if n != backup.ManifestName {
			s := sha256.Sum256(data)
			sums[n] = hex.EncodeToString(s[:])
		}
	}
	man["files"] = sums
	files[backup.ManifestName], _ = json.Marshal(man)
	return rezip(t, files)
}

func (c *client) downloadBackup(personal bool) response {
	c.t.Helper()
	v := url.Values{}
	if personal {
		v.Set("personal", "1")
	}
	return c.post("/admin/backup/download", v)
}

func (c *client) uploadBackup(data []byte) response {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("csrf", c.token)
	part, _ := mw.CreateFormFile("backup", "site-backup.zip")
	part.Write(data)
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+"/admin/backup/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(req, nil)
}

var reSource = regexp.MustCompile(`name="source" value="([^"]+)"`)

func (c *client) restore(source string, extra map[string]string) response {
	c.t.Helper()
	v := url.Values{"source": {source}, "label": {"test"}, "confirm": {"RESTORE"}}
	for k, val := range extra {
		v.Set(k, val)
	}
	return c.post("/admin/backup/restore", v)
}

// restoreFile uploads a backup, checks the review page and restores it. It returns the final page.
func (c *client) restoreUpload(t *testing.T, data []byte, extra map[string]string) response {
	t.Helper()
	r := c.uploadBackup(data)
	if r.Status != 200 || !strings.Contains(r.Body, "Restore this backup?") {
		t.Fatalf("review: %d %s", r.Status, firstLine(r.Body))
	}
	m := reSource.FindStringSubmatch(r.Body)
	if m == nil {
		t.Fatal("no source on the review page")
	}
	return c.restore(m[1], extra)
}

// siteSnapshot is everything a restore replaces, as text, so two states can be compared.
func (e *testEnv) siteSnapshot() string {
	e.t.Helper()
	parts := []string{
		e.sql(`SELECT COALESCE(string_agg(p.slug||'|'||p.title||'|'||p.status||'|'||COALESCE(p.publish_at::text, '')||'|'||p.seo::text||'|'||p.search_text||'|'||COALESCE((SELECT string_agg(b.type||b.props::text||b.style::text, '~' ORDER BY b.position, b.id) FROM blocks b WHERE b.page_id = p.id AND b.parent_id IS NULL), ''), E'\n' ORDER BY p.slug), '') FROM pages p`),
		e.sql(`SELECT COALESCE(string_agg(p.slug||'|'||p.title||'|'||p.excerpt||'|'||p.body||'|'||p.cover_image||'|'||p.status||'|'||COALESCE(p.publish_at::text, '')||'|'||p.seo::text||'|'||COALESCE(p.published_at::text, '')||'|'||COALESCE((SELECT string_agg(c.slug, ',' ORDER BY c.slug) FROM post_categories pc JOIN categories c ON c.id = pc.category_id WHERE pc.post_id = p.id), ''), E'\n' ORDER BY p.slug), '') FROM posts p`),
		e.sql(`SELECT COALESCE(string_agg(slug||'|'||name, E'\n' ORDER BY slug), '') FROM categories`),
		e.sql(`SELECT COALESCE(string_agg(name||'|'||type||'|'||props::text||'|'||style::text, E'\n' ORDER BY name), '') FROM reusable_blocks`),
		e.sql(`SELECT COALESCE(string_agg(from_path||'|'||to_url||'|'||permanent::text||'|'||auto::text, E'\n' ORDER BY from_path), '') FROM redirects`),
		e.sql(`SELECT COALESCE(string_agg(location||items::text||sticky::text, E'\n' ORDER BY location), '') FROM menus`),
		e.sql(`SELECT title||'|'||active_theme||'|'||tokens::text||'|'||custom_css||'|'||seo::text||'|'||show_search::text||'|'||color_mode||'|'||consent::text||'|'||footer::text FROM site_settings`),
		e.sql(`SELECT COALESCE(string_agg(path||'|'||original_name||'|'||alt||'|'||mime||'|'||width||'x'||height||'|'||size_bytes, E'\n' ORDER BY path), '') FROM media`),
	}
	return strings.Join(parts, "\n====\n")
}

func (e *testEnv) mediaPaths() []string {
	rows, err := e.pool.Query(context.Background(), `SELECT path FROM media ORDER BY path`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		out = append(out, p)
	}
	return out
}

func (e *testEnv) onDisk(urlPath string) bool {
	_, err := os.Stat(e.file(urlPath))
	return err == nil
}

// buildSite fills a site with a bit of everything and returns the uploaded images.
func buildSite(t *testing.T, e *testEnv, admin *client) (img1, img2 uploaded) {
	t.Helper()
	img1 = admin.uploadOne(t, "photo.jpg", "image/jpeg", jpegImage(900, 600))
	img2 = admin.uploadOne(t, "logo.png", "image/png", pngImage(300, 200))
	admin.post("/admin/media/"+strconv.FormatInt(img1.ID, 10), url.Values{"alt": {"A photograph"}})

	id := newPage(t, admin, "Gallery")
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"image"}})
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"richtext"}})
	admin.post("/admin/blocks/"+e.blockID(id, "image"), url.Values{"p.image": {img1.URL}, "p.alt": {"A photograph"}, "s.background": {"#ffeedd"}})
	admin.post("/admin/blocks/"+e.blockID(id, "richtext"), url.Values{"p.content": {"<p>Zebra crossing <strong>ahead</strong></p>"}})
	admin.post("/admin/pages/"+id, url.Values{"title": {"Gallery"}, "slug": {"gallery"}, "status": {"published"}, "seo_description": {"Pictures"}})

	e.pool.Exec(context.Background(), `INSERT INTO categories (slug, name) VALUES ('news', 'News'), ('tips', 'Tips')`)
	e.pool.Exec(context.Background(), `INSERT INTO posts (slug, title, excerpt, body, cover_image, status, published_at) VALUES
		('hello', 'Hello world', 'First', '<p>Giraffe in the <em>body</em></p>', $1, 'published', '2026-03-04 05:06:07+00'),
		('draft-post', 'A draft', '', '<p>Not yet</p>', '', 'draft', NULL)`, img2.URL)
	e.pool.Exec(context.Background(), `INSERT INTO post_categories (post_id, category_id) SELECT p.id, c.id FROM posts p, categories c WHERE p.slug = 'hello'`)
	admin.post("/admin/footer", url.Values{"structure": {`{"version":2,"bottom":"© {year} Demo","style":{"background":"#102030","padding":"lg"},"rows":[{"align":"center","columns":[{"widgets":[{"type":"links","inline":true,"links":[{"label":"Gallery","url":"/gallery"},{"label":"Privacy","url":"/privacy","new_tab":true}]}]}]}]}`}})

	admin.post("/admin/appearance/title", url.Values{"title": {"Backup Demo Co"}})
	admin.post("/admin/appearance/tokens", url.Values{"t.color-primary": {"#aa3300"}, "t.radius": {"12px"}})
	admin.post("/admin/appearance/dark", url.Values{"d.color-bg": {"#101010"}})
	admin.post("/admin/appearance/css", url.Values{"css": {".site-title { letter-spacing: .1em; }"}})
	admin.post("/admin/appearance/consent", url.Values{"consent_enabled": {"on"}, "consent_mode": {"choice"}})
	return img1, img2
}

// --- tests ---

func TestBackupDownloadHoldsTheSiteButNotTheAccounts(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	e.newUser(admin, "ed@example.com", "editor")
	img1, img2 := buildSite(t, e, admin)
	e.subscribe("ann@example.com", true)
	e.addMessage("Vera", "vera@example.org", "Hi", "Hello")

	r := admin.downloadBackup(false)
	if r.Status != 200 || r.Header.Get("Content-Type") != "application/zip" || !strings.HasPrefix(r.Header.Get("Content-Disposition"), `attachment; filename="webbuilder-backup-`) ||
		r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download: %d %v", r.Status, r.Header)
	}
	files := unzip(t, []byte(r.Body))
	names := make([]string, 0)
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	want := []string{"manifest.json", "media/" + strings.TrimPrefix(img1.URL, "/media/"), "media/" + strings.TrimPrefix(img2.URL, "/media/"), "site.json"}
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("files in the backup: %v, want %v", names, want)
	}
	var man backup.Manifest
	json.Unmarshal(files["manifest.json"], &man)
	if man.Format != "webbuilder-backup" || man.Version != backup.Version || man.Personal || man.SiteTitle != "Backup Demo Co" || len(man.Files) != 3 {
		t.Errorf("manifest: %+v", man)
	}
	if man.Counts.Media != 2 || man.Counts.Posts != 2 || man.Counts.Categories != 2 || man.Counts.Pages < 3 {
		t.Errorf("counts: %+v", man.Counts)
	}
	if !bytes.Equal(files["media/"+strings.TrimPrefix(img1.URL, "/media/")], e.stored(img1.URL)) {
		t.Error("images are saved exactly as stored")
	}

	// nothing about accounts, sessions, subscribers or messages is in it, anywhere
	for name, data := range files {
		if strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".png") {
			continue
		}
		for _, secret := range []string{"admin@example.com", "ed@example.com", "$argon2", "password_hash", "ann@example.com", "vera@example.org", "csrf", "sessions"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("%s contains %q", name, secret)
			}
		}
	}
	site := string(files["site.json"])
	for _, want := range []string{`"title": "Backup Demo Co"`, "Zebra crossing", "Giraffe in the", "#aa3300", `"dark.color-bg": "#101010"`, "letter-spacing", "Privacy", `"slug": "gallery"`} {
		if !strings.Contains(site, want) {
			t.Errorf("site.json lacks %q", want)
		}
	}
	if strings.Contains(site, `"domain"`) || strings.Contains(site, "notify") {
		t.Error("the domain and email settings belong to the server, not the content")
	}

	// the personal option adds exactly the list and the messages
	r = admin.downloadBackup(true)
	files = unzip(t, []byte(r.Body))
	json.Unmarshal(files["manifest.json"], &man)
	if !man.Personal || man.Counts.Subscribers != 1 || man.Counts.Messages != 1 || !bytes.Contains(files["site.json"], []byte("ann@example.com")) || !bytes.Contains(files["site.json"], []byte("vera@example.org")) {
		t.Errorf("personal backup: %+v", man)
	}
	if !strings.Contains(r.Header.Get("Content-Disposition"), "with-subscribers") {
		t.Error("a file with personal data says so in its name")
	}
	if bytes.Contains(files["site.json"], []byte("$argon2")) || bytes.Contains(files["site.json"], []byte("token_hash")) {
		t.Error("no secrets, even in a personal backup")
	}
}

func TestBackupRoundTrip(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	e.pool.Exec(context.Background(), `UPDATE site_settings SET domain = 'keep.example.com'`)
	img1, img2 := buildSite(t, e, admin)
	// restoring fills in what older or hand-written blocks leave out (a block's missing settings get their defaults),
	// so the starting point is a site that has been through one restore
	if r := admin.restoreUpload(t, []byte(admin.downloadBackup(false).Body), nil); r.Status != 200 {
		t.Fatalf("settling restore: %d", r.Status)
	}
	before := e.siteSnapshot()
	raw := []byte(admin.downloadBackup(false).Body)

	// the site changes in every way a restore replaces
	id := e.sql(`SELECT id FROM pages WHERE slug = 'gallery'`)
	admin.post("/admin/pages/"+id+"/delete", nil)
	admin.post("/admin/pages", url.Values{"title": {"Junk"}})
	admin.post("/admin/appearance/title", url.Values{"title": {"Something else"}})
	admin.post("/admin/appearance/tokens", url.Values{"t.color-bg": {"#eeeeff"}})
	admin.post("/admin/appearance/dark", url.Values{"d.color-text": {"#ffffff"}})
	admin.post("/admin/appearance/css", url.Values{"css": {"body { color: red }"}})
	e.pool.Exec(context.Background(), `DELETE FROM posts; DELETE FROM categories; UPDATE menus SET items = '[]', sticky = false`)
	admin.post("/admin/media/"+e.sql(`SELECT id FROM media WHERE path = $1`, strings.TrimPrefix(img1.URL, "/media/"))+"/delete", nil) // a restore gives images new ids
	extra := admin.uploadOne(t, "later.jpg", "image/jpeg", jpegImage(2000, 1000))
	anon.get(extra.URL + "?w=480") // a resized copy now exists too
	if !e.onDisk(extra.URL) || !e.onDisk(extra.Thumb) {
		t.Fatal("test setup: the later image should be on disk")
	}
	mutated := e.siteSnapshot()
	if mutated == before || e.onDisk(img1.URL) {
		t.Fatal("test setup: the site should have changed")
	}

	// review shows what is in the file; nothing changes yet
	rv := admin.uploadBackup(raw)
	for _, want := range []string{"Restore this backup?", "Backup Demo Co", "2 images", "Replace the site with this backup", `name="confirm"`, "pre-restore"} {
		if rv.Status != 200 || !strings.Contains(rv.Body, want) {
			t.Errorf("review page lacks %q (%d)", want, rv.Status)
		}
	}
	if strings.Contains(rv.Body, `name="personal"`) {
		t.Error("a backup without personal data offers no personal option")
	}
	if e.siteSnapshot() != mutated {
		t.Fatal("looking at a backup must change nothing")
	}
	source := reSource.FindStringSubmatch(rv.Body)[1]

	// the word must be typed
	for _, word := range []string{"", "restore", "YES", "RESTOR", "RESTORE NOW"} {
		if r := admin.restore(source, map[string]string{"confirm": word}); r.Status != 400 || !strings.Contains(r.Body, "Type RESTORE") {
			t.Errorf("confirm %q: %d", word, r.Status)
		}
	}
	if e.siteSnapshot() != mutated {
		t.Fatal("a refused restore must change nothing")
	}

	r := admin.restore(source, nil)
	if r.Status != 200 || !strings.Contains(r.Body, "Restored") {
		t.Fatalf("restore: %d %s", r.Status, firstLine(r.Body))
	}
	if after := e.siteSnapshot(); after != before {
		t.Errorf("the site is not as it was.\nbefore:\n%s\n\nafter:\n%s", before, after)
	}

	// the things a restore must not touch
	if got := e.sql(`SELECT domain FROM site_settings`); got != "keep.example.com" {
		t.Errorf("the domain was changed: %q", got)
	}
	if r := admin.get("/admin/pages"); r.Status != 200 {
		t.Errorf("the admin session survived: %d", r.Status)
	}
	if e.sql(`SELECT count(*) FROM users`) != "1" {
		t.Error("accounts untouched")
	}

	// images are back, served, with thumbnails; the one added later is gone, with its thumbnail and copy
	if !e.onDisk(img1.URL) || !e.onDisk(img1.Thumb) || !e.onDisk(img2.URL) {
		t.Error("restored images and thumbnails should be on disk")
	}
	if r := anon.get(img1.URL); r.Status != 200 || !bytes.Equal([]byte(r.Body), e.stored(img1.URL)) {
		t.Errorf("restored image: %d", r.Status)
	}
	if r := anon.get(img1.URL + "?w=480"); r.Status != 200 {
		t.Errorf("a restored image can be resized: %d", r.Status)
	}
	if e.onDisk(extra.URL) || e.onDisk(extra.Thumb) || e.onDisk(strings.Replace(extra.URL, ".jpg", "_w480.jpg", 1)) {
		t.Error("files of an image that is no longer in the library should be removed")
	}
	if len(e.mediaPaths()) != 2 {
		t.Errorf("media rows: %v", e.mediaPaths())
	}

	// the public site works from the restored content: pages, search, posts, styling
	if r := anon.get("/gallery"); r.Status != 200 || !strings.Contains(r.Body, "Zebra crossing") || !strings.Contains(r.Body, img1.URL) {
		t.Errorf("gallery page: %d", r.Status)
	}
	if r := anon.get("/search?q=zebra"); !strings.Contains(r.Body, "Gallery") {
		t.Error("search finds restored pages")
	}
	if r := anon.get("/blog/hello"); r.Status != 200 || !strings.Contains(r.Body, "Giraffe") {
		t.Errorf("post: %d", r.Status)
	}
	if r := anon.get("/blog/draft-post"); r.Status != 404 {
		t.Errorf("a draft stays private: %d", r.Status)
	}
	if css := anon.get("/site.css").Body; !strings.Contains(css, "#aa3300") || !strings.Contains(css, "#101010") || !strings.Contains(css, "letter-spacing") || strings.Contains(css, "color: red") {
		t.Errorf("restored styling:\n%s", css)
	}
	if home := anon.get("/").Body; !strings.Contains(home, "Gallery") || !strings.Contains(home, "Privacy") || !strings.Contains(home, "Backup Demo Co") {
		t.Error("restored menu and title on the home page")
	}
	if e.sql(`SELECT count(*) FROM pages WHERE slug LIKE 'junk%'`) != "0" {
		t.Error("the junk page should be gone")
	}

	// a safety copy of the site as it was just before was saved, and restoring it undoes the restore
	var snap string
	for _, f := range mustList(t, e) {
		if f.Kind == backup.KindPreRestore && snap == "" { // the newest
			snap = f.Name
		}
	}
	if snap == "" || !strings.Contains(r.Body, snap) {
		t.Fatalf("a pre-restore backup should be saved and named on the result page (%q)", snap)
	}
	rv = admin.post("/admin/backup/files/"+snap+"/review", nil)
	if rv.Status != 200 || !strings.Contains(rv.Body, "Restore this backup?") {
		t.Fatalf("review a saved file: %d", rv.Status)
	}
	if r := admin.restore(reSource.FindStringSubmatch(rv.Body)[1], nil); r.Status != 200 {
		t.Fatalf("undo: %d", r.Status)
	}
	if after := e.siteSnapshot(); after != mutated {
		t.Errorf("restoring the pre-restore backup should bring back the state before the restore.\nwant:\n%s\n\ngot:\n%s", mutated, after)
	}
	if !e.onDisk(extra.URL) {
		t.Error("the image that was added after the backup is back with the undo")
	}
}

func mustList(t *testing.T, e *testEnv) []backup.File {
	t.Helper()
	files, err := e.app.Backups().List()
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestRestoreIntoAFreshSite(t *testing.T) {
	a := newEnv(t)
	adminA := a.newAdmin("admin@example.com")
	img1, _ := buildSite(t, a, adminA)
	raw := []byte(adminA.downloadBackup(false).Body)
	a.srv.Close()

	// a brand-new site with its own media folder and its own account
	b := newEnv(t, func(c *config.Config) { c.MediaDir = filepath.Join(t.TempDir(), "other-media") })
	adminB := b.newAdmin("owner@example.org")
	if b.onDisk(img1.URL) {
		t.Fatal("test setup: the image shouldn't exist yet")
	}
	r := adminB.restoreUpload(t, raw, nil)
	if r.Status != 200 || !strings.Contains(r.Body, "Restored") {
		t.Fatalf("restore: %d %s", r.Status, firstLine(r.Body))
	}
	anon := b.client()
	if !b.onDisk(img1.URL) || !b.onDisk(img1.Thumb) {
		t.Error("images and thumbnails are written into the new media folder")
	}
	if r := anon.get(img1.URL); r.Status != 200 {
		t.Errorf("image: %d", r.Status)
	}
	if r := anon.get("/gallery"); r.Status != 200 || !strings.Contains(r.Body, "Zebra crossing") {
		t.Errorf("page: %d", r.Status)
	}
	if r := anon.get("/search?q=giraffe"); !strings.Contains(r.Body, "Hello world") {
		t.Error("search works on restored posts")
	}
	if r := anon.get("/rss.xml"); !strings.Contains(r.Body, "Hello world") {
		t.Error("the feed has the restored post")
	}
	if got := b.sql(`SELECT email FROM users`); got != "owner@example.org" {
		t.Errorf("the new site keeps its own account: %s", got)
	}
	if got := b.sql(`SELECT author_id FROM posts WHERE slug = 'hello'`); got == "<nil>" {
		t.Error("restored posts are attributed to whoever restored them")
	}
}

func TestBackupPersonalDataIsOptIn(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	for _, a := range []string{"ann@example.com", "bob@example.com"} {
		e.subscribe(a, true)
	}
	e.pool.Exec(context.Background(), `UPDATE subscribers SET unsub_token = $1 WHERE email = 'ann@example.com'`, strings.Repeat("U", 43))
	msg := e.addMessage("Vera", "vera@example.org", "Hi", "Hello")
	e.pool.Exec(context.Background(), `INSERT INTO message_replies (message_id, user_id, body) SELECT $1, id, 'Our answer' FROM users LIMIT 1`, msg)
	raw := []byte(admin.downloadBackup(true).Body)
	plain := []byte(admin.downloadBackup(false).Body)

	// the owner's list changes afterwards
	e.pool.Exec(context.Background(), `DELETE FROM subscribers WHERE email = 'bob@example.com'`)
	e.subscribe("new@example.com", true)
	e.pool.Exec(context.Background(), `DELETE FROM messages`)
	e.pool.Exec(context.Background(), `INSERT INTO campaigns (subject, body) VALUES ('Old news', 'x')`)

	// restoring the full file without ticking the box leaves subscribers and messages as they are
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if e.sql(`SELECT string_agg(email, ',' ORDER BY email) FROM subscribers`) != "ann@example.com,new@example.com" || e.sql(`SELECT count(*) FROM messages`) != "0" {
		t.Error("personal data must not be replaced unless asked")
	}

	// with the box ticked they are replaced, and the unsubscribe link of newsletters already sent keeps working
	rv := admin.uploadBackup(raw)
	if !strings.Contains(rv.Body, `name="personal"`) || !strings.Contains(rv.Body, "2 subscribers and 1 contact messages") {
		t.Error("the review offers the personal option and says what is in it")
	}
	if r := admin.restore(reSource.FindStringSubmatch(rv.Body)[1], map[string]string{"personal": "1"}); r.Status != 200 || !strings.Contains(r.Body, "2 subscribers and 1 messages were restored") {
		t.Fatalf("restore with personal: %d %s", r.Status, firstLine(r.Body))
	}
	if got := e.sql(`SELECT string_agg(email, ',' ORDER BY email) FROM subscribers`); got != "ann@example.com,bob@example.com" {
		t.Errorf("subscribers: %s", got)
	}
	if e.sql(`SELECT unsub_token FROM subscribers WHERE email = 'ann@example.com'`) != strings.Repeat("U", 43) {
		t.Error("the unsubscribe link must survive a restore")
	}
	if r := e.client().post("/unsubscribe/"+strings.Repeat("U", 43), nil); r.Status != 200 {
		t.Errorf("an old unsubscribe link works after the restore: %d", r.Status)
	}
	if e.sql(`SELECT count(*) FROM messages WHERE email = 'vera@example.org'`) != "1" || e.sql(`SELECT count(*) FROM message_replies WHERE body = 'Our answer'`) != "1" {
		t.Error("messages and their replies are restored")
	}
	if e.sql(`SELECT count(*) FROM campaigns WHERE subject = 'Old news'`) != "1" {
		t.Error("sent newsletters are not part of a restore")
	}
	if e.sql(`SELECT count(*) FROM subscribers WHERE confirmed_at IS NULL`) != "0" {
		t.Error("confirmed stays confirmed")
	}

	// a file without personal data can't be used to replace them
	rv = admin.uploadBackup(plain)
	if strings.Contains(rv.Body, `name="personal"`) {
		t.Error("no personal option for a plain backup")
	}
	if r := admin.restore(reSource.FindStringSubmatch(rv.Body)[1], map[string]string{"personal": "1"}); r.Status != 400 || !strings.Contains(r.Body, "doesn't contain subscribers") && !strings.Contains(r.Body, "doesn&#39;t contain subscribers") {
		t.Errorf("personal from a plain file: %d", r.Status)
	}
}

func TestRestoreIsAllOrNothing(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	img1, _ := buildSite(t, e, admin)
	raw := []byte(admin.downloadBackup(false).Body)
	before := e.siteSnapshot()

	// something the file cannot know goes wrong half way through: the database refuses one post
	e.pool.Exec(context.Background(), `
		CREATE FUNCTION refuse_boom() RETURNS trigger AS $$ BEGIN IF NEW.slug = 'boom' THEN RAISE EXCEPTION 'refused'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql;
		CREATE TRIGGER refuse_boom BEFORE INSERT ON posts FOR EACH ROW EXECUTE FUNCTION refuse_boom()`)
	newImg := "2026/10/cccccccccccccccccccccccc.jpg"
	bad := editBackup(t, raw, func(files map[string][]byte) {
		var site map[string]any
		json.Unmarshal(files["site.json"], &site)
		site["posts"] = append(site["posts"].([]any), map[string]any{"slug": "boom", "title": "Boom", "excerpt": "", "body": "", "cover": "", "status": "draft", "seo": map[string]any{}, "published_at": nil, "updated_at": time.Now(), "categories": []string{}})
		site["media"] = append(site["media"].([]any), map[string]any{"path": newImg, "original_name": "new.jpg", "alt": "", "created_at": time.Now()})
		site["settings"].(map[string]any)["title"] = "Should not appear"
		files["site.json"], _ = json.MarshalIndent(site, "", " ")
		files["media/"+newImg] = jpegImage(100, 100)
	})
	r := admin.restoreUpload(t, bad, nil)
	if r.Status != 500 || !strings.Contains(r.Body, "nothing was changed") {
		t.Fatalf("a failed restore: %d %s", r.Status, firstLine(r.Body))
	}
	if after := e.siteSnapshot(); after != before {
		t.Errorf("a failed restore changed the site.\nbefore:\n%s\n\nafter:\n%s", before, after)
	}
	if e.onDisk("/media/"+newImg) || e.onDisk("/media/"+strings.TrimSuffix(newImg, ".jpg")+"_thumb.jpg") {
		t.Error("files written for the failed restore must be removed")
	}
	if !e.onDisk(img1.URL) || !e.onDisk(img1.Thumb) {
		t.Error("the site's own images must be untouched")
	}
	if r := e.client().get("/gallery"); r.Status != 200 || !strings.Contains(r.Body, "Zebra crossing") {
		t.Errorf("the site still works: %d", r.Status)
	}

	// an image that clashes with a different file of the same name is refused before anything changes
	e.pool.Exec(context.Background(), `DROP TRIGGER refuse_boom ON posts`)
	clash := editBackup(t, raw, func(files map[string][]byte) {
		files["media/"+strings.TrimPrefix(img1.URL, "/media/")] = jpegImage(50, 50)
	})
	r = admin.restoreUpload(t, clash, nil)
	if r.Status != 400 || !strings.Contains(r.Body, "already exists") {
		t.Errorf("a clashing image: %d %s", r.Status, firstLine(r.Body))
	}
	if e.siteSnapshot() != before {
		t.Error("a refused restore must change nothing")
	}
	if !bytes.Equal(e.stored(img1.URL), unzip(t, raw)["media/"+strings.TrimPrefix(img1.URL, "/media/")]) {
		t.Error("the existing image must be untouched")
	}
}

func TestBackupUploadsAreChecked(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	buildSite(t, e, admin)
	raw := []byte(admin.downloadBackup(false).Body)
	before := e.siteSnapshot()
	incoming := func() int {
		entries, _ := os.ReadDir(filepath.Join(filepath.Dir(e.mediaDir), "backups", "incoming"))
		return len(entries)
	}

	for name, data := range map[string][]byte{
		"text":            []byte("not a backup"),
		"empty":           {},
		"truncated":       raw[:len(raw)/2],
		"a different zip": rezip(t, map[string][]byte{"hello.txt": []byte("hi")}),
		"tampered": editBackupRaw(t, raw, func(files map[string][]byte) {
			files["site.json"] = bytes.Replace(files["site.json"], []byte("Zebra"), []byte("Yebra"), 1) // manifest not fixed
		}),
		"an extra file": editBackupRaw(t, raw, func(files map[string][]byte) { files["evil.php"] = []byte("<?php") }),
		"a new format": editBackup(t, raw, func(files map[string][]byte) {
			files["manifest.json"] = bytes.Replace(files["manifest.json"], []byte(fmt.Sprintf(`"version": %d`, backup.Version)), []byte(fmt.Sprintf(`"version": %d`, backup.Version+1)), 1)
		}),
	} {
		r := admin.uploadBackup(data)
		if name == "a new format" {
			if r.Status != 400 || !strings.Contains(r.Body, "newer version") {
				t.Errorf("%s: %d", name, r.Status)
			}
			continue
		}
		if r.Status != 400 || strings.Contains(r.Body, "Restore this backup?") || !strings.Contains(r.Body, `class="alert error"`) {
			t.Errorf("%s: %d %s", name, r.Status, firstLine(r.Body))
		}
	}
	if incoming() != 0 {
		t.Errorf("rejected uploads must not be kept: %d files", incoming())
	}
	if e.siteSnapshot() != before {
		t.Error("rejected uploads change nothing")
	}

	// no file, or two
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("csrf", admin.token)
	mw.Close()
	req, _ := http.NewRequest("POST", admin.base+"/admin/backup/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if r := admin.do(req, nil); r.Status != 400 || !strings.Contains(r.Body, "Choose one backup file") {
		t.Errorf("no file: %d", r.Status)
	}

	// a restore needs a file that is really waiting
	for _, src := range []string{"", "upload:", "upload:zzzz", "upload:../../etc/passwd", "upload:" + strings.Repeat("0", 32), "file:../x", "file:evil.zip", "file:manual-20260101-000000.zip", "other:thing", "../x"} {
		if r := admin.restore(src, nil); r.Status != 404 {
			t.Errorf("source %q: %d", src, r.Status)
		}
	}
	if e.siteSnapshot() != before {
		t.Error("nothing changed")
	}
}

func editBackupRaw(t *testing.T, raw []byte, edit func(files map[string][]byte)) []byte {
	t.Helper()
	files := unzip(t, raw)
	edit(files)
	return rezip(t, files)
}

func TestBackupPagesAreAdminOnlyAndCsrfProtected(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	buildSite(t, e, admin)
	if _, err := e.app.Backups().Save(context.Background(), backup.KindManual, false); err != nil {
		t.Fatal(err)
	}
	name := mustList(t, e)[0].Name
	before := e.siteSnapshot()

	gets := []string{"/admin/backup", "/admin/backup/files/" + name}
	posts := []string{"/admin/backup/download", "/admin/backup/save", "/admin/backup/upload", "/admin/backup/restore",
		"/admin/backup/files/" + name + "/delete", "/admin/backup/files/" + name + "/review"}
	for _, p := range gets {
		if r := editor.get(p); r.Status != 403 {
			t.Errorf("editor GET %s: %d", p, r.Status)
		}
		if r := anon.get(p); !strings.Contains(r.Location(), "/admin/login") || strings.Contains(r.Body, "PK") {
			t.Errorf("visitor GET %s: %d %q", p, r.Status, r.Location())
		}
	}
	form := url.Values{"source": {"file:" + name}, "confirm": {"RESTORE"}}
	for _, p := range posts {
		if r := editor.post(p, form); r.Status != 403 {
			t.Errorf("editor POST %s: %d", p, r.Status)
		}
		if r := anon.post(p, form); !strings.Contains(r.Location(), "/admin/login") || strings.Contains(r.Body, "PK") {
			t.Errorf("visitor POST %s: %d", p, r.Status)
		}
		if r := admin.postNoToken(p, form); r.Status != 403 || strings.HasPrefix(r.Body, "PK") {
			t.Errorf("no CSRF token at %s: %d", p, r.Status)
		}
	}
	if e.siteSnapshot() != before || len(mustList(t, e)) != 1 {
		t.Error("none of those may change or create anything")
	}
	if page := admin.get("/admin/backup").Body; !strings.Contains(page, "Download a backup") || !strings.Contains(page, name) || !strings.Contains(page, `href="/admin/backup"`) {
		t.Error("the screen lists the saved backups")
	}
	if editorPage := editor.get("/admin/"); strings.Contains(editorPage.Body, "/admin/backup") {
		t.Error("editors don't see the Backup link")
	}

	// file names are checked: nothing outside the backup folder can be read or removed
	for _, bad := range []string{"..%2f..%2fgo.mod", "evil.zip", "manual-1.zip", "manual-20260101-000000.zip.bak", "incoming", "%2e%2e%2fbackups%2f" + name} {
		if r := admin.get("/admin/backup/files/" + bad); r.Status == 200 || strings.HasPrefix(r.Body, "PK") {
			t.Errorf("GET file %q: %d", bad, r.Status)
		}
		admin.post("/admin/backup/files/"+bad+"/delete", nil)
	}
	if len(mustList(t, e)) != 1 {
		t.Error("a bad name must not delete anything")
	}
}

func TestSavedBackupsAreListedDownloadedAndDeleted(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	buildSite(t, e, admin)

	if r := admin.post("/admin/backup/save", nil); r.Status != 303 || !strings.Contains(r.Location(), "done=saved") {
		t.Fatalf("save: %d", r.Status)
	}
	admin.post("/admin/backup/save", url.Values{"personal": {"1"}})
	files := mustList(t, e)
	if len(files) != 2 || files[0].Kind != "manual" || files[0].Size < 1000 {
		t.Fatalf("files: %+v", files)
	}
	page := admin.get("/admin/backup").Body
	for _, f := range files {
		if !strings.Contains(page, f.Name) || !strings.Contains(page, f.SizeLabel()) {
			t.Errorf("the page lacks %s", f.Name)
		}
	}
	d := admin.get("/admin/backup/files/" + files[1].Name)
	if d.Status != 200 || d.Header.Get("Content-Type") != "application/zip" || !strings.Contains(d.Header.Get("Content-Disposition"), files[1].Name) {
		t.Errorf("download: %d %v", d.Status, d.Header)
	}
	if got := unzip(t, []byte(d.Body)); got["site.json"] == nil || got["manifest.json"] == nil {
		t.Error("a saved backup is a valid backup")
	}
	if r := admin.post("/admin/backup/files/"+files[1].Name+"/delete", nil); r.Status != 303 {
		t.Errorf("delete: %d", r.Status)
	}
	if got := mustList(t, e); len(got) != 1 || got[0].Name != files[0].Name {
		t.Errorf("after deleting: %+v", got)
	}
	if r := admin.get("/admin/backup/files/" + files[1].Name); r.Status != 404 {
		t.Errorf("deleted file: %d", r.Status)
	}
	// no temporary files are left in the backup folder
	entries, _ := os.ReadDir(filepath.Join(filepath.Dir(e.mediaDir), "backups"))
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), ".tmp") {
			t.Errorf("leftover %s", en.Name())
		}
	}
}

func TestScheduledBackupsAreMadeAndPruned(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	buildSite(t, e, admin)
	b := e.app.Backups()
	clock := time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC)
	b.Now = func() time.Time { return clock }
	run := func(keep int, personal bool) {
		t.Helper()
		if err := b.Housekeep(context.Background(), keep, personal); err != nil {
			t.Fatal(err)
		}
	}
	kinds := func(kind string) []string {
		var out []string
		for _, f := range mustList(t, e) {
			if f.Kind == kind {
				out = append(out, f.Name)
			}
		}
		return out
	}

	run(3, false)
	if len(kinds("auto")) != 1 {
		t.Fatalf("first run makes one: %v", kinds("auto"))
	}
	run(3, false)
	clock = clock.Add(2 * time.Hour)
	run(3, false)
	if len(kinds("auto")) != 1 {
		t.Errorf("one a day, not one an hour: %v", kinds("auto"))
	}
	for i := 0; i < 4; i++ {
		clock = clock.Add(24 * time.Hour)
		run(3, false)
	}
	autos := kinds("auto")
	if len(autos) != 3 || autos[0] != "auto-20260505-050000.zip" || autos[2] != "auto-20260503-050000.zip" {
		t.Errorf("the newest three are kept: %v", autos)
	}

	// scheduled backups hold no personal data unless asked
	e.subscribe("ann@example.com", true)
	clock = clock.Add(24 * time.Hour)
	run(3, false)
	f, _, err := b.Open(kinds("auto")[0])
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(f)
	f.Close()
	if bytes.Contains(unzip(t, data)["site.json"], []byte("ann@example.com")) {
		t.Error("scheduled backups leave out personal data by default")
	}
	clock = clock.Add(24 * time.Hour)
	run(3, true)
	f, _, _ = b.Open(kinds("auto")[0])
	data, _ = io.ReadAll(f)
	f.Close()
	if !bytes.Contains(unzip(t, data)["site.json"], []byte("ann@example.com")) {
		t.Error("BACKUP_PERSONAL puts it in")
	}

	// manual backups are never pruned; turning scheduling off keeps what exists
	b.Save(context.Background(), backup.KindManual, false)
	clock = clock.Add(5 * 24 * time.Hour)
	run(0, false)
	if len(kinds("auto")) != 3 || len(kinds("manual")) != 1 {
		t.Errorf("keep=0 makes no new backup and removes none: auto %v manual %v", kinds("auto"), kinds("manual"))
	}

	// only the newest few safety copies are kept
	for i := 0; i < 5; i++ {
		clock = clock.Add(time.Hour)
		b.Save(context.Background(), backup.KindPreRestore, false)
	}
	run(0, false)
	if got := kinds("pre-restore"); len(got) != 3 {
		t.Errorf("pre-restore copies: %v", got)
	}

	// two backups in the same second get different names
	clock = clock.Add(time.Hour)
	f1, _ := b.Save(context.Background(), backup.KindManual, false)
	f2, _ := b.Save(context.Background(), backup.KindManual, false)
	if f1.Name == f2.Name || f1.Name == "" {
		t.Errorf("names: %s %s", f1.Name, f2.Name)
	}

	// uploads nobody confirmed are cleared after an hour
	token, _, err := b.Stage(bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	run(0, false)
	if fh, _, err := b.OpenStaged(token); err != nil {
		t.Error("a fresh upload is kept")
	} else {
		fh.Close()
	}
	b.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	run(0, false)
	if _, _, err := b.OpenStaged(token); err == nil {
		t.Error("an old upload is removed")
	}
}

func TestBackupOfASiteWithAMissingImageFile(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	img1, img2 := buildSite(t, e, admin)
	os.Remove(e.file(img1.URL)) // the file vanished from the disk, the library still lists it

	raw := []byte(admin.downloadBackup(false).Body)
	files := unzip(t, raw)
	var man backup.Manifest
	json.Unmarshal(files["manifest.json"], &man)
	if len(man.Missing) != 1 || "/media/"+man.Missing[0] != img1.URL || man.Counts.Media != 1 || files["media/"+strings.TrimPrefix(img1.URL, "/media/")] != nil {
		t.Errorf("manifest: %+v", man)
	}
	rv := admin.uploadBackup(raw)
	if !strings.Contains(rv.Body, "was missing from the site") {
		t.Error("the review mentions the missing image")
	}
	if r := admin.restore(reSource.FindStringSubmatch(rv.Body)[1], nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if got := e.mediaPaths(); len(got) != 1 || "/media/"+got[0] != img2.URL {
		t.Errorf("only the image that could be saved is restored: %v", got)
	}
}
