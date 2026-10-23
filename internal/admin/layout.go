package admin

import (
	"net/http"
	"strconv"
	"strings"

	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
)

// layoutView is what the "Page layout" and "Header" cards on Appearance show.
type layoutView struct {
	content.Layout
	Banners       []string // always MaxBanners slots, so the form shows every row
	ImageModes    []struct{ Key, Label string }
	HeaderStyles  []struct{ Key, Label string }
	BannerPlaces  []struct{ Key, Label string }
	BannerHeights []struct {
		Key, Label string
		Pixels     int
	}
	MinWidth, MaxWidth int
}

func newLayoutView(l content.Layout) layoutView {
	v := layoutView{Layout: l, Banners: make([]string, content.MaxBanners), ImageModes: content.ImageModes,
		HeaderStyles: content.HeaderStyles, BannerPlaces: content.BannerPlaces, BannerHeights: content.BannerHeights,
		MinWidth: content.MinBoxWidth, MaxWidth: content.MaxBoxWidth}
	copy(v.Banners, l.Header.Banners)
	return v
}

func (h *Handler) registerLayout(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /admin/appearance/layout", admin(h.appearanceLayout))
	mux.Handle("POST /admin/appearance/header", admin(h.appearanceHeader))
}

// colorField is a colour from a form: "" (the theme's) or #rrggbb; ok is false for anything else.
func colorField(r *http.Request, name string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(r.PostFormValue(name)))
	if v == "" {
		return "", true
	}
	return v, len(v) == 7 && v[0] == '#' && strings.Trim(v[1:], "0123456789abcdef") == ""
}

// appearanceLayout saves the page frame: full width or boxed, the box width, the background colour and picture.
func (h *Handler) appearanceLayout(w http.ResponseWriter, r *http.Request) {
	st, err := h.Content.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	l := st.Layout
	l.Boxed = r.PostFormValue("boxed") == "on"
	l.ImageMode = r.PostFormValue("image_mode")
	l.Image = strings.TrimSpace(r.PostFormValue("image"))
	width, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("width")))
	switch bg, ok := colorField(r, "background"); {
	case !ok:
		h.renderAppearance(w, r, 400, "", "The page background colour must look like #1a2b3c, or be left empty for the theme's.", nil)
		return
	case l.Image != "" && blocks.SafeImageURL(l.Image) == "":
		h.renderAppearance(w, r, 400, "", "The background picture must be from the media library (such as /media/photo.jpg) or an https:// address.", nil)
		return
	case l.Boxed && (err != nil || width < content.MinBoxWidth || width > content.MaxBoxWidth):
		h.renderAppearance(w, r, 400, "", "The page width must be a number of pixels from "+strconv.Itoa(content.MinBoxWidth)+" to "+strconv.Itoa(content.MaxBoxWidth)+".", nil)
		return
	default:
		l.Background = bg
	}
	if err == nil {
		l.Width = width
	}
	if err := h.Content.SetLayout(r.Context(), l); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=layout", http.StatusSeeOther)
}

// appearanceHeader saves the header style, tagline, banner pictures and the menu bar's colours.
func (h *Handler) appearanceHeader(w http.ResponseWriter, r *http.Request) {
	st, err := h.Content.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	l := st.Layout
	hd := content.Header{
		Style: r.PostFormValue("header_style"), Tagline: r.PostFormValue("tagline"),
		BannerPlace: r.PostFormValue("banner_place"), BannerHeight: r.PostFormValue("banner_height"),
	}
	if len([]rune(strings.TrimSpace(hd.Tagline))) > content.MaxTagline {
		h.renderAppearance(w, r, 400, "", "The tagline can be at most "+strconv.Itoa(content.MaxTagline)+" characters.", nil)
		return
	}
	for i := 1; i <= content.MaxBanners; i++ {
		b := strings.TrimSpace(r.PostFormValue("banner_" + strconv.Itoa(i)))
		if b == "" {
			continue
		}
		if blocks.SafeImageURL(b) == "" {
			h.renderAppearance(w, r, 400, "", "Banner picture "+strconv.Itoa(i)+" must be from the media library (such as /media/photo.jpg) or an https:// address.", nil)
			return
		}
		hd.Banners = append(hd.Banners, b)
	}
	for _, f := range []struct {
		name string
		to   *string
	}{{"bar_background", &hd.BarBackground}, {"bar_text", &hd.BarText}, {"bar_active", &hd.BarActive}} {
		v, ok := colorField(r, f.name)
		if !ok {
			h.renderAppearance(w, r, 400, "", "Menu bar colours must look like #1a2b3c, or be left empty for the theme's.", nil)
			return
		}
		*f.to = v
	}
	l.Header = hd
	if err := h.Content.SetLayout(r.Context(), l); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=header", http.StatusSeeOther)
}
