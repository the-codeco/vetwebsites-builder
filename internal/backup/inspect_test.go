package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/footer"
	"webbuilder/internal/menus"
	"webbuilder/internal/themes"
)

// These tests feed Inspect backup files that are damaged, tampered with or hostile. It needs no database:
// a file is checked before anything is stored.

func testService(t *testing.T) *Service {
	t.Helper()
	e, err := themes.Load()
	if err != nil {
		t.Fatal(err)
	}
	return &Service{engine: e, Now: time.Now}
}

const imgPath = "2026/10/0123456789abcdef01234567.jpg"

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 6), uint8(y * 8), 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// goodContent is a small, valid site.
func goodContent() Content {
	return Content{
		Settings: Settings{Title: "Demo", ActiveTheme: "default", Tokens: map[string]map[string]string{"default": {"color-primary": "#aa3300", "dark.color-bg": "#000000"}},
			CustomCSS: "p{color:red}", SEO: map[string]any{"description": "A demo"}, ShowSearch: true, ColorMode: "auto", Consent: map[string]any{"enabled": true}},
		Pages: []Page{{Slug: "home", Title: "Home", Status: "published", SEO: map[string]any{}, UpdatedAt: time.Now().UTC(),
			Blocks: []Block{{Type: "richtext", Props: map[string]any{"content": "<p>Hello</p>"}, Style: map[string]any{}},
				{Type: "image", Props: map[string]any{"image": "/media/" + imgPath, "alt": "x"}, Style: map[string]any{}}}}},
		Posts:      []Post{{Slug: "first", Title: "First", Body: "<p>Body</p>", Status: "published", SEO: map[string]any{}, UpdatedAt: time.Now().UTC(), Categories: []string{"news"}}},
		Categories: []Category{{Slug: "news", Name: "News"}},
		Menus:      []Menu{{Location: "main", Items: []menus.Item{{Kind: "link", Label: "Home", URL: "/"}}}},
		Media:      []Media{{Path: imgPath, OriginalName: "a.jpg", Alt: "an image", CreatedAt: time.Now().UTC()}},
	}
}

// pack builds the ZIP's entries for a content and its images, with a correct manifest.
func pack(t *testing.T, c Content, personal bool, images map[string][]byte) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	site, _ := marshal(c)
	files[SiteName] = site
	man := Manifest{Format: Format, Version: Version, CreatedAt: time.Now().UTC(), SiteTitle: c.Settings.Title, Personal: personal, Files: map[string]string{SiteName: sumHex(site)}}
	for p, data := range images {
		files[MediaPrefix+p] = data
		man.Files[MediaPrefix+p] = sumHex(data)
	}
	files[ManifestName], _ = marshal(man)
	return files
}

func goodFiles(t *testing.T) map[string][]byte {
	return pack(t, goodContent(), false, map[string][]byte{imgPath: jpegBytes(t)})
}

// zipOf writes entries as they are. Names are used verbatim.
func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func inspect(t *testing.T, s *Service, files map[string][]byte) (*Archive, error) {
	t.Helper()
	raw := zipOf(t, files)
	return s.Inspect(t.Context(), bytes.NewReader(raw), int64(len(raw)))
}

// withFixedHashes rebuilds the manifest after a test changed site.json or an image, so the change itself is what is tested.
func withFixedHashes(t *testing.T, files map[string][]byte) map[string][]byte {
	t.Helper()
	var man Manifest
	json.Unmarshal(files[ManifestName], &man)
	man.Files = map[string]string{}
	for name, data := range files {
		if name != ManifestName {
			man.Files[name] = sumHex(data)
		}
	}
	files[ManifestName], _ = marshal(man)
	return files
}

func wantInvalid(t *testing.T, name string, err error, contains string) {
	t.Helper()
	var inv *Invalid
	if err == nil {
		t.Errorf("%s: should be refused", name)
		return
	}
	if !errorAs(err, &inv) {
		t.Errorf("%s: %v is not an *Invalid", name, err)
		return
	}
	if !strings.Contains(inv.Msg, contains) {
		t.Errorf("%s: message %q lacks %q", name, inv.Msg, contains)
	}
}

func errorAs(err error, target **Invalid) bool {
	inv, ok := err.(*Invalid)
	if ok {
		*target = inv
	}
	return ok
}

