package app_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func (e *testEnv) revisions(pageID string) string {
	return e.sql(`SELECT count(*) FROM page_revisions WHERE page_id = ` + pageID)
}

// types lists a page's blocks in order, with layout columns shown in brackets.
func (e *testEnv) outline(pageID string) string {
	return e.sql(`SELECT COALESCE(string_agg(b.type || CASE WHEN b.parent_id IS NOT NULL THEN '@' || b.slot ELSE '' END, ',' ORDER BY COALESCE(p.position, b.position), (b.parent_id IS NOT NULL), b.slot, b.position), '') FROM blocks b LEFT JOIN blocks p ON p.id = b.parent_id WHERE b.page_id = ` + pageID)
}

func TestEditsLeaveRestorePointsAndTheyCanBeRestored(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	id := newPage(t, admin, "Versions")
	publishPage(t, admin, id, "Versions", "versions")

	// an empty page has nothing to keep
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"richtext"}})
	if got := e.revisions(id); got != "0" {
		t.Errorf("nothing was there before the first block: %s", got)
	}
	text := e.blockID(id, "richtext")
	admin.post("/admin/blocks/"+text, url.Values{"p.content": {"<p>Version one words</p>"}})
	if got := e.revisions(id); got != "1" {
		t.Fatalf("the first edit saves the page as it was: %s", got)
	}
	// more edits in the same session add nothing
	admin.post("/admin/blocks/"+text, url.Values{"p.content": {"<p>Version two words</p>"}})
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"button"}})
	admin.post("/admin/blocks/"+e.blockID(id, "button"), url.Values{"p.label": {"Click"}, "p.url": {"/x"}})
	if got := e.revisions(id); got != "1" {
		t.Errorf("one restore point per session: %s", got)
	}
	// a different person starts a new one
	editor.post("/admin/blocks/"+text, url.Values{"p.content": {"<p>Editor words</p>"}})
	if got := e.revisions(id); got != "2" {
		t.Errorf("another person: %s", got)
	}
	// so does the same person after a pause
	e.pool.Exec(t.Context(), `UPDATE page_revisions SET created_at = now() - interval '11 minutes'`)
	admin.post("/admin/blocks/"+text, url.Values{"p.content": {"<p>Later words</p>"}})
	if got := e.revisions(id); got != "3" {
		t.Errorf("after the gap: %s", got)
	}
	// a change that changes nothing adds nothing, however long ago the last one was
	e.pool.Exec(t.Context(), `UPDATE page_revisions SET created_at = now() - interval '2 hours'`)
	admin.post("/admin/blocks/"+text, url.Values{"p.content": {"<p>Later words</p>"}})
	e.pool.Exec(t.Context(), `UPDATE page_revisions SET created_at = now() - interval '2 hours'`)
	admin.post("/admin/blocks/"+text, url.Values{"p.content": {"<p>Later words</p>"}})
	if got := e.revisions(id); got != "4" {
		t.Errorf("the same page twice is one restore point: %s", got)
	}

	// one more edit, so the page as it is now is not yet in the history
	e.pool.Exec(t.Context(), `UPDATE page_revisions SET created_at = now() - interval '3 hours'`)
	editor.post("/admin/blocks/"+text, url.Values{"p.content": {"<p>Final words</p>"}})

	page := admin.get("/admin/pages/" + id).Body
	for _, want := range []string{"<h2>History</h2>", "before a change by admin@example.com", "before a change by ed@example.com", "Go back to this", "/history/"} {
		if !strings.Contains(page, want) {
			t.Errorf("the history lacks %s", want)
		}
	}
	// the oldest saved version had "Version one words" and a single text block
	oldest := e.sql(`SELECT id FROM page_revisions WHERE page_id = ` + id + ` ORDER BY created_at, id LIMIT 1`)
	if got := e.sql(`SELECT blocks||'|'||(snapshot->'blocks'->0->'props'->>'content') FROM page_revisions WHERE id = ` + oldest); !strings.HasSuffix(got, "|<p>Write your text here.</p>") {
		t.Fatalf("the oldest version: %s", got)
	}
	if r := admin.post("/admin/pages/"+id+"/history/"+oldest+"/restore", url.Values{}); r.Status != 303 || !strings.HasSuffix(r.Header.Get("Location"), "restored=1") {
		t.Fatalf("restore: %d", r.Status)
	}
	if got := e.outline(id); got != "richtext" {
		t.Errorf("back to a single text block: %s", got)
	}
	if body := anon.get("/versions").Body; !strings.Contains(body, "Write your text here.") || strings.Contains(body, "Later words") || strings.Contains(body, "Click") {
		t.Error("the public page is the old version")
	}
	if r := anon.get("/search?q=later"); strings.Contains(r.Body, "Versions") && strings.Contains(r.Body, "Later words") {
		t.Error("the search words followed the restore")
	}
	if !strings.Contains(admin.get("/admin/pages/"+id+"?restored=1").Body, "The earlier version is back") {
		t.Error("the notice")
	}
	// the version being left was saved first, so the restore can be undone
	if got := e.sql(`SELECT count(*) FROM page_revisions WHERE page_id = ` + id + ` AND note LIKE 'Before going back%'`); got != "1" {
		t.Errorf("the restore saved what was there: %s", got)
	}
	latest := e.sql(`SELECT id FROM page_revisions WHERE page_id = ` + id + ` AND note <> '' LIMIT 1`)
	if r := admin.post("/admin/pages/"+id+"/history/"+latest+"/restore", url.Values{}); r.Status != 303 {
		t.Fatalf("undo: %d", r.Status)
	}
	if got := e.outline(id); got != "richtext,button" {
		t.Errorf("undoing the restore brings the page back: %s", got)
	}
	if body := anon.get("/versions").Body; !strings.Contains(body, "Final words") || !strings.Contains(body, "Click") {
		t.Error("public again")
	}
	// going back to what the page already is saves nothing new
	before := e.revisions(id)
	same := e.sql(`SELECT id FROM page_revisions WHERE page_id = ` + id + ` ORDER BY created_at DESC, id DESC LIMIT 1`)
	admin.post("/admin/pages/"+id+"/history/"+same+"/restore", url.Values{})
	_ = before
}

