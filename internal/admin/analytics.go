package admin

import (
	"fmt"
	"net/http"
	"strconv"

	"webbuilder/internal/analytics"
	"webbuilder/internal/auth"
	"webbuilder/internal/content"
)

// Anyone who can log in may read the numbers; only admins decide what is collected.
func (h *Handler) registerAnalytics(mux *http.ServeMux, authed, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/analytics", authed(h.analyticsPage))
	mux.Handle("POST /admin/analytics/settings", admin(h.analyticsSettings))
	mux.Handle("POST /admin/analytics/tracking", admin(h.analyticsTracking))
	mux.Handle("POST /admin/analytics/delete", admin(h.analyticsDelete))
}

var analyticsRanges = []int{7, 30, 90}

const (
	chartW = 720.0
	chartH = 160.0
)

// bar is one day of the chart, with coordinates already formatted for the SVG.
type bar struct {
	X, W                 string // the slot
	ViewsY, ViewsH       string
	VisitorsY, VisitorsH string
	Label                string // tooltip text
}

// tableRow is one line of a "top …" table; Max sizes the little bar.
type tableRow struct {
	Name            string
	Views, Visitors int
	Pct             int
	Max             int
}

type rangeLink struct {
	Days    int
	Current bool
}

type analyticsScreen struct {
	Report                              analytics.Report
	Bars                                []bar
	Ranges                              []rangeLink
	Enabled                             bool
	IsAdmin                             bool
	Retention                           int // days of history kept
	Empty                               bool
	ChartW                              string
	ChartH                              string
	Pages, Referrers, Devices, Browsers []tableRow

	// outside analytics services (Google Analytics, Plausible), set up by an admin
	Tracking    content.Tracking
	ConsentAsks bool // the cookie notice offers Accept and Decline, which the services wait for
}

func f1(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

func chart(series []analytics.Day) []bar {
	maxViews := 1
	for _, d := range series {
		maxViews = max(maxViews, d.Views)
	}
	slot := chartW / float64(len(series))
	out := make([]bar, len(series))
	for i, d := range series {
		vh := chartH * float64(d.Views) / float64(maxViews)
		uh := chartH * float64(d.Visitors) / float64(maxViews)
		out[i] = bar{
			X: f1(float64(i)*slot + slot*0.15), W: f1(slot * 0.7),
			ViewsY: f1(chartH - vh), ViewsH: f1(vh), VisitorsY: f1(chartH - uh), VisitorsH: f1(uh),
			Label: fmt.Sprintf("%s: %d view%s, %d visitor%s", d.Date.Format("Mon 2 Jan"), d.Views, plural(d.Views), d.Visitors, plural(d.Visitors)),
		}
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func rows(cs []analytics.Count, total int) []tableRow {
	out := make([]tableRow, len(cs))
	top := 1
	for _, c := range cs {
		top = max(top, c.Views)
	}
	for i, c := range cs {
		pct := 0
		if total > 0 {
			pct = c.Views * 100 / total
		}
		out[i] = tableRow{c.Name, c.Views, c.Visitors, pct, top}
	}
	return out
}

func (h *Handler) renderAnalytics(w http.ResponseWriter, r *http.Request, status int, notice, errMsg string, days int) {
	rep, err := h.Analytics.Report(r.Context(), days)
	if err != nil {
		serverError(w, err)
		return
	}
	id := auth.CurrentIdentity(r)
	s := analyticsScreen{
		Report: rep, Bars: chart(rep.Series), Enabled: h.Analytics.Enabled(), IsAdmin: id.Role == auth.RoleAdmin,
		Retention: int(analytics.Retention.Hours() / 24), Empty: rep.Views == 0,
		ChartW: f1(chartW), ChartH: f1(chartH),
		Pages: rows(rep.Pages, rep.Views), Referrers: rows(rep.Referrers, rep.Views),
		Devices: rows(rep.Devices, rep.Views), Browsers: rows(rep.Browsers, rep.Views),
	}
	for _, d := range analyticsRanges {
		s.Ranges = append(s.Ranges, rangeLink{d, d == days})
	}
	if st, err := h.Content.Settings(r.Context()); err == nil {
		s.Tracking, s.ConsentAsks = st.Tracking, st.Consent.Enabled && st.Consent.Ask
	}
	h.render(w, r, status, "analytics", pageData{
		Title: "Analytics", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Data: s,
	})
}

func (h *Handler) analyticsPage(w http.ResponseWriter, r *http.Request) {
	days := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		for _, ok := range analyticsRanges {
			if n == ok {
				days = n
			}
		}
	}
	notice := map[string]string{"saved": "Analytics settings saved.", "deleted": "All analytics data deleted.", "tracking": "Tracking services saved."}[r.URL.Query().Get("done")]
	h.renderAnalytics(w, r, 200, notice, "", days)
}

func (h *Handler) analyticsSettings(w http.ResponseWriter, r *http.Request) {
	if err := h.Analytics.SetEnabled(r.Context(), r.PostFormValue("enabled") == "on"); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/analytics?done=saved", http.StatusSeeOther)
}

// analyticsTracking saves the outside analytics services. Only the service's ID is entered, never a script, and it
// is checked against the shape that service uses, so nothing typed here can run code on the site.
func (h *Handler) analyticsTracking(w http.ResponseWriter, r *http.Request) {
	t := content.Tracking{
		GA4:             r.PostFormValue("ga4"),
		PlausibleDomain: r.PostFormValue("plausible"),
		NoWait:          r.PostFormValue("wait") != "on",
	}
	switch {
	case !content.ValidGA4(t.GA4):
		h.renderAnalytics(w, r, 400, "", "A Google Analytics ID looks like G-ABC123XYZ (find it under Admin, Data streams).", 30)
		return
	case !content.ValidPlausible(t.PlausibleDomain):
		h.renderAnalytics(w, r, 400, "", "Enter your site's domain as it is set up in Plausible, such as example.com.", 30)
		return
	}
	if err := h.Content.SetTracking(r.Context(), t); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/analytics?done=tracking", http.StatusSeeOther)
}

func (h *Handler) analyticsDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.Analytics.DeleteAll(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/analytics?done=deleted", http.StatusSeeOther)
}
