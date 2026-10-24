// Package wizard is the new-website wizard: ready-made looks ("kits"), ready-made home page sections, and the code that
// turns a person's answers into the site's real settings. It only chooses and checks; every value goes through the same
// validators the Appearance, Menus, Footer and Site details screens use, so the wizard can never save something those
// screens would refuse.
package wizard

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Kit is a complete look: a theme with its colours, fonts and corners, how the header, top bar and footer are
// dressed, and how dark mode behaves. Every value is one the Appearance screen offers.
type Kit struct {
	ID, Name, Blurb string
	Theme           string            // themes/<name>
	Tokens          map[string]string // overrides of the theme's own tokens (colours, fonts, radius)
	ColorMode       string            // "light" (no switch), "toggle" or "auto"
	Header          string            // content.HeaderInline or HeaderCentered
	TopBg, TopFg    string            // the top bar's colours
	FootBg, FootFg  string            // the footer's colours
}

// Kits are the looks the wizard offers, in the order shown. Fonts are the system fonts the themes already use: nothing
// is downloaded for any of them.
var Kits = []Kit{
	{ID: "clinical", Name: "Clean & clinical", Blurb: "Crisp white and a calm teal. Easy to read, trusted and modern.",
		Theme: "default", ColorMode: "toggle", Header: "inline", TopBg: "#0b5f68", TopFg: "#ffffff", FootBg: "#0f2a2e", FootFg: "#e8f2f3",
		Tokens: map[string]string{"color-bg": "#ffffff", "color-text": "#1b2b2e", "color-muted": "#5a6b6e", "color-surface": "#eef6f7",
			"color-primary": "#0e7c86", "color-primary-text": "#ffffff", "font-body": "sans", "font-heading": "sans", "radius": "6px"}},
	{ID: "fresh", Name: "Fresh & natural", Blurb: "Leafy greens on a soft, light background. Calm and caring.",
		Theme: "default", ColorMode: "toggle", Header: "inline", TopBg: "#2f6b34", TopFg: "#ffffff", FootBg: "#1f3d22", FootFg: "#eaf4e8",
		Tokens: map[string]string{"color-bg": "#f9fcf7", "color-text": "#1e2b1f", "color-muted": "#5b6b5c", "color-surface": "#e9f3e5",
			"color-primary": "#2f7d32", "color-primary-text": "#ffffff", "font-body": "humanist", "font-heading": "humanist", "radius": "10px"}},
	{ID: "friendly", Name: "Warm & friendly", Blurb: "Cream and a warm amber, with rounded type and soft corners.",
		Theme: "default", ColorMode: "toggle", Header: "inline", TopBg: "#8a4a0b", TopFg: "#ffffff", FootBg: "#3b2a1a", FootFg: "#fbf0e0",
		Tokens: map[string]string{"color-bg": "#fffaf3", "color-text": "#2e241a", "color-muted": "#7a6a58", "color-surface": "#fcefd9",
			"color-primary": "#b45309", "color-primary-text": "#ffffff", "font-body": "rounded", "font-heading": "rounded", "radius": "16px"}},
	{ID: "bold", Name: "Bold & modern", Blurb: "Deep blue on white, strong headings and sharp corners.",
		Theme: "default", ColorMode: "toggle", Header: "inline", TopBg: "#0f172a", TopFg: "#ffffff", FootBg: "#0f172a", FootFg: "#e5e9f2",
		Tokens: map[string]string{"color-bg": "#ffffff", "color-text": "#0f172a", "color-muted": "#546078", "color-surface": "#eef2fb",
			"color-primary": "#1d4ed8", "color-primary-text": "#ffffff", "font-body": "sans", "font-heading": "trebuchet", "radius": "2px"}},
	{ID: "classic", Name: "Classic & literary", Blurb: "A paper-toned page, classical serif type and a centred masthead.",
		Theme: "journal", ColorMode: "toggle", Header: "centered", TopBg: "#3a2c24", TopFg: "#f6efe6", FootBg: "#2b2622", FootFg: "#efe7dc",
		Tokens: map[string]string{}},
	{ID: "night", Name: "Night sky", Blurb: "A dark theme with a teal accent. Always dark, no switch.",
		Theme: "midnight", ColorMode: "light", Header: "inline", TopBg: "#1a1f2b", TopFg: "#e6e9ef", FootBg: "#0b0e13", FootFg: "#c9cfdb",
		Tokens: map[string]string{}},
}

// KitByID finds a kit.
func KitByID(id string) (Kit, bool) {
	for _, k := range Kits {
		if k.ID == id {
			return k, true
		}
	}
	return Kit{}, false
}

// Swatches are the colours that show what a kit looks like.
func (k Kit) Swatches() []string {
	out := []string{}
	for _, key := range []string{"color-bg", "color-surface", "color-primary", "color-text"} {
		if v := k.Tokens[key]; v != "" {
			out = append(out, v)
		}
	}
	switch k.ID {
	case "classic":
		out = []string{"#faf7f2", "#efe9df", "#9c3d2a", "#2b2622"}
	case "night":
		out = []string{"#0f1218", "#1a1f2b", "#2dd4bf", "#e6e9ef"}
	}
	return out
}

// --- colour arithmetic, so one chosen colour can carry the others with it ---

type rgb struct{ r, g, b float64 }

func parseHex(s string) (rgb, bool) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "#")
	if len(s) != 6 {
		return rgb{}, false
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return rgb{}, false
	}
	return rgb{float64(n >> 16 & 255), float64(n >> 8 & 255), float64(n & 255)}, true
}

func (c rgb) hex() string {
	f := func(v float64) int { return int(math.Round(math.Min(255, math.Max(0, v)))) }
	return fmt.Sprintf("#%02x%02x%02x", f(c.r), f(c.g), f(c.b))
}

func (c rgb) luminance() float64 {
	lin := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

// Contrast is the WCAG contrast ratio of two colours (1 to 21).
func Contrast(a, b string) float64 {
	x, ok1 := parseHex(a)
	y, ok2 := parseHex(b)
	if !ok1 || !ok2 {
		return 0
	}
	l1, l2 := x.luminance(), y.luminance()
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + .05) / (l2 + .05)
}

// mix is a colour a part of the way (0 to 1) from a to b.
func mix(a, b string, t float64) string {
	x, ok1 := parseHex(a)
	y, ok2 := parseHex(b)
	if !ok1 || !ok2 {
		return a
	}
	return rgb{x.r + (y.r-x.r)*t, x.g + (y.g-x.g)*t, x.b + (y.b-x.b)*t}.hex()
}

// readableOn is white or near-black, whichever reads better on the colour.
func readableOn(bg string) string {
	if Contrast(bg, "#ffffff") >= Contrast(bg, "#111111") {
		return "#ffffff"
	}
	return "#111111"
}
