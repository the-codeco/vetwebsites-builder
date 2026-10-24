package platform_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/config"
)

// Site-wide elements are in the static copy a customer publishes: each page carries the ones for it with their start and
// end times (the visitor's browser keeps them, the copy has no server), the script comes along, a sign-up popup posts to
// the builder like any other newsletter form, and an element that has already ended is not sent at all.
func TestStaticCopyCarriesSiteWideElements(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.BaseURL = "https://builder.example.test" })
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	newPage(t, ann, "About us", "about-us", "<p>We fix teeth</p>")
	e.pool.Exec(t.Context(), `UPDATE sites SET public_url = 'https://www.acme.example' WHERE name = 'Acme Dental'`)
	now := time.Now().UTC()
	end := now.Add(72 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	post := func(kind string, v url.Values) {
		t.Helper()
		v.Set("kind", kind)
		v.Set("enabled", "on")
		if r := ann.post("/admin/elements", v); r.Status != 303 {
			t.Fatalf("add %s: %d %s", kind, r.Status, firstLine(r.Body))
		}
	}
	post("bar", url.Values{"text": {"Closed for the long weekend"}, "end_at": {end}})
	post("bar", url.Values{"text": {"Old news"}, "end_at": {now.Add(-time.Hour).Format(time.RFC3339)}})
	post("bar", url.Values{"text": {"About page news"}, "pages_mode": {"some"}, "pages": {"/about-us"}})
	post("popup", url.Values{"mode": {"subscribe"}, "heading": {"Join our list"}, "trigger": {"delay"}, "delay": {"6"}})

	files := listFiles(t, e.buildSite(ann, "Acme Dental"))
	home, about := string(files["index.html"]), string(files["about-us.html"])
	if !strings.Contains(home, "Closed for the long weekend") || !strings.Contains(home, `data-el-end="`+end+`"`) {
		t.Error("the home page carries the notice and its end time")
	}
	if strings.Contains(home, "Old news") || strings.Contains(about, "Old news") {
		t.Error("an element that has ended is sent")
	}
	if strings.Contains(home, "About page news") || !strings.Contains(about, "About page news") {
		t.Error("page targeting is applied when the copy is built")
	}
	if _, ok := files["theme/default/assets/elements.js"]; !ok {
		t.Fatal("the copy lacks the elements script")
	}
	if !regexp.MustCompile(`<script src="/theme/default/assets/elements\.js\?v=[0-9a-f]+" defer>`).MatchString(home) {
		t.Error("the page loads the script from the copy itself")
	}
	token := e.scalar(`SELECT public_token FROM sites WHERE name = 'Acme Dental'`)
	form := regexp.MustCompile(`(?s)<div class="el-popup".*?</div>\s*</div>`).FindString(home)
	if !strings.Contains(form, `action="https://builder.example.test/f/`+token+`/subscribe"`) || !strings.Contains(form, `data-wb-form="subscribe"`) {
		t.Errorf("the popup's form should post to the builder:\n%s", form)
	}
	if regexp.MustCompile(`name="t" value="[^"]+"`).MatchString(form) {
		t.Error("a form token was baked into the popup")
	}
	if !strings.Contains(home, "forms.js") {
		t.Error("the page loads the form script when the only form is in a popup")
	}
}
