// Package content is the data layer for pages, blocks and menus.
package content

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/blocks"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrSlugTaken = errors.New("slug already in use")
	ErrTooMany   = errors.New("too many blocks on this page")

	ErrNotNestable = errors.New("that block can't go inside a layout")
)

const MaxBlocksPerPage = 200

type Page struct {
	ID                  int64
	Slug, Title, Status string
	SEO                 SEO // search and sharing settings
	UpdatedAt           time.Time
	BlockCount          int
	PublishAt           *time.Time // a draft that goes live at this time
	// ParentID is the top-level page this one belongs under (0: none); Position orders a section's pages.
	ParentID int64
	Position int
	Sidebar  bool // shows the site's sidebar
}

type Block struct {
	ID, PageID   int64
	ParentID     int64 // the layout this block is inside; 0 for a block directly on the page
	Slot         int   // which column of that layout
	Position     int   // place in its list (the page, or one column)
	Type         string
	Props, Style map[string]any
	Children     [][]Block // for a layout: the blocks in each column
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// --- pages ---

func (s *Store) ListPages(ctx context.Context) ([]Page, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.slug, p.title, p.status, p.seo, p.updated_at,
		       (SELECT count(*) FROM blocks b WHERE b.page_id = p.id), p.publish_at, COALESCE(p.parent_id, 0), p.position, p.sidebar
		FROM pages p ORDER BY (p.slug = 'home') DESC, lower(p.title)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		var p Page
		var raw map[string]any
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Status, &raw, &p.UpdatedAt, &p.BlockCount, &p.PublishAt, &p.ParentID, &p.Position, &p.Sidebar); err != nil {
			return nil, err
		}
		p.SEO = SEOFrom(raw)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Page(ctx context.Context, id int64) (Page, error) {
	var p Page
	var raw map[string]any
	err := s.pool.QueryRow(ctx, `
		SELECT id, slug, title, status, seo, updated_at,
		       (SELECT count(*) FROM blocks WHERE page_id = pages.id), publish_at, COALESCE(parent_id, 0), position, sidebar
		FROM pages WHERE id = $1`, id).
		Scan(&p.ID, &p.Slug, &p.Title, &p.Status, &raw, &p.UpdatedAt, &p.BlockCount, &p.PublishAt, &p.ParentID, &p.Position, &p.Sidebar)
	p.SEO = SEOFrom(raw)
	return p, notFound(err)
}

func (s *Store) CountPages(ctx context.Context) (published, drafts int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'published'), count(*) FILTER (WHERE status = 'draft')
		FROM pages`).Scan(&published, &drafts)
	return
}

func (s *Store) CreatePage(ctx context.Context, slug, title string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO pages (slug, title) VALUES ($1, $2) RETURNING id`, slug, title).Scan(&id)
	if isUnique(err) {
		return 0, ErrSlugTaken
	}
	return id, err
}

