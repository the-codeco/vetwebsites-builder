package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service keeps the image library: files in the media folder and a row for each in the database.
type Service struct {
	pool *pgxpool.Pool
	root *os.Root
}

func New(pool *pgxpool.Pool, dir string) (*Service, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, root: root}, nil
}

func (s *Service) Close() error { return s.root.Close() }

const itemColumns = `m.id, m.path, m.thumb_path, m.mime, m.size_bytes, m.original_name, m.alt, m.width, m.height, m.created_at,
	(SELECT count(DISTINCT b.page_id) FROM blocks b WHERE b.props::text LIKE '%/media/' || m.path || '%')`

func scanItem(row pgx.Row) (Item, error) {
	var it Item
	err := row.Scan(&it.ID, &it.Path, &it.ThumbPath, &it.Mime, &it.SizeBytes, &it.OriginalName, &it.Alt, &it.Width, &it.Height, &it.CreatedAt, &it.UsedOn)
	return it, err
}

// List returns one page of images, newest first, and how many there are in all.
func (s *Service) List(ctx context.Context, limit, offset int) ([]Item, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM media`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+itemColumns+` FROM media m ORDER BY m.id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	return items, total, rows.Err()
}

func (s *Service) Get(ctx context.Context, id int64) (Item, error) {
	it, err := scanItem(s.pool.QueryRow(ctx, `SELECT `+itemColumns+` FROM media m WHERE m.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	return it, err
}

func (s *Service) SetAlt(ctx context.Context, id int64, alt string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE media SET alt = $2 WHERE id = $1`, id, alt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes an image, its thumbnail and its resized copies.
func (s *Service) Delete(ctx context.Context, id int64) error {
	var p, t string
	err := s.pool.QueryRow(ctx, `DELETE FROM media WHERE id = $1 RETURNING path, thumb_path`, id).Scan(&p, &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	s.root.Remove(p)
	if t != "" && t != p {
		s.root.Remove(t)
	}
	for _, v := range VariantFiles(p) {
		s.root.Remove(v)
	}
	return nil
}

// Save checks an upload, removes its hidden details and stores it with a thumbnail. Nothing is written
// unless the whole picture is valid.
func (s *Service) Save(ctx context.Context, userID int64, filename string, data []byte) (Item, error) {
	info, err := Inspect(data)
	if err != nil {
		return Item{}, err
	}
	cleaned, info, err := clean(data, info)
	if err != nil {
		return Item{}, err
	}
	return s.store(ctx, userID, CleanName(filename), "", cleaned, info)
}

// Edited saves a turned, mirrored, cropped or shrunk copy of an image as a new image; the original stays as it is.
func (s *Service) Edited(ctx context.Context, userID, id int64, e Edit) (Item, error) {
	it, err := s.Get(ctx, id)
	if err != nil {
		return Item{}, err
	}
	if it.Mime == "image/gif" {
		return Item{}, ErrNotEditable
	}
	data, err := s.root.ReadFile(it.Path)
	if err != nil {
		return Item{}, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Item{}, ErrCorrupt
	}
	img, err := e.apply(src)
	if err != nil {
		return Item{}, err
	}
	ext := ".png"
	if it.Mime == "image/jpeg" {
		ext = ".jpg"
	}
	out, err := encode(img, ext, 90)
	if err != nil {
		return Item{}, err
	}
	if len(out) > MaxFileSize {
		return Item{}, ErrTooLarge
	}
	info, err := Inspect(out)
	if err != nil {
		return Item{}, err
	}
	name := strings.TrimSuffix(it.OriginalName, path.Ext(it.OriginalName)) + " (edited)" + ext
	return s.store(ctx, userID, CleanName(name), it.Alt, out, info)
}

func (s *Service) store(ctx context.Context, userID int64, name, alt string, data []byte, info Info) (Item, error) {
	thumb, err := MakeThumb(data, info)
	if err != nil {
		return Item{}, err
	}
	var rnd [12]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return Item{}, err
	}
	p := time.Now().UTC().Format("2006/01") + "/" + hex.EncodeToString(rnd[:]) + info.Ext
	if err := s.root.MkdirAll(filepath.ToSlash(path.Dir(p)), 0o755); err != nil {
		return Item{}, err
	}
	var written []string
	undo := func() {
		for _, w := range written {
			s.root.Remove(w)
		}
	}
	put := func(name string, b []byte) error {
		f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		written = append(written, name)
		if _, err := f.Write(b); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
	if err := put(p, data); err != nil {
		undo()
		return Item{}, err
	}
	tp := p
	if thumb != nil {
		tp = ThumbName(p, thumb.Ext)
		if err := put(tp, thumb.Data); err != nil {
			undo()
			return Item{}, err
		}
	}
	var by any
	if userID != 0 {
		by = userID
	}
	var id int64
	var created time.Time
	err = s.pool.QueryRow(ctx, `INSERT INTO media (path, thumb_path, mime, size_bytes, original_name, alt, width, height, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id, created_at`,
		p, tp, info.Mime, len(data), name, alt, info.Width, info.Height, by).Scan(&id, &created)
	if err != nil {
		undo()
		return Item{}, err
	}
	return Item{ID: id, Path: p, ThumbPath: tp, Mime: info.Mime, SizeBytes: int64(len(data)), OriginalName: name, Alt: alt,
		Width: info.Width, Height: info.Height, CreatedAt: created}, nil
}
