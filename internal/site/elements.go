package site

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"webbuilder/internal/auth"
	"webbuilder/internal/elements"
	"webbuilder/internal/themes"
)

// elementsInfo is the site-wide elements that apply to this page. An element that has already ended is left out, so
// nothing dead is sent; one that has not started yet is sent, and the visitor's browser holds it back until its time,
// because a published copy has no server to do that. A logged-in person can open a page with ?wb-element=<id> to see
// one now, whatever its window.
func (s *Service) elementsInfo(r *http.Request, details themes.DetailsInfo) *themes.ElementsInfo {
	list, err := s.content.Elements(r.Context())
	if err != nil {
		log.Printf("site: elements: %v", err)
		return nil
	}
	if len(list) == 0 {
		return nil
	}
	previewID := int64(0)
	if auth.CurrentIdentity(r) != nil {
		previewID, _ = strconv.ParseInt(r.URL.Query().Get("wb-element"), 10, 64)
	}
	now := time.Now()
	var out themes.ElementsInfo
	for _, e := range list {
		preview := e.ID == previewID
		if !preview && (!e.Enabled || e.StatusAt(now) == elements.StatusEnded || !e.ShowsOn(r.URL.Path)) {
			continue
		}
		v := themes.ElementView{ID: e.ID, Kind: e.Kind, Version: e.Version(themes.Fingerprint), Preview: preview,
			DismissDays: e.Config.DismissDays, Dismissible: e.Config.Dismissible}
		if e.Start != nil {
			v.Start = e.Start.UTC().Format(time.RFC3339)
		}
		if e.End != nil {
			v.End = e.End.UTC().Format(time.RFC3339)
		}
		c := e.Config
		switch e.Kind {
		case elements.KindBar, elements.KindAlert:
			v.Text, v.Link, v.Label, v.Style = c.Text, c.Link, c.LinkLabel, c.Style
			out.Top = append(out.Top, v)
		case elements.KindPopup:
			v.Heading, v.Text, v.Trigger, v.Delay, v.Scroll = c.Heading, c.Text, c.Trigger, c.Delay, c.Scroll
			v.Link, v.Label = c.Link, c.LinkLabel
			if c.Mode == elements.ModeSubscribe {
				v.Subscribe = true
				v.Form = s.newsletterFormData(r, 0)
				v.Button, v.Thanks = c.Button, c.Success
				if !v.Form.Sent && v.Form.Token == "" { // the site has no newsletter to sign up for
					continue
				}
			}
			out.Bottom = append(out.Bottom, v)
		case elements.KindContact:
			v.Action, v.Label, v.Side = c.Action, c.Label, c.Side
			switch c.Action {
			case elements.ActionCall:
				v.Link = details.PhoneLink
			case elements.ActionWhatsApp:
				v.Link = details.WhatsAppLink
			default:
				v.Link = c.Link
			}
			if v.Link == "" { // the number it calls is not in Site details (yet)
				continue
			}
			out.Bottom = append(out.Bottom, v)
		}
	}
	if len(out.Top) == 0 && len(out.Bottom) == 0 {
		return nil
	}
	return &out
}
