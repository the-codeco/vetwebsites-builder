package admin

import (
	"crypto/subtle"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"

	"webbuilder/internal/analytics"
	"webbuilder/internal/auth"
	"webbuilder/internal/backup"
	"webbuilder/internal/blocks"
	"webbuilder/internal/campaign"
	"webbuilder/internal/contact"
	"webbuilder/internal/content"
	"webbuilder/internal/domain"
	"webbuilder/internal/mailer"
	"webbuilder/internal/media"
	"webbuilder/internal/menus"
	"webbuilder/internal/newsletter"
	"webbuilder/internal/themes"
	"webbuilder/web"
)

//go:embed templates/*.html
var tmplFS embed.FS

// Deps are the services the admin screens use.
type Deps struct {
	Auth    *auth.Auth
	Content *content.Store
	Media   *media.Service
	Engine  *themes.Engine
	Domain  *domain.Service
	Checker *domain.Checker
	// Analytics records and reports page views.
	Analytics *analytics.Service
	// Contact stores and mails the messages sent through contact form blocks.
	Contact *contact.Service
	// Newsletter keeps the signup list.
	Newsletter *newsletter.Service
	// Campaigns writes and sends newsletters to that list.
	Campaigns *campaign.Service
	// Backup makes, saves and restores backups of the site.
	Backup *backup.Service
	// BackupKeep and BackupPersonal describe the scheduled backups, so the Backup screen can say what they are.
	BackupKeep     int
	BackupPersonal bool
	// Drafts holds block edits that are typed but not saved, for the live preview (shared with the site, which shows them).
	Drafts *blocks.Drafts
	// Mail sends the password reset emails; with no mail settings, resets are made by an admin instead.
	Mail mailer.Mailer
	// MultiTenant says a platform runs this website and does the logging in: the identity arrives with each request,
	// so the login, setup, invite, users, account and domain screens are not served here.
	MultiTenant bool
	// PreviewBase is where the website's live preview is mounted: "" when the site is served at the root, or the
	// platform's prefix (such as /preview/live) when it is not.
	PreviewBase string
	// Publish, when set, adds the publish bar and the publish and hosting screens (a platform publishes the website).
	Publish PublishService
	// Platform, when set, makes this handler the platform's own screens (sites, customers, audit) instead of a website's.
	Platform PlatformService
	// BaseURL is the public address of this server, used for links in emails. When empty the site's saved domain is used.
	BaseURL string
	// SetupToken, if set, must be entered to create the first admin account, so a
	// stranger who finds a fresh server first can't claim it.
	SetupToken string
}

type Handler struct {
	Deps
	checkMu sync.Mutex // one domain check at a time
	pages   map[string]*template.Template
}

type pageData struct {
	Title   string
	User    *auth.Identity
	CSRF    string
	Error   string
	Notice  string
	Wide    bool   // full-width layout (editor)
	Section string // which sidebar entry is current; set by render from the template name
	Unread  int    // unread contact messages, for the sidebar badge; set by render
	Data    any

	// set by render from the handler: what kind of screen this is
	MultiTenant    bool        // a platform runs this website
	PreviewBase    string      // where the live preview is mounted
	PlatformScreen bool        // one of the platform's own screens
	ActingFor      string      // a platform admin is working on this website: whose it is ("" otherwise)
	ActingAs       string      // and who the admin is
	Bar            *PublishBar // the publish bar (nil when this website is not published by a platform)
}

// sectionOf says which sidebar entry each screen belongs to. The pre-login
// screens (setup, login, invite) have no sidebar and are listed in authScreens.
var sectionOf = map[string]string{
	"dashboard": "dashboard", "pages": "pages", "page_edit": "pages",
	"posts": "posts", "post_edit": "posts", "categories": "posts",
	"media": "media", "media_edit": "media", "menus": "menus", "appearance": "appearance",
	"domain": "domain", "users": "users", "account": "account", "seo": "seo", "analytics": "analytics", "messages": "messages", "message": "messages", "subscribers": "subscribers", "campaigns": "campaigns", "campaign": "campaigns", "backup": "backup", "backup_review": "backup", "shared": "shared", "shared_edit": "shared", "redirects": "redirects", "sitedetails": "sitedetails", "platform_sites": "sites", "platform_audit": "audit", "publish": "publish", "hosting": "hosting",
}

var authScreens = map[string]bool{"setup": true, "login": true, "invite": true, "forgot": true, "reset": true}

