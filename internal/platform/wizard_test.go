package platform_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// newCustomerUnsetUp creates a customer who has just chosen a password and not yet seen the wizard.
func (e *env) newCustomerUnsetUp(admin *client, email, name string) *client {
	e.t.Helper()
	path := e.createCustomer(admin, email, name)
	c := e.client()
	tok := c.formToken(path)
	if r := c.post(path, url.Values{"csrf": {tok}, "password": {testPassword}, "confirm": {testPassword}}); r.Status != http.StatusSeeOther {
		e.t.Fatalf("choose password: %d", r.Status)
	}
	c.refreshToken()
	return c
}

func step(t *testing.T, c *client, n string, v url.Values) response {
	t.Helper()
	v.Set("step", n)
	v.Set("go", "next")
	return c.post("/admin/setup", v)
}

// A new customer is walked through choosing a look, contact details, menus and home page sections. Nothing is applied
// until the last step; applying again replaces what the wizard made and leaves the customer's own work.
func TestNewWebsiteWizard(t *testing.T) {
	e := newEnv(t)
	admin := e.setupAdmin("admin@example.com")
	ann := e.newCustomerUnsetUp(admin, "ann@a.example", "Acme Dental")

	// a new website opens on the wizard; strangers get the login page
	if r := ann.get("/admin/"); r.Status != 303 || r.Location() != "/admin/setup" {
		t.Fatalf("first visit: %d %s", r.Status, r.Location())
	}
	if r := e.client().get("/admin/setup"); r.Status == 200 {
		t.Error("the wizard is open to strangers")
	}
	page := ann.get("/admin/setup")
	for _, want := range []string{`value="Acme Dental"`, "A veterinary practice", `href="/preview/live/"`} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("step 1 lacks %q", want)
		}
	}
	// a platform administrator looking at the website is not sent into the customer's wizard
	admin.post("/platform/sites/1/act", nil)
	admin.refreshToken()
	if r := admin.get("/admin/"); r.Status != 200 {
		t.Errorf("an administrator working on a website sees the dashboard: %d", r.Status)
	}

	// step 1 and a refused step 2
	if r := step(t, ann, "1", url.Values{"title": {"Acme Dental"}, "business": {"vet"}}); r.Status != 303 || r.Location() != "/admin/setup?step=2" {
		t.Fatalf("step 1: %d %s", r.Status, r.Location())
	}
	if r := step(t, ann, "2", url.Values{"kit": {"fresh"}, "primary": {"blue"}}); r.Status != 400 || !strings.Contains(r.Body, "must look like #1a2b3c") {
		t.Errorf("a bad colour: %d", r.Status)
	}
	look := ann.get("/admin/setup?step=2").Body
	for _, want := range []string{"Clean &amp; clinical", "Night sky", `data-kit`, `data-tokens=`, `name="primary"`, "Dark mode"} {
		if !strings.Contains(look, want) {
			t.Errorf("step 2 lacks %q", want)
		}
	}
	if e.scalar(`SELECT active_theme FROM site_1.site_settings`) != "default" {
		t.Fatal("a refused step changed the website")
	}

	// the other steps
	if r := step(t, ann, "2", url.Values{"kit": {"fresh"}, "primary": {"#aa0000"}, "corners": {"round"}, "header": {"centered"}, "dark": {"auto"}}); r.Status != 303 {
		t.Fatalf("step 2: %d %s", r.Status, firstLine(r.Body))
	}
	if r := step(t, ann, "3", url.Values{"phone": {"021 555 0123"}, "email": {"hello@acme.example"}, "hours": {"Mon to Fri 8 to 5"}, "address": {"1 Main Road"},
		"social_facebook": {"https://facebook.com/acme"}}); r.Status != 303 {
		t.Fatalf("step 3: %d %s", r.Status, firstLine(r.Body))
	}
	if r := step(t, ann, "3", url.Values{"phone": {"1"}}); r.Status != 400 || !strings.Contains(r.Body, "phone number") {
		t.Errorf("a bad phone number: %d", r.Status)
	}
	if r := step(t, ann, "4", url.Values{"top_bar": {"contact"}, "cookie": {"on"}, "newsletter": {"on"}, "announcement": {"Open late on Fridays"}}); r.Status != 303 {
		t.Fatalf("step 4: %d", r.Status)
	}
	if r := step(t, ann, "5", url.Values{"sections": {"hero", "services", "hours", "faq", "contact"}}); r.Status != 303 {
		t.Fatalf("step 5: %d", r.Status)
	}
	// the wizard remembers: the answers are on the pages when it is opened again
	if body := ann.get("/admin/setup?step=3").Body; !strings.Contains(body, `value="021 555 0123"`) || !strings.Contains(body, "https://facebook.com/acme") {
		t.Error("the contact step remembers what was typed")
	}
	if body := ann.get("/admin/setup?step=2").Body; !strings.Contains(body, `value="#aa0000"`) || !regexp.MustCompile(`value="fresh" checked`).MatchString(body) {
		t.Error("the look step remembers the choice")
	}
	// the review says what will happen, and nothing has happened yet
	review := ann.get("/admin/setup?step=6")
	for _, want := range []string{"Apply to my website", "Acme Dental", "Fresh &amp; natural", "centred logo", "5 section block"} {
		if !strings.Contains(review.Body, want) {
			t.Errorf("the review lacks %q", want)
		}
	}
	if e.scalar(`SELECT active_theme FROM site_1.site_settings`) != "default" || e.scalar(`SELECT count(*) FROM site_1.site_elements`) != "0" ||
		e.scalar(`SELECT count(*) FROM site_1.blocks WHERE type = 'faq'`) != "0" {
		t.Fatal("something was applied before the last step")
	}

	// apply
	done := ann.post("/admin/setup/apply", nil)
	if done.Status != 200 || !strings.Contains(done.Body, "Your website is set up") {
		t.Fatalf("apply: %d %s", done.Status, firstLine(done.Body))
	}
	for q, want := range map[string]string{
		`SELECT tokens->'default'->>'color-primary' FROM site_1.site_settings`:                                                              "#aa0000",
		`SELECT tokens->'default'->>'radius' FROM site_1.site_settings`:                                                                     "16px",
		`SELECT color_mode FROM site_1.site_settings`:                                                                                       "auto",
		`SELECT layout->'header'->>'style' FROM site_1.site_settings`:                                                                       "centered",
		`SELECT details->>'phone' FROM site_1.site_settings`:                                                                                "021 555 0123",
		`SELECT consent->>'enabled' FROM site_1.site_settings`:                                                                              "true",
		`SELECT jsonb_array_length(footer->'rows') FROM site_1.site_settings`:                                                               "2",
		`SELECT jsonb_array_length(items) FROM site_1.menus WHERE location = 'topbar'`:                                                      "3",
		`SELECT jsonb_array_length(items) FROM site_1.menus WHERE location = 'main'`:                                                        "2",
		`SELECT count(*) FROM site_1.site_elements WHERE kind = 'bar'`:                                                                      "1",
		`SELECT string_agg(b.type, ',' ORDER BY b.position) FROM site_1.blocks b JOIN site_1.pages p ON p.id = b.page_id WHERE p.slug = 'home'`: "hero,features,details,faq,contactform",
		`SELECT wizard->>'done' FROM site_1.site_settings`:                                                                                  "true",
	} {
		if got := e.scalar(q); got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}
	if e.scalar(`SELECT active_theme FROM site_1.site_settings`) != "default" {
		t.Error("the fresh kit uses the default theme")
	}
	if e.scalar(`SELECT count(*) FROM site_1.blocks b JOIN site_1.pages p ON p.id = b.page_id WHERE p.slug = 'home' AND b.props->>'heading' = 'Welcome to your new website'`) != "0" {
		t.Error("the untouched starter hero should have been replaced")
	}
	// the website shows it
	home := ann.get("/preview/live/").Body
	for _, want := range []string{"Caring for your pets, like family", "Opening hours and emergencies", "Open late on Fridays", "021 555 0123", `id="consent"`, "Quick links"} {
		if !strings.Contains(home, want) {
			t.Errorf("the home page lacks %q", want)
		}
	}
	// and the dashboard is theirs now
	if r := ann.get("/admin/"); r.Status != 200 || strings.Contains(r.Body, "Run the set-up wizard") {
		t.Errorf("dashboard after the wizard: %d", r.Status)
	}

	// the owner's own work, then run it again: the wizard replaces only what it made
	homeID := e.scalar(`SELECT id FROM site_1.pages WHERE slug = 'home'`)
	if r := ann.post("/admin/pages/"+homeID+"/blocks", url.Values{"type": {"richtext"}}); r.Status != 200 {
		t.Fatalf("own block: %d", r.Status)
	}
	own := e.scalar(`SELECT id FROM site_1.blocks WHERE type = 'richtext' AND page_id = ` + homeID)
	if r := step(t, ann, "5", url.Values{"sections": {"hero", "team"}}); r.Status != 303 {
		t.Fatalf("step 5 again: %d", r.Status)
	}
	if r := step(t, ann, "4", url.Values{"top_bar": {"none"}}); r.Status != 303 {
		t.Fatalf("step 4 again: %d", r.Status)
	}
	if body := ann.get("/admin/setup?step=1").Body; !strings.Contains(body, "already run this wizard") {
		t.Error("running it again says what will be replaced")
	}
	if r := ann.post("/admin/setup/apply", nil); r.Status != 200 {
		t.Fatalf("apply again: %d %s", r.Status, firstLine(r.Body))
	}
	if got := e.scalar(`SELECT string_agg(type, ',' ORDER BY position) FROM site_1.blocks WHERE page_id = ` + homeID); got != "richtext,hero,team" {
		t.Errorf("home blocks after a re-run: %s (the owner's text block stays, the wizard's old ones are replaced)", got)
	}
	if e.scalar(`SELECT count(*) FROM site_1.blocks WHERE id = `+own) != "1" {
		t.Error("the owner's own block was removed")
	}
	if e.scalar(`SELECT count(*) FROM site_1.site_elements`) != "0" {
		t.Error("the notice bar should go when its text is cleared... it was made by the wizard")
	}
	if e.scalar(`SELECT count(*) FROM site_1.menus WHERE location = 'topbar' AND jsonb_array_length(items) = 0`) != "1" {
		t.Error("no top bar")
	}
}

