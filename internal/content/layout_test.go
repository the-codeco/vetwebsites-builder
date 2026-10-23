package content

import (
	"strings"
	"testing"
)

func TestLayoutCleanAndCSS(t *testing.T) {
	if css := (Layout{}).CSS(); css != "" {
		t.Errorf("the default layout adds nothing: %q", css)
	}
	l := layoutFrom(map[string]any{
		"boxed": true, "width": 980.0, "background": "#BBBBBB", "image": "javascript:alert(1)", "image_mode": "sideways",
		"header": map[string]any{"style": "stacked", "tagline": "  Caring   for pets ", "banners": []any{"/media/a.jpg", "javascript:x", "/media/b.jpg"},
			"banner_place": "nowhere", "banner_height": "lg", "bar_background": "#003A40", "bar_text": "red;}", "bar_active": "#222222"},
	})
	if !l.Boxed || l.Width != 980 || l.Background != "#bbbbbb" || l.Image != "" || l.ImageMode != "cover" {
		t.Errorf("page: %+v", l)
	}
	h := l.Header
	if h.Style != HeaderStacked || h.Tagline != "Caring for pets" || len(h.Banners) != 2 || h.BannerPlace != "beside" || h.BannerPixels() != 250 || h.BarBackground != "#003a40" || h.BarText != "" {
		t.Errorf("header: %+v", h)
	}
	css := l.CSS()
	for _, want := range []string{"html { background-color: #bbbbbb; }", "body { max-width: 980px;", ":root { --bar-bg: #003a40; }", ":root { --bar-active: #222222; }"} {
		if !strings.Contains(css, want) {
			t.Errorf("css lacks %q:\n%s", want, css)
		}
	}
	if strings.Contains(css, "red;") || strings.Contains(css, "javascript") {
		t.Errorf("unsafe values reached the css:\n%s", css)
	}
	if w := layoutFrom(map[string]any{"width": 99999.0}).Width; w != DefBoxWidth {
		t.Errorf("width out of range: %d", w)
	}
	// a full-width page with a picture: the picture shows through
	full := Layout{Image: `/media/a b".jpg`, ImageMode: "top"}.CSS()
	if !strings.Contains(full, `background-image: url("/media/a b%22.jpg")`) || !strings.Contains(full, "background-size: 100% auto") || !strings.Contains(full, "body, .site-header { background: transparent; }") {
		t.Errorf("full width with a picture:\n%s", full)
	}
	if again := CleanLayout(l.asJSON()); layoutFrom(again).Header.Tagline != "Caring for pets" {
		t.Error("round trip")
	}
}
