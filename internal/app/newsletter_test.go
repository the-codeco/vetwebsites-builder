package app_test

import (
	"context"
	"encoding/csv"
	"net"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/config"
)

var (
	reNLToken     = regexp.MustCompile(`name="t" value="([^"]+)"`)
	reNLBlock     = regexp.MustCompile(`name="block" value="(\d+)"`)
	reConfirmLink = regexp.MustCompile(`/subscribe/confirm/([A-Za-z0-9_-]{43})`)
	reUnsubLink   = regexp.MustCompile(`/unsubscribe/([A-Za-z0-9_-]{43})`)
)

// signupPage creates a published page holding a newsletter block and returns its path.
func signupPage(t *testing.T, admin *client) string {
	t.Helper()
	id := newPage(t, admin, "Join")
	if r := admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"newsletter"}}); r.Status != 200 {
		t.Fatalf("add block: %d", r.Status)
	}
	savePage(t, admin, id, url.Values{"title": {"Join"}, "slug": {"join"}})
	return "/join"
}

func TestNewsletterSignupWithoutEmailSetup(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	page := signupPage(t, admin)

	body := anon.get(page).Body
	tok, blockID := scrape(reNLToken, body), scrape(reNLBlock, body)
	for _, want := range []string{`<form class="newsletter-form"`, `action="/subscribe"`, `type="email"`, `name="website"`, "Stay in the loop", ">Subscribe</button>"} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %s", want)
		}
	}
	if tok == "" || blockID == "" {
		t.Fatal("no token or block id")
	}
	if extra := foreignScripts(body); len(extra) != 0 {
		t.Errorf("scripts on the page: %v", extra)
	}
	form := func(over map[string]string) url.Values {
		v := url.Values{"t": {tok}, "from": {"/join"}, "block": {blockID}, "email": {"Reader@Example.com"}}
		for k, val := range over {
			v.Set(k, val)
		}
		return v
	}
	count := func() string { return e.sql(`SELECT count(*) FROM subscribers`) }

	if r := anon.post("/subscribe", form(nil)); r.Status != 400 || !strings.Contains(r.Body, "too quickly") {
		t.Errorf("instant post: %d", r.Status)
	}
	time.Sleep(3200 * time.Millisecond)

	if r := anon.post("/subscribe", form(nil), "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"); r.Status != 403 {
		t.Errorf("cross-site post: %d", r.Status)
	}
	if r := anon.post("/subscribe", form(map[string]string{"website": "x"})); r.Status != 303 || count() != "0" {
		t.Errorf("honeypot: %d, %s rows", r.Status, count())
	}
	for name, over := range map[string]map[string]string{
		"forged token": {"t": "1.abc"}, "no email": {"email": ""}, "bad email": {"email": "nope"},
		"two addresses": {"email": "a@b.co,c@d.co"}, "header injection": {"email": "a@b.co\r\nBcc: x@y.co"},
	} {
		if r := anon.post("/subscribe", form(over)); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if count() != "0" {
		t.Fatalf("invalid signups stored: %s", count())
	}

	// no email or domain configured: the address counts at once
	r := anon.post("/subscribe", form(nil))
	if r.Status != 303 || r.Location() != "/join?subscribed="+blockID+"#newsletter-"+blockID {
		t.Fatalf("subscribe: %d %q", r.Status, r.Location())
	}
	if e.sql(`SELECT email FROM subscribers`) != "reader@example.com" || e.sql(`SELECT confirmed_at IS NOT NULL FROM subscribers`) != "true" {
		t.Error("the address should be stored in lower case and confirmed")
	}
	thanks := anon.get("/join?subscribed=" + blockID).Body
	if !strings.Contains(thanks, `class="form-notice"`) || strings.Contains(thanks, "confirm your address") || strings.Contains(thanks, `<form class="newsletter-form"`) {
		t.Error("the thank-you should replace the form, without asking to confirm")
	}
	anon.post("/subscribe", form(map[string]string{"email": "READER@example.com"}))
	if count() != "1" {
		t.Errorf("the same address twice made %s rows", count())
	}

	// redirects stay on the site
	for _, evil := range []string{"//evil.example", "https://evil.example", "javascript:alert(1)"} {
		if loc := anon.post("/subscribe", form(map[string]string{"from": evil, "website": "x"})).Location(); !strings.HasPrefix(loc, "/?subscribed=") {
			t.Errorf("from=%q went to %q", evil, loc)
		}
	}

	// admin: list, export, removal
	ctx := context.Background()
	e.pool.Exec(ctx, `INSERT INTO subscribers (email, token_hash, confirmed_at) VALUES ('+plus@example.com', 'h1', now()), ('waiting@example.com', 'h2', NULL)`)
	list := admin.get("/admin/subscribers")
	for _, want := range []string{"reader@example.com", "waiting@example.com", `badge published">Confirmed`, `badge draft">Waiting`, "/admin/subscribers.csv"} {
		if !strings.Contains(list.Body, want) {
			t.Errorf("list lacks %s", want)
		}
	}
	exp := admin.get("/admin/subscribers.csv")
	if !strings.HasPrefix(exp.Header.Get("Content-Type"), "text/csv") || !strings.Contains(exp.Header.Get("Content-Disposition"), "attachment") {
		t.Errorf("export headers: %v", exp.Header)
	}
	rows, err := csv.NewReader(strings.NewReader(exp.Body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, row := range rows[1:] {
		got[row[0]] = true
	}
	if rows[0][0] != "email" || !got["reader@example.com"] || got["waiting@example.com"] {
		t.Errorf("export should list confirmed addresses only: %v", rows)
	}
	if !got["'+plus@example.com"] || got["+plus@example.com"] {
		t.Errorf("a spreadsheet formula could run from the export: %v", rows)
	}

	if r := editor.get("/admin/subscribers"); r.Status != 403 {
		t.Errorf("editor opened the list: %d", r.Status)
	}
	if r := editor.get("/admin/subscribers.csv"); r.Status != 403 {
		t.Errorf("editor exported the list: %d", r.Status)
	}
	id := e.sql(`SELECT id FROM subscribers WHERE email = 'waiting@example.com'`)
	if r := editor.post("/admin/subscribers/"+id+"/delete", nil); r.Status != 403 {
		t.Errorf("editor removed a subscriber: %d", r.Status)
	}
	if r := anon.get("/admin/subscribers.csv"); r.Status == 200 {
		t.Error("the export is open to visitors")
	}
	if r := admin.post("/admin/subscribers/"+id+"/delete", nil); r.Status != 303 || e.sql(`SELECT count(*) FROM subscribers WHERE email = 'waiting@example.com'`) != "0" {
		t.Errorf("remove: %d", r.Status)
	}
}

func TestNewsletterDoubleOptIn(t *testing.T) {
	smtp := startFakeSMTP(t)
	host, port, _ := net.SplitHostPort(smtp.addr)
	e := newEnv(t, func(c *config.Config) {
		c.SMTPHost, c.SMTPPort, c.SMTPFrom = host, port, "Site <noreply@example.com>"
	})
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	page := signupPage(t, admin)

	tok := scrape(reNLToken, anon.get(page).Body)
	blockID := scrape(reNLBlock, anon.get(page).Body)
	send := func(email string) response {
		return anon.post("/subscribe", url.Values{"t": {tok}, "from": {"/join"}, "block": {blockID}, "email": {email}})
	}
	time.Sleep(3200 * time.Millisecond)

	// email works but there's no saved domain: a confirmation link would have to trust the Host
	// header of whoever asked, so the address is simply counted
	send("first@example.com")
	if e.sql(`SELECT confirmed_at IS NOT NULL FROM subscribers WHERE email = 'first@example.com'`) != "true" {
		t.Error("without a saved domain, signups should count at once")
	}
	select {
	case m := <-smtp.got:
		t.Errorf("an email was sent without a domain:\n%s", m)
	case <-time.After(300 * time.Millisecond):
	}
	if !strings.Contains(admin.get("/admin/subscribers").Body, "custom domain") {
		t.Error("the admin should explain what is missing")
	}

	// with a domain: confirmation by email
	e.pool.Exec(context.Background(), `UPDATE site_settings SET domain = 'example.com'`)
	if body := anon.get(page).Body; !strings.Contains(body, "Subscribe") {
		t.Fatal("form missing")
	}
	if r := send("second@example.com"); r.Status != 303 {
		t.Fatalf("subscribe: %d", r.Status)
	}
	if e.sql(`SELECT confirmed_at IS NULL FROM subscribers WHERE email = 'second@example.com'`) != "true" {
		t.Fatal("a new address must wait for confirmation")
	}
	if !strings.Contains(anon.get("/join?subscribed="+blockID).Body, "confirm your address") {
		t.Error("the thank-you should ask the visitor to check their inbox")
	}
	mail := smtp.next(t)
	for _, want := range []string{"RCPT TO:<second@example.com>", "To: second@example.com", "https://example.com/subscribe/confirm/", "https://example.com/unsubscribe/"} {
		if !strings.Contains(mail, want) {
			t.Errorf("email lacks %q:\n%s", want, mail)
		}
	}
	confirmTok, unsubTok := scrape(reConfirmLink, mail), scrape(reUnsubLink, mail)
	if confirmTok == "" || confirmTok != unsubTok {
		t.Fatalf("links missing: %q %q", confirmTok, unsubTok)
	}
	if strings.Contains(e.sql(`SELECT token_hash FROM subscribers WHERE email = 'second@example.com'`), confirmTok) {
		t.Error("the raw token must not be stored")
	}

	// the link shows a button; opening it (as a mail scanner might) confirms nothing
	page1 := anon.get("/subscribe/confirm/" + confirmTok)
	if page1.Status != 200 || !strings.Contains(page1.Body, `method="post" action="/subscribe/confirm/`+confirmTok+`"`) || !strings.Contains(page1.Body, `content="noindex, nofollow"`) {
		t.Errorf("confirm page: %d", page1.Status)
	}
	if e.sql(`SELECT confirmed_at IS NULL FROM subscribers WHERE email = 'second@example.com'`) != "true" {
		t.Fatal("opening the link must not confirm")
	}
	if r := anon.post("/subscribe/confirm/"+confirmTok, nil); r.Status != 200 || !strings.Contains(r.Body, "You&#39;re subscribed") && !strings.Contains(r.Body, "You're subscribed") {
		t.Errorf("confirm: %d", r.Status)
	}
	if e.sql(`SELECT confirmed_at IS NOT NULL FROM subscribers WHERE email = 'second@example.com'`) != "true" {
		t.Error("the button should confirm")
	}

	// signing up a confirmed address again sends nothing and changes nothing
	send("second@example.com")
	select {
	case m := <-smtp.got:
		t.Errorf("a confirmed address was emailed again:\n%s", m)
	case <-time.After(300 * time.Millisecond):
	}

	// bad links
	for _, bad := range []string{"short", strings.Repeat("a", 43), strings.Repeat("!", 43)} {
		if r := anon.get("/subscribe/confirm/" + bad); r.Status != 404 {
			t.Errorf("confirm %q: %d", bad, r.Status)
		}
		if r := anon.post("/unsubscribe/"+bad, nil); r.Status != 404 {
			t.Errorf("unsubscribe %q: %d", bad, r.Status)
		}
	}

	// unsubscribing also needs the button, then removes the address
	if r := anon.get("/unsubscribe/" + unsubTok); r.Status != 200 || e.sql(`SELECT count(*) FROM subscribers WHERE email = 'second@example.com'`) != "1" {
		t.Errorf("unsubscribe page: %d", r.Status)
	}
	if r := anon.post("/unsubscribe/"+unsubTok, nil); r.Status != 200 || e.sql(`SELECT count(*) FROM subscribers WHERE email = 'second@example.com'`) != "0" {
		t.Errorf("unsubscribe: %d", r.Status)
	}
	if r := anon.get("/unsubscribe/" + unsubTok); r.Status != 404 {
		t.Errorf("the link works once: %d", r.Status)
	}

	// pending signups that are never confirmed are cleaned up
	e.pool.Exec(context.Background(), `INSERT INTO subscribers (email, token_hash, created_at) VALUES ('old@example.com', 'x1', now() - interval '15 days'), ('new@example.com', 'x2', now() - interval '2 days')`)
	e.app.Housekeeping(context.Background())
	if got := e.sql(`SELECT string_agg(email, ',' ORDER BY email) FROM subscribers WHERE confirmed_at IS NULL`); got != "new@example.com" {
		t.Errorf("stale pending signups: %q", got)
	}
}

func TestNewsletterRateLimit(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	page := signupPage(t, admin)
	tok := scrape(reNLToken, anon.get(page).Body)
	time.Sleep(3200 * time.Millisecond)
	for i := 1; i <= 6; i++ {
		r := anon.post("/subscribe", url.Values{"t": {tok}, "from": {"/join"}, "block": {"1"}, "email": {"p" + string(rune('a'+i)) + "@example.com"}})
		want := 303
		if i == 6 {
			want = 429
		}
		if r.Status != want {
			t.Errorf("signup %d: %d, want %d", i, r.Status, want)
		}
	}
}
