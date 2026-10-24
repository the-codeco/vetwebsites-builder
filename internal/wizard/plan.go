package wizard

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
	"webbuilder/internal/elements"
	"webbuilder/internal/footer"
	"webbuilder/internal/menus"
	"webbuilder/internal/themes"
)

// Answers is everything the wizard asks. Empty means "the kit's own", so a person who only picks a kit gets that kit exactly.
type Answers struct {
	Business string `json:"business,omitempty"` // "vet" or "other"
	Title    string `json:"title,omitempty"`

	Kit         string `json:"kit,omitempty"`
	Primary     string `json:"primary,omitempty"`
	Background  string `json:"background,omitempty"`
	Text        string `json:"text,omitempty"`
	FontBody    string `json:"font_body,omitempty"`
	FontHeading string `json:"font_heading,omitempty"`
	Corners     string `json:"corners,omitempty"` // "sharp", "soft" or "round"
	Dark        string `json:"dark,omitempty"`    // "off", "toggle" or "auto"
	Header      string `json:"header,omitempty"`  // "inline" or "centered"

	Logo     string            `json:"logo,omitempty"`
	LogoAlt  string            `json:"logo_alt,omitempty"`
	Phone    string            `json:"phone,omitempty"`
	WhatsApp string            `json:"whatsapp,omitempty"`
	Email    string            `json:"email,omitempty"`
	Address  string            `json:"address,omitempty"`
	Hours    string            `json:"hours,omitempty"`
	Social   map[string]string `json:"social,omitempty"`

	TopBar       string   `json:"top_bar,omitempty"` // "contact", "search" or "none"
	Cookie       bool     `json:"cookie,omitempty"`
	Newsletter   bool     `json:"newsletter,omitempty"`
	Announcement string   `json:"announcement,omitempty"`
	Sections     []string `json:"sections,omitempty"`
}

// The choices that are not free text.
var (
	CornerChoices = []struct{ Key, Label, Value string }{{"sharp", "Sharp", "2px"}, {"soft", "Soft", "8px"}, {"round", "Round", "16px"}}
	DarkChoices   = []struct{ Key, Label string }{{"off", "Light only"}, {"toggle", "Visitors can switch to dark"}, {"auto", "Follow the visitor's device"}}
	HeaderChoices = []struct{ Key, Label string }{{content.HeaderInline, "Logo with the menu beside it"}, {content.HeaderCentered, "Menu bar on top, large centred logo below"}}
	TopBarChoices = []struct{ Key, Label string }{{"contact", "Phone number, social icons and search"}, {"search", "Just the search icon"}, {"none", "No top bar"}}
)

// Plan is what applying the answers will do, already checked. Nothing in it has been saved yet.
type Plan struct {
	Title       string
	Theme       string
	Tokens      map[string]string
	ColorMode   string
	ColorSwitch string
	Layout      content.Layout
	Brand       content.Brand
	Details     content.Details
	Consent     content.Consent
	Footer      footer.Footer
	TopItems    []menus.Item
	TopStyle    menus.Style
	MainItems   []menus.Item
	Element     *elements.Element // the notice bar, nil for none
	Blocks      []BlockSpec       // the home page sections
	Warnings    []string          // things worth a second look; none of them stops the wizard
}

// ErrProblem is a mistake in an answer, in words for the person.
type ErrProblem struct{ Msg string }

func (e ErrProblem) Error() string { return e.Msg }

func problem(format string, a ...any) error { return ErrProblem{fmt.Sprintf(format, a...)} }

