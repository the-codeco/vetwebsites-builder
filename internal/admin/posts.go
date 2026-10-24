package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"webbuilder/internal/auth"
	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
	"webbuilder/internal/seo"
	"webbuilder/internal/site"
)

const (
	maxExcerpt  = 500
	maxBodySize = 200_000
	maxCatName  = 50
)

func (h *Handler) registerPosts(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/posts", authed(h.postsList))
	mux.Handle("POST /admin/posts", authed(h.postCreate))
	mux.Handle("GET /admin/posts/{id}", authed(h.postEdit))
	mux.Handle("POST /admin/posts/{id}", authed(h.postSave))
	mux.Handle("POST /admin/posts/{id}/delete", authed(h.postDelete))
	mux.Handle("GET /admin/categories", authed(h.categoriesPage))
	mux.Handle("POST /admin/categories", authed(h.categoryCreate))
	mux.Handle("POST /admin/categories/{id}", authed(h.categoryRename))
	mux.Handle("POST /admin/categories/{id}/delete", authed(h.categoryDelete))
}

// --- posts list / create ---

type postsData struct {
	Posts             []content.Post
	NewTitle, NewSlug string
}

func (h *Handler) renderPosts(w http.ResponseWriter, r *http.Request, status int, errMsg, title, slug string) {
	posts, err := h.Content.ListPosts(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "posts", pageData{
		Title: "Posts", User: id, CSRF: id.CSRF, Error: errMsg, Data: postsData{posts, title, slug},
	})
}

func (h *Handler) postsList(w http.ResponseWriter, r *http.Request) {
	h.renderPosts(w, r, 200, "", "", "")
}

func (h *Handler) postCreate(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.PostFormValue("title"))
	slug := strings.ToLower(strings.TrimSpace(r.PostFormValue("slug")))
	auto := slug == ""
	if auto {
		slug = site.Slugify(title)
	}
	switch {
	case title == "":
		h.renderPosts(w, r, 400, "Title is required.", title, slug)
		return
	case utf8.RuneCountInString(title) > maxTitle:
		h.renderPosts(w, r, 400, "Title is too long.", title, slug)
		return
	}
	if msg := h.limitProblem(r.Context(), "posts", 0); msg != "" {
		h.renderPosts(w, r, 400, msg, title, slug)
		return
	}
	if msg := site.CheckSlugFormat(slug); msg != "" {
		h.renderPosts(w, r, 400, msg, title, slug)
		return
	}
	user := auth.CurrentIdentity(r)
	candidate := slug
	var id int64
	var err error
	for i := 2; ; i++ {
		id, err = h.Content.CreatePost(r.Context(), candidate, title, user.UserID)
		if !errors.Is(err, content.ErrSlugTaken) || !auto || i > 20 {
			break
		}
		candidate = fmt.Sprintf("%s-%d", slug, i)
	}
	if errors.Is(err, content.ErrSlugTaken) {
		h.renderPosts(w, r, 409, "A post with that slug already exists.", title, slug)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/posts/%d", id), http.StatusSeeOther)
}

// --- post editor ---

type catChoice struct {
	ID      int64
	Name    string
	Checked bool
}

type postEditData struct {
	Post       content.Post
	Categories []catChoice
	SEO        seoView
}

func (h *Handler) renderPostEdit(w http.ResponseWriter, r *http.Request, status int, id int64, notice, errMsg string, override *content.Post, checked map[int64]bool) {
	post, err := h.Content.Post(r.Context(), id)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	cats, err := h.Content.Categories(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	if checked == nil {
		checked = map[int64]bool{}
		for _, c := range post.Categories {
			checked[c.ID] = true
		}
	}
	if override != nil {
		post = *override
	}
	choices := make([]catChoice, len(cats))
	for i, c := range cats {
		choices[i] = catChoice{c.ID, c.Name, checked[c.ID]}
	}
	view, err := h.newSEOView(r, post.SEO, post.Title, "/blog/"+post.Slug, excerptHint(post))
	if err != nil {
		serverError(w, err)
		return
	}
	ident := auth.CurrentIdentity(r)
	h.render(w, r, status, "post_edit", pageData{
		Title: "Edit " + post.Title, User: ident, CSRF: ident.CSRF, Notice: notice, Error: errMsg,
		Data: postEditData{post, choices, view},
	})
}

func (h *Handler) postEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	notice := ""
	if r.URL.Query().Get("saved") == "1" {
		notice = "Post saved."
	}
	h.renderPostEdit(w, r, 200, id, notice, "", nil, nil)
}

