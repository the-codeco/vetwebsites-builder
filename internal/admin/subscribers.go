package admin

import (
	"encoding/csv"
	"net/http"
	"strings"
	"time"

	"webbuilder/internal/auth"
	"webbuilder/internal/newsletter"
)

// The list is personal data, so only admins see it, export it or remove from it.
func (h *Handler) registerSubscribers(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/subscribers", admin(h.subscribersPage))
	mux.Handle("GET /admin/subscribers.csv", admin(h.subscribersCSV))
	mux.Handle("POST /admin/subscribers/{id}/delete", admin(h.subscriberDelete))
}

type subscribersScreen struct {
	Subscribers        []newsletter.Subscriber
	Confirmed, Pending int
	DoubleOptIn        bool // signups are confirmed by email
	MailReady          bool
	HasDomain          bool
}

func (h *Handler) subscribersPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := h.Newsletter.List(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	confirmed, pending, err := h.Newsletter.Counts(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	st, err := h.Content.Settings(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	mail := h.Newsletter.Mail.Configured()
	id := auth.CurrentIdentity(r)
	notice := map[string]string{"deleted": "Subscriber removed."}[r.URL.Query().Get("done")]
	h.render(w, r, 200, "subscribers", pageData{
		Title: "Subscribers", User: id, CSRF: id.CSRF, Notice: notice,
		Data: subscribersScreen{list, confirmed, pending, mail && (st.Domain != "" || h.LinkBase != ""), mail, st.Domain != "" || h.LinkBase != ""},
	})
}

// csvCell stops a spreadsheet from running a cell as a formula: a cell that starts with one of
// these characters is prefixed with an apostrophe. (A valid address can start with + or -.)
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// subscribersCSV exports confirmed addresses, ready to import into a mailing tool.
func (h *Handler) subscribersCSV(w http.ResponseWriter, r *http.Request) {
	list, err := h.Newsletter.List(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/csv; charset=utf-8")
	hd.Set("Content-Disposition", `attachment; filename="subscribers-`+time.Now().Format("2006-01-02")+`.csv"`)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "no-store")
	cw := csv.NewWriter(w)
	cw.Write([]string{"email", "subscribed"})
	for _, s := range list {
		if s.Confirmed() {
			cw.Write([]string{csvCell(s.Email), s.CreatedAt.Format("2006-01-02")})
		}
	}
	cw.Flush()
}

func (h *Handler) subscriberDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := h.Newsletter.Delete(r.Context(), id); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/subscribers?done=deleted", http.StatusSeeOther)
}
