package site

import (
	"context"

	"webbuilder/internal/menus"
	"webbuilder/internal/themes"
)

func navLink(l menus.Link, path string) themes.NavLink {
	return themes.NavLink{Label: l.Label, URL: l.URL, Description: l.Description, NewTab: l.NewTab, Active: l.URL == path}
}

// navExtras is what the icon items need from the rest of the site: the social links (from Site details) and whether
// the site has search switched on. Without them the icons are left out rather than drawn empty.
type navExtras struct {
	Social []themes.SocialLink
	Search bool
}

// navSection turns a saved menu into what the templates render: hidden items and
// emptied panels are dropped (the saved data keeps them, so hiding is reversible)
// and the link for the current page is marked.
func navSection(m menus.Menu, path string, x navExtras) themes.NavSection {
	out := themes.NavSection{Sticky: m.Sticky, Background: m.Style.Background, Text: m.Style.Text, Size: m.Style.Size}
	for _, it := range menus.Visible(m.Items) {
		ni := themes.NavItem{Kind: it.Kind, Label: it.Label, URL: it.URL, NewTab: it.NewTab, Button: it.Button, Side: it.Side}
		switch it.Kind {
		case menus.KindSocial:
			if len(x.Social) == 0 {
				continue
			}
			ni.Social = x.Social
		case menus.KindSearch:
			if !x.Search {
				continue
			}
		case menus.KindDropdown:
			for _, l := range it.Children {
				nl := navLink(l, path)
				ni.Within = ni.Within || nl.Active
				ni.Children = append(ni.Children, nl)
			}
		case menus.KindMega:
			for _, c := range it.Columns {
				col := themes.NavColumn{Heading: c.Heading}
				for _, l := range c.Links {
					nl := navLink(l, path)
					ni.Within = ni.Within || nl.Active
					col.Links = append(col.Links, nl)
				}
				ni.Columns = append(ni.Columns, col)
			}
		default:
			ni.Kind = menus.KindLink
			ni.Active = it.URL == path
		}
		out.Items = append(out.Items, ni)
	}
	return out
}

// nav loads all three menu sections for rendering the page at path.
func (s *Service) nav(ctx context.Context, path string) (themes.Nav, error) {
	all, err := s.content.Menus(ctx)
	if err != nil {
		return themes.Nav{}, err
	}
	st, err := s.settings(ctx)
	if err != nil {
		return themes.Nav{}, err
	}
	x := navExtras{Social: detailsInfo(st.Details).Social, Search: st.ShowSearch}
	return themes.Nav{
		TopBar: navSection(all["topbar"], path, x),
		Main:   navSection(all["main"], path, x),
		Footer: navSection(all["footer"], path, x),
	}, nil
}
