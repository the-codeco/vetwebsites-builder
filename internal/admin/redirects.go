package admin

import (
	"errors"
	"fmt"
	"net/http"

	"webbuilder/internal/auth"
	"webbuilder/internal/content"
)

func (h *Handler) registerRedirects(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/redirects", authed(h.redirectsList))
	mux.Handle("POST /admin/redirects", authed(h.redirectAdd))
	mux.Handle("POST /admin/redirects/{id}/delete", authed(h.redirectDelete))
}

type redirectsData struct {
	List     []content.Redirect
	From, To string
	Temp     bool
}

func (h *Handler) renderRedirects(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string, form redirectsData) {
	id := auth.CurrentIdentity(r)
	list, err := h.Content.Redirects(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	form.List = list
	h.render(w, r, status, "redirects", pageData{Title: "Redirects", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Data: form})
}

func (h *Handler) redirectsList(w http.ResponseWriter, r *http.Request) {
	notice := ""
	switch r.URL.Query().Get("done") {
	case "added":
		notice = "Redirect added."
	case "deleted":
		notice = "Redirect removed."
	}
	h.renderRedirects(w, r, 200, notice, "", redirectsData{})
}

func (h *Handler) redirectAdd(w http.ResponseWriter, r *http.Request) {
	form := redirectsData{From: r.PostFormValue("from"), To: r.PostFormValue("to"), Temp: r.PostFormValue("kind") == "temporary"}
	from, err := content.CleanRedirectFrom(form.From)
	if err != nil {
		h.renderRedirects(w, r, 400, "", err.Error(), form)
		return
	}
	to, err := content.CleanRedirectTo(form.To)
	if err == nil {
		err = content.CheckRedirectStar(from, to)
	}
	if errors.Is(err, content.ErrRedirectLoop) {
		h.renderRedirects(w, r, 400, "", "That would send visitors round in a circle.", form)
		return
	}
	if err != nil {
		h.renderRedirects(w, r, 400, "", err.Error(), form)
		return
	}
	switch err = h.Content.AddRedirect(r.Context(), from, to, !form.Temp); {
	case errors.Is(err, content.ErrRedirectExists):
		h.renderRedirects(w, r, 409, "", "There is already a redirect from "+from+". Remove it first to change where it goes.", form)
		return
	case errors.Is(err, content.ErrRedirectLoop):
		h.renderRedirects(w, r, 400, "", "That would send visitors round in a circle.", form)
		return
	case errors.Is(err, content.ErrRedirectsFull):
		h.renderRedirects(w, r, 400, "", fmt.Sprintf("The list holds at most %d redirects. Remove some you no longer need.", content.MaxRedirects), form)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/redirects?done=added", http.StatusSeeOther)
}

func (h *Handler) redirectDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch err := h.Content.DeleteRedirect(r.Context(), id); {
	case errors.Is(err, content.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/redirects?done=deleted", http.StatusSeeOther)
}
