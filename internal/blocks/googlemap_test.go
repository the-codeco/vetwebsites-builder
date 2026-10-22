package blocks

import (
	"net/url"
	"strings"
	"testing"
)

func TestGoogleMapOf(t *testing.T) {
	m, ok := GoogleMapOf("  10 Downing   Street,\tLondon & more=1#x ", 99)
	if !ok {
		t.Fatal("a street address is a map")
	}
	u, err := url.Parse(m.Src)
	if err != nil || u.Scheme != "https" || u.Host != hostGoogle || u.Path != "/maps" {
		t.Fatalf("embed address %q", m.Src)
	}
	q := u.Query()
	if q.Get("q") != "10 Downing Street, London & more=1#x" || q.Get("z") != "19" || q.Get("output") != "embed" || len(q) != 3 {
		t.Errorf("the address is one escaped value and nothing else is added: %v", q)
	}
	if l, _ := url.Parse(m.Link); l.Host != hostGoogle || l.Query().Get("query") != q.Get("q") || l.Query().Get("api") != "1" {
		t.Errorf("link %q", m.Link)
	}
	if !strings.Contains(FrameSources, "https://"+hostGoogle) || !strings.HasPrefix(m.Src, "https://"+hostGoogle+"/") {
		t.Error("the embed host is allowed by the content security policy")
	}
	if m, _ := GoogleMapOf("x", 0); !strings.Contains(m.Src, "&z=1&") {
		t.Errorf("zoom lower bound: %s", m.Src)
	}
	for name, in := range map[string]string{"empty": "", "blanks": " \t\n ", "control": "a\x00b", "bell": "a\x07", "too long": strings.Repeat("a", 201)} {
		if _, ok := GoogleMapOf(in, 14); ok {
			t.Errorf("%s should not make a map", name)
		}
	}
	if _, ok := GoogleMapOf(strings.Repeat("é", 200), 14); !ok {
		t.Error("200 characters is allowed, counted in characters not bytes")
	}
}

func TestMapForUsesTheChosenService(t *testing.T) {
	if m, ok := MapFor("google", "51.5, -0.12", "Big Ben", 14); !ok || !strings.Contains(m.Src, hostGoogle) {
		t.Errorf("google: %v %v", m, ok)
	}
	if _, ok := MapFor("google", "51.5, -0.12", "", 14); ok {
		t.Error("Google needs an address, coordinates are not enough")
	}
	for _, p := range []string{"osm", "", "bing", "GOOGLE"} {
		if m, ok := MapFor(p, "51.5, -0.12", "Big Ben", 14); !ok || !strings.Contains(m.Src, hostOSM) {
			t.Errorf("%q is OpenStreetMap: %v %v", p, m, ok)
		}
	}
	if _, ok := MapFor("osm", "", "Big Ben", 14); ok {
		t.Error("OpenStreetMap needs coordinates, an address is not enough")
	}
	bt, _ := Get("map")
	if bt.Clean(map[string]any{"provider": "bing"})["provider"] != "osm" {
		t.Error("only the two services are offered")
	}
	if c := bt.Clean(map[string]any{"provider": "<x>", "place": strings.Repeat("p", 400)}); c["provider"] != "osm" || len([]rune(c["place"].(string))) != 300 {
		t.Errorf("settings are cleaned: %v", c)
	}
}
