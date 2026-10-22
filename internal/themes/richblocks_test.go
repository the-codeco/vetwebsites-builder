package themes

import (
	"strings"
	"testing"

	"webbuilder/internal/blocks"
)

func renderBlockOf(t *testing.T, theme, typ string, props map[string]any) string {
	t.Helper()
	e, _ := Load()
	th, ok := e.Lookup(theme)
	if !ok {
		t.Fatal("no theme " + theme)
	}
	bt, _ := blocks.Get(typ)
	out, err := th.RenderBlock(blocks.Block{ID: 7, Type: typ, Props: bt.Clean(props), Style: blocks.CleanStyle(nil)})
	if err != nil {
		t.Fatalf("%s/%s: %v", theme, typ, err)
	}
	return string(out)
}

func TestPricingTable(t *testing.T) {
	for _, theme := range []string{"default", "journal", "midnight"} {
		out := renderBlockOf(t, theme, "pricing", map[string]any{
			"count":      "2",
			"plan1_name": "Basic <b>", "plan1_price": "$5", "plan1_period": "a month", "plan1_features": "One\n\n<i>Two</i>\nThree",
			"plan1_button_label": "Buy", "plan1_button_url": "/buy",
			"plan2_name": "Pro", "plan2_price": "$9", "plan2_featured": "on", "plan2_button_label": "No link", "plan2_button_url": "javascript:alert(1)",
			"plan3_name": "Hidden by the count",
		})
		for _, want := range []string{
			`<h3>Basic &lt;b&gt;</h3>`, `<span class="plan-amount">$5</span>`, `<li>&lt;i&gt;Two&lt;/i&gt;</li>`, `<li>Three</li>`,
			`class="btn btn-outline" href="/buy">Buy</a>`, `class="plan plan-featured"`, `Best value`, `cols-2`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: pricing lacks %q\n%s", theme, want, out)
			}
		}
		if strings.Count(out, `<article class="plan`) != 2 || strings.Contains(out, "Hidden by the count") || strings.Contains(out, "javascript:") || strings.Contains(out, "No link") {
			t.Errorf("%s: plan count / unsafe link / button without link:\n%s", theme, out)
		}
	}
}

func TestTestimonials(t *testing.T) {
	out := renderBlockOf(t, "default", "testimonials", map[string]any{
		"count": "3", "t1_quote": "Great <script>alert(1)</script>", "t1_name": "Ann", "t1_role": "CEO", "t1_photo": "/media/ann.jpg",
		"t2_quote": "Fine", "t3_name": "No quote, so no card",
	})
	for _, want := range []string{`<blockquote><p>Great &lt;script&gt;alert(1)&lt;/script&gt;</p></blockquote>`, `<strong>Ann</strong>`, `testimonial-role">CEO`, `src="/media/ann.jpg" alt=""`} {
		if !strings.Contains(out, want) {
			t.Errorf("testimonials lack %q\n%s", want, out)
		}
	}
	if strings.Count(out, `<figure class="testimonial">`) != 2 || strings.Contains(out, "No quote") {
		t.Errorf("a testimonial needs a quote:\n%s", out)
	}
	if strings.Contains(renderBlockOf(t, "default", "testimonials", map[string]any{"t1_quote": "Q", "t1_photo": "javascript:alert(1)"}), "javascript:") {
		t.Error("unsafe photo address printed")
	}
}

