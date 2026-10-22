package admin

import (
	"net/http"
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

type detailsScreen struct {
	D      content.Details
	Social []socialField
	Errors map[string]string // a message for each field that was not accepted
}

func (h *Handler) renderDetails(w http.ResponseWriter, r *http.Request, status int, d content.Details, notice, errMsg string, bad map[string]string) {
	id := auth.CurrentIdentity(r)
	s := detailsScreen{D: d, Errors: bad}
	for _, n := range content.Networks {
		s.Social = append(s.Social, socialField{n.Key, n.Label, d.Social[n.Key]})
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
		InFooter: r.PostFormValue("in_footer") == "on", Social: map[string]string{},
	}
	for _, n := range content.Networks {
		if v := strings.TrimSpace(r.PostFormValue("social_" + n.Key)); v != "" {
			in.Social[n.Key] = v
		}
	}
	bad := map[string]string{}
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
