package content

import (
	"context"

	"webbuilder/internal/menus"
)

// Menus returns every saved menu section keyed by section name. A section that
// has never been saved is simply absent (callers get the zero Menu).
func (s *Store) Menus(ctx context.Context) (map[string]menus.Menu, error) {
	rows, err := s.pool.Query(ctx, `SELECT location, items, sticky, style FROM menus`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]menus.Menu{}
	for rows.Next() {
		var loc string
		var m menus.Menu
		if err := rows.Scan(&loc, &m.Items, &m.Sticky, &m.Style); err != nil {
			return nil, err
		}
		out[loc] = m
	}
	return out, rows.Err()
}

// SaveMenu replaces one section. Items must already have passed menus.Validate.
func (s *Store) SaveMenu(ctx context.Context, section string, items []menus.Item, sticky bool, style menus.Style) error {
	if items == nil {
		items = []menus.Item{} // store [], not null
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO menus (location, items, sticky, style) VALUES ($1, $2, $3, $4)
		ON CONFLICT (location) DO UPDATE SET items = EXCLUDED.items, sticky = EXCLUDED.sticky, style = EXCLUDED.style`,
		section, items, sticky, style)
	return err
}
