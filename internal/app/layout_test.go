package app_test

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"webbuilder/internal/backup"
	"webbuilder/internal/blocks"
)

// layoutPage makes a page with one layout block and returns the page id and the layout's id.
func layoutPage(t *testing.T, e *testEnv, admin *client, title string) (pageID, layoutID string) {
	t.Helper()
	pageID = newPage(t, admin, title)
	if r := admin.post("/admin/pages/"+pageID+"/blocks", url.Values{"type": {"layout"}}); r.Status != 200 {
		t.Fatalf("add layout: %d", r.Status)
	}
	layoutID = e.blockID(pageID, "layout")
	return
}

func (e *testEnv) addChild(t *testing.T, admin *client, layoutID string, slot int, typ string) string {
	t.Helper()
	before := e.sql(`SELECT COALESCE(max(id), 0) FROM blocks`)
	r := admin.post("/admin/blocks/"+layoutID+"/children", url.Values{"type": {typ}, "slot": {strconv.Itoa(slot)}})
	if r.Status != 200 {
		t.Fatalf("add %s to column %d: %d %s", typ, slot, r.Status, firstLine(r.Body))
	}
	id := e.sql(`SELECT max(id) FROM blocks`)
	if id == before {
		t.Fatalf("nothing was added")
	}
	return id
}

// columns reads back what is in each column: "type,type | type | ".
func (e *testEnv) columns(layoutID string, count int) string {
	parts := make([]string, count)
	for i := range parts {
		parts[i] = e.sql(`SELECT COALESCE(string_agg(type, ',' ORDER BY position), '') FROM blocks WHERE parent_id = $1 AND slot = $2`, layoutID, i)
	}
	return strings.Join(parts, " | ")
}

func (e *testEnv) saveChild(t *testing.T, admin *client, id string, props url.Values) {
	t.Helper()
	if r := admin.post("/admin/blocks/"+id, props); r.Status != 200 || !strings.Contains(r.Body, "Saved") {
		t.Fatalf("save block %s: %d", id, r.Status)
	}
}

