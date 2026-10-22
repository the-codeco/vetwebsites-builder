package app_test

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var reRefID = regexp.MustCompile(`"ref": \d+`) // ids change on restore; what they point at is compared separately

var reSharedLocation = regexp.MustCompile(`^/admin/shared/(\d+)`)

// newShared makes a shared block of a kind and returns its id.
func newShared(t *testing.T, admin *client, name, typ string) string {
	t.Helper()
	r := admin.post("/admin/shared", url.Values{"name": {name}, "type": {typ}})
	m := reSharedLocation.FindStringSubmatch(r.Header.Get("Location"))
	if r.Status != 303 || m == nil {
		t.Fatalf("create shared %s: %d %s", name, r.Status, r.Header.Get("Location"))
	}
	return m[1]
}

func publishPage(t *testing.T, admin *client, id, title, slug string) {
	t.Helper()
	if r := admin.post("/admin/pages/"+id, url.Values{"title": {title}, "slug": {slug}, "status": {"published"}}); r.Status != 303 {
		t.Fatalf("publish %s: %d", slug, r.Status)
	}
}

func TestSharedBlocksAreWrittenOnceAndShownEverywhere(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()

	if body := admin.get("/admin/shared").Body; !strings.Contains(body, "Nothing shared yet") || !strings.Contains(body, `action="/admin/shared"`) || strings.Contains(body, `value="reusable"`) || strings.Contains(body, `value="layout"`) {
		t.Error("an empty library, offering kinds that can be shared but not layouts or shared blocks")
	}
	if !strings.Contains(admin.get("/admin/").Body, `href="/admin/shared"`) {
		t.Error("the sidebar links to the library")
	}
	shared := newShared(t, admin, "Opening <b>hours</b>", "richtext")
	if page := admin.get("/admin/shared/" + shared).Body; !strings.Contains(page, `name="p.content"`) || !strings.Contains(page, `name="s.background"`) || !strings.Contains(page, "No page shows it yet") || strings.Contains(page, "<b>hours</b>") {
		t.Error("the editor for a shared block (its name is escaped)")
	}
	if r := admin.post("/admin/shared/"+shared, url.Values{"name": {"Opening hours"}, "p.content": {"<p>Open Quokkaday <script>alert(1)</script></p>"}, "s.background": {"#abcdef"}, "s.padding": {"lg"}}); r.Status != 303 || !strings.HasSuffix(r.Header.Get("Location"), "?saved=1") {
		t.Fatalf("save: %d", r.Status)
	}
	if !strings.Contains(admin.get("/admin/shared/"+shared+"?saved=1").Body, "Saved") {
		t.Error("the saved notice")
	}

	// two pages use it, as do the columns of a layout on a third
	var pages []string
	for i, slug := range []string{"one", "two", "three"} {
		id := newPage(t, admin, "Page "+slug)
		publishPage(t, admin, id, "Page "+slug, slug)
		pages = append(pages, id)
		_ = i
	}
	for _, id := range pages[:2] {
		page := admin.get("/admin/pages/" + id).Body
		if !strings.Contains(page, `name="ref"`) || !strings.Contains(page, `>Opening hours</option>`) || !strings.Contains(page, "Add a shared block") {
			t.Error("the page editor offers the shared block")
		}
		if r := admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"reusable"}, "ref": {shared}}); r.Status != 200 || !strings.Contains(r.Body, "Edit the shared block") || !strings.Contains(r.Body, "Opening hours") {
			t.Fatalf("add shared block: %d\n%s", r.Status, r.Body)
		}
	}
	admin.post("/admin/pages/"+pages[2]+"/blocks", url.Values{"type": {"layout"}})
	layout := e.blockID(pages[2], "layout")
	if body := admin.get("/admin/pages/" + pages[2]).Body; !strings.Contains(body, `<option value="`+shared+`">Opening hours</option>`) {
		t.Error("a column offers shared blocks")
	}
	if r := admin.post("/admin/blocks/"+layout+"/children", url.Values{"type": {"reusable"}, "ref": {shared}, "slot": {"1"}}); r.Status != 200 {
		t.Fatalf("add to a column: %d", r.Status)
	}

	for _, slug := range []string{"one", "two", "three"} {
		body := anon.get("/" + slug).Body
		for _, want := range []string{"Open Quokkaday", `pad-lg`, `--bg:#abcdef`} {
			if !strings.Contains(body, want) {
				t.Errorf("/%s lacks %s", slug, want)
			}
		}
		if strings.Contains(body, "<script>alert") || strings.Contains(body, "block-reusable") {
			t.Errorf("/%s: shared content is cleaned, and shows as the kind of block it is", slug)
		}
	}
	if body := anon.get("/three").Body; !strings.Contains(body, `class="block block-richtext nested`) {
		t.Error("in a column it is drawn as a nested block")
	}
	// each use keeps its own anchor
	one, two := anon.get("/one").Body, anon.get("/two").Body
	if idOf(one) == idOf(two) {
		t.Error("blocks on different pages have their own ids")
	}

	// the library lists where it is used
	list := admin.get("/admin/shared").Body
	if !strings.Contains(list, "3 places") {
		t.Errorf("uses counted:\n%s", list)
	}
	if page := admin.get("/admin/shared/" + shared).Body; !strings.Contains(page, "Page one") || !strings.Contains(page, "Page three") || !strings.Contains(page, "3 pages") && !strings.Contains(page, "Changes appear on 3") {
		t.Error("the pages using it are listed")
	}

	// search finds all three pages
	if r := anon.get("/search?q=quokkaday"); strings.Count(r.Body, "Page ") < 3 {
		t.Errorf("search finds pages through shared blocks:\n%s", r.Body)
	}

	// one edit changes every page, and the search words follow
	admin.post("/admin/shared/"+shared, url.Values{"name": {"Opening hours"}, "p.content": {"<p>Now Wombatday only</p>"}, "s.padding": {"sm"}})
	for _, slug := range []string{"one", "two", "three"} {
		body := anon.get("/" + slug).Body
		if !strings.Contains(body, "Now Wombatday only") || strings.Contains(body, "Quokkaday") {
			t.Errorf("/%s did not follow the edit", slug)
		}
	}
	if r := anon.get("/search?q=quokkaday"); strings.Contains(r.Body, "Page one") {
		t.Error("old words are gone from search")
	}
	if r := anon.get("/search?q=wombatday"); strings.Count(r.Body, "Page ") < 3 {
		t.Error("new words are searchable on every page")
	}

	// deleting is refused while it is used
	if r := admin.post("/admin/shared/"+shared+"/delete", url.Values{}); r.Status != 409 || !strings.Contains(r.Body, "still used") {
		t.Errorf("delete while used: %d", r.Status)
	}
	// removing a block from a page never deletes the library block
	admin.post("/admin/blocks/"+e.blockID(pages[0], "reusable")+"/delete", url.Values{})
	if got := e.sql(`SELECT count(*) FROM reusable_blocks`); got != "1" {
		t.Error("the library keeps it")
	}
	if strings.Contains(anon.get("/one").Body, "Wombatday") {
		t.Error("page one no longer shows it")
	}
	admin.post("/admin/blocks/"+e.blockID(pages[1], "reusable")+"/delete", url.Values{})
	admin.post("/admin/blocks/"+layout+"/delete", url.Values{})
	if r := admin.post("/admin/shared/"+shared+"/delete", url.Values{}); r.Status != 303 {
		t.Errorf("delete when unused: %d", r.Status)
	}
	if got := e.sql(`SELECT count(*) FROM reusable_blocks`); got != "0" {
		t.Error("deleted")
	}
}