func TestInspectAcceptsAGoodBackup(t *testing.T) {
	s := testService(t)
	a, err := inspect(t, s, goodFiles(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Warnings) != 0 {
		t.Errorf("warnings: %v", a.Warnings)
	}
	c := a.Manifest.Counts
	if c.Pages != 1 || c.Blocks != 2 || c.Posts != 1 || c.Categories != 1 || c.Media != 1 {
		t.Errorf("counts: %+v", c)
	}
	if a.Content.Settings.Tokens["default"]["color-primary"] != "#aa3300" || a.Content.Settings.Tokens["default"]["dark.color-bg"] != "#000000" {
		t.Errorf("appearance settings were lost: %v", a.Content.Settings.Tokens)
	}
	if info := a.Images[imgPath]; info.Mime != "image/jpeg" || info.Width != 40 || info.Height != 30 {
		t.Errorf("the image is described from its bytes: %+v", info)
	}
	if a.Sizes[imgPath] != int64(len(jpegBytes(t))) {
		t.Errorf("size: %d", a.Sizes[imgPath])
	}
}

func TestInspectRefusesFilesThatAreNotBackups(t *testing.T) {
	s := testService(t)
	raw := func(b []byte) (*Archive, error) { return s.Inspect(t.Context(), bytes.NewReader(b), int64(len(b))) }

	for name, b := range map[string][]byte{
		"empty": {}, "text": []byte("hello, I am not a zip"), "a png": {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
		"cut off": zipOf(t, goodFiles(t))[:200], "an empty zip": zipOf(t, map[string][]byte{}),
	} {
		_, err := raw(b)
		if err == nil {
			t.Errorf("%s accepted", name)
		}
		var inv *Invalid
		if err != nil && !errorAs(err, &inv) {
			t.Errorf("%s: %v should be an *Invalid, for the person choosing the file", name, err)
		}
	}

	// the wrong kind of ZIP
	_, err := inspect(t, s, map[string][]byte{"hello.txt": []byte("hi")})
	wantInvalid(t, "a ZIP of something else", err, "a backup doesn't have")

	f := goodFiles(t)
	delete(f, ManifestName)
	_, err = inspect(t, s, f)
	wantInvalid(t, "no manifest", err, "no manifest.json")

	for name, mod := range map[string]func(*Manifest){
		"other format":   func(m *Manifest) { m.Format = "something-else" },
		"empty format":   func(m *Manifest) { m.Format = "" },
		"newer version":  func(m *Manifest) { m.Version = Version + 1 },
		"version zero":   func(m *Manifest) { m.Version = 0 },
		"version -1":     func(m *Manifest) { m.Version = -1 },
		"version 999999": func(m *Manifest) { m.Version = 999999 },
	} {
		f := goodFiles(t)
		var man Manifest
		json.Unmarshal(f[ManifestName], &man)
		mod(&man)
		f[ManifestName], _ = marshal(man)
		_, err := inspect(t, s, f)
		if err == nil {
			t.Errorf("%s accepted", name)
		}
		if name == "newer version" {
			wantInvalid(t, name, err, "newer version")
		}
	}

	f = goodFiles(t)
	f[ManifestName] = []byte(`{"format":"webbuilder-backup","version":1,"files":{},"surprise":true}`)
	_, err = inspect(t, s, f)
	wantInvalid(t, "unknown manifest field", err, "manifest.json can't be read")
	f[ManifestName] = []byte(`not json`)
	_, err = inspect(t, s, f)
	wantInvalid(t, "manifest not json", err, "manifest.json can't be read")
}

func TestInspectRefusesUnsafeOrUnexpectedEntries(t *testing.T) {
	s := testService(t)
	for _, name := range []string{
		"../evil.sh", "media/../../evil", "/etc/passwd", "media\\2026\\10\\x.jpg", "evil.sh", "media/evil.php", "media/2026/10/short.jpg",
		"media/2026/13/0123456789abcdef01234567.jpg", "media/2026/10/0123456789abcdef01234567.svg", "media/2026/10/0123456789abcdef01234567.jpg/../x",
		"media/2026/10/0123456789ABCDEF01234567.jpg", "site.json.bak", "Site.json", "./site.json", "media//2026/10/0123456789abcdef01234567.jpg",
		"C:/windows/x", "media/2026/10/0123456789abcdef01234567.jpg\x00.php", "..", ".", "nested/manifest.json",
	} {
		f := goodFiles(t)
		f[name] = []byte("x")
		if _, err := inspect(t, s, f); err == nil {
			t.Errorf("an entry named %q was accepted", name)
		}
	}

	// the same name twice
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{ManifestName, SiteName, SiteName} {
		w, _ := zw.Create(n)
		w.Write([]byte("{}"))
	}
	zw.Close()
	_, err := s.Inspect(t.Context(), bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	wantInvalid(t, "duplicate entries", err, "twice")

	// a symbolic link
	buf.Reset()
	zw = zip.NewWriter(&buf)
	for name, data := range goodFiles(t) {
		hdr := &zip.FileHeader{Name: name, Method: zip.Store}
		if name == SiteName {
			hdr.SetMode(os.ModeSymlink | 0o777)
			data = []byte("/etc/passwd")
		}
		w, _ := zw.CreateHeader(hdr)
		w.Write(data)
	}
	zw.Close()
	_, err = s.Inspect(t.Context(), bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	wantInvalid(t, "a symlink", err, "isn't an ordinary file")

	// folders are harmless and ignored
	f := goodFiles(t)
	raw := zipOf(t, f)
	var withDir bytes.Buffer
	zr, _ := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	zw = zip.NewWriter(&withDir)
	zw.Create("media/")
	zw.Create("media/2026/")
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		w, _ := zw.Create(zf.Name)
		var b bytes.Buffer
		b.ReadFrom(rc)
		w.Write(b.Bytes())
		rc.Close()
	}
	zw.Close()
	if _, err := s.Inspect(t.Context(), bytes.NewReader(withDir.Bytes()), int64(withDir.Len())); err != nil {
		t.Errorf("folder entries are ignored: %v", err)
	}
}

func TestInspectRefusesTamperedFiles(t *testing.T) {
	s := testService(t)

	// site.json changed without fixing the manifest
	f := goodFiles(t)
	f[SiteName] = bytes.Replace(f[SiteName], []byte("Demo"), []byte("Dem0"), 1)
	_, err := inspect(t, s, f)
	wantInvalid(t, "site.json changed", err, "checksum")

	// an image changed
	f = goodFiles(t)
	img := append([]byte{}, f[MediaPrefix+imgPath]...)
	img[len(img)/2] ^= 0xff
	f[MediaPrefix+imgPath] = img
	_, err = inspect(t, s, f)
	wantInvalid(t, "image changed", err, "checksum")

	// a file added after the manifest was made
	f = goodFiles(t)
	f[MediaPrefix+"2026/10/aaaaaaaaaaaaaaaaaaaaaaaa.jpg"] = jpegBytes(t)
	_, err = inspect(t, s, f)
	wantInvalid(t, "an extra image", err, "not in its manifest")

	// a file listed but missing
	f = goodFiles(t)
	delete(f, MediaPrefix+imgPath)
	_, err = inspect(t, s, f)
	wantInvalid(t, "a missing image", err, "missing")

	// manifest checksum for a file that doesn't exist
	f = goodFiles(t)
	var man Manifest
	json.Unmarshal(f[ManifestName], &man)
	man.Files["media/2026/10/bbbbbbbbbbbbbbbbbbbbbbbb.jpg"] = strings.Repeat("0", 64)
	f[ManifestName], _ = marshal(man)
	_, err = inspect(t, s, f)
	wantInvalid(t, "a ghost entry", err, "missing")

	// site.json valid-looking but with checksum fixed: unknown fields, trailing data and bad JSON
	for name, body := range map[string]string{
		"unknown field": `{"settings":{},"pages":[],"posts":[],"categories":[],"menus":[],"media":[],"backdoor":1}`,
		"trailing data": `{"settings":{},"pages":[],"posts":[],"categories":[],"menus":[],"media":[]} {"x":1}`,
		"not json":      `<html>`,
		"wrong type":    `{"pages":"yes"}`,
		"null":          `null`,
	} {
		f := goodFiles(t)
		f[SiteName] = []byte(body)
		_, err := inspect(t, s, withFixedHashes(t, f))
		if name == "null" {
			continue // null decodes to an empty site, which is fine
		}
		wantInvalid(t, name, err, "site.json can't be read")
	}

	// personal data the manifest doesn't admit to
	c := goodContent()
	c.Subscribers = []Subscriber{{Email: "a@example.com", CreatedAt: time.Now()}}
	f = pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)})
	_, err = inspect(t, s, f)
	wantInvalid(t, "hidden personal data", err, "personal data")
}

