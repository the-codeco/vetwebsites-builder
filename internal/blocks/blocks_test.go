package blocks

import (
	"reflect"
	"strings"
	"testing"
)

func TestSafeURL(t *testing.T) {
	ok := []string{"/about", "#top", "https://example.com/x", "HTTP://example.com", "mailto:a@b.com", "/media/a.png",
		"tel:+27 82 123 4567", "tel:0821234567", "tel:(021) 555-0123", "TEL:+1.555.123.4567"}
	for _, u := range ok {
		if SafeURL(u) != u {
			t.Errorf("%q should be allowed", u)
		}
	}
	bad := []string{"javascript:alert(1)", " JaVaScRiPt:alert(1)", "data:text/html,x", "//evil.com", `/\evil.com`,
		"vbscript:x", "relative/path", "/a\nb", "ftp://x",
		"tel:", "tel:+1", "tel:abc", "tel:+27 82 <script>", "tel:123;ext=1", "tel:+" + strings.Repeat("1", 40), "tel: 0821234567", "tel:082\n1234567", "tel:+-123", "sms:0821234567"}
	for _, u := range bad {
		if got := SafeURL(u); got != "" {
			t.Errorf("%q should be rejected, got %q", u, got)
		}
	}
	if SafeImageURL("mailto:a@b.com") != "" {
		t.Error("mailto must not be an image URL")
	}
	if SafeImageURL("tel:0821234567") != "" {
		t.Error("tel must not be an image URL")
	}
}

func TestCleanDropsUnknownAndCoerces(t *testing.T) {
	hero, _ := Get("hero")
	got := hero.Clean(map[string]any{
		"heading":    "Hi",
		"evil":       "dropped",
		"button_url": "javascript:alert(1)",
		"image":      "/media/x.png",
		"size":       "huge", // not an option -> default
		"subheading": 42,     // wrong type -> default
	})
	if _, has := got["evil"]; has {
		t.Error("unknown key kept")
	}
	if got["button_url"] != "" || got["image"] != "/media/x.png" || got["size"] != "md" || got["subheading"] != "" {
		t.Errorf("unexpected: %#v", got)
	}
	if len(got) != len(hero.Fields) {
		t.Errorf("all schema keys must be present, got %d", len(got))
	}
}

func TestCleanNilGivesDefaults(t *testing.T) {
	for _, bt := range All() {
		got := bt.Clean(nil)
		if len(got) != len(bt.Fields) {
			t.Errorf("%s: want %d keys, got %d", bt.Name, len(bt.Fields), len(got))
		}
	}
}

func TestImageList(t *testing.T) {
	g, _ := Get("gallery")
	got := g.Clean(map[string]any{"images": "/a.png\njavascript:x\n\nhttps://x.com/b.jpg"})["images"]
	if !reflect.DeepEqual(got, []string{"/a.png", "https://x.com/b.jpg"}) {
		t.Errorf("got %#v", got)
	}
	got = g.Clean(map[string]any{"images": []any{"/a.png", 5, "//evil"}})["images"]
	if !reflect.DeepEqual(got, []string{"/a.png"}) {
		t.Errorf("got %#v", got)
	}
}

func TestNumberAndBoolAndColor(t *testing.T) {
	f := Field{Kind: KindNumber, Default: 5.0, Min: 1, Max: 10}
	if f.clean("99") != 10.0 || f.clean(-3.0) != 1.0 || f.clean("abc") != 5.0 || f.clean(nil) != 5.0 {
		t.Error("number coercion wrong")
	}
	b := Field{Kind: KindBool}
	if b.clean("on") != true || b.clean("no") != false || b.clean(nil) != false {
		t.Error("bool coercion wrong")
	}
	c := Field{Kind: KindColor}
	if c.clean("#ABCDEF") != "#abcdef" || c.clean("red;}body{display:none") != "" || c.clean("#abc") != "" {
		t.Error("colour validation wrong")
	}
}

