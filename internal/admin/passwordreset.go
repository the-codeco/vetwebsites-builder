package admin

import (
	"errors"
	"log"
	"net/http"
	"regexp"

	"webbuilder/internal/auth"
	"webbuilder/internal/mailer"
)

// domainRe is what a saved site domain must look like before a reset link is built from it.
var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9\-]{1,23}$`)

// forgotMessage is the answer to every "forgot password" request, whether or not the address has an account,
// so the form can't be used to find out who has one.
const forgotMessage = "If that address has an account and this site can send email, a link to choose a new password is on its way. It works once, for an hour. If nothing arrives, ask another admin to make you a reset link."

// resetBase is where reset links point. It comes from the site's saved domain, never from the request: a request
// can claim any Host it likes, and a link built from it could send the token to someone else's server. A site with
// no domain saved sends no reset email.
func (h *Handler) resetBase(r *http.Request) string {
	if h.BaseURL != "" {
		return h.BaseURL // the platform serves one address, set by its owner
	}
	if h.Content == nil {
		return ""
	}
	st, err := h.Content.Settings(r.Context())
	if err != nil || !domainRe.MatchString(st.Domain) {
		return ""
	}
	return "https://" + st.Domain
}

func (h *Handler) forgotForm(w http.ResponseWriter, r *http.Request) {
	if auth.CurrentIdentity(r) != nil {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	h.render(w, r, 200, "forgot", pageData{Title: "Forgot your password?", CSRF: h.Auth.PreCSRF(w, r)})
}

func (h *Handler) forgotSubmit(w http.ResponseWriter, r *http.Request) {
	page := func(status int, notice, errMsg string) {
		h.render(w, r, status, "forgot", pageData{Title: "Forgot your password?", CSRF: h.Auth.PreCSRF(w, r), Notice: notice, Error: errMsg})
	}
	if !h.Auth.CheckPreCSRF(r) {
		page(400, "", "Session expired, please try again.")
		return
	}
	email := auth.NormalizeEmail(r.PostFormValue("email"))
	ipKey, emailKey := "ip:"+h.Auth.ClientIP(r), "email:"+email
	if h.Auth.Resets.Blocked(ipKey) || h.Auth.Resets.Blocked(emailKey) {
		page(http.StatusTooManyRequests, "", "Too many requests. Try again in an hour.")
		return
	}
	// every request counts, successful or not, so the limit holds whether or not the address exists
	h.Auth.Resets.Fail(ipKey)
	if email != "" {
		h.Auth.Resets.Fail(emailKey)
	}
	if email != "" {
		u, _, err := h.Auth.Store.UserByEmail(r.Context(), email)
		switch {
		case errors.Is(err, auth.ErrNotFound):
		case err != nil:
			serverError(w, err)
			return
		default:
			base := h.resetBase(r)
			if base != "" && h.Mail.Configured() {
				token, err := h.Auth.Store.CreateReset(r.Context(), u.ID)
				if err != nil {
					serverError(w, err)
					return
				}
				// sent in the background, so the answer takes the same time whether or not the address has an account
				go h.sendReset(u.Email, base+"/reset/"+token)
			}
		}
	}
	page(200, forgotMessage, "")
}

func (h *Handler) sendReset(to, link string) {
	body := "Someone asked to choose a new password for your account on this site.\n\n" +
		"To choose one, open this link within the hour (it works once):\n\n" + link + "\n\n" +
		"If you did not ask for this, ignore this email: your password has not changed.\n"
	if err := h.Mail.Send(to, "", "Choose a new password", body); err != nil {
		log.Printf("password reset email to %s failed: %v", mailer.CleanLine(to), err)
	}
}

func (h *Handler) resetForm(w http.ResponseWriter, r *http.Request) {
	email, err := h.Auth.Store.ResetEmail(r.Context(), r.PathValue("token"))
	if err != nil {
		h.render(w, r, http.StatusNotFound, "reset", pageData{Title: "Link not valid", Data: resetData{Invalid: true}})
		return
	}
	h.render(w, r, 200, "reset", pageData{Title: "Choose a new password", CSRF: h.Auth.PreCSRF(w, r), Data: resetData{Email: email}})
}

type resetData struct {
	Email   string
	Invalid bool
}

func (h *Handler) resetSubmit(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	email, err := h.Auth.Store.ResetEmail(r.Context(), token)
	if err != nil {
		h.render(w, r, http.StatusNotFound, "reset", pageData{Title: "Link not valid", Data: resetData{Invalid: true}})
		return
	}
	fail := func(msg string) {
		h.render(w, r, 400, "reset", pageData{Title: "Choose a new password", CSRF: h.Auth.PreCSRF(w, r), Error: msg, Data: resetData{Email: email}})
	}
	if !h.Auth.CheckPreCSRF(r) {
		fail("Session expired, please try again.")
		return
	}
	pw := r.PostFormValue("password")
	if msg := auth.ValidatePassword(pw); msg != "" {
		fail(msg)
		return
	}
	if pw != r.PostFormValue("confirm") {
		fail("Passwords do not match.")
		return
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		serverError(w, err)
		return
	}
	u, err := h.Auth.Store.CompleteReset(r.Context(), token, hash)
	if errors.Is(err, auth.ErrInvalidToken) {
		h.render(w, r, http.StatusNotFound, "reset", pageData{Title: "Link not valid", Data: resetData{Invalid: true}})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// a changed password is reported to the address it belongs to, so an unexpected change is noticed
	if h.Mail.Configured() {
		go func() {
			if err := h.Mail.Send(u.Email, "", "Your password was changed", "The password for your account on this site was just changed using a reset link.\n\nIf this was not you, contact the site's administrator now.\n"); err != nil {
				log.Printf("password change notice to %s failed: %v", mailer.CleanLine(u.Email), err)
			}
		}()
	}
	h.Auth.Logins.Reset("email:" + auth.NormalizeEmail(u.Email))
	if err := h.Auth.StartSession(w, r, u.ID); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// userReset lets an admin make a reset link for a user (who forgot their password) when email is not set up. The link
// is shown once, to the admin, who passes it on.
func (h *Handler) userReset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	email, err := h.Auth.Store.UserEmail(r.Context(), id)
	if errors.Is(err, auth.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	token, err := h.Auth.Store.CreateReset(r.Context(), id)
	if err != nil {
		serverError(w, err)
		return
	}
	scheme := "http"
	if h.Auth.SecureCookies || h.Auth.IsHTTPS(r) {
		scheme = "https"
	}
	h.renderUsersWith(w, r, 200, usersData{ResetLink: scheme + "://" + r.Host + "/reset/" + token, ResetFor: email})
}
