package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"webbuilder/internal/content"
	"webbuilder/internal/themes"
)

// State is what the wizard remembers between visits.
type State struct {
	Done    bool    `json:"done,omitempty"`
	Skipped bool    `json:"skipped,omitempty"`
	Answers Answers `json:"answers"`
	Blocks  []int64 `json:"blocks,omitempty"`  // the home page blocks the wizard made, so a re-run replaces exactly those
	Element int64   `json:"element,omitempty"` // the notice bar it made
}

// Load reads the wizard's state. started is false for a website nobody has run the wizard on (or skipped it for).
func Load(ctx context.Context, st *content.Store) (s State, started bool, err error) {
	m, err := st.WizardState(ctx)
	if err != nil {
		return s, false, err
	}
	if len(m) == 0 {
		return s, false, nil
	}
	data, _ := json.Marshal(m)
	_ = json.Unmarshal(data, &s) // a state this version cannot read is treated as an empty one
	return s, true, nil
}

// Save keeps the wizard's state.
func Save(ctx context.Context, st *content.Store, s State) error {
	data, _ := json.Marshal(s)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if len(m) == 0 {
		m = map[string]any{"started": true} // never an empty map: that means "not started"
	}
	return st.SetWizardState(ctx, m)
}

// NeedsWizard says whether to send a person to the wizard: it has never been started, run or skipped.
func NeedsWizard(ctx context.Context, st *content.Store) (bool, error) {
	_, started, err := Load(ctx, st)
	return !started, err
}

// Defaults are the answers a first visit starts from: what the site already says, and sensible choices for the rest.
func Defaults(now content.Settings) Answers {
	a := Answers{
		Business: BusinessVet, Title: now.Title, Kit: Kits[0].ID, TopBar: "contact", Cookie: now.Consent.Enabled,
		Logo: now.Brand.Logo, LogoAlt: now.Brand.LogoAlt,
		Phone: now.Details.Phone, WhatsApp: now.Details.WhatsApp, Email: now.Details.Email, Address: now.Details.Address, Hours: now.Details.Hours,
		Social: map[string]string{}, Sections: DefaultSectionIDs(),
	}
	for k, v := range now.Details.Social {
		a.Social[k] = v
	}
	return a
}

// Apply checks the answers and, only if all of them are good, puts them on the website: its look, logo and contact details,
// menus, footer, cookie notice, notice bar and home page sections. What the wizard made before is replaced; what the owner
// made themselves is left alone. The state is updated and marked done.
func Apply(ctx context.Context, st *content.Store, eng *themes.Engine, s *State) (Plan, error) {
	// Once it has started it finishes, even if the browser that asked goes away: a website with half a wizard on it is worse
	// than one with all of it.
	ctx = context.WithoutCancel(ctx)
	now, err := st.Settings(ctx)
	if err != nil {
		return Plan{}, err
	}
	saved, err := st.Menus(ctx)
	if err != nil {
		return Plan{}, err
	}
	p, err := Build(s.Answers, now, eng, saved)
	if err != nil {
		return p, err
	}

	// the blocks go first, in one step: if the page cannot take them nothing else has changed yet
	var made, remove0 []int64
	if home, err := st.HomePageID(ctx); err == nil {
		remove, err := removable(ctx, st, home, s.Blocks, len(p.Blocks) > 0)
		if err != nil {
			return p, err
		}
		remove0 = remove
		if len(p.Blocks) > 0 || len(remove) > 0 {
			add := make([]content.NewBlock, len(p.Blocks))
			for i, b := range p.Blocks {
				add[i] = content.NewBlock{Type: b.Type, Props: b.Props, Style: b.Style}
			}
			if made, err = st.ReplaceBlocks(ctx, home, remove, add); err != nil {
				if errors.Is(err, content.ErrTooMany) {
					return p, ErrProblem{"The home page has too many blocks to add these sections. Remove some blocks first."}
				}
				return p, err
			}
		}
	} else if !errors.Is(err, content.ErrNotFound) {
		return p, err
	} else if len(p.Blocks) > 0 {
		return p, ErrProblem{"The home page has been deleted, so the sections have nowhere to go. Untick them, or create a page called home first."}
	}
	s.Blocks = made
	if len(made) > 0 || len(remove0) > 0 {
		// remembered at once, so if anything below fails the next run still knows which blocks are the wizard's
		if err := Save(ctx, st, *s); err != nil {
			return p, err
		}
	}

	tokens := p.Tokens
	for k, v := range now.TokensFor(p.Theme) { // the dark palette is not the wizard's
		if strings.HasPrefix(k, "dark.") {
			tokens[k] = v
		}
	}
	steps := []func() error{
		func() error { return st.SetTitle(ctx, p.Title) },
		func() error { return st.SetActiveTheme(ctx, p.Theme) },
		func() error { return st.SetTokens(ctx, p.Theme, tokens) },
		func() error { return st.SetColorMode(ctx, p.ColorMode) },
		func() error { return st.SetColorSwitch(ctx, p.ColorSwitch) },
		func() error { return st.SetLayout(ctx, p.Layout) },
		func() error { return st.SetBrand(ctx, p.Brand) },
		func() error { return st.SetDetails(ctx, p.Details) },
		func() error { return st.SetConsent(ctx, p.Consent) },
		func() error { return st.SetFooter(ctx, p.Footer) },
		func() error { return st.SaveMenu(ctx, "main", p.MainItems, saved["main"].Sticky, saved["main"].Style) },
		func() error { return st.SaveMenu(ctx, "topbar", p.TopItems, saved["topbar"].Sticky, p.TopStyle) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return p, err
		}
	}

	switch {
	case p.Element != nil:
		e := *p.Element
		if s.Element != 0 {
			if _, err := st.Element(ctx, s.Element); err == nil {
				e.ID = s.Element
			}
		}
		id, err := st.SaveElement(ctx, e)
		if err != nil {
			return p, err
		}
		s.Element = id
	case s.Element != 0:
		if err := st.DeleteElement(ctx, s.Element); err != nil && !errors.Is(err, content.ErrNotFound) {
			return p, err
		}
		s.Element = 0
	}
	s.Done, s.Skipped = true, false
	return p, Save(ctx, st, *s)
}

// The two blocks a new website starts with. They are replaced by the sections, but only while they are still as they were made.
const (
	seedHeroHeading = "Welcome to your new website"
	seedColumnTitle = "Build with blocks"
)

// removable lists the home page blocks the wizard may take away: the ones it made last time, and, when new sections are
// coming, the starting blocks nobody has edited.
func removable(ctx context.Context, st *content.Store, home int64, made []int64, replacing bool) ([]int64, error) {
	list, err := st.Blocks(ctx, home)
	if err != nil {
		return nil, err
	}
	ours := map[int64]bool{}
	for _, id := range made {
		ours[id] = true
	}
	var out []int64
	for _, b := range list {
		switch {
		case ours[b.ID]:
			out = append(out, b.ID)
		case replacing && b.Type == "hero" && b.Props["heading"] == seedHeroHeading:
			out = append(out, b.ID)
		case replacing && b.Type == "columns" && b.Props["col1_title"] == seedColumnTitle:
			out = append(out, b.ID)
		}
	}
	return out, nil
}

// Skip marks the wizard as skipped: the person will set things up themselves.
func Skip(ctx context.Context, st *content.Store) error {
	s, _, err := Load(ctx, st)
	if err != nil {
		return err
	}
	s.Skipped = true
	return Save(ctx, st, s)
}
