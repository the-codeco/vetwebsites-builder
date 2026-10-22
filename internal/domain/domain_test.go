package domain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	good := map[string]string{
		"example.com":           "example.com",
		"  Example.COM  ":       "example.com",
		"https://example.com/":  "example.com",
		"http://my-site.co.uk":  "my-site.co.uk",
		"blog.example.org.":     "blog.example.org",
		"xn--bcher-kva.example": "", // .example is reserved: see below
		"xn--bcher-kva.de":      "xn--bcher-kva.de",
		"a.io":                  "a.io",
		"www.example.com":       "www.example.com",
	}
	for in, want := range good {
		got, err := Normalize(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q should be rejected", in)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "   ", "localhost", "example", "192.168.1.1", "8.8.8.8", "[::1]", "::1",
		"example.com:8080", "example.com/path", "example.com?x=1", "user@example.com", "exa mple.com",
		"-bad.com", "bad-.com", "a..com", ".com", "example.c", "example.123", "bücher.de",
		"printer.local", "app.internal", "foo.localhost", "x.test", "a.onion", "1.0.0.127.in-addr.arpa",
		strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 130) + "com",
		"javascript:alert(1)", "example.com\r\nHost: evil.com",
	}
	for _, in := range bad {
		if got, err := Normalize(in); err == nil {
			t.Errorf("Normalize(%q) = %q, want an error", in, got)
		}
	}
}

func TestAllowed(t *testing.T) {
	cases := []struct {
		configured, host string
		want             bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "EXAMPLE.com.", true},
		{"example.com", "www.example.com", true},
		{"example.com", "evil.com", false},
		{"example.com", "example.com.evil.com", false},
		{"example.com", "notexample.com", false},
		{"example.com", "sub.example.com", false},
		{"example.com", "", false},
		{"", "example.com", false}, // nothing configured: never issue certificates
		{"", "", false},
		{"www.example.com", "www.example.com", true},
		{"www.example.com", "example.com", false},
		{"www.example.com", "www.www.example.com", false},
	}
	for _, c := range cases {
		if got := Allowed(c.configured, c.host); got != c.want {
			t.Errorf("Allowed(%q, %q) = %v, want %v", c.configured, c.host, got, c.want)
		}
	}
}

// primed returns a Service whose cache already holds the domain, so no database is needed.
func primed(domain string) *Service {
	return &Service{cached: domain, fetched: time.Now()}
}

func TestTLSCheckHandler(t *testing.T) {
	s := primed("example.com")
	cases := map[string]int{
		"example.com":     200,
		"www.example.com": 200,
		"evil.com":        404,
		"":                404,
	}
	for domain, want := range cases {
		w := httptest.NewRecorder()
		s.TLSCheck(w, httptest.NewRequest("GET", "/tls-check?domain="+domain, nil))
		if w.Code != want {
			t.Errorf("domain %q: %d, want %d", domain, w.Code, want)
		}
	}
	// no domain configured: refuse everything
	w := httptest.NewRecorder()
	primed("").TLSCheck(w, httptest.NewRequest("GET", "/tls-check?domain=example.com", nil))
	if w.Code != 404 {
		t.Errorf("unconfigured: %d", w.Code)
	}
}

func TestCanonical(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("site")) })
	h := primed("example.com").Canonical(next)

	get := func(host, target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := get("www.example.com", "/blog/post?page=2")
	if w.Code != 301 || w.Header().Get("Location") != "https://example.com/blog/post?page=2" {
		t.Errorf("www redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := get("WWW.Example.com:443", "/"); w.Code != 301 {
		t.Errorf("host with port/case: %d", w.Code)
	}
	for _, host := range []string{"example.com", "localhost:8080", "www.other.com", "www.example.com.evil.com"} {
		if w := get(host, "/"); w.Code != 200 || w.Body.String() != "site" {
			t.Errorf("%s must pass through: %d", host, w.Code)
		}
	}
	// an attacker-controlled path can't turn this into an open redirect to another host
	w = get("www.example.com", "//evil.com/x")
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "https://example.com/") {
		t.Errorf("open redirect: %q", loc)
	}
}

func TestIsPublicIP(t *testing.T) {
	public := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111", "::ffff:8.8.8.8"}
	private := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.169.254", // cloud metadata
		"0.0.0.0", "100.64.0.1", "198.18.0.1", "224.0.0.1", "255.255.255.255", "240.0.0.1", "203.0.113.5",
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1", "2001:db8::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
	}
	for _, s := range public {
		if !IsPublicIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be public", s)
		}
	}
	for _, s := range private {
		if IsPublicIP(netip.MustParseAddr(s)) {
			t.Errorf("%s must not be treated as public", s)
		}
	}
}

