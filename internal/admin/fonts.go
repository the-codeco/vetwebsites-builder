package admin

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"webbuilder/internal/content"
	"webbuilder/internal/themes"
)

func (h *Handler) registerFonts(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /admin/appearance/fonts", admin(h.fontAdd))
	mux.Handle("POST /admin/appearance/fonts/{id}/delete", admin(h.fontDelete))
}

// fontView is one uploaded font in the list.
type fontView struct {
	content.Font
	Token  string
	Weight string
	KB     int
}

func fontViews(fonts []content.Font) []fontView {
	out := make([]fontView, len(fonts))
	for i, f := range fonts {
		w := "Regular"
		if f.Weight == 700 {
			w = "Bold"
		}
		out[i] = fontView{Font: f, Token: content.FontToken(f.Family), Weight: w, KB: (f.Size + 1023) / 1024}
	}
	return out
}

// uploadedFontOptions are the font choices the uploaded families add to a font setting.
func uploadedFontOptions(fonts []content.Font, current string) []fontOption {
	var out []fontOption
	seen := map[string]bool{}
	for _, f := range fonts {
		tok := content.FontToken(f.Family)
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, fontOption{tok, f.Family + " (uploaded)", `"` + f.Family + `", ` + themes.FontStacks["sans"], current == tok})
	}
	return out
}

// fontAdd takes an uploaded WOFF2 file. Only the file's signature and length are checked here (the browser does
// the real parsing, and a font file can't run code), and its name is limited to letters, digits, spaces and dashes.
func (h *Handler) fontAdd(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, content.MaxFontBytes+(64<<10))
	if err := r.ParseMultipartForm(content.MaxFontBytes + (64 << 10)); err != nil {
		h.renderAppearance(w, r, 400, "", fmt.Sprintf("The font file is too large: the limit is %d KB.", content.MaxFontBytes>>10), nil)
		return
	}
	family, ok := content.CleanFamily(r.PostFormValue("family"))
	if !ok {
		h.renderAppearance(w, r, 400, "", "Give the font a name of letters, numbers, spaces and dashes, such as Acme Sans.", nil)
		return
	}
	weight, _ := strconv.Atoi(r.PostFormValue("weight"))
	if weight != 400 && weight != 700 {
		h.renderAppearance(w, r, 400, "", "Choose Regular or Bold.", nil)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		h.renderAppearance(w, r, 400, "", "Choose a .woff2 font file to upload.", nil)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, content.MaxFontBytes+1))
	if err != nil {
		h.renderAppearance(w, r, 400, "", "The file could not be read.", nil)
		return
	}
	if msg := h.limitProblem(r.Context(), "storage", int64(len(data))); msg != "" {
		h.renderAppearance(w, r, 400, "", msg, nil)
		return
	}
	switch err = h.Content.AddFont(r.Context(), family, weight, data); {
	case errors.Is(err, content.ErrNotAFont):
		h.renderAppearance(w, r, 400, "", fmt.Sprintf("That is not a WOFF2 font file (or it is larger than %d KB). Most font sites offer a .woff2 download.", content.MaxFontBytes>>10), nil)
		return
	case errors.Is(err, content.ErrFontsFull):
		h.renderAppearance(w, r, 400, "", fmt.Sprintf("You can upload at most %d font files. Delete one you no longer use first.", content.MaxFonts), nil)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=font", http.StatusSeeOther)
}

func (h *Handler) fontDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch err := h.Content.DeleteFont(r.Context(), id); {
	case errors.Is(err, content.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=fontdeleted", http.StatusSeeOther)
}
