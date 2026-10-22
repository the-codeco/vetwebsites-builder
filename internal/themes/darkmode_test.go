package themes

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func token(th *Theme, key string) Token {
	for _, tk := range th.Tokens {
		if tk.Key == key {
			return tk
		}
	}
	return Token{}
}

func TestWhichThemesHaveADarkPalette(t *testing.T) {
	e, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"default": true, "journal": true, "midnight": false} {
		th, _ := e.Lookup(name)
		if th.HasDark() != want {
			t.Errorf("%s: HasDark = %v, want %v", name, th.HasDark(), want)
		}
	}
	// Midnight inherits Default but replaces its colours, so Default's dark values must not leak in
	m, _ := e.Lookup("midnight")
	for _, tk := range m.Tokens {
		if tk.Dark != "" {
			t.Errorf("midnight %s has a dark value %q inherited from default", tk.Key, tk.Dark)
		}
	}
	// Journal gives its own dark colours, including text on its (lighter, warmer) dark primary
	j, _ := e.Lookup("journal")
	if token(j, "color-primary-text").Dark != "#1c1815" || token(j, "color-bg").Dark != "#1c1815" || token(j, "color-primary").Dark != "#e8866f" {
		t.Errorf("journal dark palette: %+v", j.Tokens)
	}
}