func (h *Handler) postSave(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	existing, err := h.Content.Post(r.Context(), id)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	in := existing // keeps ID, dates and categories for re-rendering on error
	in.Title = strings.TrimSpace(r.PostFormValue("title"))
	in.Slug = strings.ToLower(strings.TrimSpace(r.PostFormValue("slug")))
	in.Status = r.PostFormValue("status")
	in.Excerpt = strings.TrimSpace(r.PostFormValue("excerpt"))
	in.Cover = strings.TrimSpace(r.PostFormValue("cover"))
	in.Body = r.PostFormValue("body")
	var seoProblem string
	in.SEO, seoProblem = seoForm(r)
	var scheduleProblem string
	in.PublishAt, scheduleProblem = publishAtFrom(r, in.Status, time.Now())

	checked := map[int64]bool{}
	var catIDs []int64
	for _, v := range r.PostForm["category"] {
		if cid, err := strconv.ParseInt(v, 10, 64); err == nil {
			checked[cid] = true
			catIDs = append(catIDs, cid)
		}
	}

	fail := func(status int, msg string) { h.renderPostEdit(w, r, status, id, "", msg, &in, checked) }
	switch {
	case in.Title == "":
		fail(400, "Title is required.")
		return
	case utf8.RuneCountInString(in.Title) > maxTitle:
		fail(400, "Title is too long.")
		return
	case in.Status != "draft" && in.Status != "published":
		fail(400, "Choose a valid status.")
		return
	case utf8.RuneCountInString(in.Excerpt) > maxExcerpt:
		fail(400, fmt.Sprintf("Excerpt must be at most %d characters.", maxExcerpt))
		return
	case len(in.Body) > maxBodySize:
		fail(400, "The post is too long.")
		return
	case seoProblem != "":
		fail(400, seoProblem)
		return
	case scheduleProblem != "":
		fail(400, scheduleProblem)
		return
	case in.Cover != "" && blocks.SafeImageURL(in.Cover) == "":
		fail(400, "The cover image must be a path like /media/photo.jpg or an http(s):// address.")
		return
	}
	if msg := site.CheckSlugFormat(in.Slug); msg != "" {
		fail(400, msg)
		return
	}

	err = h.Content.UpdatePost(r.Context(), id, content.PostInput{
		Slug: in.Slug, Title: in.Title, Excerpt: in.Excerpt, Cover: blocks.SafeImageURL(in.Cover),
		Body: blocks.NormalizeRichText(in.Body), Status: in.Status, SEO: in.SEO, CategoryIDs: catIDs, PublishAt: in.PublishAt,
	})
	if errors.Is(err, content.ErrSlugTaken) {
		fail(409, "Another post already uses that slug.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/posts/%d?saved=1", id), http.StatusSeeOther)
}

func (h *Handler) postDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := h.Content.DeletePost(r.Context(), id); err != nil && !errors.Is(err, content.ErrNotFound) {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

// excerptHint is what search engines will show for a post with no description of its own.
func excerptHint(p content.Post) string {
	text := p.Excerpt
	if text == "" {
		text = blocks.PlainText(p.Body)
	}
	return seo.Shorten(text, 160)
}

// --- categories ---

func (h *Handler) renderCategories(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string) {
	cats, err := h.Content.Categories(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "categories", pageData{
		Title: "Categories", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Data: cats,
	})
}

func (h *Handler) categoriesPage(w http.ResponseWriter, r *http.Request) {
	notice := ""
	switch r.URL.Query().Get("done") {
	case "created":
		notice = "Category added."
	case "renamed":
		notice = "Category renamed."
	case "deleted":
		notice = "Category deleted."
	}
	h.renderCategories(w, r, 200, notice, "")
}

// categoryFields validates a category's submitted name and returns it with its slug.
func categoryFields(r *http.Request) (name, slug, problem string) {
	name = strings.TrimSpace(r.PostFormValue("name"))
	switch {
	case name == "":
		return "", "", "Name is required."
	case utf8.RuneCountInString(name) > maxCatName:
		return "", "", fmt.Sprintf("Name must be at most %d characters.", maxCatName)
	}
	slug = site.Slugify(name)
	if msg := site.CheckSlugFormat(slug); msg != "" {
		return "", "", "Use at least one letter or number in the name."
	}
	return name, slug, ""
}

func (h *Handler) categoryCreate(w http.ResponseWriter, r *http.Request) {
	name, slug, problem := categoryFields(r)
	if problem != "" {
		h.renderCategories(w, r, 400, "", problem)
		return
	}
	err := h.Content.CreateCategory(r.Context(), slug, name)
	if errors.Is(err, content.ErrSlugTaken) {
		h.renderCategories(w, r, 409, "", "A category with that name already exists.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/categories?done=created", http.StatusSeeOther)
}

func (h *Handler) categoryRename(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	name, slug, problem := categoryFields(r)
	if problem != "" {
		h.renderCategories(w, r, 400, "", problem)
		return
	}
	err := h.Content.RenameCategory(r.Context(), id, slug, name)
	switch {
	case errors.Is(err, content.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, content.ErrSlugTaken):
		h.renderCategories(w, r, 409, "", "A category with that name already exists.")
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/categories?done=renamed", http.StatusSeeOther)
}

func (h *Handler) categoryDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := h.Content.DeleteCategory(r.Context(), id); err != nil && !errors.Is(err, content.ErrNotFound) {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/categories?done=deleted", http.StatusSeeOther)
}
