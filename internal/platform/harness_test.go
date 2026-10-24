package platform_test

// Integration tests: the real platform (accounts, websites, routing, every website's own handlers) against a real
// PostgreSQL database, driven over HTTP like a browser. They need a database whose name ends in "_test"; each test wipes
// it. Without TEST_DATABASE_URL they are skipped (see internal/app/harness_test.go).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/config"
	"webbuilder/internal/db"
	"webbuilder/internal/platform"
)

const testPassword = "correct-horse-battery"

var (
	reCSRFInput = regexp.MustCompile(`name="csrf" value="([^"]*)"`)
	reCSRFMeta  = regexp.MustCompile(`name="csrf" content="([^"]*)"`)
	reLink      = regexp.MustCompile(`<code>(http[^<]*(/reset/[A-Za-z0-9_-]+))</code>`)
)

type env struct {
	t    *testing.T
	p    *platform.Platform
	pool *pgxpool.Pool
	srv  *httptest.Server
	cfg  config.Config
}

type opts func(*config.Config)

func newEnv(t *testing.T, mods ...opts) *env {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL (a database whose name ends in _test) to run the platform integration tests")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") {
		t.Fatalf("TEST_DATABASE_URL must name a database ending in _test, got %q", u.Path)
	}
	// the platform tests use a database of their own, so they can run at the same time as the single-site tests,
	// which wipe theirs on every test
	name := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "_test") + "_platform_test"
	if !regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(name) {
		t.Fatalf("odd database name %q", name)
	}
	u.Path = "/" + name
	dsn = u.String()
	ensureDB(t, dsn, name)
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// wipe: every website schema, then the platform's own
	if _, err := pool.Exec(ctx, `DO $$ DECLARE r record; BEGIN
		FOR r IN SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'site\_%' LOOP
			EXECUTE 'DROP SCHEMA ' || quote_ident(r.schema_name) || ' CASCADE';
		END LOOP; END $$; DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DatabaseURL: dsn, DataDir: t.TempDir(), MaxRuntimes: 40, BackupKeep: 0}
	for _, m := range mods {
		m(&cfg)
	}
	p, err := platform.New(ctx, cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(p.Handler)
	e := &env{t: t, p: p, pool: pool, srv: srv, cfg: cfg}
	t.Cleanup(func() {
		srv.Close()
		p.Close()
		pool.Close()
	})
	return e
}

// scalar runs a query that returns one value and returns it as text.
// ensureDB creates the platform test database if it is not there.
func ensureDB(t *testing.T, dsn, name string) {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = "postgres"
	conn, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var exists bool
	if err := conn.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if _, err := conn.Exec(context.Background(), `CREATE DATABASE "`+name+`"`); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) scalar(q string, args ...any) string {
	e.t.Helper()
	var out any
	if err := e.pool.QueryRow(context.Background(), q, args...).Scan(&out); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	if out == nil {
		return ""
	}
	if b, ok := out.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(out)
}

type client struct {
	t     *testing.T
	base  string
	hc    *http.Client
	token string
}

type response struct {
	Status int
	Header http.Header
	Body   string
}

func (r response) Location() string { return r.Header.Get("Location") }

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: e.t, base: e.srv.URL, hc: &http.Client{
		Jar: jar, Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *client) do(req *http.Request, hdr []string) response {
	c.t.Helper()
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return response{resp.StatusCode, resp.Header, string(b)}
}

func (c *client) get(path string, hdr ...string) response {
	c.t.Helper()
	req, _ := http.NewRequest("GET", c.base+path, nil)
	return c.do(req, hdr)
}

// post sends a form. The session's CSRF token is added unless the form already has one.
func (c *client) post(path string, v url.Values, hdr ...string) response {
	c.t.Helper()
	if v == nil {
		v = url.Values{}
	}
	if v.Get("csrf") == "" && c.token != "" {
		v.Set("csrf", c.token)
	}
	req, _ := http.NewRequest("POST", c.base+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.token != "" {
		req.Header.Set("X-CSRF-Token", c.token)
	}
	return c.do(req, hdr)
}

func (c *client) postNoToken(path string, v url.Values) response {
	c.t.Helper()
	req, _ := http.NewRequest("POST", c.base+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req, nil)
}

func scrape(re *regexp.Regexp, body string) string {
	if m := re.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

// formToken reads the pre-login token a form page carries.
func (c *client) formToken(path string) string {
	c.t.Helper()
	r := c.get(path)
	tok := scrape(reCSRFInput, r.Body)
	if tok == "" {
		c.t.Fatalf("no csrf field on %s (status %d)", path, r.Status)
	}
	return tok
}

// refreshToken reads the session token from a logged-in page.
func (c *client) refreshToken() {
	c.t.Helper()
	if !c.tryRefreshToken() {
		c.t.Fatal("no session token found")
	}
}

// tryRefreshToken is refreshToken that reports instead of failing (a suspended website has no pages to read it from).
func (c *client) tryRefreshToken() bool {
	c.t.Helper()
	for _, p := range []string{"/platform/sites", "/admin/", "/admin/setup"} { // a new customer's first page is the set-up wizard
		if tok := scrape(reCSRFMeta, c.get(p).Body); tok != "" {
			c.token = tok
			return true
		}
	}
	return false
}

func (c *client) login(email, password string) response {
	c.t.Helper()
	tok := c.formToken("/admin/login")
	r := c.post("/admin/login", url.Values{"csrf": {tok}, "email": {email}, "password": {password}})
	if r.Status == http.StatusSeeOther {
		c.token = ""
		c.tryRefreshToken()
	}
	return r
}

// setupAdmin creates the first platform admin through the real /setup page.
func (e *env) setupAdmin(email string) *client {
	e.t.Helper()
	c := e.client()
	tok := c.formToken("/setup")
	r := c.post("/setup", url.Values{"csrf": {tok}, "email": {email}, "password": {testPassword}, "confirm": {testPassword}})
	if r.Status != http.StatusSeeOther {
		e.t.Fatalf("setup: %d %s", r.Status, firstLine(r.Body))
	}
	c.refreshToken()
	return c
}

// createCustomer has the admin make a customer and website, and returns the one-time link path.
func (e *env) createCustomer(admin *client, email, name string) string {
	e.t.Helper()
	r := admin.post("/platform/sites", url.Values{"email": {email}, "name": {name}})
	m := reLink.FindStringSubmatch(r.Body)
	if r.Status != 200 || m == nil {
		e.t.Fatalf("create customer %s: %d %s", email, r.Status, firstLine(r.Body))
	}
	return m[2]
}

// customer creates a customer and returns a client logged in as them (they follow the link and choose a password).
func (e *env) customer(admin *client, email, name string) *client {
	e.t.Helper()
	path := e.createCustomer(admin, email, name)
	c := e.client()
	tok := c.formToken(path)
	r := c.post(path, url.Values{"csrf": {tok}, "password": {testPassword}, "confirm": {testPassword}})
	if r.Status != http.StatusSeeOther {
		e.t.Fatalf("choose password: %d %s", r.Status, firstLine(r.Body))
	}
	e.markSetUp()
	c.refreshToken()
	return c
}

// markSetUp marks every website as having been through the set-up wizard, so tests that are not about it open on the dashboard.
// The wizard's own tests make their customers with createCustomer and walk through it.
func (e *env) markSetUp() {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT schema_name FROM sites`)
	if err != nil {
		e.t.Fatal(err)
	}
	var schemas []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		schemas = append(schemas, s)
	}
	rows.Close()
	for _, s := range schemas {
		if _, err := e.pool.Exec(e.t.Context(), `UPDATE "`+s+`".site_settings SET wizard = '{"done": true}' WHERE wizard = '{}'`); err != nil {
			e.t.Fatal(err)
		}
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i > 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
