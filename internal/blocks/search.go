package blocks

import (
	"html"
	"regexp"
	"strings"
)

// maxSearchText caps what one page contributes to the search index.
const maxSearchText = 100_000

var tagRe = regexp.MustCompile(`<[^>]*>`)

// WordsOf turns rich text into plain words: tags become spaces (so "</p><p>" doesn't glue
// two words together), entities are decoded and whitespace is collapsed.
func WordsOf(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// formChrome lists text fields that belong to a form's controls rather than to the page's content
// (a button's label, the thank-you shown after sending). Searching for "message" shouldn't find
// every page with a contact form.
var formChrome = map[string]map[string]bool{
	"contactform": {"button_label": true, "success": true},
	"newsletter":  {"button_label": true, "success": true},
	// labels read out by screen readers, and the button on each pricing plan, aren't page content
	"features": {"link_label": true},
	"team":     {"link_label": true},
	"video":    {"title": true},
	"map":      {"title": true},
	"pricing":  {"plan1_button_label": true, "plan2_button_label": true, "plan3_button_label": true},
}

// SearchText is the text a visitor could read in a block: its text, text-area and rich-text
// fields, in order. Links, image addresses, colours and choices are not words, so they are left out.
func SearchText(typeName string, props map[string]any) string {
	bt, ok := Get(typeName)
	if !ok {
		return ""
	}
	props = bt.Clean(props)
	var parts []string
	var add func(fields []Field, values map[string]any)
	add = func(fields []Field, values map[string]any) {
		for _, f := range fields {
			if f.Kind == KindRepeater {
				items, _ := values[f.Key].([]map[string]any)
				for _, item := range items {
					add(f.Item, item)
				}
				continue
			}
			s, _ := values[f.Key].(string)
			if s == "" || formChrome[typeName][f.Key] {
				continue
			}
			switch f.Kind {
			case KindText, KindTextarea:
				parts = append(parts, strings.Join(strings.Fields(s), " "))
			case KindRichText:
				parts = append(parts, WordsOf(s))
			}
		}
	}
	add(bt.Fields, props)
	out := strings.Join(parts, " ")
	if len(out) > maxSearchText {
		out = out[:maxSearchText]
	}
	return strings.ToValidUTF8(out, "")
}
