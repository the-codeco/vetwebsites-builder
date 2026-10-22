package app_test

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"webbuilder/internal/config"
)

var (
	reFormToken = regexp.MustCompile(`name="t" value="([^"]+)"`)
	reBlockID   = regexp.MustCompile(`name="block" value="(\d+)"`)
)

// fakeSMTP accepts mail the way a real server would and hands each message to a channel.
type fakeSMTP struct {
	addr string
	got  chan string

	mu     sync.Mutex
	reject map[string]string // part of a RCPT line -> the whole reply, e.g. "550 no such user"
}

// refuse makes the server answer "reply" when asked to deliver to an address containing part.
func (f *fakeSMTP) refuse(part, reply string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reject == nil {
		f.reject = map[string]string{}
	}
	if reply == "" {
		delete(f.reject, part)
	} else {
		f.reject[part] = reply
	}
}

func (f *fakeSMTP) refusal(rcpt string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for part, reply := range f.reject {
		if strings.Contains(strings.ToLower(rcpt), part) {
			return reply
		}
	}
	return ""
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeSMTP{addr: ln.Addr().String(), got: make(chan string, 200)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				say := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
				say("220 fake ESMTP")
				var envelope []string
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
						say("250 fake")
					case strings.HasPrefix(cmd, "RCPT") && f.refusal(line) != "":
						say(f.refusal(line))
					case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
						envelope = append(envelope, strings.TrimSpace(line))
						say("250 ok")
					case cmd == "DATA":
						say("354 go ahead")
						var data strings.Builder
						for {
							l, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if l == ".\r\n" {
								break
							}
							data.WriteString(l)
						}
						say("250 queued")
						f.got <- strings.Join(envelope, "\n") + "\n\n" + data.String()
						envelope = nil
					case cmd == "QUIT":
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}()
		}
	}()
	return f
}

func (f *fakeSMTP) next(t *testing.T) string {
	t.Helper()
	select {
	case m := <-f.got:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no email arrived")
		return ""
	}
}

// contactPage creates a published page holding a contact form block and returns its path.
func contactPage(t *testing.T, admin *client) string {
	t.Helper()
	id := newPage(t, admin, "Contact")
	if r := admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"contactform"}}); r.Status != 200 {
		t.Fatalf("add block: %d", r.Status)
	}
	savePage(t, admin, id, url.Values{"title": {"Contact"}, "slug": {"contact"}})
	return "/contact"
}

