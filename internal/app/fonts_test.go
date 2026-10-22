package app_test

import (
	"bytes"
	"encoding/binary"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// fakeWOFF2 is bytes that look like a WOFF2 file as far as the program checks: the signature, and a length
// field that matches the size. (The browser does the real parsing.)
func fakeWOFF2(size int, fill byte) []byte {
	b := bytes.Repeat([]byte{fill}, size)
	copy(b, "wOF2")
	binary.BigEndian.PutUint32(b[8:12], uint32(size))
	return b
}

func (c *client) uploadFont(family, weight, name string, data []byte, token ...string) response {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	tok := c.token
	if len(token) > 0 {
		tok = token[0]
	}
	mw.WriteField("csrf", tok)
	mw.WriteField("family", family)
	mw.WriteField("weight", weight)
	if name != "" {
		part, _ := mw.CreateFormFile("file", name)
		part.Write(data)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+"/admin/appearance/fonts", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(req, nil)
}

var reFontURL = regexp.MustCompile(`url\("(/fonts/[0-9]+-[0-9a-f]{10}\.woff2)"\)`)

func TestUploadedFonts(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	card := admin.get("/admin/appearance").Body
	if !strings.Contains(card, `action="/admin/appearance/fonts"`) || !strings.Contains(card, `enctype="multipart/form-data"`) || !strings.Contains(card, `name="weight"`) {
		t.Error("the fonts card")
	}
	css := anon.get("/site.css").Body
	if strings.Contains(css, "@font-face") {
		t.Error("no fonts, no @font-face")
	}

	regular, bold := fakeWOFF2(2000, 'a'), fakeWOFF2(3000, 'b')
	if r := admin.uploadFont("  Acme   Sans ", "400", "acme.woff2", regular); r.Status != 303 || !strings.HasSuffix(r.Header.Get("Location"), "done=font") {
		t.Fatalf("upload regular: %d %s", r.Status, r.Body)
	}
	if r := admin.uploadFont("Acme Sans", "700", "acme-bold.woff2", bold); r.Status != 303 {
		t.Fatalf("upload bold: %d", r.Status)
	}
	card = admin.get("/admin/appearance").Body
	for _, want := range []string{"<td>Acme Sans</td>", "<td>Regular</td>", "<td>Bold</td>", `value="custom:acme-sans"`, "Acme Sans (uploaded)"} {
		if !strings.Contains(card, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	// use it for headings
	if r := admin.post("/admin/appearance/tokens", url.Values{"t.font-heading": {"custom:acme-sans"}}); r.Status != 303 {
		t.Fatalf("use it: %d", r.Status)
	}
	css = anon.get("/site.css").Body
	faces := reFontURL.FindAllStringSubmatch(css, -1)
	if len(faces) != 2 {
		t.Fatalf("two font faces expected:\n%s", css)
	}
	for _, want := range []string{
		`font-family: "Acme Sans"; font-weight: 400; font-style: normal; font-display: swap;`, `font-family: "Acme Sans"; font-weight: 700;`,
		`--font-heading: "Acme Sans", system-ui, -apple-system,`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("site.css lacks %s", want)
		}
	}
	// served from this site, cacheable for good, and nothing else
	r := anon.get(faces[0][1])
	if r.Status != 200 || r.Header.Get("Content-Type") != "font/woff2" || !strings.Contains(r.Header.Get("Cache-Control"), "immutable") || r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Body != string(regular) {
		t.Errorf("the font file: %d %q %q", r.Status, r.Header.Get("Content-Type"), r.Header.Get("Cache-Control"))
	}
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("the file carries a locked-down policy")
	}
	for _, p := range []string{"/fonts/1-0000000000.woff2", "/fonts/999-" + strings.Repeat("a", 10) + ".woff2", "/fonts/abc.woff2", "/fonts/1.woff2", "/fonts/1-short.woff2", "/fonts/1-" + strings.Repeat("a", 10) + ".woff", "/fonts/../x"} {
		if r := anon.get(p); r.Status != 404 && r.Status != 301 && r.Status != 307 {
			t.Errorf("%s: %d", p, r.Status)
		}
	}
	idHash := strings.TrimSuffix(strings.TrimPrefix(faces[0][1], "/fonts/"), ".woff2")
	wrong := idHash[:len(idHash)-10] + strings.Repeat("0", 10)
	if r := anon.get("/fonts/" + wrong + ".woff2"); r.Status != 404 {
		t.Errorf("a wrong hash: %d", r.Status)
	}
	// replacing a file gives it a new address
	if r := admin.uploadFont("acme sans", "400", "acme2.woff2", fakeWOFF2(2500, 'c')); r.Status != 303 {
		t.Fatalf("replace: %d", r.Status)
	}
	if got := e.sql(`SELECT count(*) FROM fonts`); got != "2" {
		t.Errorf("the same family and weight is replaced, whatever the case: %s", got)
	}
	css = anon.get("/site.css").Body
	if now := reFontURL.FindAllStringSubmatch(css, -1); len(now) != 2 || (now[0][1] == faces[0][1] && now[1][1] == faces[0][1]) {
		t.Error("a replaced file has a new address")
	}
	if r := anon.get(faces[0][1]); r.Status != 404 && r.Body == string(regular) {
		t.Error("the old copy is gone")
	}

	// refusals
	for name, c := range map[string]struct {
		family, weight, file string
		data                 []byte
	}{
		"not woff2":       {"Good", "400", "a.woff2", []byte(strings.Repeat("x", 2000))},
		"woff1":           {"Good", "400", "a.woff", append([]byte("wOFF"), make([]byte, 2000)...)},
		"wrong signature": {"Good", "400", "a.woff2", func() []byte { b := fakeWOFF2(2000, 1); copy(b, "wOFF"); return b }()},
		"wrong length":    {"Good", "400", "a.woff2", func() []byte { b := fakeWOFF2(2000, 'a'); binary.BigEndian.PutUint32(b[8:12], 5); return b }()},
		"too small":       {"Good", "400", "a.woff2", fakeWOFF2(100, 'a')},
		"too big":         {"Good", "400", "a.woff2", fakeWOFF2(600<<10+1, 'a')},
		"html as font":    {"Good", "400", "a.woff2", []byte("<script>alert(1)</script>" + strings.Repeat(" ", 2000))},
		"no file":         {"Good", "400", "", nil},
		"empty name":      {"", "400", "a.woff2", regular},
		"quote in name":   {`A"} body{display:none} @font-face{x:"`, "400", "a.woff2", regular},
		"long name":       {strings.Repeat("a", 41), "400", "a.woff2", regular},
		"bad weight":      {"Good", "600", "a.woff2", regular},
		"script in name":  {"<script>", "400", "a.woff2", regular},
		"semicolon":       {"A;B", "400", "a.woff2", regular},
	} {
		if r := admin.uploadFont(c.family, c.weight, c.file, c.data); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if got := e.sql(`SELECT count(*) FROM fonts`); got != "2" {
		t.Errorf("refused uploads saved nothing: %s", got)
	}
	if r := editor.uploadFont("Editor Font", "400", "a.woff2", regular); r.Status != 403 {
		t.Errorf("an editor cannot upload fonts: %d", r.Status)
	}
	if r := anon.uploadFont("Anon Font", "400", "a.woff2", regular, ""); r.Status == 303 && strings.Contains(r.Header.Get("Location"), "done=font") {
		t.Error("logged out")
	}
	if r := admin.uploadFont("No Token", "400", "a.woff2", regular, "wrong"); r.Status != 403 {
		t.Errorf("without a valid CSRF token: %d", r.Status)
	}
	// the list is limited: 12 files in all
	e.pool.Exec(t.Context(), `INSERT INTO fonts (family, weight, data, hash) SELECT 'F'||g, 400, '\x00', 'h' FROM generate_series(1, 10) g`)
	if r := admin.uploadFont("One Too Many", "400", "a.woff2", regular); r.Status != 400 || !strings.Contains(r.Body, "at most 12") {
		t.Errorf("full: %d", r.Status)
	}
	e.pool.Exec(t.Context(), `DELETE FROM fonts WHERE family LIKE 'F%' AND family <> 'F'`)

	// a setting that names a font that is not there is refused; one saved earlier falls back
	if r := admin.post("/admin/appearance/tokens", url.Values{"t.font-body": {"custom:nothing-here"}}); r.Status != 400 {
		t.Errorf("an unknown uploaded font: %d", r.Status)
	}
	if r := admin.post("/admin/appearance/tokens", url.Values{"t.font-body": {"custom:Bad;Value"}}); r.Status != 400 {
		t.Errorf("a malformed font value: %d", r.Status)
	}
	id := e.sql(`SELECT id FROM fonts WHERE weight = 400`)
	if r := admin.post("/admin/appearance/fonts/"+id+"/delete", url.Values{}); r.Status != 303 {
		t.Errorf("delete: %d", r.Status)
	}
	e.pool.Exec(t.Context(), `DELETE FROM fonts`)
	css = anon.get("/site.css").Body
	if strings.Contains(css, "Acme Sans") || strings.Contains(css, "@font-face") || !strings.Contains(css, "--font-heading: system-ui") {
		t.Errorf("with the font gone the setting falls back to the theme's font:\n%s", css)
	}
	if r := admin.post("/admin/appearance/fonts/999999/delete", url.Values{}); r.Status != 404 {
		t.Errorf("no such font: %d", r.Status)
	}
	if r := editor.post("/admin/appearance/fonts/1/delete", url.Values{}); r.Status != 403 {
		t.Errorf("an editor cannot delete fonts: %d", r.Status)
	}
}

func TestFontsSurviveABackupRestore(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	regular := fakeWOFF2(2000, 'r')
	admin.uploadFont("Acme Sans", "400", "a.woff2", regular)
	admin.post("/admin/appearance/tokens", url.Values{"t.font-body": {"custom:acme-sans"}})
	if r := admin.restoreUpload(t, []byte(admin.downloadBackup(false).Body), nil); r.Status != 200 {
		t.Fatalf("settle: %d", r.Status)
	}
	raw := []byte(admin.downloadBackup(false).Body)
	if !strings.Contains(string(unzip(t, raw)["site.json"]), `"family": "Acme Sans"`) {
		t.Error("the backup holds the font")
	}
	e.pool.Exec(t.Context(), `DELETE FROM fonts`)
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if got := e.sql(`SELECT family||weight||octet_length(data) FROM fonts`); got != "Acme Sans4002000" {
		t.Errorf("restored: %q", got)
	}
	faces := reFontURL.FindStringSubmatch(anon.get("/site.css").Body)
	if faces == nil {
		t.Fatal("the restored font is in the stylesheet")
	}
	if r := anon.get(faces[1]); r.Status != 200 || r.Body != string(regular) {
		t.Errorf("the restored font file: %d", r.Status)
	}
}
