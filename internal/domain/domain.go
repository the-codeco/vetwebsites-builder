// Package domain handles the site's custom domain: validating it, telling
// Caddy which hostnames may get a certificate, redirecting www, and checking
// that DNS and HTTPS are set up.
package domain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"webbuilder/internal/content"
)

var (
	labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	tldRe   = regexp.MustCompile(`^([a-z]{2,63}|xn--[a-z0-9-]{1,59})$`)

	// Names that can never get a public certificate (or must not be reachable from outside).
	reservedSuffixes = []string{".localhost", ".local", ".internal", ".invalid", ".test", ".example",
		".lan", ".home", ".corp", ".onion", ".arpa"}
)

var errFormat = errors.New("Enter just the domain name, like example.com (no path, port or spaces).")

// Normalize cleans up and validates a domain typed by the admin. It accepts a
// pasted URL (the scheme and trailing slash are dropped) but nothing else.
func Normalize(in string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(in))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".")
	if s == "" {
		return "", errors.New("Enter a domain name.")
	}
	for _, r := range s {
		if r > 127 {
			return "", errors.New("Use the ASCII (punycode, xn--…) form of internationalised domain names.")
		}
	}
	if strings.ContainsAny(s, " /:?#@\\\t\r\n") {
		return "", errFormat
	}
	if len(s) > 253 {
		return "", errors.New("That domain name is too long.")
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return "", errors.New("A domain needs at least one dot, like example.com.")
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return "", errors.New("Domain names use letters, numbers and hyphens, separated by dots.")
		}
	}
	// rejects IP addresses: their last "label" is a number
	if !tldRe.MatchString(labels[len(labels)-1]) {
		return "", errors.New("That doesn't look like a domain name (IP addresses can't get certificates).")
	}
	for _, suf := range reservedSuffixes {
		if strings.HasSuffix(s, suf) {
			return "", errors.New("Domains ending in " + suf + " can't be used on the public internet.")
		}
	}
	return s, nil
}

// Allowed reports whether host may be served for the configured domain: the
// domain itself, plus www.<domain> (which is redirected to it).
func Allowed(configured, host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if configured == "" || host == "" {
		return false
	}
	return host == configured || (!strings.HasPrefix(configured, "www.") && host == "www."+configured)
}

var instanceID = func() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}()

// InstanceID identifies this running process. The connection check fetches it
// back over the public domain to prove the domain reaches this very site.
func InstanceID() string { return instanceID }

// Service reads the configured domain, cached briefly because Caddy and the
// www redirect ask for it on hot paths.
type Service struct {
	store   *content.Store
	mu      sync.Mutex
	cached  string
	fetched time.Time
}

const cacheTTL = 10 * time.Second

func New(store *content.Store) *Service { return &Service{store: store} }

func (s *Service) Current(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.fetched.IsZero() && time.Since(s.fetched) < cacheTTL {
		return s.cached, nil
	}
	st, err := s.store.Settings(ctx)
	if err != nil {
		return "", err
	}
	s.cached, s.fetched = st.Domain, time.Now()
	return s.cached, nil
}

// Set saves the domain (empty clears it) and drops the cache.
func (s *Service) Set(ctx context.Context, domain string) error {
	err := s.store.SetDomain(ctx, domain)
	s.mu.Lock()
	s.fetched = time.Time{}
	s.mu.Unlock()
	return err
}

// TLSCheck answers Caddy's on-demand TLS "ask" request: 200 if a certificate
// may be issued for ?domain=, 404 otherwise. Without this, anyone could point a
// DNS name at the server and make it request certificates, exhausting rate limits.
// It is served on the internal listener only.
func (s *Service) TLSCheck(w http.ResponseWriter, r *http.Request) {
	configured, err := s.Current(r.Context())
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if !Allowed(configured, r.URL.Query().Get("domain")) {
		http.NotFound(w, r)
		return
	}
	w.Write([]byte("ok"))
}

// RegisterPublic adds the endpoint the connection check fetches.
func RegisterPublic(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/site-id", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(instanceID))
	})
}

// Canonical keeps visitors on https://<domain>. Once a domain is saved it
// redirects www.<domain> to it, and plain-HTTP requests for it (as reported by
// the reverse proxy's X-Forwarded-Proto) to HTTPS. Requests for any other host,
// e.g. the server's IP address during first setup, are served as they are.
func (s *Service) Canonical(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.TrimSuffix(host, ".")
		if host != "" && strings.Contains(host, ".") {
			if configured, err := s.Current(r.Context()); err == nil && Allowed(configured, host) {
				plainHTTP := r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "http"
				if host != configured || plainHTTP {
					http.Redirect(w, r, "https://"+configured+r.URL.RequestURI(), http.StatusMovedPermanently)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
