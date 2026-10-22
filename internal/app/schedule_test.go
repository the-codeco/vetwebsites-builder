package app_test

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func iso(d time.Duration) string {
	return time.Now().UTC().Add(d).Truncate(time.Minute).Format(time.RFC3339)
}

func (e *testEnv) publishDue() (int, int) {
	e.t.Helper()
	p, q, err := e.app.PublishDue(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	return p, q
}

func TestPagesCanBeScheduled(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	id := newPage(t, admin, "Launch")
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"richtext"}})
	admin.post("/admin/blocks/"+e.blockID(id, "richtext"), url.Values{"p.content": {"<p>Big announcement</p>"}})

	editorPage := admin.get("/admin/pages/" + id).Body
	for _, want := range []string{`data-schedule`, `type="datetime-local"`, `name="publish_local"`, `name="publish_at"`} {
		if !strings.Contains(editorPage, want) {
			t.Errorf("the editor lacks %s", want)
		}
	}
	save := func(c *client, v url.Values) response {
		v.Set("title", "Launch")
		v.Set("slug", "launch")
		return c.post("/admin/pages/"+id, v)
	}
	// schedule it for an hour from now
	when := iso(time.Hour)
	if r := save(editor, url.Values{"status": {"draft"}, "publish_at": {when}}); r.Status != 303 {
		t.Fatalf("schedule: %d %s", r.Status, firstLine(r.Body))
	}
	if got := e.sql(`SELECT status||'|'||to_char(publish_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM pages WHERE id = ` + id); got != "draft|"+when {
		t.Errorf("stored: %s", got)
	}
	page := admin.get("/admin/pages/" + id).Body
	if !strings.Contains(page, "goes live") || !strings.Contains(page, `data-utc="`+when+`"`) || !strings.Contains(page, `value="`+when+`"`) {
		t.Errorf("the editor shows the schedule:\n%s", page)
	}
	if !strings.Contains(admin.get("/admin/pages").Body, "goes live") {
		t.Error("the page list shows it")
	}
	if r := anon.get("/launch"); r.Status != 404 {
		t.Errorf("not public yet: %d", r.Status)
	}
	if p, q := e.publishDue(); p != 0 || q != 0 {
		t.Errorf("nothing is due yet: %d %d", p, q)
	}
	// the time comes
	e.pool.Exec(t.Context(), `UPDATE pages SET publish_at = now() - interval '1 minute' WHERE id = `+id)
	if p, q := e.publishDue(); p != 1 || q != 0 {
		t.Errorf("one page is due: %d %d", p, q)
	}
	if got := e.sql(`SELECT status||'|'||COALESCE(publish_at::text, 'none') FROM pages WHERE id = ` + id); got != "published|none" {
		t.Errorf("published, and the schedule is used up: %s", got)
	}
	if r := anon.get("/launch"); r.Status != 200 || !strings.Contains(r.Body, "Big announcement") {
		t.Errorf("public now: %d", r.Status)
	}
	if p, q := e.publishDue(); p != 0 || q != 0 {
		t.Errorf("only once: %d %d", p, q)
	}

	// a schedule only counts while the page is a draft
	save(admin, url.Values{"status": {"draft"}, "publish_at": {iso(2 * time.Hour)}})
	save(admin, url.Values{"status": {"published"}, "publish_at": {iso(2 * time.Hour)}})
	if got := e.sql(`SELECT COALESCE(publish_at::text, 'none') FROM pages WHERE id = ` + id); got != "none" {
		t.Errorf("publishing now clears the schedule: %s", got)
	}
	save(admin, url.Values{"status": {"draft"}, "publish_at": {iso(2 * time.Hour)}})
	save(admin, url.Values{"status": {"draft"}})
	if got := e.sql(`SELECT COALESCE(publish_at::text, 'none') FROM pages WHERE id = ` + id); got != "none" {
		t.Errorf("an empty time clears it: %s", got)
	}
	// without the script the typed time is read as UTC
	typed := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Minute)
	if r := save(admin, url.Values{"status": {"draft"}, "publish_local": {typed.Format("2006-01-02T15:04")}}); r.Status != 303 {
		t.Fatalf("typed time: %d", r.Status)
	}
	if got := e.sql(`SELECT publish_at = '` + typed.Format(time.RFC3339) + `' FROM pages WHERE id = ` + id); got != "true" {
		t.Errorf("the typed time is UTC: %s", got)
	}

	// refusals
	for name, v := range map[string]url.Values{
		"in the past": {"publish_at": {iso(-time.Hour)}}, "now": {"publish_at": {iso(0)}}, "three years": {"publish_at": {iso(3 * 366 * 24 * time.Hour)}},
		"not a time": {"publish_at": {"tomorrow"}}, "date only": {"publish_at": {"2030-01-01"}}, "script": {"publish_at": {"<script>"}}, "bad typed": {"publish_local": {"2030-13-45T99:99"}},
	} {
		v.Set("status", "draft")
		r := save(admin, v)
		if r.Status != 400 || !strings.Contains(r.Body, "publish") {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if got := e.sql(`SELECT publish_at = '` + typed.Format(time.RFC3339) + `' FROM pages WHERE id = ` + id); got != "true" {
		t.Error("refused saves left the schedule alone")
	}
	// scheduling does not change the address or publish a page that is being edited as published
	if r := anon.get("/launch"); r.Status != 404 {
		t.Errorf("still a draft: %d", r.Status)
	}
}

func TestPostsCanBeScheduled(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	pid := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {"Coming soon"}}).Location())
	if body := admin.get("/admin/posts/" + pid).Body; !strings.Contains(body, `data-schedule`) || !strings.Contains(body, `name="publish_at"`) {
		t.Error("the post editor has the schedule field")
	}
	when := iso(90 * time.Minute)
	if r := admin.post("/admin/posts/"+pid, url.Values{"title": {"Coming soon"}, "slug": {"coming-soon"}, "status": {"draft"}, "body": {"Words"}, "publish_at": {when}}); r.Status != 303 {
		t.Fatalf("schedule: %d", r.Status)
	}
	if !strings.Contains(admin.get("/admin/posts").Body, "goes live") {
		t.Error("the post list shows it")
	}
	if r := anon.get("/blog/coming-soon"); r.Status != 404 {
		t.Errorf("not public yet: %d", r.Status)
	}
	e.pool.Exec(t.Context(), `UPDATE posts SET publish_at = now() - interval '2 hours'`)
	if p, q := e.publishDue(); p != 0 || q != 1 {
		t.Errorf("one post is due: %d %d", p, q)
	}
	if got := e.sql(`SELECT status||'|'||COALESCE(publish_at::text, 'none')||'|'||(published_at < now() - interval '1 hour')::text FROM posts`); got != "published|none|true" {
		t.Errorf("published with the scheduled time as its date: %s", got)
	}
	if r := anon.get("/blog/coming-soon"); r.Status != 200 || !strings.Contains(r.Body, "Words") {
		t.Errorf("public now: %d", r.Status)
	}
	if r := anon.get("/rss.xml"); !strings.Contains(r.Body, "Coming soon") {
		t.Error("in the feed")
	}
	// the same rules as pages
	if r := admin.post("/admin/posts/"+pid, url.Values{"title": {"Coming soon"}, "slug": {"coming-soon"}, "status": {"draft"}, "body": {"Words"}, "publish_at": {iso(-time.Hour)}}); r.Status != 400 {
		t.Errorf("past: %d", r.Status)
	}
	// a post being published by hand has no schedule
	admin.post("/admin/posts/"+pid, url.Values{"title": {"Coming soon"}, "slug": {"coming-soon"}, "status": {"published"}, "body": {"Words"}, "publish_at": {iso(time.Hour)}})
	if got := e.sql(`SELECT COALESCE(publish_at::text, 'none') FROM posts`); got != "none" {
		t.Errorf("no schedule on a published post: %s", got)
	}
}

func TestScheduledPagesAreNotPublishedEarlyOrTwice(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	a := newPage(t, admin, "Later")
	b := newPage(t, admin, "Soon")
	c := newPage(t, admin, "Unscheduled")
	e.pool.Exec(t.Context(), `UPDATE pages SET publish_at = now() + interval '1 hour' WHERE id = `+a)
	e.pool.Exec(t.Context(), `UPDATE pages SET publish_at = now() - interval '1 second' WHERE id = `+b)
	// a page already published keeps its (stale) schedule out of the way: only drafts are touched
	e.pool.Exec(t.Context(), `UPDATE pages SET status = 'published', publish_at = now() - interval '1 hour' WHERE id = `+c)
	if p, _ := e.publishDue(); p != 1 {
		t.Errorf("only the draft that is due: %d", p)
	}
	if got := e.sql(`SELECT string_agg(title||':'||status, ',' ORDER BY title) FROM pages WHERE id IN (` + a + `,` + b + `,` + c + `)`); got != "Later:draft,Soon:published,Unscheduled:published" {
		t.Errorf("states: %s", got)
	}
}
