package content

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Category struct {
	ID         int64
	Slug, Name string
	Posts      int // posts attached (any status)
}

type Post struct {
	ID                                int64
	Slug, Title, Excerpt, Body, Cover string
	Status                            string
	SEO                               SEO
	PublishedAt                       *time.Time
	PublishAt                         *time.Time // a draft that goes live at this time
	UpdatedAt                         time.Time
	Categories                        []Category
}

// Date is the date shown to readers: when it was published, else when last edited.
func (p Post) Date() time.Time {
	if p.PublishedAt != nil {
		return *p.PublishedAt
	}
	return p.UpdatedAt
}

type PostInput struct {
	Slug, Title, Excerpt, Body, Cover, Status string
	SEO                                       SEO
	CategoryIDs                               []int64
	PublishAt                                 *time.Time // for a draft: when to publish it
}

// listCols omits the body (it can be large) but keeps a snippet so cards can
// derive an excerpt when the author didn't write one.
const listCols = `p.id, p.slug, p.title, p.excerpt,
	CASE WHEN p.excerpt = '' THEN left(p.body, 2000) ELSE '' END,
	p.cover_image, p.status, p.seo, p.published_at, p.updated_at, p.publish_at`

const fullCols = `p.id, p.slug, p.title, p.excerpt, p.body, p.cover_image, p.status, p.seo, p.published_at, p.updated_at, p.publish_at`

func scanPost(row pgx.Row) (Post, error) {
	var p Post
	var raw map[string]any
	err := row.Scan(&p.ID, &p.Slug, &p.Title, &p.Excerpt, &p.Body, &p.Cover, &p.Status, &raw, &p.PublishedAt, &p.UpdatedAt, &p.PublishAt)
	p.SEO = SEOFrom(raw)
	return p, err
}

func (s *Store) scanPosts(ctx context.Context, rows pgx.Rows) ([]Post, error) {
	defer rows.Close()
	var out []Post
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, s.loadCategories(ctx, out)
}

// loadCategories fills Categories for every post with one query.
func (s *Store) loadCategories(ctx context.Context, posts []Post) error {
	if len(posts) == 0 {
		return nil
	}
	ids := make([]int64, len(posts))
	index := make(map[int64]int, len(posts))
	for i, p := range posts {
		ids[i] = p.ID
		index[p.ID] = i
	}
	rows, err := s.pool.Query(ctx, `
		SELECT pc.post_id, c.id, c.slug, c.name FROM post_categories pc
		JOIN categories c ON c.id = pc.category_id
		WHERE pc.post_id = ANY($1) ORDER BY lower(c.name)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var postID int64
		var c Category
		if err := rows.Scan(&postID, &c.ID, &c.Slug, &c.Name); err != nil {
			return err
		}
		i := index[postID]
		posts[i].Categories = append(posts[i].Categories, c)
	}
	return rows.Err()
}

// ListPosts returns every post (admin view), newest first.
func (s *Store) ListPosts(ctx context.Context) ([]Post, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+listCols+` FROM posts p ORDER BY COALESCE(p.published_at, p.updated_at) DESC, p.id DESC`)
	if err != nil {
		return nil, err
	}
	return s.scanPosts(ctx, rows)
}

func (s *Store) Post(ctx context.Context, id int64) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx, `SELECT `+fullCols+` FROM posts p WHERE p.id = $1`, id))
	if err != nil {
		return Post{}, notFound(err)
	}
	ps := []Post{p}
	if err := s.loadCategories(ctx, ps); err != nil {
		return Post{}, err
	}
	return ps[0], nil
}

func (s *Store) PostBySlug(ctx context.Context, slug string) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx, `SELECT `+fullCols+` FROM posts p WHERE p.slug = $1`, slug))
	if err != nil {
		return Post{}, notFound(err)
	}
	ps := []Post{p}
	if err := s.loadCategories(ctx, ps); err != nil {
		return Post{}, err
	}
	return ps[0], nil
}

const publishedWhere = `p.status = 'published' AND ($1::text = '' OR EXISTS (
	SELECT 1 FROM post_categories pc JOIN categories c ON c.id = pc.category_id
	WHERE pc.post_id = p.id AND c.slug = $1::text))`

// PublishedPosts returns one page of published posts (optionally within a
// category) newest first, plus the total number matching.
func (s *Store) PublishedPosts(ctx context.Context, categorySlug string, limit, offset int) ([]Post, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM posts p WHERE `+publishedWhere, categorySlug).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+listCols+` FROM posts p WHERE `+publishedWhere+`
		ORDER BY p.published_at DESC, p.id DESC LIMIT $2 OFFSET $3`, categorySlug, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	posts, err := s.scanPosts(ctx, rows)
	return posts, total, err
}

func (s *Store) CreatePost(ctx context.Context, slug, title string, authorID int64) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO posts (slug, title, author_id) VALUES ($1, $2, NULLIF($3, 0)) RETURNING id`,
		slug, title, authorID).Scan(&id)
	if isUnique(err) {
		return 0, ErrSlugTaken
	}
	return id, err
}

// UpdatePost saves a post. published_at is set the first time it is published
// and kept afterwards, so unpublishing and republishing keeps the original date.
func (s *Store) UpdatePost(ctx context.Context, id int64, in PostInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var oldSlug string
	if err := tx.QueryRow(ctx, `SELECT slug FROM posts WHERE id = $1 FOR UPDATE`, id).Scan(&oldSlug); err != nil {
		return notFound(err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE posts SET slug = $2, title = $3, excerpt = $4, body = $5, cover_image = $6, status = $7::text, seo = $8,
		       published_at = CASE WHEN $7::text = 'published' AND published_at IS NULL THEN now() ELSE published_at END,
		       publish_at = CASE WHEN $7::text = 'draft' THEN $9::timestamptz ELSE NULL END,
		       updated_at = now()
		WHERE id = $1`, id, in.Slug, in.Title, in.Excerpt, in.Body, in.Cover, in.Status, in.SEO.asJSON(), in.PublishAt)
	if isUnique(err) {
		return ErrSlugTaken
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if oldSlug != in.Slug {
		if err := moveAddress(ctx, tx, "/blog/"+oldSlug, "/blog/"+in.Slug); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM post_categories WHERE post_id = $1`, id); err != nil {
		return err
	}
	// selecting from categories silently drops ids that don't exist
	if _, err := tx.Exec(ctx,
		`INSERT INTO post_categories (post_id, category_id) SELECT $1, id FROM categories WHERE id = ANY($2)`,
		id, in.CategoryIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeletePost(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// --- categories ---

func (s *Store) Categories(ctx context.Context) ([]Category, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.slug, c.name, (SELECT count(*) FROM post_categories pc WHERE pc.category_id = c.id)
		FROM categories c ORDER BY lower(c.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Posts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CategoryBySlug(ctx context.Context, slug string) (Category, error) {
	var c Category
	err := s.pool.QueryRow(ctx, `SELECT id, slug, name FROM categories WHERE slug = $1`, slug).Scan(&c.ID, &c.Slug, &c.Name)
	return c, notFound(err)
}

func (s *Store) CreateCategory(ctx context.Context, slug, name string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO categories (slug, name) VALUES ($1, $2)`, slug, name)
	if isUnique(err) {
		return ErrSlugTaken
	}
	return err
}

func (s *Store) RenameCategory(ctx context.Context, id int64, slug, name string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE categories SET slug = $2, name = $3 WHERE id = $1`, id, slug, name)
	if isUnique(err) {
		return ErrSlugTaken
	}
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) DeleteCategory(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM categories WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
