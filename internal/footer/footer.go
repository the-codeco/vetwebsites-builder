// Package footer is the data model and validation for the site footer the owner
// builds: rows of 1–4 columns, each column a list of widgets (text, a list of
// links, contact details, social links, the logo, a newsletter signup), above a
// bottom line, with the footer's own colours and spacing.
package footer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"webbuilder/internal/blocks"
	"webbuilder/internal/menus"
)

// Version marks a footer saved in this shape. Older footers (plain columns of text,
// with the footer menu kept apart) are upgraded by Upgrade.
const Version = 2

// Widget types.
const (
	TypeText       = "text"
	TypeLinks      = "links"
	TypeContact    = "contact"
	TypeSocial     = "social"
	TypeLogo       = "logo"
	TypeNewsletter = "newsletter"
)

// Types lists the widgets in the order the builder offers them.
var Types = []struct{ Name, Label string }{
	{TypeText, "Text"},
	{TypeLinks, "Links"},
	{TypeContact, "Contact details"},
	{TypeSocial, "Social links"},
	{TypeLogo, "Logo"},
	{TypeNewsletter, "Newsletter signup"},
}

func typeLabel(t string) string {
	for _, x := range Types {
		if x.Name == t {
			return x.Label
		}
	}
	return ""
}

// ContactParts are the site details a contact widget can show, in the order shown.
var ContactParts = []struct{ Key, Label string }{
	{"phone", "Phone"}, {"whatsapp", "WhatsApp"}, {"email", "Email"},
	{"address", "Address"}, {"hours", "Opening hours"}, {"social", "Social links"},
}

// Layout choices for a row.
var (
	Ratios = []struct{ Value, Label string }{
		{"equal", "All equal"}, {"wide-left", "First column wider"},
		{"wide-middle", "Middle column wider (3 columns)"}, {"wide-right", "Last column wider"},
	}
	Aligns   = []struct{ Value, Label string }{{"left", "Left"}, {"center", "Centre"}, {"right", "Right"}}
	Paddings = []struct{ Value, Label string }{{"sm", "Small"}, {"md", "Medium"}, {"lg", "Large"}}
)

// Limits keep the footer usable and requests bounded.
const (
	MaxRows          = 4
	MaxColumns       = 4
	MaxWidgets       = 6 // per column
	MaxLinks         = 20
	MaxHeading       = 60
	MaxLabel         = 60
	MaxText          = 5000
	MaxShort         = 300 // a newsletter intro, button or thank-you line, a logo's tagline
	MaxBottom        = 300
	MaxJSON          = 256 << 10
	DefaultBottom    = "© {year} {title}"
	defaultPadding   = "md"
	defaultAlignment = "left"
)

// Link is one link in a links widget.
type Link struct {
	Label  string `json:"label"`
	URL    string `json:"url"`
	NewTab bool   `json:"new_tab,omitempty"`
}

// Widget is one piece of footer content. Which fields matter depends on Type:
//
//	text        Heading, Text (basic HTML, cleaned like a block's rich text)
//	links       Heading, Links, Inline (in a row instead of a list)
//	contact     Heading, Parts (which site details to show)
//	social      Heading (the social links from Site details)
//	logo        Text (a short tagline under the logo), Logo ("alternative" for the alternative logo)
//	newsletter  Heading, Text (introduction), Button, Success
type Widget struct {
	Type    string   `json:"type"`
	Heading string   `json:"heading,omitempty"`
	Text    string   `json:"text,omitempty"`
	Links   []Link   `json:"links,omitempty"`
	Inline  bool     `json:"inline,omitempty"`
	Parts   []string `json:"parts,omitempty"`
	Button  string   `json:"button,omitempty"`
	Success string   `json:"success,omitempty"`
	Logo    string   `json:"logo,omitempty"` // logo widgets: "" (the main logo) or LogoAlternative
}

// LogoAlternative picks the alternative logo (Appearance → Logo) in a logo widget.
const LogoAlternative = "alternative"

// Column is one column of a row.
type Column struct {
	Widgets []Widget `json:"widgets"`
}

// Row is a band of the footer: its columns, how wide they are, and how their content lines up.
type Row struct {
	Ratio   string   `json:"ratio,omitempty"`
	Align   string   `json:"align,omitempty"`
	Columns []Column `json:"columns"`
}

