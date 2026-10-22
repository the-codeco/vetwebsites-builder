package themes

import (
	"strings"
	"testing"

	"webbuilder/internal/blocks"
)

func TestHeroImagePosition(t *testing.T) {
	img := "/media/a.jpg"
	for _, tc := range []struct {
		name  string
		props map[string]any
		want  []string
		not   []string
	}{
		{"behind (default)", map[string]any{"image": img}, []string{`class="hero-bg"`, `alt=""`}, []string{"hero-split", "hero-pic"}},
		{"behind (explicit)", map[string]any{"image": img, "layout": "background"}, []string{`class="hero-bg"`}, []string{"hero-split"}},
		{"right", map[string]any{"image": img, "layout": "right", "image_alt": `A "dog"`, "heading": "Hi"},
			[]string{`hero-split hero-image-right`, `class="hero-pic"`, `src="/media/a.jpg"`, `alt="A &#34;dog&#34;"`, "<h1>Hi</h1>"}, []string{"hero-bg"}},
		{"left", map[string]any{"image": img, "layout": "left"}, []string{`hero-image-left`, `class="hero-pic"`}, []string{"hero-bg"}},
		{"beside but no picture", map[string]any{"layout": "right", "heading": "Hi"}, []string{`class="hero hero-md"`, "<h1>Hi</h1>"}, []string{"hero-split", "hero-pic"}},
		{"bad value falls back", map[string]any{"image": img, "layout": "<x>"}, []string{`class="hero-bg"`}, []string{"hero-split"}},
	} {
		out := renderTree(t, "default", block(1, "hero", tc.props, nil), nil)
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: lacks %s\n%s", tc.name, w, out)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(out, n) {
				t.Errorf("%s: should not contain %s\n%s", tc.name, n, out)
			}
		}
	}
	// a hero saved before the setting existed has no layout prop at all
	old := blocks.Block{ID: 2, Type: "hero", Props: map[string]any{"image": img}}
	e, _ := Load()
	th, _ := e.Lookup("default")
	out, err := th.RenderBlockTree(old, nil)
	if err != nil || !strings.Contains(string(out), "hero-bg") {
		t.Errorf("old hero: %v %s", err, out)
	}
}
