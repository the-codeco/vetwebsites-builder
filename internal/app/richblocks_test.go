package app_test

import (
	"net/url"
	"strings"
	"testing"
)

func TestPricingFaqTabsVideoMapBlocks(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	id := newPage(t, admin, "Rich Page")

	// every new block type can be added, and the editor offers each one
	editor := admin.get("/admin/pages/" + id).Body
	for _, typ := range []string{"pricing", "testimonials", "faq", "tabs", "video", "map"} {
		if !strings.Contains(editor, `value="`+typ+`"`) {
			t.Errorf("the editor does not offer %s", typ)
		}
		r := admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {typ}})
		if r.Status != 200 || !strings.Contains(r.Body, `id="blk-`) {
			t.Fatalf("add %s: %d", typ, r.Status)
		}
	}

	save := func(typ string, v url.Values) {
		t.Helper()
		if r := admin.post("/admin/blocks/"+e.blockID(id, typ), v); r.Status != 200 || !strings.Contains(r.Body, "Saved") {
			t.Fatalf("save %s: %d", typ, r.Status)
		}
	}
	save("pricing", url.Values{"p.heading": {"Our plans"}, "p.count": {"2"}, "p.plan1_name": {"Solo"}, "p.plan1_price": {"$5"}, "p.plan1_features": {"Zebrafeature\nSecond"},
		"p.plan1_button_label": {"Join"}, "p.plan1_button_url": {"/join"}, "p.plan2_name": {"Team"}, "p.plan2_featured": {"on"}, "p.plan2_button_label": {"Bad"}, "p.plan2_button_url": {"javascript:alert(1)"}})
	save("testimonials", url.Values{"p.items.0.quote": {"Wonderful <script>alert(1)</script>"}, "p.items.0.name": {"Pat"}, "p.items.0.photo": {"javascript:alert(2)"}})
	save("faq", url.Values{"p.items.0.question": {"Do you deliver?"}, "p.items.0.answer": {`<p>Yes, <b>everywhere</b> <img src=x onerror=alert(3)></p>`}, "p.open_first": {"on"}})
	save("tabs", url.Values{"p.items.0.title": {"Overview"}, "p.items.0.content": {"First tab"}, "p.items.1.title": {"Details"}, "p.items.1.content": {"Second tab"}})
	save("video", url.Values{"p.url": {"https://www.youtube.com/watch?v=dQw4w9WgXcQ"}, "p.title": {"Welcome film"}, "p.caption": {"Meet the team"}})
	save("map", url.Values{"p.coordinates": {"51.5007, -0.1246"}, "p.zoom": {"99"}, "p.title": {"Find us"}, "p.address": {"Westminster"}})
	if got := e.sql(`SELECT props->>'zoom' FROM blocks WHERE type = 'map'`); got != "19" {
		t.Errorf("map zoom must be clamped to 19, got %s", got)
	}

	if r := admin.post("/admin/pages/"+id, url.Values{"title": {"Rich Page"}, "slug": {"rich"}, "status": {"published"}}); r.Status != 303 {
		t.Fatalf("publish: %d", r.Status)
	}
	page := anon.get("/rich")
	if page.Status != 200 {
		t.Fatalf("page: %d", page.Status)
	}
	for _, want := range []string{
		`<h2>Our plans</h2>`, `<h3>Solo</h3>`, `<li>Zebrafeature</li>`, `href="/join">Join</a>`, `plan plan-featured`,
		`class="testimonial"`, `<strong>Pat</strong>`,
		`<summary>Do you deliver?</summary>`, `<details class="faq-item" open>`, `<b>everywhere</b>`,
		`data-tabs`, `class="tab-title"`,
		`src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0"`, `title="Welcome film"`, `Meet the team`,
		`src="https://www.openstreetmap.org/export/embed.html?bbox=`, `title="Find us"`, `View larger map`,
	} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	for _, not := range []string{"javascript:", "onerror", "<script>alert", ">Bad</a>"} {
		if strings.Contains(page.Body, not) {
			t.Errorf("the page contains %q", not)
		}
	}
	if extra := foreignScripts(page.Body); len(extra) != 0 {
		t.Errorf("unexpected scripts: %v", extra)
	}

	// the page's security policy lets exactly the embeds through, and nothing else may be framed
	csp := page.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-src https://www.youtube-nocookie.com https://player.vimeo.com https://www.openstreetmap.org") ||
		strings.Contains(csp, "frame-src *") || !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Errorf("public CSP: %q", csp)
	}
	if got := anon.get("/blog").Header.Get("Content-Security-Policy"); got != csp {
		t.Errorf("every public page has the same policy: %q", got)
	}

	// the new blocks' words are found by the site search; their screen-reader labels and button text are not
	if r := anon.get("/search?q=deliver"); !strings.Contains(r.Body, "Rich Page") {
		t.Error("the FAQ question should be searchable")
	}
	if r := anon.get("/search?q=zebrafeature"); !strings.Contains(r.Body, "Rich Page") {
		t.Error("a pricing feature should be searchable")
	}
	if r := anon.get("/search?q=welcome+film"); strings.Contains(r.Body, "Rich Page") {
		t.Error("a video's accessible title is not page content")
	}

	// an address that isn't allowed shows nothing
	save("video", url.Values{"p.url": {"https://evil.example/video"}, "p.caption": {"Meet the team"}})
	if body := anon.get("/rich").Body; strings.Contains(body, "evil.example") || strings.Contains(body, "Meet the team") || strings.Contains(body, "youtube-nocookie") {
		t.Error("a video from another site must not be embedded")
	}
}