// Build checks the answers against the site as it is now and works out the settings they make. It changes nothing.
func Build(a Answers, now content.Settings, eng *themes.Engine, existingMenus map[string]menus.Menu) (Plan, error) {
	var p Plan
	biz := Business{Kind: a.Business, Title: strings.TrimSpace(a.Title)}
	if biz.Kind != BusinessVet {
		biz.Kind = BusinessOther
	}
	if biz.Title == "" {
		biz.Title = now.Title
	}
	if n := len([]rune(biz.Title)); n == 0 || n > 100 || strings.ContainsAny(biz.Title, "\r\n") {
		return p, problem("Enter your website's name, up to 100 characters.")
	}
	p.Title = biz.Title

	// --- the look ---
	kit, ok := KitByID(a.Kit)
	if !ok {
		return p, problem("Choose a look for the website.")
	}
	theme, ok := eng.Lookup(kit.Theme)
	if !ok {
		return p, problem("The theme for this look is not installed.")
	}
	p.Theme = kit.Theme
	value := func(key string) string { // what the site will use for a token
		if v := p.Tokens[key]; v != "" {
			return v
		}
		for _, tk := range theme.Tokens {
			if tk.Key == key {
				return tk.Default
			}
		}
		return ""
	}
	p.Tokens = map[string]string{}
	for k, v := range kit.Tokens {
		p.Tokens[k] = v
	}
	hexOK := func(label, v string) (string, error) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			return "", nil
		}
		if !themes.ValidToken("color", v) {
			return "", problem("%s must look like #1a2b3c.", label)
		}
		return v, nil
	}
	primary, err := hexOK("The main colour", a.Primary)
	if err != nil {
		return p, err
	}
	bg, err := hexOK("The background colour", a.Background)
	if err != nil {
		return p, err
	}
	text, err := hexOK("The text colour", a.Text)
	if err != nil {
		return p, err
	}
	if primary != "" {
		p.Tokens["color-primary"], p.Tokens["color-primary-text"] = primary, readableOn(primary)
	}
	if bg != "" {
		p.Tokens["color-bg"] = bg
	}
	if text != "" {
		p.Tokens["color-text"] = text
	}
	if bg != "" || text != "" {
		// the quieter colours follow the page's own, so they never clash with what was chosen
		p.Tokens["color-surface"] = mix(value("color-bg"), value("color-text"), .06)
		p.Tokens["color-muted"] = mix(value("color-text"), value("color-bg"), .4)
	}
	for _, f := range []struct{ key, label, v string }{{"font-body", "The body font", a.FontBody}, {"font-heading", "The heading font", a.FontHeading}} {
		if f.v == "" {
			continue
		}
		if !themes.ValidToken("font", f.v) {
			return p, problem("%s is not one of the fonts offered.", f.label)
		}
		p.Tokens[f.key] = f.v
	}
	switch a.Corners {
	case "":
	default:
		found := false
		for _, c := range CornerChoices {
			if c.Key == a.Corners {
				p.Tokens["radius"], found = c.Value, true
			}
		}
		if !found {
			return p, problem("Choose sharp, soft or round corners.")
		}
	}
	// a value the theme already has is not stored, so a later improvement to the theme still reaches the site
	for _, tk := range theme.Tokens {
		if v, ok := p.Tokens[tk.Key]; ok && strings.EqualFold(v, tk.Default) {
			delete(p.Tokens, tk.Key)
		}
	}
	for key, v := range p.Tokens {
		var kind string
		for _, tk := range theme.Tokens {
			if tk.Key == key {
				kind = tk.Kind
			}
		}
		if kind == "" || !themes.ValidToken(kind, v) {
			return p, problem("The look has a setting the theme does not know (%s).", key)
		}
	}
	if c := Contrast(value("color-text"), value("color-bg")); c < 4.5 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("The text and background colours are hard to tell apart (contrast %.1f; 4.5 or more reads well).", c))
	}
	if c := Contrast(value("color-primary-text"), value("color-primary")); c < 4.5 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("Text on the main colour is hard to read (contrast %.1f).", c))
	}

	p.ColorMode = kit.ColorMode
	switch a.Dark {
	case "":
	case "off":
		p.ColorMode = "light"
	case "toggle", "auto":
		p.ColorMode = a.Dark
	default:
		return p, problem("Choose how dark mode should work.")
	}
	if !themes.ValidColorMode(p.ColorMode) {
		return p, problem("Choose how dark mode should work.")
	}
	p.ColorSwitch = now.ColorSwitch
	if !themes.ValidColorSwitch(p.ColorSwitch) {
		p.ColorSwitch = themes.SwitchFloating
	}

	header := kit.Header
	if a.Header != "" {
		if a.Header != content.HeaderInline && a.Header != content.HeaderCentered {
			return p, problem("Choose a header style.")
		}
		header = a.Header
	}
	p.Layout = now.Layout
	p.Layout.Header.Style = header
	if header == content.HeaderCentered {
		p.Layout.Header.BarBackground, p.Layout.Header.BarText = kit.TopBg, kit.TopFg
	} else {
		p.Layout.Header.BarBackground, p.Layout.Header.BarText, p.Layout.Header.BarActive = "", "", ""
	}

	// --- the brand and the contact details ---
	p.Brand = now.Brand
	if l := strings.TrimSpace(a.Logo); l != "" {
		if blocks.SafeImageURL(l) == "" {
			return p, problem("The logo must be a picture from the media library (such as /media/logo.png) or an https:// address.")
		}
		p.Brand.Logo = l
		p.Brand.LogoAlt = strings.Join(strings.Fields(a.LogoAlt), " ")
		if p.Brand.LogoAlt == "" {
			p.Brand.LogoAlt = biz.Title
		}
	}
	p.Details = now.Details
	set := func(dst *string, v string) { // a blank answer keeps what Site details already holds
		if v = strings.TrimSpace(v); v != "" {
			*dst = v
		}
	}
	set(&p.Details.Phone, a.Phone)
	set(&p.Details.WhatsApp, a.WhatsApp)
	set(&p.Details.Email, a.Email)
	set(&p.Details.Address, a.Address)
	set(&p.Details.Hours, a.Hours)
	if p.Details.Phone != "" && content.PhoneLink(p.Details.Phone) == "" {
		return p, problem("Enter a phone number with 5 to 15 digits, such as +27 21 555 0123.")
	}
	if p.Details.WhatsApp != "" && content.WhatsAppLink(p.Details.WhatsApp) == "" {
		return p, problem("Enter the WhatsApp number with its country code, such as +27 82 555 0123.")
	}
	if p.Details.Email != "" && !content.ValidEmail(p.Details.Email) {
		return p, problem("That does not look like an email address.")
	}
	social := map[string]string{}
	for k, v := range p.Details.Social {
		social[k] = v
	}
	for _, n := range content.Networks {
		if v := strings.TrimSpace(a.Social[n.Key]); v != "" {
			if content.SocialURL(n, v) == "" {
				return p, problem("Use the full https:// address of your %s page.", n.Label)
			}
			social[n.Key] = v
		}
	}
	p.Details.Social = social

	// --- menus ---
	p.MainItems = []menus.Item{{Kind: menus.KindLink, Label: "Home", URL: "/"}, {Kind: menus.KindLink, Label: "About", URL: "/about"}}
	if existing, ok := existingMenus["main"]; ok && len(existing.Items) > 0 && !isStarterMenu(existing.Items) {
		p.MainItems = existing.Items // the person has built their own: it is not the wizard's to replace
	}
	mainSec, _ := menus.SectionByKey("main")
	if p.MainItems, err = menus.Validate(mainSec, p.MainItems); err != nil {
		return p, problem("The main menu: %s", strings.TrimSuffix(err.Error(), "."))
	}
	switch a.TopBar {
	case "", "none":
	case "contact", "search":
		if a.TopBar == "contact" {
			if link := content.PhoneLink(p.Details.Phone); link != "" {
				p.TopItems = append(p.TopItems, menus.Item{Kind: menus.KindLink, Label: "Call us: " + strings.TrimSpace(p.Details.Phone), URL: link, Side: "left"})
			}
			if len(p.Details.Social) > 0 {
				p.TopItems = append(p.TopItems, menus.Item{Kind: menus.KindSocial, Label: "Social links", Side: "right"})
			}
		}
		p.TopItems = append(p.TopItems, menus.Item{Kind: menus.KindSearch, Label: "Search", Side: "right"})
	default:
		return p, problem("Choose what the top bar shows.")
	}
	topSec, _ := menus.SectionByKey("topbar")
	if p.TopItems, err = menus.Validate(topSec, p.TopItems); err != nil {
		return p, problem("The top bar: %s", strings.TrimSuffix(err.Error(), "."))
	}
	if p.TopStyle, err = menus.ValidateStyle(menus.Style{Background: kit.TopBg, Text: kit.TopFg, Size: "sm"}); err != nil {
		return p, err
	}

	// --- footer ---
	if p.Footer, err = buildFooter(a, p.Details, kit); err != nil {
		return p, err
	}

	// --- extras ---
	p.Consent = now.Consent
	p.Consent.Enabled = a.Cookie
	if t := strings.TrimSpace(a.Announcement); t != "" {
		e, err := elements.Clean(elements.Element{Kind: elements.KindBar, Name: "Set-up notice", Enabled: true,
			Config: elements.Config{Text: t, Style: elements.StyleInfo, Dismissible: true, DismissDays: 7}}, time.Now())
		if err != nil {
			return p, problem("The notice: %s", strings.TrimSuffix(err.Error(), "."))
		}
		p.Element = &e
	}

	// --- home page sections ---
	if p.Blocks, err = Blocks(a.Sections, biz); err != nil {
		return p, ErrProblem{err.Error()}
	}
	for i := range p.Blocks {
		t, ok := blocks.Get(p.Blocks[i].Type)
		if !ok {
			return p, errors.New("wizard: unknown block type " + p.Blocks[i].Type)
		}
		p.Blocks[i].Props = t.Clean(p.Blocks[i].Props)
		p.Blocks[i].Style = blocks.CleanStyleMap(p.Blocks[i].Style)
	}
	return p, nil
}

