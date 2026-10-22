package content

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"webbuilder/internal/blocks"
)

// db is what both a connection pool and a transaction offer.
type db interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// reindexPage copies the readable words of a page's blocks into pages.search_text, which the
// database turns into the search index. It runs inside the same transaction as the block change,
// so the index is never out of step with the page.
func reindexPage(ctx context.Context, q db, pageID int64) error {
	rows, err := q.Query(ctx, `SELECT COALESCE(r.type, b.type), COALESCE(r.props, b.props) FROM blocks b LEFT JOIN blocks p ON p.id = b.parent_id
		LEFT JOIN reusable_blocks r ON b.type = 'reusable' AND b.props->>'ref' ~ '^[0-9]{1,15}$' AND r.id = (CASE WHEN b.props->>'ref' ~ '^[0-9]{1,15}$' THEN (b.props->>'ref')::bigint END)
		WHERE b.page_id = $1
		ORDER BY COALESCE(p.position, b.position), (b.parent_id IS NOT NULL), b.slot, b.position, b.id`, pageID)
	if err != nil {
		return err
	}
	var parts []string
	for rows.Next() {
		var typ string
		var props map[string]any
		if err := rows.Scan(&typ, &props); err != nil {
			rows.Close()
			return err
		}
		if t := blocks.SearchText(typ, props); t != "" {
			parts = append(parts, t)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE pages SET search_text = $2 WHERE id = $1`, pageID, strings.Join(parts, " "))
	return err
}

// ReindexAllPages rebuilds every page's search text. It runs at startup, which covers pages that
// existed before search did and the starter pages written straight to the database.
func (s *Store) ReindexAllPages(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT id FROM pages`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := reindexPage(ctx, s.pool, id); err != nil {
			return err
		}
	}
	return nil
}
