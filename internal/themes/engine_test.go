package themes

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io/fs"
	"strings"
	"testing"

	"webbuilder/internal/blocks"
	themefiles "webbuilder/themes"
)

func load(t *testing.T) *Theme {
	t.Helper()
	e, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return e.Get("default")
}

func TestEveryBlockTypeRendersWithDefaults(t *testing.T) {
	th := load(t)
	for _, bt := range blocks.All() {
		b := blocks.Block{ID: 1, Type: bt.Name, Props: bt.Clean(nil), Style: blocks.CleanStyle(nil)}
		html, err := th.RenderBlock(b)
		if err != nil {
			t.Errorf("%s: %v", bt.Name, err)
		}
		if !strings.Contains(string(html), "block-"+bt.Name) {
			t.Errorf("%s: wrapper missing: %s", bt.Name, html)
		}
	}
}

func TestRenderingIsSafe(t *testing.T) {
	th := load(t)
	evil := `<script>alert(1)</script><img src=x onerror=alert(2)><a href="javascript:alert(3)">x</a>`
	cases := map[string]map[string]any{
		"richtext": {"content": evil},
		"hero":     {"heading": evil, "subheading": evil, "image": "javascript:alert(4)", "button_label": "go", "button_url": "javascript:alert(5)"},
		"image":    {"image": "/media/a.png", "alt": `"><script>alert(6)</script>`, "link": "javascript:alert(7)"},
		"columns":  {"col1_title": evil, "col2_text": evil},
		"gallery":  {"images": "javascript:alert(8)\n/ok.png"},
	}
	for typ, raw := range cases {
		bt, _ := blocks.Get(typ)
		style := blocks.CleanStyle(map[string]any{"background": `red;} body{display:none`, "text": "#123456"})
		html, err := th.RenderBlock(blocks.Block{ID: 1, Type: typ, Props: bt.Clean(raw), Style: style})
		if err != nil {
			t.Fatal(err)
		}
		s := strings.ToLower(string(html))
		// Plain-text fields are HTML-escaped (inert), so only look for live markup.
		banned := []string{"<script", "<img src=x", `href="javascript`, `src="javascript`, "display:none"}
		if typ == "richtext" { // sanitised, not escaped: no trace of the payload may survive
			banned = append(banned, "onerror", "javascript:")
		}
		for _, bad := range banned {
			if strings.Contains(s, bad) {
				t.Errorf("%s: output contains %q:\n%s", typ, bad, html)
			}
		}
		if !strings.Contains(s, "--fg:#123456") {
			t.Errorf("%s: valid style missing", typ)
		}
	}
}

func TestUnknownBlockTypeRendersNothing(t *testing.T) {
	html, err := load(t).RenderBlock(blocks.Block{Type: "nope"})
	if err != nil || html != "" {
		t.Errorf("got %q, %v", html, err)
	}
}

func TestCSSValidatesTokens(t *testing.T) {
	th := load(t)
	css := th.CSS(map[string]string{
		"color-primary": "#ff0000",
		"color-bg":      "red;}body{display:none",
		"font-body":     "serif",
		"font-heading":  "comic-sans;x",
		"radius":        "12px",
		"space":         "expression(1)",
		"unknown":       "#000000",
	}, "/* custom */", "", nil)
	for _, want := range []string{"--color-primary: #ff0000;", "--color-bg: #ffffff;", "--font-body: Georgia",
		"--font-heading: system-ui", "--radius: 12px;", "--space: 1rem;", "/* custom */"} {
		if !strings.Contains(css, want) {
			t.Errorf("css missing %q:\n%s", want, css)
		}
	}
	if strings.Contains(css, "display:none") || strings.Contains(css, "--unknown") {
		t.Errorf("unsafe css:\n%s", css)
	}
}

