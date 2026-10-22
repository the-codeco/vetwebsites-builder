package app_test

import (
	"net/url"
	"strings"
	"testing"
)

// darkBlock is the dark palette itself (the first `:root[data-theme="dark"] {…}` rule), and the light part before it.
func darkBlock(t *testing.T, css string) (light, dark string) {
	t.Helper()
	i := strings.Index(css, `:root[data-theme="dark"]`)
	if i < 0 {
		t.Fatalf("no dark block:\n%s", css)
	}
	dark = css[i:]
	return css[:i], dark[:strings.Index(dark, "}")+1]
}

func TestDarkPaletteEditor(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()

	form := admin.get("/admin/appearance").Body
	for _, want := range []string{`id="dark-palette"`, `name="d.color-bg"`, `name="d.color-primary-text"`, `placeholder="#12151c"`, `action="/admin/appearance/dark"`} {
		if !strings.Contains(form, want) {
			t.Errorf("the dark palette editor lacks %s", want)
		}
	}
	if strings.Contains(form, "Reset dark colours") || strings.Contains(form, "hard to read") {
		t.Error("nothing to reset or warn about yet")
	}

	// the owner's dark colours go into the dark palette only, and the light palette is untouched
	if r := admin.post("/admin/appearance/dark", url.Values{"d.color-bg": {"#000000"}, "d.color-surface": {"#101010"}, "d.color-primary": {"#FF8800"}}); r.Status != 303 {
		t.Fatalf("save: %d", r.Status)
	}
	light, dark := darkBlock(t, anon.get("/site.css").Body)
	for _, want := range []string{"--color-bg: #000000;", "--color-surface: #101010;", "--color-primary: #ff8800;", "--color-text: #e6e9ef;"} {
		if !strings.Contains(dark, want) {
			t.Errorf("dark palette lacks %s:\n%s", want, dark)
		}
	}
	if !strings.Contains(light, "--color-bg: #ffffff;") || strings.Contains(light, "#000000") || strings.Contains(light, "#ff8800") {
		t.Errorf("the light palette must not change:\n%s", light)
	}
	// the stored form uses a prefix, and a colour equal to the theme's is not stored
	if got := e.sql(`SELECT tokens->'default'->>'dark.color-primary' FROM site_settings`); got != "#ff8800" {
		t.Errorf("stored colour: %q", got)
	}
	form = admin.get("/admin/appearance").Body
	for _, want := range []string{`name="d.color-bg" value="#000000"`, "Reset dark colours"} {
		if !strings.Contains(form, want) {
			t.Errorf("the saved palette should be shown: lacks %s", want)
		}
	}
	if strings.Contains(form, "Reset to theme defaults") {
		t.Error("only dark colours are saved, so there is nothing to reset in the light palette")
	}
	admin.post("/admin/appearance/dark", url.Values{"d.color-bg": {"#12151c"}})
	if got := e.sql(`SELECT tokens::text FROM site_settings`); strings.Contains(got, "dark.") || got != "{}" {
		t.Errorf("a colour equal to the theme's dark colour (and blank ones) is not stored: %s", got)
	}

	// an owner's dark colour beats the light brand colour that dark mode would otherwise reuse
	admin.post("/admin/appearance/tokens", url.Values{"t.color-primary": {"#c0392b"}})
	if _, dark := darkBlock(t, anon.get("/site.css").Body); !strings.Contains(dark, "--color-primary: #c0392b;") {
		t.Errorf("the light brand colour carries into dark mode:\n%s", dark)
	}
	admin.post("/admin/appearance/dark", url.Values{"d.color-primary": {"#00ff00"}, "d.color-text": {"#ffffff"}})
	if _, dark := darkBlock(t, anon.get("/site.css").Body); !strings.Contains(dark, "--color-primary: #00ff00;") || strings.Contains(dark, "#c0392b") {
		t.Errorf("a dark colour must win:\n%s", dark)
	}

	// saving or resetting the light palette keeps the dark one, and the reverse
	admin.post("/admin/appearance/tokens", url.Values{"t.color-bg": {"#fff4e0"}})
	if got := e.sql(`SELECT tokens->'default'->>'dark.color-primary' FROM site_settings`); got != "#00ff00" {
		t.Errorf("saving the light palette lost the dark colours: %q", got)
	}
	if got := e.sql(`SELECT tokens->'default'->>'color-bg' FROM site_settings`); got != "#fff4e0" {
		t.Errorf("light colour: %q", got)
	}
	admin.post("/admin/appearance/dark", url.Values{"d.color-text": {"#eeeeee"}})
	if got := e.sql(`SELECT tokens->'default'->>'color-bg' FROM site_settings`); got != "#fff4e0" {
		t.Errorf("saving the dark palette lost the light colours: %q", got)
	}
	if got := e.sql(`SELECT COALESCE(tokens->'default'->>'dark.color-primary', 'none') FROM site_settings`); got != "none" {
		t.Errorf("the dark form replaces the dark palette: %q", got)
	}
	admin.post("/admin/appearance/dark", url.Values{"d.color-text": {"#eeeeee"}})
	admin.post("/admin/appearance/tokens/reset", nil)
	if got := e.sql(`SELECT tokens->'default'->>'dark.color-text' FROM site_settings`); got != "#eeeeee" {
		t.Errorf("resetting the light palette must keep the dark colours: %q", got)
	}
	if got := e.sql(`SELECT COALESCE(tokens->'default'->>'color-bg', 'none') FROM site_settings`); got != "none" {
		t.Errorf("light palette not reset: %q", got)
	}
	if r := admin.post("/admin/appearance/dark/reset", nil); r.Status != 303 {
		t.Fatalf("reset: %d", r.Status)
	}
	if got := e.sql(`SELECT tokens::text FROM site_settings`); got != "{}" {
		t.Errorf("reset dark colours: %s", got)
	}

	// resetting the dark palette leaves the light one alone
	admin.post("/admin/appearance/tokens", url.Values{"t.color-bg": {"#fff4e0"}})
	admin.post("/admin/appearance/dark", url.Values{"d.color-text": {"#eeeeee"}})
	admin.post("/admin/appearance/dark/reset", nil)
	if got := e.sql(`SELECT tokens->'default'->>'color-bg' FROM site_settings`); got != "#fff4e0" {
		t.Errorf("resetting the dark palette lost the light colours: %q", got)
	}
	if got := e.sql(`SELECT COALESCE(tokens->'default'->>'dark.color-text', 'none') FROM site_settings`); got != "none" {
		t.Errorf("dark colours not reset: %q", got)
	}
}

func TestDarkPaletteValidationAndAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	admin.post("/admin/appearance/dark", url.Values{"d.color-bg": {"#101010"}})
	for name, v := range map[string]string{
		"a name":      "black",
		"short hex":   "#000",
		"css escape":  "#000000;}body{display:none",
		"expression":  "url(javascript:alert(1))",
		"no hash":     "000000",
		"transparent": "rgba(0,0,0,.5)",
	} {
		r := admin.post("/admin/appearance/dark", url.Values{"d.color-bg": {v}})
		if r.Status != 400 || !strings.Contains(r.Body, "isn&#39;t valid") {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if got := e.sql(`SELECT tokens->'default'->>'dark.color-bg' FROM site_settings`); got != "#101010" {
		t.Errorf("a rejected save changed the stored colours: %q", got)
	}
	if css := anon.get("/site.css").Body; strings.Contains(css, "display:none") || strings.Contains(css, "javascript") {
		t.Errorf("hostile value reached the stylesheet:\n%s", css)
	}
	// a hostile value put straight into the database is ignored too
	e.pool.Exec(t.Context(), `UPDATE site_settings SET tokens = '{"default": {"dark.color-bg": "red;}body{display:none", "dark.color-text": "#fafafa"}}'`)
	css := anon.get("/site.css").Body
	if _, dark := darkBlock(t, css); strings.Contains(css, "display:none") || !strings.Contains(dark, "--color-bg: #12151c;") || !strings.Contains(dark, "--color-text: #fafafa;") {
		t.Errorf("an invalid stored colour must fall back to the theme's:\n%s", css)
	}

	// unknown fields are ignored
	if r := admin.post("/admin/appearance/dark", url.Values{"d.not-a-token": {"#123456"}, "d.font-body": {"#123456"}, "d.content-width": {"#123456"}}); r.Status != 303 {
		t.Errorf("unknown fields: %d", r.Status)
	}
	if got := e.sql(`SELECT tokens::text FROM site_settings`); got != "{}" {
		t.Errorf("only colours with a dark palette can be set: %s", got)
	}

	// admin only
	if r := editor.post("/admin/appearance/dark", url.Values{"d.color-bg": {"#000000"}}); r.Status != 403 {
		t.Errorf("an editor changed the dark palette: %d", r.Status)
	}
	if r := editor.post("/admin/appearance/dark/reset", nil); r.Status != 403 {
		t.Errorf("an editor reset the dark palette: %d", r.Status)
	}
	if r := anon.post("/admin/appearance/dark", url.Values{"d.color-bg": {"#000000"}}); !strings.Contains(r.Location(), "/admin/login") {
		t.Errorf("a visitor should be sent to the login page: %d %q", r.Status, r.Location())
	}
	if r := anon.post("/admin/appearance/dark/reset", nil); !strings.Contains(r.Location(), "/admin/login") {
		t.Errorf("a visitor should be sent to the login page: %d %q", r.Status, r.Location())
	}
	if strings.Contains(editor.get("/admin/appearance").Body, "dark-palette") {
		t.Error("editors don't see the appearance screen")
	}
}

func TestDarkPaletteWarnsAboutHardToReadColours(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	// grey text on a grey background
	admin.post("/admin/appearance/dark", url.Values{"d.color-bg": {"#777777"}, "d.color-text": {"#7a7a7a"}})
	page := admin.get("/admin/appearance").Body
	if !strings.Contains(page, "Some dark colours may be hard to read") || !strings.Contains(page, "Text on Background is 1.0:1") {
		t.Errorf("a low-contrast palette should be flagged:\n%s", page[strings.Index(page, `id="dark-palette"`):])
	}
	// it is only advice: the colours were saved
	if got := e.sql(`SELECT tokens->'default'->>'dark.color-bg' FROM site_settings`); got != "#777777" {
		t.Errorf("the palette is saved anyway: %q", got)
	}
	admin.post("/admin/appearance/dark/reset", nil)
	if strings.Contains(admin.get("/admin/appearance").Body, "hard to read") {
		t.Error("the theme's own dark palette is readable")
	}
}

func TestDarkPaletteNotOfferedWhereThereIsNone(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	admin.post("/admin/appearance/theme", url.Values{"theme": {"midnight"}})
	if strings.Contains(admin.get("/admin/appearance").Body, `id="dark-palette"`) {
		t.Error("Midnight is already dark; it has no dark palette to edit")
	}
	if r := admin.post("/admin/appearance/dark", url.Values{"d.color-bg": {"#000000"}}); r.Status != 400 {
		t.Errorf("saving a dark palette for a theme without one: %d", r.Status)
	}
}

func TestDarkPaletteStaysWithItsTheme(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	admin.post("/admin/appearance/dark", url.Values{"d.color-bg": {"#000000"}})
	admin.post("/admin/appearance/theme", url.Values{"theme": {"journal"}})
	if _, dark := darkBlock(t, anon.get("/site.css").Body); strings.Contains(dark, "#000000") || !strings.Contains(dark, "--color-bg: #1c1815;") {
		t.Errorf("the default theme's dark colours must not leak into Journal:\n%s", dark)
	}
	admin.post("/admin/appearance/theme", url.Values{"theme": {"default"}})
	if _, dark := darkBlock(t, anon.get("/site.css").Body); !strings.Contains(dark, "--color-bg: #000000;") {
		t.Errorf("switching back should bring the dark colours back:\n%s", dark)
	}
}
