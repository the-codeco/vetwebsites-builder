package app_test

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func utc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// addElement creates an element with the form the admin screen posts and returns the response.
func (c *client) addElement(kind string, v url.Values) response {
	c.t.Helper()
	v.Set("kind", kind)
	return c.post("/admin/elements", v)
}

func bar(text string, extra ...string) url.Values {
	v := url.Values{"text": {text}, "enabled": {"on"}, "style": {"info"}, "dismissible": {"on"}, "dismiss_days": {"7"}}
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i], extra[i+1])
	}
	return v
}

// Notices, alerts, popups and a contact button are set up by the admin, and shown on the pages they are for, inside the
// times they are set for. The times travel to the visitor's browser, because a published copy has no server to keep them.
func TestSiteWideElements(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	now := time.Now()

	// only an administrator sets them up
	if r := editor.get("/admin/elements"); r.Status == 200 {
		t.Error("an editor opened the elements screen")
	}
	if r := editor.addElement("bar", bar("Hello")); r.Status == 303 {
		t.Error("an editor added an element")
	}
	if body := admin.get("/admin/elements").Body; !strings.Contains(body, "Notice bar") || !strings.Contains(body, "Emergency alert") || !strings.Contains(body, "Contact button") {
		t.Error("the screen offers the kinds")
	}

	// bad input is explained and nothing is saved
	for name, r := range map[string]response{
		"no message":         admin.addElement("bar", bar("")),
		"a long message":     admin.addElement("bar", bar(strings.Repeat("x", 301))),
		"a bad link":         admin.addElement("bar", bar("Hi", "link", "javascript:alert(1)")),
		"end before start":   admin.addElement("bar", bar("Hi", "start_at", utc(now.Add(2*time.Hour)), "end_at", utc(now.Add(time.Hour)))),
		"a bad date":         admin.addElement("bar", bar("Hi", "start_at", "tomorrow")),
		"a bad page":         admin.addElement("bar", bar("Hi", "pages_mode", "some", "pages_extra", "no spaces/here")),
		"unknown kind":       admin.addElement("sparkle", bar("Hi")),
		"popup with nothing": admin.addElement("popup", url.Values{"mode": {"info"}, "trigger": {"delay"}}),
		"call and no way":    admin.addElement("contact_button", url.Values{"action": {"teleport"}}),
		"link and no link":   admin.addElement("contact_button", url.Values{"action": {"link"}}),
	} {
		if r.Status == 303 {
			t.Errorf("%s was accepted", name)
		}
	}
	if e.sql(`SELECT count(*) FROM site_elements`) != "0" {
		t.Fatal("a refused element was stored")
	}

	// a live notice, a notice not started, one that has ended, one that is off, one for a single page
	mk := func(r response) string {
		t.Helper()
		if r.Status != 303 {
			t.Fatalf("add: %d %s", r.Status, firstLine(r.Body))
		}
		return e.sql(`SELECT max(id) FROM site_elements`)
	}
	live := mk(admin.addElement("bar", bar("We are open late on Fridays", "link", "/contact", "link_label", "See hours", "end_at", utc(now.Add(48*time.Hour)))))
	soon := mk(admin.addElement("bar", bar("Winter hours begin", "start_at", utc(now.Add(24*time.Hour)))))
	gone := mk(admin.addElement("bar", bar("Last month's news", "end_at", utc(now.Add(-time.Hour)))))
	off := bar("Switched off")
	off.Del("enabled")
	offID := mk(admin.addElement("bar", off))
	_ = mk(admin.addElement("bar", bar("About page only", "pages_mode", "some", "pages", "/about")))

	home := anon.get("/").Body
	if !strings.Contains(home, "We are open late on Fridays") || !strings.Contains(home, `data-el="`+live+`"`) || !strings.Contains(home, "See hours") {
		t.Error("the live notice is on the home page")
	}
	if !strings.Contains(home, `data-el-end="`+utc(now.Add(48*time.Hour).Truncate(time.Second))+`"`) {
		t.Errorf("the end time travels with the page:\n%s", home)
	}
	if !strings.Contains(home, "Winter hours begin") || !strings.Contains(home, `data-el-start="`+utc(now.Add(24*time.Hour).Truncate(time.Second))+`"`) {
		t.Error("a notice that has not started is sent, with its start time, for the browser to hold back")
	}
	for _, not := range []string{"Last month", "Switched off", "About page only"} {
		if strings.Contains(home, not) {
			t.Errorf("%q should not be on the home page", not)
		}
	}
	if !regexp.MustCompile(`<div class="el el-bar el-info"[^>]* hidden>`).MatchString(home) {
		t.Error("an element is hidden in the markup until its script has checked its times (no flash)")
	}
	if !regexp.MustCompile(`<script src="/theme/default/assets/elements\.js\?v=[0-9a-f]+" defer></script>`).MatchString(home) {
		t.Error("the elements script is loaded from the site itself")
	}
	if got := foreignScripts(home); len(got) > 0 {
		t.Errorf("scripts from elsewhere: %v", got)
	}
	if r := anon.get("/theme/default/assets/elements.js"); r.Status != 200 {
		t.Errorf("the script is served: %d", r.Status)
	}

	// page targeting
	if body := anon.get("/about").Body; !strings.Contains(body, "About page only") || !strings.Contains(body, "We are open late") {
		t.Error("a page-only element shows on its page, with the every-page ones")
	}
	// the owner can see one now, whatever its times; a visitor cannot
	if body := admin.get("/?wb-element=" + gone).Body; !strings.Contains(body, "Last month") || !strings.Contains(body, "data-el-preview") {
		t.Error("the owner can preview an ended element")
	}
	if body := anon.get("/?wb-element=" + gone).Body; strings.Contains(body, "Last month") {
		t.Error("a visitor can preview an ended element")
	}
	_ = soon

	// an alert, a popup and a contact button
	admin.addElement("alert", url.Values{"text": {"Closed Monday for a power cut"}, "enabled": {"on"}})
	admin.addElement("popup", url.Values{"mode": {"subscribe"}, "heading": {"Get our newsletter"}, "text": {"<p>Pet care tips</p><script>x()</script>"}, "trigger": {"scroll"}, "scroll": {"60"}, "button": {"Join"}, "enabled": {"on"}, "dismiss_days": {"30"}})
	admin.addElement("contact_button", url.Values{"action": {"call"}, "enabled": {"on"}, "label": {"Call us"}, "side": {"left"}})
	body := anon.get("/").Body
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "Closed Monday") {
		t.Error("the alert is on the page")
	}
	if !strings.Contains(body, `role="dialog"`) || !strings.Contains(body, "Get our newsletter") || !strings.Contains(body, "Pet care tips") ||
		!strings.Contains(body, `data-el-trigger="scroll"`) || !strings.Contains(body, `data-el-scroll="60"`) {
		t.Error("the popup is on the page with its trigger")
	}
	if strings.Contains(body, "x()") || strings.Contains(body, "<script>x") {
		t.Error("a popup's text is cleaned of scripts")
	}
	if !regexp.MustCompile(`<form class="newsletter-form" method="post" action="/subscribe"[^>]*>\s*<input type="hidden" name="t" value="[^"]+">`).MatchString(body) {
		t.Error("the sign-up popup has the site's newsletter form, so a published copy points it at the builder like any other")
	}
	if strings.Contains(body, `class="el-contact`) {
		t.Error("a call button has no number to call yet, so it is left out")
	}
	if r := admin.post("/admin/details", url.Values{"phone": {"+27 21 555 0123"}}); r.Status != 303 {
		t.Fatalf("site details: %d %s", r.Status, firstLine(r.Body))
	}
	if body := anon.get("/").Body; !strings.Contains(body, `class="el-contact el-side-left" href="tel:&#43;27215550123"`) {
		t.Errorf("the call button uses the number in Site details:\n%s", body)
	}

	// the list shows where each one is in its life; switching, moving and removing work
	list := admin.get("/admin/elements").Body
	for _, want := range []string{"live", "scheduled", "ended", "off"} {
		if !strings.Contains(list, `<span class="badge`) || !strings.Contains(list, ">"+want+"</span>") {
			t.Errorf("the list lacks the %q status", want)
		}
	}
	if r := admin.post("/admin/elements/"+offID+"/toggle", url.Values{"on": {"1"}}); r.Status != 303 || e.sql(`SELECT enabled FROM site_elements WHERE id = `+offID) != "true" {
		t.Error("switch on")
	}
	order := func() string {
		return e.sql(`SELECT string_agg(id::text, ',' ORDER BY position, id) FROM site_elements WHERE id IN (` + live + `,` + soon + `)`)
	}
	if order() != live+","+soon {
		t.Fatalf("order: %s", order())
	}
	admin.post("/admin/elements/"+soon+"/move", url.Values{"dir": {"up"}})
	if order() != soon+","+live {
		t.Errorf("move up: %s", order())
	}
	if r := admin.post("/admin/elements/"+gone+"/delete", nil); r.Status != 303 || e.sql(`SELECT count(*) FROM site_elements WHERE id = `+gone) != "0" {
		t.Error("delete")
	}

	// editing keeps the kind, and changing what it says makes a new version, so visitors who closed it see it again
	before := regexp.MustCompile(`data-el="` + live + `" data-el-kind="bar" data-el-version="([0-9a-f]+)"`).FindStringSubmatch(anon.get("/").Body)
	if before == nil {
		t.Fatal("no version on the live notice")
	}
	admin.post("/admin/elements/"+live, bar("We are open late on Fridays and Saturdays", "kind", "alert"))
	if e.sql(`SELECT kind FROM site_elements WHERE id = `+live) != "bar" {
		t.Error("an element keeps its kind")
	}
	after := regexp.MustCompile(`data-el="` + live + `" data-el-kind="bar" data-el-version="([0-9a-f]+)"`).FindStringSubmatch(anon.get("/").Body)
	if after == nil || after[1] == before[1] {
		t.Error("editing the message changes the version")
	}
	// moving its end date alone is not news to someone who closed it
	admin.post("/admin/elements/"+live, bar("We are open late on Fridays and Saturdays", "end_at", utc(now.Add(96*time.Hour))))
	again := regexp.MustCompile(`data-el="` + live + `" data-el-kind="bar" data-el-version="([0-9a-f]+)"`).FindStringSubmatch(anon.get("/").Body)
	if again == nil || again[1] != after[1] {
		t.Error("changing only the end date keeps the version")
	}

	// the edit screens open, and an unknown element is not found
	if r := admin.get("/admin/elements/" + live); r.Status != 200 || !strings.Contains(r.Body, "We are open late on Fridays and Saturdays") {
		t.Errorf("edit screen: %d", r.Status)
	}
	if r := admin.get("/admin/elements/new?kind=popup"); r.Status != 200 || !strings.Contains(r.Body, "data-richtext") {
		t.Errorf("new popup: %d", r.Status)
	}
	if r := admin.get("/admin/elements/99999"); r.Status != 404 {
		t.Errorf("an unknown element: %d", r.Status)
	}

	// the changes count as unpublished work
	if e.sql(`SELECT n FROM change_counter`) == "0" {
		t.Error("element changes bump the change counter")
	}
}


