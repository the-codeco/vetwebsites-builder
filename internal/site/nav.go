package site

import (
	"context"

	"webbuilder/internal/menus"
	"webbuilder/internal/themes"
)

func navLink(l menus.Link, path string) themes.NavLink {
	return themes.NavLink{Label: l.Label, URL: l.URL, Description: l.Description, NewTab: l.NewTab, Active: l.URL == path}
}

// navSection turns a saved menu into what the templates render: hidden items and
// emptied panels are dropped (the saved data keeps them, so hiding is reversible)
// and the link for the current page is marked.
func navSection(m menus.Menu, path string) themes.NavSection {
	out := themes.NavSection{Sticky: m.Sticky}
	for _, it := range menus.Visible(m.Items) {
		ni := themes.NavItem{Kind: it.Kind, Label: it.Label, URL: it.URL, NewTab: it.NewTab, Button: it.Button}
		switch it.Kind {
		case menus.KindDropdown:
			for _, l := range it.Children {
				nl := navLink(l, path)
				ni.Within = ni.Within || nl.Active
				ni.Children = append(ni.Children, nl)
			}
		case menus.KindMega:
			for _, c := range it.Columns {
				typ := c.Type
				if typ == "" {
					typ = menus.SectionLinks
				}
				col := themes.NavColumn{Type: typ, Heading: c.Heading, URL: c.URL, Active: c.URL != "" && c.URL == path,
					Image: c.Image, Alt: c.Alt, Caption: c.Caption, Text: c.Text}
				ni.Within = ni.Within || col.Active
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

// nav loads the menu sections for rendering the page at path. The footer's links are part of the
// footer itself now, so Nav.Footer stays empty (layouts may still place it).
func (s *Service) nav(ctx context.Context, path string) (themes.Nav, error) {
	all, err := s.content.Menus(ctx)
	if err != nil {
		return themes.Nav{}, err
	}
	return themes.Nav{
		TopBar: navSection(all["topbar"], path),
		Main:   navSection(all["main"], path),
	}, nil
}
