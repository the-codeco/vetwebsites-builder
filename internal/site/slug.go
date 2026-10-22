package site

import (
	"regexp"
	"strings"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Reserved slugs are served by other routes and can't be used for pages.
var Reserved = map[string]bool{
	"admin": true, "setup": true, "media": true, "static": true, "theme": true,
	"healthz": true, "invite": true, "internal": true, "rss": true, "blog": true,
	"posts": true, "search": true, "category": true, "login": true, "logout": true, "api": true,
}

// Slugify turns a title into a slug candidate (ASCII letters and digits only).
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 70 {
		out = strings.TrimRight(out[:70], "-")
	}
	return out
}

// CheckSlugFormat validates only the shape of a slug. Posts and categories live
// under /blog/, so they don't need the page-level reserved words.
func CheckSlugFormat(slug string) string {
	switch {
	case slug == "":
		return "Slug is required."
	case len(slug) > 80:
		return "Slug must be at most 80 characters."
	case !slugRe.MatchString(slug):
		return "Slug may only contain lowercase letters, numbers and single hyphens."
	}
	return ""
}

// CheckSlug returns a human-readable problem with a page slug, or "" if it's usable.
func CheckSlug(slug string) string {
	if msg := CheckSlugFormat(slug); msg != "" {
		return msg
	}
	if Reserved[slug] {
		return "That slug is reserved."
	}
	return ""
}
