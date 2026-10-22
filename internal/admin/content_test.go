package admin

import (
	"net/url"
	"strings"
	"testing"
	"webbuilder/internal/themes"

	"webbuilder/internal/blocks"
)

func TestFormValuesAndClean(t *testing.T) {
	hero, _ := blocks.Get("hero")
	form := url.Values{
		"p.heading":    {"Hello"},
		"p.button_url": {"javascript:alert(1)"},
		"p.size":       {"lg"},
		"p.evil":       {"ignored"},
		"s.padding":    {"lg"},
		"s.background": {"#abcdef"},
	}
	props := hero.Clean(formValues(hero.Fields, "p.", form))
	if props["heading"] != "Hello" || props["size"] != "lg" || props["button_url"] != "" {
		t.Errorf("props: %#v", props)
	}
	if _, has := props["evil"]; has {
		t.Error("unknown field leaked through")
	}
	style := blocks.CleanStyleMap(formValues(blocks.StyleFields(), "s.", form))
	if style["padding"] != "lg" || style["background"] != "#abcdef" || style["align"] != "left" {
		t.Errorf("style: %#v", style)
	}
}

func TestFormValuesBoolAndMissing(t *testing.T) {
	fields := []blocks.Field{{Key: "on", Kind: blocks.KindBool}, {Key: "off", Kind: blocks.KindBool}, {Key: "t", Kind: blocks.KindText, Default: "dflt"}}
	got := formValues(fields, "p.", url.Values{"p.on": {"on"}})
	if got["on"] != true || got["off"] != false {
		t.Errorf("bool handling: %#v", got)
	}
	if _, has := got["t"]; has {
		t.Error("absent text field should be left unset so the default applies")
	}
}

func TestRichTextSanitisedOnSave(t *testing.T) {
	rt, _ := blocks.Get("richtext")
	props := rt.Clean(formValues(rt.Fields, "p.", url.Values{
		"p.content": {`<p onclick="x()">ok</p><script>alert(1)</script><a href="javascript:alert(2)">l</a>`},
	}))
	s := props["content"].(string)
	for _, bad := range []string{"<script", "onclick", "javascript:"} {
		if strings.Contains(s, bad) {
			t.Errorf("sanitised content still has %q: %s", bad, s)
		}
	}
	if !strings.Contains(s, "<p>ok</p>") {
		t.Errorf("safe content lost: %s", s)
	}
}

func TestFieldViewsAndSummary(t *testing.T) {
	g, _ := blocks.Get("gallery")
	props := g.Clean(map[string]any{"images": "/a.png\n/b.png", "columns": "4"})
	fv := fieldViews("p.", g.Fields, props)
	if fv[0].Str != "/a.png\n/b.png" || fv[0].Name != "p.images" {
		t.Errorf("imagelist view: %#v", fv[0])
	}
	var selected string
	for _, o := range fv[1].Options {
		if o.Selected {
			selected = o.Value
		}
	}
	if selected != "4" {
		t.Errorf("selected option = %q", selected)
	}

	rt, _ := blocks.Get("richtext")
	long := "<p>" + strings.Repeat("word ", 40) + "</p>"
	sum := summarize(rt, rt.Clean(map[string]any{"content": long}))
	if strings.Contains(sum, "<") || !strings.HasSuffix(sum, "…") {
		t.Errorf("summary: %q", sum)
	}
}

func TestParseTokens(t *testing.T) {
	e, err := themes.Load()
	if err != nil {
		t.Fatal(err)
	}
	th := e.Get("default")
	got, problem := parseTokens(th, url.Values{
		"t.color-primary": {"#FF0000"}, // normalised to lower case
		"t.color-bg":      {"#ffffff"}, // equals the default: not stored
		"t.font-body":     {"serif"},
		"t.radius":        {"  12px  "}, // trimmed
		"t.space":         {""},         // blank: not stored
		"t.unknown":       {"#000000"},  // not a token of this theme
	})
	if problem != "" {
		t.Fatal(problem)
	}
	want := map[string]string{"color-primary": "#ff0000", "font-body": "serif", "radius": "12px"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	bad := []url.Values{
		{"t.color-primary": {"red"}},
		{"t.color-primary": {"#fff"}},
		{"t.color-bg": {"#ffffff;}body{display:none"}},
		{"t.radius": {"12"}},
		{"t.radius": {"expression(alert(1))"}},
		{"t.space": {"1rem;color:red"}},
		{"t.font-body": {"comic-sans"}},
		{"t.font-heading": {"sans, url(//evil)"}},
	}
	for _, form := range bad {
		if _, problem := parseTokens(th, form); problem == "" {
			t.Errorf("%v accepted", form)
		}
	}
}