// UpdatePage saves a page's settings. If its address changes, the old address is redirected to the new one.
func (s *Store) UpdatePage(ctx context.Context, id int64, slug, title, status string, seo SEO, publishAt *time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var oldSlug string
	if err := tx.QueryRow(ctx, `SELECT slug FROM pages WHERE id = $1 FOR UPDATE`, id).Scan(&oldSlug); err != nil {
		return notFound(err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE pages SET slug = $2, title = $3, status = $4, seo = $5, updated_at = now(),
		       publish_at = CASE WHEN $4::text = 'draft' THEN $6::timestamptz ELSE NULL END
		WHERE id = $1`, id, slug, title, status, seo.asJSON(), publishAt)
	if isUnique(err) {
		return ErrSlugTaken
	}
	if err != nil {
		return err
	}
	if oldSlug != slug && oldSlug != "home" && slug != "home" {
		if err := moveAddress(ctx, tx, "/"+oldSlug, "/"+slug); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) DeletePage(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM pages WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// --- blocks ---

const blockCols = `id, page_id, COALESCE(parent_id, 0), slot, position, type, props, style`

func scanBlock(row pgx.Row) (Block, error) {
	var b Block
	err := row.Scan(&b.ID, &b.PageID, &b.ParentID, &b.Slot, &b.Position, &b.Type, &b.Props, &b.Style)
	return b, err
}

// Blocks returns a page's blocks in order. Blocks inside a layout are not listed on their own: they are in the
// layout's Children, one list per column.
func (s *Store) Blocks(ctx context.Context, pageID int64) ([]Block, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+blockCols+` FROM blocks WHERE page_id = $1 ORDER BY (parent_id IS NOT NULL), slot, position, id`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var top []Block
	nested := map[int64][]Block{}
	for rows.Next() {
		b, err := scanBlock(rows)
		if err != nil {
			return nil, err
		}
		if b.ParentID == 0 {
			top = append(top, b)
		} else {
			nested[b.ParentID] = append(nested[b.ParentID], b)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range top {
		if top[i].Type != blocks.LayoutType {
			continue
		}
		cols := blocks.LayoutColumns(top[i].Props)
		for _, c := range nested[top[i].ID] {
			cols = max(cols, c.Slot+1)
		}
		top[i].Children = make([][]Block, cols)
		for _, c := range nested[top[i].ID] {
			top[i].Children[c.Slot] = append(top[i].Children[c.Slot], c)
		}
	}
	return top, nil
}

func (s *Store) Block(ctx context.Context, id int64) (Block, error) {
	b, err := scanBlock(s.pool.QueryRow(ctx, `SELECT `+blockCols+` FROM blocks WHERE id = $1`, id))
	return b, notFound(err)
}

// lockPage takes the page's row lock (so two edits to one page happen one after the other) and returns how many
// blocks it has, nested ones included.
func lockPage(ctx context.Context, tx pgx.Tx, pageID int64) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM blocks WHERE page_id = p.id) FROM pages p WHERE p.id = $1 FOR UPDATE OF p`,
		pageID).Scan(&n)
	return n, notFound(err)
}

func touchPage(ctx context.Context, tx pgx.Tx, pageID int64) error {
	if _, err := tx.Exec(ctx, `UPDATE pages SET updated_at = now() WHERE id = $1`, pageID); err != nil {
		return err
	}
	return reindexPage(ctx, tx, pageID)
}

// AddBlock adds a block at the end of the page.
func (s *Store) AddBlock(ctx context.Context, pageID int64, typ string, props, style map[string]any) (Block, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Block{}, err
	}
	defer tx.Rollback(ctx)

	n, err := lockPage(ctx, tx, pageID)
	if err != nil {
		return Block{}, err
	}
	if n >= MaxBlocksPerPage {
		return Block{}, ErrTooMany
	}
	b := Block{PageID: pageID, Type: typ, Props: props, Style: style}
	err = tx.QueryRow(ctx, `
		INSERT INTO blocks (page_id, position, type, props, style)
		VALUES ($1, (SELECT COALESCE(MAX(position) + 1, 0) FROM blocks WHERE page_id = $1 AND parent_id IS NULL), $2, $3, $4)
		RETURNING id, position`, pageID, typ, props, style).Scan(&b.ID, &b.Position)
	if err != nil {
		return Block{}, err
	}
	if err := touchPage(ctx, tx, pageID); err != nil {
		return Block{}, err
	}
	return b, tx.Commit(ctx)
}

// AddChildBlock adds a block at the end of one column of a layout. The layout must exist and the column must be
// one it has; a layout cannot go inside a layout.
func (s *Store) AddChildBlock(ctx context.Context, parentID int64, slot int, typ string, props, style map[string]any) (Block, error) {
	if !blocks.Nestable(typ) {
		return Block{}, ErrNotNestable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Block{}, err
	}
	defer tx.Rollback(ctx)

	var pageID int64
	var parentType string
	var parentProps map[string]any
	if err := tx.QueryRow(ctx, `SELECT page_id, type, props FROM blocks WHERE id = $1 AND parent_id IS NULL`, parentID).Scan(&pageID, &parentType, &parentProps); err != nil {
		return Block{}, notFound(err)
	}
	if parentType != blocks.LayoutType || slot < 0 || slot >= blocks.LayoutColumns(parentProps) {
		return Block{}, ErrNotFound
	}
	n, err := lockPage(ctx, tx, pageID)
	if err != nil {
		return Block{}, err
	}
	var inColumn int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM blocks WHERE parent_id = $1 AND slot = $2`, parentID, slot).Scan(&inColumn); err != nil {
		return Block{}, err
	}
	if n >= MaxBlocksPerPage || inColumn >= blocks.MaxBlocksPerColumn {
		return Block{}, ErrTooMany
	}
	b := Block{PageID: pageID, ParentID: parentID, Slot: slot, Type: typ, Props: props, Style: style}
	err = tx.QueryRow(ctx, `
		INSERT INTO blocks (page_id, parent_id, slot, position, type, props, style)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		pageID, parentID, slot, inColumn, typ, props, style).Scan(&b.ID)
	if err != nil {
		return Block{}, err
	}
	b.Position = inColumn
	if err := touchPage(ctx, tx, pageID); err != nil {
		return Block{}, err
	}
	return b, tx.Commit(ctx)
}

