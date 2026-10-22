package app_test

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSchedulesSurviveABackupRestore(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	id := newPage(t, admin, "Timed")
	admin.post("/admin/pages/"+id, url.Values{"title": {"Timed"}, "slug": {"timed"}, "status": {"draft"}, "publish_at": {iso(48 * time.Hour)}})
	pid := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {"Timed post"}}).Location())
	admin.post("/admin/posts/"+pid, url.Values{"title": {"Timed post"}, "slug": {"timed-post"}, "status": {"draft"}, "body": {"x"}, "publish_at": {iso(72 * time.Hour)}})
	read := func() string {
		return e.sql(`SELECT (SELECT publish_at::text FROM pages WHERE slug = 'timed')||'|'||(SELECT publish_at::text FROM posts WHERE slug = 'timed-post')`)
	}
	before := read()
	if r := admin.restoreUpload(t, []byte(admin.downloadBackup(false).Body), nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if after := read(); after != before || strings.Contains(after, "|") && strings.HasPrefix(after, "|") {
		t.Errorf("schedules changed: %s vs %s", before, after)
	}
	if p, q := e.publishDue(); p != 0 || q != 0 {
		t.Errorf("a restored schedule is not due yet: %d %d", p, q)
	}
}
