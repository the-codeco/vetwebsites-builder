package app_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"webbuilder/internal/auth"
	"webbuilder/internal/config"
)

func TestFirstRunSetupAndLogin(t *testing.T) {
	e := newEnv(t)
	c := e.client()

	if r := c.get("/"); r.Status != 200 || !strings.Contains(r.Body, "Welcome to your new website") {
		t.Fatalf("seeded home page missing: %d", r.Status)
	}
	if r := c.get("/admin/"); r.Status != 303 || r.Location() != "/admin/login" {
		t.Errorf("anonymous admin access: %d %q", r.Status, r.Location())
	}
	if r := c.get("/admin/login"); r.Status != 303 || r.Location() != "/setup" {
		t.Errorf("login with no users should lead to setup: %d %q", r.Status, r.Location())
	}

	tok := c.formToken("/setup")
	good := url.Values{"csrf": {tok}, "email": {"Admin@Example.com"}, "password": {testPassword}, "confirm": {testPassword}}
	with := func(k, v string) url.Values {
		out := url.Values{}
		for key, vals := range good {
			out[key] = vals
		}
		out.Set(k, v)
		return out
	}
	bad := map[string]url.Values{
		"bad csrf":      with("csrf", "nope"),
		"short":         with("password", "short"),
		"mismatch":      with("confirm", "different-password"),
		"invalid email": with("email", "not-an-email"),
	}
	for name, v := range bad {
		if r := c.post("/setup", v); r.Status != 400 {
			t.Errorf("setup %s: %d, want 400", name, r.Status)
		}
	}
	if n := e.sql(`SELECT count(*) FROM users`); n != "0" {
		t.Fatalf("a rejected setup created %s users", n)
	}

	r := c.post("/setup", good)
	if r.Status != 303 || r.Location() != "/admin/" {
		t.Fatalf("setup: %d %q", r.Status, r.Location())
	}
	cookie := strings.Join(r.Header.Values("Set-Cookie"), "\n")
	for _, want := range []string{"wb_session=", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("session cookie lacks %q: %s", want, cookie)
		}
	}
	if strings.Contains(cookie, "Secure") {
		t.Error("a plain-HTTP request must not get Secure cookies (the browser would drop them)")
	}
	if got := e.sql(`SELECT email FROM users`); got != "admin@example.com" {
		t.Errorf("email should be normalised: %q", got)
	}

	// the setup page is gone for good
	other := e.client()
	if r := other.get("/setup"); r.Status != 404 {
		t.Errorf("GET /setup after setup: %d", r.Status)
	}
	if r := other.post("/setup", url.Values{"csrf": {"x"}, "email": {"b@example.com"}, "password": {testPassword}, "confirm": {testPassword}}); r.Status != 404 {
		t.Errorf("POST /setup after setup: %d", r.Status)
	}

	// logout must end the session on the server, not just clear the browser's cookie:
	// a copied cookie has to stop working too
	c.refreshToken()
	stolen := c.cookie("wb_session")
	if stolen == "" {
		t.Fatal("no session cookie to test with")
	}
	if r := e.client().get("/admin/", "Cookie", "wb_session="+stolen); r.Status != 200 {
		t.Fatalf("the copied cookie should work before logout: %d", r.Status)
	}
	if r := c.post("/admin/logout", nil); r.Status != 303 {
		t.Errorf("logout: %d", r.Status)
	}
	if r := c.get("/admin/"); r.Status != 303 {
		t.Errorf("session survived logout: %d", r.Status)
	}
	if r := e.client().get("/admin/", "Cookie", "wb_session="+stolen); r.Status != 303 {
		t.Errorf("a copied session cookie still works after logout: %d", r.Status)
	}
	if r := c.login("admin@example.com", "wrong-password-here"); r.Status != 401 {
		t.Errorf("wrong password: %d", r.Status)
	}
	if r := c.login("nobody@example.com", testPassword); r.Status != 401 {
		t.Errorf("unknown user must look the same as a wrong password: %d", r.Status)
	}
	if r := c.login("ADMIN@example.com", testPassword); r.Status != 303 {
		t.Errorf("login: %d", r.Status)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	e.newAdmin("admin@example.com")
	c := e.client()
	for i := 0; i < 8; i++ {
		if r := c.login("admin@example.com", "wrong-password-"+string(rune('a'+i))); r.Status != 401 {
			t.Fatalf("attempt %d: %d", i+1, r.Status)
		}
	}
	// locked: even the correct password is refused until the window passes
	if r := c.login("admin@example.com", testPassword); r.Status != 429 {
		t.Errorf("correct password while locked out: %d, want 429", r.Status)
	}
	// a different account from the same address is also blocked (per-IP limit)
	if r := c.login("someone-else@example.com", testPassword); r.Status != 429 {
		t.Errorf("per-IP limit: %d, want 429", r.Status)
	}
}

