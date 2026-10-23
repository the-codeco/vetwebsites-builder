package admin

import (
	"net/http"
	"strconv"
	"strings"

	"webbuilder/internal/auth"
	"webbuilder/internal/content"
)

func (h *Handler) registerSiteDetails(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/details", admin(h.detailsPage))
	mux.Handle("POST /admin/details", admin(h.detailsSave))
}

type socialField struct {
	Key, Label, Value string
}

// numberSlot and emailSlot are the form's rows for extra numbers and addresses; empty rows are left out on save.
type numberSlot struct {
	N int
	content.ContactNumber
}

type emailSlot struct {
	N int
	content.ContactEmail
}

type detailsScreen struct {
	D       content.Details
	Social  []socialField
	Numbers []numberSlot
	Emails  []emailSlot
	Kinds   []struct{ Key, Label string }
	Errors  map[string]string // a message for each field that was not accepted
}

func (h *Handler) renderDetails(w http.ResponseWriter, r *http.Request, status int, d content.Details, notice, errMsg string, bad map[string]string) {
	id := auth.CurrentIdentity(r)
	s := detailsScreen{D: d, Errors: bad, Kinds: content.NumberKinds}
	for _, n := range content.Networks {
		s.Social = append(s.Social, socialField{n.Key, n.Label, d.Social[n.Key]})
	}
	for i := 0; i < content.MaxNumbers; i++ {
		slot := numberSlot{N: i + 1}
		if i < len(d.Numbers) {
			slot.ContactNumber = d.Numbers[i]
		}
		s.Numbers = append(s.Numbers, slot)
	}
	for i := 0; i < content.MaxEmails; i++ {
		slot := emailSlot{N: i + 1}
		if i < len(d.Emails) {
			slot.ContactEmail = d.Emails[i]
		}
		s.Emails = append(s.Emails, slot)
	}
	h.render(w, r, status, "sitedetails", pageData{Title: "Site details", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Data: s})
}

func (h *Handler) detailsPage(w http.ResponseWriter, r *http.Request) {
	st, err := h.Content.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	notice := ""
	if r.URL.Query().Get("done") == "saved" {
		notice = "Site details saved."
	}
	h.renderDetails(w, r, 200, st.Details, notice, "", nil)
}

// detailsSave saves the contact details. A value that is not usable (a number with too few digits, an address that
// is not on the network it claims) is reported against its field and nothing is saved, so the owner never sees a
// detail silently disappear.
func (h *Handler) detailsSave(w http.ResponseWriter, r *http.Request) {
	in := content.Details{
		Phone: strings.TrimSpace(r.PostFormValue("phone")), WhatsApp: strings.TrimSpace(r.PostFormValue("whatsapp")),
		Email: strings.TrimSpace(r.PostFormValue("email")), Address: r.PostFormValue("address"), Hours: r.PostFormValue("hours"),
		Social: map[string]string{},
	}
	for _, n := range content.Networks {
		if v := strings.TrimSpace(r.PostFormValue("social_" + n.Key)); v != "" {
			in.Social[n.Key] = v
		}
	}
	bad := map[string]string{}
	for i := 1; i <= content.MaxNumbers; i++ {
		n := strconv.Itoa(i)
		cn := content.ContactNumber{Kind: r.PostFormValue("number_kind_" + n), Label: r.PostFormValue("number_label_" + n), Number: strings.TrimSpace(r.PostFormValue("number_" + n))}
		switch {
		case cn.Number == "" && strings.TrimSpace(cn.Label) == "":
			continue // an unused row
		case content.PhoneLink(cn.Number) == "":
			bad["number_"+n] = "Enter a number with 5 to 15 digits, such as 011 555 0123."
		}
		in.Numbers = append(in.Numbers, cn)
	}
	for i := 1; i <= content.MaxEmails; i++ {
		n := strconv.Itoa(i)
		ce := content.ContactEmail{Label: r.PostFormValue("email_label_" + n), Email: strings.TrimSpace(r.PostFormValue("extra_email_" + n))}
		switch {
		case ce.Email == "" && strings.TrimSpace(ce.Label) == "":
			continue
		case !content.ValidEmail(ce.Email):
			bad["extra_email_"+n] = "That does not look like an email address."
		}
		in.Emails = append(in.Emails, ce)
	}
	if in.Phone != "" && content.PhoneLink(in.Phone) == "" {
		bad["phone"] = "Enter a phone number with 5 to 15 digits, such as +27 21 555 0123."
	}
	if in.WhatsApp != "" && content.WhatsAppLink(in.WhatsApp) == "" {
		bad["whatsapp"] = "Enter the number with its country code, such as +27 82 555 0123."
	}
	if in.Email != "" && content.CleanDetails(map[string]any{"email": in.Email})["email"] == nil {
		bad["email"] = "That does not look like an email address."
	}
	for _, n := range content.Networks {
		if v := in.Social[n.Key]; v != "" && content.SocialURL(n, v) == "" {
			bad["social_"+n.Key] = "Use the full https:// address of your " + n.Label + " page."
		}
	}
	if len(bad) > 0 {
		h.renderDetails(w, r, 400, in, "", "Some details need fixing. Nothing was saved.", bad)
		return
	}
	if err := h.Content.SetDetails(r.Context(), in); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/details?done=saved", http.StatusSeeOther)
}
