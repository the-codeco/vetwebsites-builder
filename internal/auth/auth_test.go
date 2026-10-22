package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery", h) {
		t.Error("correct password rejected")
	}
	if VerifyPassword("wrong password!!", h) {
		t.Error("wrong password accepted")
	}
	if VerifyPassword("x", "garbage") || VerifyPassword("x", "") {
		t.Error("malformed hash accepted")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("hashes should be salted")
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("short") == "" {
		t.Error("short password accepted")
	}
	if ValidatePassword(strings.Repeat("a", MaxPasswordLen+1)) == "" {
		t.Error("overlong password accepted")
	}
	if ValidatePassword("long enough pw") != "" {
		t.Error("valid password rejected")
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  Foo@Example.COM "); got != "foo@example.com" {
		t.Errorf("got %q", got)
	}
	for _, bad := range []string{"", "nope", "Name <a@b.com>", "a@b.com, c@d.com"} {
		if NormalizeEmail(bad) != "" {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, 50*time.Millisecond)
	for i := 0; i < 3; i++ {
		if l.Blocked("k") {
			t.Fatal("blocked too early")
		}
		l.Fail("k")
	}
	if !l.Blocked("k") {
		t.Error("should be blocked")
	}
	if l.Blocked("other") {
		t.Error("keys must be independent")
	}
	time.Sleep(60 * time.Millisecond)
	if l.Blocked("k") {
		t.Error("window should have expired")
	}
	l.Fail("k")
	l.Reset("k")
	if l.fails["k"] != nil {
		t.Error("reset failed")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 1.2.3.4")
	if got := (&Auth{}).ClientIP(r); got != "10.0.0.5" {
		t.Errorf("untrusted proxy: got %q", got)
	}
	if got := (&Auth{TrustProxy: true}).ClientIP(r); got != "1.2.3.4" {
		t.Errorf("trusted proxy should use rightmost entry, got %q", got)
	}
}

func TestPreCSRF(t *testing.T) {
	a := &Auth{}
	w := httptest.NewRecorder()
	token := a.PreCSRF(w, httptest.NewRequest("GET", "/", nil))
	cookies := w.Result().Cookies()
	if token == "" || len(cookies) != 1 {
		t.Fatal("token/cookie not issued")
	}

	post := func(field string, withCookie bool) bool {
		r := httptest.NewRequest("POST", "/", strings.NewReader(url.Values{"csrf": {field}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if withCookie {
			r.AddCookie(cookies[0])
		}
		return a.CheckPreCSRF(r)
	}
	if !post(token, true) {
		t.Error("valid token rejected")
	}
	if post("wrong", true) || post("", true) || post(token, false) {
		t.Error("invalid token accepted")
	}
}

func TestSessionCSRFMiddleware(t *testing.T) {
	a := &Auth{}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := a.CSRF(ok)

	withID := func(method, token, header string) int {
		r := httptest.NewRequest(method, "/", strings.NewReader(url.Values{"csrf": {token}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if header != "" {
			r.Header.Set("X-CSRF-Token", header)
		}
		r = r.WithContext(contextWithIdentity(r, &Identity{CSRF: "secret"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if withID("GET", "", "") != 200 {
		t.Error("GET should pass")
	}
	if withID("POST", "secret", "") != 200 || withID("POST", "", "secret") != 200 {
		t.Error("valid token rejected")
	}
	if withID("POST", "bad", "") != 403 || withID("POST", "", "") != 403 {
		t.Error("invalid token accepted")
	}
	// anonymous POST
	r := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Error("anonymous POST should be 403")
	}
}

func contextWithIdentity(r *http.Request, id *Identity) context.Context {
	return context.WithValue(r.Context(), ctxKey{}, id)
}

func TestCookieSecureFlagFollowsTheRequest(t *testing.T) {
	secure := func(a *Auth, r *http.Request) bool {
		w := httptest.NewRecorder()
		a.setCookie(w, r, "c", "v", 60)
		cs := w.Result().Cookies()
		return len(cs) == 1 && cs[0].Secure && cs[0].HttpOnly
	}
	plain := httptest.NewRequest("GET", "/", nil)
	viaProxy := httptest.NewRequest("GET", "/", nil)
	viaProxy.Header.Set("X-Forwarded-Proto", "https")
	tlsReq := httptest.NewRequest("GET", "https://example.com/", nil) // sets r.TLS

	if secure(&Auth{}, plain) {
		t.Error("plain HTTP must not get Secure cookies (the browser would drop them and login would break)")
	}
	if secure(&Auth{}, viaProxy) {
		t.Error("X-Forwarded-Proto must be ignored unless the proxy is trusted")
	}
	if !secure(&Auth{TrustProxy: true}, viaProxy) {
		t.Error("trusted proxy saying https => Secure")
	}
	if secure(&Auth{TrustProxy: true}, plain) {
		t.Error("trusted proxy but plain request => not Secure")
	}
	if !secure(&Auth{}, tlsReq) {
		t.Error("direct TLS => Secure")
	}
	if !secure(&Auth{SecureCookies: true}, plain) {
		t.Error("SECURE_COOKIES=true forces Secure")
	}
}

func TestHashingConcurrencyIsBounded(t *testing.T) {
	// many simultaneous verifications must all complete (queued, not dropped or deadlocked)
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	const n = 12
	done := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func() { done <- VerifyPassword("correct horse battery", hash) }()
	}
	deadline := time.After(60 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case ok := <-done:
			if !ok {
				t.Error("a queued verification failed")
			}
		case <-deadline:
			t.Fatal("verifications deadlocked")
		}
	}
	if cap(hashSlots) != 4 {
		t.Errorf("expected 4 slots, got %d", cap(hashSlots))
	}
}
