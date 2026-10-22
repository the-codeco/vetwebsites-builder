package app_test

import (
	"encoding/xml"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestCategories(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")

	for name, v := range map[string]url.Values{"empty": {"name": {""}}, "symbols only": {"name": {"!!"}}, "too long": {"name": {strings.Repeat("a", 51)}}} {
		if r := admin.post("/admin/categories", v); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if r := admin.post("/admin/categories", url.Values{"name": {"Tech & Tools"}}); r.Status != 303 {
		t.Fatalf("create: %d", r.Status)
	}
	if got := e.sql(`SELECT slug FROM categories`); got != "tech-tools" {
		t.Errorf("slug: %q", got)
	}
	if r := admin.post("/admin/categories", url.Values{"name": {"tech tools"}}); r.Status != 409 {
		t.Errorf("duplicate: %d", r.Status)
	}
	id := e.sql(`SELECT id FROM categories`)
	if r := admin.post("/admin/categories/"+id, url.Values{"name": {"Gadgets"}}); r.Status != 303 {
		t.Errorf("rename: %d", r.Status)
	}
	if got := e.sql(`SELECT slug FROM categories`); got != "gadgets" {
		t.Errorf("renamed slug: %q", got)
	}
	if r := admin.post("/admin/categories/999999", url.Values{"name": {"x"}}); r.Status != 404 {
		t.Errorf("rename missing: %d", r.Status)
	}
	if r := admin.post("/admin/categories/"+id+"/delete", nil); r.Status != 303 {
		t.Errorf("delete: %d", r.Status)
	}
}

// publish saves a post with the given body and categories.
func publish(t *testing.T, c *client, id, title, slug, body string, cats ...string) response {
	t.Helper()
	v := url.Values{"title": {title}, "slug": {slug}, "status": {"published"}, "body": {body}}
	for _, cat := range cats {
		v.Add("category", cat)
	}
	r := c.post("/admin/posts/"+id, v)
	if r.Status != 303 {
		t.Fatalf("publish %q: %d", title, r.Status)
	}
	return r
}

func TestPostsAndPublicBlog(t *testing.T) {
	e := newEnv(t)
	admin := e.newAdmin("admin@example.com")
	anon := e.client()

	admin.post("/admin/categories", url.Values{"name": {"News"}})
	admin.post("/admin/categories", url.Values{"name": {"Tech & Tools"}})
	news := e.sql(`SELECT id FROM categories WHERE slug = 'news'`)
	tech := e.sql(`SELECT id FROM categories WHERE slug = 'tech-tools'`)

	// creating
	for name, v := range map[string]url.Values{"empty": {"title": {""}}, "bad slug": {"title": {"T"}, "slug": {"Bad Slug"}}} {
		if r := admin.post("/admin/posts", v); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	id := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {"Hello World"}}).Location())
	if got := e.sql(`SELECT slug FROM posts WHERE id = $1`, id); got != "hello-world" {
		t.Errorf("slug: %q", got)
	}
	id2 := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {"Hello World"}}).Location())
	if got := e.sql(`SELECT slug FROM posts WHERE id = $1`, id2); got != "hello-world-2" {
		t.Errorf("duplicate title: %q", got)
	}

	// drafts are private; the blog is empty for visitors
	if r := anon.get("/blog/hello-world"); r.Status != 404 {
		t.Errorf("draft post public: %d", r.Status)
	}
	if r := anon.get("/blog"); r.Status != 200 || strings.Contains(r.Body, "Hello World") || !strings.Contains(r.Body, "No posts yet") {
		t.Errorf("empty blog: %d", r.Status)
	}
	if r := admin.get("/blog/hello-world"); r.Status != 200 || !strings.Contains(r.Body, "Draft preview") {
		t.Errorf("draft preview: %d", r.Status)
	}

	// validation on save
	base := url.Values{"title": {"Hello World"}, "slug": {"hello-world"}, "status": {"draft"}}
	with := func(k, v string) url.Values {
		out := url.Values{}
		for key, vals := range base {
			out[key] = vals
		}
		out.Set(k, v)
		return out
	}
	for name, v := range map[string]url.Values{
		"bad status": with("status", "zzz"), "bad cover": with("cover", "javascript:alert(1)"),
		"bad slug": with("slug", "Not A Slug"), "long excerpt": with("excerpt", strings.Repeat("e", 501)),
		"no title": with("title", ""),
	} {
		if r := admin.post("/admin/posts/"+id, v); r.Status != 400 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
	if r := admin.post("/admin/posts/"+id, with("slug", "hello-world-2")); r.Status != 409 {
		t.Errorf("slug taken: %d", r.Status)
	}
	if r := admin.post("/admin/posts/999999", base); r.Status != 404 {
		t.Errorf("missing post: %d", r.Status)
	}

	// plain paragraphs mixed with toolbar formatting and hostile HTML
	body := "Plain first paragraph with <strong>bold</strong>.\n\n<h2>A heading</h2>\n\nSecond & last. <script>alert(1)</script><img src=x onerror=alert(2)> " +
		`<a href="javascript:alert(3)">bad</a> <a href="/about">good</a>`
	v := url.Values{"title": {"Hello World"}, "slug": {"hello-world"}, "status": {"published"}, "excerpt": {"A short & sweet intro"},
		"body": {body}, "cover": {"/media/2026/10/none.jpg"}}
	v.Add("category", news)
	v.Add("category", tech)
	if r := admin.post("/admin/posts/"+id, v); r.Status != 303 {
		t.Fatalf("publish: %d", r.Status)
	}
	post := anon.get("/blog/hello-world")
	if post.Status != 200 {
		t.Fatalf("published post: %d", post.Status)
	}
	for _, want := range []string{"<p>Plain first paragraph with <strong>bold</strong>.</p>", "<h2>A heading</h2>", "<p>Second &amp; last.",
		`href="/about"`, `content="A short &amp; sweet intro"`, `href="/blog/category/news"`, `Tech &amp; Tools`, `class="post-cover"`, "<time datetime="} {
		if !strings.Contains(post.Body, want) {
			t.Errorf("post page lacks %q", want)
		}
	}
	if extra := foreignScripts(post.Body); len(extra) != 0 {
		t.Errorf("post page has script tags besides the menu script: %v", extra)
	}
	for _, bad := range []string{"onerror", "javascript:", "Draft preview"} {
		if strings.Contains(post.Body, bad) {
			t.Errorf("post page contains %q", bad)
		}
	}

	// the publish date survives unpublishing and republishing
	first := e.sql(`SELECT published_at FROM posts WHERE id = $1`, id)
	admin.post("/admin/posts/"+id, url.Values{"title": {"Hello World"}, "slug": {"hello-world"}, "status": {"draft"}})
	if r := anon.get("/blog/hello-world"); r.Status != 404 {
		t.Errorf("unpublished post still public: %d", r.Status)
	}
	publish(t, admin, id, "Hello World", "hello-world", body, news)
	if again := e.sql(`SELECT published_at FROM posts WHERE id = $1`, id); again != first {
		t.Errorf("published_at changed: %s -> %s", first, again)
	}

	// pagination, ordering and categories
	for i := 1; i <= 12; i++ {
		pid := idFromLocation(t, admin.post("/admin/posts", url.Values{"title": {fmt.Sprintf("Bulk Post %d", i)}}).Location())
		publish(t, admin, pid, fmt.Sprintf("Bulk Post %d", i), fmt.Sprintf("bulk-post-%d", i), fmt.Sprintf("Body %d.", i), tech)
	}
	cards := func(path string) int { return strings.Count(anon.get(path).Body, `class="postcard"`) }
	if n := cards("/blog"); n != 10 {
		t.Errorf("page 1 has %d cards", n)
	}
	if n := cards("/blog?page=2"); n != 3 {
		t.Errorf("page 2 has %d cards", n)
	}
	p1 := anon.get("/blog")
	if !strings.Contains(p1.Body, "Page 1 of 2") || !strings.Contains(p1.Body, "Older →") || strings.Contains(p1.Body, "← Newer") {
		t.Error("page 1 navigation wrong")
	}
	if p2 := anon.get("/blog?page=2"); !strings.Contains(p2.Body, "← Newer") || !strings.Contains(p2.Body, `href="/blog"`) || strings.Contains(p2.Body, "Older →") {
		t.Error("page 2 navigation wrong")
	}
	for path, want := range map[string]int{"/blog?page=3": 404, "/blog?page=0": 200, "/blog?page=abc": 200, "/blog?page=-5": 200} {
		if r := anon.get(path); r.Status != want {
			t.Errorf("GET %s: %d, want %d", path, r.Status, want)
		}
	}
	titles := regexp.MustCompile(`<h3><a href="/blog/([a-z0-9-]+)"`).FindAllStringSubmatch(p1.Body, -1)
	if len(titles) != 10 || titles[0][1] != "bulk-post-12" || titles[1][1] != "bulk-post-11" {
		t.Errorf("newest first expected: %v", titles)
	}
	if n := cards("/blog/category/tech-tools"); n != 10 {
		t.Errorf("tech category page 1: %d cards", n)
	}
	if n := cards("/blog/category/news"); n != 1 {
		t.Errorf("news category: %d cards", n)
	}
	for _, p := range []string{"/blog/category/nope", "/blog/category/Bad_Slug", "/blog/nope", "/blog/Bad_Slug"} {
		if r := anon.get(p); r.Status != 404 {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	if r := anon.get("/"); !strings.Contains(r.Body, `<link rel="alternate" type="application/rss+xml"`) {
		t.Error("pages should advertise the feed")
	}

	// RSS: valid XML, newest first, escaped, only published posts, absolute links
	admin.post("/admin/posts", url.Values{"title": {"Secret <draft> & more"}})
	feed := anon.get("/rss.xml")
	if feed.Status != 200 || !strings.HasPrefix(feed.Header.Get("Content-Type"), "application/rss+xml") {
		t.Fatalf("feed: %d %s", feed.Status, feed.Header.Get("Content-Type"))
	}
	var rss struct {
		Channel struct {
			Title string `xml:"title"`
			Link  string `xml:"link"`
			Items []struct {
				Title string `xml:"title"`
				Link  string `xml:"link"`
				GUID  string `xml:"guid"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(feed.Body), &rss); err != nil {
		t.Fatalf("feed is not valid XML: %v", err)
	}
	if len(rss.Channel.Items) != 13 || rss.Channel.Items[0].Title != "Bulk Post 12" {
		t.Errorf("feed has %d items, first %q", len(rss.Channel.Items), rss.Channel.Items[0].Title)
	}
	if !strings.HasPrefix(rss.Channel.Items[0].Link, e.srv.URL+"/blog/") || rss.Channel.Link != e.srv.URL+"/" {
		t.Errorf("feed links must be absolute: %q %q", rss.Channel.Items[0].Link, rss.Channel.Link)
	}
	if strings.Contains(feed.Body, "Secret") {
		t.Error("drafts must not appear in the feed")
	}
	// a forged Host header can't inject markup into absolute URLs
	if r := anon.get("/rss.xml", "Host", `evil.com"><script>`); r.Status == 200 && strings.Contains(r.Body, "<script>") {
		t.Error("Host header injected into the feed")
	}

	// the "Latest posts" block on a normal page
	pageID := idFromLocation(t, admin.post("/admin/pages", url.Values{"title": {"News Page"}}).Location())
	admin.post("/admin/pages/"+pageID+"/blocks", url.Values{"type": {"postlist"}})
	admin.post("/admin/blocks/"+e.blockID(pageID, "postlist"), url.Values{"p.heading": {"What is new"}, "p.count": {"2"}})
	admin.post("/admin/pages/"+pageID, url.Values{"title": {"News Page"}, "slug": {"news-page"}, "status": {"published"}})
	if page := anon.get("/news-page"); !strings.Contains(page.Body, "<h2>What is new</h2>") || strings.Count(page.Body, `class="postcard"`) != 2 || !strings.Contains(page.Body, "bulk-post-12") {
		t.Errorf("latest posts block: cards=%d", strings.Count(page.Body, `class="postcard"`))
	}
	admin.post("/admin/blocks/"+e.blockID(pageID, "postlist"), url.Values{"p.count": {"999"}})
	if got := e.sql(`SELECT props->>'count' FROM blocks WHERE page_id = $1`, pageID); got != "12" {
		t.Errorf("count must be clamped to 12, got %s", got)
	}

	// deleting
	if r := admin.post("/admin/posts/"+id+"/delete", nil); r.Status != 303 {
		t.Errorf("delete post: %d", r.Status)
	}
	if r := anon.get("/blog/hello-world"); r.Status != 404 {
		t.Errorf("deleted post still served: %d", r.Status)
	}
}
