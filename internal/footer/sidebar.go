package footer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Sidebar is a column of widgets beside the content of every page that shows it (a page can turn it off). Side is
// "left" or "right"; Width is "narrow" or "wide".
type Sidebar struct {
	Side    string   `json:"side"`
	Width   string   `json:"width"`
	Widgets []Widget `json:"widgets"`
}

// MaxSidebarWidgets is how many widgets the sidebar holds.
const MaxSidebarWidgets = 12

var (
	Sides  = []struct{ Value, Label string }{{"left", "Left of the content"}, {"right", "Right of the content"}}
	Widths = []struct{ Value, Label string }{{"narrow", "Narrow"}, {"wide", "Wide"}}
)

// DecodeSidebar reads the builder's JSON (shape only); ValidateSidebar applies the rules.
func DecodeSidebar(data []byte) (Sidebar, error) {
	if len(data) > MaxJSON {
		return Sidebar{}, errors.New("The sidebar is too large.")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Sidebar
	if err := dec.Decode(&s); err != nil {
		return Sidebar{}, errors.New("The sidebar couldn't be read. Reload the page and try again.")
	}
	return s, nil
}

// ValidateSidebar checks every widget like a footer widget's and returns the sidebar ready to store and render.
func ValidateSidebar(in Sidebar) (Sidebar, error) {
	v := func(o struct{ Value, Label string }) string { return o.Value }
	out := Sidebar{Side: oneOf(in.Side, Sides, v, "left"), Width: oneOf(in.Width, Widths, v, "narrow"), Widgets: []Widget{}}
	if len(in.Widgets) > MaxSidebarWidgets {
		return Sidebar{}, fmt.Errorf("The sidebar can hold at most %d widgets.", MaxSidebarWidgets)
	}
	for _, w := range in.Widgets {
		cw, err := validateWidget(w, "The sidebar")
		if err != nil {
			return Sidebar{}, err
		}
		out.Widgets = append(out.Widgets, cw)
	}
	return out, nil
}

// SidebarFromStored reads a saved sidebar, cut down to fit rather than refused.
func SidebarFromStored(m map[string]any) Sidebar {
	data, err := json.Marshal(m)
	var s Sidebar
	if err == nil {
		err = json.Unmarshal(data, &s)
	}
	if err == nil {
		s.Widgets = trimWidgets(s.Widgets, MaxSidebarWidgets)
		if out, err := ValidateSidebar(s); err == nil {
			return out
		}
	}
	out, _ := ValidateSidebar(Sidebar{})
	return out
}

// AsMap is the sidebar as stored JSON.
func (s Sidebar) AsMap() map[string]any {
	data, _ := json.Marshal(s)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}