// Style is the footer's own look. Empty colours mean the theme's.
type Style struct {
	Background string `json:"background,omitempty"`
	Text       string `json:"text,omitempty"`
	Padding    string `json:"padding,omitempty"`
}

// Footer is the whole footer.
type Footer struct {
	Version int    `json:"version"`
	Rows    []Row  `json:"rows"`
	Bottom  string `json:"bottom,omitempty"` // "" means DefaultBottom
	Style   Style  `json:"style"`
}

// BottomLine is the text to show, with {year} and {title} filled in.
func (f Footer) BottomLine(title string, year int) string {
	b := f.Bottom
	if b == "" {
		b = DefaultBottom
	}
	return strings.NewReplacer("{year}", strconv.Itoa(year), "{title}", title).Replace(b)
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Classes are the footer element's extra classes (each with a leading space).
func (s Style) Classes() string {
	c := " footer-pad-" + s.Padding
	if s.Background != "" {
		c += " has-bg"
	}
	if s.Text != "" {
		c += " has-fg"
	}
	return c
}

// CSSVars are the footer's validated colours as custom properties.
func (s Style) CSSVars() template.CSS {
	var b strings.Builder
	if colorRe.MatchString(s.Background) {
		b.WriteString("--footer-bg:" + s.Background + ";")
	}
	if colorRe.MatchString(s.Text) {
		b.WriteString("--footer-fg:" + s.Text + ";")
	}
	return template.CSS(b.String())
}

// Decode reads the builder's JSON. It only checks the shape (and size); use Validate for the rules.
func Decode(data []byte) (Footer, error) {
	if len(data) > MaxJSON {
		return Footer{}, errors.New("The footer is too large.")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f Footer
	if err := dec.Decode(&f); err != nil {
		return Footer{}, errors.New("The footer couldn't be read. Reload the page and try again.")
	}
	return f, nil
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

func tooLong(s string, n int) bool { return utf8.RuneCountInString(s) > n }

func oneOf[T any](v string, opts []T, value func(T) string, def string) string {
	for _, o := range opts {
		if value(o) == v {
			return v
		}
	}
	return def
}

// Validate applies every rule and returns the footer ready to store and render. Errors are
// written for the person building the footer. Empty widgets, columns and rows are kept
// where they hold the layout (a row keeps its columns) but empty rows are dropped.
func Validate(in Footer) (Footer, error) {
	out := Footer{Version: Version, Bottom: clean(in.Bottom)}
	if tooLong(out.Bottom, MaxBottom) {
		return Footer{}, fmt.Errorf("The bottom line can be at most %d characters.", MaxBottom)
	}
	out.Style = Style{
		Background: strings.ToLower(strings.TrimSpace(in.Style.Background)),
		Text:       strings.ToLower(strings.TrimSpace(in.Style.Text)),
		Padding:    oneOf(in.Style.Padding, Paddings, func(o struct{ Value, Label string }) string { return o.Value }, defaultPadding),
	}
	for _, c := range []*string{&out.Style.Background, &out.Style.Text} {
		if *c != "" && !colorRe.MatchString(*c) {
			return Footer{}, errors.New("Footer colours must look like #1a2b3c, or be left empty for the theme's.")
		}
	}

	for ri, r := range in.Rows {
		pos := fmt.Sprintf("Row %d", ri+1)
		if len(r.Columns) == 0 {
			continue
		}
		if len(r.Columns) > MaxColumns {
			return Footer{}, fmt.Errorf("%s can have at most %d columns.", pos, MaxColumns)
		}
		row := Row{
			Ratio: oneOf(r.Ratio, Ratios, func(o struct{ Value, Label string }) string { return o.Value }, "equal"),
			Align: oneOf(r.Align, Aligns, func(o struct{ Value, Label string }) string { return o.Value }, defaultAlignment),
		}
		if len(r.Columns) == 1 || (row.Ratio == "wide-middle" && len(r.Columns) != 3) {
			row.Ratio = "equal"
		}
		filled := false
		for ci, c := range r.Columns {
			if len(c.Widgets) > MaxWidgets {
				return Footer{}, fmt.Errorf("%s, column %d can hold at most %d widgets.", pos, ci+1, MaxWidgets)
			}
			col := Column{Widgets: []Widget{}}
			for _, w := range c.Widgets {
				cw, err := validateWidget(w, fmt.Sprintf("%s, column %d", pos, ci+1))
				if err != nil {
					return Footer{}, err
				}
				col.Widgets = append(col.Widgets, cw)
			}
			filled = filled || len(col.Widgets) > 0
			row.Columns = append(row.Columns, col)
		}
		if !filled {
			continue // a row with nothing in it
		}
		out.Rows = append(out.Rows, row)
	}
	if len(out.Rows) > MaxRows {
		return Footer{}, fmt.Errorf("The footer can have at most %d rows.", MaxRows)
	}
	if out.Rows == nil {
		out.Rows = []Row{}
	}
	return out, nil
}

func validateWidget(w Widget, where string) (Widget, error) {
	label := typeLabel(w.Type)
	if label == "" {
		return Widget{}, fmt.Errorf("%s has a widget of an unknown type.", where)
	}
	where = fmt.Sprintf("%s (%s)", where, strings.ToLower(label))
	out := Widget{Type: w.Type}
	heading := clean(w.Heading)
	if tooLong(heading, MaxHeading) {
		return Widget{}, fmt.Errorf("%s: headings can be at most %d characters.", where, MaxHeading)
	}
	short := func(s, what string) (string, error) {
		s = clean(s)
		if tooLong(s, MaxShort) {
			return "", fmt.Errorf("%s: the %s can be at most %d characters.", where, what, MaxShort)
		}
		return s, nil
	}
	var err error
	switch w.Type {
	case TypeText:
		if tooLong(w.Text, MaxText) {
			return Widget{}, fmt.Errorf("%s: the text can be at most %d characters.", where, MaxText)
		}
		out.Heading, out.Text = heading, blocks.NormalizeRichText(w.Text)
	case TypeLinks:
		out.Heading, out.Inline = heading, w.Inline
		for _, l := range w.Links {
			l.Label, l.URL = clean(l.Label), strings.TrimSpace(l.URL)
			if l.Label == "" && l.URL == "" {
				continue // the builder's empty "add link" row
			}
			switch {
			case l.Label == "":
				return Widget{}, fmt.Errorf("%s: every link needs a label.", where)
			case tooLong(l.Label, MaxLabel):
				return Widget{}, fmt.Errorf("%s: link labels can be at most %d characters (“%.20s…”).", where, MaxLabel, l.Label)
			}
			u := blocks.SafeURL(l.URL)
			if u == "" {
				return Widget{}, fmt.Errorf("“%s” needs a valid address: a path like /about, or an http(s)://, mailto: or tel: address.", l.Label)
			}
			out.Links = append(out.Links, Link{Label: l.Label, URL: u, NewTab: l.NewTab})
		}
		if len(out.Links) > MaxLinks {
			return Widget{}, fmt.Errorf("%s: a links widget can have at most %d links.", where, MaxLinks)
		}
	case TypeContact:
		out.Heading = heading
		want := map[string]bool{}
		for _, p := range w.Parts {
			want[p] = true
		}
		for _, p := range ContactParts { // known parts only, in the usual order
			if want[p.Key] {
				out.Parts = append(out.Parts, p.Key)
			}
		}
	case TypeSocial:
		out.Heading = heading
	case TypeLogo:
		if out.Text, err = short(w.Text, "tagline"); err != nil {
			return Widget{}, err
		}
		if w.Logo == LogoAlternative {
			out.Logo = LogoAlternative
		}
	case TypeNewsletter:
		out.Heading = heading
		if out.Text, err = short(w.Text, "introduction"); err != nil {
			return Widget{}, err
		}
		if out.Button, err = short(w.Button, "button label"); err != nil {
			return Widget{}, err
		}
		if out.Success, err = short(w.Success, "thank-you message"); err != nil {
			return Widget{}, err
		}
	}
	return out, nil
}

// FromStored reads a footer saved in the current shape (Version 2). Stored and imported data is
// trimmed to fit rather than refused (see trim); a footer that can't be read at all is empty.
func FromStored(m map[string]any) Footer {
	data, err := json.Marshal(m)
	if err != nil {
		return Empty()
	}
	var f Footer
	if err := json.Unmarshal(data, &f); err != nil {
		return Empty()
	}
	out, err := Validate(trim(f))
	if err != nil {
		return Empty()
	}
	return out
}

func cut(s string, n int) string {
	s = clean(s)
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n])
	}
	return s
}

