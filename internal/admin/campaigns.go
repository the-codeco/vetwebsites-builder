package admin

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"webbuilder/internal/auth"
	"webbuilder/internal/campaign"
)

// Newsletters go to the subscriber list, which is personal data and the site's reputation as a sender,
// so only admins write and send them.
func (h *Handler) registerCampaigns(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/newsletters", admin(h.campaignsPage))
	mux.Handle("POST /admin/newsletters", admin(h.campaignCreate))
	mux.Handle("GET /admin/newsletters/{id}", admin(h.campaignPage))
	mux.Handle("GET /admin/newsletters/{id}/progress", admin(h.campaignProgress))
	mux.Handle("POST /admin/newsletters/{id}", admin(h.campaignSave))
	mux.Handle("POST /admin/newsletters/{id}/test", admin(h.campaignTest))
	mux.Handle("POST /admin/newsletters/{id}/send", admin(h.campaignSend))
	mux.Handle("POST /admin/newsletters/{id}/cancel", admin(h.campaignCancel))
	mux.Handle("POST /admin/newsletters/{id}/delete", admin(h.campaignDelete))
}

type campaignsScreen struct {
	Campaigns  []campaign.Campaign
	Recipients int
	MailReady  bool
	HasDomain  bool
	Subject    string // kept when a new newsletter was rejected
	Body       string
}

type campaignScreen struct {
	Campaign   campaign.Campaign
	Recipients int  // how many people it would go to now
	MailReady  bool // SMTP is set up
	HasDomain  bool // the unsubscribe links need the site's real address
	CanSend    bool
	Footer     string
	Failures   []campaign.Failure
	Me         string // where a test goes
}

// sendBase is the address the unsubscribe links are made from: the site's saved domain, never a Host header a
// visitor could have forged.
func (h *Handler) sendBase(r *http.Request) (base, title string, err error) {
	st, err := h.Content.Settings(r.Context())
	if err != nil {
		return "", "", err
	}
	if h.LinkBase != "" { // a published copy: its confirm and unsubscribe pages are on the platform
		return h.LinkBase, st.Title, nil
	}
	if st.Domain == "" {
		return "", st.Title, nil
	}
	return "https://" + st.Domain, st.Title, nil
}

func (h *Handler) renderCampaigns(w http.ResponseWriter, r *http.Request, status int, notice, errMsg, subject, body string) {
	ctx := r.Context()
	list, err := h.Campaigns.List(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	n, err := h.Campaigns.Recipients(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	base, _, err := h.sendBase(r)
	if err != nil {
		serverError(w, err)
		return
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "campaigns", pageData{
		Title: "Newsletters", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg,
		Data: campaignsScreen{list, n, h.Campaigns.Mail.Configured(), base != "", subject, body},
	})
}

func (h *Handler) campaignsPage(w http.ResponseWriter, r *http.Request) {
	notice := map[string]string{"deleted": "Newsletter deleted."}[r.URL.Query().Get("done")]
	h.renderCampaigns(w, r, http.StatusOK, notice, "", "", "")
}

func (h *Handler) campaignCreate(w http.ResponseWriter, r *http.Request) {
	subject, body, problem := campaign.Clean(r.PostFormValue("subject"), r.PostFormValue("body"))
	if problem != "" {
		h.renderCampaigns(w, r, http.StatusBadRequest, "", problem, r.PostFormValue("subject"), r.PostFormValue("body"))
		return
	}
	id, err := h.Campaigns.Create(r.Context(), auth.CurrentIdentity(r).UserID, subject, body)
	if err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/newsletters/"+strconv.FormatInt(id, 10)+"?done=created", http.StatusSeeOther)
}

func (h *Handler) loadCampaign(w http.ResponseWriter, r *http.Request) (campaign.Campaign, bool) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return campaign.Campaign{}, false
	}
	c, err := h.Campaigns.Get(r.Context(), id)
	if errors.Is(err, campaign.ErrNotFound) {
		http.NotFound(w, r)
		return campaign.Campaign{}, false
	}
	if err != nil {
		serverError(w, err)
		return campaign.Campaign{}, false
	}
	return c, true
}

