package content

import "context"

// Settings is the site-wide configuration (the single site_settings row).
type Settings struct {
	Title, Domain, ActiveTheme, CustomCSS string
	// Tokens holds each theme's overrides: theme name -> token key -> value.
	// Keeping them per theme means switching themes doesn't lose customisation.
	Tokens map[string]map[string]string
	// SEO holds the site-wide defaults (description, sharing image, hide from search).
	SEO SEO
	// ShowSearch turns on the search box in the themes' headers.
	ShowSearch bool
	// ColorMode is "light", "toggle" or "auto": how the site treats dark mode.
	ColorMode string
	// ColorSwitch is where the dark mode switch is offered: "floating", "footer" or "off".
	ColorSwitch string
	// Consent is the cookie notice.
	Consent Consent
	// Footer is the content the owner has written for the bottom of every page.
	Footer Footer
	// Brand is the logo and browser-tab icon; Details the contact details; Tracking the analytics services.
	Brand    Brand
	Details  Details
	Tracking Tracking
	// Layout is the page frame (full width or boxed, the background) and the header arrangement.
	Layout Layout
	// Sidebar is the column of widgets beside the content of pages that show it.
	Sidebar Sidebar
}

// TokensFor returns the saved overrides for one theme (never nil).
func (s Settings) TokensFor(theme string) map[string]string {
	if m := s.Tokens[theme]; m != nil {
		return m
	}
	return map[string]string{}
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var st Settings
	var raw, seo, consent, footer, brand, details, tracking, layout, sidebar map[string]any
	err := s.pool.QueryRow(ctx,
		`SELECT title, domain, active_theme, tokens, custom_css, seo, show_search, color_mode, color_switch, consent, footer, brand, details, tracking, layout, sidebar FROM site_settings WHERE id = 1`).
		Scan(&st.Title, &st.Domain, &st.ActiveTheme, &raw, &st.CustomCSS, &seo, &st.ShowSearch, &st.ColorMode, &st.ColorSwitch, &consent, &footer, &brand, &details, &tracking, &layout, &sidebar)
	st.Layout = layoutFrom(layout)
	st.Sidebar = sidebarFrom(sidebar)
	st.Consent = consentFrom(consent)
	st.Footer = footerFrom(footer)
	st.Brand, st.Details, st.Tracking = brandFrom(brand), detailsFrom(details), trackingFrom(tracking)
	st.SEO = SEOFrom(seo)
	st.Tokens = map[string]map[string]string{}
	for theme, v := range raw {
		inner, ok := v.(map[string]any)
		if !ok {
			continue // ignore anything that isn't {theme: {key: value}}
		}
		m := map[string]string{}
		for k, val := range inner {
			if sv, ok := val.(string); ok {
				m[k] = sv
			}
		}
		st.Tokens[theme] = m
	}
	return st, err
}

func (s *Store) SetTitle(ctx context.Context, title string) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET title = $1 WHERE id = 1`, title)
	return err
}

func (s *Store) SetColorMode(ctx context.Context, mode string) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET color_mode = $1 WHERE id = 1`, mode)
	return err
}

func (s *Store) SetColorSwitch(ctx context.Context, place string) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET color_switch = $1 WHERE id = 1`, place)
	return err
}

func (s *Store) SetShowSearch(ctx context.Context, on bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET show_search = $1 WHERE id = 1`, on)
	return err
}

func (s *Store) SetActiveTheme(ctx context.Context, name string) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET active_theme = $1 WHERE id = 1`, name)
	return err
}

// SetTokens replaces one theme's overrides; an empty map removes them.
func (s *Store) SetTokens(ctx context.Context, theme string, tokens map[string]string) error {
	if len(tokens) == 0 {
		_, err := s.pool.Exec(ctx, `UPDATE site_settings SET tokens = tokens - $1::text WHERE id = 1`, theme)
		return err
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE site_settings SET tokens = jsonb_set(tokens, ARRAY[$1::text], $2::jsonb, true) WHERE id = 1`,
		theme, tokens)
	return err
}

func (s *Store) SetCustomCSS(ctx context.Context, css string) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET custom_css = $1 WHERE id = 1`, css)
	return err
}

// SetDomain stores the site's custom domain ("" removes it).
func (s *Store) SetDomain(ctx context.Context, domain string) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET domain = $1 WHERE id = 1`, domain)
	return err
}
