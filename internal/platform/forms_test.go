package platform_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"webbuilder/internal/config"
)

const siteOrigin = "https://www.acme.example"

// formCall sends a request to a published website's forms the way a visitor's browser does from the published site.
func (c *client) formCall(method, path string, v url.Values, origin string) (response, map[string]any) {
	c.t.Helper()
	var body *strings.Reader
	if v != nil {
		body = strings.NewReader(v.Encode())
	} else {
		body = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, c.base+path, body)
	if v != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	}
	r := c.do(req, nil)
	var out map[string]any
	json.Unmarshal([]byte(r.Body), &out)
	return r, out
}

func TestPublishedFormsTalkToTheBuilder(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.BaseURL = "https://builder.example.test" })
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	populate(t, e, ann)
	e.pool.Exec(t.Context(), `UPDATE sites SET public_url = $1 WHERE name = 'Acme Dental'`, siteOrigin)
	token := e.scalar(`SELECT public_token FROM sites WHERE name = 'Acme Dental'`)
	base := "/f/" + token
	visitor := e.client() // nobody is logged in

	// the static copy carries the script that makes its forms work
	files := listFiles(t, e.buildSite(ann, "Acme Dental"))
	if _, ok := files["forms.js"]; !ok {
		t.Error("the copy lacks forms.js")
	}
	if !strings.Contains(string(files["contact-us.html"]), `<script src="/forms.js" defer></script>`) {
		t.Error("the contact page doesn't load forms.js")
	}
	if strings.Contains(string(files["about-us.html"]), "forms.js") {
		t.Error("a page without a form doesn't need forms.js")
	}
	if !strings.Contains(string(files["contact-us.html"]), `data-success="`) {
		t.Error("the form carries its thank-you text")
	}

	// who may ask
	if r, _ := visitor.formCall("GET", "/f/nosuchtoken/token", nil, siteOrigin); r.Status != 404 {
		t.Errorf("an unknown website: %d", r.Status)
	}
	if r, _ := visitor.formCall("GET", base+"/token", nil, "https://evil.example"); r.Status != 403 {
		t.Errorf("another site's page: %d", r.Status)
	}
	pre, _ := visitor.formCall("OPTIONS", base+"/contact", nil, siteOrigin)
	if pre.Status != 204 || pre.Header.Get("Access-Control-Allow-Origin") != siteOrigin || !strings.Contains(pre.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("preflight: %d %v", pre.Status, pre.Header)
	}

	// tokens, fetched when the page opens
	r, toks := visitor.formCall("GET", base+"/token", nil, siteOrigin)
	if r.Status != 200 || r.Header.Get("Access-Control-Allow-Origin") != siteOrigin || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("token: %d %v", r.Status, r.Header)
	}
	ct, _ := toks["contact"].(string)
	st, _ := toks["subscribe"].(string)
	if ct == "" || st == "" {
		t.Fatalf("tokens: %s", r.Body)
	}

	msg := func(tok string) url.Values {
		return url.Values{"t": {tok}, "name": {"Pat"}, "email": {"pat@example.com"}, "message": {"Do you take new patients?"}, "from": {"/contact-us"}, "block": {"1"}}
	}
	// a form sent at once is turned away, like on the live site
	if r, out := visitor.formCall("POST", base+"/contact", msg(ct), siteOrigin); r.Status != 400 || out["ok"] != false {
		t.Errorf("too fast: %d %s", r.Status, r.Body)
	}
	time.Sleep(3200 * time.Millisecond)

	// a real message gets through, and lands in the website's inbox
	if r, out := visitor.formCall("POST", base+"/contact", msg(ct), siteOrigin); r.Status != 200 || out["ok"] != true {
		t.Fatalf("contact: %d %s", r.Status, r.Body)
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.messages WHERE email = 'pat@example.com'`); got != "1" {
		t.Errorf("messages stored: %s", got)
	}
	// problems come back as text for the visitor
	bad := msg(ct)
	bad.Set("email", "not-an-email")
	if r, out := visitor.formCall("POST", base+"/contact", bad, siteOrigin); r.Status != 400 || out["error"] == "" {
		t.Errorf("bad email: %d %s", r.Status, r.Body)
	}
	if r, out := visitor.formCall("POST", base+"/contact", msg("forged.token"), siteOrigin); r.Status != 400 || out["ok"] != false {
		t.Errorf("forged token: %d %s", r.Status, r.Body)
	}
	// a bot that fills the trap field is told it worked, and nothing is stored
	trap := msg(ct)
	trap.Set("website", "http://spam.example")
	trap.Set("message", "buy my pills")
	if r, out := visitor.formCall("POST", base+"/contact", trap, siteOrigin); r.Status != 200 || out["ok"] != true {
		t.Errorf("trap: %d %s", r.Status, r.Body)
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.messages`); got != "1" {
		t.Errorf("a bot's message was stored: %s", got)
	}
	// another site's page can't post here
	if r, _ := visitor.formCall("POST", base+"/contact", msg(ct), "https://evil.example"); r.Status != 403 {
		t.Errorf("forged origin: %d", r.Status)
	}

	// newsletter signups
	sub := url.Values{"t": {st}, "email": {"Pat@Example.com"}, "from": {"/contact-us"}, "block": {"2"}}
	if r, out := visitor.formCall("POST", base+"/subscribe", sub, siteOrigin); r.Status != 200 || out["ok"] != true {
		t.Fatalf("subscribe: %d %s", r.Status, r.Body)
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.subscribers WHERE email = 'pat@example.com'`); got != "1" {
		t.Errorf("subscribers: %s", got)
	}

	// without JavaScript a form still gets a readable page, not JSON
	req, _ := http.NewRequest("POST", visitor.base+base+"/subscribe", strings.NewReader(sub.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", siteOrigin)
	if page := visitor.do(req, nil); !strings.Contains(page.Header.Get("Content-Type"), "text/html") || !strings.Contains(page.Body, "Thank you for subscribing") {
		t.Errorf("plain form: %d %s", page.Status, page.Body)
	}

	// search: the published copy's page asks the builder, which answers like the live site
	sp := string(files["search.html"])
	if !strings.Contains(sp, `data-wb-search="https://builder.example.test/f/`+token+`/search"`) {
		t.Error("the search form doesn't name the builder")
	}
	if _, ok := files["search.js"]; !ok {
		t.Error("the copy lacks search.js")
	}
	r, found := visitor.formCall("GET", base+"/search?q=orthodontics", nil, siteOrigin)
	items, _ := found["items"].([]any)
	if r.Status != 200 || found["total"] != float64(12) || found["pages"] != float64(2) || len(items) != 10 {
		t.Fatalf("search: %d %s", r.Status, r.Body)
	}
	first, _ := items[0].(map[string]any)
	parts, _ := first["snippet"].([]any)
	marked := false
	for _, p := range parts {
		if m, _ := p.(map[string]any); m["m"] == true && strings.EqualFold(m["t"].(string), "orthodontics") {
			marked = true
		}
	}
	if first["kind"] != "post" || !strings.HasPrefix(first["path"].(string), "/blog/post-") || !marked {
		t.Errorf("first result: %v", first)
	}
	if r, p2 := visitor.formCall("GET", base+"/search?q=orthodontics&page=2", nil, siteOrigin); r.Status != 200 || len(p2["items"].([]any)) != 2 {
		t.Errorf("page 2: %d %s", r.Status, r.Body)
	}
	if _, none := visitor.formCall("GET", base+"/search?q=zzzzqqq", nil, siteOrigin); none["total"] != float64(0) {
		t.Errorf("nothing found: %v", none)
	}
	if _, draft := visitor.formCall("GET", base+"/search?q=Unpublished", nil, siteOrigin); draft["total"] != float64(0) {
		t.Errorf("a draft was found: %v", draft)
	}
	if r, empty := visitor.formCall("GET", base+"/search?q=", nil, siteOrigin); r.Status != 200 || empty["total"] != float64(0) {
		t.Errorf("empty question: %d", r.Status)
	}
	if r, _ := visitor.formCall("GET", base+"/search?q=orthodontics", nil, "https://evil.example"); r.Status != 403 {
		t.Errorf("search from another site: %d", r.Status)
	}
	// the question can't be used to run more than a few searches a minute
	limited := false
	for i := 0; i < 40 && !limited; i++ {
		rr, _ := visitor.formCall("GET", base+"/search?q=teeth", nil, siteOrigin)
		limited = rr.Status == 429
	}
	if !limited {
		t.Error("searching is not rate limited")
	}

	// a suspended website's forms are off
	e.pool.Exec(t.Context(), `UPDATE sites SET status = 'suspended' WHERE name = 'Acme Dental'`)
	if r, _ := visitor.formCall("GET", base+"/token", nil, siteOrigin); r.Status != 404 {
		t.Errorf("suspended: %d", r.Status)
	}
}
