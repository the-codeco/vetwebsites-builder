package wizard

// BlockSpec is one block of a ready-made section.
type BlockSpec struct {
	Type  string
	Props map[string]any
	Style map[string]any
}

// Section is a ready-made part of the home page: one block, or a few that belong together.
type Section struct {
	ID, Name, Blurb string
	// Default says whether the wizard ticks it for a new site.
	Default bool
	build   func(biz Business) []BlockSpec
}

// Business is what the section texts are written for.
type Business struct {
	Kind  string // "vet" or "other"
	Title string
}

// Business kinds.
const (
	BusinessVet   = "vet"
	BusinessOther = "other"
)

func (b Business) vet() bool { return b.Kind == BusinessVet }

func (b Business) name() string {
	if b.Title == "" {
		return "us"
	}
	return b.Title
}

// Sections are the ready-made home page sections, in the order they are added to the page. Their words are plain
// starting points, written to be replaced: nothing in them claims anything about the business that the owner has not said.
var Sections = []Section{
	{ID: "hero", Name: "Welcome banner", Blurb: "A large heading and a button, to open the page.", Default: true, build: func(b Business) []BlockSpec {
		h, sub, btn := "Welcome to "+b.name(), "Tell visitors in a sentence what you do and who it is for.", "Contact us"
		if b.vet() {
			h, sub = "Caring for your pets, like family", "Friendly, professional veterinary care for dogs, cats and other pets, at "+b.name()+"."
		}
		return []BlockSpec{{Type: "hero", Props: map[string]any{"heading": h, "subheading": sub, "button_label": btn, "button_url": "/about", "size": "lg"},
			Style: map[string]any{"align": "center", "width": "wide"}}}
	}},
	{ID: "services", Name: "Our services", Blurb: "A grid of cards describing what you offer.", Default: true, build: func(b Business) []BlockSpec {
		cards := []any{
			map[string]any{"title": "Service one", "text": "Describe it in a sentence or two.", "icon": "★"},
			map[string]any{"title": "Service two", "text": "Describe it in a sentence or two.", "icon": "★"},
			map[string]any{"title": "Service three", "text": "Describe it in a sentence or two.", "icon": "★"},
		}
		heading := "What we do"
		if b.vet() {
			heading = "Our services"
			cards = []any{
				map[string]any{"title": "Check-ups & vaccinations", "text": "Regular health checks and the vaccines that keep your pet protected.", "icon": "💉"},
				map[string]any{"title": "Surgery", "text": "Routine and specialised procedures, with careful pain management.", "icon": "🩺"},
				map[string]any{"title": "Dentistry", "text": "Dental examinations and cleaning for healthier teeth and gums.", "icon": "🦷"},
				map[string]any{"title": "Diagnostics", "text": "On-site tests and imaging to find answers quickly.", "icon": "🔬"},
				map[string]any{"title": "Emergency care", "text": "Urgent help when your pet needs it most.", "icon": "🚑"},
				map[string]any{"title": "Nutrition & wellness", "text": "Advice on diet, weight and everyday care.", "icon": "🐾"},
			}
		}
		return []BlockSpec{{Type: "features", Props: map[string]any{"heading": heading, "columns": "3", "look": "boxed", "cards": cards}}}
	}},
	{ID: "hours", Name: "Opening hours and contact details", Blurb: "Your phone, address and hours, taken from Site details.", Default: true, build: func(b Business) []BlockSpec {
		h := "Get in touch"
		if b.vet() {
			h = "Opening hours and emergencies"
		}
		return []BlockSpec{{Type: "details", Props: map[string]any{"heading": h}, Style: map[string]any{"background": "", "align": "center"}}}
	}},
	{ID: "team", Name: "Meet the team", Blurb: "Photos and short bios. Fill in names, roles and photos.", build: func(b Business) []BlockSpec {
		role := "Role"
		if b.vet() {
			role = "Veterinarian"
		}
		return []BlockSpec{{Type: "team", Props: map[string]any{"heading": "Meet the team", "columns": "3", "shape": "round", "people": []any{
			map[string]any{"name": "Team member name", "role": role, "bio": "Add a short bio and a photo."},
			map[string]any{"name": "Team member name", "role": role, "bio": "Add a short bio and a photo."},
			map[string]any{"name": "Team member name", "role": role, "bio": "Add a short bio and a photo."},
		}}}}
	}},
	{ID: "testimonials", Name: "What clients say", Blurb: "Space for client reviews. Replace the placeholders with real ones.", build: func(b Business) []BlockSpec {
		return []BlockSpec{{Type: "testimonials", Props: map[string]any{"heading": "What people say", "columns": "3", "items": []any{
			map[string]any{"quote": "Add a real client's words here.", "name": "Client name", "role": ""},
			map[string]any{"quote": "Add a real client's words here.", "name": "Client name", "role": ""},
		}}}}
	}},
	{ID: "faq", Name: "Common questions", Blurb: "An accordion of questions and answers.", build: func(b Business) []BlockSpec {
		items := []any{
			map[string]any{"question": "How do I contact you?", "answer": "<p>Use the contact details on this page. Replace this answer with your own.</p>"},
			map[string]any{"question": "Where are you?", "answer": "<p>Add your address and directions here.</p>"},
		}
		if b.vet() {
			items = []any{
				map[string]any{"question": "How often should my pet have a check-up?", "answer": "<p>Most pets benefit from a check-up at least once a year. Puppies, kittens and older pets may need to be seen more often.</p>"},
				map[string]any{"question": "Do I need an appointment?", "answer": "<p>Replace this answer with how your practice takes visits, and what to do in an emergency.</p>"},
				map[string]any{"question": "What should I bring to the first visit?", "answer": "<p>Bring any records or medicines your pet is on, and your questions.</p>"},
			}
		}
		return []BlockSpec{{Type: "faq", Props: map[string]any{"heading": "Common questions", "open_first": true, "items": items}}}
	}},
	{ID: "news", Name: "Latest news", Blurb: "Your newest blog posts.", build: func(b Business) []BlockSpec {
		return []BlockSpec{{Type: "postlist", Props: map[string]any{"heading": "Latest news", "count": 3.0}}}
	}},
	{ID: "contact", Name: "Contact form", Blurb: "A form that sends visitors' messages to your inbox.", Default: true, build: func(b Business) []BlockSpec {
		return []BlockSpec{{Type: "contactform", Props: map[string]any{"heading": "Send us a message", "intro": "Questions or bookings? Write to us and we will get back to you."}}}
	}},
	{ID: "newsletter", Name: "Newsletter sign-up", Blurb: "Let visitors subscribe to your emails.", build: func(b Business) []BlockSpec {
		return []BlockSpec{{Type: "newsletter", Props: map[string]any{"heading": "Stay in the loop"}}}
	}},
}

// SectionByID finds a section.
func SectionByID(id string) (Section, bool) {
	for _, s := range Sections {
		if s.ID == id {
			return s, true
		}
	}
	return Section{}, false
}

// Blocks are the blocks of the chosen sections for a business, in page order (the order of Sections, whatever order
// the ids came in), each cleaned by its block type's own rules. An unknown id is an error.
func Blocks(ids []string, b Business) ([]BlockSpec, error) {
	want := map[string]bool{}
	for _, id := range ids {
		if _, ok := SectionByID(id); !ok {
			return nil, errUnknownSection(id)
		}
		want[id] = true
	}
	var out []BlockSpec
	for _, s := range Sections {
		if want[s.ID] {
			out = append(out, s.build(b)...)
		}
	}
	return out, nil
}

type errUnknownSection string

func (e errUnknownSection) Error() string {
	return "There is no ready-made section called " + string(e) + "."
}

// DefaultSectionIDs are the sections ticked for a new site.
func DefaultSectionIDs() []string {
	var out []string
	for _, s := range Sections {
		if s.Default {
			out = append(out, s.ID)
		}
	}
	return out
}
