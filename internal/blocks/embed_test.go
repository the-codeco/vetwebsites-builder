package blocks

import (
	"strings"
	"testing"
)

func TestVideoOf(t *testing.T) {
	good := map[string]Video{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":          {Src: "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0"},
		"https://youtu.be/dQw4w9WgXcQ":                         {Src: "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0"},
		"https://youtu.be/dQw4w9WgXcQ?t=90":                    {Src: "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0&start=90"},
		"https://m.youtube.com/watch?v=dQw4w9WgXcQ&list=x":     {Src: "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0"},
		"https://www.youtube.com/embed/dQw4w9WgXcQ":            {Src: "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0"},
		"https://www.youtube.com/shorts/dQw4w9WgXcQ":           {Src: "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0"},
		"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ":   {Src: "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0"},
		"https://vimeo.com/76979871":                           {Src: "https://player.vimeo.com/video/76979871?dnt=1"},
		"https://vimeo.com/76979871/abcdef1234":                {Src: "https://player.vimeo.com/video/76979871?dnt=1&h=abcdef1234"},
		"https://player.vimeo.com/video/76979871?h=abcdef1234": {Src: "https://player.vimeo.com/video/76979871?dnt=1&h=abcdef1234"},
	}
	for in, want := range good {
		got, ok := VideoOf(in)
		if !ok || got != want {
			t.Errorf("%q → %+v ok=%v, want %+v", in, got, ok, want)
		}
	}

	bad := []string{
		"", "   ", "not a url", "javascript:alert(1)", "data:text/html,<script>alert(1)</script>",
		"//www.youtube.com/watch?v=dQw4w9WgXcQ",                    // protocol-relative
		"https://evil.example/watch?v=dQw4w9WgXcQ",                 // another site
		"https://www.youtube.com.evil.example/watch?v=dQw4w9WgXcQ", // look-alike host
		"https://evil.example/https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://user:pw@www.youtube.com/watch?v=dQw4w9WgXcQ",      // credentials in the address
		"https://www.youtube.com/watch?v=short",                    // not an id
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ%22onload%3Dx", // markup in the id
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ\"><script>",
		"https://www.youtube.com/playlist?list=PL12345678901",
		"https://youtu.be/dQw4w9WgXcQ/extra",
		"https://vimeo.com/channels/staffpicks",
		"https://vimeo.com/76979871/not-hex!",
		"https://vimeo.com/76979871?h=zzz",
		"ftp://youtu.be/dQw4w9WgXcQ",
	}
	for _, in := range bad {
		if got, ok := VideoOf(in); ok {
			t.Errorf("%q should not embed, got %+v", in, got)
		}
	}
}

func TestVideoSourcesAreAllowedByTheCSP(t *testing.T) {
	for _, in := range []string{"https://youtu.be/dQw4w9WgXcQ", "https://vimeo.com/76979871"} {
		v, _ := VideoOf(in)
		host := strings.SplitN(strings.TrimPrefix(v.Src, "https://"), "/", 2)[0]
		if !strings.Contains(FrameSources, "https://"+host) {
			t.Errorf("%s embeds from %s, which the content security policy doesn't allow", in, host)
		}
	}
	m, _ := MapOf("51.5, -0.12", 12)
	if !strings.HasPrefix(m.Src, "https://"+hostOSM+"/") || !strings.Contains(FrameSources, "https://"+hostOSM) {
		t.Errorf("map source %q", m.Src)
	}
}

func TestMapOf(t *testing.T) {
	for _, in := range []string{"51.5007, -0.1246", "51.5007,-0.1246", "51.5007 -0.1246", "51.5007; -0.1246", " -33.86 , 151.21 ", "0,0"} {
		m, ok := MapOf(in, 14)
		if !ok {
			t.Errorf("%q should be a position", in)
			continue
		}
		if !strings.HasPrefix(m.Src, "https://www.openstreetmap.org/export/embed.html?bbox=") || !strings.Contains(m.Src, "&layer=mapnik&marker=") {
			t.Errorf("%q: source %q", in, m.Src)
		}
		if !strings.HasPrefix(m.Link, "https://www.openstreetmap.org/?mlat=") || !strings.Contains(m.Link, "#map=14/") {
			t.Errorf("%q: link %q", in, m.Link)
		}
	}
	m, _ := MapOf("51.5007, -0.1246", 14)
	if !strings.Contains(m.Src, "marker=51.500700%2C-0.124600") {
		t.Errorf("the marker should sit on the position: %s", m.Src)
	}

	for _, in := range []string{"", "London", "51.5", "51.5, -0.12, 3", "91, 0", "-86, 0", "0, 181", "NaN, 1", "Inf, 1", "1e999, 1", "51.5\"><script>, 1", "a, b", "javascript:alert(1)"} {
		if m, ok := MapOf(in, 14); ok {
			t.Errorf("%q should not be a position, got %+v", in, m)
		}
	}

	// zoom is clamped, and a wide view stays on the globe
	for _, z := range []float64{-5, 0, 1, 19, 99, 1e9} {
		m, ok := MapOf("85, 180", z)
		if !ok || strings.Contains(m.Src, "NaN") || strings.Contains(m.Src, "Inf") {
			t.Errorf("zoom %v: %q", z, m.Src)
		}
	}
	if m, _ := MapOf("10, 20", 99); !strings.Contains(m.Link, "#map=19/") {
		t.Errorf("zoom should be capped at 19: %s", m.Link)
	}
}

