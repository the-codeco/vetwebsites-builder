package content

import (
	"context"
	"regexp"
	"strings"
)

var (
	ga4Re       = regexp.MustCompile(`^G-[A-Z0-9]{4,14}$`)
	plausibleRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9\-]{1,23}$`)
)

// Tracking is the optional analytics services the site loads for visitors. Each is a fixed, known service: the
// owner enters an ID, never a script, so nothing the owner (or anyone who tricks the owner) types can run code.
type Tracking struct {
	GA4             string // a Google Analytics 4 measurement ID, such as G-ABC123XYZ
	PlausibleDomain string // the site's domain as set up in Plausible
	// NoWait loads the services as soon as a page opens. By default they wait until the visitor accepts the cookie notice.
	NoWait bool
}

// Any says whether a service is set up.
func (t Tracking) Any() bool { return t.GA4 != "" || t.PlausibleDomain != "" }

func (t Tracking) clean() Tracking {
	t.GA4 = strings.ToUpper(strings.TrimSpace(t.GA4))
	if !ga4Re.MatchString(t.GA4) {
		t.GA4 = ""
	}
	t.PlausibleDomain = strings.ToLower(strings.TrimSpace(t.PlausibleDomain))
	t.PlausibleDomain = strings.TrimPrefix(strings.TrimPrefix(t.PlausibleDomain, "https://"), "http://")
	t.PlausibleDomain = strings.TrimPrefix(t.PlausibleDomain, "www.")
	t.PlausibleDomain = strings.TrimRight(t.PlausibleDomain, "/")
	if len(t.PlausibleDomain) > 100 || !plausibleRe.MatchString(t.PlausibleDomain) {
		t.PlausibleDomain = ""
	}
	if !t.Any() {
		t.NoWait = false
	}
	return t
}

func (t Tracking) asJSON() map[string]any {
	t = t.clean()
	m := map[string]any{}
	if t.GA4 != "" {
		m["ga4"] = t.GA4
	}
	if t.PlausibleDomain != "" {
		m["plausible"] = t.PlausibleDomain
	}
	if t.NoWait {
		m["no_wait"] = true
	}
	return m
}

func trackingFrom(m map[string]any) Tracking {
	var t Tracking
	t.GA4, _ = m["ga4"].(string)
	t.PlausibleDomain, _ = m["plausible"].(string)
	t.NoWait, _ = m["no_wait"].(bool)
	return t.clean()
}

func (s *Store) SetTracking(ctx context.Context, t Tracking) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET tracking = $1 WHERE id = 1`, t.asJSON())
	return err
}

// CleanTracking makes any stored or imported tracking settings safe.
func CleanTracking(m map[string]any) map[string]any { return trackingFrom(m).asJSON() }

// ValidGA4 and ValidPlausible say whether text is usable as the service's ID, for form error messages.
func ValidGA4(s string) bool {
	return s == "" || ga4Re.MatchString(strings.ToUpper(strings.TrimSpace(s)))
}
func ValidPlausible(s string) bool {
	return s == "" || (Tracking{PlausibleDomain: s}).clean().PlausibleDomain != ""
}