func TestNormalizeRichText(t *testing.T) {
	cases := map[string]string{
		"First paragraph.\n\nSecond one,\nwith a break.": "<p>First paragraph.</p>\n<p>Second one,<br>with a break.</p>",
		"Fish & chips":                 "<p>Fish &amp; chips</p>",
		"":                             "",
		"\n\n  \n\n":                   "",
		"<p>Already <b>markup</b></p>": "<p>Already <b>markup</b></p>",
		"<p onclick=x()>hi</p><script>x</script>": "<p>hi</p>",
	}
	for in, want := range cases {
		if got := NormalizeRichText(in); got != want {
			t.Errorf("NormalizeRichText(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestPlainTextDecodesEntities(t *testing.T) {
	if got := PlainText("<p>Fish &amp; <b>chips</b></p>"); got != "Fish & chips" {
		t.Errorf("got %q", got)
	}
}

func TestPostListCount(t *testing.T) {
	pl, _ := Get("postlist")
	cases := map[any]float64{nil: 3, "99": 12, "0": 1, "5": 5, "junk": 3, 7.0: 7}
	for in, want := range cases {
		if got := pl.Clean(map[string]any{"count": in})["count"]; got != want {
			t.Errorf("count %v => %v, want %v", in, got, want)
		}
	}
}

func TestStyle(t *testing.T) {
	s := CleanStyle(map[string]any{"background": "#112233", "text": "url(x)", "padding": "lg", "align": "diagonal"})
	if s.Background != "#112233" || s.Text != "" || s.Padding != "lg" || s.Align != "left" || s.Width != "normal" {
		t.Errorf("got %#v", s)
	}
	if string(s.CSSVars()) != "--bg:#112233;" {
		t.Errorf("vars: %q", s.CSSVars())
	}
	if s.Classes() != "pad-lg align-left width-normal pinned" {
		t.Errorf("a custom background pins the block's colours in dark mode: %q", s.Classes())
	}
	for name, c := range map[string]struct {
		style map[string]any
		want  string
	}{
		"nothing set":    {map[string]any{}, "pad-md align-left width-normal"},
		"text only":      {map[string]any{"text": "#222222"}, "pad-md align-left width-normal text-only"},
		"both":           {map[string]any{"text": "#222222", "background": "#ffeedd"}, "pad-md align-left width-normal pinned"},
		"invalid colour": {map[string]any{"background": "red;}x{"}, "pad-md align-left width-normal"},
	} {
		if got := CleanStyle(c.style).Classes(); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestNormalizeRichTextMixedContent(t *testing.T) {
	cases := map[string]string{
		// plain paragraphs mixed with toolbar formatting keep their paragraph breaks
		"Intro with <strong>bold</strong>.\n\n<h2>Title</h2>\n\nMore text.\nSecond line.": "<p>Intro with <strong>bold</strong>.</p>\n<h2>Title</h2>\n<p>More text.<br>Second line.</p>",
		"<ul>\n  <li>one</li>\n  <li>two</li>\n</ul>\n\nAfter the list.":                  "<ul>\n  <li>one</li>\n  <li>two</li>\n</ul>\n<p>After the list.</p>",
		"1 < 2 and 3 > 2": "<p>1 &lt; 2 and 3 &gt; 2</p>",
		// hostile input stays neutralised after wrapping
		`Click <a href="javascript:alert(1)">me</a> <img src=x onerror=alert(2)>`: `<p>Click me <img src="x"></p>`,
		"<script>alert(1)</script>\n\nsafe":                                       "<p></p>\n<p>safe</p>",
	}
	for in, want := range cases {
		if got := NormalizeRichText(in); got != want {
			t.Errorf("NormalizeRichText(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	// idempotent: re-saving normalised content must not change it
	once := NormalizeRichText("Para one.\n\nPara <em>two</em>.")
	if twice := NormalizeRichText(once); twice != once {
		t.Errorf("not idempotent:\n once %q\ntwice %q", once, twice)
	}
}