// screens lists every admin template.
var screens = []string{"setup", "login", "invite", "forgot", "reset", "dashboard", "users", "pages", "page_edit", "menus", "media", "media_edit",
	"posts", "post_edit", "categories", "appearance", "seo", "domain", "account", "analytics", "messages", "message", "subscribers", "campaigns", "campaign", "backup", "backup_review", "shared", "shared_edit", "redirects", "sitedetails", "platform_sites", "platform_audit", "publish", "hosting"}

var funcs = template.FuncMap{
	"lower": strings.ToLower,
	"inc":   func(n int) int { return n + 1 },
	// one item of a repeating list, with what an item is called, for the "repitem" template
	"repctx": func(label string, it repeatItem) struct {
		Label  string
		N      int
		Fields []fieldView
	} {
		return struct {
			Label  string
			N      int
			Fields []fieldView
		}{label, it.N, it.Fields}
	},
	// the first letter of an email, for the avatar circle (rune-safe, unlike slicing bytes)
	"initial": func(s string) string {
		for _, r := range s {
			return strings.ToUpper(string(r))
		}
		return "?"
	},
	// the menu builder renders the same row templates for saved rows and for blank
	// "add" templates, so rows are wrapped with the context they need
	"itemctx": func(it menus.Item, sec menus.Section) struct {
		menus.Item
		Nested, Icons, Sides bool
	} {
		return struct {
			menus.Item
			Nested, Icons, Sides bool
		}{it, sec.Nested, sec.Icons, sec.Sides}
	},
	"linkctx": func(l menus.Link, desc bool) struct {
		menus.Link
		Desc bool
	} {
		return struct {
			menus.Link
			Desc bool
		}{l, desc}
	},
	"blankitem": func() menus.Item { return menus.Item{Kind: menus.KindLink} },
	"blanklink": func() menus.Link { return menus.Link{} },
	"blankcol":  func() menus.Column { return menus.Column{} },
	// analytics screen helpers
	"divf": func(a, b int) float64 {
		if b == 0 {
			return 0
		}
		return float64(a) / float64(b)
	},
	"tbl": func(rows []tableRow, head string) struct {
		Rows []tableRow
		Head string
	} {
		return struct {
			Rows []tableRow
			Head string
		}{rows, head}
	},
	"add1": func(n int) int { return n + 1 },
	"sub1": func(n int) int { return n - 1 },
}

func New(d Deps) (*Handler, error) {
	h := &Handler{Deps: d, pages: map[string]*template.Template{}}
	for _, name := range screens {
		t, err := template.New(name).Funcs(funcs).ParseFS(tmplFS, "templates/base.html", "templates/fields.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		h.pages[name] = t
	}
	return h, nil
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, name string, d pageData) {
	if d.User != nil && h.Contact != nil {
		d.Unread, _ = h.Contact.Unread(r.Context()) // a failed count just hides the badge
	}
	d.MultiTenant, d.PreviewBase, d.PlatformScreen = h.MultiTenant, h.PreviewBase, h.Platform != nil
	if d.User != nil && h.Publish != nil {
		if bar, err := h.Publish.Bar(r.Context()); err == nil {
			d.Bar = &bar
		}
	}
	if d.User != nil && d.User.ImpersonatedBy != "" {
		d.ActingFor, d.ActingAs = d.User.SiteName, d.User.ImpersonatedBy
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	d.Section = sectionOf[name]
	if err := h.pages[name].ExecuteTemplate(w, "base", d); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}

func (h *Handler) Register(mux *http.ServeMux) {
	a := h.Auth
	authed := func(f http.HandlerFunc) http.Handler { return a.RequireLogin(a.CSRF(f)) }
	admin := func(f http.HandlerFunc) http.Handler { return a.RequireAdmin(a.CSRF(f)) }

	if !h.MultiTenant {
		h.registerLogin(mux, authed, admin)
	}
	mux.Handle("GET /admin", authed(h.dashboard))
	mux.Handle("GET /admin/{$}", authed(h.dashboard))
	h.registerStatic(mux)

	h.registerContent(mux, authed)
	h.registerReusable(mux, authed)
	h.registerRedirects(mux, authed)
	h.registerMenus(mux, authed)
	h.registerMedia(mux, authed)
	h.registerPosts(mux, authed)
	h.registerAppearance(mux, admin)
	h.registerSiteDetails(mux, admin)
	if !h.MultiTenant { // the domain is the platform's concern (where the site is published), not the customer's
		h.registerDomain(mux, admin)
	}
	h.registerSEO(mux, admin)
	h.registerAnalytics(mux, authed, admin)
	h.registerMessages(mux, authed, admin)
	h.registerSubscribers(mux, admin)
	h.registerCampaigns(mux, admin)
	h.registerBackups(mux, admin)
	if !h.MultiTenant {
		h.registerAccount(mux, authed, admin)
	}
	if h.Publish != nil {
		h.registerPublish(mux, authed)
	}
}

// registerLogin serves everything about accounts: first-run setup, login and logout, invitations, the team screen and
// password resets. A website run by a platform does not (the platform does it all), and the platform's own screens do.
func (h *Handler) registerLogin(mux *http.ServeMux, authed, admin func(http.HandlerFunc) http.Handler) {
	mux.HandleFunc("GET /setup", h.setupForm)
	mux.HandleFunc("POST /setup", h.setupSubmit)
	mux.HandleFunc("GET /admin/login", h.loginForm)
	mux.HandleFunc("POST /admin/login", h.loginSubmit)
	mux.HandleFunc("GET /invite/{token}", h.inviteForm)
	mux.HandleFunc("POST /invite/{token}", h.inviteSubmit)
	mux.Handle("POST /admin/logout", authed(h.logout))
	mux.HandleFunc("GET /admin/forgot", h.forgotForm)
	mux.HandleFunc("POST /admin/forgot", h.forgotSubmit)
	mux.HandleFunc("GET /reset/{token}", h.resetForm)
	mux.HandleFunc("POST /reset/{token}", h.resetSubmit)
	if h.Platform == nil { // a platform manages its accounts through its sites, not a team list
		mux.Handle("GET /admin/users", admin(h.users))
		mux.Handle("POST /admin/users/invite", admin(h.invite))
		mux.Handle("POST /admin/users/{id}/reset", admin(h.userReset))
		mux.Handle("POST /admin/users/{id}/delete", admin(h.userDelete))
	}
}

// registerStatic serves the admin's own scripts and styles.
func (h *Handler) registerStatic(mux *http.ServeMux) {
	static, _ := fs.Sub(web.Static, "static")
	files := http.StripPrefix("/static/", http.FileServerFS(web.NoDir(static)))
	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	})
}

