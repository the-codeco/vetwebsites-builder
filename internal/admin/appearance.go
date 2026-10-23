package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"webbuilder/internal/auth"
	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
	"webbuilder/internal/themes"
)

const (
	maxSiteTitle = 100
	maxCustomCSS = 20_000
)

// Appearance changes how the whole site looks, so it is admin-only.
func (h *Handler) registerAppearance(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/appearance", admin(h.appearancePage))
	mux.Handle("POST /admin/appearance/title", admin(h.appearanceTitle))
	h.registerFonts(mux, admin)
	mux.Handle("POST /admin/appearance/brand", admin(h.appearanceBrand))
	mux.Handle("POST /admin/appearance/theme", admin(h.appearanceTheme))
	mux.Handle("POST /admin/appearance/search", admin(h.appearanceSearch))
	mux.Handle("POST /admin/appearance/colormode", admin(h.appearanceColorMode))
	mux.Handle("POST /admin/appearance/consent", admin(h.appearanceConsent))
	mux.Handle("POST /admin/appearance/tokens", admin(h.appearanceTokens))
	mux.Handle("POST /admin/appearance/tokens/reset", admin(h.appearanceReset))
	mux.Handle("POST /admin/appearance/dark", admin(h.appearanceDark))
	mux.Handle("POST /admin/appearance/dark/reset", admin(h.appearanceDarkReset))
	mux.Handle("POST /admin/appearance/css", admin(h.appearanceCSS))
}

type themeCard struct {
	Name, Label, Description string
	Swatches                 []string
	Active                   bool
}

type fontOption struct {
	Value, Label, Stack string
	Selected            bool
}

type tokenField struct {
	Key, Label, Kind string
	Value, Default   string // Value is the saved override ("" = use the theme's default)
	Fonts            []fontOption
}

// darkField is one colour of the dark palette in the editor.
type darkField struct {
	Key, Label string
	Value      string // the owner's dark colour ("" = the theme's)
	Default    string // what dark mode uses when Value is blank
}

type appearanceData struct {
	SiteTitle        string
	ShowSearch       bool
	ColorMode        string // light, toggle or auto
	Consent          content.Consent
	ConsentHint      string // the text used when the owner leaves it blank
	Brand            content.Brand
	Fonts            []fontView
	LogoHeights      []logoHeight
	HasDark          bool // the active theme ships a dark palette
	DarkFields       []darkField
	DarkNotes        []string // dark colour pairs that are hard to read
	HasDarkOverrides bool
	Themes           []themeCard
	ActiveLabel      string
	Tokens           []tokenField
	HasOverrides     bool
	CustomCSS        string
}

func tokenHint(kind string) string {
	switch kind {
	case "color":
		return "use a hex colour such as #3b5bdb."
	case "size":
		return "use a size such as 16px, 1.5rem or 80%."
	}
	return "choose one of the listed fonts."
}

func (h *Handler) renderAppearance(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string, form url.Values) {
	st, err := h.Content.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	active := h.Engine.Get(st.ActiveTheme)
	saved := st.TokensFor(active.Name)
	uploaded, err := h.Content.Fonts(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}

	d := appearanceData{SiteTitle: st.Title, ShowSearch: st.ShowSearch, ColorMode: st.ColorMode, HasDark: active.HasDark(), Consent: st.Consent, ConsentHint: content.DefaultConsentText, Brand: st.Brand, Fonts: fontViews(uploaded), LogoHeights: logoHeights(st.Brand), ActiveLabel: active.Label, CustomCSS: st.CustomCSS, HasOverrides: len(withoutDark(saved)) > 0}
	for _, t := range h.Engine.List() {
		d.Themes = append(d.Themes, themeCard{t.Name, t.Label, t.Description, t.Swatches(), t.Name == active.Name})
	}
	for _, tk := range active.Tokens {
		f := tokenField{Key: tk.Key, Label: tk.Label, Kind: tk.Kind, Default: tk.Default, Value: saved[tk.Key]}
		if form != nil { // re-showing a rejected submission
			f.Value = strings.TrimSpace(form.Get("t." + tk.Key))
		}
		if tk.Kind == "font" {
			f.Fonts = append(f.Fonts, fontOption{"", "Theme default (" + fontLabel(tk.Default) + ")", themes.FontStacks[tk.Default], f.Value == ""})
			for _, o := range themes.FontOptions {
				f.Fonts = append(f.Fonts, fontOption{o.Key, o.Label, themes.FontStacks[o.Key], f.Value == o.Key})
			}
			f.Fonts = append(f.Fonts, uploadedFontOptions(uploaded, f.Value)...)
		}
		d.Tokens = append(d.Tokens, f)
	}
	for _, tk := range active.DarkColors() {
		f := darkField{Key: tk.Key, Label: tk.Label, Value: saved[themes.DarkPrefix+tk.Key], Default: themes.DarkValue(tk, map[string]string{tk.Key: saved[tk.Key]})}
		if form != nil && form.Has("d."+tk.Key) { // re-showing a rejected submission
			f.Value = strings.TrimSpace(form.Get("d." + tk.Key))
		}
		d.HasDarkOverrides = d.HasDarkOverrides || saved[themes.DarkPrefix+tk.Key] != ""
		d.DarkFields = append(d.DarkFields, f)
	}
	for _, n := range active.DarkReadability(saved) {
		d.DarkNotes = append(d.DarkNotes, n.Text)
	}
	if form != nil {
		d.CustomCSS = form.Get("css")
		if v := form.Get("title"); v != "" {
			d.SiteTitle = v
		}
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "appearance", pageData{
		Title: "Appearance", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Wide: true, Data: d,
	})
}