func TestSetupToken(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.SetupToken = "s3cret-setup-token" })
	c := e.client()
	if r := c.get("/setup"); !strings.Contains(r.Body, `name="setup_token"`) {
		t.Fatal("setup form must ask for the token when one is configured")
	}
	tok := c.formToken("/setup")
	form := func(token string) url.Values {
		return url.Values{"csrf": {tok}, "setup_token": {token}, "email": {"admin@example.com"}, "password": {testPassword}, "confirm": {testPassword}}
	}
	for _, wrong := range []string{"", "wrong", "s3cret-setup-token-and-more", "S3CRET-SETUP-TOKEN"} {
		if r := c.post("/setup", form(wrong)); r.Status != 400 || !strings.Contains(r.Body, "setup token") {
			t.Errorf("token %q: %d", wrong, r.Status)
		}
	}
	if n := e.sql(`SELECT count(*) FROM users`); n != "0" {
		t.Fatalf("an account was created without the right token (%s users)", n)
	}
	if r := c.post("/setup", form("s3cret-setup-token")); r.Status != 303 {
		t.Fatalf("correct token: %d", r.Status)
	}
	if r := e.client().get("/setup"); r.Status != 404 {
		t.Errorf("setup must close afterwards: %d", r.Status)
	}
}

func TestSetupTokenBruteForceIsLimited(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.SetupToken = "s3cret-setup-token" })
	c := e.client()
	tok := c.formToken("/setup")
	form := func(token string) url.Values {
		return url.Values{"csrf": {tok}, "setup_token": {token}, "email": {"admin@example.com"}, "password": {testPassword}, "confirm": {testPassword}}
	}
	for i := 0; i < 8; i++ {
		c.post("/setup", form("guess"))
	}
	if r := c.post("/setup", form("s3cret-setup-token")); r.Status != 400 {
		t.Errorf("after repeated wrong guesses even the right token must be refused: %d", r.Status)
	}
	if n := e.sql(`SELECT count(*) FROM users`); n != "0" {
		t.Errorf("%s users created", n)
	}
}

