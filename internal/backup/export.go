package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/menus"
	"webbuilder/internal/themes"
)

// Service makes and restores backups, and keeps the backups saved on the server.
type Service struct {
	pool   *pgxpool.Pool
	media  *os.Root // the media library
	dir    *os.Root // where saved backups live; nil when the server keeps none
	engine *themes.Engine
	// Now is the clock; tests replace it.
	Now func() time.Time
}

// New opens the media folder and the folder saved backups (and uploads waiting to be restored) go in. The two must
// be different folders: the media folder is served to the public.
func New(pool *pgxpool.Pool, mediaDir, backupDir string, engine *themes.Engine) (*Service, error) {
	if backupDir == "" {
		return nil, errors.New("backup folder: not set")
	}
	if inside(mediaDir, backupDir) || inside(backupDir, mediaDir) {
		return nil, errors.New("backup folder: it must not be the media folder or inside it (the media folder is public), or the other way round")
	}
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return nil, err
	}
	media, err := os.OpenRoot(mediaDir)
	if err != nil {
		return nil, err
	}
	s := &Service{pool: pool, media: media, engine: engine, Now: time.Now}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		media.Close()
		return nil, fmt.Errorf("backup folder: %w", err)
	}
	if s.dir, err = os.OpenRoot(backupDir); err != nil {
		media.Close()
		return nil, err
	}
	return s, nil
}

func (s *Service) Close() error {
	err := s.media.Close()
	if e := s.dir.Close(); err == nil {
		err = e
	}
	return err
}

