package content

import (
	"context"
	"strings"

	"webbuilder/internal/blocks"
)

// Limits for the logo settings.
const MaxLogoAlt = 120

// LogoHeights are the sizes offered for the logo in the header, in pixels.
var LogoHeights = []struct {
	Key, Label string
	Pixels     int
}{{"sm", "Small", 32}, {"md", "Medium", 44}, {"lg", "Large", 64}}

// Brand is the site's logo and browser-tab icon.
type Brand struct {
	Logo, LogoAlt string
	LogoHeight    string // "sm", "md" or "lg"
	HideTitle     bool   // show only the logo, not the site title beside it
	Icon          string // the small picture on the browser tab
}

// HeightPixels is the logo's height in the header.
func (b Brand) HeightPixels() int {
	for _, h := range LogoHeights {
		if h.Key == b.LogoHeight {
			return h.Pixels
		}
	}
	return 44
}

func (b Brand) clean() Brand {
	b.Logo = blocks.SafeImageURL(truncateRunes(strings.TrimSpace(b.Logo), 2000))
	b.Icon = blocks.SafeImageURL(truncateRunes(strings.TrimSpace(b.Icon), 2000))
	b.LogoAlt = strings.TrimSpace(truncateRunes(strings.Join(strings.Fields(b.LogoAlt), " "), MaxLogoAlt))
	ok := false
	for _, h := range LogoHeights {
		ok = ok || h.Key == b.LogoHeight
	}
	if !ok {
		b.LogoHeight = "md"
	}
	if b.Logo == "" {
		b.HideTitle = false // the title is the only thing left to show
	}
	return b
}

func (b Brand) asJSON() map[string]any {
	b = b.clean()
	m := map[string]any{}
	if b.Logo != "" {
		m["logo"] = b.Logo
		m["height"] = b.LogoHeight
		if b.LogoAlt != "" {
			m["logo_alt"] = b.LogoAlt
		}
		if b.HideTitle {
			m["hide_title"] = true
		}
	}
	if b.Icon != "" {
		m["icon"] = b.Icon
	}
	return m
}

func brandFrom(m map[string]any) Brand {
	var b Brand
	b.Logo, _ = m["logo"].(string)
	b.LogoAlt, _ = m["logo_alt"].(string)
	b.LogoHeight, _ = m["height"].(string)
	b.HideTitle, _ = m["hide_title"].(bool)
	b.Icon, _ = m["icon"].(string)
	return b.clean()
}

func (s *Store) SetBrand(ctx context.Context, b Brand) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET brand = $1 WHERE id = 1`, b.asJSON())
	return err
}

// CleanBrand makes any stored or imported logo settings safe.
func CleanBrand(m map[string]any) map[string]any { return brandFrom(m).asJSON() }
