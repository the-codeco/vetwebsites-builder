package content

import (
	"context"
	"errors"
)

// Sections: a page can belong under a top-level page (one level deep). The top page and the pages under it are a
// section, which the sidebar's section menu lists.

var (
	ErrParentNotTop    = errors.New("a page can only go under a top-level page")
	ErrParentHasPages  = errors.New("a page that other pages belong under cannot itself go under a page")
	ErrParentIsItself  = errors.New("a page cannot go under itself")
	ErrParentIsHome    = errors.New("the home page cannot go under another page")
	MaxSectionPosition = 9999
)

// SetPageSection saves where a page sits: under parentID (0 for none), at position, and whether it shows the
// sidebar. Only one level is allowed, so the parent must be a top-level page and the page must have no pages of
// its own.
func (s *Store) SetPageSection(ctx context.Context, id, parentID int64, position int, sidebar bool) error {
	position = min(max(position, 0), MaxSectionPosition)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var slug string
	if err := tx.QueryRow(ctx, `SELECT slug FROM pages WHERE id = $1 FOR UPDATE`, id).Scan(&slug); err != nil {
		return notFound(err)
	}
	if parentID != 0 {
		var grand int64
		switch err := tx.QueryRow(ctx, `SELECT COALESCE(parent_id, 0) FROM pages WHERE id = $1`, parentID).Scan(&grand); {
		case isNoRows(err):
			return ErrNotFound
		case err != nil:
			return err
		case parentID == id:
			return ErrParentIsItself
		case slug == "home":
			return ErrParentIsHome
		case grand != 0:
			return ErrParentNotTop
		}
		var kids int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM pages WHERE parent_id = $1`, id).Scan(&kids); err != nil {
			return err
		}
		if kids > 0 {
			return ErrParentHasPages
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE pages SET parent_id = NULLIF($2::bigint, 0), position = $3, sidebar = $4 WHERE id = $1`, id, parentID, position, sidebar); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SectionPage is one entry of a section menu.
type SectionPage struct {
	Slug, Title string
}

// Section is the section the page with this slug is in: its top page and the published pages under it, in order
// (position, then title). ok is false when the page is in no section (it has no parent and no pages under it).
func (s *Store) Section(ctx context.Context, slug string) (top SectionPage, pages []SectionPage, ok bool, err error) {
	var topID int64
	err = s.pool.QueryRow(ctx, `
		SELECT t.id, t.slug, t.title FROM pages p JOIN pages t ON t.id = COALESCE(p.parent_id, p.id)
		WHERE p.slug = $1`, slug).Scan(&topID, &top.Slug, &top.Title)
	if isNoRows(err) {
		return top, nil, false, nil
	}
	if err != nil {
		return top, nil, false, err
	}
	rows, err := s.pool.Query(ctx, `SELECT slug, title FROM pages WHERE parent_id = $1 AND status = 'published' ORDER BY position, lower(title), id`, topID)
	if err != nil {
		return top, nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var p SectionPage
		if err := rows.Scan(&p.Slug, &p.Title); err != nil {
			return top, nil, false, err
		}
		pages = append(pages, p)
	}
	return top, pages, len(pages) > 0, rows.Err()
}
