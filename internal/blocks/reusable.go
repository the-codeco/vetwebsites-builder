package blocks

import "math"

// ReusableType is the block a page holds in place of a shared one: its only setting is which shared block
// (by id) to show. The page renderer swaps in the shared block's type, settings and style before drawing.
const ReusableType = "reusable"

// MaxReusableName is the longest name a shared block can have.
const MaxReusableName = 100

// RefOf is the id of the shared block a reusable block points at, or 0 if it points at none.
func RefOf(props map[string]any) int64 {
	f, _ := props["ref"].(float64)
	if f < 1 || f != math.Trunc(f) || f > 1e15 {
		return 0
	}
	return int64(f)
}

// CanBeShared says whether blocks of this type can go in the shared library: any kind except a layout (it
// holds other blocks) and a shared block itself.
func CanBeShared(typeName string) bool {
	_, ok := Get(typeName)
	return ok && typeName != LayoutType && typeName != ReusableType
}

// Pickable lists the block types an author can choose from when adding a block.
func Pickable() []*Type {
	var out []*Type
	for _, t := range All() {
		if !t.Internal {
			out = append(out, t)
		}
	}
	return out
}
