package themes

import (
	"fmt"
	"math"
	"strconv"
)

// DarkPrefix marks an owner's dark-palette colour among a theme's saved overrides
// ("dark.color-bg" is the dark-mode background); the plain key is the light-mode value.
const DarkPrefix = "dark."

// DarkColors are the tokens the owner can recolour for dark mode: the colours the theme has a dark palette for.
func (t *Theme) DarkColors() []Token {
	var out []Token
	for _, tk := range t.Tokens {
		if tk.Kind == "color" && tk.Dark != "" {
			out = append(out, tk)
		}
	}
	return out
}

// DarkValue is the colour a token has in dark mode: the owner's dark colour if they set one; otherwise,
// for a brand colour, the one they chose for light mode (a brand colour shouldn't change because the
// background did); otherwise the theme's own dark colour.
func DarkValue(tk Token, saved map[string]string) string {
	if v, ok := saved[DarkPrefix+tk.Key]; ok && ValidToken(tk.Kind, v) {
		return v
	}
	if v, ok := saved[tk.Key]; ok && brandTokens[tk.Key] && ValidToken(tk.Kind, v) {
		return v
	}
	return tk.Dark
}

// LightValue is the colour a token has in light mode.
func LightValue(tk Token, saved map[string]string) string {
	if v, ok := saved[tk.Key]; ok && ValidToken(tk.Kind, v) {
		return v
	}
	return tk.Default
}

func luminanceOf(hex string) float64 {
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

// Contrast is the WCAG contrast ratio (1–21) between two #rrggbb colours.
func Contrast(a, b string) float64 {
	la, lb := luminanceOf(a), luminanceOf(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// ReadabilityNote describes one pair of colours that is hard to read.
type ReadabilityNote struct {
	Text  string
	Ratio float64
}

// readabilityPairs are the combinations that must stay legible: foreground, background, the least ratio, and what they are.
var readabilityPairs = []struct {
	fg, bg string
	min    float64
	what   string
}{
	{"color-text", "color-bg", 4.5, "Text on Background"},
	{"color-text", "color-surface", 4.5, "Text on Surface"},
	{"color-muted", "color-bg", 4.5, "Muted text on Background"},
	{"color-primary", "color-bg", 3, "Primary (links, buttons) on Background"},
	{"color-primary-text", "color-primary", 4.5, "Text on primary on Primary"},
}

// DarkReadability lists the colour pairs in the site's dark palette that fall below the WCAG minimum,
// so the owner can be told. It is advice only: the owner may still choose any colours.
func (t *Theme) DarkReadability(saved map[string]string) []ReadabilityNote {
	return t.readability(saved, DarkValue)
}

func (t *Theme) readability(saved map[string]string, value func(Token, map[string]string) string) []ReadabilityNote {
	byKey := map[string]string{}
	for _, tk := range t.Tokens {
		if tk.Kind == "color" {
			byKey[tk.Key] = value(tk, saved)
		}
	}
	var out []ReadabilityNote
	for _, p := range readabilityPairs {
		fg, bg := byKey[p.fg], byKey[p.bg]
		if fg == "" || bg == "" {
			continue
		}
		if r := Contrast(fg, bg); r < p.min {
			out = append(out, ReadabilityNote{fmt.Sprintf("%s is %.1f:1; at least %.1f:1 is recommended.", p.what, r, p.min), r})
		}
	}
	return out
}
