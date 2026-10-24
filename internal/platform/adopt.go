package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/auth"
	"webbuilder/internal/config"
	"webbuilder/internal/db"
)

// AdoptOptions say how to turn a database that runs one website (the program without PLATFORM=true) into one that runs
// the platform, with that website as the first customer's.
type AdoptOptions struct {
	// Owner is the email of the existing user who becomes the website's owner.
	Owner string
	// MediaFrom and BackupsFrom are the single-site install's picture and backup folders. They are copied, not moved,
	// into the platform's own place for the website. Empty leaves them.
	MediaFrom, BackupsFrom string
	// Apply makes the changes. Without it nothing is changed and the report says what would happen.
	Apply bool
}

// AdoptReport is what was done (or, without Apply, what would be).
type AdoptReport struct {
	Applied    bool
	SiteName   string
	Owner      string
	PublicURL  string   // the saved domain, as the address the website will be published at ("" if none)
	Admins     []string // accounts that become platform administrators
	Skipped    []string // users who cannot become accounts: the platform has one editor per website, its owner
	Tables     int
	MediaFiles int
	Notes      []string
}

type adoptUser struct {
	id               int64
	email, hash, role string
}

// Adopt moves a single-site install's tables into a schema of their own (site_1), sets up the platform's tables beside
// them, and turns the install's users into platform accounts that keep their ids and passwords: the owner gets the
// website, administrators become platform administrators. The steps can be repeated if one of them stops half way.
func Adopt(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, opt AdoptOptions) (AdoptReport, error) {
	var rep AdoptReport
	const schema = "site_1"
	exists := func(q string, args ...any) (bool, error) {
		var ok bool
		err := pool.QueryRow(ctx, q, args...).Scan(&ok)
		return ok, err
	}
	hasPages, err := exists(`SELECT to_regclass('public.pages') IS NOT NULL`)
	if err != nil {
		return rep, err
	}
	hasSites, err := exists(`SELECT to_regclass('public.sites') IS NOT NULL`)
	if err != nil {
		return rep, err
	}
	movedBefore, err := exists(`SELECT to_regclass('site_1.pages') IS NOT NULL`)
	if err != nil {
		return rep, err
	}
	src := "public"
	switch {
	case hasSites && !hasPages:
		return rep, errors.New("this database already runs the platform: there is nothing to adopt")
	case hasSites && hasPages:
		return rep, errors.New("this database has both the platform's tables and a single website's: it is in a state this tool does not know how to repair. Restore the backup you made before starting")
	case !hasPages && movedBefore:
		src = schema // an earlier run moved the tables and stopped before the platform was set up: carry on
		rep.Notes = append(rep.Notes, "An earlier run already moved the website's tables; carrying on from there.")
	case !hasPages:
		return rep, errors.New("no single website was found in this database (there is no pages table)")
	}
	if src == "public" {
		if used, err := exists(`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'site_1')`); err != nil {
			return rep, err
		} else if used {
			return rep, errors.New("a schema called site_1 already exists next to this website's tables")
		}
	}

	// who the users are, and who becomes what
	rows, err := pool.Query(ctx, fmt.Sprintf(`SELECT id, email, password_hash, role FROM %s.users ORDER BY id`, src))
	if err != nil {
		return rep, err
	}
	var users []adoptUser
	for rows.Next() {
		var u adoptUser
		if err := rows.Scan(&u.id, &u.email, &u.hash, &u.role); err != nil {
			rows.Close()
			return rep, err
		}
		users = append(users, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, err
	}
	owner := auth.NormalizeEmail(opt.Owner)
	var accounts []adoptUser
	var ownerID int64
	for _, u := range users {
		switch {
		case u.email == owner:
			ownerID = u.id
			if u.role != "admin" {
				u.role = "customer"
			}
			accounts = append(accounts, u)
		case u.role == "admin":
			accounts = append(accounts, u)
		default:
			rep.Skipped = append(rep.Skipped, u.email)
		}
	}
	if ownerID == 0 {
		var have []string
		for _, u := range users {
			have = append(have, u.email)
		}
		return rep, fmt.Errorf("-owner must be one of this website's users: %s", strings.Join(have, ", "))
	}
	admins := 0
	for _, u := range accounts {
		if u.role == "admin" {
			admins++
			rep.Admins = append(rep.Admins, u.email)
		}
	}
	if admins == 0 {
		return rep, errors.New("none of this website's users is an administrator, and the platform needs at least one. Make one of them an administrator first (or name an administrator as the owner)")
	}
	rep.Owner = owner

	var title, domain string
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT title, domain FROM %s.site_settings WHERE id = 1`, src)).Scan(&title, &domain); err != nil {
		return rep, err
	}
	rep.SiteName = strings.TrimSpace(title)
	if rep.SiteName == "" {
		rep.SiteName = owner
	}
	rep.SiteName = cleanName(rep.SiteName)
	if domain != "" {
		rep.PublicURL = "https://" + domain
	}
	if len(rep.Skipped) > 0 {
		rep.Notes = append(rep.Notes, "These users get no account (the platform gives a website to one customer, who is its editor): "+strings.Join(rep.Skipped, ", ")+". They stay in the website's history.")
	}
	if rep.PublicURL == "" {
		rep.Notes = append(rep.Notes, "The website has no saved domain: set its address and hosting under Publish before publishing.")
	}
	if opt.MediaFrom == "" {
		rep.Notes = append(rep.Notes, "Uploaded pictures were not copied (no -media-from): copy the old media folder to DATA_DIR/media/1 yourself, or the pictures will be missing.")
	}

	if !opt.Apply {
		if src == "public" {
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`).Scan(&rep.Tables); err != nil {
				return rep, err
			}
		}
		return rep, nil
	}

	// 1. the website's tables (and the function its change counter uses) move into their own schema, all or nothing
	if src == "public" {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return rep, err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
			return rep, err
		}
		tables, err := tx.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
		if err != nil {
			return rep, err
		}
		var names []string
		for tables.Next() {
			var t string
			if err := tables.Scan(&t); err != nil {
				tables.Close()
				return rep, err
			}
			names = append(names, t)
		}
		tables.Close()
		for _, t := range names {
			if _, err := tx.Exec(ctx, `ALTER TABLE public.`+pgx.Identifier{t}.Sanitize()+` SET SCHEMA `+schema); err != nil {
				return rep, fmt.Errorf("moving %s: %w", t, err)
			}
		}
		rep.Tables = len(names)
		// the program's own functions (the change counter's trigger); an extension's are not ours to move
		fns, err := tx.Query(ctx, `SELECT p.oid::regprocedure::text FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'public' AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')`)
		if err != nil {
			return rep, err
		}
		var funcs []string
		for fns.Next() {
			var f string
			if err := fns.Scan(&f); err != nil {
				fns.Close()
				return rep, err
			}
			funcs = append(funcs, f)
		}
		fns.Close()
		for _, f := range funcs {
			if _, err := tx.Exec(ctx, `ALTER FUNCTION `+f+` SET SCHEMA `+schema); err != nil {
				return rep, fmt.Errorf("moving function %s: %w", f, err)
			}
			// the change counter's function was made to look in the schema it was created in (the default one): it must now look here
			if _, err := tx.Exec(ctx, `ALTER FUNCTION `+schema+`.`+f+` SET search_path = `+schema); err != nil {
				return rep, fmt.Errorf("pointing function %s at its schema: %w", f, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return rep, err
		}
	}

	// 2. the platform's own tables, beside the (now empty) default schema
	if err := db.MigratePlatform(pool); err != nil {
		return rep, fmt.Errorf("setting up the platform's tables: %w", err)
	}

	// 3. accounts that keep their ids and passwords, the website, and a clean slate for what only a lone website uses
	tx, err := pool.Begin(ctx)
	if err != nil {
		return rep, err
	}
	defer tx.Rollback(ctx)
	var sites int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM sites`).Scan(&sites); err != nil {
		return rep, err
	}
	if sites > 0 {
		return rep, errors.New("the platform already has websites: this tool only adopts into an empty platform")
	}
	for _, u := range accounts {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, password_hash, role) VALUES ($1, $2, $3, $4)`, u.id, u.email, u.hash, u.role); err != nil {
			return rep, fmt.Errorf("account %s: %w", u.email, err)
		}
	}
	if _, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT max(id) FROM users))`); err != nil {
		return rep, err
	}
	token := randomHex(16)
	if _, err := tx.Exec(ctx, `INSERT INTO sites (id, owner_id, name, schema_name, public_token, public_url) VALUES (1, $1, $2, $3, $4, $5)`,
		ownerID, rep.SiteName, schema, token, rep.PublicURL); err != nil {
		return rep, err
	}
	if _, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence('sites', 'id'), 1)`); err != nil {
		return rep, err
	}
	// logins belong to the platform now: what the website kept of them is only history
	for _, q := range []string{
		`UPDATE ` + schema + `.users SET password_hash = '!'`,
		`TRUNCATE ` + schema + `.sessions, ` + schema + `.invites, ` + schema + `.password_resets`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return rep, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log (actor, site_id, site_name, action, detail) VALUES ('system', 1, $1, 'adopted a single website', $2)`,
		rep.SiteName, fmt.Sprintf("%d table(s), %d account(s)", rep.Tables, len(accounts))); err != nil {
		return rep, err
	}
	if err := tx.Commit(ctx); err != nil {
		return rep, err
	}

	// 4. the files
	if opt.MediaFrom != "" {
		n, err := copyFolder(opt.MediaFrom, filepath.Join(cfg.DataDir, "media", "1"))
		if err != nil {
			return rep, fmt.Errorf("copying the pictures: %w", err)
		}
		rep.MediaFiles = n
	}
	if opt.BackupsFrom != "" {
		if _, err := copyFolder(opt.BackupsFrom, filepath.Join(cfg.DataDir, "backups", "1")); err != nil {
			return rep, fmt.Errorf("copying the backups: %w", err)
		}
	}
	rep.Applied = true
	return rep, nil
}

// copyFolder copies a folder into one that does not exist yet (or is empty), and returns how many files it copied.
func copyFolder(from, to string) (int, error) {
	if st, err := os.Stat(from); err != nil || !st.IsDir() {
		return 0, fmt.Errorf("%s is not a folder", from)
	}
	if entries, err := os.ReadDir(to); err == nil && len(entries) > 0 {
		return 0, fmt.Errorf("%s already has files in it", to)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return 0, err
	}
	if err := os.CopyFS(to, os.DirFS(from)); err != nil {
		return 0, err
	}
	n := 0
	err := filepath.WalkDir(to, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	return n, err
}
