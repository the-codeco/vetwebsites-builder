package content

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"webbuilder/internal/blocks"
)

// MaxRedirects is how many redirects the site keeps.
const MaxRedirects = 1000

var (
	ErrRedirectExists = errors.New("there is already a redirect from that address")
	ErrRedirectsFull  = errors.New("the redirect list is full")
	ErrRedirectLoop   = errors.New("that would send visitors round in a circle")
)

// Redirect sends visitors from an old address to a new one.
type Redirect struct {
	ID        int64
	From, To  string
	Permanent bool // 301 (search engines move to the new address) rather than 302
	Auto      bool // made automatically when a page or post changed address
	CreatedAt time.Time
}

// reservedPrefixes are addresses the program itself answers; a redirect from one would never be used.
var reservedPrefixes = []string{"/admin", "/static", "/theme", "/media", "/internal", "/healthz", "/site.css", "/sitemap.xml", "/robots.txt", "/setup", "/invite", "/contact", "/newsletter", "/search", "/rss.xml"}

// CleanRedirectFrom turns what was typed into the stored form of an old address (a path, lower case, no
// trailing slash) or says what is wrong with it.
func CleanRedirectFrom(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", errors.New("Enter the old address, such as /old-page.")
	}
	if l := strings.ToLower(p); strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		u, err := url.Parse(p)
		if err != nil || u.Path == "" {
			return "", errors.New("The old address must be a path on this site, such as /old-page.")
		}
		p = u.Path // someone pasted a full address: only the path matters
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "?#\\ \t\r\n") {
		return "", errors.New("The old address must be a path on this site such as /old-page, with no ?, # or spaces.")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("The old address has characters that are not allowed.")
		}
	}
	if len(p) > 300 {
		return "", errors.New("The old address is too long.")
	}
	if p = NormalizePath(p); p == "/" {
		return "", errors.New("The home page cannot be redirected.")
	}
	for _, pre := range reservedPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return "", fmt.Errorf("%s is used by the site itself and cannot be redirected.", pre)
		}
	}
	return p, nil
}

// NormalizePath lowers a request path and drops a trailing slash, so /About/ and /about are the same address.
func NormalizePath(p string) string {
	p = strings.ToLower(p)
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	if p == "" {
		p = "/"
	}
	return p
}

// CleanRedirectTo checks where a redirect goes: a path on this site, or an http(s) address.
func CleanRedirectTo(raw string) (string, error) {
	t := blocks.SafeURL(strings.TrimSpace(raw))
	l := strings.ToLower(t)
	if t == "" || strings.HasPrefix(l, "mailto:") || strings.HasPrefix(l, "tel:") || t[0] == '#' {
		return "", errors.New("Enter where it should go: a path such as /new-page, or an https:// address.")
	}
	if len(t) > 2000 {
		return "", errors.New("The new address is too long.")
	}
	return t, nil
}

// pathOf is the path part of a redirect target on this site ("" for another site).
func pathOf(to string) string {
	if strings.HasPrefix(to, "/") {
		p, _, _ := strings.Cut(to, "?")
		p, _, _ = strings.Cut(p, "#")
		return NormalizePath(p)
	}
	return ""
}

func (s *Store) Redirects(ctx context.Context) ([]Redirect, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, from_path, to_url, permanent, auto, created_at FROM redirects ORDER BY from_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Redirect
	for rows.Next() {
		var r Redirect
		if err := rows.Scan(&r.ID, &r.From, &r.To, &r.Permanent, &r.Auto, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RedirectFor finds where a request path should be sent, if anywhere.
func (s *Store) RedirectFor(ctx context.Context, path string) (to string, permanent, ok bool) {
	err := s.pool.QueryRow(ctx, `SELECT to_url, permanent FROM redirects WHERE from_path = $1`, NormalizePath(path)).Scan(&to, &permanent)
	return to, permanent, err == nil
}

// AddRedirect saves a redirect (both addresses already cleaned). It refuses one that would send visitors in a
// circle: back to where they started, or through other redirects that return here.
func (s *Store) AddRedirect(ctx context.Context, from, to string, permanent bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM redirects`).Scan(&n); err != nil {
		return err
	}
	if n >= MaxRedirects {
		return ErrRedirectsFull
	}
	if err := checkLoop(ctx, tx, from, to); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO redirects (from_path, to_url, permanent) VALUES ($1, $2, $3)`, from, to, permanent)
	if isUnique(err) {
		return ErrRedirectExists
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func checkLoop(ctx context.Context, tx pgx.Tx, from, to string) error {
	at := pathOf(to)
	for hops := 0; at != "" && hops < 20; hops++ {
		if at == from {
			return ErrRedirectLoop
		}
		var next string
		err := tx.QueryRow(ctx, `SELECT to_url FROM redirects WHERE from_path = $1`, at).Scan(&next)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		at = pathOf(next)
	}
	return nil
}

func (s *Store) DeleteRedirect(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM redirects WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// moveAddress records that a page or post now lives at a new address: visitors and search engines arriving at the
// old one are sent to the new one. Redirects that pointed at the old address are updated to skip the extra hop, and
// a redirect from the new address (there is a page there now) is removed.
func moveAddress(ctx context.Context, tx pgx.Tx, oldPath, newPath string) error {
	if oldPath == newPath {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM redirects WHERE from_path = $1`, newPath); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE redirects SET to_url = $2 WHERE lower(to_url) = $1`, oldPath, newPath); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM redirects`).Scan(&n); err != nil {
		return err
	}
	if n >= MaxRedirects {
		return nil // the list is full; the change itself must still go through
	}
	_, err := tx.Exec(ctx, `INSERT INTO redirects (from_path, to_url, permanent, auto) VALUES ($1, $2, true, true)
		ON CONFLICT (from_path) DO UPDATE SET to_url = EXCLUDED.to_url, permanent = true, auto = true`, oldPath, newPath)
	return err
}

// ReplaceRedirects swaps the whole list inside a restore's transaction.
func ReplaceRedirects(ctx context.Context, tx pgx.Tx, list []Redirect) error {
	if _, err := tx.Exec(ctx, `DELETE FROM redirects`); err != nil {
		return err
	}
	for _, r := range list {
		if _, err := tx.Exec(ctx, `INSERT INTO redirects (from_path, to_url, permanent, auto) VALUES ($1, $2, $3, $4)`, r.From, r.To, r.Permanent, r.Auto); err != nil {
			return err
		}
	}
	return nil
}
