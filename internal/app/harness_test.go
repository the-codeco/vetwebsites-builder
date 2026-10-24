package app_test

// Integration tests: the real application (every handler, middleware and store)
// against a real PostgreSQL database, driven over HTTP exactly like a browser.
//
// They need a database whose name ends in "_test" (each test wipes it):
//
//	docker compose up -d db
//	TEST_DATABASE_URL="postgres://webbuilder:webbuilder@127.0.0.1:5433/webbuilder_test?sslmode=disable" go test ./...
//
// Without TEST_DATABASE_URL they are skipped.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/app"
	"webbuilder/internal/config"
	"webbuilder/internal/db"
)

const testPassword = "correct-horse-battery"

var (
	reDBName    = regexp.MustCompile(`^[a-z0-9_]+$`)
	reCSRFInput = regexp.MustCompile(`name="csrf" value="([^"]*)"`)
	reCSRFMeta  = regexp.MustCompile(`name="csrf" content="([^"]*)"`)
)

type testEnv struct {
	t        *testing.T
	app      *app.App
	pool     *pgxpool.Pool
	srv      *httptest.Server
	cfg      config.Config
	mediaDir string
}

// newEnv wipes the test database, migrates it and starts the app on a test server.
func newEnv(t *testing.T, mods ...func(*config.Config)) *testEnv {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL (a database whose name ends in _test) to run the integration tests")
	}
	ensureTestDB(t, dsn)

	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(pool); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{DatabaseURL: dsn, MediaDir: t.TempDir()}
	for _, m := range mods {
		m(&cfg)
	}
	a, err := app.New(ctx, cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(func() { srv.Close(); a.Close(); pool.Close() })
	return &testEnv{t: t, app: a, pool: pool, srv: srv, cfg: cfg, mediaDir: cfg.MediaDir}
}

// ensureTestDB refuses to touch any database not named *_test, and creates it if missing.
func ensureTestDB(t *testing.T, dsn string) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := cfg.ConnConfig.Database
	if !strings.HasSuffix(name, "_test") || !reDBName.MatchString(name) {
		t.Fatalf("TEST_DATABASE_URL must point at a database named *_test (it is wiped on every test), got %q", name)
	}
	admin := cfg.ConnConfig.Copy()
	admin.Database = "postgres"
	conn, err := pgx.ConnectConfig(context.Background(), admin)
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

// --- a browser-like client ---

type response struct {
	Status int
	Header http.Header
	Body   string
}

func (r response) Location() string { return r.Header.Get("Location") }

type client struct {
	t     *testing.T
	base  string
	hc    *http.Client
	token string // session CSRF token, set after login
}

