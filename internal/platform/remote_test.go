package platform_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/config"
)

// beaconCall sends a visit report the way a published page's script does.
func (c *client) beaconCall(path string, v url.Values, origin string, hdr ...string) response {
	c.t.Helper()
	req, _ := http.NewRequest("POST", c.base+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return c.do(req, hdr)
}

// A published copy has no server, so it counts its visits through the builder.
func TestPublishedSiteCountsVisitsOnTheBuilder(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.BaseURL = "https://builder.example.test" })
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	populate(t, e, ann)
	e.pool.Exec(t.Context(), `UPDATE sites SET public_url = $1 WHERE name = 'Acme Dental'`, siteOrigin+"/shop")
	token := e.scalar(`SELECT public_token FROM sites WHERE name = 'Acme Dental'`)
	files := listFiles(t, e.buildSite(ann, "Acme Dental"))

	// every page but the "not found" page reports its visit
	script := `<script src="/shop/beacon.js" data-endpoint="https://builder.example.test/f/` + token + `/b" defer></script>`
	if _, ok := files["beacon.js"]; !ok {
		t.Fatal("the copy lacks beacon.js")
	}
	for _, page := range []string{"index.html", "about-us.html", "blog.html", "contact-us.html"} {
		if !strings.Contains(string(files[page]), script) {
			t.Errorf("%s does not report visits", page)
		}
	}
	if strings.Contains(string(files["404.html"]), "beacon.js") {
		t.Error("the not-found page is not a visit")
	}

	visitor := e.client()
	base := "/f/" + token + "/b"
	count := func(where string) string {
		t.Helper()
		if err := e.p.FlushSite(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		return e.scalar(`SELECT count(*) FROM site_1.hits WHERE ` + where)
	}

	// a visit is counted, with where it came from; the site's own address is not a referral
	if r := visitor.beaconCall(base, url.Values{"p": {"/about-us"}, "r": {"https://www.google.com/search?q=teeth"}}, siteOrigin); r.Status != 204 {
		t.Fatalf("beacon: %d %s", r.Status, r.Body)
	} else if r.Header.Get("Access-Control-Allow-Origin") != siteOrigin {
		t.Errorf("the site's own page may report: %v", r.Header)
	}
	if got := count(`path = '/about-us' AND referrer = 'google.com'`); got != "1" {
		t.Errorf("the visit should be counted with its referrer: %s", got)
	}
	visitor.beaconCall(base, url.Values{"p": {"/services"}, "r": {siteOrigin + "/shop/about-us"}}, siteOrigin)
	if got := count(`path = '/services' AND referrer = ''`); got != "1" {
		t.Errorf("a click from the site's own page is not a referral: %s", got)
	}
	// a visitor can't make their own site address count as "the site itself" by sending one with the report
	visitor.beaconCall(base, url.Values{"p": {"/blog"}, "r": {"https://spam.example/page"}}, siteOrigin, "X-WB-Site-URL", "https://spam.example")
	if got := count(`path = '/blog' AND referrer = 'spam.example'`); got != "1" {
		t.Errorf("the website's address comes from the platform's records, not the visitor: %s", got)
	}

	// the same rules as on the live site
	before := count(`true`)
	visitor.beaconCall(base, url.Values{"p": {"/about-us"}}, siteOrigin, "DNT", "1")
	visitor.beaconCall(base, url.Values{"p": {"/about-us"}}, siteOrigin, "Sec-GPC", "1")
	visitor.beaconCall(base, url.Values{"p": {"/about-us"}}, siteOrigin, "User-Agent", "Googlebot/2.1 (+http://www.google.com/bot.html)")
	visitor.beaconCall(base, url.Values{"p": {"/admin/pages"}}, siteOrigin)
	visitor.beaconCall(base, url.Values{"p": {"no-slash"}}, siteOrigin)
	visitor.beaconCall(base, url.Values{"p": {"/page?x=1"}}, siteOrigin)
	visitor.beaconCall(base, url.Values{"p": {"/" + strings.Repeat("a", 300)}}, siteOrigin)
	if got := count(`true`); got != before {
		t.Errorf("DNT, GPC, bots, admin paths and malformed paths are not counted: %s -> %s", before, got)
	}
	// the owner looking at the built preview (served from the builder) is not a visitor
	if r := visitor.beaconCall(base, url.Values{"p": {"/about-us"}}, "https://builder.example.test"); r.Status != 204 {
		t.Errorf("preview: %d", r.Status)
	}
	if got := count(`true`); got != before {
		t.Errorf("the preview was counted: %s -> %s", before, got)
	}
	// only the website's own address may report from a browser
	if r := visitor.beaconCall(base, url.Values{"p": {"/about-us"}}, "https://evil.example"); r.Status != 403 {
		t.Errorf("another site: %d", r.Status)
	}
	// and counting can be switched off in the website's analytics settings
	if r := ann.post("/admin/analytics/settings", url.Values{}); r.Status != 303 {
		t.Fatalf("analytics off: %d", r.Status)
	}
	visitor.beaconCall(base, url.Values{"p": {"/services"}}, siteOrigin)
	if got := count(`true`); got != before {
		t.Errorf("analytics is off: %s -> %s", before, got)
	}
}

var reLinkInMail = regexp.MustCompile(`https://builder\.example\.test(/f/[a-z0-9]+/(?:subscribe/confirm|unsubscribe)/[A-Za-z0-9_-]+)`)

