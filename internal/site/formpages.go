package site

import (
	"html"
	"net/http"
	"net/url"
	"strings"
)

// A published static copy of a website has no pages of its own for the links in the emails it causes (confirm a
// newsletter signup, leave the newsletter) and no way to count a visit. Those live on the builder, under the same
// /f/<site token>/ address as the forms, and are written here: standalone pages in the website's name, and a beacon.

// SiteURLHeader is how the platform tells these handlers the address the website is published at. The platform always
// sets it itself (a visitor's own value is thrown away), so it can be trusted as far as being a web address.
const SiteURLHeader = "X-WB-Site-URL"

func siteURLFrom(r *http.Request) string {
	u, err := url.Parse(r.Header.Get(SiteURLHeader))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}

// remotePage writes a short page. body is trusted HTML made by the callers below, with every dynamic value escaped.
func (s *Service) remotePage(w http.ResponseWriter, r *http.Request, status int, title, body string) {
	site := "the website"
	if st, err := s.settings(r.Context()); err == nil && st.Title != "" {
		site = st.Title
	}
	back := ""
	if u := siteURLFrom(r); u != "" {
		back = `<p><a href="` + html.EscapeString(u) + `">← Back to ` + html.EscapeString(site) + `</a></p>`
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer") // the address carries a personal token
	h.Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex">` +
		`<title>` + html.EscapeString(title) + ` · ` + html.EscapeString(site) + `</title>` +
		`<style>body{font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;background:#f4f5f8;color:#1f2430;margin:0;padding:3rem 1rem}` +
		`main{max-width:30rem;margin:0 auto;background:#fff;border-radius:12px;padding:2rem;box-shadow:0 2px 12px rgba(0,0,0,.08)}` +
		`.site{margin:0 0 1.5rem;color:#667085;font-size:.9rem;text-transform:uppercase;letter-spacing:.06em}h1{font-size:1.4rem;margin:0 0 1rem}` +
		`button{font:inherit;font-weight:600;padding:.7em 1.4em;border:0;border-radius:8px;background:#3b5bdb;color:#fff;cursor:pointer}a{color:#3b5bdb}</style>` +
		`<body><main><p class="site">` + html.EscapeString(site) + `</p><h1>` + html.EscapeString(title) + `</h1>` + body + back + `</main></body></html>`))
}

const linkNotValid = "<p>This link isn't valid any more. It may have expired or already been used.</p>"

// remoteTokenAction shows a page with one button for an action that follows an emailed link. The action itself needs the
// button, so a mail program that opens the link to check it changes nothing.
func (s *Service) remoteTokenAction(w http.ResponseWriter, r *http.Request, title, intro, button string) {
	ok, err := s.Newsletter.Exists(r.Context(), r.PathValue("token"))
	switch {
	case err != nil:
		s.remotePage(w, r, http.StatusInternalServerError, "Something went wrong", "<p>Please try again in a moment.</p>")
	case !ok:
		s.remotePage(w, r, http.StatusNotFound, "Link not valid", linkNotValid)
	default:
		s.remotePage(w, r, http.StatusOK, title, "<p>"+html.EscapeString(intro)+`</p><form method="post"><button type="submit">`+html.EscapeString(button)+`</button></form>`)
	}
}

func (s *Service) formConfirmPage(w http.ResponseWriter, r *http.Request) {
	s.remoteTokenAction(w, r, "Confirm your subscription", "Press the button to start receiving updates.", "Confirm subscription")
}

func (s *Service) formConfirm(w http.ResponseWriter, r *http.Request) {
	ok, err := s.Newsletter.Confirm(r.Context(), r.PathValue("token"))
	switch {
	case err != nil:
		s.remotePage(w, r, http.StatusInternalServerError, "Something went wrong", "<p>Please try again in a moment.</p>")
	case !ok:
		s.remotePage(w, r, http.StatusNotFound, "Link not valid", linkNotValid)
	default:
		s.remotePage(w, r, http.StatusOK, "You're subscribed", "<p>Thank you — your address is confirmed.</p>")
	}
}

func (s *Service) formUnsubscribePage(w http.ResponseWriter, r *http.Request) {
	s.remoteTokenAction(w, r, "Unsubscribe", "Press the button to stop receiving updates and remove your address from the list.", "Unsubscribe")
}

// formUnsubscribe is also where a mail program's one-click unsubscribe (List-Unsubscribe-Post) lands.
func (s *Service) formUnsubscribe(w http.ResponseWriter, r *http.Request) {
	ok, err := s.Newsletter.Unsubscribe(r.Context(), r.PathValue("token"))
	switch {
	case err != nil:
		s.remotePage(w, r, http.StatusInternalServerError, "Something went wrong", "<p>Please try again in a moment.</p>")
	case !ok:
		s.remotePage(w, r, http.StatusNotFound, "Link not valid", "<p>This link isn't valid any more. You may already be unsubscribed.</p>")
	default:
		s.remotePage(w, r, http.StatusOK, "You've been unsubscribed", "<p>Your address has been removed from the list.</p>")
	}
}

// formBeacon counts a visit reported by a published copy's own script. It always answers "no content": the page
// reporting neither knows nor cares whether the visit was counted.
func (s *Service) formBeacon(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if s.Beacon != nil && r.ParseForm() == nil {
		own := ""
		if u, err := url.Parse(siteURLFrom(r)); err == nil {
			own = u.Host
		}
		s.Beacon(r, strings.TrimSpace(r.PostFormValue("p")), strings.TrimSpace(r.PostFormValue("r")), own)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