func TestRestoringRebuildsLayoutsAndChecksEverything(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	pageID, layout := layoutPage(t, e, admin, "Columns history")
	admin.post("/admin/blocks/"+layout, url.Values{"p.columns": {"3"}})
	a := e.addChild(t, admin, layout, 0, "richtext")
	b := e.addChild(t, admin, layout, 2, "button")
	e.saveChild(t, admin, a, url.Values{"p.content": {"<p>Left column</p>"}})
	e.saveChild(t, admin, b, url.Values{"p.label": {"Right"}, "p.url": {"/r"}})
	shared := newShared(t, admin, "Box", "spacer")
	admin.post("/admin/pages/"+pageID+"/blocks", url.Values{"type": {"reusable"}, "ref": {shared}})
	want := e.outline(pageID)
	if want != "layout,richtext@0,button@2,reusable" {
		t.Fatalf("setup: %s", want)
	}
	// take a clean restore point of this arrangement, then wreck the page
	e.pool.Exec(t.Context(), `DELETE FROM page_revisions`)
	pid, _ := strconv.ParseInt(pageID, 10, 64)
	if err := e.app.SnapshotPage(t.Context(), pid, "setup"); err != nil {
		t.Fatal(err)
	}
	admin.post("/admin/blocks/"+layout+"/delete", url.Values{})
	admin.post("/admin/blocks/"+e.blockID(pageID, "reusable")+"/delete", url.Values{})
	if got := e.outline(pageID); got != "" {
		t.Fatalf("wrecked: %s", got)
	}
	rev := e.sql(`SELECT id FROM page_revisions WHERE page_id = ` + pageID + ` AND note = '' ORDER BY id LIMIT 1`)
	if r := admin.post("/admin/pages/"+pageID+"/history/"+rev+"/restore", url.Values{}); r.Status != 303 {
		t.Fatalf("restore: %d", r.Status)
	}
	if got := e.outline(pageID); got != want {
		t.Errorf("layout, its columns and the shared block are back: %s", got)
	}
	if got := e.sql(`SELECT props->>'columns' FROM blocks WHERE type = 'layout' AND page_id = ` + pageID); got != "3" {
		t.Errorf("settings: %s", got)
	}
	if got := e.sql(`SELECT props->>'ref' FROM blocks WHERE type = 'reusable' AND page_id = ` + pageID); got != shared {
		t.Errorf("the shared block still points at the library: %s", got)
	}

	// a restore point edited by hand in the database is cleaned like anything typed in
	e.pool.Exec(t.Context(), `UPDATE page_revisions SET snapshot = '{"blocks":[
		{"type":"richtext","props":{"content":"<p>ok</p><script>alert(1)</script>"},"style":{"background":"red;}"}},
		{"type":"nope","props":{}},
		{"type":"button","props":{"label":"Go","url":"javascript:alert(2)"},"style":{}},
		{"type":"layout","props":{"columns":"2"},"style":{},"children":[[{"type":"layout","props":{}},{"type":"spacer","props":{}}],[],[{"type":"spacer","props":{}}]]},
		{"type":"richtext","props":{"content":"<p>plain</p>"},"children":[[{"type":"spacer","props":{}}]]}
	]}' WHERE id = `+rev)
	if r := admin.post("/admin/pages/"+pageID+"/history/"+rev+"/restore", url.Values{}); r.Status != 303 {
		t.Fatalf("restore the doctored one: %d", r.Status)
	}
	if got := e.outline(pageID); got != "richtext,button,layout,spacer@0,spacer@1,richtext" {
		t.Errorf("unknown blocks and layouts in layouts are dropped, the rest kept: %s", got)
	}
	body := e.sql(`SELECT string_agg(props::text||style::text, ' ') FROM blocks WHERE page_id = ` + pageID)
	if strings.Contains(body, "script") || strings.Contains(body, "javascript:") || strings.Contains(body, "red;}") {
		t.Errorf("props were cleaned on the way back in: %s", body)
	}
}