// load reads the whole site in one consistent snapshot.
func (s *Service) load(ctx context.Context, personal bool) (Content, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Content{}, err
	}
	defer tx.Rollback(ctx)

	var c Content
	st := &c.Settings
	if err := tx.QueryRow(ctx, `SELECT title, active_theme, tokens, custom_css, seo, show_search, color_mode, consent, footer, brand, details, tracking, layout FROM site_settings WHERE id = 1`).
		Scan(&st.Title, &st.ActiveTheme, &st.Tokens, &st.CustomCSS, &st.SEO, &st.ShowSearch, &st.ColorMode, &st.Consent, &st.Footer, &st.Brand, &st.Details, &st.Tracking, &st.Layout); err != nil {
		return c, err
	}

	// the shared block library (pages refer to its blocks by id)
	rows, err := tx.Query(ctx, `SELECT id, name, type, props, style FROM reusable_blocks ORDER BY id`)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var r Reusable
		if err := rows.Scan(&r.ID, &r.Name, &r.Type, &r.Props, &r.Style); err != nil {
			rows.Close()
			return c, err
		}
		c.Reusables = append(c.Reusables, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}

	rows, err = tx.Query(ctx, `SELECT from_path, to_url, permanent, auto FROM redirects ORDER BY from_path`)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var r Redirect
		if err := rows.Scan(&r.From, &r.To, &r.Permanent, &r.Auto); err != nil {
			rows.Close()
			return c, err
		}
		c.Redirects = append(c.Redirects, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}

	rows, err = tx.Query(ctx, `SELECT family, weight, data FROM fonts ORDER BY family, weight`)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var f Font
		if err := rows.Scan(&f.Family, &f.Weight, &f.Data); err != nil {
			rows.Close()
			return c, err
		}
		c.Fonts = append(c.Fonts, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}

	// pages and their blocks
	rows, err = tx.Query(ctx, `SELECT id, slug, title, status, seo, updated_at, publish_at FROM pages ORDER BY id`)
	if err != nil {
		return c, err
	}
	pageIdx := map[int64]int{}
	for rows.Next() {
		var id int64
		var p Page
		if err := rows.Scan(&id, &p.Slug, &p.Title, &p.Status, &p.SEO, &p.UpdatedAt, &p.PublishAt); err != nil {
			rows.Close()
			return c, err
		}
		p.Blocks = []Block{}
		pageIdx[id] = len(c.Pages)
		c.Pages = append(c.Pages, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}
	// the page's own blocks come first, then the blocks inside layouts, each in its column
	rows, err = tx.Query(ctx, `SELECT page_id, id, COALESCE(parent_id, 0), slot, type, props, style FROM blocks
		ORDER BY page_id, (parent_id IS NOT NULL), slot, position, id`)
	if err != nil {
		return c, err
	}
	type where struct{ page, block int }
	layouts := map[int64]where{}
	for rows.Next() {
		var pid, id, parent int64
		var slot int
		var b Block
		if err := rows.Scan(&pid, &id, &parent, &slot, &b.Type, &b.Props, &b.Style); err != nil {
			rows.Close()
			return c, err
		}
		i, ok := pageIdx[pid]
		if !ok {
			continue
		}
		if parent == 0 {
			layouts[id] = where{i, len(c.Pages[i].Blocks)}
			c.Pages[i].Blocks = append(c.Pages[i].Blocks, b)
			continue
		}
		if w, ok := layouts[parent]; ok {
			p := &c.Pages[w.page].Blocks[w.block]
			for len(p.Children) <= slot {
				p.Children = append(p.Children, []Block{})
			}
			p.Children[slot] = append(p.Children[slot], b)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}

	// categories and posts
	catSlug := map[int64]string{}
	rows, err = tx.Query(ctx, `SELECT id, slug, name FROM categories ORDER BY id`)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var id int64
		var cat Category
		if err := rows.Scan(&id, &cat.Slug, &cat.Name); err != nil {
			rows.Close()
			return c, err
		}
		catSlug[id] = cat.Slug
		c.Categories = append(c.Categories, cat)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}
	postIdx := map[int64]int{}
	rows, err = tx.Query(ctx, `SELECT id, slug, title, excerpt, body, cover_image, status, seo, published_at, updated_at, publish_at FROM posts ORDER BY id`)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var id int64
		var p Post
		if err := rows.Scan(&id, &p.Slug, &p.Title, &p.Excerpt, &p.Body, &p.Cover, &p.Status, &p.SEO, &p.PublishedAt, &p.UpdatedAt, &p.PublishAt); err != nil {
			rows.Close()
			return c, err
		}
		p.Categories = []string{}
		postIdx[id] = len(c.Posts)
		c.Posts = append(c.Posts, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}
	rows, err = tx.Query(ctx, `SELECT post_id, category_id FROM post_categories ORDER BY post_id, category_id`)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var pid, cid int64
		if err := rows.Scan(&pid, &cid); err != nil {
			rows.Close()
			return c, err
		}
		if i, ok := postIdx[pid]; ok && catSlug[cid] != "" {
			c.Posts[i].Categories = append(c.Posts[i].Categories, catSlug[cid])
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}

	// menus
	rows, err = tx.Query(ctx, `SELECT location, items, sticky FROM menus ORDER BY location`)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var m Menu
		if err := rows.Scan(&m.Location, &m.Items, &m.Sticky); err != nil {
			rows.Close()
			return c, err
		}
		if m.Items == nil {
			m.Items = []menus.Item{}
		}
		c.Menus = append(c.Menus, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}

	// the image library
	rows, err = tx.Query(ctx, `SELECT path, original_name, alt, created_at FROM media ORDER BY id`)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var m Media
		if err := rows.Scan(&m.Path, &m.OriginalName, &m.Alt, &m.CreatedAt); err != nil {
			rows.Close()
			return c, err
		}
		c.Media = append(c.Media, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}

	if personal {
		if err := loadPersonal(ctx, tx, &c); err != nil {
			return c, err
		}
	}
	return c, nil
}

