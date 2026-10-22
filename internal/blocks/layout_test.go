package blocks

import "testing"

func TestLayoutColumns(t *testing.T) {
	for in, want := range map[any]int{"2": 2, "3": 3, "4": 4, "1": 2, "5": 2, "0": 2, "-3": 2, "x": 2, "": 2, nil: 2, 3: 2, 3.0: 2} {
		if got := LayoutColumns(map[string]any{"columns": in}); got != want {
			t.Errorf("columns %v → %d, want %d", in, got, want)
		}
	}
	if LayoutColumns(nil) != 2 || LayoutColumns(map[string]any{}) != 2 {
		t.Error("no setting means two columns")
	}
}

func TestOnlyNonLayoutBlocksNest(t *testing.T) {
	for _, bt := range All() {
		if got, want := Nestable(bt.Name), bt.Name != LayoutType; got != want {
			t.Errorf("Nestable(%q) = %v", bt.Name, got)
		}
	}
	for _, name := range []string{"", "nope", "LAYOUT", "layout "} {
		if Nestable(name) {
			t.Errorf("%q is not a block type", name)
		}
	}
}

func TestLayoutIsOfferedAfterColumns(t *testing.T) {
	var order []string
	for _, bt := range All() {
		order = append(order, bt.Name)
	}
	pos := func(n string) int {
		for i, o := range order {
			if o == n {
				return i
			}
		}
		return -1
	}
	if pos("layout") != pos("columns")+1 || pos("layout") < 0 {
		t.Errorf("layout should follow columns: %v", order)
	}
	if order[0] != "hero" {
		t.Errorf("hero stays first: %v", order)
	}
}

func TestLayoutSettingsAreChecked(t *testing.T) {
	bt, _ := Get(LayoutType)
	def := bt.Clean(nil)
	if def["columns"] != "2" || def["ratio"] != "equal" || def["gap"] != "md" || def["valign"] != "top" || def["reverse"] != false {
		t.Errorf("defaults: %v", def)
	}
	c := bt.Clean(map[string]any{"columns": "4", "ratio": "wide-left", "gap": "lg", "valign": "stretch", "reverse": "on", "extra": "x"})
	if c["columns"] != "4" || c["ratio"] != "wide-left" || c["gap"] != "lg" || c["valign"] != "stretch" || c["reverse"] != true || c["extra"] != nil {
		t.Errorf("valid settings: %v", c)
	}
	c = bt.Clean(map[string]any{"columns": "99", "ratio": "<script>", "gap": "huge", "valign": "diagonal", "reverse": "maybe"})
	if c["columns"] != "2" || c["ratio"] != "equal" || c["gap"] != "md" || c["valign"] != "top" || c["reverse"] != false {
		t.Errorf("invalid settings fall back: %v", c)
	}
	if MaxBlocksPerColumn != 20 || MaxLayoutColumns != 4 {
		t.Error("limits")
	}
}
