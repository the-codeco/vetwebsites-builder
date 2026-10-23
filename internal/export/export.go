// Package export turns a website into plain files: the HTML of every public page, the stylesheets, scripts, pictures and
// fonts they use, a sitemap and feed, a search index, and the web server settings (.htaccess) that make the files behave
// like the live site. The files can be put on any ordinary web hosting.
//
// It does not re-implement the website: it asks the website's own handler for every page, as a visitor's browser would,
// and saves the answers. So a page looks exactly as it does on the live site, drafts are never included (nobody is logged
// in), and anything the site learns to draw is exported for free.
package export

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	xhtml "golang.org/x/net/html"

	"webbuilder/internal/rewrite"
)

//go:embed assets/forms.js
var formsJS []byte

//go:embed assets/search.js
var searchJS []byte

// Limits that keep one export from using up the server.
const (
	MaxFiles      = 20000
	MaxPages      = 5000
	MaxTotalBytes = 1 << 30
	maxIndexText  = 3000 // characters of each page in the search index
	perPage       = 10   // posts per page of the blog (the live site's page size)
)

// Entry is one page or post in the search index.
type Entry struct{ Path, Title, Kind, Text string }

// Redirect is an old address that should lead somewhere else.
type Redirect struct {
	From, To  string
	Permanent bool
}

// Source is the website being exported.
type Source interface {
	// Handler is the website's own public handler.
	Handler() http.Handler
	// Paths lists the address of every public page (drafts excluded).
	Paths(ctx context.Context, perPage int) ([]string, error)
	SearchIndex(ctx context.Context, maxText int) ([]Entry, error)
	Redirects(ctx context.Context) ([]Redirect, error)
	// Changes is the website's change counter at the moment of the export.
	Changes(ctx context.Context) (int64, error)
}

// Options say where the exported site will live.
type Options struct {
	// PublicURL is the address of the published site: https://www.example.com, or https://example.com/folder when it
	// goes in a folder of the hosting. It decides the web addresses written into the pages (canonical links, the
	// sitemap) and the folder prefix on every link.
	PublicURL string
	// BuilderURL and SiteToken are where the published site's forms and visit counter talk to (empty: not wired).
	BuilderURL, SiteToken string
}

// File is one file of the export.
type File struct {
	Path   string // relative to the export folder, with forward slashes
	Size   int64
	SHA256 string
}

// Result describes a finished export.
type Result struct {
	Files    []File
	Warnings []string // links that lead nowhere, pages that could not be made
	Changes  int64    // the website's change counter when it was exported
}

type builder struct {
	src  Source
	opt  Options
	dir  string
	host string // the public host, such as www.example.com
	tls  bool
	base string // the folder prefix, such as /folder (empty at the root)

	written  map[string]bool // file path -> done
	seen     map[string]bool // fetched address -> done
	queue    []string
	bytes    int64
	warnings []string
	csp      string // the site's own security policy, as its home page sent it
	redirs   []Redirect

	pageForms bool // the page being finished has a form wired to the builder
	anyForms  bool // some page does, so forms.js is written
}

// Build exports the website into dir (which must be empty or not exist).
func Build(ctx context.Context, src Source, opt Options, dir string) (*Result, error) {
	u, err := url.Parse(opt.PublicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return nil, errors.New("export: the public address must be like https://www.example.com or https://example.com/folder")
	}
	changes, err := src.Changes(ctx)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	b := &builder{
		src: src, opt: opt, dir: dir, host: u.Host, tls: u.Scheme == "https", base: strings.TrimRight(u.Path, "/"),
		written: map[string]bool{}, seen: map[string]bool{},
	}
	paths, err := src.Paths(ctx, perPage)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		b.enqueue(p)
	}
	for _, p := range []string{"/sitemap.xml", "/robots.txt", "/rss.xml", "/search"} {
		b.enqueue(p)
	}
	for len(b.queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next := b.queue[0]
		b.queue = b.queue[1:]
		if err := b.process(next); err != nil {
			return nil, err
		}
	}
	if err := b.notFoundPage(); err != nil {
		return nil, err
	}
	if err := b.searchFiles(ctx); err != nil {
		return nil, err
	}
	if b.anyForms {
		if err := b.writeFile("forms.js", formsJS); err != nil {
			return nil, err
		}
	}
	redirs, err := src.Redirects(ctx)
	if err != nil {
		return nil, err
	}
	b.redirs = append(b.redirs, redirs...)
	if err := b.writeFile(".htaccess", b.htaccess()); err != nil {
		return nil, err
	}
	res := &Result{Warnings: b.warnings, Changes: changes}
	if res.Files, err = Manifest(dir); err != nil {
		return nil, err
	}
	return res, nil
}

