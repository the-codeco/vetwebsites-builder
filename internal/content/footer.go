package content

import (
	"context"

	"github.com/jackc/pgx/v5"

	"webbuilder/internal/footer"
	"webbuilder/internal/menus"
)

// Footer is what the owner builds for the bottom of every page (see package footer).
type Footer = footer.Footer

// footerFrom reads the stored footer. One saved before the footer builder is shown as its
// upgrade would be; UpgradeFooter normally rewrites it at startup, so this is a fallback.
func footerFrom(m map[string]any) Footer {
	if footer.IsCurrent(m) {
		return footer.FromStored(m)
	}
	return footer.Upgrade(m, nil, false)
}

// SetFooter saves the footer. It must already have passed footer.Validate.
func (s *Store) SetFooter(ctx context.Context, f Footer) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET footer = $1 WHERE id = 1`, f.AsMap())
	return err
}

// CleanFooter makes any stored or imported footer JSON safe. A footer from before the builder is
// upgraded with the footer menu and contact-details setting that came with it.
func CleanFooter(m map[string]any, footerMenu []menus.Item, contactInFooter bool) map[string]any {
	if footer.IsCurrent(m) {
		return footer.FromStored(m).AsMap()
	}
	return footer.Upgrade(m, footerMenu, contactInFooter).AsMap()
}

// UpgradeFooter moves a footer saved before the footer builder into the builder's shape, once:
// its columns, the footer menu's links and the "contact details in the footer" setting all
// become rows of widgets, and the footer menu (which now lives in the footer) is emptied.
func (s *Store) UpgradeFooter(ctx context.Context) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var stored, details map[string]any
		if err := tx.QueryRow(ctx, `SELECT footer, details FROM site_settings WHERE id = 1 FOR UPDATE`).Scan(&stored, &details); err != nil {
			if isNoRows(err) {
				return nil
			}
			return err
		}
		if footer.IsCurrent(stored) {
			return nil
		}
		var items []menus.Item
		if err := tx.QueryRow(ctx, `SELECT items FROM menus WHERE location = 'footer'`).Scan(&items); err != nil && !isNoRows(err) {
			return err
		}
		f := footer.Upgrade(stored, items, detailsFrom(details).InFooter)
		if _, err := tx.Exec(ctx, `UPDATE site_settings SET footer = $1 WHERE id = 1`, f.AsMap()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE menus SET items = '[]', sticky = false WHERE location = 'footer'`)
		return err
	})
}