func fontLabel(key string) string {
	for _, o := range themes.FontOptions {
		if o.Key == key {
			return o.Label
		}
	}
	return key
}

func (h *Handler) appearancePage(w http.ResponseWriter, r *http.Request) {
	notice := map[string]string{
		"title": "Site title saved.", "theme": "Theme activated.", "tokens": "Colours, fonts and spacing saved.",
		"reset": "Reset to the theme's defaults.", "dark": "Dark palette saved.", "darkreset": "Dark palette reset to the theme's.", "css": "Custom CSS saved.", "search": "Search box setting saved.", "colormode": "Dark mode setting saved.", "consent": "Cookie notice saved.", "font": "Font uploaded.", "fontdeleted": "Font deleted.", "brand": "Logo and icon saved.",
	}[r.URL.Query().Get("done")]
	h.renderAppearance(w, r, 200, notice, "", nil)
}

func (h *Handler) appearanceTitle(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.PostFormValue("title"))
	if title == "" || utf8.RuneCountInString(title) > maxSiteTitle {
		h.renderAppearance(w, r, 400, "", fmt.Sprintf("The site title must be 1–%d characters.", maxSiteTitle), r.PostForm)
		return
	}
	if err := h.Content.SetTitle(r.Context(), title); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=title", http.StatusSeeOther)
}

func (h *Handler) appearanceColorMode(w http.ResponseWriter, r *http.Request) {
	mode := r.PostFormValue("color_mode")
	if !themes.ValidColorMode(mode) {
		h.renderAppearance(w, r, 400, "", "Choose one of the dark mode options.", nil)
		return
	}
	if err := h.Content.SetColorMode(r.Context(), mode); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=colormode", http.StatusSeeOther)
}

// appearanceConsent saves the cookie notice. The owner can't break the page with it: the text is plain
// text (escaped when shown) and the link must be an address that is safe to follow.
func (h *Handler) appearanceConsent(w http.ResponseWriter, r *http.Request) {
	c := content.Consent{
		Enabled:   r.PostFormValue("consent_enabled") == "on",
		Ask:       r.PostFormValue("consent_mode") == "choice",
		Text:      strings.Join(strings.Fields(r.PostFormValue("consent_text")), " "),
		LinkLabel: strings.TrimSpace(r.PostFormValue("consent_link_label")),
		LinkURL:   strings.TrimSpace(r.PostFormValue("consent_link_url")),
	}
	fail := func(msg string) { h.renderAppearance(w, r, 400, "", msg, nil) }
	switch {
	case utf8.RuneCountInString(c.Text) > content.MaxConsentText:
		fail(fmt.Sprintf("The notice text must be at most %d characters.", content.MaxConsentText))
		return
	case utf8.RuneCountInString(c.LinkLabel) > content.MaxConsentLabel:
		fail(fmt.Sprintf("The link text must be at most %d characters.", content.MaxConsentLabel))
		return
	case (c.LinkLabel == "") != (c.LinkURL == ""):
		fail("Fill in both the link text and the link address, or leave both empty.")
		return
	case c.LinkURL != "" && blocks.SafeURL(c.LinkURL) == "":
		fail("The link address must be a page on this site such as /privacy, or an https:// address.")
		return
	}
	if err := h.Content.SetConsent(r.Context(), c); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=consent", http.StatusSeeOther)
}