func TestRolesInvitesAndCSRF(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")

	path := e.invite(admin, "editor@example.com", "editor")
	if r := admin.post("/admin/users/invite", url.Values{"email": {"editor@example.com"}, "role": {"editor"}}); r.Status != 200 {
		t.Errorf("a second invite for the same email while pending is allowed (status %d)", r.Status)
	}
	if r := admin.post("/admin/users/invite", url.Values{"email": {"nope"}, "role": {"editor"}}); r.Status != 400 {
		t.Errorf("invalid email: %d", r.Status)
	}
	if r := admin.post("/admin/users/invite", url.Values{"email": {"x@example.com"}, "role": {"superuser"}}); r.Status != 400 {
		t.Errorf("invalid role: %d", r.Status)
	}

	editor := e.client()
	itok := editor.formToken(path)
	if r := editor.post(path, url.Values{"csrf": {itok}, "password": {"short"}, "confirm": {"short"}}); r.Status != 400 {
		t.Errorf("short invite password: %d", r.Status)
	}
	if r := editor.post(path, url.Values{"csrf": {itok}, "password": {testPassword}, "confirm": {testPassword}}); r.Status != 303 {
		t.Fatalf("accept invite: %d", r.Status)
	}
	editor.refreshToken()
	if r := e.client().get(path); r.Status != 404 {
		t.Errorf("an invite link must work only once: %d", r.Status)
	}
	if r := admin.post("/admin/users/invite", url.Values{"email": {"editor@example.com"}, "role": {"editor"}}); r.Status != 409 {
		t.Errorf("inviting an existing user: %d, want 409", r.Status)
	}

	// editors can edit content but not manage the site
	for _, p := range []string{"/admin/users", "/admin/appearance", "/admin/domain"} {
		if r := editor.get(p); r.Status != 403 {
			t.Errorf("editor GET %s: %d, want 403", p, r.Status)
		}
	}
	for _, p := range []string{"/admin/appearance/theme", "/admin/appearance/css", "/admin/appearance/title", "/admin/appearance/tokens/reset",
		"/admin/domain", "/admin/domain/check", "/admin/domain/remove", "/admin/users/invite", "/admin/users/1/delete"} {
		if r := editor.post(p, url.Values{"theme": {"midnight"}, "css": {"body{}"}, "title": {"x"}, "domain": {"a.com"}, "email": {"y@example.com"}, "role": {"admin"}}); r.Status != 403 {
			t.Errorf("editor POST %s: %d, want 403", p, r.Status)
		}
	}
	for _, p := range []string{"/admin/", "/admin/pages", "/admin/posts", "/admin/media", "/admin/menus", "/admin/categories", "/admin/account"} {
		if r := editor.get(p); r.Status != 200 {
			t.Errorf("editor GET %s: %d, want 200", p, r.Status)
		}
	}
	if r := e.client().get("/admin/pages"); r.Status != 303 {
		t.Errorf("anonymous GET /admin/pages: %d", r.Status)
	}
	if r := e.client().post("/admin/pages", url.Values{"title": {"x"}}); r.Status != 303 && r.Status != 403 {
		t.Errorf("anonymous POST /admin/pages: %d", r.Status)
	}

	// CSRF: every state change needs the session's token
	before := e.sql(`SELECT count(*) FROM pages`)
	if r := admin.postNoToken("/admin/pages", url.Values{"title": {"No Token"}}); r.Status != 403 {
		t.Errorf("no token: %d", r.Status)
	}
	if r := admin.post("/admin/pages", url.Values{"csrf": {"forged"}, "title": {"Forged"}}); r.Status != 403 {
		t.Errorf("forged token: %d", r.Status)
	}
	if r := editor.post("/admin/pages", url.Values{"csrf": {admin.token}, "title": {"Other Session"}}); r.Status != 403 {
		t.Errorf("another session's token must not work: %d", r.Status)
	}
	// ...and a browser-style cross-origin request is stopped even with a valid token
	r := admin.post("/admin/pages", url.Values{"title": {"Cross Site"}}, "Origin", "http://evil.example", "Sec-Fetch-Site", "cross-site")
	if r.Status != 403 {
		t.Errorf("cross-origin POST: %d, want 403", r.Status)
	}
	if after := e.sql(`SELECT count(*) FROM pages`); after != before {
		t.Errorf("a blocked request still created a page (%s -> %s)", before, after)
	}
	if r := admin.post("/admin/pages", url.Values{"title": {"Same Origin"}}, "Origin", e.srv.URL, "Sec-Fetch-Site", "same-origin"); r.Status != 303 {
		t.Errorf("legitimate same-origin POST: %d", r.Status)
	}
	// htmx sends the token as a header instead of a form field
	pageID := idFromLocation(t, admin.post("/admin/pages", url.Values{"title": {"Header Token"}}).Location())
	if r := admin.postNoToken("/admin/pages/"+pageID+"/blocks", url.Values{"type": {"hero"}}, "X-CSRF-Token", admin.token); r.Status != 200 {
		t.Errorf("header token: %d", r.Status)
	}
}

