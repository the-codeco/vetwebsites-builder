// Package themes loads embedded themes and renders blocks and pages with them.
// A theme may override any template; missing ones fall back to the default theme.
package themes

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"regexp"
	"sort"
	"strings"

	"webbuilder/internal/blocks"
	"webbuilder/internal/media"
)

const DefaultTheme = "default"

type Token struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Kind    string `json:"kind"` // color | font | size
	Default string `json:"default"`
	// Dark is the colour used in dark mode (colours only). Empty means the token doesn't change.
	Dark string `json:"dark"`
}

// rawMeta is a theme's theme.json. A theme "extends" another (default, if not
// stated): it inherits its templates, stylesheets and tokens, and only needs to
// provide what differs. A child's token entry for an existing key may give just
// the key and a new default.
type rawMeta struct {
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Extends     string  `json:"extends"`
	Tokens      []Token `json:"tokens"`
}

type Theme struct {
	Name        string
	Label       string
	Description string
	Tokens      []Token  // inherited tokens merged with this theme's own
	Styles      []string // stylesheet URLs to load, parent first
	FS          fs.FS    // theme directory, for static assets
	consentJS   string   // address of the cookie-notice script, with its fingerprint
	trackJS     string   // address of the analytics loader, with its fingerprint
	colorJS     string   // address of the colour-mode script, with its fingerprint
	menuJS      string   // address of the menu script, with its fingerprint
	tmpl        *template.Template
}

type Engine struct{ themes map[string]*Theme }

// SiteInfo is the site-wide data layout.html receives.
type SiteInfo struct {
	Title, Theme string
	Styles       []string      // theme stylesheets, in load order
	CSSURL       string        // the generated /site.css (tokens + custom CSS)
	ColorMode    string        // "toggle" or "auto" when the visitor can switch to dark mode; empty when dark mode is off or the theme has none
	ColorJS      string        // the script that applies the visitor's choice (loaded in <head>, before the page paints)
	Consent      *ConsentInfo  // the cookie notice, nil when it is off
	ConsentJS    string        // the script that shows it and remembers the answer
	TrackJS      string        // the analytics loader (used only when Tracking is set)
	Footer       FooterInfo    // the footer the owner built
	Brand        BrandInfo     // the logo and browser-tab icon
	Details      DetailsInfo   // the contact details
	Tracking     *TrackingInfo // analytics services to load, nil when there are none (or the visitor is logged in)
	Search       bool          // show the search box (the site owner can turn it off)
	MenuJS       string        // the menu script's address; layouts load it with <script src="{{.Site.MenuJS}}" defer>
}

// Info builds the layout's site data. previewTheme marks a request that views
// the theme without activating it, so the generated CSS must match too.
func (t *Theme) Info(title string, previewTheme bool) SiteInfo {
	css := "/site.css"
	if previewTheme {
		css += "?theme=" + t.Name
	}
	return SiteInfo{Title: title, Theme: t.Name, Styles: t.Styles, CSSURL: css, MenuJS: t.menuJS, ColorJS: t.colorJS, ConsentJS: t.consentJS, TrackJS: t.trackJS}
}

// TrackJS is the address of the analytics loader script.
func (t *Theme) TrackJS() string { return t.trackJS }

// Swatches returns a few colour defaults for showing what a theme looks like.
func (t *Theme) Swatches() []string {
	var out []string
	for _, key := range []string{"color-bg", "color-surface", "color-primary", "color-text"} {
		for _, tk := range t.Tokens {
			if tk.Key == key {
				out = append(out, tk.Default)
			}
		}
	}
	return out
}

// Navigation, ready to render. Hidden items and empty panels are already removed.
type NavLink struct {
	Label, URL, Description string
	NewTab, Active          bool
	Button                  bool // drawn as a button (only top-level links can be)
}

type NavColumn struct {
	Heading string
	URL     string // the heading is a link when set
	Active  bool   // that link is the current page
	Links   []NavLink
}

// NavItem is a top-level entry: Kind is "link", "dropdown" or "mega".
type NavItem struct {
	Kind, Label, URL string
	NewTab           bool
	Button           bool // drawn as a button
	Active           bool // this link is the current page
	Within           bool // a link inside its panel is the current page
	Children         []NavLink
	Columns          []NavColumn
}

// NavSection is one menu (top bar, main navigation or footer). Sticky means the
// site owner chose to keep it on screen while the page scrolls.
type NavSection struct {
	Sticky bool
	Items  []NavItem
}

