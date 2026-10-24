// Package elements is the site-wide things a website can show for a while: a notice bar, an emergency alert, a popup
// (a notice or a newsletter sign-up) and a floating contact button. Each has a kind, the settings that kind uses, a
// window in time and the pages it shows on. This package only checks and describes them; storing them is the content
// package's job and drawing them is the theme's.
//
// The window in time is handed to the visitor's browser, which compares it with its own clock: a published copy of a
// website is a set of files with no server behind it, so nothing else could end a notice at the minute it should end.
package elements

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"webbuilder/internal/blocks"
)

// The kinds, in the order the builder offers them.
const (
	KindBar     = "bar"            // a slim strip above the header
	KindAlert   = "alert"          // an urgent strip: closures, emergencies
	KindPopup   = "popup"          // a dialog: a notice, or a newsletter sign-up
	KindContact = "contact_button" // a button fixed to the corner of the screen: call, WhatsApp or a link
)

// Kinds lists the kinds with the words the builder shows.
var Kinds = []Kind{
	{KindBar, "Notice bar", "A slim strip above the header, for news such as new opening hours."},
	{KindAlert, "Emergency alert", "An urgent red strip, for closures and emergencies. Stays on screen unless you let visitors close it."},
	{KindPopup, "Popup", "A box over the page: a notice, or a newsletter sign-up. Opens after a delay, on scroll, or when the visitor is about to leave."},
	{KindContact, "Contact button", "A button fixed to a corner of the screen that calls, opens WhatsApp or goes to a page."},
}

// Kind describes one kind for the builder.
type Kind struct{ ID, Label, Help string }

// Limits.
const (
	MaxElements    = 20
	MaxName        = 80
	MaxText        = 300 // a bar's message
	MaxHeading     = 100
	MaxBody        = 1500 // a popup's text (HTML)
	MaxLabel       = 40
	MaxPages       = 30
	MaxDismissDays = 365
	MaxDelay       = 300
	farFuture      = 5 * 366 * 24 * time.Hour
)

// Styles of a notice bar.
const (
	StyleInfo   = "info"   // the site's main colour
	StyleAccent = "accent" // the site's accent colour
	StyleLight  = "light"  // pale, quiet
)

// Popup modes and triggers.
const (
	ModeInfo      = "info"
	ModeSubscribe = "subscribe"

	TriggerDelay  = "delay"  // after some seconds
	TriggerScroll = "scroll" // once the visitor has scrolled some way down
	TriggerExit   = "exit"   // when the mouse leaves the top of the window (after a delay on phones)
)

// Contact button actions and sides.
const (
	ActionCall     = "call"
	ActionWhatsApp = "whatsapp"
	ActionLink     = "link"

	SideLeft  = "left"
	SideRight = "right"
)

// Config is everything an element can be set up with. A kind uses only some of it; Clean drops the rest.
type Config struct {
	Text        string `json:"text,omitempty"`    // bar, alert: the message. Popup: the body (HTML from the visual editor)
	Heading     string `json:"heading,omitempty"` // popup
	Link        string `json:"link,omitempty"`    // bar, alert: where it leads. Popup: where its button leads. Contact: the link
	LinkLabel   string `json:"link_label,omitempty"`
	Style       string `json:"style,omitempty"`        // bar: info, accent or light
	Dismissible bool   `json:"dismissible,omitempty"`  // bar, alert: a close button
	DismissDays int    `json:"dismiss_days,omitempty"` // how long a closed element stays away (0: until the browser is closed)
	Mode        string `json:"mode,omitempty"`         // popup: info or subscribe
	Trigger     string `json:"trigger,omitempty"`      // popup: delay, scroll or exit
	Delay       int    `json:"delay,omitempty"`        // seconds (popup)
	Scroll      int    `json:"scroll,omitempty"`       // percent of the page (popup)
	Button      string `json:"button,omitempty"`       // popup: the sign-up button's words
	Success     string `json:"success,omitempty"`      // popup: the thank-you after signing up
	Action      string `json:"action,omitempty"`       // contact: call, whatsapp or link
	Label       string `json:"label,omitempty"`        // contact: the words on the button
	Side        string `json:"side,omitempty"`         // contact: left or right
}