func TestLayoutEditorAddsAndArrangesBlocks(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	pageID, layout := layoutPage(t, e, admin, "Columns Page")

	// the editor shows the layout's settings and an empty, labelled column ready to take blocks
	page := admin.get("/admin/pages/" + pageID).Body
	for _, want := range []string{`name="p.columns"`, `name="p.ratio"`, `name="p.gap"`, `name="p.valign"`, `name="p.reverse"`, ">Column 1<", ">Column 2<", `hx-post="/admin/blocks/` + layout + `/children"`, `name="slot" value="0"`, `name="slot" value="1"`, "Add to column 2"} {
		if !strings.Contains(page, want) {
			t.Errorf("the editor lacks %s", want)
		}
	}
	if strings.Contains(page, ">Column 3<") {
		t.Error("two columns by default")
	}
	// the "add to a column" menus offer every block but the layout
	menu := regexp.MustCompile(`(?s)aria-label="Block to add to column 1">(.*?)</select>`).FindStringSubmatch(page)
	if menu == nil || strings.Contains(menu[1], `value="layout"`) || !strings.Contains(menu[1], `value="richtext"`) || !strings.Contains(menu[1], `value="contactform"`) {
		t.Errorf("the column's menu: %v", menu)
	}
	if !strings.Contains(page, `<option value="layout">`) {
		t.Error("the page's own menu still offers layouts")
	}

	// blocks go into the column asked for, in the order added
	rt := e.addChild(t, admin, layout, 0, "richtext")
	img := e.addChild(t, admin, layout, 0, "image")
	form := e.addChild(t, admin, layout, 1, "contactform")
	if got := e.columns(layout, 2); got != "richtext,image | contactform" {
		t.Fatalf("columns: %q", got)
	}
	for _, id := range []string{rt, img, form} {
		if e.sql(`SELECT parent_id FROM blocks WHERE id = `+id) != layout || e.sql(`SELECT page_id FROM blocks WHERE id = `+id) != pageID {
			t.Errorf("block %s is not tied to the layout and page", id)
		}
		if e.sql(`SELECT style->>'padding' FROM blocks WHERE id = `+id) != "none" {
			t.Errorf("a block in a column starts with no padding of its own")
		}
	}
	// the new block's card is open, inside its column, and has no 'content width' (a column fills itself)
	r := admin.post("/admin/blocks/"+layout+"/children", url.Values{"type": {"richtext"}, "slot": {"1"}})
	nid := e.sql(`SELECT max(id) FROM blocks`)
	start := strings.Index(r.Body, `<details class="blk nested" id="blk-`+nid+`" open>`)
	end := strings.Index(r.Body, `/admin/blocks/`+nid+`/slot"`)
	if start < 0 || end < start || strings.Contains(r.Body[start:end], `name="s.width"`) || !strings.Contains(r.Body[start:end], `name="s.padding"`) {
		t.Errorf("a new block's card in a column: %d %d", start, end)
	}
	if !strings.Contains(r.Body, `id="blk-`+layout+`" open`) {
		t.Error("the layout holding the new block is open")
	}
	if r.Header.Get("HX-Trigger") != "preview-refresh" {
		t.Error("the preview should refresh")
	}
	admin.post("/admin/blocks/"+nid+"/delete", nil)

	// saving a nested block works like any other
	e.saveChild(t, admin, rt, url.Values{"p.content": {"<p>Left <b>side</b></p><script>alert(1)</script>"}, "s.padding": {"sm"}, "s.background": {"#ffeedd"}})
	if got := e.sql(`SELECT props->>'content' FROM blocks WHERE id = ` + rt); strings.Contains(got, "script") || !strings.Contains(got, "Left") {
		t.Errorf("rich text saved in a column is cleaned: %q", got)
	}
	if e.sql(`SELECT style->>'background' FROM blocks WHERE id = `+rt) != "#ffeedd" || e.sql(`SELECT parent_id FROM blocks WHERE id = `+rt) != layout {
		t.Error("style saved, and the block stays where it is")
	}

	// moving up and down inside a column; the ends do nothing
	admin.post("/admin/blocks/"+img+"/move", url.Values{"dir": {"up"}})
	if got := e.columns(layout, 2); got != "image,richtext | contactform" {
		t.Errorf("after moving the image up: %q", got)
	}
	admin.post("/admin/blocks/"+img+"/move", url.Values{"dir": {"up"}})
	admin.post("/admin/blocks/"+form+"/move", url.Values{"dir": {"down"}})
	if got := e.columns(layout, 2); got != "image,richtext | contactform" {
		t.Errorf("moving past an end changes nothing: %q", got)
	}
	// moving a block in a column must not disturb the page's own order
	other := newPage(t, admin, "Two")
	admin.post("/admin/pages/"+other+"/blocks", url.Values{"type": {"spacer"}})
	if got := e.sql(`SELECT string_agg(type, ',' ORDER BY position) FROM blocks WHERE page_id = $1 AND parent_id IS NULL`, pageID); got != "layout" {
		t.Errorf("the page's own blocks: %s", got)
	}

	// between columns: to the end of the next column; the ends do nothing
	admin.post("/admin/blocks/"+img+"/slot", url.Values{"dir": {"right"}})
	if got := e.columns(layout, 2); got != "richtext | contactform,image" {
		t.Errorf("after moving right: %q", got)
	}
	admin.post("/admin/blocks/"+img+"/slot", url.Values{"dir": {"right"}})
	if got := e.columns(layout, 2); got != "richtext | contactform,image" {
		t.Errorf("no column beyond the last: %q", got)
	}
	admin.post("/admin/blocks/"+rt+"/slot", url.Values{"dir": {"left"}})
	if got := e.columns(layout, 2); got != "richtext | contactform,image" {
		t.Errorf("no column before the first: %q", got)
	}
	admin.post("/admin/blocks/"+img+"/slot", url.Values{"dir": {"left"}})
	if got := e.columns(layout, 2); got != "richtext,image | contactform" {
		t.Errorf("after moving left: %q", got)
	}
	for _, col := range []int{0, 1} { // positions stay gapless in every column
		if e.sql(`SELECT string_agg(position::text, ',' ORDER BY position) FROM blocks WHERE parent_id = $1 AND slot = $2`, layout, col) != map[int]string{0: "0,1", 1: "0"}[col] {
			t.Errorf("positions in column %d", col)
		}
	}

	// deleting a nested block; deleting the layout takes everything in it
	if r := admin.post("/admin/blocks/"+form+"/delete", nil); r.Status != 200 {
		t.Fatalf("delete child: %d", r.Status)
	}
	if got := e.columns(layout, 2); got != "richtext,image | " {
		t.Errorf("after deleting: %q", got)
	}
	if !strings.Contains(admin.get("/admin/pages/"+pageID).Body, "Delete this layout and every block inside it?") {
		t.Error("deleting a layout asks first, and says what goes with it")
	}
	admin.post("/admin/blocks/"+layout+"/delete", nil)
	if e.sql(`SELECT count(*) FROM blocks WHERE page_id = `+pageID) != "0" {
		t.Error("deleting the layout deletes its blocks")
	}
}