// --- first-run setup ---

func (h *Handler) setupAllowed(w http.ResponseWriter, r *http.Request) bool {
	has, err := h.Auth.Store.HasUsers(r.Context())
	if err != nil {
		serverError(w, err)
		return false
	}
	if has {
		http.NotFound(w, r)
		return false
	}
	return true
}

func (h *Handler) setupForm(w http.ResponseWriter, r *http.Request) {
	if !h.setupAllowed(w, r) {
		return
	}
	h.render(w, r, 200, "setup", pageData{Title: "Create admin account", CSRF: h.Auth.PreCSRF(w, r), Data: h.SetupToken != ""})
}

func (h *Handler) setupSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.setupAllowed(w, r) {
		return
	}
	fail := func(msg string) {
		h.render(w, r, 400, "setup", pageData{Title: "Create admin account", CSRF: h.Auth.PreCSRF(w, r), Error: msg, Data: h.SetupToken != ""})
	}
	if !h.Auth.CheckPreCSRF(r) {
		fail("Session expired, please try again.")
		return
	}
	if h.SetupToken != "" {
		key := "setup:" + h.Auth.ClientIP(r)
		given := strings.TrimSpace(r.PostFormValue("setup_token"))
		if h.Auth.Logins.Blocked(key) || subtle.ConstantTimeCompare([]byte(given), []byte(h.SetupToken)) != 1 {
			h.Auth.Logins.Fail(key)
			fail("The setup token is missing or incorrect, or there have been too many attempts.")
			return
		}
	}
	email := auth.NormalizeEmail(r.PostFormValue("email"))
	pw := r.PostFormValue("password")
	switch {
	case email == "":
		fail("Enter a valid email address.")
		return
	case auth.ValidatePassword(pw) != "":
		fail(auth.ValidatePassword(pw))
		return
	case pw != r.PostFormValue("confirm"):
		fail("Passwords do not match.")
		return
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		serverError(w, err)
		return
	}
	u, err := h.Auth.Store.CreateFirstAdmin(r.Context(), email, hash)
	if errors.Is(err, auth.ErrSetupDone) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if err := h.Auth.StartSession(w, r, u.ID); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// --- login / logout ---

