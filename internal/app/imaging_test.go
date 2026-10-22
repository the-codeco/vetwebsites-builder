package app_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// photoWithSecrets is a JPEG whose EXIF says "turn 90° clockwise" and carries a (fake) location and camera name.
func photoWithSecrets(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	raw := buf.Bytes()

	tiff := make([]byte, 8+2+12+4)
	copy(tiff, "II*\x00")
	binary.LittleEndian.PutUint32(tiff[4:], 8)
	binary.LittleEndian.PutUint16(tiff[8:], 1)
	e := tiff[10:]
	binary.LittleEndian.PutUint16(e[0:], 0x0112)
	binary.LittleEndian.PutUint16(e[2:], 3)
	binary.LittleEndian.PutUint32(e[4:], 1)
	binary.LittleEndian.PutUint16(e[8:], 6)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	payload = append(payload, []byte("GPSLatitude 51.5007 N GPSLongitude 0.1246 W SecretCam9000")...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)
	return append(append(append([]byte{}, raw[:2]...), seg...), raw[2:]...)
}

type uploaded struct {
	ID    int64  `json:"id"`
	URL   string `json:"url"`
	Thumb string `json:"thumb"`
}

func (c *client) uploadOne(t *testing.T, name, mime string, data []byte) uploaded {
	t.Helper()
	r := c.upload(upFile{name, mime, data})
	var out struct {
		Uploaded []uploaded `json:"uploaded"`
		Errors   []string   `json:"errors"`
	}
	if err := json.Unmarshal([]byte(r.Body), &out); err != nil || len(out.Uploaded) != 1 {
		t.Fatalf("upload %s: %d %s", name, r.Status, r.Body)
	}
	return out.Uploaded[0]
}

func (e *testEnv) stored(path string) []byte {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.mediaDir, strings.TrimPrefix(path, "/media/")))
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func dims(t *testing.T, b []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

func TestUploadsLoseTheirHiddenDetailsAndStandUpright(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")

	up := admin.uploadOne(t, "holiday.jpg", "image/jpeg", photoWithSecrets(600, 400))
	data := e.stored(up.URL)
	for _, secret := range []string{"GPSLatitude", "SecretCam9000", "Exif"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("the stored photo still contains %q", secret)
		}
	}
	// EXIF said "turn clockwise": the photo is really 400 wide and 600 tall, and the library says so
	if w, h := dims(t, data); w != 400 || h != 600 {
		t.Errorf("stored photo is %d×%d, want 400×600", w, h)
	}
	if got := e.sql(`SELECT width || 'x' || height FROM media WHERE id = $1`, up.ID); got != "400x600" {
		t.Errorf("the library records %s", got)
	}
	// and so is its thumbnail, which a browser can't turn for itself
	if w, h := dims(t, e.stored(up.Thumb)); w >= h {
		t.Errorf("the thumbnail is %d×%d: it should be taller than wide", w, h)
	}
	if got := e.sql(`SELECT size_bytes FROM media WHERE id = $1`, up.ID); got != strconv.Itoa(len(data)) {
		t.Errorf("recorded size %s, real size %d", got, len(data))
	}

	// a photo with no rotation still loses its details, without being re-encoded
	plain := stripOrientation(photoWithSecrets(300, 200))
	up2 := admin.uploadOne(t, "plain.jpg", "image/jpeg", plain)
	d2 := e.stored(up2.URL)
	if bytes.Contains(d2, []byte("GPSLatitude")) || bytes.Contains(d2, []byte("Exif")) {
		t.Error("details survived in an upright photo")
	}
	if w, h := dims(t, d2); w != 300 || h != 200 {
		t.Errorf("upright photo is %d×%d", w, h)
	}
}

// stripOrientation changes the orientation value in a test photo to 1 (as shot).
func stripOrientation(b []byte) []byte {
	out := append([]byte{}, b...)
	i := bytes.Index(out, []byte("II*\x00"))
	binary.LittleEndian.PutUint16(out[i+10+8:], 1)
	return out
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Join(strings.Fields(strings.Repeat(" ", 0)+string(rune('0'))), "")) + bytesToDecimal(n)
}

func bytesToDecimal(n int) string {
	if n == 0 {
		return ""
	}
	return bytesToDecimal(n/10) + string(rune('0'+n%10))
}

var reSrcset = regexp.MustCompile(`srcset="([^"]*)" sizes="([^"]*)"`)