// Element is one site-wide element.
type Element struct {
	ID         int64
	Kind, Name string
	Enabled    bool
	Config     Config
	Pages      []string   // addresses it shows on (see CleanPages); empty is every page
	Start, End *time.Time // it shows from Start until End; either may be open
	Position   int
	UpdatedAt  time.Time
}

// Status says where an element is in its life, for the list.
type Status string

const (
	StatusOff       Status = "off"
	StatusScheduled Status = "scheduled"
	StatusLive      Status = "live"
	StatusEnded     Status = "ended"
)

// StatusAt is the element's status at a moment.
func (e Element) StatusAt(now time.Time) Status {
	switch {
	case !e.Enabled:
		return StatusOff
	case e.End != nil && !e.End.After(now):
		return StatusEnded
	case e.Start != nil && e.Start.After(now):
		return StatusScheduled
	}
	return StatusLive
}

// Label is the kind's name for people.
func (e Element) Label() string {
	for _, k := range Kinds {
		if k.ID == e.Kind {
			return k.Label
		}
	}
	return e.Kind
}

// Title is what the list calls an element: its name, or what it says.
func (e Element) Title() string {
	switch {
	case e.Name != "":
		return e.Name
	case e.Config.Heading != "":
		return e.Config.Heading
	case e.Config.Text != "":
		return truncate(plain(e.Config.Text), 60)
	case e.Config.Label != "":
		return e.Config.Label
	}
	return e.Label()
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func plain(s string) string { return strings.Join(strings.Fields(tagRe.ReplaceAllString(s, " ")), " ") }

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ValidKind says whether a kind is one this version knows.
func ValidKind(k string) bool {
	for _, x := range Kinds {
		if x.ID == k {
			return true
		}
	}
	return false
}

// Clean checks an element as typed and returns it in the form that is stored, or says what is wrong in words for the
// owner. It is also what a restored backup goes through, so it never trusts what it is given.
func Clean(e Element, now time.Time) (Element, error) {
	e.Name = strings.TrimSpace(e.Name)
	if len([]rune(e.Name)) > MaxName {
		return e, fmt.Errorf("The name can be at most %d characters.", MaxName)
	}
	if !ValidKind(e.Kind) {
		return e, errors.New("Choose what kind of element this is.")
	}
	var err error
	if e.Config, err = cleanConfig(e.Kind, e.Config); err != nil {
		return e, err
	}
	if e.Pages, err = CleanPages(e.Pages); err != nil {
		return e, err
	}
	if e.Start != nil {
		t := e.Start.UTC().Truncate(time.Second)
		e.Start = &t
		if t.After(now.Add(farFuture)) {
			return e, errors.New("The start is too far away.")
		}
	}
	if e.End != nil {
		t := e.End.UTC().Truncate(time.Second)
		e.End = &t
		if t.After(now.Add(farFuture)) {
			return e, errors.New("The end is too far away.")
		}
	}
	if e.Start != nil && e.End != nil && !e.End.After(*e.Start) {
		return e, errors.New("The end must be after the start.")
	}
	return e, nil
}

func cleanText(s string, max int, what string) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > max {
		return "", fmt.Errorf("%s can be at most %d characters.", what, max)
	}
	return s, nil
}

func cleanLink(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) > 2000 {
		return "", errors.New("The link is too long.")
	}
	u := blocks.SafeURL(s)
	if u == "" {
		return "", errors.New("The link must be a page on this site such as /contact, an https:// address, a mailto: or a tel: link.")
	}
	return u, nil
}

func cleanDays(n int) int {
	if n < 0 {
		return 0
	}
	return min(n, MaxDismissDays)
}

