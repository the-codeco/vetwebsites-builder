package export

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
)

// Serve answers requests from an exported folder the way the generated .htaccess makes a web server answer them: "/" is
// index.html, "/about" is about.html, a trailing slash is taken off with a redirect, and an address with no file gets the
// site's own 404.html. base is the folder prefix the site was exported for ("" at the root); it is taken off each address.
// Files are opened through a confined root, so no address can reach outside the folder. Close it when done.
func Serve(dir, base string) (*Static, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Static{root: root, base: strings.TrimRight(base, "/")}, nil
}

// Static is a handler over an exported folder.
type Static struct {
	root *os.Root
	base string
}

// Close releases the folder.
func (s *Static) Close() error { return s.root.Close() }

func (s *Static) open(name string) (fs.File, fs.FileInfo, error) {
	f, err := s.root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		return nil, nil, fs.ErrNotExist
	}
	return f, st, nil
}

func (s *Static) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := r.URL.Path
	if s.base != "" {
		if p != s.base && !strings.HasPrefix(p, s.base+"/") {
			s.notFound(w, r)
			return
		}
		p = strings.TrimPrefix(p, s.base)
		if p == "" {
			p = "/"
		}
	}
	if p != "/" && strings.HasSuffix(p, "/") {
		http.Redirect(w, r, s.base+strings.TrimRight(p, "/"), http.StatusMovedPermanently)
		return
	}
	clean := path.Clean("/" + p)
	rel := strings.TrimPrefix(clean, "/")
	candidates := []string{rel + ".html"}
	if rel == "" {
		candidates = []string{"index.html"}
	} else {
		candidates = append([]string{rel}, candidates...)
	}
	for _, name := range candidates {
		if strings.HasPrefix(path.Base(name), ".") { // .htaccess and other dot files are never served
			continue
		}
		f, st, err := s.open(name)
		if err != nil {
			continue
		}
		defer f.Close()
		s.send(w, r, name, f, st, http.StatusOK)
		return
	}
	s.notFound(w, r)
}

func (s *Static) notFound(w http.ResponseWriter, r *http.Request) {
	f, st, err := s.open("404.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	s.send(w, r, "404.html", f, st, http.StatusNotFound)
}

func (s *Static) send(w http.ResponseWriter, r *http.Request, name string, f fs.File, st fs.FileInfo, status int) {
	ct := mime.TypeByExtension(path.Ext(name))
	switch path.Ext(name) {
	case ".html":
		ct = "text/html; charset=utf-8"
	case ".woff2":
		ct = "font/woff2"
	case ".json":
		ct = "application/json"
	case ".xml":
		ct = "application/xml; charset=utf-8"
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if status != http.StatusOK {
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			io.Copy(w, f)
		}
		return
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, st.ModTime(), rs)
		return
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		io.Copy(w, f)
	}
}
