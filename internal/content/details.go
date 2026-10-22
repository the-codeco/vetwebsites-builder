package content

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"webbuilder/internal/blocks"
)

// Limits for the contact details.
const (
	MaxDetailLine  = 120
	MaxAddress     = 300
	MaxHours       = 500
	MaxDetailLines = 8
)

// Network is one social site the details can link to; links must be https addresses on one of its hosts.
type Network struct {
	Key, Label string
	Hosts      []string
}

// Networks are the social sites offered, in the order they are shown.
var Networks = []Network{
	{"facebook", "Facebook", []string{"facebook.com", "fb.com", "fb.me"}},
	{"instagram", "Instagram", []string{"instagram.com"}},
	{"x", "X (Twitter)", []string{"x.com", "twitter.com"}},
	{"linkedin", "LinkedIn", []string{"linkedin.com"}},
	{"youtube", "YouTube", []string{"youtube.com", "youtu.be"}},
	{"tiktok", "TikTok", []string{"tiktok.com"}},
}

// Details are the contact details of the business or person behind the site, written once and shown wherever
// a "Contact details" block or the footer asks for them.
type Details struct {
	Phone, WhatsApp, Email string
	Address, Hours         string // several lines
	Social                 map[string]string
	InFooter               bool // show them in the footer of every page
}

var (
	emailRe  = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}$`)
	digitsRe = regexp.MustCompile(`[0-9]`)
)

// PhoneLink is the tel: link for a phone number as typed ("" if it is not a usable number).
func PhoneLink(phone string) string {
	p := strings.TrimSpace(phone)
	n := len(digitsRe.FindAllString(p, -1))
	if n < 5 || n > 15 {
		return ""
	}
	var b strings.Builder
	for i, r := range p {
		if r >= '0' && r <= '9' || r == '+' && i == 0 {
			b.WriteRune(r)
		}
	}
	return "tel:" + b.String()
}

// WhatsAppLink is the wa.me link for a number as typed ("" if it is not a usable number). WhatsApp needs the
// international form, so the number must start with + and a country code.
func WhatsAppLink(number string) string {
	l := PhoneLink(number)
	if l == "" || !strings.HasPrefix(strings.TrimSpace(number), "+") {
		return ""
	}
	return "https://wa.me/" + strings.TrimPrefix(strings.TrimPrefix(l, "tel:"), "+")
}

// cleanNumber keeps what a phone number is written with and nothing else.
func cleanNumber(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if r >= '0' && r <= '9' || strings.ContainsRune("+ ()-.", r) {
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = truncateRunes(out, 30)
	if PhoneLink(out) == "" {
		return ""
	}
	return out
}

// cleanLines trims each line, drops empty ones and limits how much there is.
func cleanLines(s string, maxLen int) string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if l = strings.TrimSpace(strings.Join(strings.Fields(l), " ")); l != "" {
			lines = append(lines, truncateRunes(l, MaxDetailLine))
		}
		if len(lines) == MaxDetailLines {
			break
		}
	}
	return truncateRunes(strings.Join(lines, "\n"), maxLen)
}

// SocialURL checks a social link: an https address on one of the network's own sites.
func SocialURL(n Network, raw string) string {
	u := blocks.SafeURL(strings.TrimSpace(raw))
	if u == "" {
		return ""
	}
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.User != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(p.Hostname()), "www.")
	for _, h := range n.Hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return truncateRunes(u, 500)
		}
	}
	return ""
}

func (d Details) clean() Details {
	out := Details{
		Phone: cleanNumber(d.Phone), WhatsApp: cleanNumber(d.WhatsApp),
		Address: cleanLines(d.Address, MaxAddress), Hours: cleanLines(d.Hours, MaxHours),
		Social: map[string]string{}, InFooter: d.InFooter,
	}
	if e := strings.TrimSpace(d.Email); len(e) <= MaxDetailLine && emailRe.MatchString(e) {
		out.Email = e
	}
	for _, n := range Networks {
		if u := SocialURL(n, d.Social[n.Key]); u != "" {
			out.Social[n.Key] = u
		}
	}
	return out
}

// Empty says whether there is nothing to show.
func (d Details) Empty() bool {
	return d.Phone == "" && d.WhatsApp == "" && d.Email == "" && d.Address == "" && d.Hours == "" && len(d.Social) == 0
}

func (d Details) asJSON() map[string]any {
	d = d.clean()
	m := map[string]any{}
	for k, v := range map[string]string{"phone": d.Phone, "whatsapp": d.WhatsApp, "email": d.Email, "address": d.Address, "hours": d.Hours} {
		if v != "" {
			m[k] = v
		}
	}
	if len(d.Social) > 0 {
		soc := map[string]any{}
		for k, v := range d.Social {
			soc[k] = v
		}
		m["social"] = soc
	}
	if d.InFooter {
		m["in_footer"] = true
	}
	return m
}

func detailsFrom(m map[string]any) Details {
	var d Details
	d.Phone, _ = m["phone"].(string)
	d.WhatsApp, _ = m["whatsapp"].(string)
	d.Email, _ = m["email"].(string)
	d.Address, _ = m["address"].(string)
	d.Hours, _ = m["hours"].(string)
	d.InFooter, _ = m["in_footer"].(bool)
	d.Social = map[string]string{}
	if soc, ok := m["social"].(map[string]any); ok {
		for k, v := range soc {
			if s, ok := v.(string); ok {
				d.Social[k] = s
			}
		}
	}
	return d.clean()
}

func (s *Store) SetDetails(ctx context.Context, d Details) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET details = $1 WHERE id = 1`, d.asJSON())
	return err
}

// CleanDetails makes any stored or imported contact details safe.
func CleanDetails(m map[string]any) map[string]any { return detailsFrom(m).asJSON() }
