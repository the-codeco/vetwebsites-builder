package blocks

import (
	"testing"
	"time"
)

func TestDraftsShowOnlyToTheirOwnerAndLeaveTheOriginalAlone(t *testing.T) {
	d := NewDrafts()
	saved := []Block{
		{ID: 1, Type: "richtext", Props: map[string]any{"content": "saved"}},
		{ID: 2, Type: LayoutType, Children: [][]Block{{{ID: 3, Type: "richtext", Props: map[string]any{"content": "inside"}}}, nil}},
	}
	d.Set(7, 1, map[string]any{"content": "typed"}, Style{Padding: "lg"})
	d.Set(7, 3, map[string]any{"content": "typed inside"}, Style{})

	got := d.Apply(7, saved)
	if got[0].Props["content"] != "typed" || got[0].Style.Padding != "lg" {
		t.Errorf("draft not applied: %+v", got[0])
	}
	if got[1].Children[0][0].Props["content"] != "typed inside" {
		t.Errorf("draft in a layout column not applied: %+v", got[1].Children)
	}
	if saved[0].Props["content"] != "saved" || saved[1].Children[0][0].Props["content"] != "inside" {
		t.Error("the saved blocks must not change")
	}
	if other := d.Apply(8, saved); other[0].Props["content"] != "saved" {
		t.Error("another person's preview must not show the draft")
	}

	d.Clear(7, 1)
	if got := d.Apply(7, saved); got[0].Props["content"] != "saved" || got[1].Children[0][0].Props["content"] != "typed inside" {
		t.Errorf("clearing one block: %+v", got)
	}
	d.ClearUser(7)
	if got := d.Apply(7, saved); got[1].Children[0][0].Props["content"] != "inside" {
		t.Error("ClearUser forgets everything")
	}
}

func TestDraftsExpireAndAreBounded(t *testing.T) {
	d := NewDrafts()
	now := time.Now()
	d.now = func() time.Time { return now }
	d.Set(1, 1, map[string]any{"content": "typed"}, Style{})
	saved := []Block{{ID: 1, Props: map[string]any{"content": "saved"}}}
	if d.Apply(1, saved)[0].Props["content"] != "typed" {
		t.Fatal("fresh draft")
	}
	now = now.Add(draftTTL + time.Minute)
	if d.Apply(1, saved)[0].Props["content"] != "saved" {
		t.Error("an old draft must expire")
	}
	for i := int64(0); i < maxDrafts+50; i++ {
		d.Set(2, i, map[string]any{}, Style{})
	}
	if len(d.m) > maxDrafts {
		t.Errorf("%d drafts kept", len(d.m))
	}
}
