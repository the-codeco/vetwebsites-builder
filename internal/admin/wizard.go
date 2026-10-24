package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"webbuilder/internal/auth"
	"webbuilder/internal/content"
	"webbuilder/internal/themes"
	"webbuilder/internal/wizard"
)

func (h *Handler) registerWizard(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /admin/setup", admin(h.setupPage))
	mux.Handle("POST /admin/setup", admin(h.setupSave))
	mux.Handle("POST /admin/setup/apply", admin(h.setupApply))
	mux.Handle("POST /admin/setup/skip", admin(h.setupSkip))
}

// The steps, in order.
var setupSteps = []struct{ Title, Short string }{
	{"Your website", "Name"},
	{"Choose a look", "Look"},
	{"Logo and contact details", "Contact"},
	{"Menus and extras", "Menus"},
	{"Home page sections", "Sections"},
	{"Review", "Review"},
}

type stepLink struct {
	N             int
	Title         string
	Current, Seen bool
}

type kitView struct {
	wizard.Kit
	Selected   bool
	Swatches   []string
	TokensJSON string
}

type setupSection struct {
	wizard.Section
	Checked bool
}

type setupData struct {
	Step     int
	Last     bool
	Title    string
	Steps    []stepLink
	A        wizard.Answers
	Kits     []kitView
	Fonts    []fontOption
	Corners  []struct{ Key, Label, Value string }
	Dark     []struct{ Key, Label string }
	Headers  []struct{ Key, Label string }
	TopBars  []struct{ Key, Label string }
	Networks []content.Network
	Sections []setupSection
	Plan     *wizard.Plan
	Done     bool
	Redo     bool // the wizard has been run before: running it again replaces what it made
	Preview  string
	KitName  string
	Stacks   string // font token -> CSS stack, for the live preview
	Kind     struct{ Vet, Other bool }
}

// shouldRunWizard says whether this person, on this request, should be sent to the wizard: the administrator of a customer's
// website on the platform that has not been set up, who is not a platform administrator looking at somebody's site. A
// website that runs on its own is offered the wizard on the dashboard instead.
func (h *Handler) shouldRunWizard(r *http.Request) bool {
	id := auth.CurrentIdentity(r)
	if id == nil || id.Role != auth.RoleAdmin || id.ImpersonatedBy != "" || h.Platform != nil || !h.MultiTenant {
		return false
	}
	need, err := wizard.NeedsWizard(r.Context(), h.Content)
	return err == nil && need
}

// setupHint says whether the dashboard should offer the wizard: it has not been finished (skipped, or never started).
func (h *Handler) setupHint(r *http.Request) bool {
	s, _, err := wizard.Load(r.Context(), h.Content)
	return err == nil && !s.Done
}

func (h *Handler) loadSetup(r *http.Request) (wizard.State, error) {
	st, started, err := wizard.Load(r.Context(), h.Content)
	if err != nil {
		return st, err
	}
	if !started || (st.Answers.Kit == "" && st.Answers.Title == "") {
		now, err := h.Content.Settings(r.Context())
		if err != nil {
			return st, err
		}
		st.Answers = wizard.Defaults(now)
	}
	return st, nil
}

func (h *Handler) setupData(r *http.Request, st wizard.State, step int) setupData {
	d := setupData{Step: step, Last: step == len(setupSteps), A: st.Answers, Title: setupSteps[step-1].Title, Redo: st.Done,
		Preview: h.PreviewBase + "/", Corners: wizard.CornerChoices, Dark: wizard.DarkChoices, Headers: wizard.HeaderChoices,
		TopBars: wizard.TopBarChoices, Networks: content.Networks}
	if k, ok := wizard.KitByID(st.Answers.Kit); ok {
		d.KitName = k.Name
	}
	d.Kind.Vet, d.Kind.Other = st.Answers.Business != wizard.BusinessOther, st.Answers.Business == wizard.BusinessOther
	for i, s := range setupSteps {
		d.Steps = append(d.Steps, stepLink{N: i + 1, Title: s.Short, Current: i+1 == step, Seen: i+1 < step})
	}
	for _, k := range wizard.Kits {
		tj, _ := json.Marshal(k.Tokens)
		d.Kits = append(d.Kits, kitView{Kit: k, Selected: k.ID == st.Answers.Kit, Swatches: k.Swatches(), TokensJSON: string(tj)})
	}
	for _, f := range themes.FontOptions {
		d.Fonts = append(d.Fonts, fontOption{Value: f.Key, Label: f.Label, Stack: themes.FontStacks[f.Key]})
	}
	stacks, _ := json.Marshal(themes.FontStacks)
	d.Stacks = string(stacks)
	chosen := map[string]bool{}
	for _, id := range st.Answers.Sections {
		chosen[id] = true
	}
	for _, s := range wizard.Sections {
		d.Sections = append(d.Sections, setupSection{Section: s, Checked: chosen[s.ID]})
	}
	return d
}