func TestInspectRefusesOversizedAndOverfullFiles(t *testing.T) {
	s := testService(t)

	// a site.json that is mostly spaces compresses to almost nothing but would fill memory
	big := append([]byte(`{"settings":{}}`), bytes.Repeat([]byte(" "), MaxJSONBytes+10)...)
	f := goodFiles(t)
	f[SiteName] = big
	raw := zipOf(t, withFixedHashes(t, f))
	if len(raw) > 1<<20 {
		t.Fatalf("test setup: the bomb should be small, it is %d bytes", len(raw))
	}
	_, err := s.Inspect(t.Context(), bytes.NewReader(raw), int64(len(raw)))
	wantInvalid(t, "a bomb", err, "larger than a backup may hold")

	// an image over the limit
	f = goodFiles(t)
	f[MediaPrefix+imgPath] = append(jpegBytes(t), bytes.Repeat([]byte{0}, MaxMediaBytes)...)
	raw = zipOf(t, withFixedHashes(t, f))
	_, err = s.Inspect(t.Context(), bytes.NewReader(raw), int64(len(raw)))
	wantInvalid(t, "a huge image", err, "larger than a backup may hold")

	// too many files
	f = goodFiles(t)
	for i := 0; i <= MaxEntries; i++ {
		f[fmt.Sprintf("media/2026/10/%024x.jpg", i)] = []byte("x")
	}
	raw = zipOf(t, f)
	_, err = s.Inspect(t.Context(), bytes.NewReader(raw), int64(len(raw)))
	wantInvalid(t, "too many files", err, "too many files")

	// a size claim beyond what the reader may take
	if _, err := s.Inspect(t.Context(), bytes.NewReader(nil), MaxArchiveBytes+1); err == nil {
		t.Error("an archive over the size limit must be refused before it is read")
	}

	// too many pages
	c := goodContent()
	for i := 0; i <= MaxPages; i++ {
		c.Pages = append(c.Pages, Page{Slug: fmt.Sprintf("p%d", i), Title: "x"})
	}
	_, err = inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	wantInvalid(t, "too many pages", err, "more than this site accepts")
}

