package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"webbuilder/internal/auth"
)

// PublishBar is what the bar at the top of every editor screen shows: whether the site can be published, and whether it has
// changes that are not published yet.
type PublishBar struct {
	Configured    bool       // hosting has been set up
	Unpublished   int64      // changes made since the last publish
	Publishing    bool       // a publish is running now
	LastPublished *time.Time // when the site was last published
	NeverBuilt    bool       // no preview copy has been made yet
}

// HostingForm is the hosting settings as shown. The password is never shown: only whether one is stored.
type HostingForm struct {
	Protocol, Host, User, Dir, TLSName, PublicURL string
	Port                                          int
	HasPassword                                   bool
	HostKey                                       string
	AutoPublish                                   bool // publish by itself when scheduled pages and posts go live
}

// HostingInput is what is submitted: the form, and a password when a new one was typed.
type HostingInput struct {
	HostingForm
	Password string
}

// TestResult is the answer to "test the connection".
type TestResult struct {
	OK         bool
	Message    string
	HostKey    string // the SFTP server's identity, as remembered or just seen
	KeyChanged bool   // the server's identity is not the one remembered
	NewKey     string
}

// JobRow is one publish.
type JobRow struct {
	ID                           int64
	Status                       string // queued, running, done, failed
	By, Version, Message, Error  string
	Warnings                     []string
	Created                      time.Time
	Started, Finished            *time.Time
	FilesTotal, FilesDone        int
	Uploaded, Deleted, Unchanged int
	Bytes                        int64
	Force                        bool
	RestoreVersion               string     // not empty: this publish put an earlier copy back
	RestoreOf                    int64      // the publish that copy came from
	RestoreOfDate                *time.Time // when that one finished, if it is still in the history
	Rollback                     bool
	Current                      bool // this is the copy on the hosting now
	CanRestore                   bool // its copy is kept and can be put back
}

// Active says whether the publish is still going.
func (j JobRow) Active() bool { return j.Status == "queued" || j.Status == "running" }

// Percent is how far along a running publish is.
func (j JobRow) Percent() int {
	if j.FilesTotal <= 0 {
		return 0
	}
	return min(100, j.FilesDone*100/j.FilesTotal)
}

// PublishState is everything the publish screen shows.
type PublishState struct {
	Hosting      HostingForm
	Configured   bool
	Unpublished  int64
	Active       *JobRow
	Jobs         []JobRow
	LastBuild    *time.Time
	PreviewStale bool // changes were made after the preview copy
}

// PublishService is what the editor's publish screens need from the platform, for one website.
type PublishService interface {
	Bar(ctx context.Context) (PublishBar, error)
	State(ctx context.Context) (PublishState, error)
	SaveHosting(ctx context.Context, actor string, in HostingInput) error
	TestHosting(ctx context.Context, actor string) (TestResult, error)
	TrustHostKey(ctx context.Context, actor string) error
	Start(ctx context.Context, actor string, force bool) (int64, error)
	Rollback(ctx context.Context, actor string, jobID int64) (int64, error)
	Job(ctx context.Context, id int64) (JobRow, error)
}

func (h *Handler) registerPublish(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/publish", authed(h.publishPage))
	mux.Handle("POST /admin/publish/start", authed(h.publishStart))
	mux.Handle("POST /admin/publish/rollback", authed(h.publishRollback))
	mux.Handle("GET /admin/publish/status", authed(h.publishStatus))
	mux.Handle("GET /admin/hosting", authed(h.hostingPage))
	mux.Handle("POST /admin/hosting", authed(h.hostingSave))
	mux.Handle("POST /admin/hosting/test", authed(h.hostingTest))
	mux.Handle("POST /admin/hosting/trust", authed(h.hostingTrust))
}

type publishScreen struct {
	PublishState
	Done string
}