type Nav struct{ TopBar, Main, Footer NavSection }

// Blog data shown by the blog templates and the "Latest posts" block.
type CategoryLink struct{ Name, URL string }

type PostSummary struct {
	Title, URL, Excerpt, Cover, Date, DateISO string
	Categories                                []CategoryLink
}

type BlogList struct {
	Heading          string
	Category         *CategoryLink // set when filtered by category
	Posts            []PostSummary
	Page, Pages      int
	PrevURL, NextURL string
}

type BlogPost struct {
	Title, Cover, Date, DateISO, BackURL string
	Categories                           []CategoryLink
	Body                                 template.HTML // already sanitised
}

// SearchPage is what the search results template shows.
type SearchPage struct {
	Query            string
	Items            []SearchItem
	Total            int
	Page, Pages      int
	PrevURL, NextURL string
	Limited          bool // the visitor has searched too often; ask them to wait
}

type SearchItem struct {
	Kind, Title, Path string
	Snippet           template.HTML // already escaped, with <mark> around matches
}

// BrandInfo is the logo and tab icon for the header.
type BrandInfo struct {
	Logo, Alt, Icon string
	Height          int  // logo height in pixels
	ShowTitle       bool // the site title is shown beside the logo (always true without a logo)
}

// SocialLink is one social profile.
type SocialLink struct{ Label, URL string }

// DetailsInfo is the contact details, with the links made.
type DetailsInfo struct {
	Phone, PhoneLink       string
	WhatsApp, WhatsAppLink string
	Email                  string
	Address, Hours         string
	Social                 []SocialLink
	InFooter               bool
}

// Empty says whether there is nothing to show.
func (d DetailsInfo) Empty() bool {
	return d.Phone == "" && d.WhatsApp == "" && d.Email == "" && d.Address == "" && d.Hours == "" && len(d.Social) == 0
}

// TrackingInfo is what the analytics loader needs.
type TrackingInfo struct {
	GA4, Plausible string
	Wait           bool   // load only after the visitor accepts the cookie notice
	JS             string // the loader script
}

// FooterInfo is the footer: rows of columns of widgets above a bottom line. Classes and Style go
// on the <footer> element (the owner's padding and colours).
type FooterInfo struct {
	Rows    []FooterRow
	Bottom  string
	Classes string
	Style   template.CSS
}

// FooterRow is one band of columns. Ratio is "equal", "wide-left", "wide-middle" or "wide-right";
// Align is "left", "center" or "right".
type FooterRow struct {
	Ratio, Align string
	Columns      []FooterColumn
}

// FooterColumn is one column's widgets, top to bottom.
type FooterColumn struct{ Widgets []FooterWidget }

// FooterWidget is one piece of footer content. Type is "text", "links", "contact", "social",
// "logo" or "newsletter"; the fields used depend on it (see package footer). Show lists the
// contact details to show; Form is the newsletter signup's state.
type FooterWidget struct {
	Type, Heading, Text string
	Links               []NavLink
	Inline              bool
	Show                map[string]bool
	Button, Success     string
	Form                NewsletterForm
}

// ConsentInfo is what the cookie notice shows.
type ConsentInfo struct {
	Text               string
	LinkLabel, LinkURL string
	Ask                bool   // Accept / Decline instead of a single OK
	Version            string // changes whenever the notice changes, so visitors are asked again
}

// ContactForm is what the contact form block needs beyond its own settings.
type ContactForm struct {
	Token string // proves the form was loaded (and when)
	Path  string // the page to come back to after sending
	Sent  bool   // show the thank-you instead of the form
}

// NewsletterForm is what the newsletter signup block needs beyond its own settings.
type NewsletterForm struct {
	Token   string // proves the form was loaded (and when)
	Path    string // the page to come back to after signing up
	Sent    bool   // show the thank-you instead of the form
	Confirm bool   // the visitor must still confirm by email
}

// SEO is everything a layout needs for search engines and link previews. The
// "seo-head" partial renders it, so a theme only has to include that partial.
type SEO struct {
	Title, Description string
	Canonical          string // absolute URL; empty when the page shouldn't be indexed
	Robots             string // e.g. "noindex, follow"; empty means no restriction
	Type               string // Open Graph type: "website" or "article"
	SiteName, Image    string // Image is an absolute URL
	Published          string // RFC 3339, articles only
	Modified           string
	JSONLD             template.JS // structured data, already JSON-encoded and safe inside a script tag
}

