package app_test

import (
	"net/url"
	"strings"
	"testing"
)

// Where visitors find the dark mode switch is the owner's choice: floating at the side of the page, a link in the
// footer, or nowhere. It is never in the header any more.
func TestDarkModeSwitchPlacement(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	set := func(mode, place string) {
		t.Helper()
		if r := admin.post("/admin/appearance/colormode", url.Values{"color_mode": {mode}, "color_switch": {place}}); r.Status != 303 {
			t.Fatalf("%s/%s: %d", mode, place, r.Status)
		}
	}

	for _, theme := range []string{"default", "journal"} {
		admin.post("/admin/appearance/theme", url.Values{"theme": {theme}})

		set("toggle", "floating")
		body := anon.get("/").Body
		if !strings.Contains(body, `<div class="color-float">`) || strings.Count(body, "data-color-toggle") != 1 {
			t.Errorf("%s floating: one floating switch expected", theme)
		}
		if header := between(body, `<header class="site-header`, `</header>`); strings.Contains(header, "data-color-toggle") {
			t.Errorf("%s: the switch must not be in the header", theme)
		}
		if strings.Contains(body, `class="color-footer"`) {
			t.Errorf("%s floating: no footer link", theme)
		}

		set("toggle", "footer")
		body = anon.get("/").Body
		footer := between(body, `<footer class="site-footer">`, `</footer>`)
		if !strings.Contains(footer, `class="color-footer"`) || !strings.Contains(footer, "data-color-toggle") || !strings.Contains(footer, "data-color-label") {
			t.Errorf("%s footer: the link should be in the footer: %s", theme, footer)
		}
		if strings.Contains(body, `class="color-float"`) || strings.Count(body, "data-color-toggle") != 1 {
			t.Errorf("%s footer: nothing floating", theme)
		}

		set("auto", "off")
		body = anon.get("/").Body
		if strings.Contains(body, "data-color-toggle") {
			t.Errorf("%s off: no switch anywhere", theme)
		}
		if !strings.Contains(body, `data-color-mode="auto"`) || !reColorJS.MatchString(body) {
			t.Errorf("%s off: Automatic still follows the device, so the script stays", theme)
		}
	}

	// dark mode off altogether: no switch, whatever the place
	set("light", "floating")
	if body := anon.get("/").Body; strings.Contains(body, "data-color-toggle") || strings.Contains(body, "color-float") {
		t.Error("dark mode off: no switch")
	}

	// the screen shows the choice, and refuses nonsense
	set("toggle", "footer")
	page := admin.get("/admin/appearance").Body
	if !strings.Contains(page, `<option value="footer" selected>`) {
		t.Error("the screen should show the saved place")
	}
	if r := admin.post("/admin/appearance/colormode", url.Values{"color_mode": {"toggle"}, "color_switch": {"header"}}); r.Status != 400 {
		t.Errorf("a made-up place: %d", r.Status)
	}
	// a form that does not mention the place leaves it where it is
	if r := admin.post("/admin/appearance/colormode", url.Values{"color_mode": {"auto"}}); r.Status != 303 {
		t.Fatalf("mode only: %d", r.Status)
	}
	if !strings.Contains(anon.get("/").Body, `class="color-footer"`) {
		t.Error("the place was lost")
	}
	if r := editor.post("/admin/appearance/colormode", url.Values{"color_mode": {"toggle"}, "color_switch": {"off"}}); r.Status != 403 {
		t.Errorf("an editor changed it: %d", r.Status)
	}
}