func TestMergeRejectsBadDarkValues(t *testing.T) {
	parent := []Token{{Key: "color-bg", Label: "Bg", Kind: "color", Default: "#ffffff"}, {Key: "radius", Label: "R", Kind: "size", Default: "4px"}}
	for name, child := range map[string][]Token{
		"not a colour": {{Key: "color-bg", Dark: "red;}body{display:none"}},
		"on a size":    {{Key: "radius", Dark: "#000000"}},
	} {
		if _, err := mergeTokens(parent, child); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	got, err := mergeTokens(parent, []Token{{Key: "color-bg", Dark: "#000000"}})
	if err != nil || got[0].Dark != "#000000" || got[0].Default != "#ffffff" {
		t.Errorf("a dark-only entry keeps the parent's default: %+v %v", got, err)
	}
	got, _ = mergeTokens([]Token{{Key: "color-bg", Kind: "color", Default: "#ffffff", Dark: "#111111"}}, []Token{{Key: "color-bg", Default: "#eeeeee"}})
	if got[0].Dark != "" {
		t.Errorf("a new default without a dark value must drop the inherited one: %+v", got[0])
	}
}

func TestCSSPerColorMode(t *testing.T) {
	e, _ := Load()
	th, _ := e.Lookup("default")
	none := map[string]string{}

	light := th.CSS(none, "", ColorLight, nil)
	if strings.Contains(light, "data-theme") || strings.Contains(light, "prefers-color-scheme") || strings.Contains(light, "color-scheme") {
		t.Errorf("light mode must carry no dark CSS:\n%s", light)
	}
	if th.CSS(none, "", "", nil) != light || th.CSS(none, "", "nonsense", nil) != light {
		t.Error("an empty or unknown mode behaves as light")
	}

	toggle := th.CSS(none, "/* custom */", ColorToggle, nil)
	for _, want := range []string{"color-scheme: light;", ":root[data-theme=\"dark\"] {", "color-scheme: dark;", "--color-bg: #12151c;", "--color-text: #e6e9ef;", "--color-primary: #8ba2ff;"} {
		if !strings.Contains(toggle, want) {
			t.Errorf("toggle mode lacks %q:\n%s", want, toggle)
		}
	}
	if strings.Contains(toggle, "prefers-color-scheme") {
		t.Error("toggle mode must not follow the system setting")
	}
	if !strings.HasSuffix(toggle, "/* custom */") {
		t.Error("custom CSS must come last so it can override the dark palette")
	}

	auto := th.CSS(none, "", ColorAuto, nil)
	if !strings.Contains(auto, "@media (prefers-color-scheme: dark) {\n  :root:not([data-theme=\"light\"]) {") || !strings.Contains(auto, "--color-bg: #12151c;") {
		t.Errorf("auto mode:\n%s", auto)
	}
	if strings.Count(auto, "--color-bg: #12151c;") != 2 {
		t.Error("auto mode needs the dark palette twice: for the system setting and for an explicit choice")
	}

	// a theme that is dark already has nothing to switch to, whatever the owner chose
	m, _ := e.Lookup("midnight")
	for _, mode := range []string{ColorToggle, ColorAuto} {
		if css := m.CSS(none, "", mode, nil); strings.Contains(css, "data-theme") || strings.Contains(css, "prefers-color-scheme") {
			t.Errorf("midnight in %s mode got dark CSS", mode)
		}
		if m.EffectiveColorMode(mode) != "" {
			t.Errorf("midnight effective mode %q", m.EffectiveColorMode(mode))
		}
	}
	if th.EffectiveColorMode(ColorAuto) != ColorAuto || th.EffectiveColorMode(ColorLight) != "" {
		t.Error("effective mode")
	}
}

func TestOwnerColoursInDarkMode(t *testing.T) {
	e, _ := Load()
	th, _ := e.Lookup("default")
	css := th.CSS(map[string]string{
		"color-primary": "#ff0000", "color-primary-text": "#111111", // brand: kept
		"color-bg": "#ffeedd", "color-text": "#222222", // structure: must follow the dark palette
	}, "", ColorToggle, nil)
	from := css[strings.Index(css, ":root[data-theme=\"dark\"]"):]
	dark := from[:strings.Index(from, "}")+1] // the dark palette itself, not the rules for hand-coloured blocks
	for _, want := range []string{"--color-primary: #ff0000;", "--color-primary-text: #111111;", "--color-bg: #12151c;", "--color-text: #e6e9ef;"} {
		if !strings.Contains(dark, want) {
			t.Errorf("dark block lacks %q:\n%s", want, dark)
		}
	}
	if strings.Contains(dark, "#ffeedd") || strings.Contains(dark, "#222222") {
		t.Error("the owner's light background or text colour leaked into dark mode")
	}
	// inside a block the owner gave a background, the owner's light colours are put back
	pinned := from[strings.Index(from, ".block.pinned"):]
	pinned = pinned[:strings.Index(pinned, "}")]
	for _, want := range []string{"--color-bg: #ffeedd;", "--color-text: #222222;", "--color-primary: #c0392b;", "color: var(--fg, var(--color-text))"} {
		if want == "--color-primary: #c0392b;" {
			want = "--color-primary: #ff0000;"
		}
		if !strings.Contains(pinned, want) {
			t.Errorf("pinned block rule lacks %q:\n%s", want, pinned)
		}
	}
	// the light block still honours every override
	light := css[:strings.Index(css, ":root[data-theme=\"dark\"]")]
	if !strings.Contains(light, "--color-bg: #ffeedd;") || !strings.Contains(light, "--color-primary: #ff0000;") {
		t.Errorf("light block:\n%s", light)
	}
	// an invalid brand override is ignored, not printed
	bad := th.CSS(map[string]string{"color-primary": "red;}x{"}, "", ColorToggle, nil)
	if strings.Contains(bad, "red;}") || !strings.Contains(bad[strings.Index(bad, "data-theme"):], "--color-primary: #8ba2ff;") {
		t.Errorf("invalid override:\n%s", bad)
	}
}

// --- readability: every palette must keep text, muted text, links and buttons legible ---

func luminance(hex string) float64 {
	ch := func(s string) float64 {
		n, _ := strconv.ParseUint(s, 16, 8)
		c := float64(n) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(hex[1:3]) + 0.7152*ch(hex[3:5]) + 0.0722*ch(hex[5:7])
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestPalettesAreReadable(t *testing.T) {
	e, _ := Load()
	for _, name := range []string{"default", "journal", "midnight"} {
		th, _ := e.Lookup(name)
		palettes := map[string]func(string) string{
			"light": func(k string) string { return token(th, k).Default },
		}
		if th.HasDark() {
			palettes["dark"] = func(k string) string { return token(th, k).Dark }
		}
		for mode, c := range palettes {
			for _, pair := range []struct {
				fg, bg string
				min    float64
				what   string
			}{
				{"color-text", "color-bg", 7, "body text"},
				{"color-text", "color-surface", 4.5, "text on cards"},
				{"color-muted", "color-bg", 4.5, "muted text"},
				{"color-primary", "color-bg", 4.5, "links"},
				{"color-primary-text", "color-primary", 4.5, "button text"},
			} {
				fg, bg := c(pair.fg), c(pair.bg)
				if r := contrast(fg, bg); r < pair.min {
					t.Errorf("%s/%s: %s (%s on %s) has contrast %.1f, want at least %.1f", name, mode, pair.what, fg, bg, r, pair.min)
				}
			}
		}
	}
}

func TestHandColouredBlocksInDarkMode(t *testing.T) {
	e, _ := Load()
	th, _ := e.Lookup("default")
	toggle := th.CSS(map[string]string{}, "", ColorToggle, nil)
	for _, want := range []string{
		":root[data-theme=\"dark\"] .block.pinned { --color-bg: #ffffff; --color-text: #1f2430;",
		":root[data-theme=\"dark\"] .block.text-only { color: inherit !important; }",
	} {
		if !strings.Contains(toggle, want) {
			t.Errorf("toggle mode lacks %q:\n%s", want, toggle)
		}
	}
	if strings.Contains(toggle, ":not([data-theme") {
		t.Error("toggle mode must not follow the system")
	}
	// automatic mode needs the same protection when the system setting (not a click) made the page dark
	auto := th.CSS(map[string]string{}, "", ColorAuto, nil)
	i := strings.Index(auto, "@media")
	for _, want := range []string{":root:not([data-theme=\"light\"]) .block.pinned {", ":root:not([data-theme=\"light\"]) .block.text-only { color: inherit !important; }"} {
		if i < 0 || !strings.Contains(auto[i:], want) {
			t.Errorf("auto mode lacks %q inside its media query:\n%s", want, auto)
		}
	}
	if strings.Count(auto, ".block.pinned") != 2 {
		t.Errorf("auto mode: expected the block rules for both the click and the system setting")
	}
	if strings.Contains(th.CSS(map[string]string{}, "", ColorLight, nil), ".block.pinned") {
		t.Error("light mode has no dark rules")
	}
}
