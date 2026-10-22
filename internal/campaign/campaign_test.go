package campaign

import (
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	s, b, problem := Clean("  Autumn \r\n news  ", "Line one\r\nLine two\x00\x07\r\n\r\n  ")
	if problem != "" || s != "Autumn news" || b != "Line one\nLine two" {
		t.Errorf("clean: %q %q %q", s, b, problem)
	}
	if _, b, _ := Clean("s", "a\tb\n\nc"); b != "a\tb\n\nc" {
		t.Errorf("tabs and blank lines are kept: %q", b)
	}
	for name, c := range map[string][2]string{
		"no subject": {"", "x"}, "blank subject": {"  \r\n ", "x"}, "no body": {"x", " \n "},
		"long subject": {strings.Repeat("s", MaxSubject+1), "x"}, "long body": {"x", strings.Repeat("b", MaxBody+1)},
	} {
		if _, _, problem := Clean(c[0], c[1]); problem == "" {
			t.Errorf("%s should be refused", name)
		}
	}
	if _, _, problem := Clean(strings.Repeat("é", MaxSubject), strings.Repeat("ü", MaxBody)); problem != "" {
		t.Errorf("the limits count characters, not bytes: %s", problem)
	}
}

func TestComposeGivesEveryoneTheirOwnLink(t *testing.T) {
	c := Campaign{Subject: "News", Body: "Hello", BaseURL: "https://example.com/", SiteTitle: "Example\r\nCo"}
	subject, body, headers := Compose(c, "TOKEN123")
	if subject != "News" || !strings.HasPrefix(body, "Hello\n\n-- \n") {
		t.Errorf("subject %q, body %q", subject, body)
	}
	for _, want := range []string{"You are receiving this because you subscribed to Example Co.", "To unsubscribe: https://example.com/unsubscribe/TOKEN123\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if headers["List-Unsubscribe"] != "<https://example.com/unsubscribe/TOKEN123>" || headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Errorf("headers: %v", headers)
	}
	if strings.Contains(body, "//unsubscribe") {
		t.Error("a trailing slash on the site address must not double up")
	}
}
