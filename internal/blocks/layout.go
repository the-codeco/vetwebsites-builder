package blocks

import "strconv"

// A layout block puts other blocks in side-by-side columns. Blocks in a layout can't be layouts themselves: one
// level is enough for any page, and it keeps editing and rendering simple.
const (
	LayoutType = "layout"

	MaxLayoutColumns     = 4
	MaxBlocksPerColumn   = 20
	defaultLayoutColumns = 2
)

// registerLayout adds the layout block. It is called from types.go so it sits after "Columns" in the editor's list.
func registerLayout() {
	Register(&Type{Name: LayoutType, Label: "Layout (columns of blocks)", Fields: []Field{
		{Key: "columns", Label: "Number of columns", Kind: KindSelect, Default: "2",
			Options: []Option{{"2", "2"}, {"3", "3"}, {"4", "4"}}},
		{Key: "ratio", Label: "Column widths", Kind: KindSelect, Default: "equal",
			Options: []Option{{"equal", "All equal"}, {"wide-left", "First column wider"}, {"wide-middle", "Middle column wider (3 columns)"}, {"wide-right", "Last column wider"}}},
		{Key: "gap", Label: "Space between columns", Kind: KindSelect, Default: "md",
			Options: []Option{{"sm", "Small"}, {"md", "Medium"}, {"lg", "Large"}}},
		{Key: "valign", Label: "Line columns up at the", Kind: KindSelect, Default: "top",
			Options: []Option{{"top", "Top"}, {"center", "Middle"}, {"bottom", "Bottom"}, {"stretch", "Top, all as tall as the tallest"}}},
		{Key: "reverse", Label: "On phones, show the last column first", Kind: KindBool},
	}})
}

// LayoutColumns is how many columns a layout block has.
func LayoutColumns(props map[string]any) int {
	s, _ := props["columns"].(string)
	if n, err := strconv.Atoi(s); err == nil && n >= 2 && n <= MaxLayoutColumns {
		return n
	}
	return defaultLayoutColumns
}

// Nestable says whether a block of this type may go inside a layout.
func Nestable(typeName string) bool {
	_, ok := Get(typeName)
	return ok && typeName != LayoutType
}