func TestContactFormEndToEnd(t *testing.T) {
	smtp := startFakeSMTP(t)
	host, port, _ := net.SplitHostPort(smtp.addr)
	e := newEnv(t, func(c *config.Config) {
		c.SMTPHost, c.SMTPPort, c.SMTPFrom = host, port, "Site <noreply@example.com>"
	})
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	page := contactPage(t, admin)

	// the form renders with a token, a hidden trap field, and no script
	body := anon.get(page).Body
	tok := scrape(reFormToken, body)
	blockID := scrape(reBlockID, body)
	for _, want := range []string{`<form class="contact-form"`, `action="/contact"`, `name="website"`, `type="email"`, `required`, `Get in touch`} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %s", want)
		}
	}
	if tok == "" || blockID == "" {
		t.Fatal("form has no token or block id")
	}
	if extra := foreignScripts(body); len(extra) != 0 {
		t.Errorf("the form added scripts: %v", extra)
	}
	form := func(over map[string]string) url.Values {
		v := url.Values{"t": {tok}, "from": {"/contact"}, "block": {blockID}, "name": {"Ada Lovelace"},
			"email": {"ada@example.com"}, "subject": {"Hello"}, "message": {"I would like to talk."}}
		for k, val := range over {
			v.Set(k, val)
		}
		return v
	}
	count := func() string { return e.sql(`SELECT count(*) FROM messages`) }

	// too fast: a bot posts the form the instant it loads
	if r := anon.post("/contact", form(nil)); r.Status != 400 || !strings.Contains(r.Body, "Message not sent") || !strings.Contains(r.Body, "too quickly") {
		t.Errorf("instant post: %d", r.Status)
	}
	time.Sleep(3200 * time.Millisecond)

	// cross-site posts are refused outright
	if r := anon.post("/contact", form(nil), "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"); r.Status != 403 {
		t.Errorf("cross-site post: %d", r.Status)
	}
	// the trap field: looks like success, stores nothing
	if r := anon.post("/contact", form(map[string]string{"website": "http://spam.example"})); r.Status != 303 || count() != "0" {
		t.Errorf("honeypot: %d, %s rows", r.Status, count())
	}
	for name, over := range map[string]map[string]string{
		"forged token": {"t": "1.abc"}, "no name": {"name": " "}, "bad email": {"email": "not-an-email"},
		"empty message": {"message": "  "}, "huge message": {"message": strings.Repeat("x", 5001)},
	} {
		if r := anon.post("/contact", form(over)); r.Status != 400 || !strings.Contains(r.Body, "Message not sent") {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if count() != "0" {
		t.Fatalf("an invalid message was stored: %s", count())
	}
	if r := anon.post("/contact", form(map[string]string{"name": `<b>x</b>`, "email": "bad"})); strings.Contains(r.Body, "<b>x</b>") {
		t.Error("the error page echoed unescaped input")
	}

	// a good message, with email switched on
	admin.post("/admin/messages/settings", url.Values{"notify_email": {"owner@example.com"}})
	r := anon.post("/contact", form(map[string]string{
		"name": "Ada\r\nBcc: evil@example.com", "message": "<script>alert(1)</script> I would like to talk.\nSecond line.",
	}))
	if r.Status != 303 || r.Location() != "/contact?sent="+blockID+"#contact-"+blockID {
		t.Fatalf("send: %d %q", r.Status, r.Location())
	}
	if count() != "1" {
		t.Fatalf("message not stored: %s", count())
	}
	if got := e.sql(`SELECT name FROM messages`); got != "Ada Bcc: evil@example.com" {
		t.Errorf("name should be flattened to one line, got %q", got)
	}
	thanks := anon.get("/contact?sent=" + blockID).Body
	if !strings.Contains(thanks, `class="form-notice"`) || strings.Contains(thanks, `<form class="contact-form"`) {
		t.Error("after sending, the page should thank the visitor instead of showing the form")
	}
	if strings.Contains(anon.get("/contact?sent=99999").Body, `class="form-notice"`) {
		t.Error("another block's id must not show the thank-you")
	}

	mail := smtp.next(t)
	for _, want := range []string{"RCPT TO:<owner@example.com>", "To: owner@example.com", "Reply-To: ada@example.com", "I would like to talk."} {
		if !strings.Contains(mail, want) {
			t.Errorf("email lacks %q:\n%s", want, mail)
		}
	}
	if strings.Contains(strings.ToLower(strings.SplitN(mail, "\r\n\r\n", 2)[0]), "\r\nbcc:") {
		t.Errorf("a visitor injected an email header:\n%s", mail)
	}

	// redirects only ever go to pages on this site
	for _, evil := range []string{"//evil.example", "https://evil.example", "/a/b", "javascript:alert(1)", "/x?y=1"} {
		r := anon.post("/contact", form(map[string]string{"from": evil, "website": "x"}))
		if loc := r.Location(); !strings.HasPrefix(loc, "/?sent=") {
			t.Errorf("from=%q redirected to %q", evil, loc)
		}
	}

	// the inbox: unread badge, reading, unread again, deleting; script text stays text
	if inbox := admin.get("/admin/messages").Body; !strings.Contains(inbox, "Ada Bcc: evil@example.com") || !strings.Contains(inbox, `class="dot"`) || !strings.Contains(inbox, `class="badge-count"`) {
		t.Error("inbox should list the unread message with a badge")
	}
	if strings.Contains(admin.get("/admin/messages").Body, "<script>alert(1)") {
		t.Error("message text was not escaped in the inbox")
	}
	id := e.sql(`SELECT id FROM messages`)
	view := admin.get("/admin/messages/" + id)
	if view.Status != 200 || !strings.Contains(view.Body, "&lt;script&gt;alert(1)&lt;/script&gt; I would like to talk.") || !strings.Contains(view.Body, `href="mailto:ada@example.com?subject=Re:%20Hello"`) {
		t.Errorf("message view: %d", view.Status)
	}
	if strings.Contains(view.Body, "<script>alert(1)") {
		t.Error("message text was not escaped in the message view")
	}
	if e.sql(`SELECT read FROM messages`) != "true" || strings.Contains(admin.get("/admin/").Body, `class="badge-count"`) {
		t.Error("opening a message should mark it read and clear the badge")
	}
	admin.post("/admin/messages/"+id+"/unread", nil)
	if e.sql(`SELECT read FROM messages`) != "false" || !strings.Contains(admin.get("/admin/").Body, `<span class="badge-count" aria-label="1 unread">1</span>`) {
		t.Error("mark as unread")
	}

	// who may do what
	if r := anon.get("/admin/messages"); r.Status == 200 {
		t.Error("the inbox is open to visitors")
	}
	if r := editor.get("/admin/messages/" + id); r.Status != 200 {
		t.Errorf("editors read messages: %d", r.Status)
	}
	if r := editor.post("/admin/messages/settings", url.Values{"notify_email": {"me@example.com"}}); r.Status != 403 {
		t.Errorf("editor changed notification settings: %d", r.Status)
	}
	if r := admin.post("/admin/messages/settings", url.Values{"notify_email": {"bad address"}}); r.Status != 400 {
		t.Errorf("bad notification address: %d", r.Status)
	}
	if got := e.sql(`SELECT notify_email FROM site_settings`); got != "owner@example.com" {
		t.Errorf("notify email: %q", got)
	}
	if r := admin.get("/admin/messages/999999"); r.Status != 404 {
		t.Errorf("missing message: %d", r.Status)
	}
	if r := editor.post("/admin/messages/"+id+"/delete", nil); r.Status != 303 || count() != "0" {
		t.Errorf("delete: %d, %s left", r.Status, count())
	}
}

func TestContactFormRateLimit(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	page := contactPage(t, admin)
	tok := scrape(reFormToken, anon.get(page).Body)
	time.Sleep(3200 * time.Millisecond)

	send := func() int {
		return anon.post("/contact", url.Values{"t": {tok}, "from": {"/contact"}, "block": {"1"}, "name": {"A"},
			"email": {"a@example.com"}, "message": {"hi"}}).Status
	}
	for i := 1; i <= 5; i++ {
		if got := send(); got != 303 {
			t.Fatalf("message %d: %d", i, got)
		}
	}
	if got := send(); got != 429 {
		t.Errorf("the sixth message in an hour should be refused, got %d", got)
	}
	if got := e.sql(`SELECT count(*) FROM messages`); got != "5" {
		t.Errorf("stored %s messages", got)
	}
}

func TestContactFormWithoutEmailSetup(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	if body := admin.get("/admin/messages").Body; !strings.Contains(body, "Email isn't set up") || !strings.Contains(body, "No messages yet") {
		t.Error("the inbox should explain how email works and that it is empty")
	}
	// a block can be added to a page and edited like any other
	id := newPage(t, admin, "Talk")
	r := admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"contactform"}})
	blk := e.blockID(id, "contactform")
	if r.Status != 200 || blk == "" {
		t.Fatalf("add: %d", r.Status)
	}
	if r := admin.post("/admin/blocks/"+blk, url.Values{"p.heading": {"Write to us"}, "p.button_label": {"Go"}}); r.Status != 200 {
		t.Fatalf("save block: %d", r.Status)
	}
	savePage(t, admin, id, url.Values{"title": {"Talk"}, "slug": {"talk"}})
	page := e.client().get("/talk").Body
	if !strings.Contains(page, "Write to us") || !strings.Contains(page, ">Go</button>") || strings.Contains(page, `name="subject"`) {
		t.Error("block settings (heading, button, subject toggle) should apply")
	}
}
