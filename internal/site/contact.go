package site

import (
	"context"
	"html"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"webbuilder/internal/contact"
	"webbuilder/internal/themes"
)

// fromRe limits where a submission may send the visitor back to: a page on this
// site (the slugs the editor allows), never an address a form could make up.
var fromRe = regexp.MustCompile(`^(/|/[a-z0-9]+(-[a-z0-9]+)*)$`)

// contactFormData is what a contact form block shows: a fresh token, the page
// to return to, and whether this block has just been used successfully.
func (s *Service) contactFormData(r *http.Request, blockID int64) themes.ContactForm {
	if s.Contact == nil {
		return themes.ContactForm{}
	}
	return themes.ContactForm{
		Token: s.Contact.Token(time.Now()),
		Path:  r.URL.Path,
		Sent:  r.URL.Query().Get("sent") == strconv.FormatInt(blockID, 10),
	}
}

// contactSubmit receives the contact form. Success redirects back to the page
// (so reloading doesn't send it twice); a problem shows a themed page with a way back.
func (s *Service) contactSubmit(w http.ResponseWriter, r *http.Request) {
	if s.Contact == nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
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
	done := from + "?sent=" + strconv.FormatInt(blockID, 10) + "#contact-" + strconv.FormatInt(blockID, 10)

	if res := s.acceptContact(r); res.Problem != "" {
		s.formProblem(w, r, res.Status, res.Problem, from)
		return
	}
	http.Redirect(w, r, done, http.StatusSeeOther)
}

// submitResult is how a form submission ended: Problem is a message for the visitor (with the HTTP status to
// send it under), empty on success. Silent marks a bot's submission, which is answered as if it had worked.
type submitResult struct {
	Status  int
	Problem string
	Silent  bool
}

// acceptContact checks and stores a contact message from an already parsed form, and emails a copy.
func (s *Service) acceptContact(r *http.Request) submitResult {
	// A real visitor never sees the "website" field. Pretend it worked so bots don't adapt.
	if r.PostFormValue("website") != "" {
		return submitResult{Silent: true}
	}
	if !s.Contact.CheckToken(r.PostFormValue("t"), time.Now()) {
		return submitResult{http.StatusBadRequest, "This form has expired or was sent too quickly. Please go back, reload the page and try again.", false}
	}
	in, problem := contact.Validate(contact.Input{
		Name: r.PostFormValue("name"), Email: r.PostFormValue("email"),
		Subject: r.PostFormValue("subject"), Body: r.PostFormValue("message"),
	})
	if problem != "" {
		return submitResult{http.StatusBadRequest, problem, false}
	}
	if !s.Contact.Allow(s.Contact.ClientIP(r)) {
		return submitResult{http.StatusTooManyRequests, "Too many messages have been sent from your connection. Please try again later.", false}
	}
	id, err := s.Contact.Save(r.Context(), in)
	if err != nil {
		log.Printf("contact: %v", err)
		return submitResult{http.StatusInternalServerError, "Sorry, your message couldn't be saved. Please try again in a moment.", false}
	}
	st, err := s.settings(r.Context())
	if err == nil && s.Contact.Mail.Configured() {
		go s.emailCopy(st.Title, baseURL(r, st)+"/admin/messages/"+strconv.FormatInt(id, 10), in)
	}
	return submitResult{}
}

// emailCopy sends the notification. It runs after the visitor has been answered, and a
// failure only goes to the log: the message is already safe in the inbox.
func (s *Service) emailCopy(siteTitle, adminURL string, in contact.Input) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	to, err := s.Contact.NotifyEmail(ctx)
	cancel()
	if err != nil || to == "" {
		return
	}
	if err := s.Contact.Mail.Send(to, in.Email, contact.MessageSubject(siteTitle, in), contact.MessageBody(in, adminURL)); err != nil {
		log.Printf("contact: emailing a copy failed: %v", err)
	}
}

// formProblem shows what went wrong inside the site's own layout.
func (s *Service) formProblem(w http.ResponseWriter, r *http.Request, status int, msg, back string) {
	s.infoPage(w, r, status, "Message not sent", "<p>"+html.EscapeString(msg)+`</p><p><a href="`+html.EscapeString(back)+`">← Back</a></p>`)
}