// --- fetching ---

// enqueue adds an address (a path with an optional query) to be fetched, once.
func (b *builder) enqueue(p string) {
	if b.seen[p] {
		return
	}
	b.seen[p] = true
	b.queue = append(b.queue, p)
}

// fetch asks the website's handler for an address, as a visitor's browser would.
func (b *builder) fetch(p string) *httptest.ResponseRecorder {
	scheme := "http"
	if b.tls {
		scheme = "https"
	}
	req := httptest.NewRequest(http.MethodGet, scheme+"://"+b.host+p, nil)
	req.RemoteAddr = "127.0.0.1:0"
	req.Header.Set("User-Agent", "wb-export")
	req.Header.Set("DNT", "1") // an export is not a visit
	if b.tls {
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	rec := httptest.NewRecorder()
	b.src.Handler().ServeHTTP(rec, req)
	return rec
}

var assetExt = map[string]bool{
	".css": true, ".js": true, ".json": true, ".xml": true, ".txt": true, ".ico": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".svg": true, ".avif": true, ".woff": true, ".woff2": true, ".ttf": true, ".otf": true,
	".pdf": true, ".mp4": true, ".webm": true, ".mp3": true, ".map": true, ".zip": true,
}

// neverFetch are addresses of the live site that are not part of a static copy: the editor, form targets, tokens.
var neverFetch = []string{"/admin", "/static/", "/contact", "/subscribe", "/unsubscribe", "/setup", "/invite", "/reset", "/healthz", "/f/"}

func skipAddress(p string) bool {
	for _, n := range neverFetch {
		if p == n || strings.HasPrefix(p, strings.TrimSuffix(n, "/")+"/") || strings.HasPrefix(p, n+"?") {
			return true
		}
	}
	return false
}

func (b *builder) process(addr string) error {
	pth, query, _ := strings.Cut(addr, "?")
	if skipAddress(pth) {
		return nil
	}
	rec := b.fetch(addr)
	status := rec.Code
	switch {
	case status >= 300 && status < 400:
		loc := rec.Header().Get("Location")
		if to, ok := b.localPath(loc); ok && !strings.Contains(to, "?") {
			b.redirs = append(b.redirs, Redirect{From: pth, To: to, Permanent: status == http.StatusMovedPermanently || status == http.StatusPermanentRedirect})
		} else if strings.HasPrefix(loc, "http") {
			b.redirs = append(b.redirs, Redirect{From: pth, To: loc, Permanent: status == http.StatusMovedPermanently})
		}
		return nil
	case status != http.StatusOK:
		b.warn("%s could not be saved (the live site answers %d)", addr, status)
		return nil
	}
	ct := strings.ToLower(rec.Header().Get("Content-Type"))
	body := rec.Body.Bytes()
	if pth == "/" && b.csp == "" {
		b.csp = rec.Header().Get("Content-Security-Policy")
	}
	switch {
	case strings.HasPrefix(ct, "text/html"):
		return b.savePage(addr, pth, body)
	case strings.HasPrefix(ct, "text/css"):
		b.discoverCSS(body)
		return b.saveAsset(pth, query, rewrite.CSSWith(body, b.mapURL))
	default:
		return b.saveAsset(pth, query, body)
	}
}

func (b *builder) warn(format string, a ...any) {
	if len(b.warnings) < 200 {
		b.warnings = append(b.warnings, fmt.Sprintf(format, a...))
	}
}

// localPath turns an address a page uses into a path on this site, if it is one: "/x?y" and "https://thishost/x" both are.
func (b *builder) localPath(u string) (string, bool) {
	u = strings.TrimSpace(u)
	switch {
	case strings.HasPrefix(u, "//"):
		return "", false
	case strings.HasPrefix(u, "/"):
		u, _, _ = strings.Cut(u, "#")
		return u, true
	case strings.HasPrefix(strings.ToLower(u), "http://") || strings.HasPrefix(strings.ToLower(u), "https://"):
		p, err := url.Parse(u)
		if err != nil || !strings.EqualFold(p.Host, b.host) {
			return "", false
		}
		out := p.EscapedPath()
		if out == "" {
			out = "/"
		}
		if p.RawQuery != "" {
			out += "?" + p.RawQuery
		}
		return out, true
	}
	return "", false
}

// --- discovering what a page uses ---

func (b *builder) follow(u string) {
	p, ok := b.localPath(u)
	if !ok || p == "" || skipAddress(strings.SplitN(p, "?", 2)[0]) {
		return
	}
	b.enqueue(p)
}

// discoverHTML queues every address a page links to or loads.
func (b *builder) discoverHTML(src []byte) {
	collect := rewrite.URLs(func(u string) string {
		b.follow(u)
		return u
	})
	rewrite.Tags(src, func(tag string, attrs []xhtml.Attribute) ([]xhtml.Attribute, bool) {
		collect(tag, attrs)
		if tag == "meta" { // sharing pictures are written as full addresses
			for _, a := range attrs {
				if a.Key == "content" && (strings.HasPrefix(a.Val, "https://") || strings.HasPrefix(a.Val, "http://")) {
					if p, ok := b.localPath(a.Val); ok && strings.HasPrefix(p, "/media/") {
						b.enqueue(p)
					}
				}
			}
		}
		return attrs, false
	}, func(css []byte) []byte {
		b.discoverCSS(css)
		return css
	})
}

func (b *builder) discoverCSS(css []byte) {
	rewrite.CSSWith(css, func(u string) string {
		b.follow(u)
		return u
	})
}

// --- writing ---

var variantRe = regexp.MustCompile(`^/media/([^?#]+)\?w=([0-9]{2,5})$`)

// mapURL is what every address in a saved page or stylesheet goes through: a resized picture (/media/a.jpg?w=480) is a
// file of its own (/media/w480/a.jpg), and everything gets the folder prefix when the site lives in a folder.
func (b *builder) mapURL(u string) string {
	if m := variantRe.FindStringSubmatch(u); m != nil {
		u = "/media/w" + m[2] + "/" + m[1]
	}
	return rewrite.URL(b.base, u)
}

// filePath is where an address is saved: "/" is index.html, a page "/about" is about.html (the .htaccess answers /about
// with it), and a file with an extension keeps its name. Query strings are not part of a file name, except for resized pictures.
func filePath(pth, query string) (string, bool) {
	clean := path.Clean("/" + strings.TrimPrefix(pth, "/"))
	if clean == "/" {
		return "index.html", true
	}
	if strings.Contains(clean, "..") || strings.ContainsAny(clean, "\\\x00:*?\"<>|") {
		return "", false
	}
	if strings.HasPrefix(clean, "/media/") && strings.HasPrefix(query, "w=") && len(query) <= 8 {
		w := strings.TrimPrefix(query, "w=")
		name := strings.TrimPrefix(clean, "/media/")
		if regexp.MustCompile(`^[0-9]{2,5}$`).MatchString(w) {
			return "media/w" + w + "/" + name, true
		}
	}
	p := strings.TrimPrefix(clean, "/")
	if assetExt[strings.ToLower(path.Ext(p))] {
		return p, true
	}
	return p + ".html", true
}

func (b *builder) writeFile(rel string, data []byte) error {
	if b.written[rel] {
		return nil
	}
	if len(b.written) >= MaxFiles {
		return fmt.Errorf("export: more than %d files", MaxFiles)
	}
	if b.bytes += int64(len(data)); b.bytes > MaxTotalBytes {
		return errors.New("export: the site is larger than the export limit")
	}
	full := filepath.Join(b.dir, filepath.FromSlash(rel))
	if rel2, err := filepath.Rel(b.dir, full); err != nil || strings.HasPrefix(rel2, "..") {
		return fmt.Errorf("export: refusing to write outside the export folder: %s", rel)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return err
	}
	b.written[rel] = true
	return nil
}

func (b *builder) saveAsset(pth, query string, data []byte) error {
	rel, ok := filePath(pth, query)
	if !ok {
		b.warn("%s was not saved: its address cannot be a file name", pth)
		return nil
	}
	return b.writeFile(rel, data)
}

func (b *builder) savePage(addr, pth string, body []byte) error {
	if len(b.written) >= MaxPages+MaxFiles {
		return nil
	}
	b.discoverHTML(body)
	rel, ok := filePath(pth, "")
	if !ok {
		b.warn("%s was not saved: its address cannot be a file name", addr)
		return nil
	}
	return b.writeFile(rel, b.finishHTML(body, pth == "/search"))
}

// finishHTML makes a page ready to be a file: its addresses are mapped, the forms point at the builder, and the search
// page gets its script.
func (b *builder) finishHTML(src []byte, searchPage bool) []byte {
	urls := rewrite.URLs(b.mapURL)
	b.pageForms = false
	out := rewrite.Tags(src, func(tag string, attrs []xhtml.Attribute) ([]xhtml.Attribute, bool) {
		attrs, changed := urls(tag, attrs)
		attrs, wired := b.wireForms(tag, attrs)
		return attrs, changed || wired
	}, func(css []byte) []byte { return rewrite.CSSWith(css, b.mapURL) })
	if searchPage {
		out = addScript(out, b.base+"/search.js")
	}
	if b.pageForms {
		out = addScript(out, b.base+"/forms.js")
		b.anyForms = true
	}
	return out
}

// addScript puts a deferred script at the end of a page.
func addScript(page []byte, src string) []byte {
	tag := `<script src="` + src + `" defer></script>`
	if i := bytes.LastIndex(page, []byte("</body>")); i >= 0 {
		return append(page[:i], append([]byte(tag+"\n"), page[i:]...)...)
	}
	return append(page, []byte(tag)...)
}

// wireForms points the contact and newsletter forms at the builder server, which stores and sends what visitors submit.
// The single-use token a form carries is only good for a short while, so none is saved in the page: the page asks the
// builder for a fresh one when it is opened.
func (b *builder) wireForms(tag string, attrs []xhtml.Attribute) ([]xhtml.Attribute, bool) {
	if b.opt.BuilderURL == "" || b.opt.SiteToken == "" {
		return attrs, false
	}
	if tag == "input" { // the form's single-use token (named "t"): not saved in the page, fetched when it is opened
		hidden, isToken, at := false, false, -1
		for i, a := range attrs {
			switch {
			case a.Key == "type" && strings.EqualFold(a.Val, "hidden"):
				hidden = true
			case a.Key == "name" && a.Val == "t":
				isToken = true
			case a.Key == "value":
				at = i
			}
		}
		if hidden && isToken && at >= 0 && attrs[at].Val != "" {
			attrs[at].Val = ""
			return attrs, true
		}
		return attrs, false
	}
	if tag != "form" {
		return attrs, false
	}
	for i, a := range attrs {
		if a.Key != "action" {
			continue
		}
		var kind string
		switch strings.TrimPrefix(a.Val, b.base) {
		case "/contact":
			kind = "contact"
		case "/subscribe":
			kind = "subscribe"
		case "/search":
			// the search form still goes to the search page of the copy; the script there asks the builder
			return append(attrs, xhtml.Attribute{Key: "data-wb-search", Val: strings.TrimRight(b.opt.BuilderURL, "/") + "/f/" + b.opt.SiteToken + "/search"}), true
		default:
			return attrs, false
		}
		attrs[i].Val = strings.TrimRight(b.opt.BuilderURL, "/") + "/f/" + b.opt.SiteToken + "/" + kind
		b.pageForms = true
		return append(attrs, xhtml.Attribute{Key: "data-wb-form", Val: kind}), true
	}
	return attrs, false
}

// notFoundPage saves the website's own "page not found" page as 404.html.
func (b *builder) notFoundPage() error {
	rec := b.fetch("/__not_found__")
	if rec.Code != http.StatusNotFound || !strings.HasPrefix(strings.ToLower(rec.Header().Get("Content-Type")), "text/html") {
		b.warn("the live site's \"page not found\" page could not be saved")
		return nil
	}
	b.discoverHTML(rec.Body.Bytes())
	for len(b.queue) > 0 { // anything only that page uses
		next := b.queue[0]
		b.queue = b.queue[1:]
		if err := b.process(next); err != nil {
			return err
		}
	}
	return b.writeFile("404.html", b.finishHTML(rec.Body.Bytes(), false))
}

type indexRow struct {
	P string `json:"p"`
	T string `json:"t"`
	K string `json:"k"`
	X string `json:"x"`
}

// searchFiles writes the search index and the script that uses it.
func (b *builder) searchFiles(ctx context.Context) error {
	entries, err := b.src.SearchIndex(ctx, maxIndexText)
	if err != nil {
		return err
	}
	rows := make([]indexRow, len(entries))
	for i, e := range entries {
		rows[i] = indexRow{P: rewrite.URL(b.base, e.Path), T: e.Title, K: e.Kind, X: e.Text}
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	if err := b.writeFile("search-index.json", data); err != nil {
		return err
	}
	return b.writeFile("search.js", searchJS)
}

// Manifest lists every file under dir with its size and checksum, in path order.
func Manifest(dir string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		sum := sha256.Sum256(data)
		files = append(files, File{Path: filepath.ToSlash(rel), Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}