func (h *Handler) renderCampaign(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string, c campaign.Campaign) {
	ctx := r.Context()
	n, err := h.Campaigns.Recipients(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	base, title, err := h.sendBase(r)
	if err != nil {
		serverError(w, err)
		return
	}
	d := campaignScreen{Campaign: c, Recipients: n, MailReady: h.Campaigns.Mail.Configured(), HasDomain: base != ""}
	d.CanSend = d.MailReady && d.HasDomain && n > 0
	d.Footer = campaign.Footer(title, "(each person's own unsubscribe link)")
	if c.Finished() {
		if d.Failures, err = h.Campaigns.Failures(ctx, c.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	id := auth.CurrentIdentity(r)
	d.Me = id.Email
	h.render(w, r, status, "campaign", pageData{Title: c.Subject, User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Data: d})
}

func (h *Handler) campaignPage(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadCampaign(w, r)
	if !ok {
		return
	}
	notice := map[string]string{
		"created": "Draft saved.", "saved": "Draft saved.", "test": "Test sent to your own address.",
		"started": "Sending has started.", "canceled": "Sending stopped. Messages already sent can't be recalled.",
	}[r.URL.Query().Get("done")]
	h.renderCampaign(w, r, http.StatusOK, notice, "", c)
}

// campaignProgress is the fragment the page polls while a newsletter is going out. When sending is over it asks the
// browser to reload the page, so the buttons and summary change too.
func (h *Handler) campaignProgress(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadCampaign(w, r)
	if !ok {
		return
	}
	if !c.Sending() {
		w.Header().Set("HX-Refresh", "true")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.pages["campaign"].ExecuteTemplate(w, "progress", c); err != nil {
		log.Printf("render progress: %v", err)
	}
}

func (h *Handler) campaignSave(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadCampaign(w, r)
	if !ok {
		return
	}
	subject, body, problem := campaign.Clean(r.PostFormValue("subject"), r.PostFormValue("body"))
	if problem != "" {
		c.Subject, c.Body = r.PostFormValue("subject"), r.PostFormValue("body")
		h.renderCampaign(w, r, http.StatusBadRequest, "", problem, c)
		return
	}
	if err := h.Campaigns.Update(r.Context(), c.ID, subject, body); err != nil {
		h.campaignError(w, r, c, err)
		return
	}
	http.Redirect(w, r, "/admin/newsletters/"+strconv.FormatInt(c.ID, 10)+"?done=saved", http.StatusSeeOther)
}

// campaignError turns the service's errors into a message on the newsletter's own page.
func (h *Handler) campaignError(w http.ResponseWriter, r *http.Request, c campaign.Campaign, err error) {
	var changed campaign.ErrListChanged
	switch {
	case errors.As(err, &changed):
		h.renderCampaign(w, r, http.StatusConflict, "", fmt.Sprintf("Nothing was sent: the list changed while you were reading and now has %d subscribers. Check it and press Send again.", changed.Now), c)
	case errors.Is(err, campaign.ErrNotDraft), errors.Is(err, campaign.ErrSending):
		h.renderCampaign(w, r, http.StatusConflict, "", err.Error()+".", c)
	case errors.Is(err, campaign.ErrNoMail):
		h.renderCampaign(w, r, http.StatusBadRequest, "", "Email isn't set up on this server, so nothing can be sent (see SMTP_HOST and SMTP_FROM in .env.example).", c)
	case errors.Is(err, campaign.ErrNoReceiver):
		h.renderCampaign(w, r, http.StatusBadRequest, "", "Nobody is subscribed yet, so there is nobody to send it to.", c)
	case errors.Is(err, campaign.ErrNotFound):
		http.NotFound(w, r)
	default:
		serverError(w, err)
	}
}

func (h *Handler) campaignTest(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadCampaign(w, r)
	if !ok {
		return
	}
	_, title, err := h.sendBase(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if err := h.Campaigns.SendTest(auth.CurrentIdentity(r).Email, c.Subject, c.Body, title); err != nil {
		if errors.Is(err, campaign.ErrNoMail) {
			h.campaignError(w, r, c, err)
			return
		}
		log.Printf("newsletter test: %v", err)
		h.renderCampaign(w, r, http.StatusBadGateway, "", "The test email couldn't be sent. Check the email settings and the server's log.", c)
		return
	}
	http.Redirect(w, r, "/admin/newsletters/"+strconv.FormatInt(c.ID, 10)+"?done=test", http.StatusSeeOther)
}

func (h *Handler) campaignSend(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadCampaign(w, r)
	if !ok {
		return
	}
	base, title, err := h.sendBase(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if base == "" {
		h.renderCampaign(w, r, http.StatusBadRequest, "", "Save your site's address on the Domain page first: every email carries an unsubscribe link, and it has to point at your real site.", c)
		return
	}
	// the owner confirms the number they were shown, so a list that changed in the meantime is noticed
	expect, err := strconv.Atoi(r.PostFormValue("recipients"))
	if err != nil || expect < 1 {
		h.renderCampaign(w, r, http.StatusBadRequest, "", "Open the newsletter again and press Send from there.", c)
		return
	}
	if _, err := h.Campaigns.Start(r.Context(), c.ID, base, title, expect); err != nil {
		h.campaignError(w, r, c, err)
		return
	}
	http.Redirect(w, r, "/admin/newsletters/"+strconv.FormatInt(c.ID, 10)+"?done=started", http.StatusSeeOther)
}

func (h *Handler) campaignCancel(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadCampaign(w, r)
	if !ok {
		return
	}
	if err := h.Campaigns.Cancel(r.Context(), c.ID); err != nil {
		h.campaignError(w, r, c, err)
		return
	}
	http.Redirect(w, r, "/admin/newsletters/"+strconv.FormatInt(c.ID, 10)+"?done=canceled", http.StatusSeeOther)
}

func (h *Handler) campaignDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := h.loadCampaign(w, r)
	if !ok {
		return
	}
	if err := h.Campaigns.Delete(r.Context(), c.ID); err != nil {
		h.campaignError(w, r, c, err)
		return
	}
	http.Redirect(w, r, "/admin/newsletters?done=deleted", http.StatusSeeOther)
}