// PageData is what layout.html receives.
type PageData struct {
	Site        SiteInfo
	PageTitle   string
	Description string // kept for older layouts; SEO.Description is what the partial uses
	SEO         SEO
	Nav         Nav
	Content     template.HTML
	Year        int
	Preview     bool
}

var (
	colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	sizeRe  = regexp.MustCompile(`^\d{1,4}(\.\d{1,2})?(px|rem|em|%)$`)
	keyRe   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

	// FontStacks maps the font token values to CSS font-family stacks. These are
	// all system fonts: nothing is downloaded from a third party, so visitors'
	// browsers make no extra requests and there is nothing to block by CSP.
	FontStacks = map[string]string{
		"sans":      `system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`,
		"humanist":  `Seravek, "Gill Sans Nova", Ubuntu, Calibri, "DejaVu Sans", sans-serif`,
		"rounded":   `ui-rounded, "Hiragino Maru Gothic ProN", Quicksand, Nunito, Calibri, sans-serif`,
		"serif":     `Georgia, "Times New Roman", serif`,
		"classical": `Palatino, "Palatino Linotype", "Book Antiqua", "URW Palladio L", serif`,
		"mono":      `ui-monospace, Menlo, Consolas, monospace`,
	}

	// FontOptions lists the fonts in the order the editor offers them.
	FontOptions = []struct{ Key, Label string }{
		{"sans", "Sans-serif"}, {"humanist", "Humanist sans"}, {"rounded", "Rounded sans"},
		{"serif", "Serif"}, {"classical", "Classical serif"}, {"mono", "Monospace"},
	}

	funcs = template.FuncMap{
		// blocks.Clean already sanitises; sanitising again here keeps templates safe
		// even if a block ever reaches rendering without being cleaned.
		"richtext": func(s string) template.HTML { return template.HTML(blocks.SanitizeHTML(s)) },
		// what a video or map block shows; the address is checked again here, never trusted as stored
		"video": func(u string) *blocks.Video {
			v, ok := blocks.VideoOf(u)
			if !ok {
				return nil
			}
			return &v
		},
		// the map for a block, whichever service it uses (nil when its position or address is not usable)
		"mapfor": func(provider, c, place string, z any) *blocks.Map {
			f, _ := z.(float64)
			m, ok := blocks.MapFor(provider, c, place, f)
			if !ok {
				return nil
			}
			return &m
		},
		"mapof": func(c string, z any) *blocks.Map {
			f, _ := z.(float64)
			m, ok := blocks.MapOf(c, f)
			if !ok {
				return nil
			}
			return &m
		},
		// a link the owner typed, checked again here; html/template would otherwise refuse tel: links
		"safeurl": func(s any) template.URL {
			u, _ := s.(string)
			return template.URL(blocks.SafeURL(u))
		},
		"lines": blocks.Lines,
		// srcset + sizes for one of the site's own images, so small screens fetch a smaller file
		"srcset": media.SrcsetAttr,
		"items":  blocks.Items,
	}

	wrapper = template.Must(template.New("wrapper").Parse(
		`<section id="block-{{.ID}}" class="block block-{{.Type}} {{.Classes}}"` +
			`{{if .Vars}} style="{{.Vars}}"{{end}}><div class="block-inner">{{.Inner}}</div></section>`))

	// the wrapper for a block inside a layout: a div, not a section, marked "nested" so its padding and width
	// come from its own settings and not from the page's column
	nestedWrapper = template.Must(template.New("nestedWrapper").Parse(
		`<div id="block-{{.ID}}" class="block block-{{.Type}} nested {{.Classes}}"` +
			`{{if .Vars}} style="{{.Vars}}"{{end}}><div class="block-inner">{{.Inner}}</div></div>`))
)

// ValidToken reports whether v is acceptable for a token of the given kind.
func ValidToken(kind, v string) bool {
	switch kind {
	case "color":
		return colorRe.MatchString(v)
	case "size":
		return sizeRe.MatchString(v)
	case "font":
		if _, ok := FontStacks[v]; ok {
			return true
		}
		return CustomFontRe.MatchString(v) && len(v) <= 60 // an uploaded font; CSS falls back if it no longer exists
	}
	return false
}

// CustomFontRe is the shape of a font setting that names an uploaded font: "custom:" and the family name as a slug.
var CustomFontRe = regexp.MustCompile(`^custom:[a-z0-9]+(-[a-z0-9]+)*$`)