// trim makes any footer pass Validate by cutting it down instead of failing: text is shortened,
// extra rows, columns, widgets and links are left off, and anything unsafe or unknown (a widget
// type, a link address, a colour) is dropped.
func trim(f Footer) Footer {
	f.Bottom = cut(f.Bottom, MaxBottom)
	for _, c := range []*string{&f.Style.Background, &f.Style.Text} {
		if !colorRe.MatchString(strings.TrimSpace(*c)) {
			*c = ""
		}
	}
	if len(f.Rows) > MaxRows {
		f.Rows = f.Rows[:MaxRows]
	}
	for ri := range f.Rows {
		r := &f.Rows[ri]
		if len(r.Columns) > MaxColumns {
			r.Columns = r.Columns[:MaxColumns]
		}
		for ci := range r.Columns {
			var ws []Widget
			for _, w := range r.Columns[ci].Widgets {
				if typeLabel(w.Type) == "" || len(ws) == MaxWidgets {
					continue
				}
				w.Heading = cut(w.Heading, MaxHeading)
				if w.Type == TypeText {
					if utf8.RuneCountInString(w.Text) > MaxText {
						w.Text = string([]rune(w.Text)[:MaxText])
					}
				} else {
					w.Text = cut(w.Text, MaxShort)
				}
				w.Button, w.Success = cut(w.Button, MaxShort), cut(w.Success, MaxShort)
				var links []Link
				for _, l := range w.Links {
					l.Label = cut(l.Label, MaxLabel)
					if l.Label != "" && blocks.SafeURL(l.URL) != "" && len(links) < MaxLinks {
						links = append(links, l)
					}
				}
				w.Links = links
				ws = append(ws, w)
			}
			r.Columns[ci].Widgets = ws
		}
	}
	return f
}