func TestLayoutColumnCountChangesKeepEveryBlock(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	pageID, layout := layoutPage(t, e, admin, "Resize")
	save := func(cols string) response {
		return admin.post("/admin/blocks/"+layout, url.Values{"p.columns": {cols}, "p.ratio": {"equal"}, "p.gap": {"md"}, "p.valign": {"top"}})
	}
	if r := save("4"); r.Status != 200 || !strings.Contains(r.Body, ">Column 4<") || !strings.Contains(r.Body, `id="blocks"`) {
		t.Fatalf("four columns: %d", r.Status)
	}
	a := e.addChild(t, admin, layout, 0, "richtext")
	b := e.addChild(t, admin, layout, 2, "image")
	c := e.addChild(t, admin, layout, 3, "button")
	d := e.addChild(t, admin, layout, 3, "spacer")
	if got := e.columns(layout, 4); got != "richtext |  | image | button,spacer" {
		t.Fatalf("columns: %q", got)
	}

	// fewer columns: the blocks of the columns that went away are added to the end of the last one, in order
	r := save("2")
	if got := e.columns(layout, 2); got != "richtext | image,button,spacer" {
		t.Errorf("after going to two columns: %q", got)
	}
	if strings.Contains(r.Body, ">Column 3<") || !strings.Contains(r.Body, ">Column 2<") {
		t.Error("the editor shows two columns")
	}
	if e.sql(`SELECT count(*) FROM blocks WHERE parent_id = `+layout) != "4" || e.sql(`SELECT max(slot) FROM blocks WHERE parent_id = `+layout) != "1" {
		t.Error("no block lost, none left in a column that doesn't exist")
	}
	if e.sql(`SELECT string_agg(position::text, ',' ORDER BY position) FROM blocks WHERE parent_id = $1 AND slot = 1`, layout) != "0,1,2" {
		t.Error("positions are gapless")
	}
	_ = []string{a, b, c, d}

	// more columns again: the blocks stay where they are
	save("3")
	if got := e.columns(layout, 3); got != "richtext | image,button,spacer | " {
		t.Errorf("after going to three: %q", got)
	}
	// a layout's settings can't be made up: bad values fall back to defaults
	admin.post("/admin/blocks/"+layout, url.Values{"p.columns": {"9"}, "p.ratio": {"fancy"}, "p.gap": {"huge"}, "p.valign": {"sideways"}})
	if e.sql(`SELECT (props->>'columns')||(props->>'ratio')||(props->>'gap')||(props->>'valign') FROM blocks WHERE id = `+layout) != "2equalmdtop" {
		t.Errorf("settings: %s", e.sql(`SELECT props::text FROM blocks WHERE id = `+layout))
	}
	if got := e.columns(layout, 2); got != "richtext | image,button,spacer" {
		t.Errorf("an invalid count falls back to two columns and keeps the blocks: %q", got)
	}
	_ = pageID
}

