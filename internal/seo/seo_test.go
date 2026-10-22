package seo

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func TestAbsolute(t *testing.T) {
	for ref, want := range map[string]string{
		"/media/a.jpg":          "https://x.test/media/a.jpg",
		"https://cdn.test/a":    "https://cdn.test/a",
		"http://cdn.test/a":     "http://cdn.test/a",
		"//evil.test/a.jpg":     "", // protocol-relative: refused
		"javascript:alert(1)":   "",
		"data:image/png;base64": "",
		"media/a.jpg":           "", // relative to nothing in particular
		"":                      "",
	} {
		if got := Absolute("https://x.test/", ref); got != want {
			t.Errorf("Absolute(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestShorten(t *testing.T) {
	if got := Shorten("  a   b\n c ", 20); got != "a b c" {
		t.Errorf("whitespace not collapsed: %q", got)
	}
	long := strings.Repeat("word ", 50)
	got := Shorten(long, 40)
	if n := len([]rune(got)); n > 40 || !strings.HasSuffix(got, "…") || strings.Contains(got, "wor…") {
		t.Errorf("cut badly (%d runes): %q", n, got)
	}
	if got := Shorten(strings.Repeat("é", 100), 10); len([]rune(got)) != 10 {
		t.Errorf("multi-byte text cut by bytes, not characters: %q", got)
	}
	if got := Shorten("short", 10); got != "short" {
		t.Errorf("short text changed: %q", got)
	}
}

func TestSitemap(t *testing.T) {
	out, err := Sitemap("https://x.test", []Entry{
		{"/", time.Date(2026, 3, 5, 23, 59, 0, 0, time.UTC)},
		{"/a&b", time.Time{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var set struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			LastMod string `xml:"lastmod"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(out, &set); err != nil {
		t.Fatalf("not valid XML: %v\n%s", err, out)
	}
	if len(set.URLs) != 2 || set.URLs[0].Loc != "https://x.test/" || set.URLs[0].LastMod != "2026-03-05" || set.URLs[1].Loc != "https://x.test/a&b" || set.URLs[1].LastMod != "" {
		t.Errorf("entries: %+v", set.URLs)
	}
	if !strings.Contains(string(out), `xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`) || !strings.Contains(string(out), "a&amp;b") {
		t.Errorf("namespace or escaping missing:\n%s", out)
	}
}

func TestRobots(t *testing.T) {
	open := Robots("https://x.test/", false)
	if !strings.Contains(open, "Disallow: /admin/") || !strings.Contains(open, "Sitemap: https://x.test/sitemap.xml") || strings.Contains(open, "Disallow: /\n") {
		t.Errorf("open site:\n%s", open)
	}
	if hidden := Robots("https://x.test", true); hidden != "User-agent: *\nDisallow: /\n" {
		t.Errorf("hidden site:\n%s", hidden)
	}
}

func codes(is []Issue) string {
	var out []string
	for _, i := range is {
		out = append(out, i.Code)
	}
	return strings.Join(out, ",")
}

func TestAudit(t *testing.T) {
	good := Page{Title: "About our company", SiteTitle: "Acme", Description: strings.Repeat("d", 100), Image: "/media/a.jpg", Published: true}
	if got := Audit(good); len(got) != 0 {
		t.Errorf("a good page has issues: %v", got)
	}
	cases := map[string]struct {
		mod  func(*Page)
		want string
	}{
		"no description":       {func(p *Page) { p.Description = "" }, "desc-missing"},
		"short description":    {func(p *Page) { p.Description = "short" }, "desc-short"},
		"long description":     {func(p *Page) { p.Description = strings.Repeat("d", 161) }, "desc-long"},
		"description at limit": {func(p *Page) { p.Description = strings.Repeat("d", 160) }, ""},
		"long title":           {func(p *Page) { p.Title = strings.Repeat("t", 70) }, "title-long"},
		"override fixes title": {func(p *Page) { p.Title = strings.Repeat("t", 70); p.MetaTitle = "A fine title here" }, ""},
		"short title":          {func(p *Page) { p.Title, p.SiteTitle = "A", "B" }, "title-short"},
		"no image":             {func(p *Page) { p.Image = "" }, "image-missing"},
		"hidden on purpose":    {func(p *Page) { p.Description, p.Image, p.NoIndex = "", "", true }, ""},
		"draft":                {func(p *Page) { p.Description, p.Published = "", false }, ""},
	}
	for name, c := range cases {
		p := good
		c.mod(&p)
		if got := codes(Audit(p)); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
	// the home page has no title of its own: its tab shows just the site name
	home := good
	home.Title = ""
	if got := home.EffectiveTitle(); got != "Acme" {
		t.Errorf("home title: %q", got)
	}
}

func TestAuditAcceptsBrandOnlyHomeTitle(t *testing.T) {
	home := Page{SiteTitle: "Acme", Description: strings.Repeat("d", 100), Image: "/media/a.jpg", Published: true}
	if got := Audit(home); len(got) != 0 {
		t.Errorf("a home page titled with just a short site name is fine, got %v", got)
	}
	home.MetaTitle = "Hi"
	if got := codes(Audit(home)); got != "title-short" {
		t.Errorf("an explicit but tiny title should still be flagged, got %q", got)
	}
}
