package blocks

import (
	"strings"
	"testing"
)

func TestWordsOf(t *testing.T) {
	if got := WordsOf("<p>Hello</p><p>World &amp; <b>friends</b></p>\n<ul><li>a</li><li>b</li></ul>"); got != "Hello World & friends a b" {
		t.Errorf("WordsOf: %q", got)
	}
	if got := WordsOf("  plain   text "); got != "plain text" {
		t.Errorf("plain: %q", got)
	}
}

func TestSearchText(t *testing.T) {
	got := SearchText("hero", map[string]any{
		"heading": "Big  news", "subheading": "Read\nthis", "image": "/media/a.jpg",
		"button_label": "Go", "button_url": "/somewhere", "size": "lg",
	})
	for _, want := range []string{"Big news", "Read this", "Go"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	for _, not := range []string{"/media/a.jpg", "/somewhere", "lg"} {
		if strings.Contains(got, not) {
			t.Errorf("%q is not readable text but was indexed: %q", not, got)
		}
	}
	if got := SearchText("richtext", map[string]any{"content": "<p>One</p><p>Two <script>x()</script></p>"}); strings.Contains(got, "script") || strings.Contains(got, "x()") || !strings.Contains(got, "One Two") {
		t.Errorf("rich text: %q", got)
	}
	// a form's heading and introduction are content; its button and thank-you text are not
	form := SearchText("contactform", map[string]any{"heading": "Get in touch", "intro": "Write to us.", "button_label": "Send message", "success": "Thanks! Your message has been sent."})
	if !strings.Contains(form, "Get in touch") || !strings.Contains(form, "Write to us.") || strings.Contains(form, "Send message") || strings.Contains(form, "Thanks") {
		t.Errorf("contact form: %q", form)
	}
	news := SearchText("newsletter", map[string]any{"heading": "Stay in the loop", "button_label": "Subscribe", "success": "Thanks for subscribing!"})
	if !strings.Contains(news, "Stay in the loop") || strings.Contains(news, "Subscribe") || strings.Contains(news, "Thanks") {
		t.Errorf("newsletter: %q", news)
	}
	if SearchText("no-such-block", map[string]any{"heading": "x"}) != "" {
		t.Error("unknown block types contribute nothing")
	}
	big := SearchText("richtext", map[string]any{"content": strings.Repeat("<p>word </p>", 20000)})
	if len(big) > maxSearchText {
		t.Errorf("not capped: %d", len(big))
	}
}

func TestFeatureCardWordsAreSearchableButNotTheLinkWords(t *testing.T) {
	got := SearchText("features", map[string]any{"heading": "Why", "cards": []any{map[string]any{"title": "Speedy", "text": "Rapid delivery", "link": "/x"}}, "link_label": "Learn more"})
	if got != "Why Speedy Rapid delivery" {
		t.Errorf("got %q", got)
	}
}

func TestTeamWordsAreSearchable(t *testing.T) {
	got := SearchText("team", map[string]any{"heading": "Us", "people": []any{map[string]any{"name": "Ann", "role": "Vet", "bio": "Loves dogs", "link": "/x"}}, "link_label": "Profile"})
	if got != "Us Ann Vet Loves dogs" {
		t.Errorf("got %q", got)
	}
}
