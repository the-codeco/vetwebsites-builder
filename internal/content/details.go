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
	MaxNumbers     = 5 // extra numbers, beside the main phone and WhatsApp
	MaxEmails      = 3 // extra email addresses, beside the main one
	MaxNumberLabel = 40
)

// NumberKinds are what an extra number can be. A fax is shown but never made a tap-to-call link.
var NumberKinds = []struct{ Key, Label string }{
	{"phone", "Phone"}, {"cell", "Cellphone"}, {"emergency", "Emergencies / after hours"}, {"fax", "Fax"},
}

// ContactNumber is one extra number. Label is what visitors see ("Reception", "Dr Smith"); without one, the
// kind's label is used.
type ContactNumber struct {
	Kind, Label, Number string
}

// ShownLabel is the label visitors see.
func (n ContactNumber) ShownLabel() string {
	if n.Label != "" {
		return n.Label
	}
	for _, k := range NumberKinds {
		if k.Key == n.Kind {
			return k.Label
		}
	}
	return "Phone"
}

// Link is the tap-to-call link ("" for a fax).
func (n ContactNumber) Link() string {
	if n.Kind == "fax" {
		return ""
	}
	return PhoneLink(n.Number)
}

// ContactEmail is one extra email address, with an optional label ("Accounts", "Dr Smith").
type ContactEmail struct {
	Label, Email string
}

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
	Numbers                []ContactNumber // more numbers: a second line, a cellphone, emergencies, a fax
	Emails                 []ContactEmail  // more addresses
	Address, Hours         string          // several lines
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
	out.Email = cleanEmail(d.Email)
	for _, n := range d.Numbers {
		n.Number = cleanNumber(n.Number)
		n.Label = truncateRunes(strings.TrimSpace(strings.Join(strings.Fields(n.Label), " ")), MaxNumberLabel)
		known := false
		for _, k := range NumberKinds {
			known = known || k.Key == n.Kind
		}
		if !known {
			n.Kind = "phone"
		}
		if n.Number != "" && len(out.Numbers) < MaxNumbers {
			out.Numbers = append(out.Numbers, n)
		}
	}
	for _, e := range d.Emails {
		e.Email = cleanEmail(e.Email)
		e.Label = truncateRunes(strings.TrimSpace(strings.Join(strings.Fields(e.Label), " ")), MaxNumberLabel)
		if e.Email != "" && len(out.Emails) < MaxEmails {
			out.Emails = append(out.Emails, e)
		}
	}
	for _, n := range Networks {
		if u := SocialURL(n, d.Social[n.Key]); u != "" {
			out.Social[n.Key] = u
		}
	}
	return out
}

// cleanEmail is the address if it looks like one, else "".
func cleanEmail(s string) string {
	if e := strings.TrimSpace(s); len(e) <= MaxDetailLine && emailRe.MatchString(e) {
		return e
	}
	return ""
}

// ValidEmail says whether s is usable as an email address.
func ValidEmail(s string) bool { return cleanEmail(s) != "" }

// Empty says whether there is nothing to show.
func (d Details) Empty() bool {
	return d.Phone == "" && d.WhatsApp == "" && d.Email == "" && len(d.Numbers) == 0 && len(d.Emails) == 0 &&
		d.Address == "" && d.Hours == "" && len(d.Social) == 0
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
	if len(d.Numbers) > 0 {
		var ns []any
		for _, n := range d.Numbers {
			e := map[string]any{"kind": n.Kind, "number": n.Number}
			if n.Label != "" {
				e["label"] = n.Label
			}
			ns = append(ns, e)
		}
		m["numbers"] = ns
	}
	if len(d.Emails) > 0 {
		var es []any
		for _, e := range d.Emails {
			x := map[string]any{"email": e.Email}
			if e.Label != "" {
				x["label"] = e.Label
			}
			es = append(es, x)
		}
		m["emails"] = es
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
	ns, _ := m["numbers"].([]any)
	for _, x := range ns {
		e, _ := x.(map[string]any)
		var n ContactNumber
		n.Kind, _ = e["kind"].(string)
		n.Label, _ = e["label"].(string)
		n.Number, _ = e["number"].(string)
		d.Numbers = append(d.Numbers, n)
	}
	es, _ := m["emails"].([]any)
	for _, x := range es {
		e, _ := x.(map[string]any)
		var ce ContactEmail
		ce.Label, _ = e["label"].(string)
		ce.Email, _ = e["email"].(string)
		d.Emails = append(d.Emails, ce)
	}
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
