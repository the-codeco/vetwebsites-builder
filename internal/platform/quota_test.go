package platform_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"testing"

	"webbuilder/internal/config"
)

// noise is a picture that does not compress, so its file size is about w*h*3 bytes.
func noise(w, h int) []byte {
	r := rand.New(rand.NewSource(7))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	r.Read(img.Pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// tryUpload sends one picture and returns the response as the library's script sees it.
func (c *client) tryUpload(name string, data []byte) (status int, uploaded int, errs []string) {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename=%q`, name))
	h.Set("Content-Type", "image/png")
	part, _ := mw.CreatePart(h)
	part.Write(data)
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+"/admin/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	r := c.do(req, []string{"X-CSRF-Token", c.token, "Accept", "application/json"})
	var out struct {
		Uploaded []struct{ URL string } `json:"uploaded"`
		Errors   []string               `json:"errors"`
	}
	json.Unmarshal([]byte(r.Body), &out)
	return r.Status, len(out.Uploaded), out.Errors
}

// A website can only hold so much: the server's defaults, or limits the platform admin gave it.
func TestWebsiteLimits(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.QuotaPages, c.QuotaPosts, c.QuotaStorageMB = 4, 2, 1 })
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	bob := e.customer(admin, "bob@b.example", "Beta Bakery")

	// pages: a new website starts with a few, and can add up to the limit
	for i, slug := range []string{"one", "two"} {
		if r := ann.post("/admin/pages", url.Values{"title": {slug}}); r.Status != 303 {
			t.Fatalf("page %d within the limit: %d %s", i, r.Status, firstLine(r.Body))
		}
	}
	r := ann.post("/admin/pages", url.Values{"title": {"three"}})
	if r.Status != 400 || !strings.Contains(r.Body, "limit of 4 pages") {
		t.Errorf("a page over the limit: %d %s", r.Status, firstLine(r.Body))
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.pages`); got != "4" {
		t.Errorf("pages: %s", got)
	}
	// another website has its own room
	if r := bob.post("/admin/pages", url.Values{"title": {"fresh"}}); r.Status != 303 {
		t.Errorf("the other website is not affected: %d", r.Status)
	}

	// posts
	for _, title := range []string{"first", "second"} {
		if r := ann.post("/admin/posts", url.Values{"title": {title}}); r.Status != 303 {
			t.Fatalf("post within the limit: %d", r.Status)
		}
	}
	if r := ann.post("/admin/posts", url.Values{"title": {"third"}}); r.Status != 400 || !strings.Contains(r.Body, "limit of 2 blog posts") {
		t.Errorf("a post over the limit: %d", r.Status)
	}

	// pictures: 1 MB of storage
	if st, n, errs := ann.tryUpload("a.png", noise(300, 300)); st != 200 || n != 1 {
		t.Fatalf("a picture within the limit: %d %v", st, errs)
	}
	st, n, errs := ann.tryUpload("b.png", noise(300, 300)) // 2 x 270 KB: still room
	if st != 200 || n != 1 {
		t.Fatalf("second picture: %d %v", st, errs)
	}
	st, n, errs = ann.tryUpload("c.png", noise(500, 500)) // another 750 KB: over 1 MB
	if st != 400 || n != 0 || len(errs) != 1 || !strings.Contains(errs[0], "picture storage is full") {
		t.Errorf("a picture over the limit: %d %d %v", st, n, errs)
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.media`); got != "2" {
		t.Errorf("a refused picture must not be stored: %s", got)
	}
	// editing makes a new picture, so a full website cannot edit either
	e.pool.Exec(t.Context(), `UPDATE sites SET quota_storage_mb = 1 WHERE name = 'Acme Dental'`)
	e.pool.Exec(t.Context(), `UPDATE site_1.media SET size_bytes = 1100000 WHERE id = (SELECT min(id) FROM site_1.media)`)
	id := e.scalar(`SELECT min(id) FROM site_1.media`)
	if r := ann.post("/admin/media/"+id+"/edit", url.Values{"rotate": {"90"}, "max_width": {"0"}}); r.Status != 400 || !strings.Contains(r.Body, "storage is full") {
		t.Errorf("editing a picture when full: %d", r.Status)
	}

	// the customer sees where they stand, and who asks for more
	dash := ann.get("/admin/").Body
	for _, want := range []string{"Your plan", "Pages", "4 of 4", "Blog posts", "2 of 2", "Picture storage", "Full", "Need more room?"} {
		if !strings.Contains(dash, want) {
			t.Errorf("the dashboard lacks %q", want)
		}
	}

	// the platform admin sees every website's usage and can change a limit; the change applies at once
	list := admin.get("/platform/sites").Body
	if !strings.Contains(list, "4 of 4 pages") || !strings.Contains(list, "2 of 2 posts") || !strings.Contains(list, "of 1 MB of pictures") {
		t.Errorf("the websites list should show usage against limits:\n%s", list)
	}
	if r := admin.post("/platform/sites/1/limits", url.Values{"quota_pages": {"6"}, "quota_posts": {"0"}, "quota_storage_mb": {""}}); r.Status != 303 {
		t.Fatalf("set limits: %d %s", r.Status, firstLine(r.Body))
	}
	if r := ann.post("/admin/pages", url.Values{"title": {"five"}}); r.Status != 303 {
		t.Errorf("a raised limit applies at once: %d", r.Status)
	}
	for i := 0; i < 3; i++ {
		if r := ann.post("/admin/posts", url.Values{"title": {fmt.Sprint("more", i)}}); r.Status != 303 {
			t.Errorf("0 means no limit: %d", r.Status)
		}
	}
	if got := e.scalar(`SELECT quota_storage_mb IS NULL FROM sites WHERE name = 'Acme Dental'`); got != "true" {
		t.Errorf("blank goes back to the server's default: %s", got)
	}
	if got := e.scalar(`SELECT count(*) FROM audit_log WHERE action = 'changed the limits'`); got != "1" {
		t.Errorf("limit changes are logged: %s", got)
	}
	// bad input is refused, and customers cannot set their own
	if r := admin.post("/platform/sites/1/limits", url.Values{"quota_pages": {"lots"}}); r.Status != 400 {
		t.Errorf("not a number: %d", r.Status)
	}
	if r := admin.post("/platform/sites/1/limits", url.Values{"quota_pages": {"-3"}}); r.Status != 400 {
		t.Errorf("negative: %d", r.Status)
	}
	if r := ann.post("/platform/sites/1/limits", url.Values{"quota_pages": {"0"}}); r.Status == 303 {
		t.Error("a customer raised their own limit")
	}
	if got := e.scalar(`SELECT quota_pages FROM sites WHERE name = 'Acme Dental'`); got != "6" {
		t.Errorf("limit after a refused change: %s", got)
	}
}