// UpdateBlock saves a block's settings. If a layout now has fewer columns, the blocks of the columns that went
// away move to the end of the last one, so nothing is lost.
func (s *Store) UpdateBlock(ctx context.Context, id int64, props, style map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var pageID int64
	var typ string
	err = tx.QueryRow(ctx, `UPDATE blocks SET props = $2, style = $3 WHERE id = $1 RETURNING page_id, type`,
		id, props, style).Scan(&pageID, &typ)
	if err != nil {
		return notFound(err)
	}
	if typ == blocks.LayoutType {
		cols := blocks.LayoutColumns(props)
		// +1000 puts them after the blocks already in the last column; renumber then closes the gap
		if _, err := tx.Exec(ctx, `UPDATE blocks SET slot = $2, position = position + 1000 WHERE parent_id = $1 AND slot >= $3`, id, cols-1, cols); err != nil {
			return err
		}
		if err := renumber(ctx, tx, pageID); err != nil {
			return err
		}
	}
	if err := touchPage(ctx, tx, pageID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// renumber rewrites positions as a gapless 0..n-1 sequence in each list (the page's own blocks, and each column of
// each layout).
func renumber(ctx context.Context, tx pgx.Tx, pageID int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE blocks b SET position = n.rn FROM (
			SELECT id, (row_number() OVER (PARTITION BY parent_id, slot ORDER BY position, id) - 1)::int AS rn
			FROM blocks WHERE page_id = $1
		) n WHERE b.id = n.id AND b.page_id = $1`, pageID)
	return err
}

// DeleteBlock removes a block (and, for a layout, everything in it) and returns its page id.
func (s *Store) DeleteBlock(ctx context.Context, id int64) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var pageID int64
	if err := tx.QueryRow(ctx, `DELETE FROM blocks WHERE id = $1 RETURNING page_id`, id).Scan(&pageID); err != nil {
		return 0, notFound(err)
	}
	if err := renumber(ctx, tx, pageID); err != nil {
		return 0, err
	}
	if err := touchPage(ctx, tx, pageID); err != nil {
		return 0, err
	}
	return pageID, tx.Commit(ctx)
}

// MoveBlock swaps a block with its neighbour in the same list (no-op at either end) and returns its page id.
func (s *Store) MoveBlock(ctx context.Context, id int64, up bool) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var pageID int64
	if err := tx.QueryRow(ctx, `SELECT page_id FROM blocks WHERE id = $1`, id).Scan(&pageID); err != nil {
		return 0, notFound(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM pages WHERE id = $1 FOR UPDATE`, pageID); err != nil {
		return 0, err
	}
	if err := renumber(ctx, tx, pageID); err != nil {
		return 0, err
	}
	var pos, slot int
	var parent *int64
	if err := tx.QueryRow(ctx, `SELECT position, slot, parent_id FROM blocks WHERE id = $1`, id).Scan(&pos, &slot, &parent); err != nil {
		return 0, notFound(err)
	}
	other := pos + 1
	if up {
		other = pos - 1
	}
	var otherID int64
	err = tx.QueryRow(ctx, `
		SELECT id FROM blocks WHERE page_id = $1 AND parent_id IS NOT DISTINCT FROM $2 AND slot = $3 AND position = $4`,
		pageID, parent, slot, other).Scan(&otherID)
	if errors.Is(err, pgx.ErrNoRows) {
		return pageID, tx.Commit(ctx) // already at the end
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE blocks SET position = CASE id WHEN $1 THEN $2::int ELSE $3::int END WHERE id IN ($1, $4)`,
		id, other, pos, otherID); err != nil {
		return 0, err
	}
	if err := touchPage(ctx, tx, pageID); err != nil {
		return 0, err
	}
	return pageID, tx.Commit(ctx)
}

// MoveBlockToColumn moves a block inside a layout one column to the left or right, to the end of that column.
// At the first or last column it does nothing. A block that isn't inside a layout can't be moved this way.
func (s *Store) MoveBlockToColumn(ctx context.Context, id int64, right bool) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var pageID, parentID int64
	var slot int
	var parentProps map[string]any
	err = tx.QueryRow(ctx, `
		SELECT b.page_id, b.parent_id, b.slot, p.props FROM blocks b JOIN blocks p ON p.id = b.parent_id WHERE b.id = $1`, id).
		Scan(&pageID, &parentID, &slot, &parentProps)
	if err != nil {
		return 0, notFound(err)
	}
	if _, err := lockPage(ctx, tx, pageID); err != nil {
		return 0, err
	}
	to := slot - 1
	if right {
		to = slot + 1
	}
	if to < 0 || to >= blocks.LayoutColumns(parentProps) {
		return pageID, tx.Commit(ctx)
	}
	var inTarget int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM blocks WHERE parent_id = $1 AND slot = $2`, parentID, to).Scan(&inTarget); err != nil {
		return 0, err
	}
	if inTarget >= blocks.MaxBlocksPerColumn {
		return 0, ErrTooMany
	}
	if _, err := tx.Exec(ctx, `UPDATE blocks SET slot = $2, position = $3 WHERE id = $1`, id, to, inTarget); err != nil {
		return 0, err
	}
	if err := renumber(ctx, tx, pageID); err != nil {
		return 0, err
	}
	if err := touchPage(ctx, tx, pageID); err != nil {
		return 0, err
	}
	return pageID, tx.Commit(ctx)
}
