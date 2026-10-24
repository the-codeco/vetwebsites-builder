package admin

import (
	"errors"
	"fmt"
	"image"
	"net/http"
	"strconv"
	"strings"

	"webbuilder/internal/auth"
	"webbuilder/internal/media"
)

type editChoice struct {
	Value, Label string
	Selected     bool
}

type imageEditData struct {
	Item         media.Item
	Rotations    []editChoice
	Widths       []editChoice
	FlipH, FlipV bool
	X, Y, W, H   string
}

// maxWidths are the sizes the editor offers for shrinking (0 keeps the size).
var maxWidths = []int{0, 800, 1200, 1600, 2400}

func newImageEditData(it media.Item, rotate, maxW int) imageEditData {
	d := imageEditData{Item: it}
	for _, r := range []int{0, 90, 180, 270} {
		label := "Not turned"
		if r > 0 {
			label = fmt.Sprintf("%d° clockwise", r)
		}
		d.Rotations = append(d.Rotations, editChoice{strconv.Itoa(r), label, r == rotate})
	}
	for _, w := range maxWidths {
		label := "Keep the size"
		if w > 0 {
			label = fmt.Sprintf("%d pixels wide", w)
		}
		d.Widths = append(d.Widths, editChoice{strconv.Itoa(w), label, w == maxW})
	}
	return d
}

func (h *Handler) editableItem(w http.ResponseWriter, r *http.Request) (media.Item, bool) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return media.Item{}, false
	}
	it, err := h.Media.Get(r.Context(), id)
	if errors.Is(err, media.ErrNotFound) {
		http.NotFound(w, r)
		return media.Item{}, false
	}
	if err != nil {
		serverError(w, err)
		return media.Item{}, false
	}
	if it.Mime == "image/gif" {
		h.renderMedia(w, r, http.StatusBadRequest, "", "GIFs can't be edited here: they may be animated, and editing would keep only the first frame.")
		return media.Item{}, false
	}
	return it, true
}

func (h *Handler) renderMediaEdit(w http.ResponseWriter, r *http.Request, status int, errMsg string, d imageEditData) {
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "media_edit", pageData{Title: "Edit image", User: id, CSRF: id.CSRF, Error: errMsg, Data: d})
}

func (h *Handler) mediaEditPage(w http.ResponseWriter, r *http.Request) {
	it, ok := h.editableItem(w, r)
	if !ok {
		return
	}
	h.renderMediaEdit(w, r, http.StatusOK, "", newImageEditData(it, 0, 0))
}

// parseEdit reads the editor form. The crop is four numbers, all given or all blank.
func parseEdit(r *http.Request, it media.Item) (media.Edit, error) {
	var e media.Edit
	rot, err := strconv.Atoi(r.PostFormValue("rotate"))
	if err != nil || rot < 0 || rot > 270 || rot%90 != 0 {
		return e, errors.New("Choose how far to turn the image: 0, 90, 180 or 270 degrees.")
	}
	e.Rotate = rot
	e.FlipH, e.FlipV = r.PostFormValue("flip_h") != "", r.PostFormValue("flip_v") != ""
	if v := r.PostFormValue("max_width"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > media.MaxDimension {
			return e, errors.New("That isn't a size the image can be shrunk to.")
		}
		e.MaxWidth = n
	}

	vals := make([]string, 4)
	given := 0
	for i, k := range []string{"x", "y", "w", "h"} {
		vals[i] = strings.TrimSpace(r.PostFormValue(k))
		if vals[i] != "" {
			given++
		}
	}
	switch given {
	case 0:
		return e, nil
	case 4:
	default:
		return e, errors.New("For a crop, fill in all four of Left, Top, Width and Height, or leave them all blank.")
	}
	n := make([]int, 4)
	for i, v := range vals {
		x, err := strconv.Atoi(v)
		if err != nil || x < 0 || x > media.MaxDimension {
			return e, errors.New("The crop box must be whole numbers of pixels.")
		}
		n[i] = x
	}
	tw, th := it.Width, it.Height
	if rot%180 != 0 {
		tw, th = th, tw
	}
	c := image.Rect(n[0], n[1], n[0]+n[2], n[1]+n[3])
	if c.Dx() < media.MinCrop || c.Dy() < media.MinCrop {
		return e, fmt.Errorf("The crop box must be at least %d pixels each way.", media.MinCrop)
	}
	if c.Max.X > tw || c.Max.Y > th {
		return e, fmt.Errorf("The crop box goes outside the image, which is %d × %d pixels once turned.", tw, th)
	}
	if c != image.Rect(0, 0, tw, th) { // the whole image is no crop
		e.Crop = &c
	}
	return e, nil
}

func (h *Handler) mediaEdit(w http.ResponseWriter, r *http.Request) {
	it, ok := h.editableItem(w, r)
	if !ok {
		return
	}
	rot, _ := strconv.Atoi(r.PostFormValue("rotate"))
	maxW, _ := strconv.Atoi(r.PostFormValue("max_width"))
	again := func(msg string) {
		d := newImageEditData(it, rot, maxW)
		d.FlipH, d.FlipV = r.PostFormValue("flip_h") != "", r.PostFormValue("flip_v") != ""
		d.X, d.Y, d.W, d.H = r.PostFormValue("x"), r.PostFormValue("y"), r.PostFormValue("w"), r.PostFormValue("h")
		h.renderMediaEdit(w, r, http.StatusBadRequest, msg, d)
	}
	e, err := parseEdit(r, it)
	if err != nil {
		again(err.Error())
		return
	}
	if msg := h.limitProblem(r.Context(), "storage", 0); msg != "" { // the edit is saved as a new picture
		again(msg)
		return
	}
	user := auth.CurrentIdentity(r)
	if _, err := h.Media.Edited(r.Context(), user.UserID, it.ID, e); err != nil {
		switch {
		case errors.Is(err, media.ErrBadEdit):
			again("That edit isn't valid for this image.")
		case errors.Is(err, media.ErrTooLarge):
			again("The edited image would be larger than 10 MB. Try shrinking it.")
		case errors.Is(err, media.ErrNotEditable):
			h.renderMedia(w, r, http.StatusBadRequest, "", "This kind of image can't be edited.")
		default:
			serverError(w, err)
		}
		return
	}
	http.Redirect(w, r, "/admin/media?done=edited", http.StatusSeeOther)
}
