package blocks

import "testing"

func TestRefOf(t *testing.T) {
	for in, want := range map[any]int64{5.0: 5, 1.0: 1, 1e15: 1e15, 0.0: 0, -3.0: 0, 2.5: 0, 1e16: 0, "5": 0, nil: 0, 5: 0, true: 0} {
		if got := RefOf(map[string]any{"ref": in}); got != want {
			t.Errorf("ref %v → %d, want %d", in, got, want)
		}
	}
	if RefOf(nil) != 0 || RefOf(map[string]any{}) != 0 {
		t.Error("no pointer")
	}
}

func TestWhatCanBeShared(t *testing.T) {
	for _, bt := range All() {
		want := bt.Name != LayoutType && bt.Name != ReusableType
		if got := CanBeShared(bt.Name); got != want {
			t.Errorf("CanBeShared(%q) = %v", bt.Name, got)
		}
	}
	for _, n := range []string{"", "nope", "Layout"} {
		if CanBeShared(n) {
			t.Errorf("%q is not a block", n)
		}
	}
	if !Nestable(ReusableType) {
		t.Error("a shared block can go in a column")
	}
}

func TestSharedBlockIsNeverOffered(t *testing.T) {
	for _, bt := range Pickable() {
		if bt.Name == ReusableType || bt.Internal {
			t.Errorf("%s is offered in the add-block list", bt.Name)
		}
	}
	if len(Pickable()) != len(All())-1 {
		t.Error("only the shared block is hidden")
	}
	bt, _ := Get(ReusableType)
	if c := bt.Clean(map[string]any{"ref": "7", "junk": "x"}); len(c) != 1 || c["ref"] != 7.0 {
		t.Errorf("clean: %v", c)
	}
	if c := bt.Clean(map[string]any{"ref": -1.0}); c["ref"] != 0.0 {
		t.Errorf("negative: %v", c)
	}
}

func TestSharedBlockHasNoSearchWordsOfItsOwn(t *testing.T) {
	if got := SearchText(ReusableType, map[string]any{"ref": 5.0}); got != "" {
		t.Errorf("its words come from the library block: %q", got)
	}
}