func TestInspectDropsWhatIsNotValidInsteadOfTrustingIt(t *testing.T) {
	s := testService(t)
	c := goodContent()
	c.Settings.ActiveTheme = "no-such-theme"
	c.Settings.ColorMode = "sideways"
	c.Settings.Title = "   "
	c.Settings.CustomCSS = strings.Repeat("a", 20001)
	c.Settings.SEO = map[string]any{"title": "site title override", "description": strings.Repeat("d", 400), "image": "javascript:alert(1)", "noindex": true, "extra": "x"}
	c.Settings.Consent = map[string]any{"enabled": true, "link_label": "Privacy", "link_url": "javascript:alert(2)", "text": strings.Repeat("t", 500), "evil": 1}
	c.Settings.Footer = map[string]any{"bottom": strings.Repeat("b", 400), "evil": 1, "columns": []any{
		map[string]any{"heading": "  Visit   us ", "text": `<p onclick="x()">Hello <a href="javascript:alert(3)">bad</a> <a href="/ok">ok</a></p><script>alert(4)</script>`},
		map[string]any{"heading": "", "text": "  "}, "junk", map[string]any{"heading": strings.Repeat("h", 100)},
		map[string]any{"heading": "3"}, map[string]any{"heading": "4"}, map[string]any{"heading": "5th is dropped"},
	}}
	c.Settings.Tokens = map[string]map[string]string{
		"default":  {"color-primary": "red;}body{display:none", "color-bg": "#FFEEDD", "dark.color-text": "#ABCDEF", "dark.font-body": "#123456", "dark.nope": "#123456", "nope": "#123456", "radius": "huge", "font-body": "serif", "content-width": "expression(1)"},
		"no-theme": {"color-bg": "#123456"},
		"journal":  {"color-bg": "#102030"},
	}
	c.Pages = []Page{
		{Slug: "admin", Title: "reserved", Status: "published"},
		{Slug: "Bad Slug!", Title: "bad"},
		{Slug: "", Title: "empty"},
		{Slug: "ok", Title: "", Status: "weird", SEO: map[string]any{"image": "data:text/html,x", "description": "fine"},
			Blocks: []Block{
				{Type: "evil-block", Props: map[string]any{"x": 1}},
				{Type: "button", Props: map[string]any{"label": "Go", "url": "javascript:alert(1)", "variant": "neon", "bogus": "x"}, Style: map[string]any{"background": "red;}", "padding": "huge"}},
				{Type: "richtext", Props: map[string]any{"content": `<p onclick="x()">Hi</p><script>alert(1)</script><a href="javascript:alert(2)">bad</a>`}},
			}},
		{Slug: "ok", Title: "duplicate"},
	}
	for i := 0; i < MaxBlocksPerPage+5; i++ {
		c.Pages[3].Blocks = append(c.Pages[3].Blocks, Block{Type: "spacer", Props: map[string]any{}})
	}
	c.Categories = []Category{{Slug: "news", Name: "News"}, {Slug: "news", Name: "Again"}, {Slug: "Bad Slug", Name: "x"}, {Slug: "empty-name", Name: " "}}
	c.Posts = []Post{
		{Slug: "p1", Title: "P1", Status: "published", Body: `<p>x</p><script>alert(1)</script><img src=x onerror=alert(2)>`, Cover: "javascript:alert(3)", Categories: []string{"news", "news", "ghost"}, SEO: map[string]any{"image": "javascript:x"}},
		{Slug: "p1", Title: "dup"},
		{Slug: "Not A Slug", Title: "x"},
		{Slug: "draft-with-date", Title: "D", Status: "draft", PublishedAt: &time.Time{}},
	}
	c.Menus = []Menu{
		{Location: "main", Items: []menus.Item{{Kind: "link", Label: "Bad", URL: "javascript:alert(1)"}}},
		{Location: "evil"},
		{Location: "footer", Items: []menus.Item{{Kind: "link", Label: "OK", URL: "/about"}}, Sticky: true},
		{Location: "footer"},
	}
	c.Media = append(c.Media,
		Media{Path: "2026/10/aaaaaaaaaaaaaaaaaaaaaaaa.jpg", OriginalName: "../../etc/passwd\x00", Alt: strings.Repeat("a", 400)}, // not an image
		Media{Path: "2026/10/bbbbbbbbbbbbbbbbbbbbbbbb.png", OriginalName: "photo.png"})                                           // a jpeg named .png
	images := map[string][]byte{
		imgPath: jpegBytes(t), "2026/10/aaaaaaaaaaaaaaaaaaaaaaaa.jpg": []byte("<script>alert(1)</script>"), "2026/10/bbbbbbbbbbbbbbbbbbbbbbbb.png": jpegBytes(t),
	}
	a, err := inspect(t, s, pack(t, c, false, images))
	if err != nil {
		t.Fatal(err)
	}
	got := a.Content
	st := got.Settings

	// settings
	if st.ActiveTheme != "default" || st.ColorMode != "toggle" || st.Title != "My Site" || st.CustomCSS != "" {
		t.Errorf("settings: %+v", st)
	}
	if _, has := st.SEO["title"]; has || st.SEO["image"] != nil || st.SEO["extra"] != nil || len([]rune(st.SEO["description"].(string))) != 300 || st.SEO["noindex"] != true {
		t.Errorf("site SEO: %v", st.SEO)
	}
	if st.Consent["link_url"] != nil || st.Consent["link_label"] != nil || st.Consent["evil"] != nil || len([]rune(st.Consent["text"].(string))) != 300 {
		t.Errorf("consent: %v", st.Consent)
	}
	// a footer from before the footer builder is upgraded: the (first) footer menu becomes a row of
	// links, and the old columns (at most four, as shown then) a row of text widgets
	foot := footer.FromStored(st.Footer)
	if st.Footer["evil"] != nil || foot.Version != footer.Version || len([]rune(foot.Bottom)) != 300 || len(foot.Rows) != 2 {
		t.Fatalf("footer: %+v", foot)
	}
	if links := foot.Rows[0].Columns[0].Widgets[0]; links.Type != footer.TypeLinks || len(links.Links) != 1 || links.Links[0].URL != "/about" {
		t.Errorf("footer menu: %+v", links)
	}
	cols := foot.Rows[1].Columns
	if len(cols) != 4 || cols[0].Widgets[0].Heading != "Visit us" {
		t.Errorf("footer columns: %+v", cols)
	}
	if txt := cols[0].Widgets[0].Text; strings.Contains(txt, "script") || strings.Contains(txt, "onclick") || strings.Contains(txt, "javascript:") || !strings.Contains(txt, `href="/ok"`) {
		t.Errorf("footer text not cleaned: %s", txt)
	}
	if h := cols[1].Widgets[0].Heading; len([]rune(h)) != 60 {
		t.Errorf("heading limit: %d", len([]rune(h)))
	}
	def := st.Tokens["default"]
	for _, bad := range []string{"color-primary", "dark.font-body", "dark.nope", "nope", "radius", "content-width"} {
		if _, ok := def[bad]; ok {
			t.Errorf("the value for %q should have been dropped: %v", bad, def)
		}
	}
	if def["color-bg"] != "#ffeedd" || def["dark.color-text"] != "#abcdef" || def["font-body"] != "serif" {
		t.Errorf("valid values are kept (colours in lower case): %v", def)
	}
	if _, ok := st.Tokens["no-theme"]; ok || st.Tokens["journal"]["color-bg"] != "#102030" {
		t.Errorf("tokens by theme: %v", st.Tokens)
	}

	// pages
	if len(got.Pages) != 1 || got.Pages[0].Slug != "ok" {
		t.Fatalf("pages: %+v", got.Pages)
	}
	p := got.Pages[0]
	if p.Title != "ok" || p.Status != "draft" || p.SEO["image"] != nil || p.SEO["description"] != "fine" {
		t.Errorf("page: %+v", p)
	}
	if len(p.Blocks) != MaxBlocksPerPage {
		t.Errorf("blocks: %d, want the first %d valid ones", len(p.Blocks), MaxBlocksPerPage)
	}
	btn := p.Blocks[0]
	if btn.Type != "button" || btn.Props["url"] != "" || btn.Props["variant"] != "primary" || btn.Props["bogus"] != nil || btn.Style["background"] != "" || btn.Style["padding"] != "md" {
		t.Errorf("button block: %+v", btn)
	}
	rt := p.Blocks[1].Props["content"].(string)
	if strings.Contains(rt, "script") || strings.Contains(rt, "onclick") || strings.Contains(rt, "javascript:") {
		t.Errorf("rich text: %s", rt)
	}

	// categories and posts
	if len(got.Categories) != 1 || got.Categories[0].Slug != "news" {
		t.Errorf("categories: %+v", got.Categories)
	}
	if len(got.Posts) != 2 {
		t.Fatalf("posts: %+v", got.Posts)
	}
	p1 := got.Posts[0]
	if strings.Contains(p1.Body, "script") || strings.Contains(p1.Body, "onerror") || p1.Cover != "" || p1.SEO["image"] != nil || len(p1.Categories) != 1 || p1.Categories[0] != "news" || p1.PublishedAt == nil {
		t.Errorf("post: %+v", p1)
	}
	if d := got.Posts[1]; d.Status != "draft" || d.PublishedAt == nil || d.PublishedAt.Year() < 2000 {
		t.Errorf("a draft keeps a sane date: %+v", d)
	}

	// menus
	byLoc := map[string]Menu{}
	for _, m := range got.Menus {
		byLoc[m.Location] = m
	}
	if len(got.Menus) != 1 || len(byLoc["main"].Items) != 0 { // the footer menu moved into the footer
		t.Errorf("menus: %+v", got.Menus)
	}

	// images
	if len(got.Media) != 1 || got.Media[0].Path != imgPath {
		t.Errorf("images: %+v", got.Media)
	}
	joined := strings.Join(a.Warnings, "\n")
	for _, want := range []string{"isn't a usable picture", "isn't the kind of file its name says", "no-such-theme", "custom CSS", "appearance settings", "isn't one this site has", "menu wasn't valid"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings lack %q:\n%s", want, joined)
		}
	}
}