var reBlockAnchor = regexp.MustCompile(`id="block-(\d+)"`)

func idOf(body string) string {
	if m := reBlockAnchor.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

func TestSavingABlockAsSharedKeepsItsPlace(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	id := newPage(t, admin, "Source")
	publishPage(t, admin, id, "Source", "source")
	for _, typ := range []string{"richtext", "button", "spacer"} {
		admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {typ}})
	}
	button := e.blockID(id, "button")
	admin.post("/admin/blocks/"+button, url.Values{"p.label": {"Book Zebrafox"}, "p.url": {"/book"}, "s.background": {"#112233"}})

	card := admin.get("/admin/pages/" + id).Body
	if !strings.Contains(card, `hx-post="/admin/blocks/`+button+`/share"`) || !strings.Contains(card, "Save as shared block") {
		t.Error("a block offers to be shared")
	}
	e.pool.Exec(context.Background(), `UPDATE pages SET updated_at = '2001-01-01' WHERE id = `+id)
	if r := admin.post("/admin/blocks/"+button+"/share", url.Values{"name": {"Book now"}}); r.Status != 200 || !strings.Contains(r.Body, "Edit the shared block") {
		t.Fatalf("share: %d", r.Status)
	}
	if got := e.sql(`SELECT updated_at > now() - interval '1 hour' FROM pages WHERE id = ` + id); got != "true" {
		t.Error("sharing counts as a change to the page")
	}
	if got := e.sql(`SELECT string_agg(type, ',' ORDER BY position) FROM blocks WHERE page_id = ` + id); got != "richtext,reusable,spacer" {
		t.Errorf("it stays in the same place: %s", got)
	}
	if got := e.sql(`SELECT name||'|'||type||'|'||(props->>'label')||'|'||(style->>'background') FROM reusable_blocks`); got != "Book now|button|Book Zebrafox|#112233" {
		t.Errorf("the library holds the block with its settings and style: %s", got)
	}
	if body := anon.get("/source").Body; !strings.Contains(body, "Book Zebrafox") || !strings.Contains(body, `--bg:#112233`) {
		t.Error("the page looks the same")
	}
	if r := anon.get("/search?q=zebrafox"); !strings.Contains(r.Body, "Source") {
		t.Error("its words are still searchable")
	}

	// refusals
	shared := e.sql(`SELECT id FROM reusable_blocks`)
	sharedBlock := e.blockID(id, "reusable")
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"layout"}})
	layout := e.blockID(id, "layout")
	for name, c := range map[string]struct {
		path string
		form url.Values
		want int
	}{
		"no name":                {"/admin/blocks/" + e.blockID(id, "richtext") + "/share", url.Values{"name": {"  "}}, 400},
		"a layout":               {"/admin/blocks/" + layout + "/share", url.Values{"name": {"x"}}, 400},
		"a shared block again":   {"/admin/blocks/" + sharedBlock + "/share", url.Values{"name": {"x"}}, 400},
		"no such block":          {"/admin/blocks/999999/share", url.Values{"name": {"x"}}, 404},
		"add without a target":   {"/admin/pages/" + id + "/blocks", url.Values{"type": {"reusable"}}, 400},
		"add junk target":        {"/admin/pages/" + id + "/blocks", url.Values{"type": {"reusable"}, "ref": {"abc"}}, 400},
		"add missing target":     {"/admin/pages/" + id + "/blocks", url.Values{"type": {"reusable"}, "ref": {"999999"}}, 400},
		"add to a column, none":  {"/admin/blocks/" + layout + "/children", url.Values{"type": {"reusable"}, "slot": {"0"}}, 400},
		"edit through the page":  {"/admin/blocks/" + sharedBlock, url.Values{"p.ref": {"5"}}, 400},
		"no such shared block":   {"/admin/shared/999999", url.Values{"name": {"x"}}, 404},
		"create without a name":  {"/admin/shared", url.Values{"name": {" "}, "type": {"richtext"}}, 400},
		"create a layout":        {"/admin/shared", url.Values{"name": {"x"}, "type": {"layout"}}, 400},
		"create a shared block":  {"/admin/shared", url.Values{"name": {"x"}, "type": {"reusable"}}, 400},
		"create an unknown kind": {"/admin/shared", url.Values{"name": {"x"}, "type": {"nope"}}, 400},
	} {
		if r := admin.post(c.path, c.form); r.Status != c.want {
			t.Errorf("%s: %d, want %d", name, r.Status, c.want)
		}
	}
	if got := e.sql(`SELECT props->>'ref' FROM blocks WHERE id = ` + sharedBlock); got != shared {
		t.Errorf("the pointer was not changed: %s", got)
	}
	if got := e.sql(`SELECT count(*) FROM reusable_blocks`); got != "1" {
		t.Errorf("refused requests added nothing: %s", got)
	}
	if r := admin.post("/admin/shared/"+shared, url.Values{"name": {" "}}); r.Status != 400 {
		t.Errorf("saving without a name: %d", r.Status)
	}

	// a block in a column can be shared too, and stays in its column
	admin.post("/admin/blocks/"+layout+"/children", url.Values{"type": {"spacer"}, "slot": {"1"}})
	nested := e.sql(`SELECT id FROM blocks WHERE parent_id = ` + layout)
	if r := admin.post("/admin/blocks/"+nested+"/share", url.Values{"name": {"Gap"}}); r.Status != 200 {
		t.Fatalf("share a nested block: %d", r.Status)
	}
	if got := e.sql(`SELECT type||slot||parent_id::text FROM blocks WHERE id = ` + nested); got != "reusable1"+layout {
		t.Errorf("same column: %s", got)
	}
}