func TestResponsiveImages(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	big := admin.uploadOne(t, "wide.jpg", "image/jpeg", jpegImage(2000, 1000))
	small := admin.uploadOne(t, "small.png", "image/png", pngImage(300, 200))

	// a page that uses the images
	id := newPage(t, admin, "Pictures")
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"image"}})
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"image"}}) // a second one, with a link
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"gallery"}})
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"hero"}})
	admin.post("/admin/blocks/"+e.blockID(id, "image"), url.Values{"p.image": {big.URL}, "p.alt": {"A wide picture"}})
	admin.post("/admin/blocks/"+e.secondBlockID(id, "image"), url.Values{"p.image": {big.URL}, "p.link": {"/about"}})
	admin.post("/admin/blocks/"+e.blockID(id, "gallery"), url.Values{"p.images": {big.URL + "\nhttps://example.com/other.jpg"}})
	admin.post("/admin/blocks/"+e.blockID(id, "hero"), url.Values{"p.image": {small.URL}})
	admin.post("/admin/pages/"+id, url.Values{"title": {"Pictures"}, "slug": {"pictures"}, "status": {"published"}})

	body := anon.get("/pictures").Body
	sets := reSrcset.FindAllStringSubmatch(body, -1)
	if len(sets) != 4 {
		t.Fatalf("the two images, one gallery picture and the hero should have srcsets, got %d:\n%s", len(sets), body)
	}
	for _, want := range []string{big.URL + "?w=480 480w", big.URL + "?w=960 960w", big.URL + "?w=1600 1600w"} {
		if !strings.Contains(sets[0][1], want) {
			t.Errorf("srcset lacks %q: %s", want, sets[0][1])
		}
	}
	if !strings.Contains(sets[0][2], "100vw") {
		t.Errorf("sizes: %s", sets[0][2])
	}
	if strings.Contains(body, `src="https://example.com/other.jpg" srcset`) {
		t.Error("another site's image can't be resized")
	}
	if !strings.Contains(body, `src="`+big.URL+`" srcset=`) {
		t.Error("the plain src stays, for browsers without srcset")
	}

	// the sizes are real, and made the first time they're asked for
	file := e.file(big.URL)
	r := anon.get(big.URL + "?w=480")
	if r.Status != 200 || !strings.Contains(r.Header.Get("Content-Type"), "image/jpeg") {
		t.Fatalf("480: %d %s", r.Status, r.Header.Get("Content-Type"))
	}
	if w, h := dims(t, []byte(r.Body)); w != 480 || h != 240 {
		t.Errorf("480 copy is %d×%d", w, h)
	}
	if len(r.Body) >= len(e.stored(big.URL)) {
		t.Error("the small copy should be lighter")
	}
	if _, err := os.Stat(strings.TrimSuffix(file, ".jpg") + "_w480.jpg"); err != nil {
		t.Errorf("the copy should be kept for next time: %v", err)
	}
	if cc := r.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("cache: %q", cc)
	}
	if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("the copy must be served as safely as the original: %q", csp)
	}
	if w, _ := dims(t, []byte(anon.get(big.URL+"?w=1600").Body)); w != 1600 {
		t.Errorf("1600 copy is %d wide", w)
	}

	// anything else gets the original
	orig := e.stored(big.URL)
	for _, q := range []string{"?w=500", "?w=0", "?w=-480", "?w=abc", "?w=", "?w=99999999999999999999", "?other=1"} {
		got := anon.get(big.URL + q)
		if got.Status != 200 || !bytes.Equal([]byte(got.Body), orig) {
			t.Errorf("%s: %d", q, got.Status)
		}
	}
	if r := anon.get(small.URL + "?w=960"); !bytes.Equal([]byte(r.Body), e.stored(small.URL)) {
		t.Error("an image that is no wider is sent as it is, never enlarged")
	}
	// can't be used to read other files, or to make copies of copies and thumbnails
	for _, p := range []string{"/media/../go.mod?w=480", "/media/%2e%2e/go.mod?w=480", "/media/2026/01/../../../go.mod?w=480", strings.Replace(big.URL, ".jpg", "_w480.jpg", 1) + "?w=960"} {
		if r := anon.get(p); r.Status == 200 && strings.Contains(r.Body, "module webbuilder") {
			t.Errorf("%s leaked a file", p)
		}
	}
	if _, err := os.Stat(strings.TrimSuffix(file, ".jpg") + "_w480_w960.jpg"); err == nil {
		t.Error("a copy of a copy was made")
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(file), "*.tmp")); len(matches) != 0 {
		t.Errorf("leftover temporary files: %v", matches)
	}

	// deleting the image deletes its copies
	if r := admin.post("/admin/media/"+strconv.FormatInt(big.ID, 10)+"/delete", nil); r.Status != 303 {
		t.Fatalf("delete: %d", r.Status)
	}
	left, _ := filepath.Glob(strings.TrimSuffix(file, ".jpg") + "*")
	if len(left) != 0 {
		t.Errorf("files left behind after deleting: %v", left)
	}
}

func (e *testEnv) file(u string) string {
	return filepath.Join(e.mediaDir, strings.TrimPrefix(u, "/media/"))
}

// halvesPNG is red on the left half and blue on the right half.
func halvesPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{255, 0, 0, 255}
			if x >= w/2 {
				c = color.NRGBA{0, 0, 255, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func pixel(t *testing.T, data []byte, x, y int) [3]uint8 {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(x, y).RGBA()
	return [3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
}

func gifImage() []byte {
	var buf bytes.Buffer
	gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, 20, 20), color.Palette{color.Black, color.White}), nil)
	return buf.Bytes()
}

// secondBlockID is the id of the second block of a type on a page.
func (e *testEnv) secondBlockID(pageID, typ string) string {
	return e.sql(`SELECT id FROM blocks WHERE page_id = $1 AND type = $2 ORDER BY id OFFSET 1 LIMIT 1`, pageID, typ)
}
