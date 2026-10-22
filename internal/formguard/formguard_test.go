package formguard

import (
	"testing"
	"time"
)

func TestToken(t *testing.T) {
	s := NewWithSecret("test", []byte("0123456789abcdef0123456789abcdef"), 5, 100)
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	tok := s.Token(now)

	if s.CheckToken(tok, now.Add(time.Second)) {
		t.Error("a form posted one second after loading is a bot")
	}
	if !s.CheckToken(tok, now.Add(10*time.Second)) || !s.CheckToken(tok, now.Add(23*time.Hour)) {
		t.Error("a normal submission was refused")
	}
	if s.CheckToken(tok, now.Add(25*time.Hour)) {
		t.Error("a day-old form must expire")
	}
	if s.CheckToken(tok, now.Add(-time.Hour)) {
		t.Error("a token from the future is forged")
	}
	forged := "1" + tok // changes the timestamp, so the signature no longer matches
	other := NewWithSecret("test", []byte("another secret another secret!!!"), 5, 100).Token(now)
	differentForm := NewWithSecret("newsletter", []byte("0123456789abcdef0123456789abcdef"), 5, 100).Token(now)
	for name, bad := range map[string]string{"empty": "", "no dot": "12345", "forged time": forged, "other secret": other, "token from another form": differentForm, "junk": "a.b", "negative": "-5.x"} {
		if s.CheckToken(bad, now.Add(time.Minute)) {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestAllowLimits(t *testing.T) {
	g := New("test", 3, 5)
	for i := 0; i < 3; i++ {
		if !g.Allow("1.1.1.1") {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if g.Allow("1.1.1.1") {
		t.Error("the fourth attempt from one address must be refused")
	}
	if !g.Allow("2.2.2.2") || !g.Allow("3.3.3.3") {
		t.Error("other addresses are unaffected")
	}
	if g.Allow("4.4.4.4") {
		t.Error("the site-wide limit must apply to everyone")
	}
}
