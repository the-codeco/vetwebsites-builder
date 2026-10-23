package site

import (
	"context"
	"html"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"

	"webbuilder/internal/newsletter"
	"webbuilder/internal/themes"
)

func (s *Service) registerNewsletter(mux *http.ServeMux) {
	mux.HandleFunc("POST /subscribe", s.subscribeSubmit)
	mux.HandleFunc("GET /subscribe/confirm/{token}", s.confirmPage)
	mux.HandleFunc("POST /subscribe/confirm/{token}", s.confirmSubmit)
	mux.HandleFunc("GET /unsubscribe/{token}", s.unsubscribePage)
	mux.HandleFunc("POST /unsubscribe/{token}", s.unsubscribeSubmit)
}

// mustConfirm says whether signups are confirmed by email. That needs working email and a saved
// domain: the confirmation link must point at the real site, never at whatever Host header a
// stranger's request happened to carry. Without both, signups count immediately.
func (s *Service) mustConfirm(domain string) bool {
	return s.Newsletter.Mail.Configured() && domain != ""
}

// newsletterFormData is what a signup block shows: a fresh token, the page to return to,
// and whether this block has just been used.
func (s *Service) newsletterFormData(r *http.Request, blockID int64) themes.NewsletterForm {
	if s.Newsletter == nil {
		return themes.NewsletterForm{}
	}
	st, err := s.settings(r.Context())
	if err != nil {
		return themes.NewsletterForm{}
	}
	return themes.NewsletterForm{
		Token:   s.Newsletter.Token(time.Now()),
		Path:    r.URL.Path,
		Sent:    r.URL.Query().Get("subscribed") == strconv.FormatInt(blockID, 10),
		Confirm: s.mustConfirm(st.Domain),
	}
}

func (s *Service) subscribeSubmit(w http.ResponseWriter, r *http.Request) {
	if s.Newsletter == nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	from := r.PostFormValue("from")
	if !fromRe.MatchString(from) {
		from = "/"
	}
	blockID, err := strconv.ParseInt(r.PostFormValue("block"), 10, 64)
	if err != nil || blockID < 0 {
		blockID = 0
	}
	id := strconv.FormatInt(blockID, 10)
	done := from + "?subscribed=" + id + "#newsletter-" + id

	if res := s.acceptSubscribe(r); res.Problem != "" {
		s.infoPage(w, r, res.Status, "Not subscribed", "<p>"+html.EscapeString(res.Problem)+`</p><p><a href="`+html.EscapeString(from)+`">← Back</a></p>`)
		return
	}
	// the same answer whether or not the address was already on the list
	http.Redirect(w, r, done, http.StatusSeeOther)
}

// acceptSubscribe checks a signup from an already parsed form and stores it, emailing the confirmation when one is needed.
func (s *Service) acceptSubscribe(r *http.Request) submitResult {
	// A real visitor never sees the "website" field. Pretend it worked so bots don't adapt.
	if r.PostFormValue("website") != "" {
		return submitResult{Silent: true}
	}
	if !s.Newsletter.CheckToken(r.PostFormValue("t"), time.Now()) {
		return submitResult{http.StatusBadRequest, "This form has expired or was sent too quickly. Please go back, reload the page and try again.", false}
	}
	email, ok := newsletter.Normalize(r.PostFormValue("email"))
	if !ok {
		return submitResult{http.StatusBadRequest, "Please enter a valid email address.", false}
	}
	if !s.Newsletter.Allow(s.Newsletter.ClientIP(r)) {
		return submitResult{http.StatusTooManyRequests, "Too many signups have been sent from your connection. Please try again later.", false}
	}
	st, err := s.settings(r.Context())
	if err != nil {
		log.Printf("newsletter: %v", err)
		return submitResult{http.StatusInternalServerError, "Sorry, we couldn't save your signup. Please try again in a moment.", false}
	}
	token, err := s.Newsletter.Subscribe(r.Context(), email, s.mustConfirm(st.Domain))
	if err != nil {
		log.Printf("newsletter: %v", err)
		return submitResult{http.StatusInternalServerError, "Sorry, we couldn't save your signup. Please try again in a moment.", false}
	}
	if token != "" {
		go s.emailConfirmation(st.Title, baseURL(r, st), token, email)
	}
	return submitResult{}
}

