package app_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var reConsentVersion = regexp.MustCompile(`data-consent-version="([0-9a-f]+)"`)

func (c *client) saveConsent(t *testing.T, v url.Values) response {
	t.Helper()
	return c.post("/admin/appearance/consent", v)
}

func TestCookieNotice(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	// off by default: a site that sets no cookies doesn't need to announce any
	home := anon.get("/")
	for _, not := range []string{`id="consent"`, "consent.js", "consent-reopen"} {
		if strings.Contains(home.Body, not) {
			t.Errorf("the notice is off but the page contains %q", not)
		}
	}
	if !strings.Contains(admin.get("/admin/appearance").Body, "Cookie notice") {
		t.Error("the admin has no cookie notice card")
	}

	// a plain notice with the default text
	if r := admin.saveConsent(t, url.Values{"consent_enabled": {"on"}, "consent_mode": {"notice"}}); r.Status != 303 {
		t.Fatalf("enable: %d", r.Status)
	}
	body := anon.get("/").Body
	for _, want := range []string{`id="consent"`, `role="region"`, "only uses the cookies and browser storage it needs", `data-consent="ok">Got it</button>`} {
		if !strings.Contains(body, want) {
			t.Errorf("notice lacks %s", want)
		}
	}
	if strings.Contains(body, "data-consent-reopen") || strings.Contains(body, `data-consent="accepted"`) {
		t.Error("a plain notice has no choice and no reopen link")
	}
	script := regexp.MustCompile(`<script src="(/theme/default/assets/consent\.js\?v=[0-9a-f]+)" defer></script>`).FindStringSubmatch(body)
	if script == nil {
		t.Fatal("the notice's script is not loaded")
	}
	if js := anon.get(script[1]); js.Status != 200 || !strings.Contains(js.Header.Get("Content-Type"), "javascript") || !strings.Contains(js.Body, "wb-consent") {
		t.Errorf("script: %d", js.Status)
	}
	if extra := foreignScripts(body); len(extra) != 0 {
		t.Errorf("unexpected scripts: %v", extra)
	}
	v1 := scrape(reConsentVersion, body)

	// the notice itself must not set any cookie
	for _, p := range []string{"/", "/about", "/blog", "/search?q=x", "/nope"} {
		if r := anon.get(p); r.Header.Get("Set-Cookie") != "" || !strings.Contains(r.Body, `id="consent"`) {
			t.Errorf("%s: cookie %q or no notice", p, r.Header.Get("Set-Cookie"))
		}
	}

	// the owner's own words and link; markup in the text stays text
	r := admin.saveConsent(t, url.Values{
		"consent_enabled": {"on"}, "consent_mode": {"choice"}, "consent_text": {`We value <b>privacy</b> & "honesty"  `},
		"consent_link_label": {"Privacy policy"}, "consent_link_url": {"/privacy"},
	})
	if r.Status != 303 {
		t.Fatalf("save: %d", r.Status)
	}
	body = anon.get("/").Body
	for _, want := range []string{
		`We value &lt;b&gt;privacy&lt;/b&gt; &amp; &#34;honesty&#34;`, `<a href="/privacy">Privacy policy</a>`,
		`data-consent="declined">Decline</button>`, `data-consent="accepted">Accept</button>`, `data-consent-reopen hidden>Cookie settings</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("choice notice lacks %s", want)
		}
	}
	if strings.Contains(body, "Got it") || strings.Contains(body, "<b>privacy</b>") {
		t.Error("wrong buttons, or the owner's markup was not escaped")
	}
	if v2 := scrape(reConsentVersion, body); v2 == "" || v2 == v1 {
		t.Errorf("changing the notice must change its version so visitors are asked again (%q → %q)", v1, v2)
	}
	if form := admin.get("/admin/appearance").Body; !strings.Contains(form, `name="consent_mode" value="choice" checked`) || !strings.Contains(form, `value="/privacy"`) {
		t.Error("the saved settings should be shown in the form")
	}

	// every theme, every kind of page
	for _, theme := range []string{"journal", "midnight", "default"} {
		admin.post("/admin/appearance/theme", url.Values{"theme": {theme}})
		for _, p := range []string{"/", "/blog", "/nope"} {
			if b := anon.get(p).Body; !strings.Contains(b, `id="consent"`) || !strings.Contains(b, "data-consent-reopen") {
				t.Errorf("%s %s: no notice", theme, p)
			}
		}
	}

	// validation: nothing unsafe or oversized is stored, and what was stored is kept
	base := func(over map[string]string) url.Values {
		v := url.Values{"consent_enabled": {"on"}, "consent_mode": {"choice"}, "consent_text": {"Fine"}, "consent_link_label": {"Privacy policy"}, "consent_link_url": {"/privacy"}}
		for k, val := range over {
			v.Set(k, val)
		}
		return v
	}
	for name, over := range map[string]map[string]string{
		"javascript link":    {"consent_link_url": "javascript:alert(1)"},
		"data link":          {"consent_link_url": "data:text/html,x"},
		"protocol-relative":  {"consent_link_url": "//evil.example"},
		"label without link": {"consent_link_url": ""},
		"link without label": {"consent_link_label": ""},
		"long text":          {"consent_text": strings.Repeat("t", 301)},
		"long label":         {"consent_link_label": strings.Repeat("l", 41)},
	} {
		if r := admin.saveConsent(t, base(over)); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if got := e.sql(`SELECT consent->>'link_url' FROM site_settings`); got != "/privacy" {
		t.Errorf("a rejected save changed the stored link: %q", got)
	}
	for _, ok := range []string{"https://example.com/privacy", "/privacy", "#cookies", "mailto:privacy@example.com"} {
		if r := admin.saveConsent(t, base(map[string]string{"consent_link_url": ok})); r.Status != 303 {
			t.Errorf("link %q rejected: %d", ok, r.Status)
		}
	}

	// who may change it
	if r := editor.saveConsent(t, base(nil)); r.Status != 403 {
		t.Errorf("an editor changed the cookie notice: %d", r.Status)
	}
	if r := anon.saveConsent(t, base(nil)); !strings.Contains(r.Location(), "/admin/login") {
		t.Errorf("a visitor should be sent to the login page: %d %q", r.Status, r.Location())
	}

	// switching it off removes everything
	if r := admin.saveConsent(t, url.Values{}); r.Status != 303 {
		t.Fatalf("disable: %d", r.Status)
	}
	if b := anon.get("/").Body; strings.Contains(b, `id="consent"`) || strings.Contains(b, "consent.js") {
		t.Error("the notice should be gone")
	}
	if got := e.sql(`SELECT consent::text FROM site_settings`); got != "{}" {
		t.Errorf("an empty notice stores nothing, got %s", got)
	}
}

// Even if an unsafe address got into the database some other way, it is never put on a page.
func TestCookieNoticeNeverPrintsAnUnsafeLink(t *testing.T) {
	e := newEnv(t)
	e.newAdmin("admin@example.com")
	e.pool.Exec(t.Context(), `UPDATE site_settings SET consent = '{"enabled": true, "text": "Hi", "link_label": "Click", "link_url": "javascript:alert(1)"}'`)
	body := e.client().get("/").Body
	if !strings.Contains(body, `id="consent"`) || strings.Contains(body, "javascript:") || strings.Contains(body, ">Click</a>") {
		t.Error("an unsafe stored link must be dropped")
	}
}