// The links in newsletter emails open pages on the builder, because a published copy has none of its own.
func TestNewsletterLinksPointAtTheBuilder(t *testing.T) {
	mail := startFakeMail(t)
	e := newEnv(t, func(c *config.Config) {
		c.BaseURL = "https://builder.example.test"
		c.SMTPHost, c.SMTPPort, c.SMTPFrom = mail.host, mail.port, "noreply@builder.example.test"
	})
	ctx := t.Context()
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	e.pool.Exec(ctx, `UPDATE sites SET public_url = $1 WHERE name = 'Acme Dental'`, siteOrigin)
	token := e.scalar(`SELECT public_token FROM sites WHERE name = 'Acme Dental'`)
	visitor := e.client()
	base := "/f/" + token

	// the newsletter screens no longer ask for a saved domain: the builder's own address is the link base
	if page := ann.get("/admin/newsletters").Body; strings.Contains(page, "Domain page") {
		t.Error("the newsletter screen still asks for a domain")
	}

	// signing up: the confirmation email links to the builder
	_, toks := visitor.formCall("GET", base+"/token", nil, siteOrigin)
	time.Sleep(3200 * time.Millisecond)
	sub := url.Values{"t": {toks["subscribe"].(string)}, "email": {"pat@example.com"}, "from": {"/"}, "block": {"1"}}
	if r, out := visitor.formCall("POST", base+"/subscribe", sub, siteOrigin); r.Status != 200 || out["ok"] != true {
		t.Fatalf("subscribe: %d %s", r.Status, r.Body)
	}
	var confirmMail string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && confirmMail == ""; time.Sleep(50 * time.Millisecond) {
		for _, m := range mail.all() {
			if strings.Contains(m, "pat@example.com") && reLinkInMail.MatchString(m) {
				confirmMail = m
			}
		}
	}
	m := reLinkInMail.FindStringSubmatch(confirmMail)
	if m == nil || !strings.HasPrefix(m[1], base+"/subscribe/confirm/") {
		t.Fatalf("the confirmation email should link to the builder: %q", confirmMail)
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.subscribers WHERE email = 'pat@example.com' AND confirmed_at IS NULL`); got != "1" {
		t.Errorf("a double opt-in signup waits for its confirmation: %s", got)
	}

	// following the link shows a page with a button; nothing changes until it is pressed, and the page is in the
	// website's name with a way back to it (the address comes from the platform, never from the visitor)
	page := visitor.beaconGet(m[1], "X-WB-Site-URL", "javascript:alert(1)")
	for _, want := range []string{"Confirm your subscription", `<button type="submit">`, "Acme Dental", `href="` + siteOrigin + `"`} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("confirm page lacks %q", want)
		}
	}
	if strings.Contains(page.Body, "javascript:") || page.Header.Get("Cache-Control") != "no-store" || page.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("a personal link: no-store, no referrer, and no injected address: %v", page.Header)
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.subscribers WHERE confirmed_at IS NOT NULL`); got != "0" {
		t.Error("just opening the link must not confirm")
	}
	if r := visitor.post(m[1], url.Values{}); r.Status != 200 || !strings.Contains(r.Body, "You&#39;re subscribed") && !strings.Contains(r.Body, "You're subscribed") {
		t.Fatalf("confirm: %d %s", r.Status, firstLine(r.Body))
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.subscribers WHERE email = 'pat@example.com' AND confirmed_at IS NOT NULL`); got != "1" {
		t.Errorf("confirmed: %s", got)
	}
	if r := visitor.beaconGet(base+"/subscribe/confirm/not-a-real-token"); r.Status != 404 || !strings.Contains(r.Body, "isn't valid") {
		t.Errorf("a made-up link: %d", r.Status)
	}

	// a newsletter carries the person's own unsubscribe link, which works for the mail program's one-click too
	e.pool.Exec(ctx, `INSERT INTO site_1.campaigns (subject, body, status, total, base_url, site_title) VALUES ('News', 'Hello', 'sending', 1, $1, 'Acme Dental')`, "https://builder.example.test"+base)
	e.pool.Exec(ctx, `INSERT INTO site_1.deliveries (campaign_id, subscriber_id) SELECT c.id, s.id FROM site_1.campaigns c, site_1.subscribers s`)
	e.p.SweepOnce(ctx)
	var news string
	for _, msg := range mail.all() {
		if strings.Contains(msg, "Subject: News") || strings.Contains(msg, "\nNews\n") || strings.Contains(msg, "Hello") && strings.Contains(msg, "unsubscribe") {
			news = msg
		}
	}
	um := regexp.MustCompile(`https://builder\.example\.test(/f/[a-z0-9]+/unsubscribe/[A-Za-z0-9_-]+)`).FindStringSubmatch(news)
	if um == nil {
		t.Fatalf("the newsletter should carry an unsubscribe link on the builder: %q", news)
	}
	if page := visitor.beaconGet(um[1]); page.Status != 200 || !strings.Contains(page.Body, "Unsubscribe") {
		t.Errorf("unsubscribe page: %d", page.Status)
	}
	req, _ := http.NewRequest("POST", visitor.base+um[1], strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if r := visitor.do(req, nil); r.Status != 200 || !strings.Contains(r.Body, "unsubscribed") {
		t.Errorf("one-click unsubscribe: %d %s", r.Status, firstLine(r.Body))
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.subscribers WHERE email = 'pat@example.com'`); got != "0" {
		t.Errorf("unsubscribing removes the person: %s", got)
	}
}

func (c *client) beaconGet(path string, hdr ...string) response {
	c.t.Helper()
	req, _ := http.NewRequest("GET", c.base+path, nil)
	return c.do(req, hdr)
}