func TestInspectCleansPersonalData(t *testing.T) {
	s := testService(t)
	c := goodContent()
	tok := strings.Repeat("a", 43)
	c.Subscribers = []Subscriber{
		{Email: "Ann@Example.com", CreatedAt: time.Now(), UnsubToken: tok},
		{Email: "ann@example.com"}, // duplicate once lower-cased
		{Email: "not an address"},
		{Email: "bob@example.com\r\nBcc: x@y.z"},
		{Email: "cy@example.com", UnsubToken: "short"},
	}
	c.Messages = []Message{
		{Name: "Vera", Email: "vera@example.org", Subject: "Hi\r\nBcc: x@y.z", Body: "Hello", Replies: []Reply{{Body: "Answer"}, {Body: "  "}}},
		{Name: "", Email: "x@example.org", Body: "no name"},
		{Name: "N", Email: "bad", Body: "bad address"},
	}
	a, err := inspect(t, s, pack(t, c, true, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Content.Subscribers) != 2 || a.Content.Subscribers[0].Email != "ann@example.com" || a.Content.Subscribers[0].UnsubToken != tok || a.Content.Subscribers[1].UnsubToken != "" {
		t.Errorf("subscribers: %+v", a.Content.Subscribers)
	}
	if len(a.Content.Messages) != 1 || strings.ContainsAny(a.Content.Messages[0].Subject, "\r\n") || len(a.Content.Messages[0].Replies) != 1 {
		t.Errorf("messages: %+v", a.Content.Messages)
	}
	if a.Manifest.Counts.Subscribers != 2 || a.Manifest.Counts.Messages != 1 {
		t.Errorf("counts say what will be restored, not what the file claims: %+v", a.Manifest.Counts)
	}
}

func TestTheBackupFolderMustNotBeTheMediaFolder(t *testing.T) {
	root := t.TempDir()
	e, _ := themes.Load()
	for name, c := range map[string][2]string{
		"the same":     {root + "/media", root + "/media"},
		"inside":       {root + "/media", root + "/media/backups"},
		"around":       {root + "/media/inner", root + "/media"},
		"with dots":    {root + "/media", root + "/other/../media/x"},
		"empty backup": {root + "/media", ""},
	} {
		if s, err := New(nil, c[0], c[1], e); err == nil {
			s.Close()
			t.Errorf("%s: accepted", name)
		}
	}
	// beside each other is fine, including a similar-looking name
	s, err := New(nil, root+"/media", root+"/media-backups", e)
	if err != nil {
		t.Fatalf("beside: %v", err)
	}
	s.Close()
}

// A picture whose header is fine but whose data is cut short passes a header check; only decoding it shows it is broken.
func TestInspectDecodesEveryImage(t *testing.T) {
	s := testService(t)
	whole := jpegBytes(t)
	c := goodContent()
	c.Media = append(c.Media, Media{Path: "2026/10/dddddddddddddddddddddddd.jpg", OriginalName: "cut.jpg"})
	a, err := inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: whole, "2026/10/dddddddddddddddddddddddd.jpg": whole[:len(whole)-120]}))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Content.Media) != 1 || a.Content.Media[0].Path != imgPath || !strings.Contains(strings.Join(a.Warnings, " "), "isn't a usable picture") {
		t.Errorf("a cut-off picture should be left out with a warning: %+v %v", a.Content.Media, a.Warnings)
	}
}

