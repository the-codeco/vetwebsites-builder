package menus

import (
	"reflect"
	"strings"
	"testing"
)

var (
	main_  = Section{Key: "main", Label: "Main navigation", Nested: true, MaxItems: 12}
	topbar = Section{Key: "topbar", Label: "Top bar", MaxItems: 3}
)

func TestSectionsAreDefined(t *testing.T) {
	want := map[string]bool{"topbar": false, "main": true, "footer": false}
	if len(Sections) != 3 {
		t.Fatalf("%d sections", len(Sections))
	}
	for _, s := range Sections {
		nested, ok := want[s.Key]
		if !ok || s.Nested != nested || s.Label == "" || s.Help == "" || s.MaxItems <= 0 {
			t.Errorf("section %+v", s)
		}
		if got, found := SectionByKey(s.Key); !found || got.Key != s.Key {
			t.Errorf("SectionByKey(%q)", s.Key)
		}
	}
	if _, ok := SectionByKey("sidebar"); ok {
		t.Error("unknown section found")
	}
}

func TestDecode(t *testing.T) {
	items, err := Decode([]byte(`[{"kind":"link","label":"Home","url":"/"},{"kind":"mega","label":"X","columns":[{"heading":"H","links":[{"label":"a","url":"/a","description":"d","new_tab":true}]}]}]`))
	if err != nil || len(items) != 2 || items[1].Columns[0].Links[0].Description != "d" || !items[1].Columns[0].Links[0].NewTab {
		t.Fatalf("%+v %v", items, err)
	}
	for name, in := range map[string]string{
		"not json": `nope`, "an object": `{"a":1}`, "unknown field": `[{"label":"x","evil":1}]`,
		"wrong type": `[{"label":5}]`, "too big": `[` + strings.Repeat(`{"label":"x"},`, 20000) + `{"label":"y"}]`,
	} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if items, err := Decode([]byte(`[]`)); err != nil || len(items) != 0 {
		t.Errorf("empty menu: %v %v", items, err)
	}
}

