package site

import (
	"strings"

	"webbuilder/internal/themes"
)

// publicCSPFor is the security policy of a public page. It is the same for every page, except that a site with
// analytics services lets exactly those services' scripts and connections through, and nothing else.
func publicCSPFor(t *themes.TrackingInfo) string {
	if t == nil {
		return publicCSP
	}
	var scripts, connects []string
	if t.GA4 != "" {
		scripts = append(scripts, "https://www.googletagmanager.com")
		connects = append(connects, "https://www.googletagmanager.com", "https://*.google-analytics.com", "https://*.analytics.google.com")
	}
	if t.Plausible != "" {
		scripts = append(scripts, "https://plausible.io")
		connects = append(connects, "https://plausible.io")
	}
	const style = "style-src 'self' 'unsafe-inline'; "
	extra := "script-src 'self' " + strings.Join(scripts, " ") + "; connect-src 'self' " + strings.Join(connects, " ") + "; "
	return strings.Replace(publicCSP, style, style+extra, 1)
}
