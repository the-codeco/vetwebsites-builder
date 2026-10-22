// Package rewrite changes the addresses in a website's pages. A website writes every link as if it lived at the root of
// its own address ("/about", "/media/a.jpg"). To show it under a prefix (the live preview), or to save it as files for
// somebody else's hosting, the addresses are rewritten: only the ones that mean an address are touched (link and image
// attributes, srcset, CSS url() and @import); text, scripts and everything else come through byte for byte.
package rewrite

import (
	"bytes"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

// URL puts prefix on a root-relative address ("/x" becomes "/prefix/x"). Anything else comes back unchanged: full web
// addresses, "//host/x", "#anchors", "mailto:" and "tel:" links, relative paths, and addresses that already have it.
func URL(prefix, u string) string {
	if prefix == "" || !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") || u == prefix || strings.HasPrefix(u, prefix+"/") {
		return u
	}
	return prefix + u
}

var (
	cssURLRe    = regexp.MustCompile(`url\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s'"]*))\s*\)`)
	cssImportRe = regexp.MustCompile(`@import\s+(?:"([^"]*)"|'([^']*)')`)
)

// CSSWith passes every address in a stylesheet (url(...) and @import) through fn.
func CSSWith(css []byte, fn func(string) string) []byte {
	out := cssURLRe.ReplaceAllFunc(css, func(m []byte) []byte {
		sub := cssURLRe.FindSubmatch(m)
		switch {
		case bytes.Contains(sub[0], []byte(`"`)):
			return []byte(`url("` + fn(string(sub[1])) + `")`)
		case bytes.Contains(sub[0], []byte(`'`)):
			return []byte(`url('` + fn(string(sub[2])) + `')`)
		default:
			return []byte("url(" + fn(string(sub[3])) + ")")
		}
	})
	return cssImportRe.ReplaceAllFunc(out, func(m []byte) []byte {
		sub := cssImportRe.FindSubmatch(m)
		if bytes.Contains(sub[0], []byte(`"`)) {
			return []byte(`@import "` + fn(string(sub[1])) + `"`)
		}
		return []byte(`@import '` + fn(string(sub[2])) + `'`)
	})
}

// CSS rewrites the root-relative addresses in a stylesheet.
func CSS(prefix string, css []byte) []byte {
	if prefix == "" {
		return css
	}
	return CSSWith(css, func(u string) string { return URL(prefix, u) })
}

// attributes that hold one address
var urlAttrs = map[string]bool{"href": true, "src": true, "action": true, "poster": true, "formaction": true, "data-src": true, "data-href": true}

// SrcsetWith passes the address of every candidate in a srcset through fn, keeping the sizes.
func SrcsetWith(v string, fn func(string) string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		lead := p[:len(p)-len(strings.TrimLeft(p, " \t\r\n"))]
		fields := strings.Fields(p)
		if len(fields) == 0 {
			continue
		}
		fields[0] = fn(fields[0])
		parts[i] = lead + strings.Join(fields, " ")
	}
	return strings.Join(parts, ",")
}

// TagFunc looks at a start tag and may change its attributes (in place, or by returning a different list). It reports whether
// it changed any; when it did not, the tag is written exactly as it was.
type TagFunc func(tag string, attrs []xhtml.Attribute) ([]xhtml.Attribute, bool)

// URLs makes a TagFunc that passes every address attribute of a tag (and srcset, and CSS in a style attribute)
// through fn.
func URLs(fn func(string) string) TagFunc {
	return func(tag string, attrs []xhtml.Attribute) ([]xhtml.Attribute, bool) {
		changed := false
		for i, a := range attrs {
			var nv string
			switch {
			case urlAttrs[a.Key]:
				nv = fn(a.Val)
			case a.Key == "srcset":
				nv = SrcsetWith(a.Val, fn)
			case a.Key == "style":
				nv = string(CSSWith([]byte(a.Val), fn))
			default:
				continue
			}
			if nv != a.Val {
				attrs[i].Val = nv
				changed = true
			}
		}
		return attrs, changed
	}
}