func TestCheckerRefusesInternalAddresses(t *testing.T) {
	// a real server on loopback: the default checker must refuse to talk to it
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(instanceID)) }))
	defer srv.Close()
	c := NewChecker()
	_, _, _, err := c.fetch(context.Background(), srv.URL)
	if !errors.Is(err, errBlockedAddr) {
		t.Fatalf("loopback request not blocked: %v", err)
	}
	// ...and with the guard off (tests only) the same request works, proving the guard is what blocked it
	c.allowPrivate = true
	status, _, body, err := c.fetch(context.Background(), srv.URL)
	if err != nil || status != 200 || body != instanceID {
		t.Fatalf("unguarded fetch: %d %q %v", status, body, err)
	}
}

func TestCheckerDoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret")) }))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redir.Close()
	c := &Checker{allowPrivate: true}
	status, loc, body, err := c.fetch(context.Background(), redir.URL)
	if err != nil || status != 302 || loc != target.URL || body == "secret" {
		t.Errorf("redirect was followed or mishandled: %d %q %q %v", status, loc, body, err)
	}
}

func TestCheckerRun(t *testing.T) {
	serve := func(body string, redirectTo string) *httptest.Server {
		return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if redirectTo != "" {
				http.Redirect(w, r, redirectTo, http.StatusMovedPermanently)
				return
			}
			w.Write([]byte(body))
		}))
	}
	run := func(httpSrv, httpsSrv *httptest.Server) Report {
		c := &Checker{allowPrivate: true, httpClient: httpsSrv.Client()}
		c.urlFor = func(scheme, _ string) string {
			if scheme == "http" {
				return httpSrv.URL
			}
			return httpsSrv.URL
		}
		return c.Run(context.Background(), "localhost") // resolves everywhere, no network needed
	}
	good, redirect, wrong := serve(instanceID, ""), serve("", "https://example.com/"), serve("someone else's site", "")
	defer good.Close()
	defer redirect.Close()
	defer wrong.Close()

	if r := run(good, good); !r.OK || len(r.Steps) != 3 {
		t.Errorf("all good: %+v", r)
	}
	if r := run(redirect, good); !r.OK || !strings.Contains(r.Steps[1].Detail, "Redirects") {
		t.Errorf("http redirecting to https: %+v", r)
	}
	if r := run(good, wrong); r.OK || r.Steps[2].OK || !strings.Contains(r.Steps[2].Detail, "doesn't reach this site") {
		t.Errorf("https reaching another site: %+v", r)
	}
	if r := run(wrong, good); r.OK == false || r.Steps[1].OK {
		t.Errorf("http reaching another site should fail the http step only: %+v", r)
	}
	dead := serve("", "")
	dead.Close()
	if r := run(good, dead); r.OK || r.Steps[2].OK {
		t.Errorf("unreachable https: %+v", r)
	}
}

func TestCheckerDNSFailure(t *testing.T) {
	r := NewChecker().Run(context.Background(), "this-domain-does-not-exist.invalid")
	if r.OK || len(r.Steps) != 1 || r.Steps[0].Name != "DNS" || r.Steps[0].OK {
		t.Errorf("%+v", r)
	}
}

func TestCanonicalHTTPToHTTPS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("site")) })
	do := func(configured, host, proto string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/blog?x=1", nil)
		r.Host = host
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		w := httptest.NewRecorder()
		primed(configured).Canonical(next).ServeHTTP(w, r)
		return w
	}
	// the configured domain over plain HTTP (as reported by the proxy) goes to HTTPS
	if w := do("example.com", "example.com", "http"); w.Code != 301 || w.Header().Get("Location") != "https://example.com/blog?x=1" {
		t.Errorf("http->https: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := do("example.com", "www.example.com", "http"); w.Header().Get("Location") != "https://example.com/blog?x=1" {
		t.Errorf("www over http: %q", w.Header().Get("Location"))
	}
	// already HTTPS, or no proxy header (direct/dev access): served normally
	for _, proto := range []string{"https", ""} {
		if w := do("example.com", "example.com", proto); w.Code != 200 {
			t.Errorf("proto %q must not redirect: %d", proto, w.Code)
		}
	}
	// first-time setup: other hosts and bare IPs are never redirected, with or without a domain
	for _, host := range []string{"203.0.113.7", "203.0.113.7:80", "localhost:8080", "other.com", "intranet"} {
		if w := do("example.com", host, "http"); w.Code != 200 {
			t.Errorf("%s was redirected: %d", host, w.Code)
		}
		if w := do("", host, "http"); w.Code != 200 {
			t.Errorf("%s redirected with no domain configured: %d", host, w.Code)
		}
	}
	if w := do("", "example.com", "http"); w.Code != 200 {
		t.Errorf("no domain configured must never redirect: %d", w.Code)
	}
}
