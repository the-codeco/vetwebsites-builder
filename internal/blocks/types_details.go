package blocks

func init() {
	// The words come from Site details in the admin, so they are written once and every page shows the same.
	Register(&Type{Name: "details", Label: "Contact details", Fields: []Field{
		{Key: "heading", Label: "Heading", Kind: KindText, Default: "Get in touch"},
		{Key: "show_phone", Label: "Show the phone number", Kind: KindBool, Default: true},
		{Key: "show_whatsapp", Label: "Show the WhatsApp number", Kind: KindBool, Default: true},
		{Key: "show_email", Label: "Show the email address", Kind: KindBool, Default: true},
		{Key: "show_address", Label: "Show the address", Kind: KindBool, Default: true},
		{Key: "show_hours", Label: "Show the opening hours", Kind: KindBool, Default: true},
		{Key: "show_social", Label: "Show the social links", Kind: KindBool, Default: true},
	}})
}
