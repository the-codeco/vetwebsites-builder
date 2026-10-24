package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
	"webbuilder/internal/media"
	"webbuilder/internal/elements"
	"webbuilder/internal/menus"
)

// Options say what a restore may touch beyond the site's content.
type Options struct {
	// Personal also replaces the subscriber list and the contact messages with the backup's.
	Personal bool
	// UserID becomes the author of restored posts.
	UserID int64
}

// Result is what a restore did.
type Result struct {
	Counts   Counts
	Warnings []string
}

// advisory lock number: one restore at a time, even across several copies of the program
const restoreLock = 7_702_001

// Restore replaces the site's content with a backup's. The file is checked completely first; the database is
// changed in one transaction, so a restore either happens entirely or not at all. User accounts, the domain,
// email settings, analytics and sent newsletters are never touched.
func (s *Service) Restore(ctx context.Context, ra io.ReaderAt, size int64, opt Options) (Result, error) {
	a, files, err := s.inspect(ctx, ra, size)
	if err != nil {
		return Result{}, err
	}
	if opt.Personal && !a.Manifest.Personal {
		return Result{}, invalid("This backup doesn't contain subscribers or contact messages.")
	}

	old, err := s.libraryFiles(ctx)
	if err != nil {
		return Result{}, err
	}

	// images first: new files carry random names, so they can sit beside the old ones until the switch
	var created []string
	undo := func() {
		for _, p := range created {
			s.media.Remove(p)
		}
	}
	thumbs := map[string]string{}
	for _, m := range a.Content.Media {
		if err := ctx.Err(); err != nil {
			undo()
			return Result{}, err
		}
		data, err := readEntry(files[MediaPrefix+m.Path], MaxMediaBytes)
		if err != nil {
			undo()
			return Result{}, err
		}
		if sumHex(data) != a.Manifest.Files[MediaPrefix+m.Path] {
			undo()
			return Result{}, invalid("The image %s doesn't match its checksum: the file is damaged or was changed.", m.Path)
		}
		info := a.Images[m.Path]
		thumb, err := media.MakeThumb(data, info)
		if err != nil {
			undo()
			return Result{}, invalid("The image %s isn't a usable picture.", m.Path)
		}
		made, err := s.putFile(m.Path, data)
		if err != nil {
			undo()
			return Result{}, err
		}
		created = append(created, made...)
		thumbs[m.Path] = m.Path
		if thumb != nil {
			tp := strings.TrimSuffix(m.Path, filepath.Ext(m.Path)) + "_thumb" + thumb.Ext
			made, err := s.putFile(tp, thumb.Data)
			if err != nil {
				undo()
				return Result{}, err
			}
			created = append(created, made...)
			thumbs[m.Path] = tp
		}
	}

	if err := s.apply(ctx, a, thumbs, opt); err != nil {
		undo()
		return Result{}, err
	}

	// the new content is live: files no image refers to any more can go
	keep := map[string]bool{}
	for _, m := range a.Content.Media {
		keep[m.Path] = true
		keep[thumbs[m.Path]] = true
	}
	for _, p := range old {
		if !keep[p] {
			s.media.Remove(p)
		}
	}
	return Result{Counts: a.Manifest.Counts, Warnings: a.Warnings}, nil
}