// isStarterMenu says whether a main menu is still the one a new website starts with (Home and About), which the wizard may rewrite.
func isStarterMenu(items []menus.Item) bool {
	if len(items) == 0 || len(items) > 2 {
		return len(items) == 0
	}
	want := []string{"Home", "About"}
	for i, it := range items {
		if it.Kind != menus.KindLink || it.Label != want[i] {
			return false
		}
	}
	return true
}

func buildFooter(a Answers, d content.Details, kit Kit) (footer.Footer, error) {
	f := footer.Empty()
	f.Bottom = footer.DefaultBottom
	f.Style = footer.Style{Background: kit.FootBg, Text: kit.FootFg, Padding: "md"}
	var parts []string
	for _, p := range []struct{ key, v string }{{"phone", d.Phone}, {"whatsapp", d.WhatsApp}, {"email", d.Email}, {"address", d.Address}, {"hours", d.Hours}} {
		if p.v != "" {
			parts = append(parts, p.key)
		}
	}
	if len(d.Social) > 0 {
		parts = append(parts, "social")
	}
	cols := []footer.Column{
		{Widgets: []footer.Widget{{Type: footer.TypeLogo}}},
		{Widgets: []footer.Widget{{Type: footer.TypeLinks, Heading: "Quick links", Links: []footer.Link{{Label: "Home", URL: "/"}, {Label: "About", URL: "/about"}}}}},
	}
	if len(parts) > 0 {
		cols = append(cols, footer.Column{Widgets: []footer.Widget{{Type: footer.TypeContact, Heading: "Contact us", Parts: parts}}})
	}
	f.Rows = []footer.Row{{Ratio: "equal", Align: "left", Columns: cols}}
	if a.Newsletter {
		f.Rows = append(f.Rows, footer.Row{Ratio: "equal", Align: "center", Columns: []footer.Column{{Widgets: []footer.Widget{
			{Type: footer.TypeNewsletter, Heading: "Stay in the loop", Text: "Get our news by email. You can unsubscribe at any time."}}}}})
	}
	out, err := footer.Validate(f)
	if err != nil {
		return footer.Footer{}, problem("The footer: %s", strings.TrimSuffix(err.Error(), "."))
	}
	return out, nil
}
