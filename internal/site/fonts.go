package site

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var fontFileRe = regexp.MustCompile(`^([0-9]{1,15})-([0-9a-f]{10})\.woff2$`)

// fontFile serves an uploaded font. Its address carries the start of the file's hash, so the file can be cached
// for good: a replaced font gets a new address. A wrong hash is a 404, so addresses cannot be guessed from ids.
func (s *Service) fontFile(w http.ResponseWriter, r *http.Request) {
	m := fontFileRe.FindStringSubmatch(r.PathValue("file"))
	if m == nil {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	data, hash, err := s.content.FontData(r.Context(), id)
	if err != nil || !strings.HasPrefix(hash, m[2]) {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "font/woff2")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		w.Write(data)
	}
}
