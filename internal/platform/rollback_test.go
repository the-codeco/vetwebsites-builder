package platform_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/publish/fakehost"
)

// waitFor polls until a condition holds (the fake hosting finishes a removal or a write a moment after it is asked).
func waitFor(cond func() bool) bool {
	for i := 0; i < 50; i++ {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cond()
}

// A publish that went wrong can be undone: an earlier copy of the website goes back on the hosting, what the owner has
// written since stays in the editor, and nothing else on the hosting is touched.
func TestRollbackABadPublish(t *testing.T) {
	e := newEnv(t, withKey)
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	h := fakehost.StartSFTP(t)
	newPage(t, ann, "About us", "about-us", "<p>We fix teeth</p>")
	newPage(t, ann, "Services", "services", "<p>Cleaning</p>")
	if r := ann.post("/admin/hosting", hostingForm(h, "sftp", "http://127.0.0.1:9/")); r.Status != 303 {
		t.Fatalf("save hosting: %d", r.Status)
	}
	if r := ann.get("/admin/publish"); strings.Contains(r.Body, `name="job"`) {
		t.Error("nothing to go back to before the first publish")
	}
	if st, _ := e.publishAndWait(ann, false); st != "done" {
		t.Fatalf("first publish: %s", e.scalar(`SELECT error FROM publish_jobs ORDER BY id DESC LIMIT 1`))
	}
	first := e.scalar(`SELECT id FROM publish_jobs ORDER BY id DESC LIMIT 1`)
	if r := ann.get("/admin/publish"); strings.Contains(r.Body, `name="job"`) {
		t.Error("the version on the hosting now is not offered")
	}
	h.WriteForeign(t, "my-own-file.txt", "not ours")

	// a bad publish: the About page is deleted and the Services page is rewritten
	aid := e.scalar(`SELECT id FROM site_1.pages WHERE slug = 'about-us'`)
	ann.post("/admin/pages/"+aid+"/delete", nil)
	newPage(t, ann, "Oops", "oops", "<p>Not ready</p>")
	if st, _ := e.publishAndWait(ann, false); st != "done" {
		t.Fatalf("second publish: %s", e.scalar(`SELECT error FROM publish_jobs ORDER BY id DESC LIMIT 1`))
	}
	if !waitFor(func() bool { return !h.Exists("about-us.html") && h.Exists("oops.html") }) {
		t.Fatal("the second publish did not reach the hosting")
	}
	second := e.scalar(`SELECT id FROM publish_jobs ORDER BY id DESC LIMIT 1`)

	r := ann.get("/admin/publish")
	if !strings.Contains(r.Body, `name="job"`) || !strings.Contains(r.Body, `name="job" value="`+first+`"`) || strings.Contains(r.Body, `name="job" value="`+second+`"`) {
		t.Fatalf("the first publish can be put back, the current one cannot:\n%s", r.Body)
	}
	// the current version, a missing one and somebody else's cannot be put back
	for _, job := range []string{second, "999999", "abc"} {
		if r := ann.post("/admin/publish/rollback", url.Values{"job": {job}}); r.Status == 303 {
			t.Errorf("rolled back to job %q", job)
		}
	}
	if e.scalar(`SELECT count(*) FROM publish_jobs WHERE restore_version <> ''`) != "0" {
		t.Error("a refused rollback left a job")
	}

	// go back
	if r := ann.post("/admin/publish/rollback", url.Values{"job": {first}}); r.Status != 303 {
		t.Fatalf("rollback: %d %s", r.Status, firstLine(r.Body))
	}
	var status string
	if !waitFor(func() bool {
		status = e.scalar(`SELECT status FROM publish_jobs ORDER BY id DESC LIMIT 1`)
		return status == "done" || status == "failed"
	}) || status != "done" {
		t.Fatalf("rollback: %s: %s", status, e.scalar(`SELECT error FROM publish_jobs ORDER BY id DESC LIMIT 1`))
	}
	if !waitFor(func() bool { return h.Exists("about-us.html") && !h.Exists("oops.html") }) {
		t.Errorf("the earlier version is back: about=%v oops=%v", h.Exists("about-us.html"), h.Exists("oops.html"))
	}
	if !strings.Contains(h.Read(t, "about-us.html"), "We fix teeth") {
		t.Error("the page content is the earlier content")
	}
	if !h.Exists("my-own-file.txt") {
		t.Error("a file that is not the website's was removed")
	}
	if e.scalar(`SELECT published_version FROM sites`) != e.scalar(`SELECT version FROM publish_jobs WHERE id = `+first) {
		t.Error("the site records the version now on the hosting")
	}
	if e.scalar(`SELECT force FROM publish_jobs ORDER BY id DESC LIMIT 1`) != "true" {
		t.Error("a rollback sends every file")
	}
	// what was written since is still in the editor, and shown as not published
	if e.scalar(`SELECT count(*) FROM site_1.pages WHERE slug = 'oops'`) != "1" {
		t.Error("the editor lost the newer work")
	}
	r = ann.get("/admin/publish")
	for _, want := range []string{"went back", "not published yet", "on your hosting now"} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("the screen lacks %q", want)
		}
	}
	// the version that was replaced can be put back again
	if !strings.Contains(r.Body, `name="job" value="`+second+`"`) {
		t.Error("the bad publish can be put back too")
	}
	if e.scalar(`SELECT count(*) FROM audit_log WHERE action = 'put an earlier version of the website back'`) != "1" {
		t.Error("a rollback is logged")
	}

	// scheduled content must not quietly undo a rollback
	e.pool.Exec(t.Context(), `UPDATE publish_targets SET auto_publish = true`)
	newPage(t, ann, "Later", "later", "<p>Soon</p>")
	e.scheduleOverdue("site_1", "later")
	before := e.scalar(`SELECT count(*) FROM publish_jobs`)
	e.p.SweepOnce(t.Context())
	time.Sleep(300 * time.Millisecond)
	if e.scalar(`SELECT count(*) FROM publish_jobs`) != before {
		t.Error("an automatic publish ran after a rollback")
	}

	// copies of the last few publishes are kept however many previews are made after them
	for i := 0; i < 8; i++ {
		if r := ann.post("/admin/build", nil); r.Status != 303 {
			t.Fatalf("preview %d: %d", i, r.Status)
		}
	}
	if !strings.Contains(ann.get("/admin/publish").Body, `name="job" value="`+second+`"`) {
		t.Error("previews pushed a published copy out of the kept ones")
	}
}