func TestLayoutRefusesWhatDoesNotBelong(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	pageID, layout := layoutPage(t, e, admin, "Rules")
	child := e.addChild(t, admin, layout, 0, "richtext")
	plain := newPage(t, admin, "Plain")
	admin.post("/admin/pages/"+plain+"/blocks", url.Values{"type": {"richtext"}})
	notLayout := e.blockID(plain, "richtext")
	count := func() string { return e.sql(`SELECT count(*) FROM blocks`) }
	before := count()

	for name, c := range map[string]struct {
		target string
		form   url.Values
		status int
	}{
		"a layout inside a layout":     {layout, url.Values{"type": {"layout"}, "slot": {"0"}}, 400},
		"an unknown block":             {layout, url.Values{"type": {"evil"}, "slot": {"0"}}, 400},
		"no type":                      {layout, url.Values{"slot": {"0"}}, 400},
		"a column that isn't there":    {layout, url.Values{"type": {"richtext"}, "slot": {"2"}}, 404},
		"a negative column":            {layout, url.Values{"type": {"richtext"}, "slot": {"-1"}}, 404},
		"a column that isn't a number": {layout, url.Values{"type": {"richtext"}, "slot": {"first"}}, 400},
		"no column":                    {layout, url.Values{"type": {"richtext"}}, 400},
		"a block that isn't a layout":  {notLayout, url.Values{"type": {"richtext"}, "slot": {"0"}}, 404},
		"a block inside a layout":      {child, url.Values{"type": {"richtext"}, "slot": {"0"}}, 404},
		"a block that doesn't exist":   {"999999", url.Values{"type": {"richtext"}, "slot": {"0"}}, 404},
	} {
		if r := admin.post("/admin/blocks/"+c.target+"/children", c.form); r.Status != c.status {
			t.Errorf("%s: %d, want %d", name, r.Status, c.status)
		}
	}
	if count() != before {
		t.Error("none of those may add anything")
	}
	// moving between columns is only for blocks inside a layout
	for _, id := range []string{layout, notLayout, "999999"} {
		if r := admin.post("/admin/blocks/"+id+"/slot", url.Values{"dir": {"right"}}); r.Status != 404 {
			t.Errorf("slot move of %s: %d", id, r.Status)
		}
	}
	if r := admin.post("/admin/blocks/"+child+"/slot", url.Values{"dir": {"up"}}); r.Status != 400 {
		t.Errorf("a bad direction: %d", r.Status)
	}

	// limits: a column holds a bounded number, and the page's limit counts every block, nested ones too
	for i := 1; i < blocks.MaxBlocksPerColumn; i++ {
		e.addChild(t, admin, layout, 0, "spacer")
	}
	if r := admin.post("/admin/blocks/"+layout+"/children", url.Values{"type": {"spacer"}, "slot": {"0"}}); r.Status != 400 {
		t.Errorf("a full column: %d", r.Status)
	}
	if r := admin.post("/admin/blocks/"+layout+"/children", url.Values{"type": {"spacer"}, "slot": {"1"}}); r.Status != 200 {
		t.Errorf("the other column still has room: %d", r.Status)
	}
	spare := e.addChild(t, admin, layout, 1, "button")
	if r := admin.post("/admin/blocks/"+spare+"/slot", url.Values{"dir": {"left"}}); r.Status != 400 {
		t.Errorf("moving into a full column: %d", r.Status)
	}

	e.pool.Exec(context.Background(), `DELETE FROM blocks WHERE page_id = $1 AND id <> $2`, pageID, layout)
	e.pool.Exec(context.Background(), `INSERT INTO blocks (page_id, position, type) SELECT $1, g, 'spacer' FROM generate_series(1, 198) g`, pageID)
	if r := admin.post("/admin/blocks/"+layout+"/children", url.Values{"type": {"spacer"}, "slot": {"0"}}); r.Status != 200 {
		t.Fatalf("the 200th block: %d", r.Status)
	}
	if r := admin.post("/admin/blocks/"+layout+"/children", url.Values{"type": {"spacer"}, "slot": {"0"}}); r.Status != 400 {
		t.Errorf("a page that is full counts nested blocks: %d", r.Status)
	}
	if r := admin.post("/admin/pages/"+pageID+"/blocks", url.Values{"type": {"spacer"}}); r.Status != 400 {
		t.Errorf("a full page takes no more: %d", r.Status)
	}
}

