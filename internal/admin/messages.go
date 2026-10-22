package admin

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"webbuilder/internal/auth"
	"webbuilder/internal/contact"
	"webbuilder/internal/mailer"
)

// Everyone who can log in may read and delete messages; choosing where copies are
// emailed is a site setting, so only admins.
func (h *Handler) registerMessages(mux *http.ServeMux, authed, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/messages", authed(h.messagesList))
	mux.Handle("POST /admin/messages/settings", admin(h.messagesSettings))
	mux.Handle("GET /admin/messages/{id}", authed(h.messageView))
	mux.Handle("POST /admin/messages/{id}/reply", authed(h.messageReply))
	mux.Handle("POST /admin/messages/{id}/unread", authed(h.messageUnread))
	mux.Handle("POST /admin/messages/{id}/delete", authed(h.messageDelete))
}

type messagesScreen struct {
	Messages    []contact.Message
	Unread      int
	NotifyEmail string
	MailReady   bool // SMTP is configured, so copies can be emailed
	IsAdmin     bool
}

func (h *Handler) renderMessages(w http.ResponseWriter, r *http.Request, status int, notice, errMsg, notify string) {
	ctx := r.Context()
	list, err := h.Contact.List(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	saved, err := h.Contact.NotifyEmail(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	if notify == "" {
		notify = saved
	}
	unread := 0
	for _, m := range list {
		if !m.Read {
			unread++
		}
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "messages", pageData{
		Title: "Messages", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg,
		Data: messagesScreen{list, unread, notify, h.Contact.Mail.Configured(), id.Role == auth.RoleAdmin},
	})
}

func (h *Handler) messagesList(w http.ResponseWriter, r *http.Request) {
	notice := map[string]string{"saved": "Notification settings saved.", "deleted": "Message deleted."}[r.URL.Query().Get("done")]
	h.renderMessages(w, r, 200, notice, "", "")
}

func (h *Handler) messagesSettings(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("notify_email"))
	if email != "" && !mailer.ValidEmail(email) {
		h.renderMessages(w, r, 400, "", "Enter a valid email address, or leave it empty to turn emails off.", email)
		return
	}
	if err := h.Contact.SetNotifyEmail(r.Context(), email); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/messages?done=saved", http.StatusSeeOther)
}

type messageScreen struct {
	Message   contact.Message
	Reply     string // a mailto: link that opens a reply in the owner's own mail program
	Replies   []contact.Reply
	MailReady bool   // the site can send the reply itself
	Me        string // replies to the visitor's reply come back here
	Text      string // what was typed, kept when it was rejected
}

func (h *Handler) messageView(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	m, err := h.Contact.Get(r.Context(), id)
	if errors.Is(err, contact.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if !m.Read {
		if err := h.Contact.SetRead(r.Context(), id, true); err != nil {
			serverError(w, err)
			return
		}
		m.Read = true
	}
	notice := map[string]string{"replied": "Reply sent."}[r.URL.Query().Get("done")]
	h.renderMessage(w, r, http.StatusOK, notice, "", m, "")
}

func (h *Handler) renderMessage(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string, m contact.Message, text string) {
	replies, err := h.Contact.Replies(r.Context(), m.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	subject := m.Subject
	if subject == "" {
		subject = "your message"
	}
	reply := fmt.Sprintf("mailto:%s?subject=%s", m.Email, url.PathEscape("Re: "+subject))
	ident := auth.CurrentIdentity(r)
	h.render(w, r, status, "message", pageData{
		Title: "Message from " + m.Name, User: ident, CSRF: ident.CSRF, Notice: notice, Error: errMsg,
		Data: messageScreen{m, reply, replies, h.Contact.Mail.Configured(), ident.Email, text},
	})
}

// messageReply emails an answer to the visitor and keeps a copy with the message.
func (h *Handler) messageReply(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	m, err := h.Contact.Get(r.Context(), id)
	if errors.Is(err, contact.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	text := r.PostFormValue("reply")
	body, problem := contact.CleanReply(text)
	if problem != "" {
		h.renderMessage(w, r, http.StatusBadRequest, "", problem, m, text)
		return
	}
	me := auth.CurrentIdentity(r)
	switch err := h.Contact.Reply(r.Context(), m, me.UserID, me.Email, body); {
	case err == nil:
		if !m.Read {
			h.Contact.SetRead(r.Context(), m.ID, true)
		}
		http.Redirect(w, r, "/admin/messages/"+strconv.FormatInt(m.ID, 10)+"?done=replied", http.StatusSeeOther)
	case errors.Is(err, contact.ErrNoMail):
		h.renderMessage(w, r, http.StatusBadRequest, "", `Email isn't set up on this server, so the site can't send the reply. Use "Reply by email" to answer from your own mail program.`, m, text)
	case errors.Is(err, contact.ErrTooMany):
		h.renderMessage(w, r, http.StatusTooManyRequests, "", "You have sent a lot of replies in the last hour. Please wait a while before sending more.", m, text)
	default:
		log.Printf("contact reply: %v", err)
		h.renderMessage(w, r, http.StatusBadGateway, "", "The reply couldn't be sent, and nothing was saved. Check the email settings and the server's log, then try again.", m, text)
	}
}

func (h *Handler) messageUnread(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := h.Contact.SetRead(r.Context(), id, false); err != nil && !errors.Is(err, contact.ErrNotFound) {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/messages", http.StatusSeeOther)
}

func (h *Handler) messageDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := h.Contact.Delete(r.Context(), id); err != nil && !errors.Is(err, contact.ErrNotFound) {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/messages?done=deleted", http.StatusSeeOther)
}