// Tags walks an HTML page, offering every start tag to tags and the contents of every <style> element to css (either may
// be nil), and writes everything else back byte for byte. A tag that was not changed is written exactly as it was.
func Tags(src []byte, tags TagFunc, css func([]byte) []byte) []byte {
	z := xhtml.NewTokenizer(bytes.NewReader(src))
	var out bytes.Buffer
	out.Grow(len(src) + 256)
	inStyle := false
	for {
		tt := z.Next()
		raw := append([]byte(nil), z.Raw()...) // the tokenizer reuses its buffer
		switch tt {
		case xhtml.ErrorToken:
			if z.Err() != io.EOF {
				out.Write(raw)
			}
			return out.Bytes()
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data == "style" && tt == xhtml.StartTagToken {
				inStyle = true
			}
			attrs, changed := tok.Attr, false
			if tags != nil {
				attrs, changed = tags(tok.Data, tok.Attr)
			}
			if !changed {
				out.Write(raw)
				continue
			}
			out.WriteByte('<')
			out.WriteString(tok.Data)
			for _, a := range attrs {
				out.WriteByte(' ')
				out.WriteString(a.Key)
				out.WriteString(`="`)
				out.WriteString(html.EscapeString(a.Val))
				out.WriteByte('"')
			}
			if tt == xhtml.SelfClosingTagToken {
				out.WriteString(" /")
			}
			out.WriteByte('>')
		case xhtml.EndTagToken:
			if bytes.HasPrefix(bytes.ToLower(raw), []byte("</style")) {
				inStyle = false
			}
			out.Write(raw)
		case xhtml.TextToken:
			if inStyle && css != nil {
				out.Write(css(raw))
			} else {
				out.Write(raw)
			}
		default:
			out.Write(raw)
		}
	}
}

// HTMLWith passes every address in an HTML page through fn.
func HTMLWith(src []byte, fn func(string) string) []byte {
	return Tags(src, URLs(fn), func(c []byte) []byte { return CSSWith(c, fn) })
}

// HTML rewrites the root-relative addresses in an HTML page.
func HTML(prefix string, src []byte) []byte {
	if prefix == "" {
		return src
	}
	return HTMLWith(src, func(u string) string { return URL(prefix, u) })
}

// Base wraps a website's handler so everything it sends works under prefix: HTML and CSS bodies and redirect addresses
// are rewritten; every other kind of answer (images, scripts, downloads) passes through untouched and unbuffered.
func Base(prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &rewriter{ResponseWriter: w, prefix: prefix}
		next.ServeHTTP(rw, r)
		rw.finish()
	})
}

type rewriter struct {
	http.ResponseWriter
	prefix    string
	status    int
	wrote     bool
	buffering bool
	kind      string
	buf       bytes.Buffer
}

func (r *rewriter) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.wrote, r.status = true, status
	h := r.Header()
	if loc := h.Get("Location"); loc != "" {
		h.Set("Location", URL(r.prefix, loc))
	}
	ct := strings.ToLower(h.Get("Content-Type"))
	switch {
	case strings.HasPrefix(ct, "text/html"):
		r.kind = "html"
	case strings.HasPrefix(ct, "text/css"):
		r.kind = "css"
	}
	if r.kind != "" && status != http.StatusNotModified && status != http.StatusNoContent {
		r.buffering = true
		h.Del("Content-Length")
		return // sent in finish, once rewritten
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *rewriter) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	if r.buffering {
		return r.buf.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

func (r *rewriter) finish() {
	if !r.wrote || !r.buffering {
		return
	}
	body := r.buf.Bytes()
	switch r.kind {
	case "html":
		body = HTML(r.prefix, body)
	case "css":
		body = CSS(r.prefix, body)
	}
	r.ResponseWriter.WriteHeader(r.status)
	r.ResponseWriter.Write(body)
}

// Flush lets streamed answers through.
func (r *rewriter) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok && !r.buffering {
		f.Flush()
	}
}
