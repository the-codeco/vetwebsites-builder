package site

import (
	"testing"

	"webbuilder/internal/menus"
)

func TestNavSection(t *testing.T) {
	m := menus.Menu{Sticky: true, Items: []menus.Item{
		{Kind: menus.KindLink, Label: "Home", URL: "/"},
		{Kind: menus.KindLink, Label: "Secret", URL: "/secret", Hidden: true},
		{Kind: menus.KindLink, Label: "Docs", URL: "https://example.com", NewTab: true},
		{Kind: menus.KindDropdown, Label: "Company", Children: []menus.Link{
			{Label: "About", URL: "/about"}, {Label: "Old", URL: "/old", Hidden: true}, {Label: "Team", URL: "/team"}}},
		{Kind: menus.KindDropdown, Label: "Empty dropdown", Children: []menus.Link{{Label: "x", URL: "/x", Hidden: true}}},
		{Kind: menus.KindMega, Label: "Products", Columns: []menus.Column{
			{Heading: "Apps", Links: []menus.Link{{Label: "Alpha", URL: "/alpha", Description: "First"}, {Label: "Beta", URL: "/beta"}}},
			{Heading: "Hidden col", Links: []menus.Link{{Label: "z", URL: "/z", Hidden: true}}}}},
	}}

	got := navSection(m, "/about", navExtras{})
	if !got.Sticky || len(got.Items) != 4 {
		t.Fatalf("sticky=%v, %d items", got.Sticky, len(got.Items))
	}
	if got.Items[0].Active || got.Items[0].Kind != "link" {
		t.Errorf("Home is not the current page: %+v", got.Items[0])
	}
	if !got.Items[1].NewTab || got.Items[1].URL != "https://example.com" {
		t.Errorf("new tab lost: %+v", got.Items[1])
	}
	dd := got.Items[2]
	if dd.Kind != "dropdown" || len(dd.Children) != 2 || !dd.Within || !dd.Children[0].Active || dd.Children[1].Active || dd.Active {
		t.Errorf("dropdown: %+v", dd)
	}
	mega := got.Items[3]
	if mega.Kind != "mega" || len(mega.Columns) != 1 || mega.Columns[0].Heading != "Apps" || mega.Columns[0].Links[0].Description != "First" || mega.Within {
		t.Errorf("mega: %+v", mega)
	}

	// the current page is found inside a mega menu too, and on the home page
	if got := navSection(m, "/beta", navExtras{}); !got.Items[3].Within || !got.Items[3].Columns[0].Links[1].Active || got.Items[2].Within {
		t.Errorf("current page in mega: %+v", got.Items[3])
	}
	if got := navSection(m, "/", navExtras{}); !got.Items[0].Active {
		t.Error("home link should be current on /")
	}
	// an external link is never "current"
	if got := navSection(m, "/anything", navExtras{}); got.Items[1].Active {
		t.Error("external link marked current")
	}
}

func TestNavSectionOfNothing(t *testing.T) {
	if got := navSection(menus.Menu{}, "/", navExtras{}); len(got.Items) != 0 || got.Sticky {
		t.Errorf("%+v", got)
	}
	// legacy rows saved before kinds existed are still plain links
	got := navSection(menus.Menu{Items: []menus.Item{{Label: "Old", URL: "/old"}}}, "/old", navExtras{})
	if len(got.Items) != 1 || got.Items[0].Kind != "link" || !got.Items[0].Active {
		t.Errorf("%+v", got.Items)
	}
}