func TestFAQ(t *testing.T) {
	props := map[string]any{
		"heading": "Questions", "q1_question": "Is it <free>?", "q1_answer": `<p>Yes</p><script>alert(1)</script>`,
		"q2_question": "Second?", "q2_answer": "Plain text answer", "q3_answer": "answer with no question",
	}
	out := renderBlockOf(t, "default", "faq", props)
	for _, want := range []string{`<summary>Is it &lt;free&gt;?</summary>`, `<p>Yes</p>`, `<summary>Second?</summary>`, `<p>Plain text answer</p>`, `<h2>Questions</h2>`} {
		if !strings.Contains(out, want) {
			t.Errorf("faq lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "<script") || strings.Contains(out, "no question") || strings.Count(out, "<details") != 2 {
		t.Errorf("faq:\n%s", out)
	}
	if strings.Contains(out, " open>") {
		t.Error("nothing is open by default")
	}
	props["open_first"] = "on"
	out = renderBlockOf(t, "default", "faq", props)
	if strings.Count(out, ` open>`) != 1 || !strings.HasPrefix(out[strings.Index(out, "<details"):], `<details class="faq-item" open>`) {
		t.Errorf("only the first answer opens:\n%s", out)
	}
}

func TestTabsMarkup(t *testing.T) {
	out := renderBlockOf(t, "default", "tabs", map[string]any{
		"tab1_title": "One", "tab1_content": "<p>First</p>", "tab2_title": "Two <b>", "tab2_content": "<p onclick=x()>Second</p>",
		"tab3_content": "no title", "tab4_title": "Four", "tab4_content": "Fourth",
	})
	for _, want := range []string{
		`data-tabs`, `id="tab-7-0" aria-labelledby="tab-7-0-title"`, `<h3 class="tab-title" id="tab-7-1-title">Two &lt;b&gt;</h3>`,
		`<p>Second</p>`, `id="tab-7-3"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tabs lack %q\n%s", want, out)
		}
	}
	if strings.Count(out, `class="tab-panel"`) != 3 || strings.Contains(out, "no title") || strings.Contains(out, "onclick") {
		t.Errorf("tabs:\n%s", out)
	}
}

func TestVideoBlock(t *testing.T) {
	out := renderBlockOf(t, "default", "video", map[string]any{"url": "https://youtu.be/dQw4w9WgXcQ?t=5", "title": `Intro "film"`, "caption": "Our story", "ratio": "4-3"})
	for _, want := range []string{
		`src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0&amp;start=5"`, `title="Intro &#34;film&#34;"`, `ratio-4-3`, `loading="lazy"`, `sandbox="`, `<figcaption>Our story</figcaption>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("video lacks %q\n%s", want, out)
		}
	}
	for _, u := range []string{"", "https://evil.example/x", "/media/a.mp4", "javascript:alert(1)", "https://youtu.be/x\"><script>"} {
		got := renderBlockOf(t, "default", "video", map[string]any{"url": u, "caption": "caption"})
		if strings.Contains(got, "<iframe") || strings.Contains(got, "<video") || strings.Contains(got, "caption") {
			t.Errorf("%q should render nothing:\n%s", u, got)
		}
	}
}

func TestMapBlock(t *testing.T) {
	out := renderBlockOf(t, "default", "map", map[string]any{"coordinates": "51.5007, -0.1246", "zoom": 15.0, "title": "Our office", "address": "1 High St <London>", "height": "lg"})
	for _, want := range []string{
		`src="https://www.openstreetmap.org/export/embed.html?bbox=`, `marker=51.500700%2C-0.124600`, `title="Our office"`, `map-lg`,
		`1 High St &lt;London&gt; · `, `href="https://www.openstreetmap.org/?mlat=51.500700&amp;mlon=-0.124600#map=15/51.500700/-0.124600"`, `sandbox="`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("map lacks %q\n%s", want, out)
		}
	}
	for _, c := range []string{"", "London", "999, 999"} {
		if got := renderBlockOf(t, "default", "map", map[string]any{"coordinates": c}); strings.Contains(got, "<iframe") {
			t.Errorf("%q should render no map:\n%s", c, got)
		}
	}
}

func TestGoogleMapBlock(t *testing.T) {
	for _, theme := range []string{"default", "journal", "midnight"} {
		out := renderBlockOf(t, theme, "map", map[string]any{"provider": "google", "place": `1 High St, "Town" <b>`, "zoom": 16.0, "title": "Find us", "height": "sm"})
		for _, want := range []string{
			`src="https://www.google.com/maps?q=1&#43;High&#43;St%2C&#43;%22Town%22&#43;%3Cb%3E&amp;z=16&amp;output=embed"`, `title="Find us"`, `map-sm`,
			`1 High St, &#34;Town&#34; &lt;b&gt; · `, `href="https://www.google.com/maps/search/?api=1&amp;query=`, `sandbox="`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: google map lacks %q\n%s", theme, want, out)
			}
		}
		if strings.Contains(out, "<b>") || strings.Contains(out, "openstreetmap") {
			t.Errorf("%s: the address is escaped and OpenStreetMap is not used:\n%s", theme, out)
		}
	}
	// a caption of its own replaces the address under the map
	if out := renderBlockOf(t, "default", "map", map[string]any{"provider": "google", "place": "Big Ben", "address": "Our office"}); !strings.Contains(out, "Our office · ") || strings.Contains(out, "Big Ben · ") {
		t.Errorf("caption:\n%s", out)
	}
	// no address, or only coordinates, shows no map; and an old block (no service saved) is still OpenStreetMap
	for _, props := range []map[string]any{{"provider": "google"}, {"provider": "google", "coordinates": "51.5, -0.12"}, {"provider": "osm", "place": "Big Ben"}} {
		if out := renderBlockOf(t, "default", "map", props); strings.Contains(out, "<iframe") {
			t.Errorf("%v should show no map:\n%s", props, out)
		}
	}
	if out := renderBlockOf(t, "default", "map", map[string]any{"coordinates": "51.5, -0.12"}); !strings.Contains(out, "openstreetmap.org/export") {
		t.Errorf("a map saved before the choice existed is unchanged:\n%s", out)
	}
}

func TestOpenStreetMapIgnoresTheGoogleAddress(t *testing.T) {
	out := renderBlockOf(t, "default", "map", map[string]any{"provider": "osm", "coordinates": "51.5, -0.12", "place": "Big Ben"})
	if strings.Contains(out, "Big Ben") || !strings.Contains(out, "<figcaption><a ") {
		t.Errorf("only a Google map shows its address as the caption:\n%s", out)
	}
}
