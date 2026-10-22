package themes

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func richNav() Nav {
	return Nav{
		TopBar: NavSection{Sticky: true, Items: []NavItem{
			{Kind: "link", Label: "Call us", URL: "mailto:hi@example.com"},
			{Kind: "link", Label: "Log in", URL: "/login", NewTab: true},
		}},
		Main: NavSection{Sticky: true, Items: []NavItem{
			{Kind: "link", Label: "Home", URL: "/", Active: true},
			{Kind: "dropdown", Label: "Company", Within: true, Children: []NavLink{
				{Label: "About", URL: "/about", Active: true}, {Label: "Team", URL: "https://example.com/team", NewTab: true},
			}},
			{Kind: "mega", Label: "Products", Columns: []NavColumn{
				{Heading: "Apps", Links: []NavLink{{Label: "Alpha", URL: "/alpha", Description: "The first one"}, {Label: "Beta", URL: "/beta"}}},
				{Heading: "", Links: []NavLink{{Label: "Gamma", URL: "/gamma"}}},
				{Heading: "Tools", Links: []NavLink{{Label: "Delta", URL: "/delta"}}},
			}},
		}},
		Footer: NavSection{Sticky: false, Items: []NavItem{{Kind: "link", Label: "Privacy", URL: "/privacy"}}},
	}
}

func render(t *testing.T, theme string, nav Nav) string {
	t.Helper()
	e, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	th := e.Get(theme)
	var buf bytes.Buffer
	if err := th.RenderPage(&buf, PageData{Site: th.Info("Site", false), Nav: nav, Content: "<p>x</p>", Year: 2026}); err != nil {
		t.Fatalf("%s: %v", theme, err)
	}
	return buf.String()
}

func TestNavigationMarkup(t *testing.T) {
	for _, theme := range []string{"default", "journal", "midnight"} {
		out := render(t, theme, richNav())
		for _, want := range []string{
			// sections and the frozen state
			`<nav class="menu-topbar is-sticky" aria-label="Utility">`,
			`class="menu-main"`, `aria-label="Main"`,
			`<nav class="menu-footer" aria-label="Footer">`,
			`site-header`, ` is-sticky"`,
			// links: current page, new tab safety
			`href="/" aria-current="page"`,
			`target="_blank" rel="noopener noreferrer">Log in</a>`,
			`href="https://example.com/team" target="_blank" rel="noopener noreferrer">Team</a>`,
			// dropdown: a real button controlling a panel
			`<button type="button" class="menu-toggle" aria-expanded="false" aria-controls="menu-panel-1">Company`,
			`<ul class="menu-panel menu-dropdown" id="menu-panel-1">`,
			// mega menu: columns, headings, descriptions
			`<button type="button" class="menu-toggle" aria-expanded="false" aria-controls="menu-panel-2">Products`,
			`class="menu-mega-grid mega-cols-3"`, `<p class="menu-col-heading">Apps</p>`, `<p class="menu-col-heading">Tools</p>`,
			`<span class="menu-desc">The first one</span>`,
			// mobile menu button
			`class="menu-burger" aria-expanded="false" aria-controls="menu-main-list"`, `id="menu-main-list"`,
			// the script every theme loads
			`<script src="/theme/default/assets/menu.js?v=`,
			`is-current`, // the dropdown containing the current page is marked
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q", theme, want)
			}
		}
		if strings.Count(out, `class="menu-col-heading"`) != 2 {
			t.Errorf("%s: a column with no heading must not render an empty heading", theme)
		}
		// the top bar sits above the header, the footer menu after the content
		top, header, main, foot := strings.Index(out, "menu-topbar"), strings.Index(out, "<header"), strings.Index(out, "<main>"), strings.Index(out, `class="menu-footer"`)
		if !(top < header && header < main && main < foot) {
			t.Errorf("%s: order topbar=%d header=%d main=%d footer=%d", theme, top, header, main, foot)
		}
		// every panel id is unique and each toggle points at an element that exists
		ids := regexp.MustCompile(`id="(menu-[a-z0-9-]+)"`).FindAllStringSubmatch(out, -1)
		seen := map[string]bool{}
		for _, m := range ids {
			if seen[m[1]] {
				t.Errorf("%s: duplicate id %s", theme, m[1])
			}
			seen[m[1]] = true
		}
		for _, m := range regexp.MustCompile(`aria-controls="([^"]+)"`).FindAllStringSubmatch(out, -1) {
			if !seen[m[1]] {
				t.Errorf("%s: aria-controls=%q points at nothing", theme, m[1])
			}
		}
	}
}

func TestStickyOnlyWhenFrozen(t *testing.T) {
	nav := richNav()
	nav.TopBar.Sticky, nav.Main.Sticky, nav.Footer.Sticky = false, false, true
	out := render(t, "default", nav)
	if strings.Contains(out, `menu-topbar is-sticky`) || regexp.MustCompile(`site-header is-sticky`).MatchString(out) {
		t.Error("an unfrozen top bar or header got the sticky class")
	}
	if !strings.Contains(out, `menu-footer is-sticky`) {
		t.Error("a frozen footer menu must get the sticky class")
	}
	// the journal masthead freezes too
	nav.Main.Sticky = true
	if out := render(t, "journal", nav); !strings.Contains(out, `site-header masthead is-sticky`) {
		t.Error("journal masthead not frozen")
	}
}

func TestEmptySectionsRenderNothing(t *testing.T) {
	out := render(t, "default", Nav{})
	for _, bad := range []string{"menu-topbar", `class="menu-main"`, "menu-footer", "menu-burger"} {
		if strings.Contains(out, bad) {
			t.Errorf("an empty navigation still rendered %q", bad)
		}
	}
	if !strings.Contains(out, `<header class="site-header">`) || !strings.Contains(out, "<p>x</p>") {
		t.Error("the page itself must still render")
	}
}

func TestNavigationEscapesEverything(t *testing.T) {
	evil := `<script>alert(1)</script>"><img src=x onerror=alert(2)>`
	nav := Nav{
		TopBar: NavSection{Items: []NavItem{{Kind: "link", Label: evil, URL: "javascript:alert(3)"}}},
		Main: NavSection{Items: []NavItem{
			{Kind: "dropdown", Label: evil, Children: []NavLink{{Label: evil, URL: "javascript:alert(4)"}}},
			{Kind: "mega", Label: evil, Columns: []NavColumn{{Heading: evil, Links: []NavLink{{Label: evil, URL: "data:text/html,x", Description: evil}}}}},
		}},
		Footer: NavSection{Items: []NavItem{{Kind: "link", Label: evil, URL: "/x"}}},
	}
	for _, theme := range []string{"default", "journal", "midnight"} {
		out := strings.ToLower(render(t, theme, nav))
		for _, bad := range []string{"<script>alert", "<img src=x", `href="javascript`, `href="data:`} {
			if strings.Contains(out, bad) {
				t.Errorf("%s: unsafe output %q", theme, bad)
			}
		}
	}
}

func TestNavigationHasNoInlineCodeForTheCSP(t *testing.T) {
	out := render(t, "default", richNav())
	if regexp.MustCompile(`(?i)<[a-z][^>]*\son[a-z]+\s*=`).MatchString(out) || regexp.MustCompile(`(?i)<[a-z][^>]*\sstyle\s*=`).MatchString(out) ||
		regexp.MustCompile(`(?i)<script(\s[^>]*)?>[^<]`).MatchString(out) {
		t.Error("navigation markup must not need inline scripts or styles")
	}
}
