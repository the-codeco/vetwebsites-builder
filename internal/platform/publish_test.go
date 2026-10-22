package platform_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/config"
	"webbuilder/internal/publish/fakehost"
)

const testSecretKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func withKey(c *config.Config) { c.SecretKey = testSecretKey; c.AllowPrivateHosts = true }

func hostingForm(h *fakehost.Host, protocol, publicURL string) url.Values {
	return url.Values{
		"protocol": {protocol}, "host": {h.Host}, "port": {strconv.Itoa(h.Port)}, "user": {h.User}, "password": {h.Password},
		"dir": {"public_html"}, "public_url": {publicURL},
	}
}

// publishAndWait starts a publish and waits for it to end; it returns the job's status and row id.
func (e *env) publishAndWait(c *client, force bool) (status, id string) {
	e.t.Helper()
	v := url.Values{}
	if force {
		v.Set("force", "1")
	}
	r := c.post("/admin/publish/start", v)
	if r.Status != 303 {
		e.t.Fatalf("start: %d %s", r.Status, firstLine(r.Body))
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		id = e.scalar(`SELECT max(id) FROM publish_jobs`)
		status = e.scalar(`SELECT status FROM publish_jobs WHERE id = ` + id)
		if status == "done" || status == "failed" {
			return status, id
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatalf("the publish did not end (status %s)", status)
	return
}

func TestPublishToSFTPHosting(t *testing.T) {
	e := newEnv(t, withKey)
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	h := fakehost.StartSFTP(t)
	newPage(t, ann, "About us", "about-us", "<p>We fix teeth</p>")
	newPage(t, ann, "Services", "services", "<p>Cleaning</p>")
	ann.post("/admin/menus/main", url.Values{"structure": {`[{"kind":"link","label":"About","url":"/about-us"},{"kind":"link","label":"Services","url":"/services"}]`}})

	// nothing can be published before the hosting is set up
	if r := ann.post("/admin/publish/start", nil); r.Status != 400 || !strings.Contains(r.Body, "Set up your hosting") {
		t.Errorf("publishing without hosting: %d", r.Status)
	}
	// bad settings are explained
	bad := hostingForm(h, "sftp", "not a url")
	if r := ann.post("/admin/hosting", bad); r.Status != 400 || !strings.Contains(r.Body, "website&#39;s address") && !strings.Contains(r.Body, "address") {
		t.Errorf("a bad address is explained: %d", r.Status)
	}
	if r := ann.post("/admin/hosting", hostingForm(h, "sftp", "http://127.0.0.1:9/")); r.Status != 303 {
		t.Fatalf("save hosting: %d %s", r.Status, firstLine(r.Body))
	}

	// the password is stored encrypted and is never shown again
	if enc := e.scalar(`SELECT password_enc FROM publish_targets`); strings.Contains(enc, h.Password) || enc == "" {
		t.Error("the stored password is encrypted")
	}
	for _, p := range []string{"/admin/hosting", "/admin/publish", "/admin/"} {
		if r := ann.get(p); strings.Contains(r.Body, h.Password) {
			t.Errorf("%s shows the hosting password", p)
		}
	}
	if r := ann.get("/admin/hosting"); !strings.Contains(r.Body, "leave blank to keep") {
		t.Error("the form says a password is kept")
	}

	if r := ann.post("/admin/hosting/test", nil); r.Status != 200 || !strings.Contains(r.Body, "files can be put in") {
		t.Fatalf("test connection: %d %s", r.Status, r.Body)
	}
	if e.scalar(`SELECT host_key FROM publish_targets`) != h.Fingerprint {
		t.Error("the server's identity is remembered on first connection")
	}

	status, _ := e.publishAndWait(ann, false)
	if status != "done" {
		t.Fatalf("publish: %s: %s", status, e.scalar(`SELECT error FROM publish_jobs ORDER BY id DESC LIMIT 1`))
	}
	for _, f := range []string{"index.html", "about-us.html", "services.html", ".htaccess", "404.html", "sitemap.xml", "site.css"} {
		if !h.Exists(f) {
			t.Errorf("%s was not published", f)
		}
	}
	if !strings.Contains(h.Read(t, "about-us.html"), "We fix teeth") {
		t.Error("the page content is published")
	}
	if h.Exists(".wb-build.json") {
		t.Error("the program's own note is not part of the website")
	}
	if e.scalar(`SELECT published_version FROM sites`) == "" {
		t.Error("the site records what was published")
	}
	if n := e.scalar(`SELECT published_changes = build_changes FROM sites`); n != "true" {
		t.Error("everything up to the build is published")
	}
	if r := ann.get("/admin/publish"); !strings.Contains(r.Body, "up to date") {
		t.Errorf("the screen says the site is up to date")
	}
	if !strings.Contains(e.scalar(`SELECT warnings FROM publish_jobs ORDER BY id DESC LIMIT 1`), "did not answer") {
		t.Error("a published address that does not answer is a warning")
	}

	// a change publishes only what differs
	first := e.scalar(`SELECT uploaded FROM publish_jobs ORDER BY id DESC LIMIT 1`)
	pid := e.scalar(`SELECT id FROM site_1.pages WHERE slug = 'services'`)
	ann.post("/admin/pages/"+pid, url.Values{"title": {"Services"}, "slug": {"services"}, "status": {"published"}})
	if r := ann.get("/admin/publish"); !strings.Contains(r.Body, "not published yet") {
		t.Error("a change is shown as unpublished")
	}
	// the page just saved: now remove About
	aid := e.scalar(`SELECT id FROM site_1.pages WHERE slug = 'about-us'`)
	if r := ann.post("/admin/pages/"+aid+"/delete", nil); r.Status != 303 {
		t.Fatalf("delete page: %d %s", r.Status, firstLine(r.Body))
	}
	status, _ = e.publishAndWait(ann, false)
	if status != "done" {
		t.Fatalf("second publish: %s", e.scalar(`SELECT error FROM publish_jobs ORDER BY id DESC LIMIT 1`))
	}
	for i := 0; i < 50 && h.Exists("about-us.html"); i++ { // the fake hosting finishes a removal a moment after it is asked (Windows)
		time.Sleep(100 * time.Millisecond)
	}
	if h.Exists("about-us.html") {
		t.Error("a deleted page is removed from the hosting")
	}
	if e.scalar(`SELECT uploaded >= `+first+` FROM publish_jobs ORDER BY id DESC LIMIT 1`) == "true" {
		t.Errorf("the second publish uploads fewer files than the first (%s)", first)
	}
	if e.scalar(`SELECT unchanged FROM publish_jobs ORDER BY id DESC LIMIT 1`) == "0" {
		t.Error("files that did not change are left alone")
	}

	// files that are not the site's are never touched
	h.WriteForeign(t, "cgi-bin/mine.txt", "not ours")
	h.WriteForeign(t, "wp-config.php", "secret")
	e.publishAndWait(ann, true)
	if !h.Exists("cgi-bin/mine.txt") || !h.Exists("wp-config.php") {
		t.Error("files we did not upload survive a publish")
	}

	// the audit log says who did what, and nothing in it carries the password
	if n := e.scalar(`SELECT count(*) FROM audit_log WHERE action LIKE '%publish%'`); n == "0" {
		t.Error("publishing is audited")
	}
	if n := e.scalar(`SELECT count(*) FROM audit_log WHERE detail LIKE '%' || $1 || '%'`, h.Password); n != "0" {
		t.Error("the password is in the audit log")
	}
	if n := e.scalar(`SELECT count(*) FROM publish_jobs WHERE error LIKE '%' || $1 || '%' OR message LIKE '%' || $1 || '%'`, h.Password); n != "0" {
		t.Error("the password is in a job")
	}
}

func TestPublishRefusalsAndFailures(t *testing.T) {
	// no secret key: nothing can be stored
	e := newEnv(t)
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	h := fakehost.StartSFTP(t)
	if r := ann.post("/admin/hosting", hostingForm(h, "sftp", "https://example.org")); r.Status != 400 || !strings.Contains(r.Body, "secret key") {
		t.Errorf("without a key: %d", r.Status)
	}

	// private addresses are refused unless the server allows them
	e2 := newEnv(t, func(c *config.Config) { c.SecretKey = testSecretKey })
	admin2 := e2.setupAdmin("admin@example.com")
	bob := e2.customer(admin2, "bob@b.example", "Bobs Shop")
	if r := bob.post("/admin/hosting", hostingForm(h, "sftp", "https://example.org")); r.Status != 303 {
		t.Fatalf("save: %d", r.Status)
	}
	if r := bob.post("/admin/hosting/test", nil); !strings.Contains(r.Body, "not allowed") {
		t.Errorf("a loopback server is refused: %s", r.Body)
	}
	if status, _ := e2.publishAndWait(bob, false); status != "failed" || h.Exists("index.html") {
		t.Errorf("publishing to a loopback server: %s", status)
	}

	// a wrong password fails in words, without the password
	e3 := newEnv(t, withKey)
	admin3 := e3.setupAdmin("admin@example.com")
	cy := e3.customer(admin3, "cy@c.example", "Cys Cafe")
	wrong := hostingForm(h, "sftp", "https://example.org")
	wrong.Set("password", "wrong-password-123")
	if r := cy.post("/admin/hosting", wrong); r.Status != 303 {
		t.Fatalf("save: %d %s", r.Status, firstLine(r.Body))
	}
	if r := cy.post("/admin/hosting/test", nil); !strings.Contains(r.Body, "refused the username or password") || strings.Contains(r.Body, "wrong-password-123") {
		t.Errorf("a wrong password is explained: %s", r.Body)
	}
	status, _ := e3.publishAndWait(cy, false)
	if status != "failed" || !strings.Contains(e3.scalar(`SELECT error FROM publish_jobs ORDER BY id DESC LIMIT 1`), "username or password") {
		t.Errorf("a publish with a wrong password fails in words: %s", status)
	}
	// a failed publish leaves the site's published state alone
	if e3.scalar(`SELECT coalesce(published_version, '') FROM sites`) != "" {
		t.Error("a failed publish does not count as published")
	}
	// the customer can fix the password and publish again; the old one is kept when the field is blank
	right := hostingForm(h, "sftp", "https://example.org")
	if r := cy.post("/admin/hosting", right); r.Status != 303 {
		t.Fatal(r.Status)
	}
	blank := hostingForm(h, "sftp", "https://example.org")
	blank.Set("password", "")
	if r := cy.post("/admin/hosting", blank); r.Status != 303 {
		t.Fatalf("a blank password keeps the saved one: %d", r.Status)
	}
	if status, _ := e3.publishAndWait(cy, false); status != "done" {
		t.Errorf("publish after fixing: %s %s", status, e3.scalar(`SELECT error FROM publish_jobs ORDER BY id DESC LIMIT 1`))
	}

	// a suspended website cannot publish; the other customer cannot see this one's hosting
	siteID := e3.scalar(`SELECT id FROM sites`)
	admin3.post("/platform/sites/"+siteID+"/status", url.Values{"active": {"0"}})
	if r := cy.post("/admin/publish/start", nil); r.Status == 303 {
		t.Errorf("a suspended website publishes: %d", r.Status)
	}
}

func TestPublishToFTPSHostingAndNewServerKey(t *testing.T) {
	e := newEnv(t, withKey)
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	h := fakehost.StartFTPS(t)
	e.p.TrustHostingCertificates(h.CAs, "127.0.0.1")
	newPage(t, ann, "About us", "about-us", "<p>We fix teeth</p>")
	if r := ann.post("/admin/hosting", hostingForm(h, "ftps", "http://127.0.0.1:9")); r.Status != 303 {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	if r := ann.post("/admin/hosting/test", nil); !strings.Contains(r.Body, "files can be put in") {
		t.Fatalf("ftps test: %s", r.Body)
	}
	if status, _ := e.publishAndWait(ann, false); status != "done" {
		t.Fatalf("ftps publish: %s %s", status, e.scalar(`SELECT error FROM publish_jobs ORDER BY id DESC LIMIT 1`))
	}
	if !strings.Contains(h.Read(t, "about-us.html"), "We fix teeth") || !h.Exists("index.html") {
		t.Error("the site is on the FTPS hosting")
	}

	// SFTP: a different identity than the remembered one stops the publish until the customer trusts it
	s := fakehost.StartSFTP(t)
	ann.post("/admin/hosting", hostingForm(s, "sftp", "http://127.0.0.1:9"))
	ann.post("/admin/hosting/test", nil)
	e.pool.Exec(t.Context(), `UPDATE publish_targets SET host_key = 'SHA256:somebodyelse'`)
	if r := ann.post("/admin/hosting/test", nil); !strings.Contains(r.Body, "identity") {
		t.Errorf("a changed identity is reported: %s", r.Body)
	}
	if status, _ := e.publishAndWait(ann, false); status != "failed" {
		t.Errorf("publishing to a server with a changed identity: %s", status)
	}
	if s.Exists("index.html") {
		t.Error("nothing is uploaded to a server whose identity changed")
	}
	if r := ann.post("/admin/hosting/trust", nil); r.Status != 303 {
		t.Fatalf("trust: %d", r.Status)
	}
	if status, _ := e.publishAndWait(ann, false); status != "done" {
		t.Errorf("after trusting: %s", status)
	}
}