func TestWizardSkipAndTheDashboardOffer(t *testing.T) {
	e := newEnv(t)
	admin := e.setupAdmin("admin@example.com")
	ann := e.newCustomerUnsetUp(admin, "ann@a.example", "Acme Dental")
	bob := e.newCustomerUnsetUp(admin, "bob@b.example", "Beta Bakery")
	if r := ann.post("/admin/setup/skip", nil); r.Status != 303 || r.Location() != "/admin/" {
		t.Fatalf("skip: %d %s", r.Status, r.Location())
	}
	dash := ann.get("/admin/")
	if dash.Status != 200 || !strings.Contains(dash.Body, "Run the set-up wizard") {
		t.Errorf("a skipped wizard is offered on the dashboard: %d", dash.Status)
	}
	if r := ann.get("/admin/setup"); r.Status != 200 {
		t.Errorf("the wizard can still be opened: %d", r.Status)
	}
	// another website's wizard is its own
	if r := bob.get("/admin/"); r.Status != 303 || r.Location() != "/admin/setup" {
		t.Errorf("the other website still starts with the wizard: %d %s", r.Status, r.Location())
	}
	// the wizard cannot make a site that no kit fits: skipping then applying with nothing chosen is refused
	if r := ann.post("/admin/setup", url.Values{"step": {"2"}, "kit": {"nonsense"}, "go": {"next"}}); r.Status != 400 {
		t.Errorf("an unknown kit: %d", r.Status)
	}
	// existing websites are not sent to it
	e.markSetUp()
	if r := bob.get("/admin/"); r.Status != 200 {
		t.Errorf("a website marked as set up: %d", r.Status)
	}
}
