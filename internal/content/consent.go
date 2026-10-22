package content

import (
	"context"

	"webbuilder/internal/blocks"
)

// DefaultConsentText is shown when the owner hasn't written their own.
const DefaultConsentText = "This site only uses the cookies and browser storage it needs to work. It doesn't track you or share your data."

// Limits for the notice's text fields.
const (
	MaxConsentText  = 300
	MaxConsentLabel = 40
)

// Consent is the cookie notice. With Ask the visitor chooses Accept or Decline; without it
// the notice simply informs and has one button to close it.
type Consent struct {
	Enabled, Ask       bool
	Text               string // "" means DefaultConsentText
	LinkLabel, LinkURL string // an optional "Privacy policy" style link
}

// Message is the text to show.
func (c Consent) Message() string {
	if c.Text == "" {
		return DefaultConsentText
	}
	return c.Text
}

func (c Consent) asJSON() map[string]any {
	m := map[string]any{}
	if c.Enabled {
		m["enabled"] = true
	}
	if c.Ask {
		m["ask"] = true
	}
	if c.Text != "" {
		m["text"] = c.Text
	}
	// a link needs both a label and an address that is safe to follow
	if c.LinkLabel != "" && blocks.SafeURL(c.LinkURL) != "" {
		m["link_label"], m["link_url"] = c.LinkLabel, blocks.SafeURL(c.LinkURL)
	}
	return m
}

func consentFrom(m map[string]any) Consent {
	var c Consent
	c.Enabled, _ = m["enabled"].(bool)
	c.Ask, _ = m["ask"].(bool)
	c.Text, _ = m["text"].(string)
	c.LinkLabel, _ = m["link_label"].(string)
	c.LinkURL, _ = m["link_url"].(string)
	if blocks.SafeURL(c.LinkURL) == "" { // never hand an unsafe address on, even if one was stored
		c.LinkLabel, c.LinkURL = "", ""
	}
	return c
}

func (s *Store) SetConsent(ctx context.Context, c Consent) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET consent = $1 WHERE id = 1`, c.asJSON())
	return err
}

// CleanConsent makes any stored or imported cookie-notice JSON safe: only the known fields survive and an
// address that isn't safe to follow is dropped.
func CleanConsent(m map[string]any) map[string]any {
	c := consentFrom(m)
	c.Text = truncateRunes(c.Text, MaxConsentText)
	c.LinkLabel = truncateRunes(c.LinkLabel, MaxConsentLabel)
	return c.asJSON()
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
