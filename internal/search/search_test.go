package search

import (
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"  hello   world ": "hello world", "a\x00b\tc\nd": "a b c d", "": "", "éé": "éé",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Clean(strings.Repeat("é", 300)); len([]rune(got)) != MaxQuery {
		t.Errorf("not capped by characters: %d", len([]rune(got)))
	}
}

func TestHighlight(t *testing.T) {
	got := string(Highlight("see " + markStart + "this" + markEnd + " <script>alert(1)</script> & <mark>fake</mark>"))
	if got != "see <mark>this</mark> &lt;script&gt;alert(1)&lt;/script&gt; &amp; &lt;mark&gt;fake&lt;/mark&gt;" {
		t.Errorf("Highlight: %s", got)
	}
}