func (h *Handler) appearanceSearch(w http.ResponseWriter, r *http.Request) {
	if err := h.Content.SetShowSearch(r.Context(), r.PostFormValue("show_search") == "on"); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=search", http.StatusSeeOther)
}

func (h *Handler) appearanceTheme(w http.ResponseWriter, r *http.Request) {
	t, ok := h.Engine.Lookup(r.PostFormValue("theme"))
	if !ok {
		h.renderAppearance(w, r, 400, "", "That theme doesn't exist.", nil)
		return
	}
	if err := h.Content.SetActiveTheme(r.Context(), t.Name); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=theme", http.StatusSeeOther)
}

// parseTokens validates a submitted token form against the theme. Blank fields
// and values equal to the theme default are not stored, so a later change to
// the theme's defaults still reaches them.
func parseTokens(theme *themes.Theme, form url.Values) (map[string]string, string) {
	out := map[string]string{}
	for _, tk := range theme.Tokens {
		v := strings.TrimSpace(form.Get("t." + tk.Key))
		if v == "" {
			continue
		}
		if tk.Kind == "color" {
			v = strings.ToLower(v)
		}
		if !themes.ValidToken(tk.Kind, v) {
			return nil, fmt.Sprintf("“%s” isn't valid: %s", tk.Label, tokenHint(tk.Kind))
		}
		if v != strings.ToLower(tk.Default) {
			out[tk.Key] = v
		}
	}
	return out, ""
}

func (h *Handler) appearanceTokens(w http.ResponseWriter, r *http.Request) {
	st, err := h.Content.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	active := h.Engine.Get(st.ActiveTheme)
	r.ParseForm() // the editor's forms arrive already parsed by the CSRF check; a request that carries its token in a header does not
	tokens, problem := parseTokens(active, r.PostForm)
	if problem == "" {
		problem = h.unknownUploadedFont(r.Context(), active, tokens)
	}
	if problem != "" {
		h.renderAppearance(w, r, 400, "", problem, r.PostForm)
		return
	}
	for k, v := range keepDark(st.TokensFor(active.Name)) {
		tokens[k] = v
	}
	if err := h.Content.SetTokens(r.Context(), active.Name, tokens); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=tokens", http.StatusSeeOther)
}

func (h *Handler) appearanceReset(w http.ResponseWriter, r *http.Request) {
	st, err := h.Content.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	active := h.Engine.Get(st.ActiveTheme)
	if err := h.Content.SetTokens(r.Context(), active.Name, keepDark(st.TokensFor(active.Name))); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=reset", http.StatusSeeOther)
}

func (h *Handler) appearanceCSS(w http.ResponseWriter, r *http.Request) {
	css := strings.ReplaceAll(r.PostFormValue("css"), "\r\n", "\n")
	switch {
	case len(css) > maxCustomCSS:
		h.renderAppearance(w, r, 400, "", fmt.Sprintf("Custom CSS must be at most %d characters.", maxCustomCSS), r.PostForm)
		return
	case strings.ContainsRune(css, 0):
		h.renderAppearance(w, r, 400, "", "Custom CSS can't contain null characters.", r.PostForm)
		return
	}
	if err := h.Content.SetCustomCSS(r.Context(), css); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=css", http.StatusSeeOther)
}

// keepDark returns saved's dark-palette colours, which the light-palette form must not lose.
func keepDark(saved map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range saved {
		if strings.HasPrefix(k, themes.DarkPrefix) {
			out[k] = v
		}
	}
	return out
}

// withoutDark returns saved without its dark-palette colours.
func withoutDark(saved map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range saved {
		if !strings.HasPrefix(k, themes.DarkPrefix) {
			out[k] = v
		}
	}
	return out
}

// parseDark validates the dark-palette form: blank fields and colours equal to what dark mode
// would use anyway are not stored, so a later change to the theme's dark colours still reaches them.
func parseDark(theme *themes.Theme, form url.Values) (map[string]string, string) {
	out := map[string]string{}
	for _, tk := range theme.DarkColors() {
		v := strings.ToLower(strings.TrimSpace(form.Get("d." + tk.Key)))
		if v == "" {
			continue
		}
		if !themes.ValidToken("color", v) {
			return nil, fmt.Sprintf("Dark “%s” isn't valid: %s", tk.Label, tokenHint("color"))
		}
		if v != strings.ToLower(tk.Dark) {
			out[themes.DarkPrefix+tk.Key] = v
		}
	}
	return out, ""
}

