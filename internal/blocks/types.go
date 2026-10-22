package blocks

func init() {
	Register(&Type{Name: "hero", Label: "Hero", Fields: []Field{
		{Key: "heading", Label: "Heading", Kind: KindText, Default: "Your heading here"},
		{Key: "subheading", Label: "Subheading", Kind: KindTextarea},
		{Key: "layout", Label: "Image position", Kind: KindSelect, Default: "background",
			Options: []Option{{"background", "Behind the text"}, {"right", "Beside the text, on the right"}, {"left", "Beside the text, on the left"}}},
		{Key: "image", Label: "Image", Kind: KindImage},
		{Key: "image_alt", Label: "Image description (when beside the text)", Kind: KindText},
		{Key: "button_label", Label: "Button label", Kind: KindText},
		{Key: "button_url", Label: "Button link", Kind: KindLink},
		{Key: "size", Label: "Height", Kind: KindSelect, Default: "md",
			Options: []Option{{"sm", "Small"}, {"md", "Medium"}, {"lg", "Large"}}},
	}})

	Register(&Type{Name: "richtext", Label: "Text", Fields: []Field{
		{Key: "content", Label: "Content", Kind: KindRichText, Default: "<p>Write your text here.</p>"},
	}})

	Register(&Type{Name: "image", Label: "Image", Fields: []Field{
		{Key: "image", Label: "Image", Kind: KindImage},
		{Key: "alt", Label: "Alt text", Kind: KindText},
		{Key: "caption", Label: "Caption", Kind: KindText},
		{Key: "link", Label: "Link (optional)", Kind: KindLink},
	}})

	Register(&Type{Name: "gallery", Label: "Gallery", Fields: []Field{
		{Key: "images", Label: "Images (one URL per line)", Kind: KindImageList},
		{Key: "columns", Label: "Columns", Kind: KindSelect, Default: "3",
			Options: []Option{{"2", "2"}, {"3", "3"}, {"4", "4"}}},
	}})

	Register(&Type{Name: "columns", Label: "Columns", Fields: []Field{
		{Key: "count", Label: "Number of columns", Kind: KindSelect, Default: "2",
			Options: []Option{{"2", "2"}, {"3", "3"}}},
		{Key: "col1_title", Label: "Column 1 title", Kind: KindText},
		{Key: "col1_text", Label: "Column 1 text", Kind: KindTextarea},
		{Key: "col2_title", Label: "Column 2 title", Kind: KindText},
		{Key: "col2_text", Label: "Column 2 text", Kind: KindTextarea},
		{Key: "col3_title", Label: "Column 3 title", Kind: KindText},
		{Key: "col3_text", Label: "Column 3 text", Kind: KindTextarea},
	}})

	registerLayout()

	Register(&Type{Name: "button", Label: "Button", Fields: []Field{
		{Key: "label", Label: "Label", Kind: KindText},
		{Key: "url", Label: "Link", Kind: KindLink},
		{Key: "variant", Label: "Style", Kind: KindSelect, Default: "primary",
			Options: []Option{{"primary", "Primary"}, {"secondary", "Secondary"}, {"outline", "Outline"}}},
	}})

	Register(&Type{Name: "postlist", Label: "Latest posts", Fields: []Field{
		{Key: "heading", Label: "Heading", Kind: KindText, Default: "Latest posts"},
		{Key: "count", Label: "Number of posts (1–12)", Kind: KindNumber, Default: 3.0, Min: 1, Max: 12},
	}})

	Register(&Type{Name: "contactform", Label: "Contact form", Fields: []Field{
		{Key: "heading", Label: "Heading", Kind: KindText, Default: "Get in touch"},
		{Key: "intro", Label: "Introduction", Kind: KindTextarea, Default: "Send us a message and we'll get back to you."},
		{Key: "show_subject", Label: "Ask for a subject", Kind: KindBool, Default: true},
		{Key: "button_label", Label: "Button label", Kind: KindText, Default: "Send message"},
		{Key: "success", Label: "Message shown after sending", Kind: KindText, Default: "Thanks! Your message has been sent."},
	}})

	Register(&Type{Name: "newsletter", Label: "Newsletter signup", Fields: []Field{
		{Key: "heading", Label: "Heading", Kind: KindText, Default: "Stay in the loop"},
		{Key: "intro", Label: "Introduction", Kind: KindTextarea, Default: "Get our updates by email. No spam, and you can unsubscribe at any time."},
		{Key: "button_label", Label: "Button label", Kind: KindText, Default: "Subscribe"},
		{Key: "success", Label: "Message shown after signing up", Kind: KindText, Default: "Thanks for subscribing!"},
	}})

	Register(&Type{Name: "spacer", Label: "Spacer", Fields: []Field{
		{Key: "height", Label: "Height", Kind: KindSelect, Default: "md",
			Options: []Option{{"sm", "Small"}, {"md", "Medium"}, {"lg", "Large"}}},
	}})
}
