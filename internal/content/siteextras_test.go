package content

import (
	"strings"
	"testing"
)

func TestPhoneAndWhatsAppLinks(t *testing.T) {
	for in, want := range map[string]string{
		"+27 (21) 555-0123": "tel:+27215550123", "021 555 0123": "tel:0215550123", "  +1.555.123.4567 ": "tel:+15551234567",
		"12345": "tel:12345", "12": "", "1234": "", "call me": "", "": "", "+": "", "1234567890123456": "", "555-0123 ext 4": "tel:55501234",
	} {
		if got := PhoneLink(in); got != want {
			t.Errorf("PhoneLink(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"+27 82 555 0123": "https://wa.me/27825550123", "+1 (555) 123-4567": "https://wa.me/15551234567", "082 555 0123": "", "27825550123": "", "+12": "", "": "",
	} {
		if got := WhatsAppLink(in); got != want {
			t.Errorf("WhatsAppLink(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetailsAreCleaned(t *testing.T) {
	d := detailsFrom(map[string]any{
		"phone": "<b>+27 21 555 0123</b>", "whatsapp": "+27 82 555 0123", "email": " Hello@Example.com ",
		"address": "  1 High St \r\n\r\n Town\n" + strings.Repeat("x\n", 20), "hours": strings.Repeat("h", 2000),
		"social": map[string]any{
			"facebook": "https://www.facebook.com/a", "instagram": "http://instagram.com/a", "x": "https://evil.example/x.com", "linkedin": "https://notlinkedin.com/a",
			"youtube": "https://m.youtube.com/@a", "tiktok": "javascript:alert(1)", "unknown": "https://example.com", "x2": 5,
		},
		"in_footer": true,
	})
	if d.Phone != "+27 21 555 0123" || d.WhatsApp != "+27 82 555 0123" || d.Email != "Hello@Example.com" || !d.InFooter {
		t.Errorf("basic fields: %+v", d)
	}
	if lines := strings.Split(d.Address, "\n"); len(lines) != MaxDetailLines || lines[0] != "1 High St" || lines[1] != "Town" {
		t.Errorf("address lines are tidied and limited: %q", d.Address)
	}
	if len([]rune(d.Hours)) > MaxHours {
		t.Errorf("hours limited: %d", len([]rune(d.Hours)))
	}
	if len(d.Social) != 2 || d.Social["facebook"] == "" || d.Social["youtube"] == "" {
		t.Errorf("only https addresses on the network's own site survive: %v", d.Social)
	}
	for in, ok := range map[string]bool{"a@b.co": true, "a.b+c@sub.example.org": true, "a@b": false, "a b@c.com": false, `a"@c.com`: false, "a@c.com<x>": false, "": false, "@c.com": false} {
		if got := detailsFrom(map[string]any{"email": in}).Email != ""; got != ok {
			t.Errorf("email %q accepted=%v, want %v", in, got, ok)
		}
	}
	if !detailsFrom(nil).Empty() || !detailsFrom(map[string]any{"in_footer": true}).Empty() {
		t.Error("nothing set is empty")
	}
	if m := CleanDetails(map[string]any{"phone": "no digits"}); len(m) != 0 {
		t.Errorf("nothing usable, nothing stored: %v", m)
	}
}

func TestSocialURLs(t *testing.T) {
	fb := Networks[0]
	for in, want := range map[string]string{
		"https://facebook.com/a": "https://facebook.com/a", "https://www.facebook.com/a?x=1": "https://www.facebook.com/a?x=1", "https://m.facebook.com/a": "https://m.facebook.com/a",
		"https://fb.me/a": "https://fb.me/a", "HTTPS://FACEBOOK.COM/A": "HTTPS://FACEBOOK.COM/A",
		"http://facebook.com/a": "", "https://facebook.com.evil.example/a": "", "https://evilfacebook.com/a": "", "https://user@facebook.com/a": "",
		"//facebook.com/a": "", "facebook.com/a": "", "/a": "", "": "", "ftp://facebook.com": "",
	} {
		if got := SocialURL(fb, in); got != want {
			t.Errorf("SocialURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBrandIsCleaned(t *testing.T) {
	b := brandFrom(map[string]any{"logo": " /media/logo.png ", "logo_alt": "  A   B ", "height": "huge", "hide_title": true, "icon": "javascript:alert(1)"})
	if b.Logo != "/media/logo.png" || b.LogoAlt != "A B" || b.LogoHeight != "md" || !b.HideTitle || b.Icon != "" || b.HeightPixels() != 44 {
		t.Errorf("%+v", b)
	}
	if b := brandFrom(map[string]any{"hide_title": true, "logo": "data:image/png;base64,xx"}); b.Logo != "" || b.HideTitle {
		t.Errorf("without a logo the title always shows: %+v", b)
	}
	for _, h := range LogoHeights {
		if got := (Brand{LogoHeight: h.Key}).HeightPixels(); got != h.Pixels {
			t.Errorf("%s: %d", h.Key, got)
		}
	}
	if m := CleanBrand(map[string]any{"icon": "/media/i.png", "logo_alt": "x"}); len(m) != 1 || m["icon"] != "/media/i.png" {
		t.Errorf("only what applies is stored: %v", m)
	}
	if n := len([]rune(brandFrom(map[string]any{"logo": "/a.png", "logo_alt": strings.Repeat("a", 300)}).LogoAlt)); n != MaxLogoAlt {
		t.Errorf("alt limit: %d", n)
	}
}

func TestTrackingIDsAreChecked(t *testing.T) {
	good := map[string]string{"G-ABC123XYZ": "G-ABC123XYZ", " g-abc123xyz ": "G-ABC123XYZ", "G-ABCD": "G-ABCD"}
	for in, want := range good {
		if got := trackingFrom(map[string]any{"ga4": in}).GA4; got != want {
			t.Errorf("ga4 %q → %q", in, got)
		}
	}
	for _, bad := range []string{"", "UA-123-1", "GTM-ABCDEF", "G-ABC", "G-" + strings.Repeat("A", 15), `G-ABC123"><script>`, "G-ABC 123", "G-abc-123", "AW-123456789"} {
		if got := trackingFrom(map[string]any{"ga4": bad}).GA4; got != "" {
			t.Errorf("ga4 %q should be refused, got %q", bad, got)
		}
	}
	for in, want := range map[string]string{
		"example.com": "example.com", "https://www.Example.com/": "example.com", "http://sub.example.co.uk": "sub.example.co.uk", "my-site.org": "my-site.org",
	} {
		if got := trackingFrom(map[string]any{"plausible": in}).PlausibleDomain; got != want {
			t.Errorf("plausible %q → %q", in, got)
		}
	}
	for _, bad := range []string{"", "localhost", "a b.com", `x.com"><script>`, "x.com/path", "-bad.com", "bad-.com", "x.com:8080", strings.Repeat("a", 120) + ".com", "x.com;y"} {
		if got := trackingFrom(map[string]any{"plausible": bad}).PlausibleDomain; got != "" {
			t.Errorf("plausible %q should be refused, got %q", bad, got)
		}
	}
	if !ValidGA4("") || !ValidGA4("g-abc123xyz") || ValidGA4("nope") || !ValidPlausible("") || !ValidPlausible("example.com") || ValidPlausible("nope") {
		t.Error("the form checks agree with the cleaning")
	}
	if tr := trackingFrom(map[string]any{"no_wait": true}); tr.NoWait || tr.Any() {
		t.Errorf("waiting is moot with no services: %+v", tr)
	}
	if m := CleanTracking(map[string]any{"ga4": "G-ABC123XYZ", "no_wait": true, "script": "alert(1)"}); len(m) != 2 || m["no_wait"] != true {
		t.Errorf("stored: %v", m)
	}
}