func TestInspectCleansBlocksInsideLayouts(t *testing.T) {
	s := testService(t)
	spacer := func() Block { return Block{Type: "spacer", Props: map[string]any{}, Style: map[string]any{}} }
	c := goodContent()
	many := make([]Block, 25) // more than one column may hold
	for i := range many {
		many[i] = spacer()
	}
	c.Pages = []Page{
		{Slug: "layouts", Title: "Layouts", Status: "published", Blocks: []Block{
			{Type: "layout", Props: map[string]any{"columns": "3", "ratio": "nonsense"}, Style: map[string]any{}, Children: [][]Block{
				{
					{Type: "richtext", Props: map[string]any{"content": "<p>kept</p><script>alert(1)</script>"}, Style: map[string]any{"background": "red;}"}},
					{Type: "layout", Props: map[string]any{}, Children: [][]Block{{spacer()}}}, // a layout inside a layout
					{Type: "removed-type"},
					{Type: "button", Props: map[string]any{"label": "Go", "url": "javascript:alert(1)"}},
				},
				{},
				many,
				{spacer()}, {spacer()}, // columns the layout does not have
			}},
			{Type: "richtext", Props: map[string]any{"content": "<p>plain</p>"}, Children: [][]Block{{spacer(), spacer()}}}, // children on a block that holds none
		}},
	}
	a, err := inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil {
		t.Fatal(err)
	}
	p := a.Content.Pages[0]
	if len(p.Blocks) != 2 || p.Blocks[0].Type != "layout" || p.Blocks[1].Type != "richtext" {
		t.Fatalf("blocks: %+v", p.Blocks)
	}
	l := p.Blocks[0]
	if len(l.Children) != 3 || l.Props["columns"] != "3" || l.Props["ratio"] != "equal" {
		t.Fatalf("the layout: %+v", l)
	}
	col0 := l.Children[0]
	if len(col0) != 2 || col0[0].Type != "richtext" || col0[1].Type != "button" {
		t.Errorf("a layout in a layout and an unknown block are dropped: %+v", col0)
	}
	if rt := col0[0].Props["content"].(string); strings.Contains(rt, "script") || col0[0].Style["background"] != "" {
		t.Errorf("blocks inside a layout are cleaned like any other: %v %v", col0[0].Props, col0[0].Style)
	}
	if col0[1].Props["url"] != "" {
		t.Errorf("an unsafe link is dropped inside a layout too: %v", col0[1].Props)
	}
	if len(l.Children[1]) != 0 {
		t.Errorf("column 2 stays empty: %v", l.Children[1])
	}
	// 25 blocks fit 20; the two blocks of columns that do not exist join the last column, where there is no room
	if len(l.Children[2]) != 20 {
		t.Errorf("a column holds at most 20: %d", len(l.Children[2]))
	}
	if len(p.Blocks[1].Children) != 0 {
		t.Errorf("only a layout holds blocks: %+v", p.Blocks[1])
	}
	joined := strings.Join(a.Warnings, "\n")
	if !strings.Contains(joined, "weren't a kind this version knows") {
		t.Errorf("what was left out is reported:\n%s", joined)
	}
	if a.Manifest.Counts.Blocks != 2+2+20 {
		t.Errorf("blocks counted, nested ones included: %d", a.Manifest.Counts.Blocks)
	}

	// the page's limit counts nested blocks too: the layout takes 1+20, the rest of the page is full
	c = goodContent()
	var blocks []Block
	for i := 0; i < MaxBlocksPerPage; i++ {
		blocks = append(blocks, spacer())
	}
	full := Block{Type: "layout", Props: map[string]any{}, Children: [][]Block{{spacer(), spacer(), spacer()}}}
	c.Pages = []Page{{Slug: "full", Title: "Full", Blocks: append([]Block{full}, blocks...)}}
	a, err = inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, b := range a.Content.Pages[0].Blocks {
		total++
		for _, col := range b.Children {
			total += len(col)
		}
	}
	if total != MaxBlocksPerPage {
		t.Errorf("a page holds at most %d blocks, nested ones included: %d", MaxBlocksPerPage, total)
	}
}

