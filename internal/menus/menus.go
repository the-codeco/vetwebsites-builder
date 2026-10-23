// Package menus is the data model and validation for the site's navigation:
// two sections (top bar, main navigation), each a list of items that
// are plain links, simple dropdowns or mega menus (columns, or "sections", each a
// list of links, a picture or some text, under an optional heading that can itself
// be a link).
package menus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"webbuilder/internal/blocks"
)

const (
	KindLink     = "link"
	KindDropdown = "dropdown"
	KindMega     = "mega"
)

// Mega menu section types. A section saved before types existed is a list of links.
const (
	SectionLinks = "links"
	SectionImage = "image"
	SectionText  = "text"
)

// Limits keep menus usable and requests bounded.
const (
	MaxLabel       = 60
	MaxDescription = 120
	MaxColumns     = 6
	MaxColumnLinks = 8
	MaxChildren    = 12
	MaxSectionText = 5000 // a text section's rich text
	MaxCaption     = 300
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

// Column is one column ("section") of a mega menu. A heading with a URL is a link (and, in an
// image section, so is the picture). Which other fields are used depends on Type:
//
//	links  Links
//	image  Image, Alt, Caption
//	text   Text (basic HTML, cleaned like a block's rich text)
type Column struct {
	Type    string `json:"type,omitempty"` // "" means links
	Heading string `json:"heading,omitempty"`
	URL     string `json:"url,omitempty"`
	Links   []Link `json:"links"`
	Image   string `json:"image,omitempty"`
	Alt     string `json:"alt,omitempty"`
	Caption string `json:"caption,omitempty"`
	Text    string `json:"text,omitempty"`
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
	Children []Link   `json:"children,omitempty"`
	Columns  []Column `json:"columns,omitempty"`
}

// Menu is one saved section.
type Menu struct {
	Items []Item
	// Sticky ("frozen") keeps the section on screen while the page scrolls.
	Sticky bool
}

// Section describes one of the three menus.
type Section struct {
	Key, Label, Help string
	// Nested says whether dropdowns and mega menus are allowed here.
	Nested   bool
	MaxItems int
}

var Sections = []Section{
	{Key: "topbar", Label: "Top bar", MaxItems: 8,
		Help: "A slim bar above the header: phone number, login link, language… Links only."},
	{Key: "main", Label: "Main navigation", Nested: true, MaxItems: 12,
		Help: "The site's main menu. Items can be plain links, simple dropdowns, or mega menus with columns of links."},
}

// FooterKey is where the footer menu was kept before links moved into the footer builder
// (package footer). Old data and backups may still carry it; it is upgraded, never shown.
const FooterKey = "footer"

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
		blank := it.Label == "" && it.URL == ""
		for _, l := range it.Children {
			blank = blank && blankLink(l)
		}
		for _, c := range it.Columns {
			blank = blank && clean(c.Heading) == "" && clean(c.URL) == "" && clean(c.Image) == "" && blocks.PlainText(c.Text) == ""
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
				c.Heading, c.URL = clean(c.Heading), clean(c.URL)
				if tooLong(c.Heading, MaxLabel) {
					return nil, fmt.Errorf("Column headings can be at most %d characters (“%.20s…”).", MaxLabel, c.Heading)
				}
				if c.URL != "" {
					if c.Heading == "" && c.Type != SectionImage { // an image section's picture can be the link on its own
						return nil, fmt.Errorf("A column in “%s” has an address but no heading to link.", it.Label)
					}
					u := blocks.SafeURL(c.URL)
					if u == "" {
						return nil, fmt.Errorf("The heading “%s” needs a valid address: a path like /about, or an http(s)://, mailto: or tel: address.", c.Heading)
					}
					c.URL = u
				}
				col := Column{Type: c.Type, Heading: c.Heading, URL: c.URL}
				switch c.Type {
				case "", SectionLinks:
					links, err := validateLinks(it.Label, c.Links, MaxColumnLinks, true)
					if err != nil {
						return nil, err
					}
					if c.Heading == "" && len(links) == 0 {
						continue // an empty column
					}
					col.Type, col.Links = SectionLinks, links
				case SectionImage:
					col.Alt, col.Caption = clean(c.Alt), clean(c.Caption)
					if clean(c.Image) == "" && c.Heading == "" && col.Caption == "" {
						continue // an empty section
					}
					if col.Image = blocks.SafeImageURL(clean(c.Image)); col.Image == "" {
						return nil, fmt.Errorf("An image section in “%s” needs a picture: choose one from the library, or give a /media/… or https:// address.", it.Label)
					}
					switch {
					case tooLong(col.Alt, MaxLabel*2):
						return nil, fmt.Errorf("Image descriptions can be at most %d characters (“%s”).", MaxLabel*2, it.Label)
					case tooLong(col.Caption, MaxCaption):
						return nil, fmt.Errorf("Captions can be at most %d characters (“%s”).", MaxCaption, it.Label)
					}
				case SectionText:
					if tooLong(c.Text, MaxSectionText) {
						return nil, fmt.Errorf("Text sections can be at most %d characters (“%s”).", MaxSectionText, it.Label)
					}
					col.Text = blocks.NormalizeRichText(c.Text)
					if c.Heading == "" && blocks.PlainText(col.Text) == "" {
						continue // an empty section
					}
				default:
					return nil, fmt.Errorf("A section in “%s” has an unknown type.", it.Label)
				}
				col.Links = nilIfEmpty(col.Links)
				cols = append(cols, col)
			}
			if len(cols) > MaxColumns {
				return nil, fmt.Errorf("“%s” can have at most %d columns.", it.Label, MaxColumns)
			}
			it.URL, it.NewTab, it.Button, it.Children, it.Columns = "", false, false, nil, cols

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

func nilIfEmpty(ls []Link) []Link {
	if len(ls) == 0 {
		return nil
	}
	return ls
}

// Visible returns what visitors should see: hidden items and links removed, and
// dropdowns/mega menus left with nothing to show dropped (a mega column with a
// linked heading, a picture or text still shows). The input isn't modified.
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
				if c.Type == SectionImage || c.Type == SectionText { // these have nothing to hide
					cols = append(cols, c)
					continue
				}
				if links := visibleLinks(c.Links); len(links) > 0 || c.URL != "" {
					c.Links = links
					cols = append(cols, c)
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