func (e *testEnv) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{
		t: e.t, base: e.srv.URL,
		hc: &http.Client{
			Jar: jar, Timeout: 60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *client) do(req *http.Request, hdr []string) response {
	c.t.Helper()
	for i := 0; i+1 < len(hdr); i += 2 {
		if strings.EqualFold(hdr[i], "Host") {
			req.Host = hdr[i+1]
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return response{Status: resp.StatusCode, Header: resp.Header, Body: string(b)}
}

// get fetches path; hdr is optional key, value, key, value… (use "Host" to set the Host header).
func (c *client) get(path string, hdr ...string) response {
	c.t.Helper()
	req, _ := http.NewRequest("GET", c.base+path, nil)
	return c.do(req, hdr)
}

// post sends a form. The session CSRF token is added unless v already has "csrf".
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
	return c.do(req, hdr)
}

// postNoToken sends a form with no CSRF token at all.
func (c *client) postNoToken(path string, v url.Values, hdr ...string) response {
	c.t.Helper()
	req, _ := http.NewRequest("POST", c.base+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req, hdr)
}

type upFile struct {
	Name, ContentType string
	Data              []byte
}

// upload sends multipart files the way the media library's JavaScript does.
func (c *client) upload(files ...upFile) response {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename=%q`, f.Name))
		h.Set("Content-Type", f.ContentType)
		part, _ := mw.CreatePart(h)
		part.Write(f.Data)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+"/admin/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(req, []string{"X-CSRF-Token", c.token, "Accept", "application/json"})
}

func scrape(re *regexp.Regexp, body string) string {
	m := re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// formToken reads the pre-login CSRF token from a page's hidden field.
func (c *client) formToken(path string) string {
	c.t.Helper()
	r := c.get(path)
	tok := scrape(reCSRFInput, r.Body)
	if tok == "" {
		c.t.Fatalf("no csrf field on %s (status %d)", path, r.Status)
	}
	return tok
}

// cookie returns the named cookie's current value from the client's jar.
func (c *client) cookie(name string) string {
	u, _ := url.Parse(c.base)
	for _, ck := range c.hc.Jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// refreshToken loads the session's CSRF token from any admin page.
func (c *client) refreshToken() {
	c.t.Helper()
	r := c.get("/admin/account")
	c.token = scrape(reCSRFMeta, r.Body)
	if c.token == "" {
		c.t.Fatalf("not logged in? /admin/account gave %d", r.Status)
	}
}

// --- accounts ---

func (c *client) login(email, password string) response {
	c.t.Helper()
	tok := c.formToken("/admin/login")
	r := c.post("/admin/login", url.Values{"csrf": {tok}, "email": {email}, "password": {password}})
	if r.Status == http.StatusSeeOther {
		c.refreshToken()
	}
	return r
}

// newAdmin creates the first admin through the real /setup page and returns a logged-in client.
func (e *testEnv) newAdmin(email string) *client {
	e.t.Helper()
	c := e.client()
	tok := c.formToken("/setup")
	r := c.post("/setup", url.Values{"csrf": {tok}, "email": {email}, "password": {testPassword}, "confirm": {testPassword}})
	if r.Status != http.StatusSeeOther {
		e.t.Fatalf("setup failed: %d %s", r.Status, firstLine(r.Body))
	}
	c.refreshToken()
	return c
}

// invite has admin create an invite and returns its path.
func (e *testEnv) invite(admin *client, email, role string) string {
	e.t.Helper()
	r := admin.post("/admin/users/invite", url.Values{"email": {email}, "role": {role}})
	m := regexp.MustCompile(`<code>(http[^<]*/invite/[^<]*)</code>`).FindStringSubmatch(r.Body)
	if r.Status != 200 || m == nil {
		e.t.Fatalf("invite failed: %d", r.Status)
	}
	u, err := url.Parse(m[1])
	if err != nil {
		e.t.Fatal(err)
	}
	return u.Path
}

// newUser invites a user as admin, accepts the invite in a fresh client and returns it logged in.
func (e *testEnv) newUser(admin *client, email, role string) *client {
	e.t.Helper()
	path := e.invite(admin, email, role)
	c := e.client()
	tok := c.formToken(path)
	r := c.post(path, url.Values{"csrf": {tok}, "password": {testPassword}, "confirm": {testPassword}})
	if r.Status != http.StatusSeeOther {
		e.t.Fatalf("accepting invite failed: %d", r.Status)
	}
	c.refreshToken()
	return c
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// idFromLocation extracts the trailing number of a redirect like /admin/pages/7.
func idFromLocation(t *testing.T, loc string) string {
	t.Helper()
	m := regexp.MustCompile(`/(\d+)(?:\?.*)?$`).FindStringSubmatch(loc)
	if m == nil {
		t.Fatalf("no id in redirect %q", loc)
	}
	return m[1]
}

func (e *testEnv) sql(query string, args ...any) string {
	e.t.Helper()
	var out any
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&out); err != nil {
		e.t.Fatalf("%s: %v", query, err)
	}
	return fmt.Sprint(out)
}

var (
	reScriptTag = regexp.MustCompile(`(?i)<script[^>]*>`)
	// structured data is a JSON block the browser never runs; seo_test.go checks what goes inside it
	reLDJSON = regexp.MustCompile(`(?s)<script type="application/ld\+json">.*?</script>`)
)

// withoutStructuredData removes the JSON-LD blocks, which are data rather than code.
func withoutStructuredData(body string) string { return reLDJSON.ReplaceAllString(body, "") }

// foreignScripts returns every <script> tag on a public page other than the site's own
// menu script. Anything else on a page would be an injection that got through.
func foreignScripts(body string) []string {
	var out []string
	for _, tag := range reScriptTag.FindAllString(withoutStructuredData(body), -1) {
		// the site's own two scripts: the menu, and the dark-mode switch that runs in <head>
		if !strings.Contains(tag, `src="/theme/default/assets/menu.js?v=`) && !strings.Contains(tag, `src="/theme/default/assets/colormode.js?v=`) && !strings.Contains(tag, `src="/theme/default/assets/consent.js?v=`) && !strings.Contains(tag, `src="/theme/default/assets/elements.js?v=`) {
			out = append(out, tag)
		}
	}
	return out
}
