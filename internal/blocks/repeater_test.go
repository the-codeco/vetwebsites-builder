package blocks

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestRepeaterListsHaveNoSmallLimit(t *testing.T) {
	bt, _ := Get("faq")
	var in []any
	for i := 0; i < 60; i++ {
		in = append(in, map[string]any{"question": fmt.Sprintf("Q%d", i), "answer": "A"})
	}
	items := bt.Clean(map[string]any{"items": in})["items"].([]map[string]any)
	if len(items) != 60 || items[59]["question"] != "Q59" {
		t.Errorf("sixty questions all kept, in order: %d", len(items))
	}
	for i := len(in); i < 500; i++ {
		in = append(in, map[string]any{"question": fmt.Sprintf("Q%d", i)})
	}
	items = bt.Clean(map[string]any{"items": in})["items"].([]map[string]any)
	if len(items) != MaxRepeaterItems || items[MaxRepeaterItems-1]["question"] != fmt.Sprintf("Q%d", MaxRepeaterItems-1) {
		t.Errorf("a list stops at the safety cap: %d", len(items))
	}
}

func TestRepeaterCleaningIsStrict(t *testing.T) {
	bt, _ := Get("features")
	for name, raw := range map[string]any{"string": "x", "number": 4.0, "nil": nil, "map": map[string]any{"title": "x"}, "junk": []any{"a", 1, nil, []any{}}, "empty items": []any{map[string]any{}, map[string]any{"title": "  "}}} {
		items, ok := bt.Clean(map[string]any{"cards": raw})["cards"].([]map[string]any)
		if !ok || len(items) != 0 {
			t.Errorf("%s: want an empty list, got %#v", name, items)
		}
	}
	got := bt.Clean(map[string]any{"cards": []any{map[string]any{
		"title": strings.Repeat("t", 400), "link": "javascript:alert(1)", "image": "data:x", "icon": "★", "extra": "dropped",
	}}})["cards"].([]map[string]any)[0]
	if len([]rune(got["title"].(string))) != 300 || got["link"] != "" || got["image"] != "" || got["icon"] != "★" || got["extra"] != nil {
		t.Errorf("items are cleaned by their fields: %v", got)
	}
}

func TestRepeaterFormValuesAreReadInOrder(t *testing.T) {
	bt, _ := Get("faq")
	f := bt.Fields[2]
	form := url.Values{
		"p.items.10.question": {"third"}, "p.items.2.question": {"second"}, "p.items.0.question": {"first"}, "p.items.0.answer": {"a"},
		"p.items.x.question": {"bad number"}, "p.items.-1.question": {"negative"}, "p.items.1234567.question": {"too long a number"},
		"p.items.3": {"no field"}, "p.other.0.question": {"other field"},
	}
	list := f.RepeaterFormValues("p.", form)
	var got []string
	for _, e := range list {
		got = append(got, e.(map[string]any)["question"].(string))
	}
	if strings.Join(got, ",") != "first,second,third" {
		t.Errorf("items sorted by number, junk ignored: %v", got)
	}
	// an unticked box is not sent, so it must read as false
	tm := Field{Key: "r", Kind: KindRepeater, Item: []Field{{Key: "t", Kind: KindText}, {Key: "on", Kind: KindBool, Default: true}}}
	l := tm.RepeaterFormValues("p.", url.Values{"p.r.0.t": {"x"}})
	if l[0].(map[string]any)["on"] != false {
		t.Errorf("unticked: %v", l)
	}
}

func TestOldNumberedBlocksAreUpgraded(t *testing.T) {
	faq, _ := Get("faq")
	old := map[string]any{"q1_question": "One?", "q1_answer": "a1", "q3_question": "Three?", "q11_question": "beyond", "open_first": true}
	c := faq.Clean(old)
	items := c["items"].([]map[string]any)
	if len(items) != 2 || items[0]["question"] != "One?" || items[1]["question"] != "Three?" || c["open_first"] != true {
		t.Errorf("faq: %v", c)
	}
	if _, changed := old["items"]; changed {
		t.Error("the stored props are not changed in place")
	}
	// a block already in the new shape is left alone, even if stray old keys remain
	c = faq.Clean(map[string]any{"items": []any{map[string]any{"question": "New"}}, "q1_question": "Old"})
	if items := c["items"].([]map[string]any); len(items) != 1 || items[0]["question"] != "New" {
		t.Errorf("new shape wins: %v", items)
	}
	tabs, _ := Get("tabs")
	if items := tabs.Clean(map[string]any{"tab1_title": "T", "tab1_content": "c", "tab5_title": "Five"})["items"].([]map[string]any); len(items) != 2 || items[1]["title"] != "Five" {
		t.Errorf("tabs: %v", items)
	}
	tm, _ := Get("testimonials")
	c = tm.Clean(map[string]any{"count": "2", "t1_quote": "a", "t2_quote": "b", "t3_quote": "hidden by the old count"})
	if items := c["items"].([]map[string]any); len(items) != 2 || c["columns"] != "2" {
		t.Errorf("testimonials: %v %v", items, c["columns"])
	}
}

func TestEveryBlockStillCleansToItsOwnKeys(t *testing.T) {
	for _, bt := range All() {
		c := bt.Clean(nil)
		for _, f := range bt.Fields {
			if _, ok := c[f.Key]; !ok {
				t.Errorf("%s: %s missing", bt.Name, f.Key)
			}
			if f.Kind == KindRepeater {
				if items, ok := c[f.Key].([]map[string]any); !ok || items == nil {
					t.Errorf("%s: an empty list is an empty list, not null: %#v", bt.Name, c[f.Key])
				}
				if f.ItemLabel == "" || len(f.Item) == 0 {
					t.Errorf("%s: the editor needs a label and fields for %s", bt.Name, f.Key)
				}
				for _, sf := range f.Item {
					if sf.Kind == KindRepeater {
						t.Errorf("%s: lists inside lists are not supported", bt.Name)
					}
				}
			}
		}
	}
}