// libraryFiles lists every file the current image library owns: originals, thumbnails and resized copies.
func (s *Service) libraryFiles(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT path, thumb_path FROM media`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p, t string
		if err := rows.Scan(&p, &t); err != nil {
			return nil, err
		}
		out = append(out, p)
		if t != "" && t != p {
			out = append(out, t)
		}
		out = append(out, media.VariantFiles(p)...)
	}
	return out, rows.Err()
}

// putFile stores a file in the media folder without overwriting anything. A file already there with the same
// bytes is fine (restoring a backup onto the site it came from); one with different bytes is a conflict.
// It returns the files it created, for undoing.
func (s *Service) putFile(name string, data []byte) ([]string, error) {
	if err := s.media.MkdirAll(filepath.ToSlash(filepath.Dir(name)), 0o755); err != nil {
		return nil, err
	}
	f, err := s.media.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		have, rerr := s.media.ReadFile(name)
		if rerr != nil || !bytes.Equal(have, data) {
			return nil, invalid("An image called %s already exists on this site with different contents, so the backup can't be restored here.", name)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		s.media.Remove(name)
		return nil, err
	}
	if err := f.Close(); err != nil {
		s.media.Remove(name)
		return nil, err
	}
	return []string{name}, nil
}

func randomHex() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// apply replaces the content in one transaction.
func (s *Service) apply(ctx context.Context, a *Archive, thumbs map[string]string, opt Options) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, restoreLock); err != nil {
		return err
	}
	c := a.Content

	for _, q := range []string{`DELETE FROM pages`, `DELETE FROM reusable_blocks`, `DELETE FROM posts`, `DELETE FROM categories`, `DELETE FROM media`} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}

	st := c.Settings
	if _, err := tx.Exec(ctx, `UPDATE site_settings SET title = $1, active_theme = $2, tokens = $3, custom_css = $4, seo = $5,
		show_search = $6, color_mode = $7, consent = $8, footer = $9, brand = $10, details = $11, tracking = $12, layout = $13, sidebar = $14, color_switch = $15 WHERE id = 1`,
		st.Title, st.ActiveTheme, st.Tokens, st.CustomCSS, st.SEO, st.ShowSearch, st.ColorMode, st.Consent, st.Footer, st.Brand, st.Details, st.Tracking, st.Layout, st.Sidebar, st.ColorSwitch); err != nil {
		return err
	}

	for _, m := range c.Media {
		info := a.Images[m.Path]
		if _, err := tx.Exec(ctx, `INSERT INTO media (path, thumb_path, mime, size_bytes, original_name, alt, width, height, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			m.Path, thumbs[m.Path], info.Mime, a.Sizes[m.Path], m.OriginalName, m.Alt, info.Width, info.Height, m.CreatedAt); err != nil {
			return err
		}
	}

	rl := make([]content.Redirect, len(c.Redirects))
	for i, r := range c.Redirects {
		rl[i] = content.Redirect{From: r.From, To: r.To, Permanent: r.Permanent, Auto: r.Auto}
	}
	if err := content.ReplaceRedirects(ctx, tx, rl); err != nil {
		return err
	}

	el := make([]elements.Element, len(c.Elements))
	for i, e := range c.Elements {
		el[i] = elements.Element{Kind: e.Kind, Name: e.Name, Enabled: e.Enabled, Config: e.Config, Pages: e.Pages, Start: e.Start, End: e.End}
	}
	if err := content.ReplaceElements(ctx, tx, el); err != nil {
		return err
	}

	fl := make([]content.BackupFont, len(c.Fonts))
	for i, f := range c.Fonts {
		fl[i] = content.BackupFont{Family: f.Family, Weight: f.Weight, Data: f.Data}
	}
	if err := content.ReplaceFonts(ctx, tx, fl); err != nil {
		return err
	}

	// the shared library first: its ids are new, so the blocks that point at them are rewritten as they go in
	newID := map[int64]int64{}
	shared := map[int64]Reusable{}
	for _, r := range c.Reusables {
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO reusable_blocks (name, type, props, style) VALUES ($1, $2, $3, $4) RETURNING id`,
			r.Name, r.Type, r.Props, r.Style).Scan(&id); err != nil {
			return err
		}
		newID[r.ID], shared[r.ID] = id, r
	}
	// what a block on a page shows, and the props it is saved with (a shared block points at its new id)
	words := func(b Block) string {
		if b.Type == blocks.ReusableType {
			r := shared[blocks.RefOf(b.Props)]
			return blocks.SearchText(r.Type, r.Props)
		}
		return blocks.SearchText(b.Type, b.Props)
	}
	saved := func(b Block) map[string]any {
		if b.Type == blocks.ReusableType {
			return map[string]any{"ref": float64(newID[blocks.RefOf(b.Props)])}
		}
		return b.Props
	}

	for _, p := range c.Pages {
		var search []string
		for _, b := range p.Blocks {
			if t := words(b); t != "" {
				search = append(search, t)
			}
			for _, col := range b.Children {
				for _, ch := range col {
					if t := words(ch); t != "" {
						search = append(search, t)
					}
				}
			}
		}
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO pages (slug, title, status, seo, updated_at, search_text, publish_at, position, sidebar) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			p.Slug, p.Title, p.Status, p.SEO, p.UpdatedAt, strings.Join(search, " "), p.PublishAt, p.Position, !p.NoSidebar).Scan(&id); err != nil {
			return err
		}
		for i, b := range p.Blocks {
			var blockID int64
			if err := tx.QueryRow(ctx, `INSERT INTO blocks (page_id, position, type, props, style) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
				id, i, b.Type, saved(b), b.Style).Scan(&blockID); err != nil {
				return err
			}
			for slot, col := range b.Children {
				for pos, ch := range col {
					if _, err := tx.Exec(ctx, `INSERT INTO blocks (page_id, parent_id, slot, position, type, props, style) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
						id, blockID, slot, pos, ch.Type, saved(ch), ch.Style); err != nil {
						return err
					}
				}
			}
		}
	}
	// sections: now that every page exists, each page is put under its parent (checked when the file was read)
	for _, p := range c.Pages {
		if p.Parent == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE pages SET parent_id = (SELECT id FROM pages WHERE slug = $2) WHERE slug = $1`, p.Slug, p.Parent); err != nil {
			return err
		}
	}

	catID := map[string]int64{}
	for _, cat := range c.Categories {
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO categories (slug, name) VALUES ($1, $2) RETURNING id`, cat.Slug, cat.Name).Scan(&id); err != nil {
			return err
		}
		catID[cat.Slug] = id
	}
	for _, p := range c.Posts {
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO posts (slug, title, excerpt, body, cover_image, status, seo, author_id, published_at, updated_at, publish_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8::bigint, 0), $9, $10, $11) RETURNING id`,
			p.Slug, p.Title, p.Excerpt, p.Body, p.Cover, p.Status, p.SEO, opt.UserID, p.PublishedAt, p.UpdatedAt, p.PublishAt).Scan(&id); err != nil {
			return err
		}
		for _, slug := range p.Categories {
			if _, err := tx.Exec(ctx, `INSERT INTO post_categories (post_id, category_id) VALUES ($1, $2)`, id, catID[slug]); err != nil {
				return err
			}
		}
	}

	// every section is set: one the backup doesn't have becomes empty
	have := map[string]Menu{}
	for _, m := range c.Menus {
		have[m.Location] = m
	}
	for _, sec := range menus.Sections {
		m := have[sec.Key]
		if m.Items == nil {
			m.Items = []menus.Item{}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO menus (location, items, sticky, style) VALUES ($1, $2, $3, $4)
			ON CONFLICT (location) DO UPDATE SET items = EXCLUDED.items, sticky = EXCLUDED.sticky, style = EXCLUDED.style`, sec.Key, m.Items, m.Sticky, m.Style); err != nil {
			return err
		}
	}

	if opt.Personal {
		if err := restorePersonal(ctx, tx, c); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func restorePersonal(ctx context.Context, tx pgx.Tx, c Content) error {
	for _, q := range []string{`DELETE FROM subscribers`, `DELETE FROM messages`} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	for _, sub := range c.Subscribers {
		// the confirmation link of an old signup is not kept (only its hash ever was), so a fresh unusable one stands in
		if _, err := tx.Exec(ctx, `INSERT INTO subscribers (email, created_at, confirmed_at, token_hash, unsub_token) VALUES ($1, $2, $3, $4, NULLIF($5, ''))`,
			sub.Email, sub.CreatedAt, sub.ConfirmedAt, randomHex(), sub.UnsubToken); err != nil {
			return err
		}
	}
	for _, m := range c.Messages {
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO messages (name, email, subject, body, read, created_at) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			m.Name, m.Email, m.Subject, m.Body, m.Read, m.CreatedAt).Scan(&id); err != nil {
			return err
		}
		for _, r := range m.Replies {
			if _, err := tx.Exec(ctx, `INSERT INTO message_replies (message_id, body, created_at) VALUES ($1, $2, $3)`, id, r.Body, r.CreatedAt); err != nil {
				return err
			}
		}
	}
	return nil
}
