package themes

import (
	"strings"
	"testing"
)

func TestTeamBlock(t *testing.T) {
	for _, theme := range []string{"default", "journal", "midnight"} {
		out := renderBlockOf(t, theme, "team", map[string]any{
			"columns": "4", "shape": "square", "link_label": "More",
			"people": []any{
				map[string]any{"name": "Ann <b>", "role": "Vet", "photo": "/media/a.jpg", "bio": "Loves\ndogs", "link": "/ann"},
				map[string]any{"name": "Bob", "link": "javascript:alert(1)"},
				map[string]any{"role": "No name, so not shown"},
				map[string]any{"name": "Ninth person"},
			},
		})
		for _, want := range []string{
			`cols-4 team-square`, `<h3>Ann &lt;b&gt;</h3>`, `member-role">Vet`, `class="member-photo" src="/media/a.jpg"`,
			`<a class="member-link" href="/ann">More`, `<h3>Bob</h3>`, `Ninth person`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: lacks %q\n%s", theme, want, out)
			}
		}
		if strings.Count(out, `<article class="member">`) != 3 || strings.Count(out, `class="member-link"`) != 1 ||
			strings.Contains(out, "No name") || strings.Contains(out, "javascript:") {
			t.Errorf("%s: names required, unsafe links dropped:\n%s", theme, out)
		}
	}
	out := renderBlockOf(t, "default", "team", map[string]any{"columns": "9", "shape": "<x>", "people": []any{map[string]any{"name": "A", "link": "/a"}}})
	if !strings.Contains(out, "cols-3 team-round") || !strings.Contains(out, "Profile") {
		t.Errorf("bad settings fall back:\n%s", out)
	}
}
