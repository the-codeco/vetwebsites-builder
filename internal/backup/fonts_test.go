package backup

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

func fakeFont(size int) []byte {
	b := bytes.Repeat([]byte{7}, size)
	copy(b, "wOF2")
	binary.BigEndian.PutUint32(b[8:12], uint32(size))
	return b
}

func TestInspectChecksFonts(t *testing.T) {
	s := testService(t)
	c := goodContent()
	wrong := fakeFont(1000)
	binary.BigEndian.PutUint32(wrong[8:12], 3)
	c.Fonts = []Font{
		{Family: " Acme   Sans ", Weight: 400, Data: fakeFont(1000)},
		{Family: "acme sans", Weight: 400, Data: fakeFont(1100)}, // the same family and weight again
		{Family: "Acme Sans", Weight: 700, Data: fakeFont(1200)},
		{Family: `Evil"} body{display:none}`, Weight: 400, Data: fakeFont(1000)},
		{Family: "Not Woff", Weight: 400, Data: []byte(strings.Repeat("x", 1000))},
		{Family: "Wrong Length", Weight: 400, Data: wrong},
		{Family: "Odd Weight", Weight: 600, Data: fakeFont(1000)},
		{Family: "Empty", Weight: 400},
	}
	a, err := inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range a.Content.Fonts {
		got = append(got, fmt.Sprintf("%s/%d/%d", f.Family, f.Weight, len(f.Data)))
	}
	if strings.Join(got, ",") != "Acme Sans/400/1000,Acme Sans/700/1200" {
		t.Errorf("fonts kept: %v", got)
	}
	if joined := strings.Join(a.Warnings, "\n"); !strings.Contains(joined, "not a valid WOFF2") || !strings.Contains(joined, "second font file") {
		t.Errorf("what was left out is reported:\n%s", joined)
	}
	c.Fonts = nil
	for i := 0; i < 15; i++ {
		c.Fonts = append(c.Fonts, Font{Family: fmt.Sprintf("Font %d", i), Weight: 400, Data: fakeFont(500)})
	}
	a, err = inspect(t, s, pack(t, c, false, map[string][]byte{imgPath: jpegBytes(t)}))
	if err != nil || len(a.Content.Fonts) != 12 {
		t.Errorf("at most 12: %v %d", err, len(a.Content.Fonts))
	}
}