func (h *Handler) appearanceDark(w http.ResponseWriter, r *http.Request) {
	st, err := h.Content.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	active := h.Engine.Get(st.ActiveTheme)
	if !active.HasDark() {
		http.Error(w, "this theme has no dark palette", http.StatusBadRequest)
		return
	}
	r.ParseForm()
	dark, problem := parseDark(active, r.PostForm)
	if problem != "" {
		h.renderAppearance(w, r, 400, "", problem, r.PostForm)
		return
	}
	merged := withoutDark(st.TokensFor(active.Name))
	for k, v := range dark {
		merged[k] = v
	}
	if err := h.Content.SetTokens(r.Context(), active.Name, merged); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=dark", http.StatusSeeOther)
}

func (h *Handler) appearanceDarkReset(w http.ResponseWriter, r *http.Request) {
	st, err := h.Content.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	active := h.Engine.Get(st.ActiveTheme)
	if err := h.Content.SetTokens(r.Context(), active.Name, withoutDark(st.TokensFor(active.Name))); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=darkreset", http.StatusSeeOther)
}

type logoHeight struct {
	Key, Label string
	Selected   bool
}

func logoHeights(b content.Brand) []logoHeight {
	var out []logoHeight
	for _, h := range content.LogoHeights {
		out = append(out, logoHeight{h.Key, fmt.Sprintf("%s (%d px)", h.Label, h.Pixels), h.Key == b.LogoHeight || b.LogoHeight == "" && h.Key == "md"})
	}
	return out
}

// appearanceBrand saves the logo, the alternative logo and the browser-tab icon. All must be pictures from the media library or an
// https address; anything else is refused rather than quietly dropped, so the owner sees why nothing changed.
func (h *Handler) appearanceBrand(w http.ResponseWriter, r *http.Request) {
	b := content.Brand{
		Logo:       strings.TrimSpace(r.PostFormValue("logo")),
		LogoAlt:    strings.Join(strings.Fields(r.PostFormValue("logo_alt")), " "),
		LogoHeight: r.PostFormValue("logo_height"),
		HideTitle:  r.PostFormValue("hide_title") == "on",
		Icon:       strings.TrimSpace(r.PostFormValue("icon")),
		AltLogo:    strings.TrimSpace(r.PostFormValue("alt_logo")),
	}
	switch {
	case b.Logo != "" && blocks.SafeImageURL(b.Logo) == "":
		h.renderAppearance(w, r, 400, "", "The logo must be a picture from the media library (such as /media/logo.png) or an https:// address.", nil)
		return
	case b.AltLogo != "" && blocks.SafeImageURL(b.AltLogo) == "":
		h.renderAppearance(w, r, 400, "", "The alternative logo must be a picture from the media library (such as /media/logo-light.png) or an https:// address.", nil)
		return
	case b.Icon != "" && blocks.SafeImageURL(b.Icon) == "":
		h.renderAppearance(w, r, 400, "", "The tab icon must be a picture from the media library or an https:// address.", nil)
		return
	case utf8.RuneCountInString(b.LogoAlt) > content.MaxLogoAlt:
		h.renderAppearance(w, r, 400, "", fmt.Sprintf("The logo description must be at most %d characters.", content.MaxLogoAlt), nil)
		return
	}
	if err := h.Content.SetBrand(r.Context(), b); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/appearance?done=brand", http.StatusSeeOther)
}

// unknownUploadedFont reports a font setting that names an uploaded font which does not exist (it was deleted while
// the form was open, or the value was made up).
func (h *Handler) unknownUploadedFont(ctx context.Context, theme *themes.Theme, tokens map[string]string) string {
	fonts, err := h.Content.Fonts(ctx)
	if err != nil {
		return "The fonts could not be checked. Try again."
	}
	have := map[string]bool{}
	for _, f := range fonts {
		have[content.FontToken(f.Family)] = true
	}
	for _, tk := range theme.Tokens {
		if v := tokens[tk.Key]; tk.Kind == "font" && themes.CustomFontRe.MatchString(v) && !have[v] {
			return fmt.Sprintf("“%s” asks for a font that is not uploaded: %s", tk.Label, tokenHint("font"))
		}
	}
	return ""
}
