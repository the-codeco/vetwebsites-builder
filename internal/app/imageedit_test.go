package app_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func editForm(over map[string]string) url.Values {
	v := url.Values{"rotate": {"0"}, "max_width": {"0"}}
	for k, val := range over {
		v.Set(k, val)
	}
	return v
}

func TestImageEditor(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	orig := admin.uploadOne(t, "holiday.jpg", "image/jpeg", jpegImage(800, 400))
	id := strconv.FormatInt(orig.ID, 10)
	admin.post("/admin/media/"+id, url.Values{"alt": {"A beach"}})

	// the library links to the editor, and the editor shows the picture
	if lib := admin.get("/admin/media").Body; !strings.Contains(lib, `href="/admin/media/`+id+`/edit"`) {
		t.Error("the library should link to the editor")
	}
	page := admin.get("/admin/media/" + id + "/edit")
	for _, want := range []string{`id="ie-canvas"`, `data-w="800"`, `data-h="400"`, `data-src="` + orig.URL + `"`, `name="x"`, `name="max_width"`, `/static/imageedit.js`, `holiday.jpg`} {
		if page.Status != 200 || !strings.Contains(page.Body, want) {
			t.Errorf("editor page lacks %s (status %d)", want, page.Status)
		}
	}
	if strings.Contains(page.Body, "style=") {
		t.Error("the admin's content security policy forbids inline styles")
	}
	if js := anon.get("/static/imageedit.js"); js.Status != 200 || !strings.Contains(js.Body, "WBCrop") {
		t.Errorf("editor script: %d", js.Status)
	}

	newest := func() (int64, string) {
		var n int64
		var name string
		row := e.pool.QueryRow(t.Context(), `SELECT id, original_name FROM media ORDER BY id DESC LIMIT 1`)
		row.Scan(&n, &name)
		return n, name
	}
	count := func() string { return e.sql(`SELECT count(*) FROM media`) }

	// turn, crop and shrink: a new image, and the original is untouched
	r := admin.post("/admin/media/"+id+"/edit", editForm(map[string]string{
		"rotate": "90", "x": "10", "y": "20", "w": "200", "h": "300", "max_width": "100",
	}))
	if r.Status != 303 || !strings.Contains(r.Location(), "/admin/media?done=edited") {
		t.Fatalf("edit: %d %s", r.Status, r.Body)
	}
	nid, name := newest()
	if nid == orig.ID || count() != "2" {
		t.Fatalf("an edit makes a new image (now %s)", count())
	}
	if name != "holiday (edited).jpg" {
		t.Errorf("name: %q", name)
	}
	// the turned image is 400×800; the crop is 200×300; shrunk to 100 wide it is 100×150
	if got := e.sql(`SELECT width || 'x' || height FROM media WHERE id = $1`, nid); got != "100x150" {
		t.Errorf("edited size %s", got)
	}
	if got := e.sql(`SELECT alt FROM media WHERE id = $1`, nid); got != "A beach" {
		t.Errorf("the copy keeps the alt text: %q", got)
	}
	if got := e.sql(`SELECT width || 'x' || height FROM media WHERE id = $1`, orig.ID); got != "800x400" {
		t.Errorf("the original must not change: %s", got)
	}
	var newPath string
	e.pool.QueryRow(t.Context(), `SELECT path FROM media WHERE id = $1`, nid).Scan(&newPath)
	if w, h := dims(t, e.stored(newPath)); w != 100 || h != 150 {
		t.Errorf("stored copy is %d×%d", w, h)
	}
	if !strings.HasSuffix(newPath, ".jpg") {
		t.Errorf("a JPEG stays a JPEG: %s", newPath)
	}
	if lib := admin.get("/admin/media?done=edited").Body; !strings.Contains(lib, "Saved as a new image") {
		t.Error("the library should confirm")
	}

	// the crop really cuts out the right part: the picture's left half is red, the right half blue
	two := admin.uploadOne(t, "halves.png", "image/png", halvesPNG(200, 100))
	tid := strconv.FormatInt(two.ID, 10)
	admin.post("/admin/media/"+tid+"/edit", editForm(map[string]string{"x": "120", "y": "0", "w": "80", "h": "100"}))
	rid, _ := newest()
	var p2 string
	e.pool.QueryRow(t.Context(), `SELECT path FROM media WHERE id = $1`, rid).Scan(&p2)
	if c := pixel(t, e.stored(p2), 10, 50); c != [3]uint8{0, 0, 255} {
		t.Errorf("the right-hand crop should be blue, got %v", c)
	}
	admin.post("/admin/media/"+tid+"/edit", editForm(map[string]string{"flip_h": "1"}))
	fid, _ := newest()
	e.pool.QueryRow(t.Context(), `SELECT path FROM media WHERE id = $1`, fid).Scan(&p2)
	if c := pixel(t, e.stored(p2), 10, 50); c != [3]uint8{0, 0, 255} {
		t.Errorf("a mirrored image has blue on the left, got %v", c)
	}
	if !strings.HasSuffix(p2, ".png") {
		t.Errorf("a PNG stays a PNG: %s", p2)
	}

	// no crop and nothing else asked for is allowed: it makes a plain copy
	before := count()
	if r := admin.post("/admin/media/"+id+"/edit", editForm(nil)); r.Status != 303 {
		t.Errorf("a plain copy: %d", r.Status)
	}
	if count() == before {
		t.Error("expected a copy")
	}

	// mistakes are explained and nothing is saved
	before = count()
	for name, v := range map[string]struct {
		over map[string]string
		msg  string
	}{
		"three of four":   {map[string]string{"x": "0", "y": "0", "w": "100"}, "all four"},
		"outside":         {map[string]string{"x": "700", "y": "0", "w": "200", "h": "100"}, "outside the image"},
		"outside, turned": {map[string]string{"rotate": "90", "x": "0", "y": "0", "w": "800", "h": "400"}, "outside the image"},
		"too small":       {map[string]string{"x": "0", "y": "0", "w": "5", "h": "100"}, "at least 16"},
		"not numbers":     {map[string]string{"x": "a", "y": "0", "w": "100", "h": "100"}, "whole numbers"},
		"negative":        {map[string]string{"x": "-5", "y": "0", "w": "100", "h": "100"}, "whole numbers"},
		"odd turn":        {map[string]string{"rotate": "45"}, "0, 90, 180 or 270"},
		"no turn value":   {map[string]string{"rotate": ""}, "0, 90, 180 or 270"},
		"bad size":        {map[string]string{"max_width": "-1"}, "can be shrunk to"},
		"huge size":       {map[string]string{"max_width": "99999"}, "can be shrunk to"},
		"size not number": {map[string]string{"max_width": "wide"}, "can be shrunk to"},
	} {
		r := admin.post("/admin/media/"+id+"/edit", editForm(v.over))
		if r.Status != 400 || !strings.Contains(r.Body, v.msg) {
			t.Errorf("%s: %d, want a message with %q", name, r.Status, v.msg)
		}
		if !strings.Contains(r.Body, `id="ie-canvas"`) {
			t.Errorf("%s: the editor should be shown again", name)
		}
	}
	if count() != before {
		t.Error("a rejected edit must not save anything")
	}
	// the numbers typed in are kept when the form comes back
	r = admin.post("/admin/media/"+id+"/edit", editForm(map[string]string{"x": "5", "y": "6", "w": "7", "flip_v": "1", "rotate": "180"}))
	for _, want := range []string{`name="x" min="0" value="5"`, `name="y" min="0" value="6"`, `name="flip_v" value="1" checked`, `value="180" selected`} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("the rejected form should keep %s", want)
		}
	}

	// GIFs can't be edited (they may be animated), and missing images are not found
	gif := admin.uploadOne(t, "anim.gif", "image/gif", gifImage())
	gid := strconv.FormatInt(gif.ID, 10)
	if r := admin.get("/admin/media/" + gid + "/edit"); r.Status != 400 || !strings.Contains(r.Body, "GIFs can") {
		t.Errorf("gif page: %d", r.Status)
	}
	if r := admin.post("/admin/media/"+gid+"/edit", editForm(nil)); r.Status != 400 {
		t.Errorf("gif edit: %d", r.Status)
	}
	if lib := admin.get("/admin/media").Body; strings.Contains(lib, `/admin/media/`+gid+`/edit`) {
		t.Error("no edit link for a GIF")
	}
	for _, p := range []string{"/admin/media/999999/edit", "/admin/media/abc/edit"} {
		if r := admin.get(p); r.Status != 404 {
			t.Errorf("GET %s: %d", p, r.Status)
		}
		if r := admin.post(p, editForm(nil)); r.Status != 404 {
			t.Errorf("POST %s: %d", p, r.Status)
		}
	}

	// editors may edit images (they manage the media library); visitors may not, and a forged request has no token
	if r := editor.post("/admin/media/"+id+"/edit", editForm(nil)); r.Status != 303 {
		t.Errorf("an editor: %d", r.Status)
	}
	if r := anon.get("/admin/media/" + id + "/edit"); !strings.Contains(r.Location(), "/admin/login") {
		t.Errorf("a visitor: %d %q", r.Status, r.Location())
	}
	if r := anon.post("/admin/media/"+id+"/edit", editForm(nil)); !strings.Contains(r.Location(), "/admin/login") {
		t.Errorf("a visitor posting: %d %q", r.Status, r.Location())
	}
	before = count()
	if r := admin.postNoToken("/admin/media/"+id+"/edit", editForm(nil)); r.Status != 403 || count() != before {
		t.Errorf("no CSRF token: %d", r.Status)
	}
}
