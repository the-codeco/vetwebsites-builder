package content

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"webbuilder/internal/blocks"
)

// MaxReusable is how many shared blocks the library holds.
const MaxReusable = 100

var (
	ErrLibraryFull  = errors.New("the shared block library is full")
	ErrInUse        = errors.New("that shared block is still used on a page")
	ErrNotShareable = errors.New("that kind of block can't be shared")
)

// Reusable is a block in the shared library.
type Reusable struct {
	ID         int64
	Name, Type string
	Props      map[string]any
	Style      map[string]any
	Uses       int // how many blocks on pages show it
	UpdatedAt  time.Time
}

// Use is one page that shows a shared block.
type Use struct {
	PageID      int64
	Title, Slug string
}

// A block "points at" shared block $n when it is a reusable block whose ref is that number. The ref is read only
// if it is a plain number, so a block saved by hand with a junk value can't make a query fail.
const pointsAt = `b.type = 'reusable' AND b.props->>'ref' ~ '^[0-9]{1,15}$' AND (b.props->>'ref')::bigint = `

const reusableCols = `r.id, r.name, r.type, r.props, r.style, r.updated_at,
	(SELECT count(*) FROM blocks b WHERE ` + pointsAt + `r.id)`

func scanReusable(row interface{ Scan(...any) error }) (Reusable, error) {
	var r Reusable
	err := row.Scan(&r.ID, &r.Name, &r.Type, &r.Props, &r.Style, &r.UpdatedAt, &r.Uses)
	return r, err
}

// Reusables lists the library, by name.
func (s *Store) Reusables(ctx context.Context) ([]Reusable, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+reusableCols+` FROM reusable_blocks r ORDER BY lower(r.name), r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reusable
	for rows.Next() {
		r, err := scanReusable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Reusable(ctx context.Context, id int64) (Reusable, error) {
	r, err := scanReusable(s.pool.QueryRow(ctx, `SELECT `+reusableCols+` FROM reusable_blocks r WHERE r.id = $1`, id))
	return r, notFound(err)
}

// ReusableUses lists the pages that show a shared block.
func (s *Store) ReusableUses(ctx context.Context, id int64) ([]Use, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT p.id, p.title, p.slug FROM blocks b JOIN pages p ON p.id = b.page_id
		WHERE `+pointsAt+`$1 ORDER BY p.title, p.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Use
	for rows.Next() {
		var u Use
		if err := rows.Scan(&u.PageID, &u.Title, &u.Slug); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CleanReusableName trims a name and keeps it within its limit.
func CleanReusableName(name string) string {
	return strings.TrimSpace(truncateRunes(strings.Join(strings.Fields(name), " "), blocks.MaxReusableName))
}

// createReusable adds a block to the library inside a transaction. Props and style are saved as given.
func createReusable(ctx context.Context, tx pgx.Tx, name, typ string, props, style map[string]any) (int64, error) {
	if !blocks.CanBeShared(typ) {
		return 0, ErrNotShareable
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM reusable_blocks`).Scan(&n); err != nil {
		return 0, err
	}
	if n >= MaxReusable {
		return 0, ErrLibraryFull
	}
	if name = CleanReusableName(name); name == "" {
		name = "Shared block"
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO reusable_blocks (name, type, props, style) VALUES ($1, $2, $3, $4) RETURNING id`,
		name, typ, props, style).Scan(&id)
	return id, err
}

// CreateReusable adds a block to the library.
func (s *Store) CreateReusable(ctx context.Context, name, typ string, props, style map[string]any) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	id, err := createReusable(ctx, tx, name, typ, props, style)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// UpdateReusable saves a shared block. Every page that shows it changes, so each one's search words are rebuilt
// in the same transaction.
func (s *Store) UpdateReusable(ctx context.Context, id int64, name string, props, style map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if name = CleanReusableName(name); name == "" {
		name = "Shared block"
	}
	tag, err := tx.Exec(ctx, `UPDATE reusable_blocks SET name = $2, props = $3, style = $4, updated_at = now() WHERE id = $1`, id, name, props, style)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := reindexPagesUsing(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// reindexPagesUsing rebuilds the search words (and the "last changed" time) of every page that shows a shared block.
func reindexPagesUsing(ctx context.Context, tx pgx.Tx, id int64) error {
	rows, err := tx.Query(ctx, `SELECT DISTINCT b.page_id FROM blocks b WHERE `+pointsAt+`$1`, id)
	if err != nil {
		return err
	}
	var pages []int64
	for rows.Next() {
		var p int64
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		pages = append(pages, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range pages {
		if err := touchPage(ctx, tx, p); err != nil {
			return err
		}
	}
	return nil
}

// DeleteReusable removes a shared block from the library. It is refused while any page still shows it, so a page
// never loses a block by surprise.
func (s *Store) DeleteReusable(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var uses int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM blocks b WHERE `+pointsAt+`$1`, id).Scan(&uses); err != nil {
		return err
	}
	if uses > 0 {
		return ErrInUse
	}
	tag, err := tx.Exec(ctx, `DELETE FROM reusable_blocks WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// ShareBlock moves a block on a page into the library and leaves a shared block in its place, so it keeps its
// position (also inside a layout column) and every other page can use it too. It returns the new shared block's
// id and the page's.
func (s *Store) ShareBlock(ctx context.Context, blockID int64, name string) (reusableID, pageID int64, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	var typ string
	var props, style map[string]any
	if err := tx.QueryRow(ctx, `SELECT page_id, type, props, style FROM blocks WHERE id = $1 FOR UPDATE`, blockID).
		Scan(&pageID, &typ, &props, &style); err != nil {
		return 0, 0, notFound(err)
	}
	reusableID, err = createReusable(ctx, tx, name, typ, props, style)
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE blocks SET type = 'reusable', props = $2, style = $3 WHERE id = $1`,
		blockID, map[string]any{"ref": float64(reusableID)}, blocks.CleanStyleMap(nil)); err != nil {
		return 0, 0, err
	}
	if err := touchPage(ctx, tx, pageID); err != nil {
		return 0, 0, err
	}
	return reusableID, pageID, tx.Commit(ctx)
}
