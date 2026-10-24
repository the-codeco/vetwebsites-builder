package elements

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

func at(h int) *time.Time { t := now.Add(time.Duration(h) * time.Hour); return &t }

func TestStatus(t *testing.T) {
	for name, c := range map[string]struct {
		e    Element
		want Status
	}{
		"always":     {Element{Enabled: true}, StatusLive},
		"off":        {Element{Enabled: false}, StatusOff},
		"off beats":  {Element{Enabled: false, End: at(-1)}, StatusOff},
		"scheduled":  {Element{Enabled: true, Start: at(1)}, StatusScheduled},
		"in window":  {Element{Enabled: true, Start: at(-1), End: at(1)}, StatusLive},
		"ended":      {Element{Enabled: true, Start: at(-3), End: at(-1)}, StatusEnded},
		"ends now":   {Element{Enabled: true, End: &now}, StatusEnded},
		"starts now": {Element{Enabled: true, Start: &now}, StatusLive},
		"open ended": {Element{Enabled: true, Start: at(-1)}, StatusLive},
		"open start": {Element{Enabled: true, End: at(5)}, StatusLive},
	} {
		if got := c.e.StatusAt(now); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

func TestShowsOn(t *testing.T) {
	e := Element{Pages: []string{"/", "/about", "/blog/*"}}
	for path, want := range map[string]bool{
		"/": true, "/about": true, "/About/": true, "/blog": true, "/blog/": true, "/blog/post-1": true, "/blog/category/x/page/2": true,
		"/contact": false, "/aboutus": false, "/blogging": false, "/about/team": false,
	} {
		if got := e.ShowsOn(path); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
	if !(Element{}).ShowsOn("/anything") {
		t.Error("no pages chosen means every page")
	}
}

func TestCleanPages(t *testing.T) {
	got, err := CleanPages([]string{" /About ", "/about", "/", "", "/blog/*/"})
	if err != nil || strings.Join(got, ",") != "/about,/,/blog/*" {
		t.Errorf("clean: %v %v", got, err)
	}
	for _, bad := range []string{"about", "/a b", "/a/*/b", "/*", "//x", "/a?b=1", "/a#b", "https://x.example/a", "/a*"} {
		if _, err := CleanPages([]string{bad}); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	many := make([]string, MaxPages+1)
	for i := range many {
		many[i] = "/p" + strings.Repeat("a", i+1)
	}
	if _, err := CleanPages(many); err == nil {
		t.Error("too many pages")
	}
}

func TestCleanKeepsOnlyWhatAKindUses(t *testing.T) {
	e, err := Clean(Element{Kind: KindBar, Enabled: true, Config: Config{Text: "  Open   late  ", Link: "/contact", Style: "neon", Heading: "dropped", Delay: 9, Mode: "subscribe"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	c := e.Config
	if c.Text != "Open late" || c.Style != StyleInfo || c.Heading != "" || c.Delay != 0 || c.Mode != "" || c.LinkLabel != "Read more" {
		t.Errorf("bar: %+v", c)
	}
	p, err := Clean(Element{Kind: KindPopup, Config: Config{Heading: "Hi", Text: "<p>x</p><script>y()</script>", Mode: "subscribe", Trigger: "scroll", Scroll: 1, Link: "/x", DismissDays: 9999}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Config.Text, "script") || p.Config.Scroll != 5 || p.Config.Link != "" || p.Config.DismissDays != MaxDismissDays {
		t.Errorf("popup: %+v", p.Config)
	}
	c2, err := Clean(Element{Kind: KindContact, Config: Config{Action: ActionWhatsApp, Link: "/ignored", Side: "middle"}}, now)
	if err != nil || c2.Config.Link != "" || c2.Config.Side != SideRight || c2.Config.Label != "WhatsApp" {
		t.Errorf("contact: %+v %v", c2.Config, err)
	}
}

func TestCleanRefuses(t *testing.T) {
	for name, e := range map[string]Element{
		"kind":         {Kind: "x"},
		"empty bar":    {Kind: KindBar},
		"unsafe link":  {Kind: KindAlert, Config: Config{Text: "x", Link: "javascript:alert(1)"}},
		"long name":    {Kind: KindBar, Name: strings.Repeat("n", MaxName+1), Config: Config{Text: "x"}},
		"end first":    {Kind: KindBar, Config: Config{Text: "x"}, Start: at(2), End: at(1)},
		"equal times":  {Kind: KindBar, Config: Config{Text: "x"}, Start: at(1), End: at(1)},
		"far future":   {Kind: KindBar, Config: Config{Text: "x"}, End: at(24 * 366 * 10)},
		"empty popup":  {Kind: KindPopup},
		"huge popup":   {Kind: KindPopup, Config: Config{Text: "<p>" + strings.Repeat("a", MaxBody) + "</p>"}},
		"no action":    {Kind: KindContact},
		"link no link": {Kind: KindContact, Config: Config{Action: ActionLink}},
	} {
		if _, err := Clean(e, now); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := Clean(Element{Kind: KindBar, Config: Config{Text: "x"}, End: at(-5)}, now); err != nil {
		t.Errorf("an element whose end has passed can still be saved: %v", err)
	}
}

func TestVersion(t *testing.T) {
	fp := func(b []byte) string { return string(b) }
	a := Element{Kind: KindBar, Config: Config{Text: "x"}}
	b := a
	b.End = at(3)
	b.Pages = []string{"/a"}
	if a.Version(fp) != b.Version(fp) {
		t.Error("moving the window or the pages is not a new version")
	}
	b.Config.Text = "y"
	if a.Version(fp) == b.Version(fp) {
		t.Error("changing what it says is a new version")
	}
}

func TestTitle(t *testing.T) {
	for want, e := range map[string]Element{
		"Winter":           {Kind: KindBar, Name: "Winter", Config: Config{Text: "x"}},
		"Join us":          {Kind: KindPopup, Config: Config{Heading: "Join us"}},
		"Closed on Monday": {Kind: KindAlert, Config: Config{Text: "Closed on Monday"}},
		"Call us":          {Kind: KindContact, Config: Config{Label: "Call us"}},
		"Notice bar":       {Kind: KindBar},
	} {
		if got := e.Title(); got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}
}
