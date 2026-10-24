package content

import "context"

// Overview is what the admin dashboard shows at a glance.
type Overview struct {
	PagesPublished, PagesDraft int
	PostsPublished, PostsDraft int
	Media, Users               int
	RecentPages                []Page
	RecentPosts                []Post
}

// Overview gathers the dashboard's counts in one query, plus the most recently
// edited pages and posts.
func (s *Store) Overview(ctx context.Context, recent int) (Overview, error) {
	var o Overview
	err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM pages WHERE status = 'published'),
		  (SELECT count(*) FROM pages WHERE status = 'draft'),
		  (SELECT count(*) FROM posts WHERE status = 'published'),
		  (SELECT count(*) FROM posts WHERE status = 'draft'),
		  (SELECT count(*) FROM media),
		  (SELECT count(*) FROM users)`).
		Scan(&o.PagesPublished, &o.PagesDraft, &o.PostsPublished, &o.PostsDraft, &o.Media, &o.Users)
	if err != nil {
		return o, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.slug, p.title, p.status, p.updated_at,
		       (SELECT count(*) FROM blocks b WHERE b.page_id = p.id)
		FROM pages p ORDER BY p.updated_at DESC, p.id DESC LIMIT $1`, recent)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var p Page
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Status, &p.UpdatedAt, &p.BlockCount); err != nil {
			rows.Close()
			return o, err
		}
		o.RecentPages = append(o.RecentPages, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return o, err
	}

	prow, err := s.pool.Query(ctx, `
		SELECT id, slug, title, status, published_at, updated_at
		FROM posts ORDER BY updated_at DESC, id DESC LIMIT $1`, recent)
	if err != nil {
		return o, err
	}
	defer prow.Close()
	for prow.Next() {
		var p Post
		if err := prow.Scan(&p.ID, &p.Slug, &p.Title, &p.Status, &p.PublishedAt, &p.UpdatedAt); err != nil {
			return o, err
		}
		o.RecentPosts = append(o.RecentPosts, p)
	}
	return o, prow.Err()
}

// Usage is how much a website holds, for comparing with its limits.
type Usage struct {
	Pages, Posts int
	// StorageBytes is the uploaded pictures and fonts.
	StorageBytes int64
}

// Usage counts what the website holds.
func (s *Store) Usage(ctx context.Context) (Usage, error) {
	var u Usage
	err := s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM pages), (SELECT count(*) FROM posts),
		       (SELECT COALESCE(sum(size_bytes), 0) FROM media) + (SELECT COALESCE(sum(length(data)), 0) FROM fonts)`).
		Scan(&u.Pages, &u.Posts, &u.StorageBytes)
	return u, err
}
