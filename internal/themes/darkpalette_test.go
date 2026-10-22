package themes

import (
	"math"
	"strings"
	"testing"
)

func TestContrast(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{{"#000000", "#ffffff", 21}, {"#ffffff", "#000000", 21}, {"#777777", "#777777", 1}, {"#767676", "#ffffff", 4.54}} {
		if got := Contrast(c.a, c.b); math.Abs(got-c.want) > 0.01 {
			t.Errorf("Contrast(%s, %s) = %.3f, want %.2f", c.a, c.b, got, c.want)
		}
	}
}

func TestDarkValuePriority(t *testing.T) {
	primary := Token{Key: "color-primary", Kind: "color", Default: "#3b5bdb", Dark: "#8ba2ff"}
	bg := Token{Key: "color-bg", Kind: "color", Default: "#ffffff", Dark: "#12151c"}
	for _, c := range []struct {
		name  string
		tk    Token
		saved map[string]string
		want  string
	}{
		{"nothing saved", primary, nil, "#8ba2ff"},
		{"owner's dark colour", primary, map[string]string{"dark.color-primary": "#00ff00"}, "#00ff00"},
		{"brand colour carries over", primary, map[string]string{"color-primary": "#c0392b"}, "#c0392b"},
		{"dark beats brand", primary, map[string]string{"color-primary": "#c0392b", "dark.color-primary": "#00ff00"}, "#00ff00"},
		{"invalid dark ignored", primary, map[string]string{"dark.color-primary": "red"}, "#8ba2ff"},
		{"a light background never carries over", bg, map[string]string{"color-bg": "#fff4e0"}, "#12151c"},
		{"owner's dark background", bg, map[string]string{"color-bg": "#fff4e0", "dark.color-bg": "#000000"}, "#000000"},
	} {
		if got := DarkValue(c.tk, c.saved); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestStylesheetUsesTheOwnersDarkColours(t *testing.T) {
	e, _ := Load()
	th, _ := e.Lookup("default")
	css := th.CSS(map[string]string{"color-bg": "#fff4e0", "dark.color-bg": "#000000", "dark.color-text": "not a colour"}, "", ColorToggle, nil)
	i := strings.Index(css, `:root[data-theme="dark"]`)
	dark := css[i : i+strings.Index(css[i:], "}")]
	if !strings.Contains(dark, "--color-bg: #000000;") || !strings.Contains(dark, "--color-text: #e6e9ef;") {
		t.Errorf("dark palette:\n%s", dark)
	}
	if light := css[:i]; !strings.Contains(light, "--color-bg: #fff4e0;") || strings.Contains(light, "--dark") || strings.Contains(light, "#000000") {
		t.Errorf("light palette:\n%s", light)
	}
	// automatic mode follows the system with the same colours
	auto := th.CSS(map[string]string{"dark.color-bg": "#000000"}, "", ColorAuto, nil)
	if strings.Count(auto, "--color-bg: #000000;") != 2 {
		t.Errorf("both dark rules should use the owner's colour:\n%s", auto)
	}
	// the saved dark colours are not CSS variables of their own
	if strings.Contains(css, "dark.") || strings.Contains(css, "--dark") {
		t.Errorf("the storage key leaked into the stylesheet:\n%s", css)
	}
}

func TestDarkColorsAreTheThemesDarkPalette(t *testing.T) {
	e, _ := Load()
	for name, want := range map[string]int{"default": 6, "journal": 6, "midnight": 0} {
		th, _ := e.Lookup(name)
		if got := len(th.DarkColors()); got != want {
			t.Errorf("%s: %d editable dark colours, want %d", name, got, want)
		}
		for _, tk := range th.DarkColors() {
			if tk.Kind != "color" || tk.Dark == "" {
				t.Errorf("%s: %s should not be editable in dark mode", name, tk.Key)
			}
		}
	}
}

func TestDarkReadability(t *testing.T) {
	e, _ := Load()
	for _, name := range []string{"default", "journal"} {
		th, _ := e.Lookup(name)
		if notes := th.DarkReadability(nil); len(notes) != 0 {
			t.Errorf("%s's own dark palette should be readable: %v", name, notes)
		}
	}
	th, _ := e.Lookup("default")
	notes := th.DarkReadability(map[string]string{"dark.color-bg": "#808080", "dark.color-text": "#8a8a8a", "dark.color-primary-text": "#ffffff", "dark.color-primary": "#ffff00"})
	var all string
	for _, n := range notes {
		all += n.Text + "\n"
	}
	for _, want := range []string{"Text on Background", "Muted text on Background", "Text on primary on Primary"} {
		if !strings.Contains(all, want) {
			t.Errorf("expected a note about %q:\n%s", want, all)
		}
	}
}