func TestValidateLinks(t *testing.T) {
	out, err := Validate(main_, []Item{
		{Label: "  Home ", URL: " / "},
		{Kind: KindLink, Label: "Docs", URL: "https://example.com/docs", NewTab: true},
		{Label: "", URL: ""}, // an empty row from the editor
		{Label: "Mail", URL: "mailto:hi@example.com"},
	})
	if err != nil || len(out) != 3 {
		t.Fatalf("%+v %v", out, err)
	}
	if out[0].Label != "Home" || out[0].URL != "/" || out[0].Kind != KindLink {
		t.Errorf("not normalised: %+v", out[0])
	}
	if !out[1].NewTab {
		t.Error("new_tab lost")
	}
	bad := map[string]Item{
		"javascript url":    {Label: "Evil", URL: "javascript:alert(1)"},
		"data url":          {Label: "Evil", URL: "data:text/html,x"},
		"protocol relative": {Label: "Evil", URL: "//evil.example/x"},
		"backslash":         {Label: "Evil", URL: `/\evil.example`},
		"relative path":     {Label: "Rel", URL: "about"},
		"no url":            {Label: "NoURL"},
		"no label":          {Label: "", URL: "/x"},
		"label too long":    {Label: strings.Repeat("a", 61), URL: "/x"},
		"unknown kind":      {Kind: "script", Label: "X", URL: "/x"},
	}
	for name, it := range bad {
		if _, err := Validate(main_, []Item{it}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestValidateDropdown(t *testing.T) {
	out, err := Validate(main_, []Item{{Kind: KindDropdown, Label: "Company", URL: "/ignored", NewTab: true, Columns: []Column{{Heading: "stale"}},
		Children: []Link{{Label: "About", URL: "/about", Description: "dropped"}, {Label: "", URL: ""}, {Label: "Team", URL: "/team", Hidden: true}}}})
	if err != nil || len(out) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	d := out[0]
	if d.URL != "" || d.NewTab || d.Columns != nil || len(d.Children) != 2 || d.Children[0].Description != "" || !d.Children[1].Hidden {
		t.Errorf("a dropdown keeps only its children: %+v", d)
	}
	kids := make([]Link, MaxChildren+1)
	for i := range kids {
		kids[i] = Link{Label: "x", URL: "/x"}
	}
	if _, err := Validate(main_, []Item{{Kind: KindDropdown, Label: "Big", Children: kids}}); err == nil {
		t.Error("too many children accepted")
	}
	if _, err := Validate(main_, []Item{{Kind: KindDropdown, Label: "Bad", Children: []Link{{Label: "x", URL: "javascript:1"}}}}); err == nil {
		t.Error("hostile child link accepted")
	}
	if _, err := Validate(main_, []Item{{Kind: KindDropdown, Label: "Half", Children: []Link{{Label: "", URL: "/x"}}}}); err == nil {
		t.Error("a child with no label accepted")
	}
}

func TestValidateMega(t *testing.T) {
	link := func(n string) Link { return Link{Label: n, URL: "/" + n, Description: "About " + n} }
	out, err := Validate(main_, []Item{{Kind: KindMega, Label: "Products", URL: "/ignored", Children: []Link{{Label: "stale", URL: "/s"}},
		Columns: []Column{
			{Heading: "  Apps ", Links: []Link{link("a"), link("b"), {Label: "", URL: ""}}},
			{Heading: "", Links: nil}, // empty column: dropped
			{Heading: "No heading", Links: []Link{link("c")}},
			{Links: []Link{link("d")}}, // links without a heading are fine
		}}})
	if err != nil || len(out) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	m := out[0]
	if m.URL != "" || m.Children != nil || len(m.Columns) != 3 || m.Columns[0].Heading != "Apps" || len(m.Columns[0].Links) != 2 || m.Columns[0].Links[0].Description != "About a" {
		t.Errorf("mega menu: %+v", m)
	}

	cols := make([]Column, MaxColumns+1)
	for i := range cols {
		cols[i] = Column{Heading: "H", Links: []Link{link("x")}}
	}
	if _, err := Validate(main_, []Item{{Kind: KindMega, Label: "Wide", Columns: cols}}); err == nil || !strings.Contains(err.Error(), "at most 4 columns") {
		t.Errorf("too many columns: %v", err)
	}
	many := make([]Link, MaxColumnLinks+1)
	for i := range many {
		many[i] = link("x")
	}
	if _, err := Validate(main_, []Item{{Kind: KindMega, Label: "Tall", Columns: []Column{{Heading: "H", Links: many}}}}); err == nil {
		t.Error("too many links in a column accepted")
	}
	longDesc := Link{Label: "x", URL: "/x", Description: strings.Repeat("d", MaxDescription+1)}
	if _, err := Validate(main_, []Item{{Kind: KindMega, Label: "Long", Columns: []Column{{Links: []Link{longDesc}}}}}); err == nil {
		t.Error("overlong description accepted")
	}
	if _, err := Validate(main_, []Item{{Kind: KindMega, Label: "Head", Columns: []Column{{Heading: strings.Repeat("h", 61), Links: []Link{link("x")}}}}}); err == nil {
		t.Error("overlong column heading accepted")
	}
	if _, err := Validate(main_, []Item{{Kind: KindMega, Label: "Evil", Columns: []Column{{Links: []Link{{Label: "x", URL: "javascript:alert(1)"}}}}}}); err == nil {
		t.Error("hostile mega link accepted")
	}
}

func TestSectionsWithoutNestingOnlyTakeLinks(t *testing.T) {
	for _, kind := range []string{KindDropdown, KindMega} {
		if _, err := Validate(topbar, []Item{{Kind: kind, Label: "Nope", Children: []Link{{Label: "a", URL: "/a"}}, Columns: []Column{{Links: []Link{{Label: "a", URL: "/a"}}}}}}); err == nil {
			t.Errorf("%s accepted in a links-only section", kind)
		}
	}
	if _, err := Validate(topbar, []Item{{Label: "Call", URL: "tel:+27 82 123 4567"}}); err != nil {
		t.Errorf("a phone number is a fine link: %v", err)
	}
	if _, err := Validate(topbar, []Item{{Label: "Call", URL: "tel:+1"}}); err == nil {
		t.Error("a number that short is not accepted")
	}
	if _, err := Validate(topbar, []Item{{Label: "Call", URL: "sms:0821234567"}}); err == nil {
		t.Error("sms: is not an allowed scheme")
	}
	if _, err := Validate(topbar, []Item{{Label: "a", URL: "/a"}, {Label: "b", URL: "/b"}, {Label: "c", URL: "/c"}}); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	if _, err := Validate(topbar, []Item{{Label: "a", URL: "/a"}, {Label: "b", URL: "/b"}, {Label: "c", URL: "/c"}, {Label: "d", URL: "/d"}}); err == nil {
		t.Error("over the item limit accepted")
	}
	// blank rows don't count towards the limit
	rows := []Item{{Label: "a", URL: "/a"}, {}, {}, {}, {}, {Label: "b", URL: "/b"}}
	if out, err := Validate(topbar, rows); err != nil || len(out) != 2 {
		t.Errorf("blank rows: %v %v", out, err)
	}
}

func TestValidateIsIdempotent(t *testing.T) {
	in := []Item{
		{Label: "Home", URL: "/"},
		{Kind: KindDropdown, Label: "Co", Children: []Link{{Label: "About", URL: "/about"}}},
		{Kind: KindMega, Label: "P", Columns: []Column{{Heading: "H", Links: []Link{{Label: "a", URL: "/a", Description: "d"}}}}},
	}
	once, err := Validate(main_, in)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Validate(main_, once)
	if err != nil || !reflect.DeepEqual(once, twice) {
		t.Errorf("not stable:\n%+v\n%+v\n%v", once, twice, err)
	}
}

func TestVisible(t *testing.T) {
	in := []Item{
		{Kind: KindLink, Label: "Shown", URL: "/a"},
		{Kind: KindLink, Label: "Hidden", URL: "/b", Hidden: true},
		{Kind: KindDropdown, Label: "Dd", Children: []Link{{Label: "x", URL: "/x"}, {Label: "y", URL: "/y", Hidden: true}}},
		{Kind: KindDropdown, Label: "AllHidden", Children: []Link{{Label: "z", URL: "/z", Hidden: true}}},
		{Kind: KindDropdown, Label: "Empty"},
		{Kind: KindMega, Label: "Mega", Columns: []Column{
			{Heading: "Keep", Links: []Link{{Label: "k", URL: "/k"}, {Label: "h", URL: "/h", Hidden: true}}},
			{Heading: "Gone", Links: []Link{{Label: "g", URL: "/g", Hidden: true}}},
		}},
		{Kind: KindMega, Label: "NothingLeft", Columns: []Column{{Heading: "G", Links: []Link{{Label: "g", URL: "/g", Hidden: true}}}}},
		{Kind: KindMega, Label: "HiddenMega", Hidden: true, Columns: []Column{{Links: []Link{{Label: "a", URL: "/a"}}}}},
	}
	snapshot := reflect.ValueOf(in).Pointer()
	out := Visible(in)
	var labels []string
	for _, it := range out {
		labels = append(labels, it.Label)
	}
	if strings.Join(labels, ",") != "Shown,Dd,Mega" {
		t.Errorf("visible: %v", labels)
	}
	if len(out[1].Children) != 1 || out[1].Children[0].Label != "x" {
		t.Errorf("hidden child shown: %+v", out[1].Children)
	}
	if len(out[2].Columns) != 1 || out[2].Columns[0].Heading != "Keep" || len(out[2].Columns[0].Links) != 1 {
		t.Errorf("mega filtering: %+v", out[2].Columns)
	}
	// the saved data is untouched: hiding is reversible
	if len(in[2].Children) != 2 || len(in[5].Columns) != 2 || len(in[5].Columns[0].Links) != 2 || reflect.ValueOf(in).Pointer() != snapshot {
		t.Error("Visible modified its input")
	}
}

func TestIconItemsAndSides(t *testing.T) {
	bar := Section{Key: "topbar", Label: "Top bar", Icons: true, Sides: true, MaxItems: 10}
	out, err := Validate(bar, []Item{
		{Kind: KindSocial, Side: "left", URL: "/ignored", NewTab: true},
		{Kind: KindSearch, Side: "sideways"},
		{Kind: KindLink, Label: "Login", URL: "/login", Side: "right"},
	})
	if err != nil || len(out) != 3 {
		t.Fatalf("%v %+v", err, out)
	}
	if out[0].Label != "Social media" || out[0].URL != "" || out[0].NewTab || out[0].Side != "left" {
		t.Errorf("social item: %+v", out[0])
	}
	if out[1].Label != "Search" || out[1].Side != "" {
		t.Errorf("an unknown side is dropped: %+v", out[1])
	}
	if out[2].Side != "right" {
		t.Errorf("side kept on a link: %+v", out[2])
	}

	// sections without icons or sides refuse and forget
	plain := Section{Key: "footer", Label: "Footer", MaxItems: 5}
	if _, err := Validate(plain, []Item{{Kind: KindSearch}}); err == nil {
		t.Error("the footer takes links only")
	}
	kept, err := Validate(plain, []Item{{Label: "Home", URL: "/", Side: "left"}})
	if err != nil || kept[0].Side != "" {
		t.Errorf("a side only means something in the top bar: %+v %v", kept, err)
	}

	// hidden icon items are not shown, like any hidden item
	if vis := Visible([]Item{{Kind: KindSearch, Hidden: true}, {Kind: KindSocial}}); len(vis) != 1 || vis[0].Kind != KindSocial {
		t.Errorf("%+v", vis)
	}
}

func TestStyleIsValidatedAndCleaned(t *testing.T) {
	got, err := ValidateStyle(Style{Background: " #1A2B3C ", Text: "#FFFFFF", Size: "lg"})
	if err != nil || got != (Style{Background: "#1a2b3c", Text: "#ffffff", Size: "lg"}) {
		t.Errorf("%+v %v", got, err)
	}
	if got, _ := ValidateStyle(Style{Size: "md"}); got.Size != "" {
		t.Errorf("medium is the default and is not stored: %+v", got)
	}
	for _, bad := range []Style{{Background: "red"}, {Text: "#12345"}, {Background: "#1a2b3c;color:red"}, {Size: "huge"}, {Text: "url(javascript:x)"}} {
		if _, err := ValidateStyle(bad); err == nil {
			t.Errorf("%+v should be refused", bad)
		}
	}
	// stored or restored data: keep what is valid, drop the rest
	if got := CleanStyle(Style{Background: "#112233", Text: "nonsense", Size: "xl"}); got != (Style{Background: "#112233"}) {
		t.Errorf("%+v", got)
	}
	if got := CleanStyle(Style{}); got != (Style{}) {
		t.Errorf("%+v", got)
	}
}
