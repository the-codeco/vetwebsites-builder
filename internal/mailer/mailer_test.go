package mailer

import (
	"errors"
	"fmt"
	"net/textproto"
	"strings"
	"testing"
)

func TestValidEmail(t *testing.T) {
	for _, ok := range []string{"a@b.co", "first.last+tag@example.com", "x@sub.example.org"} {
		if !ValidEmail(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{
		"", "plain", "a@b", "@b.co", "a@", "a b@c.co", "Name <a@b.co>", "a@b.co, c@d.co", "a@b.co;c@d.co",
		"a@b.co\r\nBcc: x@y.co", "a@b.co\n", "\"quoted\"@b.co", strings.Repeat("a", 250) + "@b.co",
	} {
		if ValidEmail(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestComposeResistsHeaderInjection(t *testing.T) {
	m := Mailer{Host: "smtp.example.com", From: "Site <noreply@example.com>"}
	msg := string(m.compose("owner@example.com", "visitor@example.com", "Hello\r\nBcc: evil@example.com", "Line one\nLine two"))
	head, body, _ := strings.Cut(msg, "\r\n\r\n")
	if strings.Contains(head, "Bcc") && strings.Contains(strings.ToLower(head), "\r\nbcc:") {
		t.Errorf("an injected header got through:\n%s", head)
	}
	for _, want := range []string{"From: Site <noreply@example.com>", "To: owner@example.com", "Reply-To: visitor@example.com", "MIME-Version: 1.0", "charset=utf-8"} {
		if !strings.Contains(head, want) {
			t.Errorf("header %q missing:\n%s", want, head)
		}
	}
	if body != "Line one\r\nLine two\r\n" {
		t.Errorf("body not converted to CRLF: %q", body)
	}
	if got := string(m.compose("a@b.co", "", "Grüße", "x")); !strings.Contains(got, "Subject: =?utf-8?q?Gr=C3=BC=C3=9Fe?=") {
		t.Errorf("non-ASCII subjects must be encoded:\n%s", got)
	}
	if (Mailer{}).Configured() || !m.Configured() {
		t.Error("Configured() wrong")
	}
	if err := (Mailer{}).Send("a@b.co", "", "s", "b"); err == nil {
		t.Error("sending without configuration should fail, not hang")
	}
}

func TestComposeExtraHeadersAreLimitedToUnsubscribe(t *testing.T) {
	m := Mailer{Host: "smtp.example.com", From: "Site <noreply@example.com>"}
	msg := string(m.composeWith("a@b.co", "", "Hi", "Body", map[string]string{
		"List-Unsubscribe":      "<https://example.com/unsubscribe/abc>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		"Bcc":                   "evil@example.net",
		"X-Anything":            "nope",
	}))
	head, _, _ := strings.Cut(msg, "\r\n\r\n")
	for _, want := range []string{"List-Unsubscribe: <https://example.com/unsubscribe/abc>", "List-Unsubscribe-Post: List-Unsubscribe=One-Click"} {
		if !strings.Contains(head, want) {
			t.Errorf("header %q missing:\n%s", want, head)
		}
	}
	for _, not := range []string{"Bcc", "X-Anything", "evil@example.net"} {
		if strings.Contains(head, not) {
			t.Errorf("%q must not become a header:\n%s", not, head)
		}
	}
	// values are single lines
	msg = string(m.composeWith("a@b.co", "", "Hi", "Body", map[string]string{"List-Unsubscribe": "<https://x.example/u>\r\nBcc: evil@example.net"}))
	if h, _, _ := strings.Cut(msg, "\r\n\r\n"); strings.Contains(strings.ToLower(h), "\r\nbcc:") {
		t.Errorf("an injected header got through:\n%s", h)
	}
	if got := string(m.compose("a@b.co", "", "Hi", "Body")); strings.Contains(got, "List-Unsubscribe") {
		t.Error("ordinary mail has no unsubscribe header")
	}
}

func TestPermanentFailures(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"bad address":     {ErrBadRecipient, true},
		"wrapped":         {fmt.Errorf("sending: %w", ErrBadRecipient), true},
		"mailbox missing": {&textproto.Error{Code: 550, Msg: "no such user"}, true},
		"refused":         {&textproto.Error{Code: 554, Msg: "rejected"}, true},
		"try later":       {&textproto.Error{Code: 451, Msg: "busy"}, false},
		"mailbox full":    {&textproto.Error{Code: 452, Msg: "full"}, false},
		"network":         {errors.New("connection reset"), false},
		"not configured":  {errors.New("email is not configured"), false},
	} {
		if got := Permanent(c.err); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	if err := (Mailer{Host: "h", From: "a@b.co"}).Send("not an address", "", "s", "b"); !Permanent(err) {
		t.Errorf("an invalid recipient is permanent: %v", err)
	}
}
