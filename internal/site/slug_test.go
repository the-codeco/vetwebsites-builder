package site

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello World":    "hello-world",
		"  About  Us!! ": "about-us",
		"C'est la vie":   "c-est-la-vie",
		"2026 Plans":     "2026-plans",
		"!!!":            "",
		"Café":           "caf",
		"a/b\\c":         "a-b-c",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if long := Slugify(strings.Repeat("word ", 40)); len(long) > 70 || strings.HasSuffix(long, "-") {
		t.Errorf("long slug: %q", long)
	}
}

func TestCheckSlug(t *testing.T) {
	for _, ok := range []string{"home", "about", "my-page-2", "a"} {
		if msg := CheckSlug(ok); msg != "" {
			t.Errorf("%q rejected: %s", ok, msg)
		}
	}
	for _, bad := range []string{"", "About", "a b", "a--b", "-a", "a-", "a/b", "../x", "admin", "media", "blog"} {
		if CheckSlug(bad) == "" {
			t.Errorf("%q accepted", bad)
		}
	}
}
