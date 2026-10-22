package domain

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// Step is one line of the connection report.
type Step struct {
	Name   string
	OK     bool
	Detail string
}

type Report struct {
	Domain string
	Steps  []Step
	OK     bool // everything passed, including HTTPS
}

// Checker makes requests to the admin-supplied domain. That is a server-side
// request to a name someone typed in, so it must never be usable to probe the
// server's own network: every connection is checked against the resolved
// address and refused unless it is a public one.
type Checker struct {
	allowPrivate bool                          // tests only
	urlFor       func(scheme, d string) string // tests only
	httpClient   *http.Client                  // tests only
}

func NewChecker() *Checker { return &Checker{} }

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b:1::/48", "2001:db8::/32",
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// IsPublicIP reports whether ip is a routable address on the public internet.
func IsPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

var errBlockedAddr = errors.New("refusing to connect to a non-public address")

// guard runs after DNS resolution, on the address actually being dialled, so
// it also covers hostnames that resolve to internal addresses (DNS rebinding).
func guard(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errBlockedAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !IsPublicIP(ip) {
		return errBlockedAddr
	}
	return nil
}

func (c *Checker) client() *http.Client {
	if c.httpClient != nil {
		return c.httpClient
	}
	d := &net.Dialer{Timeout: 8 * time.Second}
	if !c.allowPrivate {
		d.Control = guard
	}
	return &http.Client{
		Timeout: 20 * time.Second, // a first HTTPS request may wait while Caddy obtains the certificate
		Transport: &http.Transport{
			Proxy:               nil, // never via an environment proxy
			DialContext:         d.DialContext,
			TLSHandshakeTimeout: 18 * time.Second,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12}, // certificates are verified
			DisableKeepAlives:   true,
		},
	}
}

func (c *Checker) url(scheme, d string) string {
	if c.urlFor != nil {
		return c.urlFor(scheme, d)
	}
	return scheme + "://" + d + "/.well-known/site-id"
}

// fetch returns the status, the redirect target (if any) and up to 64 bytes of body.
func (c *Checker) fetch(ctx context.Context, url string) (status int, location, body string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", "", err
	}
	req.Header.Set("User-Agent", "webbuilder-domain-check")
	// never follow redirects (a redirect could point at an internal address); enforced
	// here so every client, including injected ones, behaves the same
	cl := *c.client()
	cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := cl.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return resp.StatusCode, resp.Header.Get("Location"), strings.TrimSpace(string(b)), nil
}

// Run checks DNS, plain HTTP and HTTPS for domain and reports in plain language.
func (c *Checker) Run(ctx context.Context, domain string) Report {
	rep := Report{Domain: domain}
	add := func(name string, ok bool, detail string) { rep.Steps = append(rep.Steps, Step{name, ok, detail}) }

	// 1. DNS
	addrs, err := net.DefaultResolver.LookupHost(ctx, domain)
	if err != nil || len(addrs) == 0 {
		add("DNS", false, "The domain doesn't point anywhere yet. Add the DNS records below, then check again (changes can take from a few minutes to a few hours).")
		return rep
	}
	var public []string
	for _, a := range addrs {
		if ip, err := netip.ParseAddr(a); err == nil && IsPublicIP(ip) {
			public = append(public, a)
		}
	}
	if len(public) == 0 && !c.allowPrivate {
		add("DNS", false, fmt.Sprintf("The domain points to %s, which isn't a public address. It should point to your server's public IP.", strings.Join(addrs, ", ")))
		return rep
	}
	add("DNS", true, "Points to "+strings.Join(addrs, ", ")+".")

	// 2. HTTP: either serves the site or redirects to HTTPS
	status, loc, body, err := c.fetch(ctx, c.url("http", domain))
	switch {
	case err != nil:
		add("HTTP (port 80)", false, "Couldn't connect: "+simplify(err)+" Make sure the server is running and port 80 is open to the internet.")
	case body == instanceID:
		add("HTTP (port 80)", true, "Reaches this site.")
	case status >= 300 && status < 400 && strings.HasPrefix(strings.ToLower(loc), "https://"):
		add("HTTP (port 80)", true, "Redirects visitors to HTTPS.")
	default:
		add("HTTP (port 80)", false, fmt.Sprintf("Something answered (status %d) but it isn't this site. Check that the domain points at this server.", status))
	}

	// 3. HTTPS, with certificate verification. The first request makes Caddy obtain the certificate.
	status, _, body, err = c.fetch(ctx, c.url("https", domain))
	switch {
	case err != nil:
		add("HTTPS (port 443)", false, "Couldn't establish a secure connection: "+simplify(err)+
			" The certificate is requested on the first visit and can take up to a minute, so try again shortly. Port 443 must be open too.")
	case body == instanceID:
		add("HTTPS (port 443)", true, "Works, with a valid certificate, and reaches this site.")
		rep.OK = true
	default:
		add("HTTPS (port 443)", false, fmt.Sprintf("Secure connection works but it doesn't reach this site (status %d).", status))
	}
	return rep
}

// simplify trims Go's nested error text down to something readable.
func simplify(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, errBlockedAddr):
		return "that address isn't public."
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return "the connection timed out."
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && i+2 < len(msg) {
		msg = msg[i+2:]
	}
	return msg + "."
}