// Empty is a footer with nothing but the default bottom line.
func Empty() Footer {
	return Footer{Version: Version, Rows: []Row{}, Style: Style{Padding: defaultPadding}}
}

// IsCurrent says whether stored footer JSON is already in this shape.
func IsCurrent(m map[string]any) bool {
	v, _ := m["version"].(float64)
	return v >= Version
}

// AsMap is the footer as stored JSON.
func (f Footer) AsMap() map[string]any {
	data, _ := json.Marshal(f)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

// Upgrade turns a footer saved before the builder — {columns: [{heading, text}], bottom} — into
// rows, together with the two things that lived elsewhere: the footer menu (its visible links
// become a row of links at the top) and the "show contact details in the footer" setting (a
// contact column). Columns go four to a row.
func Upgrade(legacy map[string]any, footerMenu []menus.Item, contactInFooter bool) Footer {
	f := Empty()
	f.Bottom, _ = legacy["bottom"].(string)

	var links []Link
	for _, it := range menus.Visible(footerMenu) {
		if it.Kind == menus.KindLink || it.Kind == "" {
			links = append(links, Link{Label: it.Label, URL: it.URL, NewTab: it.NewTab})
		}
	}
	if len(links) > MaxLinks {
		links = links[:MaxLinks]
	}
	if len(links) > 0 {
		f.Rows = append(f.Rows, Row{Ratio: "equal", Align: "center", Columns: []Column{{Widgets: []Widget{{Type: TypeLinks, Links: links, Inline: true}}}}})
	}

	var cols []Column
	raw, _ := legacy["columns"].([]any)
	for _, e := range raw {
		if len(cols) == MaxColumns { // the old footer showed at most four
			break
		}
		cm, _ := e.(map[string]any)
		heading, _ := cm["heading"].(string)
		text, _ := cm["text"].(string)
		if clean(heading) == "" && blocks.PlainText(text) == "" {
			continue
		}
		cols = append(cols, Column{Widgets: []Widget{{Type: TypeText, Heading: heading, Text: text}}})
	}
	if contactInFooter {
		var parts []string
		for _, p := range ContactParts {
			parts = append(parts, p.Key)
		}
		cols = append(cols, Column{Widgets: []Widget{{Type: TypeContact, Heading: "Contact", Parts: parts}}})
	}
	for len(cols) > 0 && len(f.Rows) < MaxRows {
		n := min(len(cols), MaxColumns)
		f.Rows = append(f.Rows, Row{Ratio: "equal", Align: "left", Columns: cols[:n]})
		cols = cols[n:]
	}

	out, err := Validate(trim(f))
	if err != nil {
		return Empty()
	}
	return out
}
