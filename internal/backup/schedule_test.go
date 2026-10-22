package backup

import (
	"testing"
	"time"
)

func TestInspectKeepsOnlyFutureSchedulesOfDrafts(t *testing.T) {
	s := testService(t)
	c := goodContent()
	future := time.Now().Add(24 * time.Hour).UTC()
	past := time.Now().Add(-24 * time.Hour).UTC()
	far := time.Now().Add(5 * 366 * 24 * time.Hour).UTC()
	c.Pages = []Page{
		{Slug: "ok", Title: "ok", Status: "draft", PublishAt: &future},
		{Slug: "past", Title: "past", Status: "draft", PublishAt: &past},
		{Slug: "far", Title: "far", Status: "draft", PublishAt: &far},
		{Slug: "live", Title: "live", Status: "published", PublishAt: &future},
	}
	post := func(slug, status string, at *time.Time) Post {
		return Post{Slug: slug, Title: slug, Status: status, PublishAt: at, UpdatedAt: time.Now(), Categories: []string{}}
	}
	c.Posts = []Post{post("ok", "draft", &future), post("past", "draft", &past), post("live", "published", &future)}
	a, err := inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]*time.Time{}
	for _, p := range a.Content.Pages {
		pages[p.Slug] = p.PublishAt
	}
	if pages["ok"] == nil || !pages["ok"].After(time.Now()) || pages["past"] != nil || pages["far"] != nil || pages["live"] != nil {
		t.Errorf("pages: %v", pages)
	}
	posts := map[string]*time.Time{}
	for _, p := range a.Content.Posts {
		posts[p.Slug] = p.PublishAt
	}
	if posts["ok"] == nil || posts["past"] != nil || posts["live"] != nil {
		t.Errorf("posts: %v", posts)
	}
}
