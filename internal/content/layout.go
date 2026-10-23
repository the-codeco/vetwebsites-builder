package content

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"webbuilder/internal/blocks"
)

// Layout is how the whole page is framed, whatever the theme: full width or a centred box on a background, and
// which header the site has (see Header).
type Layout struct {
	Boxed bool // a centred page on a background, instead of full width
	// Width is the box's width in pixels (Boxed only).
	Width int
	// Background is the colour behind the page (behind the box, or behind a full-width page's background image);
	// "" means the theme's own background.
	Background string
	// Image is a picture behind the page; ImageMode says how it is laid: "cover" (fills the window and stays put),
	// "top" (across the top of the page, then the background colour) or "tile" (repeated).
	Image, ImageMode string
	Header           Header
}

// Header styles.
const (
	HeaderInline   = "inline"   // the logo with the menu beside it (the themes' own header)
	HeaderStacked  = "stacked"  // the logo (and tagline) with a banner beside or behind it, the menu as a bar below
	HeaderCentered = "centered" // the menu as a bar on top, a large centred logo or title (and tagline) below
)

// Header is the site header's arrangement. Inline is the themes' own; the others place the logo and menu
// differently and can show a tagline, banner pictures (more than one cross-fade) and a coloured menu bar.
type Header struct {
	Style   string
	Tagline string
	// Banners are pictures shown in the header (stacked style); BannerPlace is "beside" (to the right of the logo)
	// or "behind" (filling the header behind the logo). BannerHeight is "sm", "md" or "lg".
	Banners                   []string
	BannerPlace, BannerHeight string
	// The menu bar's colours ("" is the theme's): its background, its links, and the current page's highlight.
	BarBackground, BarText, BarActive string
}

// Limits for the layout settings.
const (
	MinBoxWidth   = 640
	MaxBoxWidth   = 1600
	DefBoxWidth   = 1000
	MaxBanners    = 6
	MaxTagline    = 120
	maxLayoutURL  = 2000
	defImageMode  = "cover"
	defBannerSize = "md"
)

var (
	ImageModes    = []struct{ Key, Label string }{{"cover", "Fill the window"}, {"top", "Across the top of the page"}, {"tile", "Repeat as a pattern"}}
	HeaderStyles  = []struct{ Key, Label string }{{HeaderInline, "Logo with the menu beside it"}, {HeaderStacked, "Logo and banner, with the menu as a bar below"}, {HeaderCentered, "Menu bar on top, a large centred logo or title below"}}
	BannerPlaces  = []struct{ Key, Label string }{{"beside", "Beside the logo"}, {"behind", "Behind the logo, filling the header"}}
	BannerHeights = []struct {
		Key, Label string
		Pixels     int
	}{{"sm", "Short", 120}, {"md", "Medium", 160}, {"lg", "Tall", 250}}

	layoutColorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)
)

func pick[T any](v string, opts []T, key func(T) string, def string) string {
	for _, o := range opts {
		if key(o) == v {
			return v
		}
	}
	return def
}

func layoutColor(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if layoutColorRe.MatchString(s) {
		return s
	}
	return ""
}

func (l Layout) clean() Layout {
	k := func(o struct{ Key, Label string }) string { return o.Key }
	out := Layout{
		Boxed:      l.Boxed,
		Width:      l.Width,
		Background: layoutColor(l.Background),
		Image:      blocks.SafeImageURL(truncateRunes(strings.TrimSpace(l.Image), maxLayoutURL)),
		ImageMode:  pick(l.ImageMode, ImageModes, k, defImageMode),
	}
	if out.Width < MinBoxWidth || out.Width > MaxBoxWidth {
		out.Width = DefBoxWidth
	}
	h := l.Header
	out.Header = Header{
		Style:       pick(h.Style, HeaderStyles, k, HeaderInline),
		Tagline:     truncateRunes(strings.TrimSpace(strings.Join(strings.Fields(h.Tagline), " ")), MaxTagline),
		BannerPlace: pick(h.BannerPlace, BannerPlaces, k, "beside"),
		BannerHeight: pick(h.BannerHeight, BannerHeights, func(o struct {
			Key, Label string
			Pixels     int
		}) string {
			return o.Key
		}, defBannerSize),
		BarBackground: layoutColor(h.BarBackground),
		BarText:       layoutColor(h.BarText),
		BarActive:     layoutColor(h.BarActive),
	}
	for _, b := range h.Banners {
		if u := blocks.SafeImageURL(truncateRunes(strings.TrimSpace(b), maxLayoutURL)); u != "" && len(out.Header.Banners) < MaxBanners {
			out.Header.Banners = append(out.Header.Banners, u)
		}
	}
	return out
}

// BannerPixels is the header banner's height.
func (h Header) BannerPixels() int {
	for _, b := range BannerHeights {
		if b.Key == h.BannerHeight {
			return b.Pixels
		}
	}
	return 160
}

