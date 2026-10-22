package app_test

import (
	"context"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/config"
)

var (
	reUnsub      = regexp.MustCompile(`https://example\.com/unsubscribe/([A-Za-z0-9_-]{43})`)
	reNewsletter = regexp.MustCompile(`/admin/newsletters/(\d+)`)
)

// mailEnv is a site with email working through a fake server and a saved domain, with an admin logged in.
func mailEnv(t *testing.T, withDomain bool) (*testEnv, *client, *fakeSMTP) {
	t.Helper()
	smtp := startFakeSMTP(t)
	host, port, _ := net.SplitHostPort(smtp.addr)
	e := newEnv(t, func(c *config.Config) {
		c.SMTPHost, c.SMTPPort, c.SMTPFrom = host, port, "The Site <noreply@example.com>"
	})
	admin := e.newAdmin("admin@example.com")
	if withDomain {
		e.pool.Exec(context.Background(), `UPDATE site_settings SET domain = 'example.com', title = 'Example Co'`)
	}
	c := e.app.Campaigns()
	c.RetryStep, c.Lease = 20*time.Millisecond, time.Minute
	return e, admin, smtp
}

var subscriberSeq int

// subscribe adds an address to the list, confirmed or still waiting.
func (e *testEnv) subscribe(email string, confirmed bool) {
	e.t.Helper()
	subscriberSeq++
	q := `INSERT INTO subscribers (email, token_hash, confirmed_at) VALUES ($1, $2, ` + map[bool]string{true: "now()", false: "NULL"}[confirmed] + `)`
	if _, err := e.pool.Exec(context.Background(), q, email, "hash-"+strconv.Itoa(subscriberSeq)+"-"+email); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) newsletter(admin *client, subject, body string) string {
	e.t.Helper()
	r := admin.post("/admin/newsletters", url.Values{"subject": {subject}, "body": {body}})
	m := reNewsletter.FindStringSubmatch(r.Location())
	if r.Status != 303 || m == nil {
		e.t.Fatalf("create newsletter: %d %s", r.Status, firstLine(r.Body))
	}
	return m[1]
}

func (e *testEnv) send(admin *client, id string, recipients int) response {
	return admin.post("/admin/newsletters/"+id+"/send", url.Values{"recipients": {strconv.Itoa(recipients)}})
}

