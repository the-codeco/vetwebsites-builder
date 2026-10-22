package admin

import (
	"errors"
	"net/http"
	"strconv"

	"webbuilder/internal/auth"
)

func (h *Handler) registerAccount(mux *http.ServeMux, authed, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/account", authed(h.accountPage))
	mux.Handle("POST /admin/account/password", authed(h.accountPassword))
}

func (h *Handler) renderAccount(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string) {
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "account", pageData{Title: "Your account", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg})
}

func (h *Handler) accountPage(w http.ResponseWriter, r *http.Request) {
	notice := ""
	if r.URL.Query().Get("done") == "password" {
		notice = "Password changed. You've been signed out everywhere else."
	}
	h.renderAccount(w, r, 200, notice, "")
}

func (h *Handler) accountPassword(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	// guessing the current password from a hijacked session is still guessing: limit it
	key := "pw:" + strconv.FormatInt(id.UserID, 10)
	if h.Auth.Logins.Blocked(key) {
		h.renderAccount(w, r, http.StatusTooManyRequests, "", "Too many failed attempts. Try again in 15 minutes.")
		return
	}
	current, next, confirm := r.PostFormValue("current"), r.PostFormValue("password"), r.PostFormValue("confirm")
	if len(current) > auth.MaxPasswordLen {
		current = current[:auth.MaxPasswordLen]
	}
	hash, err := h.Auth.Store.PasswordHash(r.Context(), id.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	if !auth.VerifyPassword(current, hash) {
		h.Auth.Logins.Fail(key)
		h.renderAccount(w, r, http.StatusUnauthorized, "", "Your current password is incorrect.")
		return
	}
	switch {
	case auth.ValidatePassword(next) != "":
		h.renderAccount(w, r, 400, "", auth.ValidatePassword(next))
		return
	case next != confirm:
		h.renderAccount(w, r, 400, "", "The new passwords do not match.")
		return
	case next == current:
		h.renderAccount(w, r, 400, "", "Choose a password different from your current one.")
		return
	}
	newHash, err := auth.HashPassword(next)
	if err != nil {
		serverError(w, err)
		return
	}
	keep := ""
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		keep = c.Value
	}
	if err := h.Auth.Store.SetPassword(r.Context(), id.UserID, newHash, keep); err != nil {
		serverError(w, err)
		return
	}
	h.Auth.Logins.Reset(key)
	http.Redirect(w, r, "/admin/account?done=password", http.StatusSeeOther)
}

func (h *Handler) userDelete(w http.ResponseWriter, r *http.Request) {
	target, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if target == auth.CurrentIdentity(r).UserID {
		h.renderUsers(w, r, 400, "", "You can't remove your own account. Ask another admin, or change your password instead.")
		return
	}
	err := h.Auth.Store.DeleteUser(r.Context(), target)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, auth.ErrLastAdmin):
		h.renderUsers(w, r, 400, "", "That's the last admin account, so it can't be removed.")
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/users?done=removed", http.StatusSeeOther)
}
