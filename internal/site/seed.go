package site

import (
	"context"
	"errors"
	"webbuilder/internal/menus"

	"github.com/jackc/pgx/v5"
)

type seedBlock struct {
	Type  string
	Props map[string]any
	Style map[string]any
}

// Seed inserts starter content exactly once (guarded by site_settings.seeded),
// and only if the site has no pages yet.
func (s *Service) Seed(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var one int
	err = tx.QueryRow(ctx, `UPDATE site_settings SET seeded = true WHERE id = 1 AND NOT seeded RETURNING 1`).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already seeded
	}
	if err != nil {
		return err
	}
	var pages int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pages`).Scan(&pages); err != nil {
		return err
	}
	if pages == 0 {
		if err := seedContent(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func seedContent(ctx context.Context, tx pgx.Tx) error {
	pages := []struct {
		slug, title, desc string
		blocks            []seedBlock
	}{
		{"home", "Home", "Welcome to my new website.", []seedBlock{
			{"hero", map[string]any{
				"heading":      "Welcome to your new website",
				"subheading":   "Log in to the admin area to edit this page, add blocks and change the design.",
				"button_label": "About this site", "button_url": "/about", "size": "lg",
			}, map[string]any{"background": "#eef1ff", "align": "center", "width": "wide"}},
			{"columns", map[string]any{
				"count":      "3",
				"col1_title": "Build with blocks", "col1_text": "Compose pages from hero, text, image, gallery and more.",
				"col2_title": "Make it yours", "col2_text": "Change colours, fonts and spacing site-wide, or style each block.",
				"col3_title": "Write a blog", "col3_text": "Publish posts alongside your pages.",
			}, map[string]any{"padding": "lg", "align": "center"}},
		}},
		{"about", "About", "About this site.", []seedBlock{
			{"richtext", map[string]any{
				"content": "<h1>About</h1><p>This is an example page. Edit or delete it from the admin area.</p>",
			}, map[string]any{"width": "narrow"}},
		}},
	}
	for _, p := range pages {
		var pageID int64
		err := tx.QueryRow(ctx,
			`INSERT INTO pages (slug, title, status, seo) VALUES ($1, $2, 'published', $3) RETURNING id`,
			p.slug, p.title, map[string]any{"description": p.desc}).Scan(&pageID)
		if err != nil {
			return err
		}
		for i, b := range p.blocks {
			if _, err := tx.Exec(ctx,
				`INSERT INTO blocks (page_id, position, type, props, style) VALUES ($1, $2, $3, $4, $5)`,
				pageID, i, b.Type, b.Props, b.Style); err != nil {
				return err
			}
		}
	}

	// the main navigation gets the two starter pages; the top bar and footer start empty
	mainItems := []menus.Item{
		{Kind: menus.KindLink, Label: "Home", URL: "/"},
		{Kind: menus.KindLink, Label: "About", URL: "/about"},
	}
	for _, sec := range menus.Sections {
		items := []menus.Item{}
		if sec.Key == "main" {
			items = mainItems
		}
		// the migration has already created these rows (empty), so fill an empty one
		// rather than skipping it; a menu someone has already filled is never touched
		if _, err := tx.Exec(ctx,
			`INSERT INTO menus (location, items) VALUES ($1, $2)
			 ON CONFLICT (location) DO UPDATE SET items = EXCLUDED.items WHERE menus.items = '[]'::jsonb`,
			sec.Key, items); err != nil {
			return err
		}
	}
	return nil
}
