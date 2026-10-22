package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"webbuilder/internal/auth"
	"webbuilder/internal/media"
)

const (
	mediaPageSize    = 48
	maxFilesPerBatch = 10
	maxUploadRequest = maxFilesPerBatch*media.MaxFileSize + 1<<20 // files plus form overhead
	maxAltLen        = 300
)

func (h *Handler) registerMedia(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/media", authed(h.mediaLibrary))
	mux.Handle("GET /admin/media/picker", authed(h.mediaPicker))
	// the body cap sits outside the CSRF check, which may parse the multipart form
	mux.Handle("POST /admin/media", limitBody(maxUploadRequest, authed(h.mediaUpload)))
	mux.Handle("POST /admin/media/{id}", authed(h.mediaAlt))
	mux.Handle("POST /admin/media/{id}/delete", authed(h.mediaDelete))
	mux.Handle("GET /admin/media/{id}/edit", authed(h.mediaEditPage))
	mux.Handle("POST /admin/media/{id}/edit", authed(h.mediaEdit))
}

func limitBody(n int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, n)
		next.ServeHTTP(w, r)
	})
}

type mediaData struct {
	Items       []media.Item
	Page, Pages int
	Total       int
}

func (h *Handler) mediaPageData(r *http.Request) (mediaData, error) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	items, total, err := h.Media.List(r.Context(), mediaPageSize, (page-1)*mediaPageSize)
	if err != nil {
		return mediaData{}, err
	}
	return mediaData{Items: items, Page: page, Pages: max((total+mediaPageSize-1)/mediaPageSize, 1), Total: total}, nil
}

func (h *Handler) renderMedia(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string) {
	data, err := h.mediaPageData(r)
	if err != nil {
		serverError(w, err)
		return
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "media", pageData{
		Title: "Media", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Data: data,
	})
}

func (h *Handler) mediaLibrary(w http.ResponseWriter, r *http.Request) {
	notice := ""
	switch r.URL.Query().Get("done") {
	case "deleted":
		notice = "Image deleted."
	case "edited":
		notice = "Saved as a new image (the original is unchanged)."
	case "saved":
		notice = "Alt text saved."
	}
	h.renderMedia(w, r, 200, notice, "")
}

// mediaPicker is the htmx fragment shown in the editor's "Choose an image" dialog.
func (h *Handler) mediaPicker(w http.ResponseWriter, r *http.Request) {
	data, err := h.mediaPageData(r)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.pages["media"].ExecuteTemplate(w, "picker", data); err != nil {
		log.Printf("render picker: %v", err)
	}
}

type uploadResult struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Thumb string `json:"thumb"`
	Alt   string `json:"alt"`
}

func uploadProblem(name string, err error) string {
	switch {
	case errors.Is(err, media.ErrUnsupported):
		return name + ": not a supported image (use JPEG, PNG, GIF or WebP)."
	case errors.Is(err, media.ErrTooLarge):
		return fmt.Sprintf("%s: larger than %d MB.", name, media.MaxFileSize>>20)
	case errors.Is(err, media.ErrCorrupt):
		return name + ": couldn't be read as an image."
	case errors.Is(err, media.ErrDimensions):
		return fmt.Sprintf("%s: too many pixels (max %d per side and %d megapixels).", name, media.MaxDimension, media.MaxPixels/1_000_000)
	}
	log.Printf("media upload %q: %v", name, err)
	return name + ": upload failed."
}

func (h *Handler) mediaUpload(w http.ResponseWriter, r *http.Request) {
	wantsJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	finish := func(status int, uploaded []uploadResult, problems []string) {
		if wantsJSON {
			if uploaded == nil {
				uploaded = []uploadResult{} // encode as [] rather than null
			}
			if problems == nil {
				problems = []string{}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"uploaded": uploaded, "errors": problems})
			return
		}
		notice := ""
		if len(uploaded) > 0 {
			notice = fmt.Sprintf("Uploaded %d image%s.", len(uploaded), map[bool]string{true: "", false: "s"}[len(uploaded) == 1])
		}
		h.renderMedia(w, r, status, notice, strings.Join(problems, " "))
	}

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			finish(http.StatusRequestEntityTooLarge, nil, []string{
				fmt.Sprintf("Upload too large: at most %d files of %d MB each.", maxFilesPerBatch, media.MaxFileSize>>20)})
			return
		}
		finish(http.StatusBadRequest, nil, []string{"The upload couldn't be read."})
		return
	}
	files := r.MultipartForm.File["files"]
	switch {
	case len(files) == 0:
		finish(http.StatusBadRequest, nil, []string{"Choose at least one image."})
		return
	case len(files) > maxFilesPerBatch:
		finish(http.StatusBadRequest, nil, []string{fmt.Sprintf("Upload at most %d images at a time.", maxFilesPerBatch)})
		return
	}

	user := auth.CurrentIdentity(r)
	var uploaded []uploadResult
	var problems []string
	for _, fh := range files {
		name := media.CleanName(fh.Filename)
		if fh.Size > media.MaxFileSize {
			problems = append(problems, uploadProblem(name, media.ErrTooLarge))
			continue
		}
		f, err := fh.Open()
		if err != nil {
			problems = append(problems, uploadProblem(name, err))
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, media.MaxFileSize+1))
		f.Close()
		if err != nil {
			problems = append(problems, uploadProblem(name, err))
			continue
		}
		it, err := h.Media.Save(r.Context(), user.UserID, fh.Filename, data)
		if err != nil {
			problems = append(problems, uploadProblem(name, err))
			continue
		}
		uploaded = append(uploaded, uploadResult{it.ID, it.OriginalName, it.URL(), it.ThumbURL(), it.Alt})
	}
	status := http.StatusOK
	if len(uploaded) == 0 {
		status = http.StatusBadRequest
	}
	finish(status, uploaded, problems)
}

func (h *Handler) mediaAlt(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	alt := strings.TrimSpace(r.PostFormValue("alt"))
	if utf8.RuneCountInString(alt) > maxAltLen {
		h.renderMedia(w, r, 400, "", fmt.Sprintf("Alt text must be at most %d characters.", maxAltLen))
		return
	}
	err := h.Media.SetAlt(r.Context(), id, alt)
	if errors.Is(err, media.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/media?done=saved", http.StatusSeeOther)
}

func (h *Handler) mediaDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := h.Media.Delete(r.Context(), id)
	if err != nil && !errors.Is(err, media.ErrNotFound) {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/media?done=deleted", http.StatusSeeOther)
}
