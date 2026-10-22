package rewrite

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestURL(t *testing.T) {
	const p = "/preview/live"
	for in, want := range map[string]string{
		"/":                       "/preview/live/",
		"/about":                  "/preview/live/about",
		"/media/a.jpg?w=480":      "/preview/live/media/a.jpg?w=480",
		"/a#top":                  "/preview/live/a#top",
		"/preview/live/about":     "/preview/live/about", // never twice
		"/preview/live":           "/preview/live",
		"//evil.example/x":        "//evil.example/x",
		"https://example.com/x":   "https://example.com/x",
		"http://example.com/":     "http://example.com/",
		"#top":                    "#top",
		"mailto:a@b.com":          "mailto:a@b.com",
		"tel:+27215550123":        "tel:+27215550123",
		"relative/path":           "relative/path",
		"":                        "",
		"/preview/liveish":        "/preview/preview/liveish",
		"data:image/png;base64,x": "data:image/png;base64,x",
		"javascript:alert(1)":     "javascript:alert(1)",
	} {
		if got := URL(p, in); got != want && !(in == "/preview/liveish") {
			t.Errorf("URL(%q) = %q, want %q", in, got, want)
		}
	}
	if URL("", "/x") != "/x" {
		t.Error("no prefix, no change")
	}
	if got := URL(p, "/preview/liveish"); got != "/preview/live/preview/liveish" {
		t.Errorf("a path that only starts like the prefix still gets it: %q", got)
	}
}

func TestHTML(t *testing.T) {
	const p = "/pre"
	in := `<!doctype html><html><head><link rel="stylesheet" href="/theme/default/theme.css?v=1"><link rel="canonical" href="https://example.com/a">
<style>body{background:url(/media/bg.jpg)} @import "/x.css"; .a{background:url('/m/a.png')}</style>
<script src="/theme/default/assets/menu.js" defer></script><script>var u="/not/rewritten"; fetch("/api")</script></head>
<body style="background:url(/media/b.jpg)"><a href="/about" class="x">About</a> <a href="#top">top</a> <a href="mailto:a@b.c">m</a> <a href="//cdn.example/x">cdn</a>
<img src="/media/a.jpg" srcset="/media/a-480.jpg 480w, /media/a-960.jpg 960w" alt="a &amp; b &lt;c&gt;">
<form action="/contact" method="post"><input name="x" value="/keep" disabled><button formaction="/other">go</button></form>
<p>text with /slashes and "quotes" & ampersands</p><!-- a comment with href="/no" --></body></html>`
	got := string(HTML(p, []byte(in)))
	for _, want := range []string{
		`href="/pre/theme/default/theme.css?v=1"`, `href="https://example.com/a"`, `background:url(/pre/media/bg.jpg)`, `@import "/pre/x.css"`, `url('/pre/m/a.png')`,
		`src="/pre/theme/default/assets/menu.js"`, `var u="/not/rewritten"; fetch("/api")`, `style="background:url(/pre/media/b.jpg)"`,
		`href="/pre/about"`, `href="#top"`, `href="mailto:a@b.c"`, `href="//cdn.example/x"`, `src="/pre/media/a.jpg"`,
		`srcset="/pre/media/a-480.jpg 480w, /pre/media/a-960.jpg 960w"`, `alt="a &amp; b &lt;c&gt;"`, `action="/pre/contact"`, `formaction="/pre/other"`,
		`value="/keep"`, `<p>text with /slashes and "quotes" & ampersands</p>`, `<!-- a comment with href="/no" -->`, `<!doctype html>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\n%s", want, got)
		}
	}
	if got := string(HTML("", []byte(in))); got != in {
		t.Error("no prefix leaves the page as it was")
	}
	// a page with no addresses to change comes back byte for byte
	plain := "<!doctype html><html><body><p>Hello &amp; welcome</p><a href=\"#x\">x</a></body></html>\n"
	if got := string(HTML(p, []byte(plain))); got != plain {
		t.Errorf("unchanged page altered: %q", got)
	}
	// broken markup does not lose text
	if got := string(HTML(p, []byte(`<a href="/x">one<p>two`))); got != `<a href="/pre/x">one<p>two` {
		t.Errorf("broken markup: %q", got)
	}
}

func TestCSS(t *testing.T) {
	css := `@import "/a.css"; @import '/b.css'; @import url(/c.css);
.x { background: url( /media/x.png ); } .y { background: url("/media/y.png") } .z{background:url('/media/z.png')}
.w { background: url(https://example.com/w.png); } .v { background: url(data:image/png;base64,AAA=); } .u { background: url(//cdn/u.png) }
@font-face { src: url("/fonts/1-abcdef0123.woff2") format("woff2"); }`
	got := string(CSS("/p", []byte(css)))
	for _, want := range []string{
		`@import "/p/a.css"`, `@import '/p/b.css'`, `url(/p/c.css)`, `url(/p/media/x.png)`, `url("/p/media/y.png")`, `url('/p/media/z.png')`,
		`url(https://example.com/w.png)`, `url(data:image/png;base64,AAA=)`, `url(//cdn/u.png)`, `url("/p/fonts/1-abcdef0123.woff2")`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\n%s", want, got)
		}
	}
}

func TestBase(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", "999")
		w.Write([]byte(`<a href="/x">x</a>`))
	})
	mux.HandleFunc("/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Write([]byte(`a{background:url(/i.png)}`))
	})
	mux.HandleFunc("/image", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte(`<a href="/x">not html</a>`))
	})
	mux.HandleFunc("/script.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write([]byte(`var a = "/x"`))
	})
	mux.HandleFunc("/go", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/target", http.StatusFound) })
	mux.HandleFunc("/away", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/t", http.StatusFound)
	})
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(404)
		w.Write([]byte(`<a href="/home">home</a>`))
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(204)
	})
	h := Base("/pre", mux)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if r := get("/page"); r.Body.String() != `<a href="/pre/x">x</a>` || r.Header().Get("Content-Length") != "" {
		t.Errorf("html: %q %q", r.Body.String(), r.Header().Get("Content-Length"))
	}
	if r := get("/style.css"); r.Body.String() != `a{background:url(/pre/i.png)}` {
		t.Errorf("css: %q", r.Body.String())
	}
	if r := get("/image"); r.Body.String() != `<a href="/x">not html</a>` {
		t.Errorf("only html and css are rewritten: %q", r.Body.String())
	}
	if r := get("/script.js"); r.Body.String() != `var a = "/x"` {
		t.Errorf("scripts untouched: %q", r.Body.String())
	}
	if r := get("/go"); r.Code != 302 || r.Header().Get("Location") != "/pre/target" {
		t.Errorf("redirect: %d %q", r.Code, r.Header().Get("Location"))
	}
	if r := get("/away"); r.Header().Get("Location") != "https://example.com/t" {
		t.Errorf("redirect to another site: %q", r.Header().Get("Location"))
	}
	if r := get("/gone"); r.Code != 404 || r.Body.String() != `<a href="/pre/home">home</a>` {
		t.Errorf("an error page keeps its status and is rewritten: %d %q", r.Code, r.Body.String())
	}
	if r := get("/empty"); r.Code != 204 || r.Body.Len() != 0 {
		t.Errorf("no content: %d", r.Code)
	}
}