func (e *testEnv) deliver() int {
	e.t.Helper()
	n, err := e.app.DeliverQueuedMail(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// headerOf reads one header from a captured message.
func headerOf(msg, name string) string {
	_, data, _ := strings.Cut(msg, "\n\n") // the fake server puts the envelope first
	for _, l := range strings.Split(data, "\r\n") {
		if strings.HasPrefix(strings.ToLower(l), strings.ToLower(name)+":") {
			return strings.TrimSpace(l[len(name)+1:])
		}
	}
	return ""
}

func rcptOf(msg string) string {
	m := regexp.MustCompile(`(?i)RCPT TO:\s*<([^>]+)>`).FindStringSubmatch(msg)
	if m == nil {
		return ""
	}
	return m[1]
}

func (f *fakeSMTP) drain() []string {
	var out []string
	for {
		select {
		case m := <-f.got:
			out = append(out, m)
		case <-time.After(150 * time.Millisecond):
			return out
		}
	}
}

func TestNewsletterEndToEnd(t *testing.T) {
	e, admin, smtp := mailEnv(t, true)
	anon := e.client()
	for _, a := range []string{"ann@example.com", "bob@example.com", "cy@example.com"} {
		e.subscribe(a, true)
	}
	e.subscribe("waiting@example.com", false) // never confirmed: gets nothing

	// the screen is there, with the sidebar link
	page := admin.get("/admin/newsletters")
	if page.Status != 200 || !strings.Contains(page.Body, "Write a newsletter") || !strings.Contains(page.Body, `href="/admin/newsletters"`) {
		t.Fatalf("newsletters screen: %d", page.Status)
	}

	id := e.newsletter(admin, "Autumn news", "Hello everyone,\n\nThe harvest is in.\n\nSee https://example.com/blog")
	draft := admin.get("/admin/newsletters/" + id)
	for _, want := range []string{`value="Autumn news"`, "The harvest is in.", "unsubscribe link", "Send to 3 subscribers", `name="recipients" value="3"`, "Send a test to admin@example.com"} {
		if !strings.Contains(draft.Body, want) {
			t.Errorf("draft page lacks %q", want)
		}
	}

	// a draft can be edited, and a test goes to the person logged in, not to the list
	if r := admin.post("/admin/newsletters/"+id, url.Values{"subject": {"Autumn news!"}, "body": {"Hello everyone,\n\nThe harvest is in."}}); r.Status != 303 {
		t.Fatalf("save: %d", r.Status)
	}
	if r := admin.post("/admin/newsletters/"+id+"/test", nil); r.Status != 303 {
		t.Fatalf("test: %d", r.Status)
	}
	test := smtp.next(t)
	if rcptOf(test) != "admin@example.com" || !strings.Contains(headerOf(test, "Subject"), "[Test] Autumn news!") || !strings.Contains(test, "This is a test") {
		t.Errorf("test email:\n%s", test)
	}
	if more := smtp.drain(); len(more) != 0 {
		t.Errorf("a test must not go to the list: %v", more)
	}
	if e.sql(`SELECT count(*) FROM deliveries`) != "0" {
		t.Error("a test queues nothing")
	}

	// a stale count refuses to send; nothing is queued and it stays a draft
	if r := e.send(admin, id, 2); r.Status != 409 || !strings.Contains(r.Body, "now has 3 subscribers") {
		t.Errorf("stale count: %d", r.Status)
	}
	if e.sql(`SELECT status FROM campaigns WHERE id = `+id) != "draft" || e.sql(`SELECT count(*) FROM deliveries`) != "0" {
		t.Error("a refused send must change nothing")
	}
	if r := admin.post("/admin/newsletters/"+id+"/send", nil); r.Status != 400 {
		t.Errorf("no count: %d", r.Status)
	}

	// send: queued at once, delivered by the worker
	if r := e.send(admin, id, 3); r.Status != 303 || !strings.Contains(r.Location(), "done=started") {
		t.Fatalf("send: %d %s", r.Status, r.Body)
	}
	if e.sql(`SELECT status FROM campaigns WHERE id = `+id) != "sending" || e.sql(`SELECT total FROM campaigns WHERE id = `+id) != "3" {
		t.Error("it should be sending to 3 people")
	}
	if got := e.sql(`SELECT count(*) FROM deliveries WHERE status = 'pending'`); got != "3" {
		t.Errorf("pending: %s", got)
	}
	prog := admin.get("/admin/newsletters/" + id + "/progress")
	if !strings.Contains(prog.Body, `hx-trigger="every 3s"`) || prog.Header.Get("HX-Refresh") != "" {
		t.Errorf("a newsletter being sent polls for progress: %q %q", prog.Body, prog.Header.Get("HX-Refresh"))
	}
	if r := admin.post("/admin/newsletters/"+id, url.Values{"subject": {"x"}, "body": {"y"}}); r.Status != 409 {
		t.Errorf("a newsletter being sent can't be edited: %d", r.Status)
	}
	if r := admin.post("/admin/newsletters/"+id+"/delete", nil); r.Status != 409 {
		t.Errorf("a newsletter being sent can't be deleted: %d", r.Status)
	}
	if r := e.send(admin, id, 3); r.Status != 409 {
		t.Errorf("sending twice: %d", r.Status)
	}
	if e.sql(`SELECT count(*) FROM deliveries`) != "3" {
		t.Error("sending twice must not queue anyone twice")
	}

	if n := e.deliver(); n != 3 {
		t.Fatalf("delivered %d, want 3", n)
	}
	got := smtp.drain()
	if len(got) != 3 {
		t.Fatalf("%d emails arrived, want 3", len(got))
	}
	seen := map[string]string{}
	for _, m := range got {
		to := rcptOf(m)
		tok := reUnsub.FindStringSubmatch(m)
		if tok == nil {
			t.Fatalf("no unsubscribe link in the email to %s:\n%s", to, m)
		}
		seen[to] = tok[1]
		for _, want := range []string{
			"Subject: Autumn news!", "The harvest is in.", "You are receiving this because you subscribed to Example Co.",
			"To unsubscribe: https://example.com/unsubscribe/" + tok[1],
		} {
			if !strings.Contains(m, want) {
				t.Errorf("email to %s lacks %q", to, want)
			}
		}
		if lu := headerOf(m, "List-Unsubscribe"); lu != "<https://example.com/unsubscribe/"+tok[1]+">" {
			t.Errorf("List-Unsubscribe: %q", lu)
		}
		if headerOf(m, "List-Unsubscribe-Post") != "List-Unsubscribe=One-Click" {
			t.Error("one-click unsubscribe header missing")
		}
		if headerOf(m, "To") != to || !strings.Contains(headerOf(m, "From"), "noreply@example.com") {
			t.Errorf("addresses: To %q From %q", headerOf(m, "To"), headerOf(m, "From"))
		}
	}
	if len(seen) != 3 || seen["waiting@example.com"] != "" || seen["ann@example.com"] == seen["bob@example.com"] {
		t.Errorf("each of the three confirmed subscribers gets their own link, and nobody else gets anything: %v", seen)
	}

	// done: sent, with the numbers
	if e.sql(`SELECT status FROM campaigns WHERE id = `+id) != "sent" {
		t.Errorf("status: %s", e.sql(`SELECT status FROM campaigns WHERE id = `+id))
	}
	done := admin.get("/admin/newsletters/" + id)
	if !strings.Contains(done.Body, "3 of 3") || strings.Contains(done.Body, "Send to") {
		t.Errorf("finished page:\n%s", done.Body)
	}
	if r := admin.get("/admin/newsletters/" + id + "/progress"); r.Header.Get("HX-Refresh") != "true" || strings.Contains(r.Body, "hx-trigger") {
		t.Error("polling must stop, and the page reload, once sending is over")
	}
	if r := admin.post("/admin/newsletters/"+id, url.Values{"subject": {"x"}, "body": {"y"}}); r.Status != 409 {
		t.Errorf("a sent newsletter can't be edited: %d", r.Status)
	}
	if r := e.send(admin, id, 3); r.Status != 409 {
		t.Errorf("a sent newsletter can't be sent again: %d", r.Status)
	}
	if n := e.deliver(); n != 0 || len(smtp.drain()) != 0 {
		t.Error("nothing is left to send")
	}

	// a recipient's own link leaves the list, and only them
	tok := seen["bob@example.com"]
	if r := anon.get("/unsubscribe/" + tok); r.Status != 200 || !strings.Contains(r.Body, "Unsubscribe") {
		t.Fatalf("unsubscribe page: %d", r.Status)
	}
	if e.sql(`SELECT count(*) FROM subscribers`) != "4" {
		t.Error("opening the link must not unsubscribe anyone: mail scanners open links")
	}
	if r := anon.post("/unsubscribe/"+tok, url.Values{"List-Unsubscribe": {"One-Click"}}); r.Status != 200 {
		t.Fatalf("unsubscribe: %d", r.Status)
	}
	if e.sql(`SELECT count(*) FROM subscribers WHERE email = 'bob@example.com'`) != "0" || e.sql(`SELECT count(*) FROM subscribers`) != "3" {
		t.Error("only bob should be gone")
	}
	if r := anon.post("/unsubscribe/"+tok, nil); r.Status != 404 {
		t.Errorf("a used link: %d", r.Status)
	}
	// the next newsletter goes to the two left
	id2 := e.newsletter(admin, "Winter news", "Brr.")
	if r := e.send(admin, id2, 2); r.Status != 303 {
		t.Fatalf("second send: %d %s", r.Status, r.Body)
	}
	e.deliver()
	if got := smtp.drain(); len(got) != 2 {
		t.Errorf("second newsletter reached %d people", len(got))
	} else {
		for _, m := range got {
			if rcptOf(m) == "bob@example.com" {
				t.Error("bob unsubscribed")
			}
			if tk := reUnsub.FindStringSubmatch(m); tk == nil || tk[1] != seen[rcptOf(m)] {
				t.Errorf("a person keeps the same unsubscribe link: %s", rcptOf(m))
			}
		}
	}

	// deleting
	if r := admin.post("/admin/newsletters/"+id+"/delete", nil); r.Status != 303 {
		t.Errorf("delete: %d", r.Status)
	}
	if r := admin.get("/admin/newsletters/" + id); r.Status != 404 {
		t.Errorf("deleted newsletter: %d", r.Status)
	}
}

func TestNewsletterFailuresAndRetries(t *testing.T) {
	e, admin, smtp := mailEnv(t, true)
	for _, a := range []string{"ok@example.com", "gone@example.com", "busy@example.com", "never@example.com", "later@example.com"} {
		e.subscribe(a, true)
	}
	e.app.Campaigns().RetryStep = 250 * time.Millisecond
	smtp.refuse("gone@", "550 no such mailbox")
	smtp.refuse("busy@", "451 try again later")
	smtp.refuse("never@", "451 still busy")
	smtp.refuse("later@", "451 try again later")

	id := e.newsletter(admin, "News", "Body")
	if r := e.send(admin, id, 5); r.Status != 303 {
		t.Fatalf("send: %d", r.Status)
	}
	e.deliver()
	if got := smtp.drain(); len(got) != 1 || rcptOf(got[0]) != "ok@example.com" {
		t.Fatalf("only ok@ can have received it on the first pass: %d", len(got))
	}
	// a refusal by the server is final; a "try later" is kept for another go
	if got := e.sql(`SELECT status || ':' || attempts FROM deliveries d JOIN subscribers s ON s.id = d.subscriber_id WHERE s.email = 'gone@example.com'`); got != "failed:1" {
		t.Errorf("a refused address is given up on at once: %s", got)
	}
	if got := e.sql(`SELECT last_error FROM deliveries d JOIN subscribers s ON s.id = d.subscriber_id WHERE s.email = 'gone@example.com'`); !strings.Contains(got, "no such mailbox") {
		t.Errorf("the reason is kept: %q", got)
	}
	if got := e.sql(`SELECT status || ':' || attempts FROM deliveries d JOIN subscribers s ON s.id = d.subscriber_id WHERE s.email = 'busy@example.com'`); got != "pending:1" {
		t.Errorf("a temporary failure waits: %s", got)
	}
	if e.sql(`SELECT status FROM campaigns WHERE id = `+id) != "sending" {
		t.Error("still sending while some are waiting to be retried")
	}

	// the server recovers for one of them; the others keep failing until they run out of attempts
	smtp.refuse("later@", "")
	smtp.refuse("busy@", "")
	for i := 0; i < 12 && e.sql(`SELECT status FROM campaigns WHERE id = `+id) == "sending"; i++ {
		time.Sleep(300 * time.Millisecond)
		e.deliver()
	}
	got := map[string]bool{}
	for _, m := range smtp.drain() {
		got[rcptOf(m)] = true
	}
	if !got["busy@example.com"] || !got["later@example.com"] || got["never@example.com"] || got["gone@example.com"] {
		t.Errorf("after recovery: %v", got)
	}
	if e.sql(`SELECT status FROM campaigns WHERE id = `+id) != "sent" {
		t.Fatalf("status %s", e.sql(`SELECT status FROM campaigns WHERE id = `+id))
	}
	if got := e.sql(`SELECT status || ':' || attempts FROM deliveries d JOIN subscribers s ON s.id = d.subscriber_id WHERE s.email = 'never@example.com'`); got != "failed:3" {
		t.Errorf("three attempts, then given up: %s", got)
	}

	// the page says who it could not reach, and why
	page := admin.get("/admin/newsletters/" + id).Body
	for _, want := range []string{"Not delivered", "gone@example.com", "never@example.com", "no such mailbox"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(page, "2 could not be delivered") || !strings.Contains(page, "5 of 5") {
		t.Errorf("counts: %s", page[strings.Index(page, "<strong>"):])
	}
	if strings.Contains(page, "busy@example.com") {
		t.Error("an address that worked on a retry is not a failure")
	}
	if row := admin.get("/admin/newsletters").Body; !strings.Contains(row, "3 of 5") || !strings.Contains(row, "2 failed") {
		t.Errorf("the list shows how it went:\n%s", row)
	}
}

func TestNewsletterUnsubscribingMidSendAndCanceling(t *testing.T) {
	e, admin, smtp := mailEnv(t, true)
	for _, a := range []string{"a@example.com", "b@example.com", "c@example.com", "d@example.com"} {
		e.subscribe(a, true)
	}
	id := e.newsletter(admin, "News", "Body")
	e.send(admin, id, 4)

	// one person leaves before their turn: they get nothing, and the rest are unaffected
	e.pool.Exec(context.Background(), `DELETE FROM subscribers WHERE email = 'a@example.com'`)
	// one message goes, then the owner stops it
	worked, err := e.app.Campaigns().Tick(context.Background())
	if err != nil || !worked {
		t.Fatalf("tick: %v %v", worked, err)
	}
	if r := admin.post("/admin/newsletters/"+id+"/cancel", nil); r.Status != 303 {
		t.Fatalf("cancel: %d", r.Status)
	}
	if e.sql(`SELECT status FROM campaigns WHERE id = `+id) != "canceled" {
		t.Error("status should be canceled")
	}
	if n := e.deliver(); n != 0 {
		t.Errorf("a canceled newsletter sends nothing more, sent %d", n)
	}
	got := smtp.drain()
	if len(got) != 1 {
		t.Errorf("exactly one message went out before it was stopped: %d", len(got))
	}
	for _, m := range got {
		if rcptOf(m) == "a@example.com" {
			t.Error("a@ unsubscribed before their turn")
		}
	}
	page := admin.get("/admin/newsletters/" + id)
	if !strings.Contains(page.Body, "canceled") || !strings.Contains(page.Body, "Stopped") || strings.Contains(page.Body, "Stop sending") {
		t.Errorf("canceled page:\n%s", page.Body)
	}
	// stopping again is harmless, and a draft can't be "stopped"
	if r := admin.post("/admin/newsletters/"+id+"/cancel", nil); r.Status != 303 {
		t.Errorf("cancel twice: %d", r.Status)
	}
	if r := admin.post("/admin/newsletters/999999/cancel", nil); r.Status != 404 {
		t.Errorf("cancel missing: %d", r.Status)
	}
	if r := admin.post("/admin/newsletters/"+id+"/delete", nil); r.Status != 303 {
		t.Errorf("a canceled newsletter can be deleted: %d", r.Status)
	}

	// everyone leaves before it starts: it has nobody to send to
	e.pool.Exec(context.Background(), `DELETE FROM subscribers`)
	id2 := e.newsletter(admin, "Empty", "Nobody home")
	if r := e.send(admin, id2, 1); r.Status != 400 || !strings.Contains(r.Body, "Nobody is subscribed") {
		t.Errorf("nobody on the list: %d", r.Status)
	}
	if e.sql(`SELECT status FROM campaigns WHERE id = `+id2) != "draft" {
		t.Error("it stays a draft")
	}
}

func TestNewsletterNeedsEmailAndADomain(t *testing.T) {
	// no email set up at all
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	e.subscribe("ann@example.com", true)
	e.pool.Exec(context.Background(), `UPDATE site_settings SET domain = 'example.com'`)
	if page := admin.get("/admin/newsletters"); !strings.Contains(page.Body, "Email isn't set up") {
		t.Error("the screen should say email isn't set up")
	}
	id := e.newsletter(admin, "News", "Body")
	if page := admin.get("/admin/newsletters/" + id); !strings.Contains(page.Body, "SMTP_HOST") || !strings.Contains(page.Body, "disabled") {
		t.Error("sending is switched off, and the page says why")
	}
	if r := e.send(admin, id, 1); r.Status != 400 || !strings.Contains(r.Body, "Email isn") {
		t.Errorf("no email: %d", r.Status)
	}
	if r := admin.post("/admin/newsletters/"+id+"/test", nil); r.Status != 400 {
		t.Errorf("no email, test: %d", r.Status)
	}
	if e.sql(`SELECT status FROM campaigns WHERE id = `+id) != "draft" || e.sql(`SELECT count(*) FROM deliveries`) != "0" {
		t.Error("nothing is queued without email")
	}

	// email, but no saved domain: the unsubscribe link couldn't point at the real site
	e2, admin2, _ := mailEnv(t, false)
	e2.subscribe("ann@example.com", true)
	id = e2.newsletter(admin2, "News", "Body")
	if page := admin2.get("/admin/newsletters/" + id); !strings.Contains(page.Body, "Domain") {
		t.Error("the page should send them to the Domain page")
	}
	if r := e2.send(admin2, id, 1); r.Status != 400 || !strings.Contains(r.Body, "Domain page") {
		t.Errorf("no domain: %d", r.Status)
	}
	if e2.sql(`SELECT count(*) FROM deliveries`) != "0" {
		t.Error("nothing is queued without a domain")
	}
}

func TestNewsletterInputIsCleanedAndEscaped(t *testing.T) {
	e, admin, smtp := mailEnv(t, true)
	e.subscribe("ann@example.com", true)

	for name, v := range map[string]url.Values{
		"no subject":    {"subject": {""}, "body": {"x"}},
		"blank subject": {"subject": {"   "}, "body": {"x"}},
		"no body":       {"subject": {"x"}, "body": {"  \n "}},
		"long subject":  {"subject": {strings.Repeat("s", 151)}, "body": {"x"}},
		"long body":     {"subject": {"x"}, "body": {strings.Repeat("b", 20001)}},
	} {
		if r := admin.post("/admin/newsletters", v); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if e.sql(`SELECT count(*) FROM campaigns`) != "0" {
		t.Error("rejected drafts are not saved")
	}
	// what was typed comes back with the complaint
	if r := admin.post("/admin/newsletters", url.Values{"subject": {"Kept"}, "body": {""}}); !strings.Contains(r.Body, `value="Kept"`) {
		t.Error("the subject should be kept")
	}

	// a subject can't add mail headers; markup in the text stays text
	id := e.newsletter(admin, "Hello\r\nBcc: evil@example.net", "Dear <b>friend</b> & \"reader\"\x00\x07")
	if got := e.sql(`SELECT subject FROM campaigns WHERE id = ` + id); strings.ContainsAny(got, "\r\n") {
		t.Errorf("subject kept a line break: %q", got)
	}
	page := admin.get("/admin/newsletters/" + id).Body
	if strings.Contains(page, "<b>friend</b>") || !strings.Contains(page, "&lt;b&gt;friend&lt;/b&gt;") {
		t.Error("the body must be escaped in the admin")
	}
	e.send(admin, id, 1)
	e.deliver()
	got := smtp.drain()
	if len(got) != 1 {
		t.Fatalf("%d emails", len(got))
	}
	head, body, _ := strings.Cut(got[0], "\r\n\r\n")
	if strings.Contains(strings.ToLower(head), "\r\nbcc:") {
		t.Errorf("a header was injected:\n%s", head)
	}
	if !strings.Contains(body, "Dear <b>friend</b> & \"reader\"") || strings.ContainsAny(body, "\x00\x07") {
		t.Errorf("body should be plain text without control characters:\n%q", body)
	}
}

func TestNewsletterAccessAndCSRF(t *testing.T) {
	e, admin, _ := mailEnv(t, true)
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	e.subscribe("ann@example.com", true)
	id := e.newsletter(admin, "News", "Body")

	for _, p := range []string{"/admin/newsletters", "/admin/newsletters/" + id, "/admin/newsletters/" + id + "/progress"} {
		if r := editor.get(p); r.Status != 403 {
			t.Errorf("an editor opened %s: %d", p, r.Status)
		}
		if r := anon.get(p); !strings.Contains(r.Location(), "/admin/login") {
			t.Errorf("a visitor at %s: %d %q", p, r.Status, r.Location())
		}
	}
	form := url.Values{"subject": {"x"}, "body": {"y"}, "recipients": {"1"}}
	for _, p := range []string{"/admin/newsletters", "/admin/newsletters/" + id, "/admin/newsletters/" + id + "/test", "/admin/newsletters/" + id + "/send", "/admin/newsletters/" + id + "/cancel", "/admin/newsletters/" + id + "/delete"} {
		if r := editor.post(p, form); r.Status != 403 {
			t.Errorf("an editor posted to %s: %d", p, r.Status)
		}
		if r := anon.post(p, form); !strings.Contains(r.Location(), "/admin/login") {
			t.Errorf("a visitor posting to %s: %d", p, r.Status)
		}
		if r := admin.postNoToken(p, form); r.Status != 403 {
			t.Errorf("no CSRF token at %s: %d", p, r.Status)
		}
	}
	if e.sql(`SELECT status FROM campaigns WHERE id = `+id) != "draft" || e.sql(`SELECT count(*) FROM deliveries`) != "0" || e.sql(`SELECT count(*) FROM campaigns`) != "1" {
		t.Error("none of those may change anything")
	}
	for _, p := range []string{"/admin/newsletters/999999", "/admin/newsletters/abc"} {
		if r := admin.get(p); r.Status != 404 {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	if r := admin.post("/admin/newsletters/999999/send", url.Values{"recipients": {"1"}}); r.Status != 404 {
		t.Errorf("send missing: %d", r.Status)
	}
}

// A message that was already sent is never sent twice by a restart: the queue is in the database.
func TestNewsletterQueueSurvivesARestart(t *testing.T) {
	e, admin, smtp := mailEnv(t, true)
	for _, a := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		e.subscribe(a, true)
	}
	id := e.newsletter(admin, "News", "Body")
	e.send(admin, id, 3)
	if worked, _ := e.app.Campaigns().Tick(context.Background()); !worked {
		t.Fatal("tick")
	}
	first := smtp.drain()
	if len(first) != 1 {
		t.Fatalf("one message first: %d", len(first))
	}
	// a message reserved by a worker that then crashed is left alone until its reservation runs out
	e.pool.Exec(context.Background(), `UPDATE deliveries SET attempts = 1, next_attempt = now() + interval '1 hour'
		WHERE status = 'pending' AND subscriber_id = (SELECT d.subscriber_id FROM deliveries d JOIN subscribers s ON s.id = d.subscriber_id
			WHERE d.status = 'pending' ORDER BY s.email LIMIT 1)`)
	e.deliver()
	rest := smtp.drain()
	if len(rest) != 1 {
		t.Fatalf("one message is reserved by a crashed worker, so only one more goes: %d", len(rest))
	}
	e.pool.Exec(context.Background(), `UPDATE deliveries SET next_attempt = now() - interval '1 second' WHERE status = 'pending'`)
	e.deliver()
	rest = append(rest, smtp.drain()...)
	seen := map[string]int{rcptOf(first[0]): 1}
	for _, m := range rest {
		seen[rcptOf(m)]++
	}
	if len(seen) != 3 {
		t.Errorf("everyone got it: %v", seen)
	}
	for who, n := range seen {
		if n != 1 {
			t.Errorf("%s got %d copies", who, n)
		}
	}
}