func TestInspectChecksTheSharedBlockLibrary(t *testing.T) {
	s := testService(t)
	c := goodContent()
	c.Reusables = []Reusable{
		{ID: 5, Name: "  Hours   box ", Type: "richtext", Props: map[string]any{"content": "<p>ok</p><script>alert(1)</script>"}, Style: map[string]any{"background": "red;}"}},
		{ID: 5, Name: "same id twice", Type: "spacer"},
		{ID: 0, Name: "no id", Type: "spacer"},
		{ID: 6, Name: "a layout", Type: "layout"},
		{ID: 7, Name: "shared of shared", Type: "reusable"},
		{ID: 8, Name: "unknown", Type: "nope"},
		{ID: 9, Name: strings.Repeat("n", 300), Type: "button", Props: map[string]any{"label": "Go", "url": "javascript:alert(2)"}},
		{ID: 10, Name: "  ", Type: "spacer"},
	}
	ref := func(id float64) Block { return Block{Type: "reusable", Props: map[string]any{"ref": id}} }
	c.Pages = []Page{{Slug: "p", Title: "P", Status: "published", Blocks: []Block{
		ref(5), ref(9), ref(6), ref(7), ref(404), ref(0), {Type: "reusable", Props: map[string]any{"ref": "five"}},
		{Type: "layout", Props: map[string]any{}, Children: [][]Block{{ref(10), ref(404), ref(5)}}},
	}}}
	a, err := inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil {
		t.Fatal(err)
	}
	lib := a.Content.Reusables
	if len(lib) != 3 || lib[0].ID != 5 || lib[1].ID != 9 || lib[2].ID != 10 {
		t.Fatalf("only valid shared blocks are kept: %+v", lib)
	}
	if lib[0].Name != "Hours box" || strings.Contains(lib[0].Props["content"].(string), "script") || lib[0].Style["background"] != "" {
		t.Errorf("a shared block is cleaned like any block: %+v", lib[0])
	}
	if len([]rune(lib[1].Name)) != 100 || lib[1].Props["url"] != "" || lib[2].Name != "Shared block" {
		t.Errorf("names are limited and never empty, links are checked: %+v %+v", lib[1], lib[2])
	}
	var refs []int64
	for _, b := range a.Content.Pages[0].Blocks {
		if b.Type == "reusable" {
			refs = append(refs, int64(b.Props["ref"].(float64)))
		}
		for _, col := range b.Children {
			for _, ch := range col {
				refs = append(refs, int64(ch.Props["ref"].(float64)))
			}
		}
	}
	if fmt.Sprint(refs) != "[5 9 10 5]" {
		t.Errorf("blocks pointing at nothing are dropped, inside layouts too: %v", refs)
	}
	if joined := strings.Join(a.Warnings, "\n"); !strings.Contains(joined, "can be shared") || !strings.Contains(joined, "repeated id") {
		t.Errorf("what was left out is reported:\n%s", joined)
	}

	// the library has a limit
	c.Reusables = nil
	for i := 1; i <= 120; i++ {
		c.Reusables = append(c.Reusables, Reusable{ID: int64(i), Name: "n", Type: "spacer"})
	}
	c.Pages = nil
	a, err = inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil || len(a.Content.Reusables) != 100 {
		t.Errorf("at most 100: %v %d", err, len(a.Content.Reusables))
	}
}

