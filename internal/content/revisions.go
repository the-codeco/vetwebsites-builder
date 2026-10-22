package content

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"webbuilder/internal/blocks"
)

// How much history a page keeps.
const (
	MaxRevisions = 30
	// RevisionGap is how long after a snapshot the same person can keep editing before another is taken: one
	// editing session leaves one restore point, not one per click.
	RevisionGap = 10 * time.Minute
)

// ErrNoRevision is returned for a restore point that does not exist (or belongs to another page).
var ErrNoRevision = errors.New("that version no longer exists")

// Revision is a saved earlier version of a page's blocks.
type Revision struct {
	ID        int64
	CreatedAt time.Time
	By        string // the email of the person whose edit this version came before
	Note      string
	Blocks    int    // how many blocks it holds (inside layouts too)
	Summary   string // what is in it, such as "Hero, Text, Button"
}

// snap is a block as stored in a snapshot.
type snap struct {
	Type     string         `json:"type"`
	Props    map[string]any `json:"props"`
	Style    map[string]any `json:"style"`
	Children [][]snap       `json:"children,omitempty"`
}

type snapshot struct {
	Blocks []snap `json:"blocks"`
}

func (s snapshot) count() int {
	n := 0
	for _, b := range s.Blocks {
		n++
		for _, col := range b.Children {
			n += len(col)
		}
	}
	return n
}

// summary names the blocks of a snapshot, in order.
func (s snapshot) summary() string {
	var names []string
	for _, b := range s.Blocks {
		label := b.Type
		if bt, ok := blocks.Get(b.Type); ok {
			label = bt.Label
		}
		names = append(names, label)
	}
	out := strings.Join(names, ", ")
	if r := []rune(out); len(r) > 90 {
		out = string(r[:90]) + "…"
	}
	return out
}

func toSnap(b Block) snap {
	out := snap{Type: b.Type, Props: b.Props, Style: b.Style}
	for _, col := range b.Children {
		cs := make([]snap, 0, len(col))
		for _, c := range col {
			cs = append(cs, snap{Type: c.Type, Props: c.Props, Style: c.Style})
		}
		out.Children = append(out.Children, cs)
	}
	return out
}

func (s *Store) currentSnapshot(ctx context.Context, pageID int64) (snapshot, error) {
	bs, err := s.Blocks(ctx, pageID)
	if err != nil {
		return snapshot{}, err
	}
	var out snapshot
	for _, b := range bs {
		out.Blocks = append(out.Blocks, toSnap(b))
	}
	return out, nil
}

// SnapshotPage saves the page's blocks as they are now, as a restore point, unless one was taken for the same person a
// moment ago, the page has no blocks, or nothing has changed since the last one. Older ones beyond MaxRevisions go.
func (s *Store) SnapshotPage(ctx context.Context, pageID int64, by string) error {
	return s.snapshotPage(ctx, pageID, by, "", false)
}

func (s *Store) snapshotPage(ctx context.Context, pageID int64, by, note string, force bool) error {
	cur, err := s.currentSnapshot(ctx, pageID)
	if err != nil {
		return err
	}
	if len(cur.Blocks) == 0 {
		return nil
	}
	raw, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	var lastRaw []byte
	var lastBy string
	var lastAt time.Time
	err = s.pool.QueryRow(ctx, `SELECT snapshot, made_by, created_at FROM page_revisions WHERE page_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, pageID).
		Scan(&lastRaw, &lastBy, &lastAt)
	switch {
	case err == nil:
		if sameJSON(lastRaw, raw) {
			return nil // nothing has changed since
		}
		if !force && lastBy == by && time.Since(lastAt) < RevisionGap {
			return nil // the same person, still in the same editing session
		}
	case !isNoRows(err):
		return err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO page_revisions (page_id, made_by, note, blocks, snapshot) VALUES ($1, $2, $3, $4, $5)`,
		pageID, truncateRunes(by, 120), note, cur.count(), json.RawMessage(raw)); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM page_revisions WHERE page_id = $1 AND id NOT IN
		(SELECT id FROM page_revisions WHERE page_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2)`, pageID, MaxRevisions)
	return err
}

// sameJSON compares two JSON documents by meaning (the database may reorder object keys).
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return bytes.Equal(ax, by)
}

// Revisions lists a page's restore points, newest first.
func (s *Store) Revisions(ctx context.Context, pageID int64) ([]Revision, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, created_at, made_by, note, blocks, snapshot FROM page_revisions WHERE page_id = $1 ORDER BY created_at DESC, id DESC`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Revision
	for rows.Next() {
		var r Revision
		var raw []byte
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.By, &r.Note, &r.Blocks, &raw); err != nil {
			return nil, err
		}
		var sn snapshot
		if json.Unmarshal(raw, &sn) == nil {
			r.Summary = sn.summary()
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RestoreRevision puts a page's blocks back as they were in a restore point. The blocks as they are now are saved first,
// so the restore can itself be undone. Every block is checked again on the way in, as when typed by hand.
func (s *Store) RestoreRevision(ctx context.Context, pageID, revID int64, by string) error {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT snapshot FROM page_revisions WHERE id = $1 AND page_id = $2`, revID, pageID).Scan(&raw)
	if isNoRows(err) {
		return ErrNoRevision
	}
	if err != nil {
		return err
	}
	var sn snapshot
	if err := json.Unmarshal(raw, &sn); err != nil {
		return ErrNoRevision
	}
	if err := s.snapshotPage(ctx, pageID, by, "Before going back to an earlier version", true); err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := lockPage(ctx, tx, pageID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM blocks WHERE page_id = $1`, pageID); err != nil {
		return err
	}
	total := 0
	for i, b := range sn.Blocks {
		bt, ok := blocks.Get(b.Type)
		if !ok || total >= MaxBlocksPerPage {
			continue
		}
		total++
		props, style := bt.Clean(b.Props), blocks.CleanStyleMap(b.Style)
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO blocks (page_id, position, type, props, style) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			pageID, i, b.Type, props, style).Scan(&id); err != nil {
			return err
		}
		if b.Type != blocks.LayoutType {
			continue
		}
		cols := blocks.LayoutColumns(props)
		for slot, col := range b.Children {
			to := min(slot, cols-1) // a column the layout does not have joins the last one
			for _, c := range col {
				ct, ok := blocks.Get(c.Type)
				if !ok || !blocks.Nestable(c.Type) || total >= MaxBlocksPerPage {
					continue
				}
				total++
				if _, err := tx.Exec(ctx, `INSERT INTO blocks (page_id, parent_id, slot, position, type, props, style)
					VALUES ($1, $2, $3, (SELECT count(*) FROM blocks WHERE parent_id = $2 AND slot = $3), $4, $5, $6)`,
					pageID, id, to, c.Type, ct.Clean(c.Props), blocks.CleanStyleMap(c.Style)); err != nil {
					return err
				}
			}
		}
	}
	if err := touchPage(ctx, tx, pageID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