// CSS is the page-framing rules added to every theme's stylesheet: the box, the background and the menu bar's
// colours. It is built only from checked values (colours, a width, safe picture addresses).
func (l Layout) CSS() string {
	l = l.clean()
	var b strings.Builder
	bg := l.Background
	if bg == "" {
		bg = "var(--color-bg)"
	}
	if l.Boxed || l.Image != "" || l.Background != "" {
		fmt.Fprintf(&b, "html { background-color: %s; }\n", bg)
	}
	if l.Image != "" {
		img := cssURL(l.Image)
		switch l.ImageMode {
		case "top":
			fmt.Fprintf(&b, "html { background-image: url(%s); background-repeat: no-repeat; background-position: top center; background-size: 100%% auto; }\n", img)
		case "tile":
			fmt.Fprintf(&b, "html { background-image: url(%s); background-repeat: repeat; }\n", img)
		default:
			fmt.Fprintf(&b, "html { background-image: url(%s); background-repeat: no-repeat; background-position: center; background-size: cover; background-attachment: fixed; }\n", img)
		}
	}
	if l.Boxed {
		fmt.Fprintf(&b, "body { max-width: %dpx; margin: 1.25rem auto; background: var(--color-bg); box-shadow: 0 2px 18px rgba(0, 0, 0, .18); }\n", l.Width)
		// phones show the page full width: no box, and no background below a short page
		b.WriteString("@media (max-width: 700px) { html { background: var(--color-bg); } body { margin: 0; box-shadow: none; } }\n")
	} else if l.Image != "" {
		// the picture shows through a full-width page, behind the header too (the menu bar keeps its own colour)
		b.WriteString("body, .site-header { background: transparent; }\n")
	}
	h := l.Header
	if h.BarBackground != "" {
		fmt.Fprintf(&b, ":root { --bar-bg: %s; }\n", h.BarBackground)
	}
	if h.BarText != "" {
		fmt.Fprintf(&b, ":root { --bar-fg: %s; }\n", h.BarText)
	}
	if h.BarActive != "" {
		fmt.Fprintf(&b, ":root { --bar-active: %s; }\n", h.BarActive)
	}
	return b.String()
}

// cssURL quotes a checked picture address for url(…).
func cssURL(u string) string {
	return `"` + strings.NewReplacer(`"`, `%22`, `\`, `%5C`, "\n", "", "\r", "", "(", "%28", ")", "%29").Replace(u) + `"`
}

func (l Layout) asJSON() map[string]any {
	l = l.clean()
	m := map[string]any{"image_mode": l.ImageMode, "width": l.Width}
	if l.Boxed {
		m["boxed"] = true
	}
	if l.Background != "" {
		m["background"] = l.Background
	}
	if l.Image != "" {
		m["image"] = l.Image
	}
	h := l.Header
	hm := map[string]any{"style": h.Style, "banner_place": h.BannerPlace, "banner_height": h.BannerHeight}
	for k, v := range map[string]string{"tagline": h.Tagline, "bar_background": h.BarBackground, "bar_text": h.BarText, "bar_active": h.BarActive} {
		if v != "" {
			hm[k] = v
		}
	}
	if len(h.Banners) > 0 {
		bs := make([]any, len(h.Banners))
		for i, s := range h.Banners {
			bs[i] = s
		}
		hm["banners"] = bs
	}
	m["header"] = hm
	return m
}

func layoutFrom(m map[string]any) Layout {
	var l Layout
	l.Boxed, _ = m["boxed"].(bool)
	if w, ok := m["width"].(float64); ok {
		l.Width = int(w)
	}
	l.Background, _ = m["background"].(string)
	l.Image, _ = m["image"].(string)
	l.ImageMode, _ = m["image_mode"].(string)
	hm, _ := m["header"].(map[string]any)
	h := &l.Header
	h.Style, _ = hm["style"].(string)
	h.Tagline, _ = hm["tagline"].(string)
	h.BannerPlace, _ = hm["banner_place"].(string)
	h.BannerHeight, _ = hm["banner_height"].(string)
	h.BarBackground, _ = hm["bar_background"].(string)
	h.BarText, _ = hm["bar_text"].(string)
	h.BarActive, _ = hm["bar_active"].(string)
	bs, _ := hm["banners"].([]any)
	for _, b := range bs {
		if s, ok := b.(string); ok {
			h.Banners = append(h.Banners, s)
		}
	}
	return l.clean()
}

func (s *Store) SetLayout(ctx context.Context, l Layout) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET layout = $1 WHERE id = 1`, l.asJSON())
	return err
}

// CleanLayout makes any stored or imported layout settings safe.
func CleanLayout(m map[string]any) map[string]any { return layoutFrom(m).asJSON() }