func TestRemovingUsers(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "editor@example.com", "editor")
	adminID := e.sql(`SELECT id FROM users WHERE email = 'admin@example.com'`)
	editorID := e.sql(`SELECT id FROM users WHERE email = 'editor@example.com'`)

	if r := admin.post("/admin/users/"+adminID+"/delete", nil); r.Status != 400 || !strings.Contains(r.Body, "your own account") {
		t.Errorf("removing yourself: %d", r.Status)
	}
	if r := admin.post("/admin/users/999999/delete", nil); r.Status != 404 {
		t.Errorf("removing a missing user: %d", r.Status)
	}
	if r := admin.post("/admin/users/"+editorID+"/delete", nil); r.Status != 303 {
		t.Fatalf("removing the editor: %d", r.Status)
	}
	if r := editor.get("/admin/pages"); r.Status != 303 {
		t.Errorf("a removed user's session must stop working: %d", r.Status)
	}
	if r := e.client().login("editor@example.com", testPassword); r.Status != 401 {
		t.Errorf("a removed user must not be able to log in: %d", r.Status)
	}

	// the last admin can never be removed, even if something bypasses the UI checks
	err := auth.NewStore(e.pool).DeleteUser(context.Background(), 1)
	if !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("removing the only admin: %v, want ErrLastAdmin", err)
	}
	if n := e.sql(`SELECT count(*) FROM users WHERE role = 'admin'`); n != "1" {
		t.Errorf("admins left: %s", n)
	}
}

func TestChangingPassword(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	other := e.client() // a second device
	if r := other.login("admin@example.com", testPassword); r.Status != 303 {
		t.Fatal("second login failed")
	}

	const newPW = "a-brand-new-password"
	cases := map[string]url.Values{
		"wrong current": {"current": {"not-my-password"}, "password": {newPW}, "confirm": {newPW}},
		"too short":     {"current": {testPassword}, "password": {"short"}, "confirm": {"short"}},
		"mismatch":      {"current": {testPassword}, "password": {newPW}, "confirm": {newPW + "x"}},
		"unchanged":     {"current": {testPassword}, "password": {testPassword}, "confirm": {testPassword}},
	}
	want := map[string]int{"wrong current": 401, "too short": 400, "mismatch": 400, "unchanged": 400}
	for name, v := range cases {
		if r := admin.post("/admin/account/password", v); r.Status != want[name] {
			t.Errorf("%s: %d, want %d", name, r.Status, want[name])
		}
	}
	if r := e.client().login("admin@example.com", newPW); r.Status != 401 {
		t.Error("a rejected change must not alter the password")
	}

	ok := url.Values{"current": {testPassword}, "password": {newPW}, "confirm": {newPW}}
	if r := admin.post("/admin/account/password", ok); r.Status != 303 {
		t.Fatalf("change: %d", r.Status)
	}
	if r := admin.get("/admin/pages"); r.Status != 200 {
		t.Errorf("the session that changed the password must stay signed in: %d", r.Status)
	}
	if r := other.get("/admin/pages"); r.Status != 303 {
		t.Errorf("other sessions must be signed out after a password change: %d", r.Status)
	}
	if r := e.client().login("admin@example.com", testPassword); r.Status != 401 {
		t.Error("old password still works")
	}
	if r := e.client().login("admin@example.com", newPW); r.Status != 303 {
		t.Error("new password doesn't work")
	}

	// guessing the current password is rate limited
	for i := 0; i < 8; i++ {
		admin.post("/admin/account/password", url.Values{"current": {"wrong"}, "password": {newPW + "2"}, "confirm": {newPW + "2"}})
	}
	if r := admin.post("/admin/account/password", url.Values{"current": {newPW}, "password": {newPW + "2"}, "confirm": {newPW + "2"}}); r.Status != 429 {
		t.Errorf("after repeated failures: %d, want 429", r.Status)
	}
}
