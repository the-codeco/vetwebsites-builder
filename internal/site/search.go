package site

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"webbuilder/internal/auth"
	"webbuilder/internal/content"
	"webbuilder/internal/footer"
	"webbuilder/internal/search"
	"webbuilder/internal/themes"
)

// siteInfo is what every layout receives about the site, including whether to show the search box.
func (s *Service) siteInfo(r *http.Request, theme *themes.Theme, st content.Settings, preview bool) themes.SiteInfo {
	info := theme.Info(st.Title, preview)
	info.Search = st.ShowSearch
	info.ColorMode = theme.EffectiveColorMode(st.ColorMode)
	info.ColorSwitch = st.ColorSwitch
	if !themes.ValidColorSwitch(info.ColorSwitch) {
		info.ColorSwitch = themes.SwitchFloating
	}
	info.Footer = s.footerInfo(r, st)
	if c := st.Consent; c.Enabled {
		info.Consent = &themes.ConsentInfo{
			Text: c.Message(), LinkLabel: c.LinkLabel, LinkURL: c.LinkURL, Ask: c.Ask,
			Version: themes.Fingerprint([]byte(fmt.Sprint(c.Ask, "|", c.Message(), "|", c.LinkLabel, "|", c.LinkURL))),
		}
	}
	info.Brand = themes.BrandInfo{Logo: st.Brand.Logo, Alt: st.Brand.LogoAlt, Icon: st.Brand.Icon, AltLogo: st.Brand.AltLogo, Height: st.Brand.HeightPixels(), ShowTitle: st.Brand.Logo == "" || !st.Brand.HideTitle, Size: st.Brand.LogoHeight}
	if info.Brand.Alt == "" {
		info.Brand.Alt = st.Title
	}
	info.Details = detailsInfo(st.Details)
	// analytics are never loaded for someone who is logged in: the owner's own visits are not visitors
	if t := st.Tracking; t.Any() && auth.CurrentIdentity(r) == nil && !preview {
		info.Tracking = &themes.TrackingInfo{GA4: t.GA4, Plausible: t.PlausibleDomain, Wait: !t.NoWait, JS: theme.TrackJS()}
	}
	return info
}

// footerInfo turns the saved footer into what templates show: links marked when they point at
// this page, a newsletter widget given its form (it signs up like a signup block, as block 0), and
// contact or social widgets left out when Site details has nothing for them to show.
func (s *Service) footerInfo(r *http.Request, st content.Settings) themes.FooterInfo {
	f := st.Footer
	d := detailsInfo(st.Details)
	has := map[string]bool{"phone": d.Phone != "", "whatsapp": d.WhatsApp != "", "email": d.Email != "", "address": d.Address != "", "hours": d.Hours != "", "social": len(d.Social) > 0}
	out := themes.FooterInfo{Bottom: f.BottomLine(st.Title, time.Now().Year()), Classes: f.Style.Classes(), Style: f.Style.CSSVars()}
	for _, row := range f.Rows {
		fr := themes.FooterRow{Ratio: row.Ratio, Align: row.Align}
		for _, col := range row.Columns {
			var fc themes.FooterColumn
			for _, w := range col.Widgets {
				fw := themes.FooterWidget{Type: w.Type, Heading: w.Heading, Text: w.Text, Inline: w.Inline, Button: w.Button, Success: w.Success}
				for _, l := range w.Links {
					fw.Links = append(fw.Links, themes.NavLink{Label: l.Label, URL: l.URL, NewTab: l.NewTab, Active: l.URL == r.URL.Path})
				}
				switch w.Type {
				case footer.TypeContact:
					fw.Show = map[string]bool{}
					for _, p := range w.Parts {
						if has[p] {
							fw.Show[p] = true
						}
					}
					if len(fw.Show) == 0 {
						continue
					}
				case footer.TypeSocial:
					if !has["social"] {
						continue
					}
				}
				if w.Type == footer.TypeNewsletter {
					fw.Form = s.newsletterFormData(r, 0)
				}
				if w.Type == footer.TypeLogo { // the alternative logo when asked for and set, else the main one
					fw.Logo = st.Brand.Logo
					if w.Logo == footer.LogoAlternative && st.Brand.AltLogo != "" {
						fw.Logo = st.Brand.AltLogo
					}
				}
				fc.Widgets = append(fc.Widgets, fw)
			}
			fr.Columns = append(fr.Columns, fc)
		}
		out.Rows = append(out.Rows, fr)
	}
	return out
}

// detailsInfo turns the saved contact details into what templates show, with the links made.
func detailsInfo(d content.Details) themes.DetailsInfo {
	out := themes.DetailsInfo{
		Phone: d.Phone, PhoneLink: content.PhoneLink(d.Phone), WhatsApp: d.WhatsApp, WhatsAppLink: content.WhatsAppLink(d.WhatsApp),
		Email: d.Email, Address: d.Address, Hours: d.Hours, InFooter: d.InFooter,
	}
	for _, n := range content.Networks {
		if u := d.Social[n.Key]; u != "" {
			out.Social = append(out.Social, themes.SocialLink{Key: n.Key, Label: n.Label, URL: u})
		}
	}
	return out
}

// searchPage shows the results for ?q=…. Results pages are never cached or indexed: they are
// endless variations that search engines shouldn't crawl.
func (s *Service) searchPage(w http.ResponseWriter, r *http.Request) {
	if s.Search == nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	st, err := s.settings(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	theme, themePreview := s.themeFor(r, st)

	q := search.Clean(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)

	data := themes.SearchPage{Query: q, Page: 1, Pages: 1}
	status := http.StatusOK
	if q != "" {
		if !s.Search.Allow(r) {
			data.Limited, status = true, http.StatusTooManyRequests
		} else {
			res, err := s.Search.Run(ctx, q, page)
			if err != nil {
				serverError(w, err)
				return
			}
			if page > 1 && len(res.Items) == 0 {
				s.notFound(w, r, st, theme)
				return
			}
			data.Total, data.Page, data.Pages = res.Total, res.Page, res.Pages
			for _, it := range res.Items {
				data.Items = append(data.Items, themes.SearchItem{Kind: it.Kind, Title: it.Title, Path: it.Path, Snippet: it.Snippet})
			}
			link := func(p int) string {
				v := url.Values{"q": {q}}
				if p > 1 {
					v.Set("page", strconv.Itoa(p))
				}
				return "/search?" + v.Encode()
			}
			if data.Page > 1 {
				data.PrevURL = link(data.Page - 1)
			}
			if data.Page < data.Pages {
				data.NextURL = link(data.Page + 1)
			}
		}
	}

	body, err := theme.RenderSection("search/results", data)
	if err != nil {
		serverError(w, err)
		return
	}
	nav, err := s.nav(ctx, r.URL.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	title := "Search"
	if q != "" {
		title = "Search: " + q
	}
	s.write(w, theme, status, true, themes.PageData{
		Site:      s.siteInfo(r, theme, st, themePreview),
		PageTitle: title,
		SEO:       buildSEO(seoInput{st: st, base: baseURL(r, st), pageTitle: title, hidden: true}),
		Nav:       nav, Content: body, Year: time.Now().Year(),
	})
}
