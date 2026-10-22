package export

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// htaccess is the web server's instructions for the exported files (Apache, which cPanel hosting uses): clean addresses
// (/about is about.html), the website's own redirects, its own "page not found" page, and the same security headers and
// content policy the live site sends.
func (b *builder) htaccess() []byte {
	var w strings.Builder
	rb := b.base + "/"
	w.WriteString("# Written by the website builder each time the site is published. Changes made here are replaced by the next publish.\n")
	w.WriteString("Options -Indexes -MultiViews\nDirectorySlash Off\nDirectoryIndex index.html\nAddDefaultCharset UTF-8\n")
	w.WriteString("AddType font/woff2 .woff2\nAddType application/json .json\nAddType image/svg+xml .svg\n")
	fmt.Fprintf(&w, "ErrorDocument 404 %s/404.html\n\n", b.base)

	w.WriteString("<IfModule mod_rewrite.c>\nRewriteEngine On\nRewriteBase " + rb + "\n\n")
	if len(b.redirs) > 0 {
		w.WriteString("# redirects set up in the website's Redirects screen, and links that moved\n")
		seen := map[string]bool{}
		rs := append([]Redirect(nil), b.redirs...)
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].From < rs[j].From })
		for _, r := range rs {
			from := strings.Trim(strings.SplitN(r.From, "?", 2)[0], "/")
			to := b.redirectTarget(r.To)
			if from == "" || to == "" || seen[from] || !safeHtaccessText(from) || !safeHtaccessText(to) {
				continue
			}
			seen[from] = true
			code := "302"
			if r.Permanent {
				code = "301"
			}
			fmt.Fprintf(&w, "RewriteRule ^%s/?$ %s [R=%s,L,NC]\n", regexp.QuoteMeta(from), to, code)
		}
		w.WriteString("\n")
	}
	w.WriteString("# an address does not end in a slash\nRewriteRule ^(.+)/$ " + b.base + "/$1 [R=301,L]\n\n")
	w.WriteString("# clean addresses: /about is the file about.html\nRewriteCond %{REQUEST_FILENAME} !-f\nRewriteCond %{REQUEST_FILENAME}.html -f\nRewriteRule ^(.+)$ $1.html [L]\n")
	w.WriteString("</IfModule>\n\n")

	w.WriteString("<IfModule mod_headers.c>\n")
	w.WriteString("Header always set X-Content-Type-Options \"nosniff\"\nHeader always set Referrer-Policy \"strict-origin-when-cross-origin\"\nHeader always set X-Frame-Options \"SAMEORIGIN\"\n")
	if csp := b.contentPolicy(); csp != "" {
		fmt.Fprintf(&w, "Header always set Content-Security-Policy \"%s\"\n", csp)
	}
	w.WriteString("</IfModule>\n\n")

	w.WriteString("<IfModule mod_expires.c>\nExpiresActive On\nExpiresDefault \"access plus 1 hour\"\nExpiresByType text/html \"access plus 5 minutes\"\n")
	w.WriteString("ExpiresByType application/xml \"access plus 1 hour\"\nExpiresByType image/jpeg \"access plus 1 year\"\nExpiresByType image/png \"access plus 1 year\"\n")
	w.WriteString("ExpiresByType image/webp \"access plus 1 year\"\nExpiresByType image/gif \"access plus 1 year\"\nExpiresByType image/svg+xml \"access plus 1 year\"\nExpiresByType font/woff2 \"access plus 1 year\"\n</IfModule>\n\n")
	w.WriteString("<IfModule mod_deflate.c>\nAddOutputFilterByType DEFLATE text/html text/css text/plain text/xml application/xml application/json application/javascript text/javascript image/svg+xml\n</IfModule>\n")
	return []byte(w.String())
}

// redirectTarget is where a redirect leads, as written in the rule: a path on this site gets the folder prefix.
func (b *builder) redirectTarget(to string) string {
	to = strings.TrimSpace(to)
	switch {
	case strings.HasPrefix(to, "//"):
		return ""
	case strings.HasPrefix(to, "/"):
		return b.base + to
	case strings.HasPrefix(to, "https://") || strings.HasPrefix(to, "http://"):
		if u, err := url.Parse(to); err != nil || u.Host == "" {
			return ""
		}
		return to
	}
	return ""
}

// safeHtaccessText is true for text that can sit in a rule without changing what the rule means: no spaces, quotes,
// line breaks or control characters.
func safeHtaccessText(s string) bool {
	for _, r := range s {
		if r <= ' ' || r == 0x7f || r == '"' || r == '\'' || r == '\\' || r == '`' {
			return false
		}
	}
	return true
}

// contentPolicy is the live site's own security policy, with the builder added where the forms and the visit counter talk to it.
func (b *builder) contentPolicy() string {
	csp := b.csp
	if csp == "" {
		csp = "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'"
	}
	builder := ""
	if u, err := url.Parse(b.opt.BuilderURL); err == nil && u.Host != "" && b.opt.BuilderURL != "" {
		builder = u.Scheme + "://" + u.Host
	}
	var out []string
	hasForm, hasConnect := false, false
	for _, d := range strings.Split(csp, ";") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		switch {
		case strings.HasPrefix(d, "form-action"):
			hasForm = true
			if builder != "" {
				d += " " + builder
			}
		case strings.HasPrefix(d, "connect-src"):
			hasConnect = true
			if builder != "" {
				d += " " + builder
			}
		}
		out = append(out, d)
	}
	if !hasForm {
		f := "form-action 'self'"
		if builder != "" {
			f += " " + builder
		}
		out = append(out, f)
	}
	if !hasConnect {
		c := "connect-src 'self'"
		if builder != "" {
			c += " " + builder
		}
		out = append(out, c)
	}
	return strings.ReplaceAll(strings.Join(out, "; "), `"`, "")
}
