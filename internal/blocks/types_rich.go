package blocks

import "strconv"

// Slot counts for blocks made of a few numbered sets of fields (like the columns block). Blocks that
// hold a list of any length use a repeating field instead.
const (
	PricingPlans = 3
)

func init() {
	Register(&Type{Name: "pricing", Label: "Pricing table", Fields: pricingFields()})
	Register(&Type{Name: "testimonials", Label: "Testimonials", Fields: testimonialFields(), Upgrade: upgradeTestimonials})
	Register(&Type{Name: "faq", Label: "FAQ (accordion)", Fields: faqFields(), Upgrade: upgradeFAQ})
	Register(&Type{Name: "tabs", Label: "Tabs", Fields: tabFields(), Upgrade: upgradeTabs})

	Register(&Type{Name: "video", Label: "Video", Fields: []Field{
		{Key: "url", Label: "YouTube or Vimeo link", Kind: KindLink},
		{Key: "title", Label: "Title (read out to screen-reader users)", Kind: KindText, Default: "Video"},
		{Key: "caption", Label: "Caption", Kind: KindText},
		{Key: "ratio", Label: "Shape", Kind: KindSelect, Default: "16-9",
			Options: []Option{{"16-9", "Widescreen (16:9)"}, {"4-3", "Standard (4:3)"}, {"1-1", "Square"}}},
	}})

	Register(&Type{Name: "map", Label: "Map", Fields: []Field{
		{Key: "provider", Label: "Map service", Kind: KindSelect, Default: "osm",
			Options: []Option{{"osm", "OpenStreetMap (private, no cookies) – position by latitude, longitude"}, {"google", "Google Maps – found by address (Google may set its own cookies)"}}},
		{Key: "coordinates", Label: "OpenStreetMap: position, as latitude, longitude (e.g. 51.5007, -0.1246)", Kind: KindText},
		{Key: "place", Label: "Google Maps: address or place to find (e.g. 10 Downing Street, London)", Kind: KindText},
		{Key: "zoom", Label: "Zoom (1 = world, 19 = street)", Kind: KindNumber, Default: 14.0, Min: 1, Max: 19},
		{Key: "title", Label: "Title (read out to screen-reader users)", Kind: KindText, Default: "Map"},
		{Key: "address", Label: "Caption shown under the map", Kind: KindText},
		{Key: "height", Label: "Height", Kind: KindSelect, Default: "md",
			Options: []Option{{"sm", "Small"}, {"md", "Medium"}, {"lg", "Large"}}},
	}})
}

func slot(prefix string, i int) string { return prefix + strconv.Itoa(i) + "_" }

func pricingFields() []Field {
	fs := []Field{
		{Key: "heading", Label: "Heading", Kind: KindText, Default: "Pricing"},
		{Key: "intro", Label: "Introduction", Kind: KindTextarea},
		{Key: "count", Label: "Number of plans", Kind: KindSelect, Default: "3",
			Options: []Option{{"1", "1"}, {"2", "2"}, {"3", "3"}}},
	}
	for i := 1; i <= PricingPlans; i++ {
		p, l := slot("plan", i), "Plan "+strconv.Itoa(i)+" "
		d := func(s string) string {
			if i == 1 {
				return s
			}
			return ""
		}
		fs = append(fs,
			Field{Key: p + "name", Label: l + "name", Kind: KindText, Default: []string{"", "Starter", "Pro", "Business"}[i]},
			Field{Key: p + "price", Label: l + "price (e.g. $9)", Kind: KindText, Default: []string{"", "$9", "$29", "$99"}[i]},
			Field{Key: p + "period", Label: l + "period (e.g. per month)", Kind: KindText, Default: d("per month")},
			Field{Key: p + "features", Label: l + "features (one per line)", Kind: KindTextarea},
			Field{Key: p + "button_label", Label: l + "button label", Kind: KindText, Default: d("Get started")},
			Field{Key: p + "button_url", Label: l + "button link", Kind: KindLink},
			Field{Key: p + "featured", Label: l + "is highlighted as the best choice", Kind: KindBool},
		)
	}
	return fs
}

func testimonialFields() []Field {
	return []Field{
		{Key: "heading", Label: "Heading", Kind: KindText, Default: "What people say"},
		{Key: "columns", Label: "Testimonials per row", Kind: KindSelect, Default: "3",
			Options: []Option{{"1", "1"}, {"2", "2"}, {"3", "3"}}},
		{Key: "items", Label: "Testimonials", Kind: KindRepeater, ItemLabel: "Testimonial", Item: []Field{
			{Key: "quote", Label: "Quote (a testimonial needs a quote to show)", Kind: KindTextarea},
			{Key: "name", Label: "Person's name", Kind: KindText},
			{Key: "role", Label: "Role or company", Kind: KindText},
			{Key: "photo", Label: "Photo", Kind: KindImage},
		}},
	}
}

func faqFields() []Field {
	return []Field{
		{Key: "heading", Label: "Heading", Kind: KindText, Default: "Frequently asked questions"},
		{Key: "open_first", Label: "Show the first answer open", Kind: KindBool},
		{Key: "items", Label: "Questions", Kind: KindRepeater, ItemLabel: "Question", Item: []Field{
			{Key: "question", Label: "Question", Kind: KindText},
			{Key: "answer", Label: "Answer", Kind: KindRichText},
		}},
	}
}

func tabFields() []Field {
	return []Field{
		{Key: "heading", Label: "Heading", Kind: KindText},
		{Key: "items", Label: "Tabs", Kind: KindRepeater, ItemLabel: "Tab", Item: []Field{
			{Key: "title", Label: "Title", Kind: KindText},
			{Key: "content", Label: "Content", Kind: KindRichText},
		}},
	}
}

// The blocks below used to hold a fixed number of numbered fields (q1_question, tab1_title…). Pages saved
// that way, and backups of them, are read as lists.

func upgradeTestimonials(raw map[string]any) map[string]any {
	shown := 3
	if c, ok := raw["count"].(string); ok {
		if n, err := strconv.Atoi(c); err == nil && n >= 1 && n < shown {
			shown = n
		}
	}
	out := upgradeSlots(raw, "items", "t", shown, "quote", "name", "role", "photo")
	if _, has := raw["columns"]; !has { // the old "number of testimonials" is now how many fit in a row
		if c, ok := raw["count"].(string); ok {
			out = copyMap(out)
			out["columns"] = c
		}
	}
	return out
}

func upgradeFAQ(raw map[string]any) map[string]any {
	return upgradeSlots(raw, "items", "q", 10, "question", "answer")
}

func upgradeTabs(raw map[string]any) map[string]any {
	return upgradeSlots(raw, "items", "tab", 5, "title", "content")
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}
