package site

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"webbuilder/internal/auth"
	"webbuilder/internal/content"
	"webbuilder/internal/search"
	"webbuilder/internal/themes"
)

// siteInfo is what every layout receives about the site, including whether to show the search box.
func siteInfo(r *http.Request, theme *themes.Theme, st content.Settings, preview bool) themes.SiteInfo {
	info := theme.Info(st.Title, preview)
	info.Search = st.ShowSearch
	info.ColorMode = theme.EffectiveColorMode(st.ColorMode)
	info.Footer = themes.FooterInfo{Bottom: st.Footer.BottomLine(st.Title, time.Now().Year())}
	for _, c := range st.Footer.Columns {
		info.Footer.Columns = append(info.Footer.Columns, themes.FooterColumn{Heading: c.Heading, Text: c.Text})
	}
	if c := st.Consent; c.Enabled {
		info.Consent = &themes.ConsentInfo{
			Text: c.Message(), LinkLabel: c.LinkLabel, LinkURL: c.LinkURL, Ask: c.Ask,
			Version: themes.Fingerprint([]byte(fmt.Sprint(c.Ask, "|", c.Message(), "|", c.LinkLabel, "|", c.LinkURL))),
		}
	}
	info.Brand = themes.BrandInfo{Logo: st.Brand.Logo, Alt: st.Brand.LogoAlt, Icon: st.Brand.Icon, Height: st.Brand.HeightPixels(), ShowTitle: st.Brand.Logo == "" || !st.Brand.HideTitle}
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

// detailsInfo turns the saved contact details into what templates show, with the links made.
func detailsInfo(d content.Details) themes.DetailsInfo {
	out := themes.DetailsInfo{
		Phone: d.Phone, PhoneLink: content.PhoneLink(d.Phone), WhatsApp: d.WhatsApp, WhatsAppLink: content.WhatsAppLink(d.WhatsApp),
		Email: d.Email, Address: d.Address, Hours: d.Hours, InFooter: d.InFooter,
	}
	for _, n := range content.Networks {
		if u := d.Social[n.Key]; u != "" {
			out.Social = append(out.Social, themes.SocialLink{Label: n.Label, URL: u})
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
		Site:      siteInfo(r, theme, st, themePreview),
		PageTitle: title,
		SEO:       buildSEO(seoInput{st: st, base: baseURL(r, st), pageTitle: title, hidden: true}),
		Nav:       nav, Content: body, Year: time.Now().Year(),
	})
}