func TestLayoutOnThePublicSite(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	pageID, layout := layoutPage(t, e, admin, "Public Columns")
	admin.post("/admin/blocks/"+layout, url.Values{"p.columns": {"3"}, "p.ratio": {"wide-middle"}, "p.gap": {"lg"}, "p.valign": {"center"}, "p.reverse": {"on"}, "s.background": {"#112233"}, "s.padding": {"lg"}})
	left := e.addChild(t, admin, layout, 0, "richtext")
	mid1 := e.addChild(t, admin, layout, 1, "richtext")
	mid2 := e.addChild(t, admin, layout, 1, "button")
	form := e.addChild(t, admin, layout, 2, "contactform")
	e.saveChild(t, admin, left, url.Values{"p.content": {"<p>Alpha <em>left</em></p>"}, "s.align": {"center"}, "s.padding": {"none"}})
	e.saveChild(t, admin, mid1, url.Values{"p.content": {"<p>Bravo middle</p>"}, "s.background": {"#ffcc00"}, "s.padding": {"sm"}})
	e.saveChild(t, admin, mid2, url.Values{"p.label": {"Click <me>"}, "p.url": {"/go"}, "p.variant": {"primary"}})
	e.saveChild(t, admin, form, url.Values{"p.heading": {"Write to us"}})
	admin.post("/admin/pages/"+pageID, url.Values{"title": {"Public Columns"}, "slug": {"cols"}, "status": {"published"}})

	body := anon.get("/cols").Body
	for _, want := range []string{
		`id="block-` + layout + `" class="block block-layout pad-lg align-left width-normal pinned"`,
		`<div class="layout layout-3 ratio-wide-middle gap-lg valign-center reverse-mobile">`,
		`id="block-` + left + `" class="block block-richtext nested pad-none align-center width-normal"`,
		`id="block-` + mid1 + `" class="block block-richtext nested pad-sm align-left width-normal pinned"`,
		`id="block-` + form + `" class="block block-contactform nested`,
		`<p>Alpha <em>left</em></p>`, `Bravo middle`, `href="/go">Click &lt;me&gt;</a>`, `<h2>Write to us</h2>`,
		`style="--bg:#ffcc00;"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	// three columns, children in their own column and in order
	i0, i1, i2 := strings.Index(body, "Alpha"), strings.Index(body, "Bravo"), strings.Index(body, "Write to us")
	if !(i0 > 0 && i0 < i1 && i1 < i2) || strings.Count(body, `class="layout-col"`) != 3 {
		t.Errorf("the columns are in order: %d %d %d, columns %d", i0, i1, i2, strings.Count(body, `class="layout-col"`))
	}
	if strings.Index(body, "Bravo") > strings.Index(body, "Click &lt;me&gt;") {
		t.Error("the middle column's blocks are in order")
	}
	if strings.Count(body, `<section id="block-`) != 1 || strings.Contains(body, `<section id="block-`+left) {
		t.Error("only the layout is a section: its blocks are divs inside it")
	}
	// the contact form inside the column is a working form with its spam token
	if !strings.Contains(body, `action="/contact"`) || !reFormToken.MatchString(body) || !strings.Contains(body, `name="block" value="`+form+`"`) {
		t.Error("the contact form in a column keeps its data")
	}
	if extra := foreignScripts(body); len(extra) != 0 {
		t.Errorf("unexpected scripts: %v", extra)
	}
	css := anon.get("/theme/default/theme.css")
	for _, want := range []string{".layout-3.ratio-wide-middle", ".layout .nested > .block-inner", ".reverse-mobile", ".pad-md > .block-inner"} {
		if !strings.Contains(css.Body, want) {
			t.Errorf("the stylesheet lacks %s", want)
		}
	}

	// the words inside columns are searchable; deleting a block takes its words out
	if r := anon.get("/search?q=bravo"); !strings.Contains(r.Body, "Public Columns") {
		t.Error("search finds words inside a column")
	}
	if r := anon.get("/search?q=alpha"); !strings.Contains(r.Body, "Public Columns") {
		t.Error("search finds the other column too")
	}
	admin.post("/admin/blocks/"+mid1+"/delete", nil)
	if r := anon.get("/search?q=bravo"); strings.Contains(r.Body, "Public Columns") {
		t.Error("a deleted block's words leave the index")
	}

	// a layout holding nothing, and one whose blocks were removed from the registry, still render
	admin.post("/admin/blocks/"+left+"/delete", nil)
	admin.post("/admin/blocks/"+mid2+"/delete", nil)
	admin.post("/admin/blocks/"+form+"/delete", nil)
	if r := anon.get("/cols"); r.Status != 200 || strings.Count(r.Body, `class="layout-col"`) != 3 {
		t.Errorf("an empty layout: %d", r.Status)
	}
	// a block left in a column the layout no longer has shows in the last one
	e.pool.Exec(context.Background(), `INSERT INTO blocks (page_id, parent_id, slot, position, type, props) VALUES ($1, $2, 3, 0, 'richtext', '{"content": "<p>Stranded</p>"}')`, pageID, layout)
	r := anon.get("/cols")
	if !strings.Contains(r.Body, "Stranded") {
		t.Error("a block in a column that isn't there is still shown")
	}
	if strings.Index(r.Body, "Stranded") < strings.LastIndex(r.Body, `class="layout-col"`) {
		t.Error("…in the last column")
	}
	// a layout inside a layout (which the editor never makes) is skipped, not nested forever
	e.pool.Exec(context.Background(), `INSERT INTO blocks (page_id, parent_id, slot, position, type, props) VALUES ($1, $2, 0, 5, 'layout', '{}')`, pageID, layout)
	if r := anon.get("/cols"); r.Status != 200 || strings.Count(r.Body, `class="layout `) != 1 {
		t.Errorf("a nested layout is skipped: %d", r.Status)
	}
}

