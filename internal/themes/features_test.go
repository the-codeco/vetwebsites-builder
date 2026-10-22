package themes

import (
	"strings"
	"testing"
)

func TestFeatureCards(t *testing.T) {
	for _, theme := range []string{"default", "journal", "midnight"} {
		out := renderBlockOf(t, theme, "features", map[string]any{
			"columns": "4", "look": "plain", "link_label": "Read on",
			"cards": []any{
				map[string]any{"title": "Fast <b>", "text": "Quick\nand easy", "icon": "★", "link": "/fast"},
				map[string]any{"title": "Pretty", "image": "/media/p.jpg", "icon": "NOT SHOWN", "link": "javascript:alert(1)"},
				map[string]any{"text": "No title, so no card"},
				map[string]any{"title": "Sixth"},
				map[string]any{"title": "Seventh is still shown: lists have no small limit"},
			},
		})
		for _, want := range []string{
			`cols-4 features-plain`, `<h3>Fast &lt;b&gt;</h3>`, `class="feature-icon" aria-hidden="true">★`,
			`<a class="feature-link" href="/fast">Read on`, `class="feature-pic" src="/media/p.jpg"`, `<h3>Pretty</h3>`, "Seventh",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: lacks %q\n%s", theme, want, out)
			}
		}
		if strings.Count(out, `<article class="feature">`) != 4 || strings.Contains(out, "No title") || strings.Contains(out, "NOT SHOWN") || strings.Contains(out, "javascript:") {
			t.Errorf("%s: cards need titles, pictures replace icons, unsafe links are dropped:\n%s", theme, out)
		}
		if n := strings.Count(out, `class="feature-link"`); n != 1 {
			t.Errorf("%s: only cards with a safe link get one (links %d)\n%s", theme, n, out)
		}
	}
	out := renderBlockOf(t, "default", "features", map[string]any{"columns": "9", "look": "<x>", "cards": []any{map[string]any{"title": "A", "link": "/a"}}})
	if !strings.Contains(out, "cols-3 features-boxed") || !strings.Contains(out, "Learn more") {
		t.Errorf("bad settings fall back:\n%s", out)
	}
}
