package platform_test

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"webbuilder/internal/auth"
	"webbuilder/internal/config"
	"webbuilder/internal/db"
	"webbuilder/internal/platform"
)

const oldPassword = "old-password-123"

// adoptEnv is a database holding one website the single-site way (everything in the default schema), with a few users,
// a page, a picture and some state a lone website keeps.
func adoptEnv(t *testing.T) (cfg config.Config, e *env, media string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL (a database whose name ends in _test) to run the platform integration tests")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") {
		t.Fatalf("TEST_DATABASE_URL must name a database ending in _test")
	}
	name := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "_test") + "_adopt_test"
	u.Path = "/" + name
	dsn = u.String()
	ensureDB(t, dsn, name)
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DO $$ DECLARE r record; BEGIN
		FOR r IN SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'site\_%' LOOP
			EXECUTE 'DROP SCHEMA ' || quote_ident(r.schema_name) || ' CASCADE';
		END LOOP; END $$; DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(pool); err != nil { // the single-site program's own tables, in the default schema
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (email, password_hash, role) VALUES ('stephen@old.example', $1, 'admin'), ('ed@old.example', $1, 'editor'), ('second@old.example', $1, 'admin'), ('skipme@old.example', $1, 'editor')`, []any{hash}},
		{`UPDATE site_settings SET title = 'Old Clinic', domain = 'www.oldclinic.example'`, nil},
		{`INSERT INTO pages (slug, title, status) VALUES ('about-old', 'About the old site', 'published')`, nil},
		{`INSERT INTO media (path, mime, size_bytes, original_name, uploaded_by) VALUES ('2026/10/aaaaaaaaaaaaaaaaaaaaaaaa.jpg', 'image/jpeg', 4, 'old.jpg', (SELECT id FROM users WHERE email = 'ed@old.example'))`, nil},
		{`INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at) SELECT '\x01'::bytea, id, 'x', now() + interval '1 day' FROM users WHERE email = 'stephen@old.example'`, nil},
		{`INSERT INTO subscribers (email, confirmed_at, token_hash) VALUES ('fan@x.example', now(), 'th')`, nil},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	media = filepath.Join(t.TempDir(), "old-media")
	os.MkdirAll(filepath.Join(media, "2026", "10"), 0o755)
	os.WriteFile(filepath.Join(media, "2026", "10", "aaaaaaaaaaaaaaaaaaaaaaaa.jpg"), []byte("jpeg"), 0o644)

	cfg = config.Config{DatabaseURL: dsn, DataDir: t.TempDir(), MaxRuntimes: 10, BaseURL: "https://builder.example.test"}
	e = &env{t: t, pool: pool, cfg: cfg}
	return cfg, e, media
}

// An install that runs one website can become a platform's first customer: its tables move into a schema of their own,
// its users become accounts that keep their ids and passwords, and nothing of it is lost.
func TestAdoptASingleWebsite(t *testing.T) {
	cfg, e, media := adoptEnv(t)
	ctx := context.Background()
	pool := e.pool

	// the owner must be one of the users, and the platform needs an administrator
	if _, err := platform.Adopt(ctx, cfg, pool, platform.AdoptOptions{Owner: "nobody@old.example"}); err == nil || !strings.Contains(err.Error(), "stephen@old.example") {
		t.Errorf("an owner that is not a user: %v", err)
	}
	pool.Exec(ctx, `UPDATE users SET role = 'editor' WHERE role = 'admin'`)
	if _, err := platform.Adopt(ctx, cfg, pool, platform.AdoptOptions{Owner: "ed@old.example"}); err == nil || !strings.Contains(err.Error(), "administrator") {
		t.Errorf("no administrator: %v", err)
	}
	pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE email IN ('stephen@old.example', 'second@old.example')`)

	// without Apply, only a report
	rep, err := platform.Adopt(ctx, cfg, pool, platform.AdoptOptions{Owner: "ed@old.example", MediaFrom: media})
	if err != nil || rep.Applied || rep.SiteName != "Old Clinic" || rep.PublicURL != "https://www.oldclinic.example" || rep.Tables < 20 {
		t.Fatalf("dry run: %+v %v", rep, err)
	}
	if len(rep.Admins) != 2 || len(rep.Skipped) != 1 || rep.Skipped[0] != "skipme@old.example" {
		t.Errorf("who becomes what: %+v", rep)
	}
	if e.scalar(`SELECT to_regclass('public.pages') IS NOT NULL`) != "true" || e.scalar(`SELECT to_regclass('site_1.pages') IS NULL`) != "true" {
		t.Fatal("a dry run changed the database")
	}
	ownerID := e.scalar(`SELECT id FROM users WHERE email = 'ed@old.example'`)
	secondID := e.scalar(`SELECT id FROM users WHERE email = 'second@old.example'`)

	rep, err = platform.Adopt(ctx, cfg, pool, platform.AdoptOptions{Owner: "ed@old.example", MediaFrom: media, Apply: true})
	if err != nil || !rep.Applied || rep.MediaFiles != 1 {
		t.Fatalf("adopt: %+v %v", rep, err)
	}

	// the website's tables, history and change counter moved together
	for q, want := range map[string]string{
		`SELECT count(*) FROM site_1.pages WHERE slug = 'about-old'`:                                             "1",
		`SELECT count(*) FROM site_1.subscribers`:                                                                 "1",
		`SELECT to_regclass('public.pages') IS NULL`:                                                              "true",
		`SELECT to_regclass('site_1.goose_db_version') IS NOT NULL`:                                               "true",
		`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'site_1' AND p.proname = 'bump_change_counter'`: "1",
		`SELECT count(*) FROM site_1.sessions`:                                                                    "0",
		`SELECT count(*) FROM site_1.users WHERE password_hash <> '!'`:                                            "0",
		`SELECT count(*) FROM site_1.media WHERE uploaded_by IS NOT NULL`:                                         "1",
	} {
		if got := e.scalar(q); got != want {
			t.Errorf("%s = %s, want %s", q, got, want)
		}
	}
	// accounts keep their ids; the editor has none; the owner has the website
	if e.scalar(`SELECT id FROM users WHERE email = 'ed@old.example'`) != ownerID || e.scalar(`SELECT id FROM users WHERE email = 'second@old.example'`) != secondID {
		t.Error("accounts keep their ids")
	}
	if e.scalar(`SELECT count(*) FROM users WHERE email = 'skipme@old.example'`) != "0" {
		t.Error("an editor who is not the owner gets no account")
	}
	if got := e.scalar(`SELECT owner_id || ' ' || name || ' ' || schema_name || ' ' || public_url FROM sites`); got != ownerID+" Old Clinic site_1 https://www.oldclinic.example" {
		t.Errorf("the website: %s", got)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "media", "1", "2026", "10", "aaaaaaaaaaaaaaaaaaaaaaaa.jpg")); err != nil {
		t.Errorf("the picture was not copied: %v", err)
	}
	// adopting twice is refused
	if _, err := platform.Adopt(ctx, cfg, pool, platform.AdoptOptions{Owner: "ed@old.example", Apply: true}); err == nil || !strings.Contains(err.Error(), "already runs the platform") {
		t.Errorf("a second adopt: %v", err)
	}

	// and the platform runs on it: people log in with the passwords they had, and the old website is there to edit
	p, err := platform.New(ctx, cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(p.Handler)
	t.Cleanup(func() { srv.Close(); p.Close() })
	e.p, e.srv = p, srv
	boss := e.client()
	if r := boss.login("second@old.example", oldPassword); r.Status != 303 {
		t.Fatalf("an administrator logs in with the old password: %d", r.Status)
	}
	if r := boss.get("/platform/sites"); !strings.Contains(r.Body, "Old Clinic") || !strings.Contains(r.Body, "ed@old.example") {
		t.Errorf("the platform lists the website: %d", r.Status)
	}
	boss.post("/platform/sites/1/act", nil)
	boss.refreshToken()
	if r := boss.get("/admin/pages"); r.Status != 200 || !strings.Contains(r.Body, "About the old site") {
		t.Errorf("the old pages are there to edit: %d", r.Status)
	}
	if r := boss.get("/admin/media"); !strings.Contains(r.Body, "old.jpg") {
		t.Error("the old pictures are in the library")
	}
	// the change counter still counts (its trigger moved with the tables)
	before := e.scalar(`SELECT n FROM site_1.change_counter`)
	owner := e.client()
	if r := owner.login("ed@old.example", oldPassword); r.Status != 303 {
		t.Fatalf("the owner logs in with the old password: %d", r.Status)
	}
	if r := owner.post("/admin/pages", url.Values{"title": {"Written after the move"}}); r.Status != 303 {
		t.Fatalf("a new page: %d", r.Status)
	}
	if re := regexp.MustCompile(`Written after the move`); !re.MatchString(owner.get("/admin/pages").Body) {
		t.Error("the new page is listed")
	}
	if after := e.scalar(`SELECT n FROM site_1.change_counter`); after == before {
		t.Error("the change counter must still count: its trigger moved with the tables")
	}
	if e.scalar(`SELECT count(*) FROM site_1.pages WHERE title = 'Written after the move'`) != "1" {
		t.Error("the new page is in the website's own schema")
	}
}
