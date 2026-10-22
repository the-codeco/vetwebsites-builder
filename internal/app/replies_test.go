package app_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// addMessage puts a visitor's message in the inbox and returns its id.
func (e *testEnv) addMessage(name, email, subject, body string) string {
	e.t.Helper()
	return e.sql(`INSERT INTO messages (name, email, subject, body) VALUES ($1, $2, $3, $4) RETURNING id`, name, email, subject, body)
}

func TestReplyToAContactMessage(t *testing.T) {
	e, admin, smtp := mailEnv(t, false)
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	id := e.addMessage("Vera Visitor", "vera@example.org", "Opening hours", "When are you open?\nAnd on Sundays?")

	page := admin.get("/admin/messages/" + id)
	for _, want := range []string{`action="/admin/messages/` + id + `/reply"`, `name="reply"`, "vera@example.org", "comes to <strong>admin@example.com</strong>", "mailto:vera@example.org"} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("the message page lacks %q", want)
		}
	}
	if strings.Contains(page.Body, "Replies sent") {
		t.Error("no replies yet")
	}

	// the reply goes to the visitor, and their answer comes back to the person who wrote it
	r := admin.post("/admin/messages/"+id+"/reply", url.Values{"reply": {"We open at 9.\r\n\r\nSundays too."}})
	if r.Status != 303 || !strings.Contains(r.Location(), "done=replied") {
		t.Fatalf("reply: %d %s", r.Status, firstLine(r.Body))
	}
	m := smtp.next(t)
	if rcptOf(m) != "vera@example.org" {
		t.Errorf("sent to %q", rcptOf(m))
	}
	for name, want := range map[string]string{"Subject": "Re: Opening hours", "Reply-To": "admin@example.com", "To": "vera@example.org"} {
		if got := headerOf(m, name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if !strings.Contains(headerOf(m, "From"), "noreply@example.com") {
		t.Errorf("From: %q", headerOf(m, "From"))
	}
	for _, want := range []string{"We open at 9.", "Sundays too.", "Vera Visitor wrote:", "> When are you open?", "> And on Sundays?"} {
		if !strings.Contains(m, want) {
			t.Errorf("the email lacks %q:\n%s", want, m)
		}
	}
	if strings.Contains(headerOf(m, "List-Unsubscribe"), "http") {
		t.Error("a reply is not a newsletter")
	}

	// it is kept with the message, with who wrote it
	after := admin.get("/admin/messages/" + id + "?done=replied").Body
	for _, want := range []string{"Replies sent", "We open at 9.", "admin@example.com", "Reply sent."} {
		if !strings.Contains(after, want) {
			t.Errorf("the message page lacks %q after replying", want)
		}
	}
	if e.sql(`SELECT count(*) FROM message_replies WHERE message_id = `+id) != "1" {
		t.Error("one reply stored")
	}
	if e.sql(`SELECT read FROM messages WHERE id = `+id) != "true" {
		t.Error("answering marks a message as read")
	}

	// a message that is already a reply doesn't pile up "Re: Re:", and one with no subject gets a sensible one
	id2 := e.addMessage("Vera", "vera@example.org", "re: Opening hours", "Thanks")
	admin.post("/admin/messages/"+id2+"/reply", url.Values{"reply": {"Welcome"}})
	if got := headerOf(smtp.next(t), "Subject"); got != "re: Opening hours" {
		t.Errorf("subject: %q", got)
	}
	id3 := e.addMessage("Vera", "vera@example.org", "", "Hello")
	admin.post("/admin/messages/"+id3+"/reply", url.Values{"reply": {"Hi"}})
	if got := headerOf(smtp.next(t), "Subject"); got != "Re: your message" {
		t.Errorf("subject: %q", got)
	}

	// editors can answer too (they read the inbox); visitors and forged requests can't
	if r := editor.post("/admin/messages/"+id+"/reply", url.Values{"reply": {"From the editor"}}); r.Status != 303 {
		t.Errorf("editor: %d", r.Status)
	}
	if got := headerOf(smtp.next(t), "Reply-To"); got != "ed@example.com" {
		t.Errorf("replies go to whoever wrote the answer: %q", got)
	}
	before := e.sql(`SELECT count(*) FROM message_replies`)
	if r := anon.post("/admin/messages/"+id+"/reply", url.Values{"reply": {"x"}}); !strings.Contains(r.Location(), "/admin/login") {
		t.Errorf("visitor: %d", r.Status)
	}
	if r := admin.postNoToken("/admin/messages/"+id+"/reply", url.Values{"reply": {"x"}}); r.Status != 403 {
		t.Errorf("no token: %d", r.Status)
	}
	if e.sql(`SELECT count(*) FROM message_replies`) != before || len(smtp.drain()) != 0 {
		t.Error("none of those may send anything")
	}
	for _, p := range []string{"/admin/messages/999999/reply", "/admin/messages/abc/reply"} {
		if r := admin.post(p, url.Values{"reply": {"x"}}); r.Status != 404 {
			t.Errorf("%s: %d", p, r.Status)
		}
	}
}

func TestReplyProblemsAreExplainedAndNothingIsKept(t *testing.T) {
	e, admin, smtp := mailEnv(t, false)
	id := e.addMessage("Vera", "vera@example.org", "Hi", "Hello there")
	count := func() string { return e.sql(`SELECT count(*) FROM message_replies`) }

	for name, v := range map[string]string{"empty": "", "blank": "  \n\t ", "too long": strings.Repeat("r", 5001)} {
		r := admin.post("/admin/messages/"+id+"/reply", url.Values{"reply": {v}})
		if r.Status != 400 || !strings.Contains(r.Body, `name="reply"`) {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if r := admin.post("/admin/messages/"+id+"/reply", url.Values{"reply": {strings.Repeat("r", 5001)}}); !strings.Contains(r.Body, strings.Repeat("r", 100)) {
		t.Error("what was typed should come back")
	}

	// the mail server refuses the visitor's address: the owner is told, and no reply is recorded
	smtp.refuse("vera@", "550 no such mailbox")
	if r := admin.post("/admin/messages/"+id+"/reply", url.Values{"reply": {"Hello"}}); r.Status != 502 || !strings.Contains(r.Body, "nothing was saved") || !strings.Contains(r.Body, `name="reply"`) {
		t.Errorf("refused: %d", r.Status)
	}
	smtp.refuse("vera@", "")
	if count() != "0" || len(smtp.drain()) != 0 {
		t.Error("a reply that wasn't sent must not be recorded")
	}

	// no more than a few dozen an hour from one person
	for i := 0; i < 30; i++ {
		e.pool.Exec(context.Background(), `INSERT INTO message_replies (message_id, user_id, body) SELECT $1, id, 'x' FROM users LIMIT 1`, id)
	}
	if r := admin.post("/admin/messages/"+id+"/reply", url.Values{"reply": {"One too many"}}); r.Status != 429 {
		t.Errorf("rate limit: %d", r.Status)
	}
	e.pool.Exec(context.Background(), `UPDATE message_replies SET created_at = now() - interval '2 hours'`)
	if r := admin.post("/admin/messages/"+id+"/reply", url.Values{"reply": {"An hour later"}}); r.Status != 303 {
		t.Errorf("after the hour: %d", r.Status)
	}

	// deleting the message deletes its replies
	admin.post("/admin/messages/"+id+"/delete", nil)
	if count() != "0" {
		t.Error("replies go with their message")
	}
}

func TestReplyWithoutEmailSetUp(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	id := e.addMessage("Vera", "vera@example.org", "Hi", "Hello there")
	page := admin.get("/admin/messages/" + id)
	if strings.Contains(page.Body, `name="reply"`) || !strings.Contains(page.Body, "SMTP_HOST") || !strings.Contains(page.Body, "mailto:vera@example.org") {
		t.Error("without email the page offers the mail-program link and explains why")
	}
	if r := admin.post("/admin/messages/"+id+"/reply", url.Values{"reply": {"Hello"}}); r.Status != 400 || !strings.Contains(r.Body, "Email isn") {
		t.Errorf("post: %d", r.Status)
	}
	if e.sql(`SELECT count(*) FROM message_replies`) != "0" {
		t.Error("nothing is recorded")
	}
}