// A site can only hold so many elements.
func TestSiteWideElementsAreLimited(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	for i := 0; i < 20; i++ {
		if r := admin.addElement("bar", bar("Notice")); r.Status != 303 {
			t.Fatalf("element %d: %d", i, r.Status)
		}
	}
	if r := admin.addElement("bar", bar("One too many")); r.Status != 400 || !strings.Contains(r.Body, "at most 20") {
		t.Errorf("past the limit: %d", r.Status)
	}
}

// They are part of a backup, and a backup from outside is checked like a form.
func TestSiteWideElementsInBackups(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	now := time.Now()
	admin.addElement("bar", bar("Back in a week", "start_at", utc(now.Add(time.Hour)), "end_at", utc(now.Add(24*time.Hour)), "pages_mode", "some", "pages", "/about"))
	admin.addElement("popup", url.Values{"mode": {"subscribe"}, "heading": {"Join us"}, "trigger": {"exit"}, "delay": {"4"}, "enabled": {"on"}})
	want := e.sql(`SELECT string_agg(kind||'|'||name||'|'||enabled||'|'||config::text||'|'||pages::text||'|'||COALESCE(start_at::text,'')||'|'||COALESCE(end_at::text,''), E'\n' ORDER BY position, id) FROM site_elements`)
	if strings.Count(want, "\n") != 1 {
		t.Fatalf("setup: %s", want)
	}
	raw := []byte(admin.downloadBackup(false).Body)

	e.pool.Exec(context.Background(), `DELETE FROM site_elements`)
	admin.addElement("alert", url.Values{"text": {"A newer thing"}, "enabled": {"on"}})
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, firstLine(r.Body))
	}
	if got := e.sql(`SELECT string_agg(kind||'|'||name||'|'||enabled||'|'||config::text||'|'||pages::text||'|'||COALESCE(start_at::text,'')||'|'||COALESCE(end_at::text,''), E'\n' ORDER BY position, id) FROM site_elements`); got != want {
		t.Errorf("restored elements differ:\n got: %s\nwant: %s", got, want)
	}
}
