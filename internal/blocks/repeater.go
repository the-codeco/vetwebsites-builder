package blocks

import (
	"sort"
	"strconv"
	"strings"
)

// MaxRepeaterItems is the most items one repeating list holds. It is far more than a page needs; it is only
// there so a hand-made request or a hostile backup file cannot make one block enormous.
const MaxRepeaterItems = 200

// cleanItems coerces a repeating list: a list of items, each cleaned by the item's own fields. Anything
// that is not a list of objects is an empty list, and items with no text, picture or link at all are dropped.
func (f Field) cleanItems(v any) []map[string]any {
	var in []map[string]any
	switch x := v.(type) {
	case []map[string]any:
		in = x
	case []any:
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				in = append(in, m)
			}
		}
	}
	limit := MaxRepeaterItems
	if f.MaxItems > 0 && f.MaxItems < limit {
		limit = f.MaxItems
	}
	out := []map[string]any{}
	for _, m := range in {
		item := cleanFields(f.Item, m)
		if f.itemBlank(item) {
			continue
		}
		if out = append(out, item); len(out) == limit {
			break
		}
	}
	return out
}

// itemBlank reports whether an item has nothing a visitor could see: no words, picture or link.
func (f Field) itemBlank(item map[string]any) bool {
	for _, sf := range f.Item {
		switch sf.Kind {
		case KindText, KindTextarea, KindRichText, KindImage, KindLink:
			if s, _ := item[sf.Key].(string); strings.TrimSpace(s) != "" {
				return false
			}
		}
	}
	return true
}

// RepeaterFormValues reads a repeating list out of a submitted form: fields named "<prefix><key>.<n>.<sub>",
// where n is any whole number (the editor renumbers them in the order shown). Items come back in order of n.
func (f Field) RepeaterFormValues(prefix string, form map[string][]string) []any {
	start := prefix + f.Key + "."
	byN := map[int]map[string]any{}
	for name, vals := range form {
		rest, ok := strings.CutPrefix(name, start)
		if !ok || len(vals) == 0 {
			continue
		}
		numStr, sub, ok := strings.Cut(rest, ".")
		n, err := strconv.Atoi(numStr)
		if !ok || err != nil || n < 0 || len(numStr) > 6 {
			continue
		}
		if byN[n] == nil {
			byN[n] = map[string]any{}
		}
		byN[n][sub] = vals[0]
	}
	nums := make([]int, 0, len(byN))
	for n := range byN {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	out := make([]any, 0, len(nums))
	for _, n := range nums {
		m := byN[n]
		for _, sf := range f.Item { // an unticked box is not sent at all
			if sf.Kind == KindBool {
				v, _ := m[sf.Key].(string)
				m[sf.Key] = v == "on"
			}
		}
		out = append(out, m)
	}
	return out
}

// slotsToList reads the old numbered fields of a block ("q1_question", "q2_question"…) as a list, for blocks
// that used to hold a fixed number of items. keys are the item's fields; max slots are read.
func slotsToList(raw map[string]any, prefix string, max int, keys ...string) []any {
	var out []any
	for i := 1; i <= max; i++ {
		m := map[string]any{}
		for _, k := range keys {
			if v, ok := raw[prefix+strconv.Itoa(i)+"_"+k]; ok {
				m[k] = v
			}
		}
		if len(m) > 0 {
			out = append(out, m)
		}
	}
	return out
}

// upgradeSlots returns raw with its old numbered fields turned into a list under target, unless raw already
// has that list. The original map is not changed.
func upgradeSlots(raw map[string]any, target, prefix string, max int, keys ...string) map[string]any {
	if _, has := raw[target]; has {
		return raw
	}
	list := slotsToList(raw, prefix, max, keys...)
	if list == nil {
		return raw
	}
	out := make(map[string]any, len(raw)+1)
	for k, v := range raw {
		out[k] = v
	}
	out[target] = list
	return out
}