// emailConfirmation runs after the visitor has been answered; a failure only goes to the log.
func (s *Service) emailConfirmation(siteTitle, base, token, to string) {
	body := newsletter.ConfirmBody(siteTitle, base+"/subscribe/confirm/"+token, base+"/unsubscribe/"+token)
	if err := s.Newsletter.Mail.Send(to, "", newsletter.ConfirmSubject(siteTitle), body); err != nil {
		log.Printf("newsletter: confirmation email failed: %v", err)
	}
}

// --- confirm / unsubscribe: a page with a button, so a mail scanner opening the link changes nothing ---

func (s *Service) tokenAction(w http.ResponseWriter, r *http.Request, action, title, intro, button string) {
	token := r.PathValue("token")
	ok, err := s.Newsletter.Exists(r.Context(), token)
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok {
		s.infoPage(w, r, http.StatusNotFound, "Link not valid", "<p>This link isn't valid any more. It may have expired or already been used.</p>")
		return
	}
	s.infoPage(w, r, http.StatusOK, title, "<p>"+html.EscapeString(intro)+`</p><form method="post" action="/`+action+`/`+html.EscapeString(token)+`"><button class="btn btn-primary" type="submit">`+html.EscapeString(button)+`</button></form>`)
}

func (s *Service) confirmPage(w http.ResponseWriter, r *http.Request) {
	if s.Newsletter == nil {
		http.NotFound(w, r)
		return
	}
	s.tokenAction(w, r, "subscribe/confirm", "Confirm your subscription", "Press the button to start receiving updates.", "Confirm subscription")
}

func (s *Service) confirmSubmit(w http.ResponseWriter, r *http.Request) {
	if s.Newsletter == nil {
		http.NotFound(w, r)
		return
	}
	ok, err := s.Newsletter.Confirm(r.Context(), r.PathValue("token"))
	switch {
	case err != nil:
		serverError(w, err)
	case !ok:
		s.infoPage(w, r, http.StatusNotFound, "Link not valid", "<p>This link isn't valid any more. It may have expired or already been used.</p>")
	default:
		s.infoPage(w, r, http.StatusOK, "You're subscribed", `<p>Thank you — your address is confirmed.</p><p><a href="/">Back to the site</a></p>`)
	}
}

func (s *Service) unsubscribePage(w http.ResponseWriter, r *http.Request) {
	if s.Newsletter == nil {
		http.NotFound(w, r)
		return
	}
	s.tokenAction(w, r, "unsubscribe", "Unsubscribe", "Press the button to stop receiving updates and remove your address from our list.", "Unsubscribe")
}

func (s *Service) unsubscribeSubmit(w http.ResponseWriter, r *http.Request) {
	if s.Newsletter == nil {
		http.NotFound(w, r)
		return
	}
	ok, err := s.Newsletter.Unsubscribe(r.Context(), r.PathValue("token"))
	switch {
	case err != nil:
		serverError(w, err)
	case !ok:
		s.infoPage(w, r, http.StatusNotFound, "Link not valid", "<p>This link isn't valid any more. You may already be unsubscribed.</p>")
	default:
		s.infoPage(w, r, http.StatusOK, "You've been unsubscribed", `<p>Your address has been removed from our list.</p><p><a href="/">Back to the site</a></p>`)
	}
}

// infoPage shows a short message inside the site's own layout. It is never indexed or cached.
// body is trusted HTML built by the callers above, with every dynamic value escaped.
func (s *Service) infoPage(w http.ResponseWriter, r *http.Request, status int, title, body string) {
	st, err := s.settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	theme, themePreview := s.themeFor(r, st)
	nav, _ := s.nav(context.WithoutCancel(r.Context()), "/")
	s.write(w, theme, status, true, themes.PageData{
		Site:      s.siteInfo(r, theme, st, themePreview),
		PageTitle: title,
		SEO:       buildSEO(seoInput{st: st, base: baseURL(r, st), pageTitle: title, hidden: true}),
		Nav:       nav,
		Content: template.HTML(`<section class="block pad-md align-center width-normal"><div class="block-inner">` +
			`<h1>` + html.EscapeString(title) + `</h1>` + body + `</div></section>`),
		Year: time.Now().Year(),
	})
}