func TestSharedBlockAccessAndDamage(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	shared := newShared(t, admin, "Box", "richtext")
	id := newPage(t, admin, "Damage")
	publishPage(t, admin, id, "Damage", "damage")
	admin.post("/admin/blocks/"+shared, url.Values{})
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"richtext"}})
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"reusable"}, "ref": {shared}})
	admin.post("/admin/shared/"+shared, url.Values{"name": {"Box"}, "p.content": {"<p>Kept text</p>"}})

	// editors can use the library (they edit pages too); the logged-out cannot
	if r := editor.get("/admin/shared"); r.Status != 200 {
		t.Errorf("editor: %d", r.Status)
	}
	if r := anon.get("/admin/shared"); r.Status == 200 {
		t.Error("logged out")
	}
	if r := anon.post("/admin/shared", url.Values{"name": {"x"}, "type": {"spacer"}}); r.Status == 303 && strings.Contains(r.Header.Get("Location"), "/admin/shared/") {
		t.Error("logged-out create")
	}
	if r := admin.postNoToken("/admin/shared", url.Values{"name": {"No token"}, "type": {"spacer"}}); r.Status != 403 {
		t.Errorf("without the CSRF token: %d", r.Status)
	}
	if r := admin.postNoToken("/admin/shared/"+shared, url.Values{"name": {"Hacked"}}); r.Status != 403 {
		t.Errorf("save without the CSRF token: %d", r.Status)
	}
	if got := e.sql(`SELECT count(*) FROM reusable_blocks WHERE name <> 'Box'`); got != "0" {
		t.Error("refused requests changed nothing")
	}

	// the library block vanishes behind the page's back: the page still works, minus that block
	e.pool.Exec(context.Background(), `DELETE FROM reusable_blocks`)
	r := anon.get("/damage")
	if r.Status != 200 || strings.Contains(r.Body, "Kept text") {
		t.Errorf("a missing shared block is left out, not an error: %d", r.Status)
	}
	if strings.Count(r.Body, `<section id="block-`) != 1 {
		t.Errorf("the missing block leaves no empty section behind: %d sections", strings.Count(r.Body, `<section id="block-`))
	}
	if body := admin.get("/admin/pages/" + id).Body; !strings.Contains(body, "was deleted") {
		t.Error("the editor says the shared block is gone")
	}
	// junk refs written by hand are harmless
	e.pool.Exec(context.Background(), `UPDATE blocks SET props = '{"ref": "not a number"}' WHERE type = 'reusable'`)
	if r := anon.get("/damage"); r.Status != 200 {
		t.Errorf("junk ref: %d", r.Status)
	}
	if r := admin.post("/admin/pages/"+id, url.Values{"title": {"Damage"}, "slug": {"damage"}, "status": {"published"}}); r.Status != 303 {
		t.Errorf("saving the page: %d", r.Status)
	}
	if r := anon.get("/search?q=damage"); r.Status != 200 {
		t.Errorf("search: %d", r.Status)
	}

	// the library has a limit
	e.pool.Exec(context.Background(), `INSERT INTO reusable_blocks (name, type) SELECT 'n'||g, 'spacer' FROM generate_series(1, 100) g`)
	if r := admin.post("/admin/shared", url.Values{"name": {"101st"}, "type": {"spacer"}}); r.Status != 400 || !strings.Contains(r.Body, "at most 100") {
		t.Errorf("full library: %d", r.Status)
	}
	if got := e.sql(`SELECT count(*) FROM reusable_blocks`); got != strconv.Itoa(100) {
		t.Errorf("count: %s", got)
	}
	spacer := e.blockID(id, "richtext")
	if r := admin.post("/admin/blocks/"+spacer+"/share", url.Values{"name": {"101st"}}); r.Status != 400 {
		t.Errorf("sharing into a full library: %d", r.Status)
	}
	if got := e.sql(`SELECT type FROM blocks WHERE id = ` + spacer); got != "richtext" {
		t.Errorf("a refused share leaves the block alone: %s", got)
	}
}

