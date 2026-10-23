package media

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"image"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
)

// Widths are the only sizes a copy can be asked for.
var Widths = []int{480, 960, 1600}

var storedRe = regexp.MustCompile(`^\d{4}/(0[1-9]|1[0-2])/[0-9a-f]{24}\.(jpg|png|gif|webp)$`)

func variantName(p string, w int) string {
	ext := path.Ext(p)
	return fmt.Sprintf("%s_w%d%s", strings.TrimSuffix(p, ext), w, ext)
}

// ThumbName is where an image's thumbnail is kept.
func ThumbName(p, ext string) string { return strings.TrimSuffix(p, path.Ext(p)) + "_thumb" + ext }

// VariantFiles lists the resized copies an image may have.
func VariantFiles(p string) []string {
	out := make([]string, 0, len(Widths))
	for _, w := range Widths {
		out = append(out, variantName(p, w))
	}
	return out
}

// SrcsetAttr is the srcset and sizes attributes for one of the site's own JPEG or PNG images, or nothing for
// any other address (another site's picture, a GIF, a WebP).
func SrcsetAttr(src any, sizes string) template.HTMLAttr {
	s, _ := src.(string)
	p, ok := strings.CutPrefix(s, "/media/")
	if !ok || !storedRe.MatchString(p) || strings.HasSuffix(p, ".gif") || strings.HasSuffix(p, ".webp") {
		return ""
	}
	parts := make([]string, len(Widths))
	for i, w := range Widths {
		parts[i] = fmt.Sprintf("%s?w=%d %dw", s, w, w)
	}
	return template.HTMLAttr(fmt.Sprintf(`srcset="%s" sizes="%s"`, html.EscapeString(strings.Join(parts, ", ")), html.EscapeString(sizes)))
}

// Variants makes resized copies the first time they are asked for and keeps them on disk.
type Variants struct {
	root *os.Root
	mu   sync.Mutex
}

func NewVariants(root *os.Root) *Variants { return &Variants{root: root} }

// Resolve returns the file to send for name at the requested width: a resized copy when one makes sense,
// otherwise name itself. Copies are never enlarged, and never made of copies, thumbnails or animations.
func (v *Variants) Resolve(name string, width int) string {
	allowed := false
	for _, w := range Widths {
		allowed = allowed || w == width
	}
	if !allowed || !storedRe.MatchString(name) || strings.HasSuffix(name, ".gif") || strings.HasSuffix(name, ".webp") {
		return name
	}
	vn := variantName(name, width)
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := v.root.Stat(vn); err == nil {
		return vn
	}
	data, err := v.root.ReadFile(name)
	if err != nil {
		return name
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= width {
		return name
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return name
	}
	small := resize(img, width, maxInt(cfg.Height*width/cfg.Width, 1))
	out, err := encode(small, path.Ext(name), 82)
	if err != nil {
		return name
	}
	tmp := vn + ".tmp"
	if err := v.root.WriteFile(tmp, out, 0o644); err != nil {
		v.root.Remove(tmp)
		return name
	}
	if err := v.root.Rename(tmp, vn); err != nil {
		v.root.Remove(tmp)
		return name
	}
	return vn
}
