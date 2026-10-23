package content

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
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

// Redirect sends visitors from an old address to a new one. The old address is one of three shapes:
//
//	/old-page                        that address
//	/index.php?id=5&option=com_x     that path with (at least) these query values; others, such as Itemid, are ignored
//	/index.php/*                     every address under that prefix; a * in the new address is replaced by the
//	                                 rest of the old one, without an old ".html" or ".htm" ending
//
// A query redirect that matches wins over an exact address (so /index.php?id=5 can go to its own page while
// /index.php goes home), and either wins over a prefix; the longest matching prefix wins.
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
// trailing slash; a query in a fixed order; or a prefix ending in /*) or says what is wrong with it.
func CleanRedirectFrom(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", errors.New("Enter the old address, such as /old-page.")
	}
	var q string
	if l := strings.ToLower(p); strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		u, err := url.Parse(p)
		if err != nil || u.Path == "" {
			return "", errors.New("The old address must be a path on this site, such as /old-page.")
		}
		p, q = u.Path, u.RawQuery // someone pasted a full address: the host doesn't matter
	} else {
		p, q, _ = strings.Cut(p, "?")
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "?#\\ \t\r\n") {
		return "", errors.New("The old address must be a path on this site such as /old-page, with no # or spaces.")
	}
	if strings.Contains(p, "*") && (!strings.HasSuffix(p, "/*") || strings.Count(p, "*") > 1 || q != "") {
		return "", errors.New("A * can only end an old address, after a slash (such as /index.php/*), with no ? part.")
	}
	if q != "" {
		cq, err := canonicalQuery(q)
		if err != nil {
			return "", errors.New("The part after ? must look like name=value&name=value.")
		}
		q = cq
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("The old address has characters that are not allowed.")
		}
	}
	if p = NormalizePath(p); p == "/" || p == "/*" {
		return "", errors.New("The home page cannot be redirected.")
	}
	for _, pre := range reservedPrefixes {
		if bare := strings.TrimSuffix(p, "/*"); bare == pre || strings.HasPrefix(bare, pre+"/") {
			return "", fmt.Errorf("%s is used by the site itself and cannot be redirected.", pre)
		}
	}
	if q != "" {
		p += "?" + q
	}
	if len(p) > 300 {
		return "", errors.New("The old address is too long.")
	}
	return p, nil
}

// canonicalQuery is a query in the form stored and compared: lower case, each name once, names in order.
func canonicalQuery(q string) (string, error) {
	vals, err := url.ParseQuery(strings.ToLower(q))
	if err != nil {
		return "", err
	}
	out := url.Values{}
	for k, v := range vals {
		if k != "" && len(v) > 0 {
			out.Set(k, v[0])
		}
	}
	return out.Encode(), nil
}

// CheckRedirectStar refuses a * in the new address when the old one is not a prefix (it would have nothing to
// stand for), and a prefix redirect that leads back into itself.
func CheckRedirectStar(from, to string) error {
	wild := strings.HasSuffix(from, "/*")
	switch {
	case strings.Contains(to, "*") && !wild:
		return errors.New("A * in the new address only works when the old address ends in /* (such as /index.php/*).")
	case strings.Count(to, "*") > 1:
		return errors.New("The new address can have at most one *.")
	case wild && strings.HasPrefix(to, "/") && strings.HasPrefix(NormalizePath(to), strings.TrimSuffix(from, "*")):
		return ErrRedirectLoop
	}
	return nil
}

// RedirectTarget is where a redirect sends a request for path: the new address, with a * replaced by the part of
// the path under a prefix redirect's prefix (and an old .html or .htm ending dropped).
func RedirectTarget(r Redirect, path string) string {
	if !strings.HasSuffix(r.From, "/*") || !strings.Contains(r.To, "*") {
		return r.To
	}
	// with a slash added, the prefix's own address (/index.php for /index.php/*) leaves "" as the rest
	rest := strings.TrimSuffix(strings.TrimPrefix(NormalizePath(path)+"/", strings.TrimSuffix(r.From, "*")), "/")
	for _, ext := range []string{".html", ".htm"} {
		rest = strings.TrimSuffix(rest, ext)
	}
	return strings.Replace(r.To, "*", rest, 1)
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

// RedirectFor finds where a request (its path and raw query) should be sent, if anywhere: the query redirect for
// that path that matches the most query values, then the exact address, then the longest prefix.
func (s *Store) RedirectFor(ctx context.Context, path, rawQuery string) (to string, permanent, ok bool) {
	p := NormalizePath(path)
	if rawQuery != "" {
		if req, err := url.ParseQuery(strings.ToLower(rawQuery)); err == nil {
			rows, err := s.pool.Query(ctx, `SELECT from_path, to_url, permanent FROM redirects WHERE starts_with(from_path, $1)`, p+"?")
			if err != nil {
				return "", false, false
			}
			best := 0
			for rows.Next() {
				var from, t string
				var perm bool
				if rows.Scan(&from, &t, &perm) != nil {
					continue
				}
				want, _ := url.ParseQuery(strings.SplitN(from, "?", 2)[1])
				if n := queryMatch(want, req); n > best {
					best, to, permanent = n, t, perm
				}
			}
			rows.Close()
			if best > 0 {
				return to, permanent, true
			}
		}
	}
	if err := s.pool.QueryRow(ctx, `SELECT to_url, permanent FROM redirects WHERE from_path = $1`, p).Scan(&to, &permanent); err == nil {
		return to, permanent, true
	}
	rows, err := s.pool.Query(ctx, `SELECT from_path, to_url, permanent FROM redirects WHERE from_path LIKE '%/*'`)
	if err != nil {
		return "", false, false
	}
	defer rows.Close()
	var hit Redirect
	for rows.Next() {
		var r Redirect
		if rows.Scan(&r.From, &r.To, &r.Permanent) != nil {
			continue
		}
		if strings.HasPrefix(p+"/", strings.TrimSuffix(r.From, "*")) && len(r.From) > len(hit.From) {
			hit = r
		}
	}
	if hit.From == "" {
		return "", false, false
	}
	return RedirectTarget(hit, p), hit.Permanent, true
}

// queryMatch is how many of want's values the request has (0 unless it has all of them).
func queryMatch(want, req url.Values) int {
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if req.Get(k) != want.Get(k) {
			return 0
		}
	}
	return len(keys)
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
	if err := CheckRedirectStar(from, to); err != nil {
		return err
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
