package content

import (
	"strings"
	"testing"
)

func TestCleanRedirectFrom(t *testing.T) {
	good := map[string]string{
		"/Old-Page":                     "/old-page",
		"/old-page/":                    "/old-page",
		"  /old/deep/path  ":            "/old/deep/path",
		"https://example.com/Old-Page/": "/old-page",
		"HTTP://example.com/a/b":        "/a/b",
		"/caf%C3%A9":                    "/caf%c3%a9",
		"/" + strings.Repeat("a", 290):  "/" + strings.Repeat("a", 290),
		"/administrator":                "/administrator", // only /admin itself and what is under it are reserved
		"/blog/old-post":                "/blog/old-post",
		"/contact-us":                   "/contact-us",
		"/media-kit":                    "/media-kit",
		"/searching":                    "/searching",
		"/theme-park":                   "/theme-park",
	}
	for in, want := range good {
		got, err := CleanRedirectFrom(in)
		if err != nil || got != want {
			t.Errorf("%q → %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"", "   ", "old-page", "//evil.com/x", "/a?b=1", "/a#b", "/a b", "/a\\b", "/a\nb", "/a\x00b", "/", "///", "https://example.com", "https://example.com/",
		"/admin", "/admin/pages", "/ADMIN/x", "/static/x.js", "/theme/default/x", "/media/a.jpg", "/healthz", "/site.css", "/sitemap.xml", "/robots.txt",
		"/setup", "/invite/abc", "/contact", "/newsletter/confirm", "/search", "/rss.xml", "/internal/tls-check",
		"/" + strings.Repeat("a", 300), "mailto:a@b.com", "javascript:alert(1)",
	} {
		if got, err := CleanRedirectFrom(bad); err == nil {
			t.Errorf("%q should be refused, got %q", bad, got)
		}
	}
}

func TestCleanRedirectTo(t *testing.T) {
	for in, want := range map[string]string{
		"/new-page": "/new-page", " /new?x=1#y ": "/new?x=1#y", "https://example.com/a": "https://example.com/a", "http://example.com": "http://example.com", "/": "/",
	} {
		if got, err := CleanRedirectTo(in); err != nil || got != want {
			t.Errorf("%q → %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", " ", "new-page", "//evil.com", "javascript:alert(1)", "data:text/html,x", "mailto:a@b.com", "tel:0821234567", "#top", "/a\\b", "/a\nb", "ftp://x", "/" + strings.Repeat("a", 2001)} {
		if got, err := CleanRedirectTo(bad); err == nil {
			t.Errorf("%q should be refused, got %q", bad, got)
		}
	}
}

func TestNormalizeAndPathOf(t *testing.T) {
	for in, want := range map[string]string{"/About/": "/about", "/": "/", "": "/", "/a//": "/a", "/A/B": "/a/b"} {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"/Page/?a=1#x": "/page", "/page#top": "/page", "/": "/", "https://example.com/page": "", "": ""} {
		if got := pathOf(in); got != want {
			t.Errorf("pathOf(%q) = %q, want %q", in, got, want)
		}
	}
}