func TestRenderPage(t *testing.T) {
	th := load(t)
	var buf bytes.Buffer
	err := th.RenderPage(&buf, PageData{
		Site:      th.Info("My <Site>", false),
		PageTitle: "About",
		Nav:       Nav{Main: NavSection{Items: []NavItem{{Kind: "link", Label: "Home", URL: "/", Active: true}}}},
		Content:   template.HTML("<p>hello</p>"),
		Year:      2026,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"<title>About · My &lt;Site&gt;</title>", `aria-current="page"`, "<p>hello</p>", "/theme/default/theme.css"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestBlogTemplates(t *testing.T) {
	th := load(t)
	evil := `<script>alert(1)</script>`
	post := PostSummary{
		Title: evil, URL: "/blog/x", Excerpt: evil, Cover: "javascript:alert(1)", Date: "1 January 2026", DateISO: "2026-01-01T00:00:00Z",
		Categories: []CategoryLink{{Name: evil, URL: "/blog/category/x"}},
	}
	list, err := th.RenderSection("blog/list", BlogList{
		Heading: evil, Posts: []PostSummary{post}, Page: 2, Pages: 3, PrevURL: "/blog", NextURL: "/blog?page=3",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Page 2 of 3", "← Newer", "Older →", `href="/blog/x"`} {
		if !strings.Contains(string(list), want) {
			t.Errorf("list missing %q", want)
		}
	}
	full, err := th.RenderSection("blog/post", BlogPost{
		Title: evil, Cover: "/media/c.jpg", Date: "1 January 2026", DateISO: "2026-01-01T00:00:00Z", BackURL: "/blog",
		Body: template.HTML("<p>trusted body</p>"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(full), "<p>trusted body</p>") || !strings.Contains(string(full), `src="/media/c.jpg"`) {
		t.Errorf("post missing body or cover:\n%s", full)
	}
	for _, out := range []string{string(list), string(full)} {
		l := strings.ToLower(out)
		if strings.Contains(l, "<script") || strings.Contains(l, `src="javascript`) || strings.Contains(l, `href="javascript`) {
			t.Errorf("unsafe output:\n%s", out)
		}
	}
}

func TestPostListBlock(t *testing.T) {
	th := load(t)
	bt, _ := blocks.Get("postlist")
	b := blocks.Block{ID: 1, Type: "postlist", Props: bt.Clean(nil), Style: blocks.CleanStyle(nil)}
	empty, err := th.RenderBlock(b)
	if err != nil || !strings.Contains(string(empty), "No posts published yet") {
		t.Errorf("empty state: %v %s", err, empty)
	}
	html, err := th.RenderBlockWith(b, []PostSummary{{Title: "Hello", URL: "/blog/hello", Date: "d", DateISO: "d"}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if !strings.Contains(s, "Latest posts") || !strings.Contains(s, `href="/blog/hello"`) || !strings.Contains(s, "View all posts") {
		t.Errorf("populated block:\n%s", s)
	}
}

func TestAllThemesLoadAndRenderEverything(t *testing.T) {
	e, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"default": false, "journal": false, "midnight": false}
	for _, th := range e.List() {
		want[th.Name] = true
		if th.Label == "" || th.Description == "" {
			t.Errorf("%s: missing label/description", th.Name)
		}
		if len(th.Styles) == 0 || !strings.HasPrefix(th.Styles[0], "/theme/default/theme.css?v=") {
			t.Errorf("%s: styles must start with the default stylesheet: %v", th.Name, th.Styles)
		}
		if len(th.Swatches()) != 4 {
			t.Errorf("%s: swatches %v", th.Name, th.Swatches())
		}
		for _, tk := range th.Tokens {
			if !ValidToken(tk.Kind, tk.Default) || tk.Label == "" {
				t.Errorf("%s: bad token %+v", th.Name, tk)
			}
		}
		for _, bt := range blocks.All() {
			b := blocks.Block{ID: 1, Type: bt.Name, Props: bt.Clean(nil), Style: blocks.CleanStyle(nil)}
			if _, err := th.RenderBlock(b); err != nil {
				t.Errorf("%s/%s: %v", th.Name, bt.Name, err)
			}
		}
		for _, name := range []string{"blog/list", "blog/post"} {
			if _, err := th.RenderSection(name, BlogList{}); err != nil && name == "blog/list" {
				t.Errorf("%s/%s: %v", th.Name, name, err)
			}
		}
		var buf bytes.Buffer
		if err := th.RenderPage(&buf, PageData{Site: th.Info("T", false), Content: "x", Year: 2026}); err != nil {
			t.Errorf("%s layout: %v", th.Name, err)
		}
		if !strings.Contains(buf.String(), `href="/site.css"`) || !strings.Contains(buf.String(), "/theme/default/theme.css") {
			t.Errorf("%s layout lacks stylesheet links:\n%s", th.Name, buf.String())
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("theme %q not loaded", name)
		}
	}
}

func TestThemeInheritance(t *testing.T) {
	e, _ := Load()
	def, _ := e.Lookup("default")
	j, _ := e.Lookup("journal")
	m, _ := e.Lookup("midnight")

	if len(j.Tokens) != len(def.Tokens) || len(m.Tokens) != len(def.Tokens) {
		t.Errorf("children must inherit every token: %d/%d vs %d", len(j.Tokens), len(m.Tokens), len(def.Tokens))
	}
	find := func(th *Theme, key string) Token {
		for _, tk := range th.Tokens {
			if tk.Key == key {
				return tk
			}
		}
		t.Fatalf("%s lacks token %s", th.Name, key)
		return Token{}
	}
	if find(j, "color-bg").Default == find(def, "color-bg").Default {
		t.Error("journal should override the background default")
	}
	if tk := find(j, "color-bg"); tk.Label != "Background" || tk.Kind != "color" {
		t.Errorf("override must keep inherited label/kind: %+v", tk)
	}
	if find(j, "color-text").Default == find(def, "color-text").Default || find(m, "radius").Default != "12px" {
		t.Error("overrides not applied")
	}
	if find(j, "space").Default != find(def, "space").Default {
		t.Error("untouched tokens must keep the parent's default")
	}
	if !strings.HasPrefix(j.Styles[len(j.Styles)-1], "/theme/journal/theme.css?v=") {
		t.Errorf("journal styles: %v", j.Styles)
	}

	// journal overrides the layout (centred masthead); midnight inherits the default one
	var jb, mb bytes.Buffer
	j.RenderPage(&jb, PageData{Site: j.Info("T", false), Content: "x"})
	m.RenderPage(&mb, PageData{Site: m.Info("T", false), Content: "x"})
	if !strings.Contains(jb.String(), `site-header masthead`) || strings.Contains(mb.String(), `masthead`) {
		t.Error("layout override not applied correctly")
	}
}

func TestMergeTokensValidation(t *testing.T) {
	parent := []Token{{Key: "color-a", Label: "A", Kind: "color", Default: "#000000"}}
	if _, err := mergeTokens(parent, []Token{{Key: "color-a", Default: "red"}}); err == nil {
		t.Error("invalid override accepted")
	}
	if _, err := mergeTokens(parent, []Token{{Key: "new", Kind: "weird", Default: "x"}}); err == nil {
		t.Error("unknown kind accepted")
	}
	if _, err := mergeTokens(parent, []Token{{Key: "Bad Key", Kind: "size", Default: "1rem"}}); err == nil {
		t.Error("invalid key accepted")
	}
	got, err := mergeTokens(parent, []Token{{Key: "pad", Kind: "size", Default: "2rem"}})
	if err != nil || len(got) != 2 || got[1].Label != "pad" {
		t.Errorf("new token: %+v %v", got, err)
	}
}

func TestChainOfDetectsLoops(t *testing.T) {
	metas := map[string]rawMeta{"default": {}, "a": {Extends: "b"}, "b": {Extends: "a"}, "c": {Extends: "ghost"}}
	if _, err := chainOf("a", metas); err == nil {
		t.Error("loop not detected")
	}
	if _, err := chainOf("c", metas); err == nil {
		t.Error("unknown parent not detected")
	}
	if chain, err := chainOf("default", metas); err != nil || len(chain) != 1 {
		t.Errorf("default chain: %v %v", chain, err)
	}
}

func TestInfoPreviewURL(t *testing.T) {
	e, _ := Load()
	th := e.Get("midnight")
	if got := th.Info("T", false).CSSURL; got != "/site.css" {
		t.Error(got)
	}
	if got := th.Info("T", true).CSSURL; got != "/site.css?theme=midnight" {
		t.Error(got)
	}
	if e.Get("nope").Name != "default" {
		t.Error("unknown theme must fall back to default")
	}
}

func TestFontOptionsMatchStacks(t *testing.T) {
	if len(FontOptions) != len(FontStacks) {
		t.Fatalf("%d options vs %d stacks", len(FontOptions), len(FontStacks))
	}
	for _, o := range FontOptions {
		if FontStacks[o.Key] == "" || !ValidToken("font", o.Key) {
			t.Errorf("font %q has no stack", o.Key)
		}
	}
}

// Browsers keep theme files for an hour, so the stylesheet's address must change whenever its
// content does — otherwise a visitor keeps the old CSS after the site is updated.
func TestStylesheetAddressIsFingerprinted(t *testing.T) {
	e, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	th, _ := e.Lookup("default")
	raw, err := fs.ReadFile(th.FS, "theme.css")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if want := "/theme/default/theme.css?v=" + hex.EncodeToString(sum[:5]); th.Styles[0] != want {
		t.Errorf("stylesheet address %q, want %q", th.Styles[0], want)
	}
	m, _ := e.Lookup("midnight")
	if len(m.Styles) != 2 || m.Styles[0] != th.Styles[0] || m.Styles[1] == th.Styles[0] {
		t.Errorf("a child theme loads its parent's stylesheet then its own: %v", m.Styles)
	}
}

func TestMenuScriptAddressIsFingerprinted(t *testing.T) {
	e, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := fs.ReadFile(themefiles.FS, "default/assets/menu.js")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	want := "/theme/default/assets/menu.js?v=" + hex.EncodeToString(sum[:5])
	for _, name := range []string{"default", "journal", "midnight"} {
		th, _ := e.Lookup(name)
		info := th.Info("T", false)
		if info.MenuJS != want {
			t.Errorf("%s: menu script address %q, want %q", name, info.MenuJS, want)
		}
		var buf bytes.Buffer
		if err := th.RenderPage(&buf, PageData{Site: info, Content: "x"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), `<script src="`+want+`" defer></script>`) {
			t.Errorf("%s: layout does not load the fingerprinted script", name)
		}
	}
}

func TestEveryFontOptionHasAStack(t *testing.T) {
	for _, o := range FontOptions {
		if FontStacks[o.Key] == "" || o.Label == "" {
			t.Errorf("font option %q has no stack or label", o.Key)
		}
	}
	if !strings.HasPrefix(FontStacks["arial"], "Arial") {
		t.Errorf("arial: %s", FontStacks["arial"])
	}
}