func TestLayoutEditorIsAdminAndEditorOnlyAndCsrfProtected(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()
	_, layout := layoutPage(t, e, admin, "Access")
	child := e.addChild(t, admin, layout, 0, "richtext")

	for _, p := range []string{"/admin/blocks/" + layout + "/children", "/admin/blocks/" + child + "/slot"} {
		form := url.Values{"type": {"richtext"}, "slot": {"1"}, "dir": {"right"}}
		if r := anon.post(p, form); !strings.Contains(r.Location(), "/admin/login") {
			t.Errorf("a visitor posting to %s: %d", p, r.Status)
		}
		if r := admin.postNoToken(p, form, "X-CSRF-Token", ""); r.Status != 403 {
			t.Errorf("no CSRF token at %s: %d", p, r.Status)
		}
	}
	if e.sql(`SELECT count(*) FROM blocks WHERE parent_id = `+layout) != "1" || e.sql(`SELECT slot FROM blocks WHERE id = `+child) != "0" {
		t.Error("none of those may change anything")
	}
	// editors build pages, so they build columns too
	if r := editor.post("/admin/blocks/"+layout+"/children", url.Values{"type": {"button"}, "slot": {"1"}}); r.Status != 200 {
		t.Errorf("an editor: %d", r.Status)
	}
	if r := editor.post("/admin/blocks/"+child+"/slot", url.Values{"dir": {"right"}}); r.Status != 200 {
		t.Errorf("an editor moving a block: %d", r.Status)
	}
}

