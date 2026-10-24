package content

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// WizardState is what the new-website wizard has kept: how far the person got, their answers, and what it made (so
// running it again replaces its own work and nothing else). It is the wizard package's to read and write; the store
// only keeps it. An empty map means the wizard has never been started.
func (s *Store) WizardState(ctx context.Context) (map[string]any, error) {
	var m map[string]any
	err := s.pool.QueryRow(ctx, `SELECT wizard FROM site_settings WHERE id = 1`).Scan(&m)
	if m == nil {
		m = map[string]any{}
	}
	return m, err
}

// SetWizardState saves the wizard's state.
func (s *Store) SetWizardState(ctx context.Context, m map[string]any) error {
	if m == nil {
		m = map[string]any{}
	}
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET wizard = $1 WHERE id = 1`, m)
	return err
}

// NewBlock is a block to add: its type and its properties and style, already cleaned by the caller.
type NewBlock struct {
	Type         string
	Props, Style map[string]any
}

// HomePageID is the id of the home page, or ErrNotFound when it has been deleted.
func (s *Store) HomePageID(ctx context.Context) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM pages WHERE slug = 'home'`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// ReplaceBlocks, in one step, removes the named blocks from a page (those that are on it; a layout takes the blocks inside
// it with it) and adds new ones at the end, then returns the new blocks' ids. Nothing else on the page is touched. It
// refuses to take the page over the most blocks a page may have.
func (s *Store) ReplaceBlocks(ctx context.Context, pageID int64, remove []int64, add []NewBlock) ([]int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	n, err := lockPage(ctx, tx, pageID)
	if err != nil {
		return nil, err
	}
	if len(remove) > 0 {
		tag, err := tx.Exec(ctx, `DELETE FROM blocks WHERE page_id = $1 AND (id = ANY($2) OR parent_id = ANY($2))`, pageID, remove)
		if err != nil {
			return nil, err
		}
		n -= int(tag.RowsAffected())
	}
	if n+len(add) > MaxBlocksPerPage {
		return nil, ErrTooMany
	}
	var ids []int64
	for _, b := range add {
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO blocks (page_id, position, type, props, style)
			VALUES ($1, (SELECT COALESCE(MAX(position) + 1, 0) FROM blocks WHERE page_id = $1 AND parent_id IS NULL), $2, $3, $4)
			RETURNING id`, pageID, b.Type, b.Props, b.Style).Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := renumber(ctx, tx, pageID); err != nil {
		return nil, err
	}
	if err := touchPage(ctx, tx, pageID); err != nil {
		return nil, err
	}
	return ids, tx.Commit(ctx)
}