func TestItemsAndLines(t *testing.T) {
	props := map[string]any{
		"q1_question": "One?", "q1_answer": "a1",
		"q2_question": "  ", "q2_answer": "ignored: no question",
		"q3_question": "Three?", "q3_answer": "a3",
		"q4_question": "Four?", "q4_answer": "a4",
	}
	got := Items(props, "q", 3, "question", "answer")
	if len(got) != 2 || got[0]["question"] != "One?" || got[1]["question"] != "Three?" || got[1]["N"] != 3 {
		t.Errorf("empty questions are skipped and the limit applies: %v", got)
	}
	if n := len(Items(props, "q", "4", "question")); n != 3 {
		t.Errorf("a limit given as text: %d", n)
	}
	if n := len(Items(props, "q", "lots", "question")); n != 0 {
		t.Errorf("an unreadable limit shows nothing: %d", n)
	}
	if n := len(Items(props, "q", 10)); n != 0 {
		t.Errorf("no keys, no items: %d", n)
	}

	if got := Lines("  a \r\n\nb\n   \nc"); strings.Join(got, "|") != "a|b|c" {
		t.Errorf("lines: %q", got)
	}
}

func TestRepeatedItemBlocksCleanTheirFields(t *testing.T) {
	// every item of every list is cleaned by its own field type
	faq, _ := Get("faq")
	c := faq.Clean(map[string]any{
		"items":      []any{map[string]any{"question": strings.Repeat("q", 400), "answer": `<p onclick="x()">Hi</p><script>alert(1)</script>`}},
		"open_first": "on",
	})
	q := c["items"].([]map[string]any)[0]
	if len([]rune(q["question"].(string))) != 300 {
		t.Error("questions are limited to 300 characters")
	}
	if a := q["answer"].(string); strings.Contains(a, "script") || strings.Contains(a, "onclick") {
		t.Errorf("answer not sanitised: %s", a)
	}
	if c["open_first"] != true {
		t.Error("checkbox")
	}

	pricing, _ := Get("pricing")
	p := pricing.Clean(map[string]any{
		"count": "9", "plan1_button_url": "javascript:alert(1)", "plan2_button_url": "/signup", "plan1_featured": "on",
	})
	if p["count"] != "3" || p["plan1_button_url"] != "" || p["plan2_button_url"] != "/signup" || p["plan1_featured"] != true || p["plan2_featured"] != false {
		t.Errorf("pricing: %v", p)
	}

	tm, _ := Get("testimonials")
	items := tm.Clean(map[string]any{"items": []any{
		map[string]any{"quote": "a", "photo": "javascript:alert(1)"}, map[string]any{"quote": "b", "photo": "/media/a.jpg"},
	}})["items"].([]map[string]any)
	if items[0]["photo"] != "" || items[1]["photo"] != "/media/a.jpg" {
		t.Errorf("testimonial photos: %v", items)
	}

	mp, _ := Get("map")
	if c := mp.Clean(map[string]any{"zoom": "99"}); c["zoom"] != 19.0 {
		t.Errorf("map zoom: %v", c["zoom"])
	}
	vd, _ := Get("video")
	if c := vd.Clean(map[string]any{"url": "javascript:alert(1)", "ratio": "9-16"}); c["url"] != "" || c["ratio"] != "16-9" {
		t.Errorf("video: %v", c)
	}
}

func TestNewBlocksAreSearchableExceptTheirChrome(t *testing.T) {
	text := SearchText("faq", map[string]any{"q1_question": "Do you ship abroad?", "q1_answer": "<p>Yes, <b>worldwide</b>.</p>"})
	if !strings.Contains(text, "ship abroad") || !strings.Contains(text, "worldwide") {
		t.Errorf("faq text: %q", text)
	}
	if got := SearchText("pricing", map[string]any{"plan1_name": "Starter", "plan1_button_label": "Purchasetoday"}); !strings.Contains(got, "Starter") || strings.Contains(got, "Purchasetoday") {
		t.Errorf("pricing text: %q", got)
	}
	if got := SearchText("video", map[string]any{"title": "Screenreadertitle", "caption": "Our founder"}); strings.Contains(got, "Screenreadertitle") || !strings.Contains(got, "Our founder") {
		t.Errorf("video text: %q", got)
	}
	if got := SearchText("tabs", map[string]any{"tab2_title": "Delivery", "tab2_content": "<p>Two days</p>"}); !strings.Contains(got, "Delivery") || !strings.Contains(got, "Two days") {
		t.Errorf("tabs text: %q", got)
	}
}