func (h *Handler) loginForm(w http.ResponseWriter, r *http.Request) {
	if auth.CurrentIdentity(r) != nil {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	if has, err := h.Auth.Store.HasUsers(r.Context()); err == nil && !has {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	h.render(w, r, 200, "login", pageData{Title: "Log in", CSRF: h.Auth.PreCSRF(w, r)})
}

func (h *Handler) loginSubmit(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) {
		h.render(w, r, status, "login", pageData{Title: "Log in", CSRF: h.Auth.PreCSRF(w, r), Error: msg})
	}
	if !h.Auth.CheckPreCSRF(r) {
		fail(400, "Session expired, please try again.")
		return
	}
	email := auth.NormalizeEmail(r.PostFormValue("email"))
	pw := r.PostFormValue("password")
	ipKey := "ip:" + h.Auth.ClientIP(r)
	emailKey := "email:" + email
	if h.Auth.Logins.Blocked(ipKey) || h.Auth.Logins.Blocked(emailKey) {
		fail(http.StatusTooManyRequests, "Too many failed attempts. Try again in 15 minutes.")
		return
	}
	if len(pw) > auth.MaxPasswordLen {
		pw = pw[:auth.MaxPasswordLen]
	}
	u, hash, err := h.Auth.Store.UserByEmail(r.Context(), email)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		auth.BurnPasswordCheck(pw)
	case err != nil:
		serverError(w, err)
		return
	}
	if err != nil || !auth.VerifyPassword(pw, hash) {
		h.Auth.Logins.Fail(ipKey)
		h.Auth.Logins.Fail(emailKey)
		fail(http.StatusUnauthorized, "Invalid email or password.")
		return
	}
	h.Auth.Logins.Reset(emailKey)
	if err := h.Auth.StartSession(w, r, u.ID); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.Auth.EndSession(w, r)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// --- dashboard & user management ---

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	overview, err := h.Content.Overview(r.Context(), 5)
	if err != nil {
		serverError(w, err)
		return
	}
	h.render(w, r, 200, "dashboard", pageData{
		Title: "Dashboard", User: id, CSRF: id.CSRF, Data: overview,
	})
}

type usersData struct {
	Users      []auth.User
	InviteLink string
	ResetLink  string // a one-time password reset link just made for ResetFor
	ResetFor   string
}

func (h *Handler) renderUsers(w http.ResponseWriter, r *http.Request, status int, link, errMsg string) {
	h.renderUsersWith(w, r, status, usersData{InviteLink: link}, errMsg)
}

// renderUsersWith shows the team screen with some of its data already filled in (an invite or reset link just made).
func (h *Handler) renderUsersWith(w http.ResponseWriter, r *http.Request, status int, d usersData, errMsg ...string) {
	id := auth.CurrentIdentity(r)
	users, err := h.Auth.Store.ListUsers(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	notice := ""
	if r.URL.Query().Get("done") == "removed" {
		notice = "User removed."
	}
	d.Users = users
	msg := ""
	if len(errMsg) > 0 {
		msg = errMsg[0]
	}
	h.render(w, r, status, "users", pageData{Title: "Users", User: id, CSRF: id.CSRF, Error: msg, Notice: notice, Data: d})
}

func (h *Handler) users(w http.ResponseWriter, r *http.Request) {
	h.renderUsers(w, r, 200, "", "")
}

func (h *Handler) invite(w http.ResponseWriter, r *http.Request) {
	id := auth.CurrentIdentity(r)
	email := auth.NormalizeEmail(r.PostFormValue("email"))
	role := r.PostFormValue("role")
	if email == "" {
		h.renderUsers(w, r, 400, "", "Enter a valid email address.")
		return
	}
	if role != auth.RoleAdmin && role != auth.RoleEditor {
		h.renderUsers(w, r, 400, "", "Choose a role.")
		return
	}
	token, err := h.Auth.Store.CreateInvite(r.Context(), email, role, id.UserID)
	if errors.Is(err, auth.ErrEmailTaken) {
		h.renderUsers(w, r, 409, "", "That email already has an account.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	scheme := "http"
	if h.Auth.SecureCookies || h.Auth.IsHTTPS(r) {
		scheme = "https"
	}
	h.renderUsers(w, r, 200, scheme+"://"+r.Host+"/invite/"+token, "")
}

// --- invite acceptance ---

func (h *Handler) inviteForm(w http.ResponseWriter, r *http.Request) {
	email, err := h.Auth.Store.InviteEmail(r.Context(), r.PathValue("token"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.render(w, r, 200, "invite", pageData{Title: "Accept invitation", CSRF: h.Auth.PreCSRF(w, r), Data: email})
}

func (h *Handler) inviteSubmit(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	email, err := h.Auth.Store.InviteEmail(r.Context(), token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fail := func(msg string) {
		h.render(w, r, 400, "invite", pageData{Title: "Accept invitation", CSRF: h.Auth.PreCSRF(w, r), Error: msg, Data: email})
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
	u, err := h.Auth.Store.AcceptInvite(r.Context(), token, hash)
	if errors.Is(err, auth.ErrInvalidToken) || errors.Is(err, auth.ErrEmailTaken) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if err := h.Auth.StartSession(w, r, u.ID); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}
