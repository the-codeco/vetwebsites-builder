package app_test

import (
	"net/url"
	"strings"
	"testing"
)

// The editor's live preview shows what is being typed before it is saved. The draft is the person's own: nobody else's
// view of the page, and no visitor's, ever shows it, and it goes away when the block is saved.
func TestLivePreviewShowsUnsavedEditsToTheirOwnerOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	editor := e.newUser(admin, "ed@example.com", "editor")
	anon := e.client()

	id := newPage(t, admin, "Live")
	if r := savePage(t, admin, id, url.Values{"title": {"Live"}, "slug": {"live"}}); r.Status != 303 {
		t.Fatalf("publish: %d", r.Status)
	}
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"richtext"}})
	bid := e.blockID(id, "richtext")
	if r := admin.post("/admin/blocks/"+bid, url.Values{"p.content": {"<p>Saved words</p>"}}); r.Status != 200 {
		t.Fatalf("save: %d", r.Status)
	}
	saved := func(c *client) string { return c.get("/live").Body }

	// typing: the draft is accepted, nothing is saved
	r := admin.post("/admin/blocks/"+bid+"/draft", url.Values{"p.content": {"<p>Typed words</p>"}, "s.background": {"#112233"}})
	if r.Status != 204 {
		t.Fatalf("draft: %d %s", r.Status, r.Body)
	}
	if got := e.sql(`SELECT props->>'content' FROM blocks WHERE id = $1`, bid); !strings.Contains(got, "Saved words") {
		t.Errorf("a draft must not be saved: %q", got)
	}
	mine := saved(admin)
	if !strings.Contains(mine, "Typed words") || strings.Contains(mine, "Saved words") || !strings.Contains(mine, "#112233") {
		t.Errorf("the owner's preview should show the draft, with its style:\n%s", mine)
	}
	for who, c := range map[string]*client{"visitor": anon, "another editor": editor} {
		if body := saved(c); !strings.Contains(body, "Saved words") || strings.Contains(body, "Typed words") {
			t.Errorf("%s must see the saved page only", who)
		}
	}

	// the draft is cleaned like a save: unsafe markup never reaches the preview
	admin.post("/admin/blocks/"+bid+"/draft", url.Values{"p.content": {`<p>Hi</p><script>alert(1)</script>`}})
	if body := saved(admin); strings.Contains(body, "<script>alert") {
		t.Error("a draft was not cleaned")
	}

	// saving the block makes it the saved version and drops the draft
	admin.post("/admin/blocks/"+bid+"/draft", url.Values{"p.content": {"<p>Typed again</p>"}})
	admin.post("/admin/blocks/"+bid, url.Values{"p.content": {"<p>Second save</p>"}})
	if body := saved(anon); !strings.Contains(body, "Second save") {
		t.Error("the save was lost")
	}
	if body := saved(admin); !strings.Contains(body, "Second save") || strings.Contains(body, "Typed again") {
		t.Error("saving must clear the draft")
	}

	// opening the editor again starts from what is saved
	admin.post("/admin/blocks/"+bid+"/draft", url.Values{"p.content": {"<p>Abandoned</p>"}})
	if !strings.Contains(saved(admin), "Abandoned") {
		t.Fatal("draft not shown")
	}
	admin.get("/admin/pages/" + id)
	if body := saved(admin); strings.Contains(body, "Abandoned") {
		t.Error("an abandoned draft survived reopening the editor")
	}

	// who may send drafts, and for what
	if r := admin.post("/admin/blocks/999999/draft", url.Values{"p.content": {"x"}}); r.Status != 404 {
		t.Errorf("missing block: %d", r.Status)
	}
	if r := admin.postNoToken("/admin/blocks/"+bid+"/draft", url.Values{"p.content": {"x"}}); r.Status != 403 {
		t.Errorf("no token: %d", r.Status)
	}
	if r := anon.post("/admin/blocks/"+bid+"/draft", url.Values{"p.content": {"x"}}); r.Status == 204 {
		t.Error("a visitor can't send drafts")
	}
}
