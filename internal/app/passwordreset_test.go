package app_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	reResetLink  = regexp.MustCompile(`https://example\.com/reset/([A-Za-z0-9_-]{43})`)
	reResetAdmin = regexp.MustCompile(`<code>(http[^<]*/reset/([A-Za-z0-9_-]{43}))</code>`)
	newPassword  = "a-brand-new-passphrase-42"
)

// forgot asks for a reset link the way the public form does.
func forgot(c *client, email string) response {
	tok := c.formToken("/admin/forgot")
	return c.post("/admin/forgot", url.Values{"csrf": {tok}, "email": {email}})
}

func resetWith(c *client, token, pw, confirm string) response {
	tok := c.formToken("/admin/login")
	return c.post("/reset/"+token, url.Values{"csrf": {tok}, "password": {pw}, "confirm": {confirm}})
}

func TestForgottenPasswordIsResetByEmail(t *testing.T) {
	e, admin, smtp := mailEnv(t, true)
	editor := e.newUser(admin, "ed@example.com", "editor")
	_ = editor
	stranger := e.client()

	if body := stranger.get("/admin/login").Body; !strings.Contains(body, `href="/admin/forgot"`) {
		t.Error("the login page links to the reset form")
	}
	if r := stranger.get("/admin/forgot"); r.Status != 200 || !strings.Contains(r.Body, `name="email"`) {
		t.Errorf("the form: %d", r.Status)
	}

	r := forgot(stranger, "ed@example.com")
	if r.Status != 200 || !strings.Contains(r.Body, "link to choose a new password is on its way") {
		t.Fatalf("ask: %d %s", r.Status, firstLine(r.Body))
	}
	mail := smtp.next(t)
	m := reResetLink.FindStringSubmatch(mail)
	if m == nil || rcptOf(mail) != "ed@example.com" || !strings.Contains(mail, "Choose a new password") || !strings.Contains(mail, "within the hour") {
		t.Fatalf("the email:\n%s", mail)
	}
	token := m[1]
	if got := e.sql(`SELECT count(*) FROM password_resets`); got != "1" {
		t.Errorf("one link stored: %s", got)
	}
	if got := e.sql(`SELECT count(*) FROM password_resets WHERE encode(token_hash, 'escape') LIKE '%` + token[:10] + `%'`); got != "0" {
		t.Error("the token itself must not be stored, only its hash")
	}

	// the same answer for an address that has no account, and no email
	r2 := forgot(stranger, "nobody@example.com")
	if r2.Status != 200 || !strings.Contains(r2.Body, "link to choose a new password is on its way") {
		t.Errorf("unknown address: %d", r2.Status)
	}
	if extra := smtp.drain(); len(extra) != 0 {
		t.Errorf("no email for an address with no account: %d", len(extra))
	}
	if strings.Contains(r2.Body, "nobody@example.com") {
		t.Error("the answer does not repeat what was typed")
	}

	// the link
	form := stranger.get("/reset/" + token)
	if form.Status != 200 || !strings.Contains(form.Body, "ed@example.com") || !strings.Contains(form.Body, `name="confirm"`) {
		t.Errorf("reset form: %d", form.Status)
	}
	for name, c := range map[string]struct{ pw, confirm string }{"too short": {"short", "short"}, "mismatch": {newPassword, newPassword + "x"}, "empty": {"", ""}} {
		if r := resetWith(stranger, token, c.pw, c.confirm); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if r := stranger.post("/reset/"+token, url.Values{"password": {newPassword}, "confirm": {newPassword}}); r.Status != 400 {
		t.Errorf("without the session token: %d", r.Status)
	}
	if r := resetWith(stranger, token, newPassword, newPassword); r.Status != 303 || r.Header.Get("Location") != "/admin/" {
		t.Fatalf("reset: %d %s", r.Status, firstLine(r.Body))
	}
	stranger.refreshToken()
	if r := stranger.get("/admin/"); r.Status != 200 {
		t.Errorf("the reset logs the person in: %d", r.Status)
	}
	notice := smtp.next(t)
	if rcptOf(notice) != "ed@example.com" || !strings.Contains(notice, "password was changed") {
		t.Errorf("a change notice is sent:\n%s", notice)
	}
	// it works once
	if r := stranger.get("/reset/" + token); r.Status != 404 || !strings.Contains(r.Body, "expired") {
		t.Errorf("the link is spent: %d", r.Status)
	}
	if r := resetWith(e.client(), token, "another-one-entirely-1", "another-one-entirely-1"); r.Status != 404 {
		t.Errorf("reusing it: %d", r.Status)
	}
	// the new password works, the old does not
	login := e.client()
	if r := login.login("ed@example.com", newPassword); r.Status != 303 {
		t.Errorf("the new password: %d", r.Status)
	}
	if r := e.client().login("ed@example.com", "the-old-one-1234"); r.Status == 303 {
		t.Error("the old password is gone")
	}
}

func TestResetEndsOtherSessionsAndExpires(t *testing.T) {
	e, admin, smtp := mailEnv(t, true)
	other := e.newUser(admin, "ed@example.com", "editor")
	if r := other.get("/admin/"); r.Status != 200 {
		t.Fatalf("the editor is logged in: %d", r.Status)
	}
	forgot(e.client(), "ed@example.com")
	token := reResetLink.FindStringSubmatch(smtp.next(t))[1]
	resetWith(e.client(), token, newPassword, newPassword)
	if r := other.get("/admin/"); r.Status == 200 {
		t.Error("a session from before the reset must end")
	}
	smtp.drain()

	// a newer request replaces the earlier link
	forgot(e.client(), "ed@example.com")
	first := reResetLink.FindStringSubmatch(smtp.next(t))[1]
	forgot(e.client(), "ed@example.com")
	second := reResetLink.FindStringSubmatch(smtp.next(t))[1]
	if first == second {
		t.Fatal("each request makes a new link")
	}
	if r := e.client().get("/reset/" + first); r.Status != 404 {
		t.Errorf("the earlier link stops working: %d", r.Status)
	}
	if r := e.client().get("/reset/" + second); r.Status != 200 {
		t.Errorf("the newest works: %d", r.Status)
	}
	// it expires after an hour
	e.pool.Exec(t.Context(), `UPDATE password_resets SET expires_at = now() - interval '1 minute'`)
	if r := e.client().get("/reset/" + second); r.Status != 404 {
		t.Errorf("expired: %d", r.Status)
	}
	if r := resetWith(e.client(), second, newPassword+"2", newPassword+"2"); r.Status != 404 {
		t.Errorf("expired, posting: %d", r.Status)
	}
	// junk tokens
	for _, tok := range []string{"nope", strings.Repeat("a", 43), "..", "%00"} {
		if r := e.client().get("/reset/" + tok); r.Status != 404 && r.Status != 400 && r.Status != 307 {
			t.Errorf("token %q: %d", tok, r.Status)
		}
	}
	// deleting the user removes the link
	e.pool.Exec(t.Context(), `DELETE FROM users WHERE email = 'ed@example.com'`)
	if got := e.sql(`SELECT count(*) FROM password_resets`); got != "0" {
		t.Errorf("links go with the user: %s", got)
	}
}

func TestForgotPasswordIsRateLimitedAndSafe(t *testing.T) {
	e, admin, smtp := mailEnv(t, true)
	e.newUser(admin, "ed@example.com", "editor")
	c := e.client()
	// no session token, and junk input: answered the same, and nothing is sent
	if r := e.client().post("/admin/forgot", url.Values{"email": {"ed@example.com"}}); r.Status != 400 {
		t.Errorf("without the session token: %d", r.Status)
	}
	for _, bad := range []string{"", "not an email", "a@b.c\r\nBcc: x@y.z"} {
		if r := forgot(e.client(), bad); r.Status != 200 {
			t.Errorf("%q: %d", bad, r.Status)
		}
	}
	if extra := smtp.drain(); len(extra) != 0 {
		t.Errorf("junk addresses send nothing: %d", len(extra))
	}
	var blocked bool
	for i := 0; i < 8; i++ {
		if r := forgot(c, "ed@example.com"); r.Status == 429 {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Error("a visitor can only ask a few times an hour")
	}
	if n := len(smtp.drain()); n > 5 {
		t.Errorf("at most 5 emails: %d", n)
	}
	// the visitor's limit holds whatever is asked, unknown addresses included, so it cannot be used to find out
	// which addresses have accounts
	statuses := map[int]int{}
	for i := 0; i < 8; i++ {
		statuses[forgot(e.client(), "ghost"+string(rune('a'+i))+"@example.com").Status]++
	}
	if statuses[429] == 0 {
		t.Errorf("limited by visitor even for addresses with no account: %v", statuses)
	}
	// a logged-in person has no use for the form
	if r := admin.get("/admin/forgot"); r.Status != 303 {
		t.Errorf("logged in: %d", r.Status)
	}
}

func TestResetLinksDoNotFollowTheHostHeader(t *testing.T) {
	e, admin, smtp := mailEnv(t, true)
	e.newUser(admin, "ed@example.com", "editor")
	c := e.client()
	tok := c.formToken("/admin/forgot")
	if r := c.post("/admin/forgot", url.Values{"csrf": {tok}, "email": {"ed@example.com"}}, "Host", "evil.example", "X-Forwarded-Host", "evil.example", "Cookie", "wb_csrf="+tok); r.Status != 200 {
		t.Fatalf("the request: %d expired=%v\n%s", r.Status, strings.Contains(r.Body, "Session expired"), regexp.MustCompile(`(?s)<main.*</main>`).FindString(r.Body))
	}
	mail := smtp.next(t)
	if strings.Contains(mail, "evil.example") || !reResetLink.MatchString(mail) {
		t.Errorf("the link comes from the saved domain, not the request:\n%s", mail)
	}
}

func TestNoResetEmailWithoutADomainOrMail(t *testing.T) {
	// email works, but no domain is saved: no link can be built safely, so nothing is sent
	e, admin, smtp := mailEnv(t, false)
	e.newUser(admin, "ed@example.com", "editor")
	r := forgot(e.client(), "ed@example.com")
	if r.Status != 200 || !strings.Contains(r.Body, "ask another admin") {
		t.Errorf("same answer: %d", r.Status)
	}
	if n := len(smtp.drain()); n != 0 {
		t.Errorf("no domain, no email: %d", n)
	}
	if got := e.sql(`SELECT count(*) FROM password_resets`); got != "0" {
		t.Errorf("and no link stored: %s", got)
	}
	// a site with no email at all behaves the same
	e2 := newEnv(t)
	a2 := e2.newAdmin("admin@example.com")
	e2.newUser(a2, "ed@example.com", "editor")
	if r := forgot(e2.client(), "ed@example.com"); r.Status != 200 {
		t.Errorf("no email configured: %d", r.Status)
	}
	if got := e2.sql(`SELECT count(*) FROM password_resets`); got != "0" {
		t.Errorf("no link stored: %s", got)
	}
}

func TestAdminCanMakeAResetLink(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	userID := e.sql(`SELECT id FROM users WHERE email = 'ed@example.com'`)

	if body := admin.get("/admin/users").Body; !strings.Contains(body, `action="/admin/users/`+userID+`/reset"`) {
		t.Error("the team page offers it")
	}
	r := admin.post("/admin/users/"+userID+"/reset", url.Values{})
	m := reResetAdmin.FindStringSubmatch(r.Body)
	if r.Status != 200 || m == nil || !strings.Contains(r.Body, "ed@example.com") || !strings.Contains(r.Body, "will not be shown again") {
		t.Fatalf("make a link: %d\n%s", r.Status, firstLine(r.Body))
	}
	if strings.Contains(admin.get("/admin/users").Body, m[2]) {
		t.Error("the link is shown once")
	}
	if r := resetWith(e.client(), m[2], newPassword, newPassword); r.Status != 303 {
		t.Fatalf("use it: %d", r.Status)
	}
	if r := e.client().login("ed@example.com", newPassword); r.Status != 303 {
		t.Errorf("the new password: %d", r.Status)
	}
	if r := editor.get("/admin/"); r.Status == 200 {
		t.Error("the editor's earlier session ended")
	}
	// only admins; the token is required; no such user
	other := e.newUser(admin, "other@example.com", "editor")
	if r := other.post("/admin/users/"+userID+"/reset", url.Values{}); r.Status != 403 {
		t.Errorf("an editor cannot make reset links: %d", r.Status)
	}
	if r := e.client().post("/admin/users/"+userID+"/reset", url.Values{}); r.Status == 200 {
		t.Error("logged out")
	}
	if r := admin.postNoToken("/admin/users/"+userID+"/reset", url.Values{}); r.Status != 403 {
		t.Errorf("without the CSRF token: %d", r.Status)
	}
	if r := admin.post("/admin/users/999999/reset", url.Values{}); r.Status != 404 {
		t.Errorf("no such user: %d", r.Status)
	}
	if got := e.sql(`SELECT count(*) FROM password_resets`); got != "0" {
		t.Errorf("only the used link existed: %s", got)
	}
	_ = time.Second
}