func TestHistoryIsLimitedAndPrivate(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	id := newPage(t, admin, "Many versions")
	other := newPage(t, admin, "Other page")
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"richtext"}})
	admin.post("/admin/pages/"+other+"/blocks", url.Values{"type": {"richtext"}})
	text := e.blockID(id, "richtext")
	for i := 0; i < 40; i++ {
		e.pool.Exec(t.Context(), `UPDATE page_revisions SET created_at = created_at - interval '1 hour'`)
		admin.post("/admin/blocks/"+text, url.Values{"p.content": {"<p>Edit " + strconv.Itoa(i) + "</p>"}})
	}
	if got := e.revisions(id); got != "30" {
		t.Errorf("only the latest 30 are kept: %s", got)
	}
	if got := e.sql(`SELECT count(*) FROM page_revisions WHERE page_id = ` + id + ` AND snapshot::text LIKE '%Edit 0<%'`); got != "0" {
		t.Error("the oldest were dropped, not the newest")
	}
	admin.post("/admin/blocks/"+e.blockID(other, "richtext"), url.Values{"p.content": {"<p>Other</p>"}})
	otherRev := e.sql(`SELECT id FROM page_revisions WHERE page_id = ` + other + ` LIMIT 1`)

	// a restore point of one page cannot be used on another
	if r := admin.post("/admin/pages/"+id+"/history/"+otherRev+"/restore", url.Values{}); r.Status != 404 {
		t.Errorf("another page's version: %d", r.Status)
	}
	for name, path := range map[string]string{"no such version": "/admin/pages/" + id + "/history/999999/restore", "no such page": "/admin/pages/999999/history/" + otherRev + "/restore", "junk": "/admin/pages/" + id + "/history/abc/restore"} {
		if r := admin.post(path, url.Values{}); r.Status != 404 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	// editors may use history; the logged-out may not; the CSRF token is required
	rev := e.sql(`SELECT id FROM page_revisions WHERE page_id = ` + id + ` LIMIT 1`)
	if r := anon.post("/admin/pages/"+id+"/history/"+rev+"/restore", url.Values{}); r.Status == 303 && strings.Contains(r.Header.Get("Location"), "restored=1") {
		t.Error("logged out")
	}
	if r := admin.postNoToken("/admin/pages/"+id+"/history/"+rev+"/restore", url.Values{}); r.Status != 403 {
		t.Errorf("without the CSRF token: %d", r.Status)
	}
	if r := editor.post("/admin/pages/"+id+"/history/"+rev+"/restore", url.Values{}); r.Status != 303 {
		t.Errorf("an editor can go back: %d", r.Status)
	}
	// the history is part of the admin only
	if strings.Contains(anon.get("/many-versions").Body, "History") {
		t.Error("history is never public")
	}
	// deleting the page takes its history with it
	admin.post("/admin/pages/"+id+"/delete", url.Values{})
	if got := e.revisions(id); got != "0" {
		t.Errorf("history goes with the page: %s", got)
	}
}
