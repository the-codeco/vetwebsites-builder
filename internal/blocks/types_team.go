package blocks

func init() {
	Register(&Type{Name: "team", Label: "Team", Fields: []Field{
		{Key: "heading", Label: "Heading", Kind: KindText, Default: "Meet the team"},
		{Key: "intro", Label: "Introduction", Kind: KindTextarea},
		{Key: "columns", Label: "People per row", Kind: KindSelect, Default: "3",
			Options: []Option{{"2", "2"}, {"3", "3"}, {"4", "4"}}},
		{Key: "shape", Label: "Photo shape", Kind: KindSelect, Default: "round",
			Options: []Option{{"round", "Round"}, {"square", "Square"}}},
		{Key: "link_label", Label: "Words on each person's link", Kind: KindText, Default: "Profile"},
		{Key: "people", Label: "People", Kind: KindRepeater, ItemLabel: "Person", Item: []Field{
			{Key: "name", Label: "Name (a person needs a name to show)", Kind: KindText},
			{Key: "role", Label: "Role", Kind: KindText},
			{Key: "photo", Label: "Photo", Kind: KindImage},
			{Key: "bio", Label: "Short bio", Kind: KindTextarea},
			{Key: "link", Label: "Link (profile, LinkedIn…)", Kind: KindLink},
		}},
	}})
}