func TestSharedBlocksSurviveABackupRestore(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	a := newShared(t, admin, "Alpha", "richtext")
	b := newShared(t, admin, "Beta", "button")
	admin.post("/admin/shared/"+a, url.Values{"name": {"Alpha"}, "p.content": {"<p>Quokka alpha words</p>"}, "s.background": {"#abcdef"}})
	admin.post("/admin/shared/"+b, url.Values{"name": {"Beta"}, "p.label": {"Beta go"}, "p.url": {"/go"}})
	// a throwaway one first, so ids don't line up after the restore
	spare := newShared(t, admin, "Spare", "spacer")
	_ = spare
	p1 := newPage(t, admin, "Restore me")
	publishPage(t, admin, p1, "Restore me", "restore-me")
	admin.post("/admin/pages/"+p1+"/blocks", url.Values{"type": {"reusable"}, "ref": {a}})
	admin.post("/admin/pages/"+p1+"/blocks", url.Values{"type": {"layout"}})
	layout := e.blockID(p1, "layout")
	admin.post("/admin/blocks/"+layout+"/children", url.Values{"type": {"reusable"}, "ref": {b}, "slot": {"0"}})
	admin.post("/admin/blocks/"+layout+"/children", url.Values{"type": {"reusable"}, "ref": {a}, "slot": {"1"}})

	snapshot := func() string {
		return reRefID.ReplaceAllString(e.siteSnapshot(), `"ref": N`) + "\n####\n" + e.sql(`SELECT COALESCE(string_agg(r.name||'|'||r.type||'|'||r.props::text||'|'||r.style::text, E'\n' ORDER BY r.name), '') FROM reusable_blocks r`) +
			"\n####\n" + e.sql(`SELECT COALESCE(string_agg(b.type||'|'||b.slot||'|'||b.position||'|'||COALESCE((SELECT r.name FROM reusable_blocks r WHERE r.id = (b.props->>'ref')::bigint), '-'), E'\n' ORDER BY COALESCE(p.position, b.position), (b.parent_id IS NOT NULL), b.slot, b.position), '')
				FROM blocks b LEFT JOIN blocks p ON p.id = b.parent_id`)
	}
	if r := admin.restoreUpload(t, []byte(admin.downloadBackup(false).Body), nil); r.Status != 200 {
		t.Fatalf("settle: %d", r.Status)
	}
	before := snapshot()
	raw := []byte(admin.downloadBackup(false).Body)
	site := string(unzip(t, raw)["site.json"])
	for _, want := range []string{`"reusables"`, "Quokka alpha words", `"name": "Beta"`} {
		if !strings.Contains(site, want) {
			t.Errorf("site.json lacks %s", want)
		}
	}
	oldA := a
	// change things, so the restore has something to put back
	admin.post("/admin/shared/"+a, url.Values{"name": {"Alpha"}, "p.content": {"<p>Changed</p>"}})
	e.pool.Exec(context.Background(), `DELETE FROM reusable_blocks WHERE name = 'Spare'`)
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if after := snapshot(); after != before {
		t.Errorf("shared blocks are not as they were.\nbefore:\n%s\n\nafter:\n%s", before, after)
	}
	newA := e.sql(`SELECT id FROM reusable_blocks WHERE name = 'Alpha'`)
	if newA == oldA {
		t.Log("(ids happened to match; the remapping is still checked below)")
	}
	body := anon.get("/restore-me").Body
	if strings.Count(body, "Quokka alpha words") != 2 || !strings.Contains(body, "Beta go") || strings.Contains(body, "Changed") {
		t.Errorf("restored page shows the shared blocks:\n%s", body)
	}
	if r := anon.get("/search?q=quokka"); !strings.Contains(r.Body, "Restore me") {
		t.Error("search finds the restored page through its shared block")
	}
	// every pointer on the page names a block that exists
	if got := e.sql(`SELECT count(*) FROM blocks b WHERE b.type = 'reusable' AND NOT EXISTS (SELECT 1 FROM reusable_blocks r WHERE r.id = (b.props->>'ref')::bigint)`); got != "0" {
		t.Errorf("dangling pointers after restore: %s", got)
	}
	if got := e.sql(`SELECT count(*) FROM reusable_blocks`); got != "3" {
		t.Errorf("library size: %s", got)
	}
}