func TestInspectChecksRedirects(t *testing.T) {
	s := testService(t)
	c := goodContent()
	c.Redirects = []Redirect{
		{From: "/Old/", To: "/new", Permanent: true},
		{From: "/old", To: "/duplicate"},
		{From: "/admin/x", To: "/y"},
		{From: "/bad path", To: "/y"},
		{From: "/a", To: "javascript:alert(1)"},
		{From: "/b", To: "mailto:a@b.com"},
		{From: "/loop-1", To: "/loop-2"},
		{From: "/loop-2", To: "/loop-3"},
		{From: "/loop-3", To: "/LOOP-1/"},
		{From: "/self", To: "/self"},
		{From: "/chain-1", To: "/chain-2"},
		{From: "/elsewhere", To: "https://example.org/x?y=1", Auto: true},
		{From: "https://example.com/Full/Url", To: "/ok"},
	}
	a, err := inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range a.Content.Redirects {
		got = append(got, r.From+">"+r.To)
	}
	want := "/old>/new,/chain-1>/chain-2,/elsewhere>https://example.org/x?y=1,/full/url>/ok"
	if strings.Join(got, ",") != want {
		t.Errorf("redirects kept:\n got %v\nwant %s", got, want)
	}
	if !a.Content.Redirects[0].Permanent || !a.Content.Redirects[2].Auto {
		t.Errorf("flags are kept: %+v", a.Content.Redirects)
	}
	joined := strings.Join(a.Warnings, "\n")
	for _, w := range []string{"not valid", "second redirect", "leads back to itself"} {
		if !strings.Contains(joined, w) {
			t.Errorf("warnings lack %q:\n%s", w, joined)
		}
	}
	c.Redirects = nil
	for i := 0; i < 1100; i++ {
		c.Redirects = append(c.Redirects, Redirect{From: fmt.Sprintf("/r%d", i), To: "/x"})
	}
	a, err = inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil || len(a.Content.Redirects) != 1000 {
		t.Errorf("at most 1000: %v %d", err, len(a.Content.Redirects))
	}
}

// The top bar's colours and size come back from a backup, cleaned like anything else in it.
func TestRestoredMenuStyleIsCleaned(t *testing.T) {
	w := &warnings{}
	got := cleanMenus([]Menu{
		{Location: "topbar", Style: menus.Style{Background: "#ABCDEF", Text: `#fff" onclick="x`, Size: "lg"}},
		{Location: "main", Style: menus.Style{Background: "#112233"}}, // the footer menu is gone: its links are in the footer builder
	}, w)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].Style != (menus.Style{Background: "#abcdef", Size: "lg"}) {
		t.Errorf("top bar style: %+v", got[0].Style)
	}
	if got[1].Style != (menus.Style{}) {
		t.Errorf("only the top bar has a look: %+v", got[1].Style)
	}
}

// The dark mode switch's place comes back from a backup; a backup from before it had one (or a bad value) gets the default.
func TestRestoredColorSwitchPlace(t *testing.T) {
	s := testService(t)
	for in, want := range map[string]string{"footer": "footer", "off": "off", "floating": "floating", "": "floating", "header": "floating", `x" onclick="y`: "floating"} {
		got := s.cleanSettings(Settings{Title: "T", ActiveTheme: "default", ColorMode: "toggle", ColorSwitch: in}, nil, &warnings{})
		if got.ColorSwitch != want {
			t.Errorf("%q: got %q, want %q", in, got.ColorSwitch, want)
		}
	}
}
