package content

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Limits for uploaded fonts.
const (
	MaxFonts      = 12
	MaxFontBytes  = 600 << 10
	MinFontBytes  = 256
	MaxFamilyName = 40
)

// FontWeights are the weights a font file can be for: regular and bold.
var FontWeights = []int{400, 700}

var (
	ErrFontsFull = errors.New("the font list is full")
	ErrNotAFont  = errors.New("that file is not a WOFF2 font")
	familyRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 \-]{0,38}[A-Za-z0-9]$|^[A-Za-z0-9]$`)
	customFontRe = regexp.MustCompile(`^custom:[a-z0-9]+(-[a-z0-9]+)*$`)
	slugBreakRe  = regexp.MustCompile(`[^a-z0-9]+`)
)

// Font is an uploaded font file (the bytes are loaded separately).
type Font struct {
	ID     int64
	Family string
	Weight int
	Size   int
	Hash   string
}

// FileName is the address a font is served from: its id and the start of its content hash, so a replaced
// file gets a new address and browsers never keep an old copy.
func (f Font) FileName() string { return strconv.FormatInt(f.ID, 10) + "-" + f.Hash[:10] + ".woff2" }

// FontToken is the value a theme setting holds to use a family: "custom:" and the family name in lower case with dashes.
func FontToken(family string) string {
	return "custom:" + strings.Trim(slugBreakRe.ReplaceAllString(strings.ToLower(family), "-"), "-")
}

// IsFontToken says whether a setting value has the shape of an uploaded font's (it may still name one that is gone).
func IsFontToken(v string) bool { return len(v) <= 60 && customFontRe.MatchString(v) }

// CleanFamily checks a family name: letters, digits, spaces and dashes, so it is safe inside CSS quotes.
func CleanFamily(raw string) (string, bool) {
	s := strings.Join(strings.Fields(raw), " ")
	if !familyRe.MatchString(s) || len(s) > MaxFamilyName {
		return "", false
	}
	return s, true
}

// CheckFont says whether data is a plausible WOFF2 file: the right signature, a length field that agrees with the
// file's size, and a size within limits. It does not interpret the font, which only the visitor's browser does.
func CheckFont(data []byte) error {
	if len(data) < MinFontBytes || len(data) > MaxFontBytes {
		return ErrNotAFont
	}
	if string(data[:4]) != "wOF2" || int(binary.BigEndian.Uint32(data[8:12])) != len(data) {
		return ErrNotAFont
	}
	return nil
}

func fontHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Fonts lists the uploaded fonts, by family.
func (s *Store) Fonts(ctx context.Context) ([]Font, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, family, weight, octet_length(data), hash FROM fonts ORDER BY lower(family), weight`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Font
	for rows.Next() {
		var f Font
		if err := rows.Scan(&f.ID, &f.Family, &f.Weight, &f.Size, &f.Hash); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AddFont saves a font file; the same family and weight is replaced.
func (s *Store) AddFont(ctx context.Context, family string, weight int, data []byte) error {
	family, ok := CleanFamily(family)
	if !ok || (weight != 400 && weight != 700) {
		return ErrNotAFont
	}
	if err := CheckFont(data); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM fonts WHERE NOT (family = $1 AND weight = $2)`, family, weight).Scan(&n); err != nil {
		return err
	}
	if n >= MaxFonts {
		return ErrFontsFull
	}
	// one slug per family: two names that differ only by punctuation would collide on the same token
	var clash string
	err = tx.QueryRow(ctx, `SELECT family FROM fonts WHERE family <> $1 AND regexp_replace(lower(family), '[^a-z0-9]+', '-', 'g') = regexp_replace(lower($1), '[^a-z0-9]+', '-', 'g') LIMIT 1`, family).Scan(&clash)
	if err == nil {
		family = clash // the same family under a slightly different spelling: use the name already there
	}
	if _, err := tx.Exec(ctx, `INSERT INTO fonts (family, weight, data, hash) VALUES ($1, $2, $3, $4)
		ON CONFLICT (family, weight) DO UPDATE SET data = EXCLUDED.data, hash = EXCLUDED.hash, created_at = now()`,
		family, weight, data, fontHash(data)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteFont(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM fonts WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// FontData returns a font file by id, with its hash.
func (s *Store) FontData(ctx context.Context, id int64) (data []byte, hash string, err error) {
	err = s.pool.QueryRow(ctx, `SELECT data, hash FROM fonts WHERE id = $1`, id).Scan(&data, &hash)
	return data, hash, notFound(err)
}

// BackupFont is a font as it travels in a backup.
type BackupFont struct {
	Family string
	Weight int
	Data   []byte
}

// ReplaceFonts swaps the whole font list inside a restore's transaction. The callers have already checked each file.
func ReplaceFonts(ctx context.Context, tx pgx.Tx, list []BackupFont) error {
	if _, err := tx.Exec(ctx, `DELETE FROM fonts`); err != nil {
		return err
	}
	for _, f := range list {
		if _, err := tx.Exec(ctx, `INSERT INTO fonts (family, weight, data, hash) VALUES ($1, $2, $3, $4)`, f.Family, f.Weight, f.Data, fontHash(f.Data)); err != nil {
			return err
		}
	}
	return nil
}