func cleanConfig(kind string, c Config) (Config, error) {
	var out Config
	var err error
	switch kind {
	case KindBar, KindAlert:
		if out.Text, err = cleanText(c.Text, MaxText, "The message"); err != nil {
			return c, err
		}
		if out.Text == "" {
			return c, errors.New("Write the message to show.")
		}
		if out.Link, err = cleanLink(c.Link); err != nil {
			return c, err
		}
		if out.LinkLabel, err = cleanText(c.LinkLabel, MaxLabel, "The link's words"); err != nil {
			return c, err
		}
		if out.Link != "" && out.LinkLabel == "" {
			out.LinkLabel = "Read more"
		}
		out.Dismissible = c.Dismissible
		out.DismissDays = cleanDays(c.DismissDays)
		if kind == KindBar {
			out.Style = c.Style
			if out.Style != StyleAccent && out.Style != StyleLight {
				out.Style = StyleInfo
			}
		}
	case KindPopup:
		if out.Heading, err = cleanText(c.Heading, MaxHeading, "The heading"); err != nil {
			return c, err
		}
		out.Text = blocks.SanitizeHTML(strings.TrimSpace(c.Text))
		if len(out.Text) > MaxBody {
			return c, fmt.Errorf("The text is too long (at most %d characters, counting formatting).", MaxBody)
		}
		out.Mode = c.Mode
		if out.Mode != ModeSubscribe {
			out.Mode = ModeInfo
		}
		if out.Heading == "" && plain(out.Text) == "" {
			return c, errors.New("Give the popup a heading or some text.")
		}
		if out.Mode == ModeInfo {
			if out.Link, err = cleanLink(c.Link); err != nil {
				return c, err
			}
			if out.LinkLabel, err = cleanText(c.LinkLabel, MaxLabel, "The button's words"); err != nil {
				return c, err
			}
			if out.Link != "" && out.LinkLabel == "" {
				out.LinkLabel = "Find out more"
			}
		} else {
			if out.Button, err = cleanText(c.Button, MaxLabel, "The button's words"); err != nil {
				return c, err
			}
			if out.Success, err = cleanText(c.Success, MaxText, "The thank-you"); err != nil {
				return c, err
			}
		}
		out.Trigger = c.Trigger
		switch out.Trigger {
		case TriggerScroll:
			out.Scroll = min(max(c.Scroll, 5), 95)
		case TriggerExit:
			out.Delay = min(max(c.Delay, 0), MaxDelay)
		default:
			out.Trigger = TriggerDelay
			out.Delay = min(max(c.Delay, 0), MaxDelay)
		}
		out.DismissDays = cleanDays(c.DismissDays)
	case KindContact:
		out.Action = c.Action
		switch out.Action {
		case ActionCall, ActionWhatsApp:
		case ActionLink:
			if out.Link, err = cleanLink(c.Link); err != nil {
				return c, err
			}
			if out.Link == "" {
				return c, errors.New("Enter where the button should go.")
			}
		default:
			return c, errors.New("Choose what the button does.")
		}
		if out.Label, err = cleanText(c.Label, MaxLabel, "The button's words"); err != nil {
			return c, err
		}
		if out.Label == "" {
			out.Label = map[string]string{ActionCall: "Call us", ActionWhatsApp: "WhatsApp", ActionLink: "Contact"}[out.Action]
		}
		out.Side = SideRight
		if c.Side == SideLeft {
			out.Side = SideLeft
		}
	}
	return out, nil
}

var pathRe = regexp.MustCompile(`^/[a-z0-9._~%\-]+(/[a-z0-9._~%\-]+)*(/\*)?$`)

// CleanPages checks the addresses an element shows on: "/" for the home page, "/about" for one page, "/blog/*" for a
// page and everything under it. Lower case, no trailing slash. An empty list means every page.
func CleanPages(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if p != "/" {
			p = strings.TrimRight(p, "/")
			if !pathRe.MatchString(p) {
				return nil, fmt.Errorf("%q is not a page address such as /about.", p)
			}
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) > MaxPages {
		return nil, fmt.Errorf("Choose at most %d pages.", MaxPages)
	}
	return out, nil
}

// ShowsOn says whether an element shows on the page at a path.
func (e Element) ShowsOn(path string) bool {
	if len(e.Pages) == 0 {
		return true
	}
	path = strings.ToLower(path)
	if path != "/" {
		path = strings.TrimRight(path, "/")
	}
	for _, p := range e.Pages {
		switch {
		case p == path:
			return true
		case strings.HasSuffix(p, "/*"):
			base := strings.TrimSuffix(p, "/*")
			if path == base || strings.HasPrefix(path, base+"/") {
				return true
			}
		}
	}
	return false
}

// Version changes whenever what an element says or how it behaves changes, so a visitor who closed it sees it again.
// The window and the pages are left out: moving the end date of a notice a closed visitor already saw is not news.
func (e Element) Version(fingerprint func([]byte) string) string {
	c := e.Config
	return fingerprint([]byte(fmt.Sprint(e.Kind, "|", c.Text, "|", c.Heading, "|", c.Link, "|", c.LinkLabel, "|", c.Style, "|", c.Mode, "|", c.Button)))
}
