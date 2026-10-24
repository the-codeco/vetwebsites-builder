package wizard

import (
	"strings"
	"testing"

	"webbuilder/internal/blocks"
	"webbuilder/internal/content"
	"webbuilder/internal/menus"
	"webbuilder/internal/themes"
)

func engine(t *testing.T) *themes.Engine {
	t.Helper()
	e, err := themes.Load()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// a site as a new customer has it
func fresh() content.Settings {
	return content.Settings{Title: "Acme Vets", ActiveTheme: "default", ColorMode: "toggle", ColorSwitch: "floating", Details: content.Details{Social: map[string]string{}}}
}

// Every kit and every section is something the real screens accept, so applying a kit can never save what the
// Appearance, Menus or Footer screens would refuse.
func TestEveryKitAndSectionIsValid(t *testing.T) {
	eng := engine(t)
	for _, k := range Kits {
		for _, biz := range []string{BusinessVet, BusinessOther} {
			a := Defaults(fresh())
			a.Kit, a.Business = k.ID, biz
			a.Phone, a.Email, a.Social = "+27 21 555 0123", "info@acme.example", map[string]string{"facebook": "https://facebook.com/acme"}
			a.Newsletter, a.Announcement = true, "Open late on Friday"
			a.Sections = nil
			for _, s := range Sections {
				a.Sections = append(a.Sections, s.ID)
			}
			p, err := Build(a, fresh(), eng, nil)
			if err != nil {
				t.Errorf("kit %s (%s): %v", k.ID, biz, err)
				continue
			}
			if p.Theme != k.Theme || len(p.Blocks) == 0 || len(p.Footer.Rows) != 2 || p.Element == nil || len(p.TopItems) != 3 {
				t.Errorf("kit %s: theme %q, %d blocks, %d footer rows, element %v, %d top items", k.ID, p.Theme, len(p.Blocks), len(p.Footer.Rows), p.Element != nil, len(p.TopItems))
			}
			for _, w := range p.Warnings {
				t.Errorf("kit %s should read well as it is: %s", k.ID, w)
			}
		}
		if len(k.Swatches()) != 4 {
			t.Errorf("kit %s has %d swatches", k.ID, len(k.Swatches()))
		}
	}
	for _, s := range Sections {
		for _, biz := range []string{BusinessVet, BusinessOther} {
			for _, b := range s.build(Business{Kind: biz, Title: "Acme"}) {
				bt, ok := blocks.Get(b.Type)
				if !ok {
					t.Errorf("section %s uses an unknown block %q", s.ID, b.Type)
					continue
				}
				got := bt.Clean(b.Props)
				for key := range b.Props {
					if _, kept := got[key]; !kept {
						t.Errorf("section %s: the %s block's %q is not a field it has", s.ID, b.Type, key)
					}
				}
			}
		}
	}
	// the same section ids are never offered twice, and the defaults exist
	seen := map[string]bool{}
	for _, s := range Sections {
		if seen[s.ID] {
			t.Errorf("section %s twice", s.ID)
		}
		seen[s.ID] = true
	}
	for _, id := range DefaultSectionIDs() {
		if !seen[id] {
			t.Errorf("default section %s does not exist", id)
		}
	}
}

func TestOverridesAndTheColoursThatFollow(t *testing.T) {
	eng := engine(t)
	a := Defaults(fresh())
	a.Kit, a.Primary, a.Background, a.Text = "clinical", "#ffcc00", "#101820", "#f0f0f0"
	a.FontHeading, a.Corners, a.Dark, a.Header = "serif", "round", "auto", "centered"
	p, err := Build(a, fresh(), eng, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"color-primary": "#ffcc00", "color-bg": "#101820", "color-text": "#f0f0f0", "font-heading": "serif", "radius": "16px", "color-primary-text": "#111111"} {
		if p.Tokens[k] != want {
			t.Errorf("%s = %q, want %q", k, p.Tokens[k], want)
		}
	}
	if p.Tokens["color-surface"] == "" || p.Tokens["color-surface"] == "#eef6f7" || p.Tokens["color-muted"] == "" {
		t.Errorf("the quieter colours follow the chosen ones: %v", p.Tokens)
	}
	if p.ColorMode != "auto" || p.Layout.Header.Style != "centered" || p.Layout.Header.BarBackground == "" {
		t.Errorf("dark mode %q, header %q, bar %q", p.ColorMode, p.Layout.Header.Style, p.Layout.Header.BarBackground)
	}
	// a value equal to the theme's own default is not stored, so a later improvement to the theme still reaches the site
	a2 := Defaults(fresh())
	a2.Kit, a2.Primary = "bold", "#3b5bdb" // the default theme's own blue
	p2, _ := Build(a2, fresh(), eng, nil)
	if _, stored := p2.Tokens["color-primary"]; stored {
		t.Error("a colour that is the theme's default is stored")
	}
	// hard-to-read choices are said, not refused
	a3 := Defaults(fresh())
	a3.Kit, a3.Background, a3.Text = "clinical", "#777777", "#808080"
	p3, err := Build(a3, fresh(), eng, nil)
	if err != nil || len(p3.Warnings) == 0 {
		t.Errorf("low contrast: %v %v", p3.Warnings, err)
	}
	// a kit with the night theme is always dark: no switch
	a4 := Defaults(fresh())
	a4.Kit = "night"
	if p4, _ := Build(a4, fresh(), eng, nil); p4.Theme != "midnight" || p4.ColorMode != "light" {
		t.Errorf("night: %s %s", p4.Theme, p4.ColorMode)
	}
}

func TestBadAnswersAreExplained(t *testing.T) {
	eng := engine(t)
	for name, change := range map[string]func(*Answers){
		"no kit":          func(a *Answers) { a.Kit = "" },
		"unknown kit":     func(a *Answers) { a.Kit = "sparkly" },
		"no name":         func(a *Answers) { a.Title = ""; a.Business = "vet" },
		"a long name":     func(a *Answers) { a.Title = strings.Repeat("n", 101) },
		"a bad colour":    func(a *Answers) { a.Primary = "blue" },
		"a bad font":      func(a *Answers) { a.FontBody = "comic-sans" },
		"bad corners":     func(a *Answers) { a.Corners = "pointy" },
		"bad dark":        func(a *Answers) { a.Dark = "sometimes" },
		"bad header":      func(a *Answers) { a.Header = "stacked" },
		"a bad logo":      func(a *Answers) { a.Logo = "javascript:alert(1)" },
		"a bad phone":     func(a *Answers) { a.Phone = "12" },
		"a bad whatsapp":  func(a *Answers) { a.WhatsApp = "abc" },
		"a bad email":     func(a *Answers) { a.Email = "not-an-email" },
		"a bad social":    func(a *Answers) { a.Social = map[string]string{"facebook": "http://evil.example/x"} },
		"a bad top bar":   func(a *Answers) { a.TopBar = "everything" },
		"unknown section": func(a *Answers) { a.Sections = []string{"hero", "confetti"} },
	} {
		a := Defaults(fresh())
		a.Title = "Acme"
		change(&a)
		now := fresh()
		now.Title = "" // so that "no name" has nothing to fall back to
		_, err := Build(a, now, eng, nil)
		var pr ErrProblem
		if err == nil {
			t.Errorf("%s was accepted", name)
		} else if p, ok := err.(ErrProblem); ok {
			pr = p
			if pr.Msg == "" || !strings.HasSuffix(pr.Msg, ".") {
				t.Errorf("%s: the message should be a sentence: %q", name, pr.Msg)
			}
		} else {
			t.Errorf("%s: not a message for the person: %v", name, err)
		}
	}
}

func TestMenusAndFooterFollowTheDetails(t *testing.T) {
	eng := engine(t)
	a := Defaults(fresh())
	a.Phone, a.Social = "", map[string]string{}
	p, err := Build(a, fresh(), eng, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.TopItems) != 1 || p.TopItems[0].Kind != menus.KindSearch {
		t.Errorf("without a phone or social links the top bar has only the search icon: %+v", p.TopItems)
	}
	if got := p.Footer.Rows[0].Columns; len(got) != 2 {
		t.Errorf("without contact details the footer has no contact column: %d", len(got))
	}
	a.Phone, a.Hours = "021 555 0123", "Mon to Fri 8 to 5"
	p, _ = Build(a, fresh(), eng, nil)
	if p.TopItems[0].URL == "" || !strings.HasPrefix(p.TopItems[0].URL, "tel:") || p.TopItems[0].Side != "left" {
		t.Errorf("the phone number is a call link on the left: %+v", p.TopItems[0])
	}
	if got := p.Footer.Rows[0].Columns; len(got) != 3 {
		t.Errorf("the footer has a contact column: %d", len(got))
	}
	// a main menu the person built is not replaced; the starter one is
	own := map[string]menus.Menu{"main": {Items: []menus.Item{{Kind: menus.KindLink, Label: "Shop", URL: "/shop"}}}}
	if p, _ := Build(a, fresh(), eng, own); len(p.MainItems) != 1 || p.MainItems[0].Label != "Shop" {
		t.Errorf("own menu: %+v", p.MainItems)
	}
	starter := map[string]menus.Menu{"main": {Items: []menus.Item{{Kind: menus.KindLink, Label: "Home", URL: "/"}, {Kind: menus.KindLink, Label: "About", URL: "/about"}}}}
	if p, _ := Build(a, fresh(), eng, starter); len(p.MainItems) != 2 {
		t.Errorf("starter menu: %+v", p.MainItems)
	}
	// details already saved are kept when the wizard is left blank
	now := fresh()
	now.Details = content.Details{Phone: "011 555 0000", Email: "old@acme.example", Numbers: []content.ContactNumber{{Kind: "fax", Number: "011 555 0001"}}, Social: map[string]string{}}
	b := Defaults(now)
	b.Phone, b.Email = "", ""
	pb, _ := Build(b, now, eng, nil)
	if pb.Details.Phone != "011 555 0000" || pb.Details.Email != "old@acme.example" || len(pb.Details.Numbers) != 1 {
		t.Errorf("details: %+v", pb.Details)
	}
}

func TestColourMaths(t *testing.T) {
	if c := Contrast("#000000", "#ffffff"); c < 20.9 || c > 21.1 {
		t.Errorf("black on white: %.2f", c)
	}
	if Contrast("#ffffff", "#ffffff") != 1 {
		t.Error("same colour")
	}
	if readableOn("#ffffff") != "#111111" || readableOn("#0b3d91") != "#ffffff" {
		t.Error("readableOn")
	}
	if got := mix("#000000", "#ffffff", .5); got != "#808080" {
		t.Errorf("mix: %s", got)
	}
	if Contrast("nope", "#fff") != 0 {
		t.Error("bad colours have no contrast")
	}
}
