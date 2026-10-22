package themes

import (
	"strings"
	"testing"

	"webbuilder/internal/blocks"
)

func block(id int64, typ string, props, style map[string]any) blocks.Block {
	bt, _ := blocks.Get(typ)
	return blocks.Block{ID: id, Type: typ, Props: bt.Clean(props), Style: blocks.CleanStyle(style)}
}

func renderTree(t *testing.T, theme string, b blocks.Block, extraFor func(blocks.Block) any) string {
	t.Helper()
	e, _ := Load()
	th, _ := e.Lookup(theme)
	out, err := th.RenderBlockTree(b, extraFor)
	if err != nil {
		t.Fatalf("%s: %v", theme, err)
	}
	return string(out)
}

func TestLayoutRendersItsBlocksInColumns(t *testing.T) {
	for _, theme := range []string{"default", "journal", "midnight"} {
		l := block(1, "layout", map[string]any{"columns": "3", "ratio": "wide-left", "gap": "sm", "valign": "bottom", "reverse": true}, map[string]any{"padding": "lg", "background": "#102030"})
		l.Children = [][]blocks.Block{
			{block(2, "richtext", map[string]any{"content": "<p>First</p>"}, map[string]any{"padding": "none", "align": "center"}), block(3, "spacer", nil, nil)},
			{},
			{block(4, "button", map[string]any{"label": "Go <now>", "url": "/go"}, map[string]any{"background": "#ffcc00"})},
		}
		out := renderTree(t, theme, l, nil)
		for _, want := range []string{
			`<section id="block-1" class="block block-layout pad-lg align-left width-normal pinned" style="--bg:#102030;">`,
			`<div class="layout layout-3 ratio-wide-left gap-sm valign-bottom reverse-mobile">`,
			`<div id="block-2" class="block block-richtext nested pad-none align-center width-normal">`,
			`<div id="block-3" class="block block-spacer nested`,
			`<div id="block-4" class="block block-button nested pad-md align-left width-normal pinned" style="--bg:#ffcc00;">`,
			`<p>First</p>`, `Go &lt;now&gt;`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: lacks %s\n%s", theme, want, out)
			}
		}
		if strings.Count(out, `class="layout-col"`) != 3 {
			t.Errorf("%s: three columns, even if one is empty:\n%s", theme, out)
		}
		if strings.Count(out, "<section") != 1 {
			t.Errorf("%s: only the layout is a section", theme)
		}
		if !(strings.Index(out, "First") < strings.Index(out, `id="block-3"`) && strings.Index(out, `id="block-3"`) < strings.Index(out, `id="block-4"`)) {
			t.Errorf("%s: order", theme)
		}
	}
}

func TestLayoutColumnCountFollowsItsSettings(t *testing.T) {
	l := block(1, "layout", map[string]any{"columns": "2"}, nil)
	l.Children = [][]blocks.Block{{block(2, "spacer", nil, nil)}, {}, {block(3, "button", map[string]any{"label": "Hidden"}, nil)}}
	out := renderTree(t, "default", l, nil)
	if strings.Count(out, `class="layout-col"`) != 2 || strings.Contains(out, "Hidden") {
		t.Errorf("a block in a column the layout does not have is not drawn here (the page loader moves it):\n%s", out)
	}
	if out := renderTree(t, "default", block(1, "layout", map[string]any{"columns": "4"}, nil), nil); strings.Count(out, `class="layout-col"`) != 4 {
		t.Errorf("an empty layout keeps its columns:\n%s", out)
	}
}

func TestLayoutChildrenGetTheirOwnData(t *testing.T) {
	l := block(1, "layout", nil, nil)
	l.Children = [][]blocks.Block{{block(5, "postlist", nil, nil)}, {block(6, "richtext", nil, nil)}}
	var asked []int64
	renderTree(t, "default", l, func(b blocks.Block) any {
		asked = append(asked, b.ID)
		return nil
	})
	if len(asked) != 3 || asked[0] != 5 || asked[1] != 6 || asked[2] != 1 { // the blocks inside, in order, then the layout itself
		t.Errorf("every block, nested ones too, is asked for its data: %v", asked)
	}
	// RenderBlockWith hands the same data to the layout's blocks
	e, _ := Load()
	th, _ := e.Lookup("default")
	if _, err := th.RenderBlockWith(l, nil); err != nil {
		t.Error(err)
	}
}

func TestLayoutInsideALayoutAndUnknownBlocksAreSkipped(t *testing.T) {
	inner := block(9, "layout", nil, nil)
	inner.Children = [][]blocks.Block{{block(10, "spacer", nil, nil)}}
	l := block(1, "layout", nil, nil)
	l.Children = [][]blocks.Block{{inner, {ID: 11, Type: "removed-type"}, block(2, "richtext", map[string]any{"content": "<p>kept</p>"}, nil)}}
	out := renderTree(t, "default", l, nil)
	if strings.Contains(out, `id="block-9"`) || strings.Contains(out, `id="block-10"`) || strings.Contains(out, `id="block-11"`) || !strings.Contains(out, "kept") {
		t.Errorf("a layout inside a layout and an unknown type are skipped:\n%s", out)
	}
	if strings.Count(out, `class="layout `) != 1 {
		t.Error("only one layout")
	}
	e, _ := Load()
	th, _ := e.Lookup("default")
	if out, err := th.RenderBlockWith(blocks.Block{ID: 1, Type: "removed-type"}, nil); out != "" || err != nil {
		t.Errorf("an unknown type renders nothing: %q %v", out, err)
	}
}

func TestOrdinaryBlocksRenderAsBefore(t *testing.T) {
	out := renderTree(t, "default", block(7, "richtext", map[string]any{"content": "<p>plain</p>"}, map[string]any{"padding": "sm"}), nil)
	if !strings.HasPrefix(out, `<section id="block-7" class="block block-richtext pad-sm align-left width-normal"><div class="block-inner">`) || !strings.Contains(out, "<p>plain</p>") || strings.Contains(out, "nested") {
		t.Errorf("top-level blocks are unchanged: %s", out)
	}
}
