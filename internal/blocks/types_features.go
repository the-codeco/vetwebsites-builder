package blocks

func init() {
	Register(&Type{Name: "features", Label: "Feature cards", Fields: []Field{
		{Key: "heading", Label: "Heading", Kind: KindText, Default: "Why choose us"},
		{Key: "intro", Label: "Introduction", Kind: KindTextarea},
		{Key: "columns", Label: "Cards per row", Kind: KindSelect, Default: "3",
			Options: []Option{{"2", "2"}, {"3", "3"}, {"4", "4"}}},
		{Key: "look", Label: "Look", Kind: KindSelect, Default: "boxed",
			Options: []Option{{"boxed", "Boxed cards"}, {"plain", "Plain (no box)"}}},
		{Key: "link_label", Label: "Words on each card's link", Kind: KindText, Default: "Learn more"},
		{Key: "cards", Label: "Cards", Kind: KindRepeater, ItemLabel: "Card", Item: []Field{
			{Key: "title", Label: "Title (a card needs a title to show)", Kind: KindText},
			{Key: "text", Label: "Text", Kind: KindTextarea},
			{Key: "icon", Label: "Icon (an emoji or symbol, e.g. ★)", Kind: KindText},
			{Key: "image", Label: "Picture (shown instead of the icon)", Kind: KindImage},
			{Key: "link", Label: "Link", Kind: KindLink},
		}},
	}})
}