func (h *Handler) publishPage(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	st, err := h.Publish.State(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	d := publishScreen{PublishState: st}
	notice := map[string]string{"started": "Publishing has started. You can keep working; this page shows how it is going.", "rollback": "Putting that version back has started. Your hosting shows it when this page says it is done."}[r.URL.Query().Get("done")]
	h.render(w, r, 200, "publish", pageData{Title: "Publish", User: id, CSRF: id.CSRF, Notice: notice, Data: d})
}

func (h *Handler) publishStart(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	var ue UserError
	_, err := h.Publish.Start(r.Context(), id.Email, r.PostFormValue("force") == "1")
	switch {
	case errors.As(err, &ue):
		st, e2 := h.Publish.State(r.Context())
		if e2 != nil {
			serverError(w, e2)
			return
		}
		h.render(w, r, 400, "publish", pageData{Title: "Publish", User: id, CSRF: id.CSRF, Error: ue.Error(), Data: publishScreen{PublishState: st}})
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/publish?done=started", http.StatusSeeOther)
}

func (h *Handler) publishRollback(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	job, _ := strconv.ParseInt(r.PostFormValue("job"), 10, 64)
	var ue UserError
	_, err := h.Publish.Rollback(r.Context(), id.Email, job)
	switch {
	case errors.As(err, &ue):
		st, e2 := h.Publish.State(r.Context())
		if e2 != nil {
			serverError(w, e2)
			return
		}
		h.render(w, r, 400, "publish", pageData{Title: "Publish", User: id, CSRF: id.CSRF, Error: ue.Error(), Data: publishScreen{PublishState: st}})
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/publish?done=rollback", http.StatusSeeOther)
}

// publishStatus is the part of the publish screen that updates itself while a publish runs.
func (h *Handler) publishStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.Publish.State(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.pages["publish"].ExecuteTemplate(w, "publishstatus", publishScreen{PublishState: st}); err != nil {
		serverError(w, err)
	}
}

type hostingScreen struct {
	Form   HostingForm
	Test   *TestResult
	Done   string
	Secret bool // the server can store a password (SECRET_KEY is set)
}

func (h *Handler) renderHosting(w http.ResponseWriter, r *http.Request, status int, form HostingForm, test *TestResult, notice, errMsg string) {
	id := auth.CurrentIdentity(r)
	h.render(w, r, status, "hosting", pageData{Title: "Hosting", User: id, CSRF: id.CSRF, Notice: notice, Error: errMsg, Data: hostingScreen{Form: form, Test: test}})
}

func (h *Handler) hostingPage(w http.ResponseWriter, r *http.Request) {
	st, err := h.Publish.State(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	notice := map[string]string{"saved": "Hosting settings saved. Use “Test the connection” to check them."}[r.URL.Query().Get("done")]
	h.renderHosting(w, r, 200, st.Hosting, nil, notice, "")
}

func (h *Handler) hostingSave(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	port, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("port")))
	in := HostingInput{
		HostingForm: HostingForm{
			Protocol: r.PostFormValue("protocol"), Host: strings.TrimSpace(r.PostFormValue("host")), Port: port,
			User: strings.TrimSpace(r.PostFormValue("user")), Dir: strings.TrimSpace(r.PostFormValue("dir")),
			TLSName: strings.TrimSpace(r.PostFormValue("tls_name")), PublicURL: strings.TrimSpace(r.PostFormValue("public_url")),
			AutoPublish: r.PostFormValue("auto_publish") == "on",
		},
		Password: r.PostFormValue("password"),
	}
	var ue UserError
	switch err := h.Publish.SaveHosting(r.Context(), id.Email, in); {
	case errors.As(err, &ue):
		in.HasPassword = in.Password != ""
		h.renderHosting(w, r, 400, in.HostingForm, nil, "", ue.Error())
		return
	case err != nil:
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/hosting?done=saved", http.StatusSeeOther)
}

func (h *Handler) hostingTest(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	res, err := h.Publish.TestHosting(r.Context(), id.Email)
	var ue UserError
	if errors.As(err, &ue) {
		h.renderHosting(w, r, 400, HostingForm{}, nil, "", ue.Error())
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	st, err := h.Publish.State(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	h.renderHosting(w, r, 200, st.Hosting, &res, "", "")
}

func (h *Handler) hostingTrust(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	if err := h.Publish.TrustHostKey(r.Context(), id.Email); err != nil {
		var ue UserError
		if errors.As(err, &ue) {
			h.renderHosting(w, r, 400, HostingForm{}, nil, "", ue.Error())
			return
		}
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/hosting?done=saved", http.StatusSeeOther)
}
