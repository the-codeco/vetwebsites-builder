package footer

import (
	"strings"
	"testing"

	"webbuilder/internal/menus"
)

func row(cols ...Column) Row { return Row{Columns: cols} }

func col(ws ...Widget) Column { return Column{Widgets: ws} }

func TestValidate(t *testing.T) {
	out, err := Validate(Footer{
		Bottom: "  © {year}   {title} ",
		Style:  Style{Background: "#1A2B3C", Padding: "huge"},
		Rows: []Row{
			{Ratio: "wide-middle", Align: "sideways", Columns: []Column{
				col(Widget{Type: TypeText, Heading: " About ", Text: `Hi <a href="javascript:x()">x</a><script>alert(1)</script>`}),
				col(Widget{Type: TypeLinks, Links: []Link{{Label: "Home", URL: " / "}, {}}}), // a blank "add" row is dropped
			}},
			row(col(), col()), // nothing in it: dropped
			row(col(Widget{Type: TypeContact, Parts: []string{"hours", "nope", "phone"}})),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Version != Version || out.Bottom != "© {year} {title}" || out.Style.Background != "#1a2b3c" || out.Style.Padding != "md" || len(out.Rows) != 2 {
		t.Fatalf("%+v", out)
	}
	r := out.Rows[0]
	if r.Ratio != "equal" || r.Align != "left" { // "wide-middle" needs 3 columns
		t.Errorf("row: %+v", r)
	}
	if w := r.Columns[0].Widgets[0]; w.Heading != "About" || strings.Contains(w.Text, "script") || strings.Contains(w.Text, "javascript") {
		t.Errorf("text: %+v", w)
	}
	if l := r.Columns[1].Widgets[0].Links; len(l) != 1 || l[0].URL != "/" {
		t.Errorf("links: %+v", l)
	}
	if p := out.Rows[1].Columns[0].Widgets[0].Parts; strings.Join(p, ",") != "phone,hours" {
		t.Errorf("parts are known ones, in order: %v", p)
	}
	if got := out.BottomLine("Vets", 2026); got != "© 2026 Vets" {
		t.Errorf("bottom line: %q", got)
	}
	if got := (Footer{}).BottomLine("Vets", 2026); got != "© 2026 Vets" {
		t.Errorf("default bottom line: %q", got)
	}

	tooMany := make([]Column, MaxColumns+1)
	for i := range tooMany {
		tooMany[i] = col(Widget{Type: TypeSocial})
	}
	for name, f := range map[string]Footer{
		"unknown widget":  {Rows: []Row{row(col(Widget{Type: "iframe"}))}},
		"bad link":        {Rows: []Row{row(col(Widget{Type: TypeLinks, Links: []Link{{Label: "x", URL: "javascript:alert(1)"}}}))}},
		"link no label":   {Rows: []Row{row(col(Widget{Type: TypeLinks, Links: []Link{{URL: "/x"}}}))}},
		"bad colour":      {Style: Style{Text: "red;}body{x"}},
		"too many cols":   {Rows: []Row{{Columns: tooMany}}},
		"long bottom":     {Bottom: strings.Repeat("b", MaxBottom+1)},
		"long newsletter": {Rows: []Row{row(col(Widget{Type: TypeNewsletter, Button: strings.Repeat("b", MaxShort+1)}))}},
	} {
		if _, err := Validate(f); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestDecode(t *testing.T) {
	if _, err := Decode([]byte(`{"version":2,"rows":[{"columns":[{"widgets":[{"type":"text","text":"x"}]}]}],"style":{}}`)); err != nil {
		t.Error(err)
	}
	for name, in := range map[string]string{"not json": "nope", "unknown field": `{"rows":[],"evil":1}`, "too big": strings.Repeat(" ", MaxJSON+1)} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestStoredRoundTripAndTrim(t *testing.T) {
	f, err := Validate(Footer{Bottom: "B", Style: Style{Text: "#ffffff", Padding: "lg"}, Rows: []Row{row(col(Widget{Type: TypeLogo, Text: "Caring since 1990"}))}})
	if err != nil {
		t.Fatal(err)
	}
	m := f.AsMap()
	if !IsCurrent(m) {
		t.Fatal("a saved footer must count as current")
	}
	if back := FromStored(m); back.Bottom != "B" || back.Style.Padding != "lg" || back.Rows[0].Columns[0].Widgets[0].Text != "Caring since 1990" {
		t.Errorf("round trip: %+v", back)
	}
	// stored data over the limits is cut down, not thrown away
	m["bottom"] = strings.Repeat("b", 500)
	m["style"] = map[string]any{"background": "url(x)"}
	got := FromStored(m)
	if len([]rune(got.Bottom)) != MaxBottom || got.Style.Background != "" || len(got.Rows) != 1 {
		t.Errorf("trim: %+v", got)
	}
	if e := FromStored(map[string]any{"version": 2, "rows": "nonsense"}); len(e.Rows) != 0 || e.Version != Version {
		t.Errorf("unreadable: %+v", e)
	}
}

func TestUpgrade(t *testing.T) {
	legacy := map[string]any{"bottom": "Old line", "columns": []any{
		map[string]any{"heading": "Visit us", "text": "<p>1 Main Road</p>"},
		map[string]any{"heading": "", "text": " "}, // empty: dropped
		map[string]any{"heading": "Hours", "text": "Mon–Fri"},
	}}
	menu := []menus.Item{
		{Kind: menus.KindLink, Label: "Privacy", URL: "/privacy", NewTab: true},
		{Kind: menus.KindLink, Label: "Hidden", URL: "/h", Hidden: true},
	}
	f := Upgrade(legacy, menu, true)
	if f.Version != Version || f.Bottom != "Old line" || len(f.Rows) != 2 {
		t.Fatalf("%+v", f)
	}
	links := f.Rows[0].Columns[0].Widgets[0]
	if links.Type != TypeLinks || !links.Inline || len(links.Links) != 1 || !links.Links[0].NewTab || f.Rows[0].Align != "center" {
		t.Errorf("footer menu row: %+v", f.Rows[0])
	}
	cols := f.Rows[1].Columns
	if len(cols) != 3 || cols[0].Widgets[0].Heading != "Visit us" || cols[1].Widgets[0].Heading != "Hours" || cols[2].Widgets[0].Type != TypeContact || len(cols[2].Widgets[0].Parts) != len(ContactParts) {
		t.Errorf("columns: %+v", cols)
	}

	// four old columns plus contact details: a second row
	four := map[string]any{"columns": []any{
		map[string]any{"heading": "1"}, map[string]any{"heading": "2"}, map[string]any{"heading": "3"}, map[string]any{"heading": "4"}, map[string]any{"heading": "5 was never shown"},
	}}
	f = Upgrade(four, nil, true)
	if len(f.Rows) != 2 || len(f.Rows[0].Columns) != 4 || len(f.Rows[1].Columns) != 1 || f.Rows[1].Columns[0].Widgets[0].Type != TypeContact {
		t.Errorf("wrap: %+v", f.Rows)
	}

	if e := Upgrade(map[string]any{}, nil, false); len(e.Rows) != 0 || e.Bottom != "" || e.Version != Version {
		t.Errorf("nothing to upgrade: %+v", e)
	}
}

func TestStyle(t *testing.T) {
	s := Style{Background: "#000000", Text: "#ffffff", Padding: "sm"}
	if s.Classes() != " footer-pad-sm has-bg has-fg" || string(s.CSSVars()) != "--footer-bg:#000000;--footer-fg:#ffffff;" {
		t.Errorf("%q %q", s.Classes(), s.CSSVars())
	}
	if v := (Style{Background: "red;}"}).CSSVars(); v != "" {
		t.Errorf("unsafe colour: %q", v)
	}
}

func TestLogoWidgetChoice(t *testing.T) {
	out, err := Validate(Footer{Rows: []Row{row(
		col(Widget{Type: TypeLogo, Logo: LogoAlternative}),
		col(Widget{Type: TypeLogo, Logo: "something else"}), // unknown: the main logo
		col(Widget{Type: TypeText, Logo: LogoAlternative}),  // only logo widgets choose
	)}})
	if err != nil {
		t.Fatal(err)
	}
	c := out.Rows[0].Columns
	if c[0].Widgets[0].Logo != LogoAlternative || c[1].Widgets[0].Logo != "" || c[2].Widgets[0].Logo != "" {
		t.Errorf("%+v", c)
	}
}

func TestSidebarAndNewWidgets(t *testing.T) {
	out, err := ValidateSidebar(Sidebar{Side: "up", Width: "huge", Widgets: []Widget{
		{Type: TypeSection},
		{Type: TypeSearch, Heading: " Search  the site "},
		{Type: TypeImage, Image: " /media/dog.jpg ", Alt: " A dog ", URL: "https://vetdirectory.example", Text: "Our practice dog"},
		{Type: TypeText, Heading: "Hours", Text: "Mon–Fri <script>x</script>"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Side != "left" || out.Width != "narrow" || len(out.Widgets) != 4 {
		t.Fatalf("%+v", out)
	}
	if img := out.Widgets[2]; img.Image != "/media/dog.jpg" || img.Alt != "A dog" || img.URL != "https://vetdirectory.example" || img.Text != "Our practice dog" {
		t.Errorf("picture: %+v", img)
	}
	if out.Widgets[1].Heading != "Search the site" || strings.Contains(out.Widgets[3].Text, "script") {
		t.Errorf("%+v", out.Widgets)
	}
	tooMany := make([]Widget, MaxSidebarWidgets+1)
	for i := range tooMany {
		tooMany[i] = Widget{Type: TypeSearch}
	}
	for name, sb := range map[string]Sidebar{
		"no picture":     {Widgets: []Widget{{Type: TypeImage}}},
		"unsafe picture": {Widgets: []Widget{{Type: TypeImage, Image: "javascript:x"}}},
		"unsafe link":    {Widgets: []Widget{{Type: TypeImage, Image: "/a.jpg", URL: "javascript:x"}}},
		"too many":       {Widgets: tooMany},
		"unknown":        {Widgets: []Widget{{Type: "iframe"}}},
	} {
		if _, err := ValidateSidebar(sb); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := DecodeSidebar([]byte(`{"side":"left","widgets":[],"evil":1}`)); err == nil {
		t.Error("unknown field accepted")
	}
	// stored data is cut down, not refused: bad pictures and unknown widgets are dropped
	got := SidebarFromStored(map[string]any{"side": "right", "widgets": []any{
		map[string]any{"type": "image", "image": "javascript:x"}, map[string]any{"type": "iframe"},
		map[string]any{"type": "image", "image": "/ok.jpg", "url": "javascript:y"}, map[string]any{"type": "search"},
	}})
	if got.Side != "right" || len(got.Widgets) != 2 || got.Widgets[0].URL != "" || got.Widgets[1].Type != TypeSearch {
		t.Errorf("stored: %+v", got)
	}
	if e := SidebarFromStored(nil); len(e.Widgets) != 0 || e.Side != "left" {
		t.Errorf("empty: %+v", e)
	}
}
