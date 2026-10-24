package content

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"webbuilder/internal/elements"
)

// ErrElementsFull is returned when the site already has as many elements as it may.
var ErrElementsFull = errors.New("the list of site-wide elements is full")

const elementCols = `id, kind, name, enabled, config, pages, start_at, end_at, position, updated_at`

func scanElement(row pgx.Row) (elements.Element, error) {
	var e elements.Element
	var cfg, pages []byte
	if err := row.Scan(&e.ID, &e.Kind, &e.Name, &e.Enabled, &cfg, &pages, &e.Start, &e.End, &e.Position, &e.UpdatedAt); err != nil {
		return e, err
	}
	// stored values were cleaned when they were saved; a value that no longer reads is left empty rather than failing the page
	_ = json.Unmarshal(cfg, &e.Config)
	_ = json.Unmarshal(pages, &e.Pages)
	e.Start, e.End = inUTC(e.Start), inUTC(e.End) // shown and compared in UTC, whatever the server's own zone
	return e, nil
}

func inUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// Elements lists every site-wide element, in the order the owner arranged them.
func (s *Store) Elements(ctx context.Context) ([]elements.Element, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+elementCols+` FROM site_elements ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []elements.Element
	for rows.Next() {
		e, err := scanElement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Element is one element by id.
func (s *Store) Element(ctx context.Context, id int64) (elements.Element, error) {
	e, err := scanElement(s.pool.QueryRow(ctx, `SELECT `+elementCols+` FROM site_elements WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// SaveElement adds an element (ID 0) or changes one, after cleaning it. The error is words for the owner when the
// element is not acceptable.
func (s *Store) SaveElement(ctx context.Context, e elements.Element) (int64, error) {
	e, err := elements.Clean(e, time.Now())
	if err != nil {
		return 0, err
	}
	cfg, _ := json.Marshal(e.Config)
	pages, _ := json.Marshal(append([]string{}, e.Pages...))
	if e.ID == 0 {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM site_elements`).Scan(&n); err != nil {
			return 0, err
		}
		if n >= elements.MaxElements {
			return 0, ErrElementsFull
		}
		var id int64
		err := s.pool.QueryRow(ctx, `INSERT INTO site_elements (kind, name, enabled, config, pages, start_at, end_at, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE((SELECT max(position) + 1 FROM site_elements), 0)) RETURNING id`,
			e.Kind, e.Name, e.Enabled, cfg, pages, e.Start, e.End).Scan(&id)
		return id, err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE site_elements SET kind = $2, name = $3, enabled = $4, config = $5, pages = $6, start_at = $7, end_at = $8, updated_at = now() WHERE id = $1`,
		e.ID, e.Kind, e.Name, e.Enabled, cfg, pages, e.Start, e.End)
	if err == nil && tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	return e.ID, err
}

// SetElementEnabled switches an element on or off without opening it.
func (s *Store) SetElementEnabled(ctx context.Context, id int64, on bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE site_elements SET enabled = $2, updated_at = now() WHERE id = $1`, id, on)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// MoveElement swaps an element with its neighbour, up (-1) or down (+1) the list.
func (s *Store) MoveElement(ctx context.Context, id int64, dir int) error {
	list, err := s.Elements(ctx)
	if err != nil {
		return err
	}
	at := -1
	for i, e := range list {
		if e.ID == id {
			at = i
		}
	}
	to := at + dir
	if at < 0 || to < 0 || to >= len(list) {
		return nil
	}
	list[at], list[to] = list[to], list[at]
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i, e := range list { // renumber all, so equal positions never leave the order to chance
		if _, err := tx.Exec(ctx, `UPDATE site_elements SET position = $2 WHERE id = $1 AND position <> $2`, e.ID, i); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// DeleteElement removes an element.
func (s *Store) DeleteElement(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM site_elements WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ReplaceElements swaps the whole list inside a restore's transaction. The list was cleaned on the way in.
func ReplaceElements(ctx context.Context, tx pgx.Tx, list []elements.Element) error {
	if _, err := tx.Exec(ctx, `DELETE FROM site_elements`); err != nil {
		return err
	}
	for i, e := range list {
		cfg, _ := json.Marshal(e.Config)
		pages, _ := json.Marshal(append([]string{}, e.Pages...))
		if _, err := tx.Exec(ctx, `INSERT INTO site_elements (kind, name, enabled, config, pages, start_at, end_at, position) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			e.Kind, e.Name, e.Enabled, cfg, pages, e.Start, e.End, i); err != nil {
			return err
		}
	}
	return nil
}