func (h *Handler) renderSetup(w http.ResponseWriter, r *http.Request, status int, st wizard.State, step int, errMsg string) {
	id := auth.CurrentIdentity(r)
	d := h.setupData(r, st, step)
	if step == len(setupSteps) && errMsg == "" {
		now, err := h.Content.Settings(r.Context())
		if err == nil {
			saved, _ := h.Content.Menus(r.Context())
			if p, err := wizard.Build(st.Answers, now, h.Engine, saved); err != nil {
				var pr wizard.ErrProblem
				if errors.As(err, &pr) {
					errMsg = pr.Msg + " Go back to fix it."
				} else {
					serverError(w, err)
					return
				}
			} else {
				d.Plan = &p
			}
		}
	}
	h.render(w, r, status, "wizard", pageData{Title: "Set up your website", User: id, CSRF: id.CSRF, Error: errMsg, Wide: true, Data: d})
}

func stepFrom(r *http.Request, key string) int {
	n, _ := strconv.Atoi(r.FormValue(key))
	if n < 1 || n > len(setupSteps) {
		return 1
	}
	return n
}

func (h *Handler) setupPage(w http.ResponseWriter, r *http.Request) {
	st, err := h.loadSetup(r)
	if err != nil {
		serverError(w, err)
		return
	}
	h.renderSetup(w, r, http.StatusOK, st, stepFrom(r, "step"), "")
}

// readStep copies a step's form fields into the answers. Only the fields of that step are read, so a step never wipes another's.
func readStep(r *http.Request, step int, a *wizard.Answers) {
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	switch step {
	case 1:
		a.Title, a.Business = v("title"), v("business")
	case 2:
		a.Kit = v("kit")
		a.Primary, a.Background, a.Text = v("primary"), v("background"), v("text")
		a.FontBody, a.FontHeading = v("font_body"), v("font_heading")
		a.Corners, a.Dark, a.Header = v("corners"), v("dark"), v("header")
	case 3:
		a.Logo, a.LogoAlt = v("logo"), v("logo_alt")
		a.Phone, a.WhatsApp, a.Email, a.Address, a.Hours = v("phone"), v("whatsapp"), v("email"), r.PostFormValue("address"), r.PostFormValue("hours")
		a.Social = map[string]string{}
		for _, n := range content.Networks {
			if s := v("social_" + n.Key); s != "" {
				a.Social[n.Key] = s
			}
		}
	case 4:
		a.TopBar = v("top_bar")
		a.Cookie, a.Newsletter = r.PostFormValue("cookie") == "on", r.PostFormValue("newsletter") == "on"
		a.Announcement = v("announcement")
	case 5:
		a.Sections = r.PostForm["sections"]
	}
}

// setupSave stores one step's answers (checking them against the rest) and moves on, or back.
func (h *Handler) setupSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	st, err := h.loadSetup(r)
	if err != nil {
		serverError(w, err)
		return
	}
	step := stepFrom(r, "step")
	readStep(r, step, &st.Answers)
	now, err := h.Content.Settings(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	saved, _ := h.Content.Menus(r.Context())
	if _, err := wizard.Build(st.Answers, now, h.Engine, saved); err != nil {
		var pr wizard.ErrProblem
		if errors.As(err, &pr) {
			h.renderSetup(w, r, http.StatusBadRequest, st, step, pr.Msg)
			return
		}
		serverError(w, err)
		return
	}
	if err := wizard.Save(r.Context(), h.Content, st); err != nil {
		serverError(w, err)
		return
	}
	next := step + 1
	if r.PostFormValue("go") == "back" {
		next = step - 1
	}
	next = max(1, min(next, len(setupSteps)))
	http.Redirect(w, r, "/admin/setup?step="+strconv.Itoa(next), http.StatusSeeOther)
}

type setupDone struct {
	Plan     wizard.Plan
	Title    string
	Warnings []string
}

func (h *Handler) setupApply(w http.ResponseWriter, r *http.Request) {
	st, err := h.loadSetup(r)
	if err != nil {
		serverError(w, err)
		return
	}
	p, err := wizard.Apply(r.Context(), h.Content, h.Engine, &st)
	var pr wizard.ErrProblem
	switch {
	case errors.As(err, &pr):
		h.renderSetup(w, r, http.StatusBadRequest, st, len(setupSteps), pr.Msg)
		return
	case err != nil:
		serverError(w, err)
		return
	}
	id := auth.CurrentIdentity(r)
	h.render(w, r, http.StatusOK, "wizard_done", pageData{Title: "Your website is set up", User: id, CSRF: id.CSRF, Data: setupDone{Plan: p, Title: p.Title, Warnings: p.Warnings}})
}

func (h *Handler) setupSkip(w http.ResponseWriter, r *http.Request) {
	if err := wizard.Skip(r.Context(), h.Content); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}
