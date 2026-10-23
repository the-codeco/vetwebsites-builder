// Package menus is the data model and validation for the site's navigation:
// three sections (top bar, main navigation, footer), each a list of items that
// are plain links, simple dropdowns or mega menus (columns of links with headings).
package menus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"webbuilder/internal/blocks"
)

const (
	KindLink     = "link"
	KindDropdown = "dropdown"
	KindMega     = "mega"
	// KindSocial shows the site's social network icons (from Site details); KindSearch shows a search icon that opens
	// the search popup. Neither has a link of its own.
	KindSocial = "social"
	KindSearch = "search"
)

// Limits keep menus usable and requests bounded.
const (
	MaxLabel       = 60
	MaxDescription = 120
	MaxColumns     = 4
	MaxColumnLinks = 8
	MaxChildren    = 12
	MaxJSON        = 128 << 10
)

// Link is one destination. Description is only used inside mega menus.
type Link struct {
	Label       string `json:"label"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	NewTab      bool   `json:"new_tab,omitempty"`
	Hidden      bool   `json:"hidden,omitempty"`
}

// Column is one column of a mega menu.
type Column struct {
	Heading string `json:"heading,omitempty"`
	Links   []Link `json:"links"`
}

// Item is a top-level entry. A link has a URL; a dropdown has Children; a mega
// menu has Columns. Dropdowns and mega menus are buttons that open a panel, so
// they have no URL of their own.
type Item struct {
	Kind     string   `json:"kind"`
	Label    string   `json:"label"`
	URL      string   `json:"url,omitempty"`
	NewTab   bool     `json:"new_tab,omitempty"`
	Hidden   bool     `json:"hidden,omitempty"`
	Button   bool     `json:"button,omitempty"` // a plain link drawn as a button ("Book now")
	Side     string   `json:"side,omitempty"`   // top bar only: "left" or "right" (empty: right)
	Children []Link   `json:"children,omitempty"`
	Columns  []Column `json:"columns,omitempty"`
}

// Style is the look of a menu section (the top bar uses it): background and text colours as #rrggbb, and a size.
// Anything empty keeps the theme's own look.
type Style struct {
	Background string `json:"background,omitempty"`
	Text       string `json:"text,omitempty"`
	Size       string `json:"size,omitempty"` // "sm", "md" or "lg"
}

// Menu is one saved section.
type Menu struct {
	Items []Item
	// Sticky ("frozen") keeps the section on screen while the page scrolls.
	Sticky bool
	Style  Style
}

// Section describes one of the three menus.
type Section struct {
	Key, Label, Help string
	// Nested says whether dropdowns and mega menus are allowed here.
	Nested bool
	// Icons says whether the social icons and the search icon may be placed here.
	Icons bool
	// Sides says whether each item can be put on the left or the right.
	Sides bool
	// Styled says whether the section has colours and a size of its own.
	Styled   bool
	MaxItems int
}

var Sections = []Section{
	{Key: "topbar", Label: "Top bar", Icons: true, Sides: true, Styled: true, MaxItems: 10,
		Help: "A slim bar above the header. Put links (a phone number, a login), your social icons or the search icon on its left or its right."},
	{Key: "main", Label: "Main navigation", Nested: true, Icons: true, MaxItems: 12,
		Help: "The site's main menu. Items can be plain links, simple dropdowns, mega menus with columns of links, or the search icon."},
	{Key: "footer", Label: "Footer", MaxItems: 12,
		Help: "Links shown at the bottom of every page. Links only."},
}

func SectionByKey(key string) (Section, bool) {
	for _, s := range Sections {
		if s.Key == key {
			return s, true
		}
	}
	return Section{}, false
}

// Decode reads the editor's JSON. It only checks the shape (and size); use
// Validate for the rules.
func Decode(data []byte) ([]Item, error) {
	if len(data) > MaxJSON {
		return nil, errors.New("The menu is too large.")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var items []Item
	if err := dec.Decode(&items); err != nil {
		return nil, errors.New("The menu couldn't be read. Reload the page and try again.")
	}
	return items, nil
}

func clean(s string) string { return strings.TrimSpace(s) }

func tooLong(s string, n int) bool { return utf8.RuneCountInString(s) > n }

func blankLink(l Link) bool {
	return clean(l.Label) == "" && clean(l.URL) == "" && clean(l.Description) == ""
}

// validateLinks checks a list of links. Fully blank rows (the editor's empty
// "add link" rows) are dropped silently; anything half-filled is an error.
func validateLinks(owner string, in []Link, max int, allowDescription bool) ([]Link, error) {
	var out []Link
	for _, l := range in {
		if blankLink(l) {
			continue
		}
		l.Label, l.URL, l.Description = clean(l.Label), clean(l.URL), clean(l.Description)
		switch {
		case l.Label == "":
			return nil, fmt.Errorf("Every link in “%s” needs a label.", owner)
		case tooLong(l.Label, MaxLabel):
			return nil, fmt.Errorf("Link labels can be at most %d characters (“%.20s…”).", MaxLabel, l.Label)
		case tooLong(l.Description, MaxDescription):
			return nil, fmt.Errorf("Link descriptions can be at most %d characters (“%s”).", MaxDescription, l.Label)
		}
		u := blocks.SafeURL(l.URL)
		if u == "" {
			return nil, fmt.Errorf("“%s” needs a valid address: a path like /about, or an http(s)://, mailto: or tel: address.", l.Label)
		}
		l.URL = u
		if !allowDescription {
			l.Description = ""
		}
		out = append(out, l)
	}
	if len(out) > max {
		return nil, fmt.Errorf("“%s” can have at most %d links.", owner, max)
	}
	return out, nil
}

// Validate applies every rule for sec and returns the cleaned items, ready to
// store and render. Errors are written for the person editing the menu.
func Validate(sec Section, in []Item) ([]Item, error) {
	var out []Item
	for i, it := range in {
		it.Label, it.URL = clean(it.Label), clean(it.URL)
		if it.Kind == "" {
			it.Kind = KindLink
		}
		if it.Kind == KindSocial || it.Kind == KindSearch {
			// icons have no address; the label is only what a screen reader says
			if !sec.Icons {
				return nil, fmt.Errorf("The %s can only contain plain links.", strings.ToLower(sec.Label))
			}
			if it.Label == "" {
				it.Label = map[string]string{KindSocial: "Social media", KindSearch: "Search"}[it.Kind]
			}
		}
		if sec.Sides && (it.Side == "left" || it.Side == "right") {
			it.Side = clean(it.Side)
		} else {
			it.Side = ""
		}
		blank := it.Label == "" && it.URL == ""
		for _, l := range it.Children {
			blank = blank && blankLink(l)
		}
		for _, c := range it.Columns {
			blank = blank && clean(c.Heading) == ""
			for _, l := range c.Links {
				blank = blank && blankLink(l)
			}
		}
		if blank {
			continue
		}
		pos := fmt.Sprintf("Item %d", i+1)
		if it.Label == "" {
			return nil, fmt.Errorf("%s needs a label.", pos)
		}
		if tooLong(it.Label, MaxLabel) {
			return nil, fmt.Errorf("Labels can be at most %d characters (“%.20s…”).", MaxLabel, it.Label)
		}

		switch it.Kind {
		case KindLink:
			u := blocks.SafeURL(it.URL)
			if u == "" {
				return nil, fmt.Errorf("“%s” needs a valid address: a path like /about, or an http(s)://, mailto: or tel: address.", it.Label)
			}
			it.URL, it.Children, it.Columns = u, nil, nil

		case KindDropdown:
			if !sec.Nested {
				return nil, fmt.Errorf("The %s can only contain plain links.", strings.ToLower(sec.Label))
			}
			kids, err := validateLinks(it.Label, it.Children, MaxChildren, false)
			if err != nil {
				return nil, err
			}
			it.URL, it.NewTab, it.Button, it.Children, it.Columns = "", false, false, kids, nil

		case KindMega:
			if !sec.Nested {
				return nil, fmt.Errorf("The %s can only contain plain links.", strings.ToLower(sec.Label))
			}
			var cols []Column
			for _, c := range it.Columns {
				c.Heading = clean(c.Heading)
				if tooLong(c.Heading, MaxLabel) {
					return nil, fmt.Errorf("Column headings can be at most %d characters (“%.20s…”).", MaxLabel, c.Heading)
				}
				links, err := validateLinks(it.Label, c.Links, MaxColumnLinks, true)
				if err != nil {
					return nil, err
				}
				if c.Heading == "" && len(links) == 0 {
					continue // an empty column
				}
				cols = append(cols, Column{Heading: c.Heading, Links: links})
			}
			if len(cols) > MaxColumns {
				return nil, fmt.Errorf("“%s” can have at most %d columns.", it.Label, MaxColumns)
			}
			it.URL, it.NewTab, it.Button, it.Children, it.Columns = "", false, false, nil, cols

		case KindSocial, KindSearch:
			it.URL, it.NewTab, it.Button, it.Children, it.Columns = "", false, false, nil, nil

		default:
			return nil, fmt.Errorf("“%s” has an unknown type.", it.Label)
		}
		out = append(out, it)
	}
	if len(out) > sec.MaxItems {
		return nil, fmt.Errorf("The %s can have at most %d items.", strings.ToLower(sec.Label), sec.MaxItems)
	}
	return out, nil
}

// Visible returns what visitors should see: hidden items and links removed, and
// dropdowns/mega menus left with nothing to show dropped. The input isn't modified.
func Visible(in []Item) []Item {
	visibleLinks := func(ls []Link) []Link {
		var out []Link
		for _, l := range ls {
			if !l.Hidden {
				out = append(out, l)
			}
		}
		return out
	}
	var out []Item
	for _, it := range in {
		if it.Hidden {
			continue
		}
		switch it.Kind {
		case KindDropdown:
			it.Children = visibleLinks(it.Children)
			if len(it.Children) == 0 {
				continue
			}
		case KindMega:
			var cols []Column
			for _, c := range it.Columns {
				if links := visibleLinks(c.Links); len(links) > 0 {
					cols = append(cols, Column{Heading: c.Heading, Links: links})
				}
			}
			if len(cols) == 0 {
				continue
			}
			it.Columns = cols
		}
		out = append(out, it)
	}
	return out
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidateStyle checks a section's look: colours must be #rrggbb (or empty, to keep the theme's), the size one of
// sm, md or lg (or empty, which is medium). The error is written for the person editing the menu.
func ValidateStyle(s Style) (Style, error) {
	s.Background, s.Text, s.Size = clean(s.Background), clean(s.Text), clean(s.Size)
	if s.Background != "" && !hexColor.MatchString(s.Background) {
		return Style{}, errors.New("The background colour must look like #1a2b3c.")
	}
	if s.Text != "" && !hexColor.MatchString(s.Text) {
		return Style{}, errors.New("The text colour must look like #1a2b3c.")
	}
	switch s.Size {
	case "", "sm", "md", "lg":
	default:
		return Style{}, errors.New("The size must be small, medium or large.")
	}
	s.Background, s.Text = strings.ToLower(s.Background), strings.ToLower(s.Text)
	if s.Size == "md" {
		s.Size = "" // medium is the default
	}
	return s, nil
}

// CleanStyle is ValidateStyle for data that was stored or restored: what is not valid is dropped, not reported.
func CleanStyle(s Style) Style {
	if out, err := ValidateStyle(s); err == nil {
		return out
	}
	out := Style{}
	if v, err := ValidateStyle(Style{Background: s.Background}); err == nil {
		out.Background = v.Background
	}
	if v, err := ValidateStyle(Style{Text: s.Text}); err == nil {
		out.Text = v.Text
	}
	if v, err := ValidateStyle(Style{Size: s.Size}); err == nil {
		out.Size = v.Size
	}
	return out
}