func loadPersonal(ctx context.Context, tx pgx.Tx, c *Content) error {
	rows, err := tx.Query(ctx, `SELECT email, created_at, confirmed_at, COALESCE(unsub_token, '') FROM subscribers ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s Subscriber
		if err := rows.Scan(&s.Email, &s.CreatedAt, &s.ConfirmedAt, &s.UnsubToken); err != nil {
			rows.Close()
			return err
		}
		c.Subscribers = append(c.Subscribers, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	idx := map[int64]int{}
	rows, err = tx.Query(ctx, `SELECT id, name, email, subject, body, read, created_at FROM messages ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var m Message
		if err := rows.Scan(&id, &m.Name, &m.Email, &m.Subject, &m.Body, &m.Read, &m.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		idx[id] = len(c.Messages)
		c.Messages = append(c.Messages, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `SELECT message_id, body, created_at FROM message_replies ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var mid int64
		var r Reply
		if err := rows.Scan(&mid, &r.Body, &r.CreatedAt); err != nil {
			return err
		}
		if i, ok := idx[mid]; ok {
			c.Messages[i].Replies = append(c.Messages[i].Replies, r)
		}
	}
	return rows.Err()
}

// counts adds up what a Content holds.
func counts(c Content) Counts {
	n := Counts{Pages: len(c.Pages), Posts: len(c.Posts), Categories: len(c.Categories), Media: len(c.Media),
		Subscribers: len(c.Subscribers), Messages: len(c.Messages), Shared: len(c.Reusables)}
	for _, p := range c.Pages {
		n.Blocks += len(p.Blocks)
		for _, b := range p.Blocks {
			for _, col := range b.Children {
				n.Blocks += len(col)
			}
		}
	}
	return n
}

// Export writes a backup ZIP to w. personal adds the subscriber list and the contact messages.
func (s *Service) Export(ctx context.Context, w io.Writer, personal bool) (Manifest, error) {
	c, err := s.load(ctx, personal)
	if err != nil {
		return Manifest{}, err
	}
	zw := zip.NewWriter(w)
	m := Manifest{Format: Format, Version: Version, CreatedAt: s.Now().UTC().Truncate(time.Second), SiteTitle: c.Settings.Title,
		Personal: personal, Files: map[string]string{}}

	// images first: the ones whose file has gone missing are left out of the site data too
	kept := c.Media[:0]
	for _, md := range c.Media {
		sum, err := s.copyMedia(zw, md.Path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			m.Missing = append(m.Missing, md.Path)
		case err != nil:
			return m, fmt.Errorf("image %s: %w", md.Path, err)
		default:
			m.Files[MediaPrefix+md.Path] = sum
			kept = append(kept, md)
		}
	}
	c.Media = kept

	site, err := marshal(c)
	if err != nil {
		return m, err
	}
	if err := addFile(zw, SiteName, site, zip.Deflate, m.CreatedAt); err != nil {
		return m, err
	}
	sum := sha256.Sum256(site)
	m.Files[SiteName] = hex.EncodeToString(sum[:])
	m.Counts = counts(c)

	mf, err := marshal(m)
	if err != nil {
		return m, err
	}
	if err := addFile(zw, ManifestName, mf, zip.Deflate, m.CreatedAt); err != nil {
		return m, err
	}
	return m, zw.Close()
}

func addFile(zw *zip.Writer, name string, data []byte, method uint16, at time.Time) error {
	f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: at})
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}

// copyMedia stores one image in the ZIP (without compressing it again) and returns its SHA-256.
func (s *Service) copyMedia(zw *zip.Writer, path string) (string, error) {
	if !mediaPathRe.MatchString(path) {
		return "", fmt.Errorf("unexpected image name")
	}
	f, err := s.media.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	out, err := zw.CreateHeader(&zip.FileHeader{Name: MediaPrefix + path, Method: zip.Store, Modified: s.Now().UTC()})
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, MaxMediaBytes+1))
	if err != nil {
		return "", err
	}
	if n > MaxMediaBytes {
		return "", fmt.Errorf("larger than %d MB", MaxMediaBytes>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// inside reports whether path b is the same folder as a or lies within it.
func inside(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return true // can't tell: refuse
	}
	rel, err := filepath.Rel(aa, bb)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