// FontFace is an uploaded font file the site loads: one family at one weight.
type FontFace struct {
	Family string
	Slug   string // the family as used in a font setting, such as custom:acme-sans
	Weight int
	URL    string // where the file is served from
}

// fontStack is the CSS for a font setting: a built-in stack, or an uploaded family with a plain sans-serif behind it
// (for the moment before the file arrives). ok is false for a setting that names an uploaded font that is gone.
func fontStack(v string, faces []FontFace) (string, bool) {
	if s, ok := FontStacks[v]; ok {
		return s, true
	}
	for _, f := range faces {
		if f.Slug == v {
			return `"` + f.Family + `", ` + FontStacks["sans"], true
		}
	}
	return "", false
}

// parseInto adds layout.html and blocks/*.html from dir; later calls override earlier definitions.
func parseInto(t *template.Template, dir fs.FS) error {
	for _, pat := range []string{"layout.html", "partials/*.html", "blocks/*.html", "blog/*.html", "search/*.html"} {
		if m, _ := fs.Glob(dir, pat); len(m) > 0 {
			if _, err := t.ParseFS(dir, pat); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) Lookup(name string) (*Theme, bool) { t, ok := e.themes[name]; return t, ok }

// Get returns the named theme, falling back to the default.
func (e *Engine) Get(name string) *Theme {
	if t, ok := e.themes[name]; ok {
		return t
	}
	return e.themes[DefaultTheme]
}

func (e *Engine) List() []*Theme {
	out := make([]*Theme, 0, len(e.themes))
	for _, t := range e.themes {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RenderBlock renders one block inside its styled section wrapper.
func (t *Theme) RenderBlock(b blocks.Block) (template.HTML, error) {
	return t.RenderBlockWith(b, nil)
}

// RenderBlockWith is RenderBlock for blocks that show data from elsewhere
// (e.g. the latest posts); the template reads it as .Extra. A layout's children get the same data.
func (t *Theme) RenderBlockWith(b blocks.Block, extra any) (template.HTML, error) {
	return t.RenderBlockTree(b, func(blocks.Block) any { return extra })
}

// RenderBlockTree renders a block and, for a layout, the blocks in its columns. extraFor supplies the data each
// block needs beyond its own settings (it may be nil).
func (t *Theme) RenderBlockTree(b blocks.Block, extraFor func(blocks.Block) any) (template.HTML, error) {
	return t.renderBlock(b, extraFor, false)
}

func (t *Theme) renderBlock(b blocks.Block, extraFor func(blocks.Block) any, nested bool) (template.HTML, error) {
	if _, ok := blocks.Get(b.Type); !ok {
		return "", nil
	}
	var columns [][]template.HTML
	if b.Type == blocks.LayoutType {
		if nested {
			return "", nil // a layout inside a layout is not allowed; skip it rather than nest forever
		}
		columns = make([][]template.HTML, blocks.LayoutColumns(b.Props))
		for i := range columns {
			if i >= len(b.Children) {
				break
			}
			for _, c := range b.Children[i] {
				html, err := t.renderBlock(c, extraFor, true)
				if err != nil {
					log.Printf("themes: %v", err) // one broken block shouldn't empty the whole layout
					continue
				}
				columns[i] = append(columns[i], html)
			}
		}
	}
	var extra any
	if extraFor != nil {
		extra = extraFor(b)
	}
	var inner bytes.Buffer
	err := t.tmpl.ExecuteTemplate(&inner, "block/"+b.Type, struct {
		ID      int64
		Props   map[string]any
		Extra   any
		Columns [][]template.HTML
	}{b.ID, b.Props, extra, columns})
	if err != nil {
		return "", fmt.Errorf("block %d (%s): %w", b.ID, b.Type, err)
	}
	var out bytes.Buffer
	w := wrapper
	if nested {
		w = nestedWrapper
	}
	err = w.Execute(&out, map[string]any{
		"ID": b.ID, "Type": b.Type, "Classes": b.Style.Classes(),
		"Vars": b.Style.CSSVars(), "Inner": template.HTML(inner.String()),
	})
	return template.HTML(out.String()), err
}

// RenderSection renders a named content template (e.g. "blog/list") to HTML
// for placing inside the layout.
func (t *Theme) RenderSection(name string, data any) (template.HTML, error) {
	var buf bytes.Buffer
	if err := t.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("section %s: %w", name, err)
	}
	return template.HTML(buf.String()), nil
}

func (t *Theme) RenderPage(w io.Writer, d PageData) error {
	return t.tmpl.ExecuteTemplate(w, "layout.html", d)
}

// Colour modes: how the site treats dark mode.
const (
	ColorLight  = "light"  // no dark mode
	ColorToggle = "toggle" // light until a visitor switches to dark
	ColorAuto   = "auto"   // follows the visitor's system setting; they can still switch
)

// ValidColorMode reports whether m is one of the colour modes.
func ValidColorMode(m string) bool { return m == ColorLight || m == ColorToggle || m == ColorAuto }

// HasDark reports whether the theme ships a dark palette. A theme that is dark already
// (Midnight) has none: there is nothing to switch to.
func (t *Theme) HasDark() bool {
	for _, tk := range t.Tokens {
		if tk.Dark != "" {
			return true
		}
	}
	return false
}

// EffectiveColorMode is the mode the site really runs in: the owner's choice, unless the theme has no dark palette.
func (t *Theme) EffectiveColorMode(mode string) string {
	if (mode == ColorToggle || mode == ColorAuto) && t.HasDark() {
		return mode
	}
	return ""
}

// brandTokens keep a colour the owner chose in dark mode too: a brand colour should not change
// because the background did. Every other colour follows the theme's dark palette, so the owner's
// light-mode background can never end up under light-mode text.
var brandTokens = map[string]bool{"color-primary": true, "color-primary-text": true}

// CSS builds the site stylesheet: validated token values as CSS variables
// (invalid or missing values fall back to the theme default), then, if the colour mode
// allows it, the dark palette, then custom CSS.
func (t *Theme) CSS(tokens map[string]string, custom, colorMode string, faces []FontFace) string {
	mode := t.EffectiveColorMode(colorMode)
	var b strings.Builder
	for _, f := range faces { // family names are limited to letters, digits, spaces and dashes, so they are safe in quotes
		fmt.Fprintf(&b, "@font-face { font-family: \"%s\"; font-weight: %d; font-style: normal; font-display: swap; src: url(\"%s\") format(\"woff2\"); }\n", f.Family, f.Weight, f.URL)
	}
	b.WriteString(":root {\n")
	if mode != "" {
		b.WriteString("  color-scheme: light;\n")
	}
	for _, tk := range t.Tokens {
		v, ok := tokens[tk.Key]
		if !ok || !ValidToken(tk.Kind, v) {
			v = tk.Default
		}
		if tk.Kind == "font" {
			stack, ok := fontStack(v, faces)
			if !ok { // an uploaded font that has since been deleted
				stack = FontStacks[tk.Default]
			}
			v = stack
		}
		fmt.Fprintf(&b, "  --%s: %s;\n", tk.Key, v)
	}
	b.WriteString("}\n")
	if mode != "" {
		var dark strings.Builder
		for _, tk := range t.Tokens {
			if tk.Dark == "" {
				continue
			}
			fmt.Fprintf(&dark, "  --%s: %s;\n", tk.Key, DarkValue(tk, tokens))
		}
		// Blocks the owner coloured by hand keep the look they designed: inside a "pinned" block (custom
		// background) the light palette is put back, so its text and links stay readable on the background
		// chosen for it; a custom text colour on its own would be unreadable on the dark page, so it is ignored.
		var light strings.Builder
		for _, tk := range t.Tokens {
			if tk.Dark == "" {
				continue
			}
			v, ok := tokens[tk.Key]
			if !ok || !ValidToken(tk.Kind, v) {
				v = tk.Default
			}
			fmt.Fprintf(&light, "--%s: %s; ", tk.Key, v)
		}
		blockRules := func(root string) string {
			return root + " .block.pinned { " + light.String() + "color: var(--fg, var(--color-text)); }\n" +
				root + " .block.text-only { color: inherit !important; }\n"
		}
		// the visitor's explicit choice
		const chosen = `:root[data-theme="dark"]`
		fmt.Fprintf(&b, "%s {\n  color-scheme: dark;\n%s}\n", chosen, dark.String())
		b.WriteString(blockRules(chosen))
		if mode == ColorAuto {
			// no choice made (or no script): follow the system
			const system = `:root:not([data-theme="light"])`
			fmt.Fprintf(&b, "@media (prefers-color-scheme: dark) {\n  %s {\n    color-scheme: dark;\n%s  }\n  %s}\n",
				system, strings.ReplaceAll(dark.String(), "\n  --", "\n    --"), blockRules(system))
		}
	}
	b.WriteString(custom)
	return b.String()
}