func TestLayoutsSurviveABackupRestore(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	pageID, layout := layoutPage(t, e, admin, "Backed Up")
	admin.post("/admin/blocks/"+layout, url.Values{"p.columns": {"3"}, "p.ratio": {"wide-left"}, "p.gap": {"sm"}, "p.valign": {"stretch"}})
	a := e.addChild(t, admin, layout, 0, "richtext")
	b := e.addChild(t, admin, layout, 2, "image")
	c := e.addChild(t, admin, layout, 2, "button")
	e.saveChild(t, admin, a, url.Values{"p.content": {"<p>Quokka paragraph</p>"}, "s.background": {"#abcdef"}})
	e.saveChild(t, admin, c, url.Values{"p.label": {"Go"}, "p.url": {"/x"}})
	_ = b
	admin.post("/admin/pages/"+pageID, url.Values{"title": {"Backed Up"}, "slug": {"backed-up"}, "status": {"published"}})

	snapshot := func() string {
		return e.siteSnapshot() + "\n####\n" + e.sql(`SELECT COALESCE(string_agg(b.type||'|'||b.slot||'|'||b.position||'|'||b.props::text||'|'||b.style::text||'|'||(b.parent_id IS NOT NULL)::text, E'\n' ORDER BY COALESCE(p.position, b.position), (b.parent_id IS NOT NULL), b.slot, b.position), '')
			FROM blocks b LEFT JOIN blocks p ON p.id = b.parent_id`)
	}
	// settle (old blocks gain their defaults), then compare a full round trip
	if r := admin.restoreUpload(t, []byte(admin.downloadBackup(false).Body), nil); r.Status != 200 {
		t.Fatalf("settle: %d", r.Status)
	}
	before := snapshot()
	raw := []byte(admin.downloadBackup(false).Body)

	files := unzip(t, raw)
	site := string(files["site.json"])
	for _, want := range []string{`"children"`, "Quokka paragraph", `"columns": "3"`} {
		if !strings.Contains(site, want) {
			t.Errorf("site.json lacks %s", want)
		}
	}
	var man backup.Manifest
	json.Unmarshal(files["manifest.json"], &man)
	if want, _ := strconv.Atoi(e.sql(`SELECT count(*) FROM blocks`)); man.Counts.Blocks != want {
		t.Errorf("the manifest counts %d blocks, the site has %d (nested blocks count too)", man.Counts.Blocks, want)
	}

	e.pool.Exec(context.Background(), `DELETE FROM pages WHERE slug = 'backed-up'`)
	if r := anon.get("/backed-up"); r.Status != 404 {
		t.Fatalf("test setup: %d", r.Status)
	}
	if r := admin.restoreUpload(t, raw, nil); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	if after := snapshot(); after != before {
		t.Errorf("layouts are not as they were.\nbefore:\n%s\n\nafter:\n%s", before, after)
	}
	newLayout := e.sql(`SELECT id FROM blocks WHERE type = 'layout' AND page_id = (SELECT id FROM pages WHERE slug = 'backed-up')`)
	if got := e.columns(newLayout, 3); got != "richtext |  | image,button" {
		t.Errorf("restored columns: %q", got)
	}
	if r := anon.get("/backed-up"); r.Status != 200 || !strings.Contains(r.Body, "Quokka paragraph") || strings.Count(r.Body, `class="layout-col"`) != 3 {
		t.Errorf("the restored page: %d", r.Status)
	}
	if r := anon.get("/search?q=quokka"); !strings.Contains(r.Body, "Backed Up") {
		t.Error("words inside restored columns are searchable")
	}
}