func TestGoogleMapByAddress(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()
	id := newPage(t, admin, "Visit")
	admin.post("/admin/pages/"+id, url.Values{"title": {"Visit"}, "slug": {"visit"}, "status": {"published"}})
	admin.post("/admin/pages/"+id+"/blocks", url.Values{"type": {"map"}})
	m := e.blockID(id, "map")

	editor := admin.get("/admin/pages/" + id).Body
	for _, want := range []string{`name="p.provider"`, `value="google"`, `name="p.place"`} {
		if !strings.Contains(editor, want) {
			t.Errorf("the map editor lacks %s", want)
		}
	}
	// a new map has no position yet; by default it is OpenStreetMap
	if body := anon.get("/visit").Body; strings.Contains(body, "<iframe") {
		t.Error("an empty map shows nothing")
	}
	if r := admin.post("/admin/blocks/"+m, url.Values{"p.provider": {"google"}, "p.place": {`221B Baker Street, London "NW1" <script>alert(1)</script>`}, "p.zoom": {"15"}, "p.title": {"Where we are"}}); r.Status != 200 || !strings.Contains(r.Body, "Saved") {
		t.Fatalf("save: %d", r.Status)
	}
	page := anon.get("/visit")
	for _, want := range []string{`src="https://www.google.com/maps?q=221B`, `z=15&amp;output=embed`, `title="Where we are"`, `221B Baker Street, London &#34;NW1&#34; &lt;script&gt;`} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("the page lacks %s\n%s", want, page.Body)
		}
	}
	if strings.Contains(page.Body, "<script>alert") || strings.Contains(page.Body, "openstreetmap") {
		t.Error("the address is text, and the other service is not used")
	}
	if csp := page.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src https://www.youtube-nocookie.com https://player.vimeo.com https://www.openstreetmap.org https://www.google.com") {
		t.Errorf("the page's policy allows exactly the map hosts: %q", csp)
	}
	if extra := foreignScripts(page.Body); len(extra) != 0 {
		t.Errorf("unexpected scripts: %v", extra)
	}
	if r := anon.get("/search?q=baker+street"); !strings.Contains(r.Body, "Visit") {
		t.Error("the address is searchable")
	}
	// back to OpenStreetMap, and the address is kept for later
	admin.post("/admin/blocks/"+m, url.Values{"p.provider": {"osm"}, "p.place": {"221B Baker Street"}, "p.coordinates": {"51.5237, -0.1585"}})
	if body := anon.get("/visit").Body; !strings.Contains(body, "openstreetmap.org/export") || strings.Contains(body, "google.com/maps") {
		t.Error("switching service switches the map")
	}
}
