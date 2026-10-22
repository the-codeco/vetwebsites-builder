package content

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func woff2(size int) []byte {
	b := bytes.Repeat([]byte{1}, size)
	copy(b, "wOF2")
	if size >= 12 {
		binary.BigEndian.PutUint32(b[8:12], uint32(size))
	}
	return b
}

func TestCheckFont(t *testing.T) {
	if err := CheckFont(woff2(1000)); err != nil {
		t.Errorf("a plausible file: %v", err)
	}
	if err := CheckFont(woff2(MaxFontBytes)); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	bad := map[string][]byte{
		"empty": nil, "tiny": woff2(10), "just under the minimum": woff2(MinFontBytes - 1), "too big": woff2(MaxFontBytes + 1),
		"woff1": append([]byte("wOFF"), make([]byte, 1000)...), "ttf": append([]byte{0, 1, 0, 0}, make([]byte, 1000)...),
		"text": []byte(strings.Repeat("hello ", 200)), "html": []byte("<html>" + strings.Repeat(" ", 1000)),
	}
	wrongLen := woff2(1000)
	binary.BigEndian.PutUint32(wrongLen[8:12], 999)
	bad["length field disagrees"] = wrongLen
	wrongSig := woff2(1000) // right length, wrong signature
	copy(wrongSig, "wOFF")
	bad["woff1 with a matching length"] = wrongSig
	lower := woff2(1000)
	copy(lower, "wof2")
	bad["wrong case signature"] = lower
	for name, data := range bad {
		if CheckFont(data) == nil {
			t.Errorf("%s should be refused", name)
		}
	}
}

func TestFontNamesAndTokens(t *testing.T) {
	for in, want := range map[string]string{"Acme Sans": "Acme Sans", "  Acme   Sans ": "Acme Sans", "A": "A", "Open-Sans 2": "Open-Sans 2", "ab": "ab", strings.Repeat("a", 40): strings.Repeat("a", 40)} {
		if got, ok := CleanFamily(in); !ok || got != want {
			t.Errorf("CleanFamily(%q) = %q, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", " ", "-lead", "trail-", " -x", `A"B`, "A;B", "A{B}", "A'B", "<b>", "é", "A/B", "A\\B", "A:B", "url(x)", strings.Repeat("a", 41)} {
		if got, ok := CleanFamily(bad); ok {
			t.Errorf("CleanFamily(%q) should be refused, got %q", bad, got)
		}
	}
	for in, want := range map[string]string{"Acme Sans": "custom:acme-sans", "Open-Sans 2": "custom:open-sans-2", "A  B": "custom:a-b"} {
		if got := FontToken(in); got != want || !IsFontToken(got) {
			t.Errorf("FontToken(%q) = %q", in, got)
		}
	}
	for _, bad := range []string{"", "custom:", "custom:-a", "custom:a-", "custom:A", "custom:a b", "custom:a;b", "sans", "custom:" + strings.Repeat("a", 60)} {
		if IsFontToken(bad) {
			t.Errorf("%q is not a font token", bad)
		}
	}
	if f := (Font{ID: 42, Hash: strings.Repeat("ab", 32)}); f.FileName() != "42-ababababab.woff2" {
		t.Errorf("file name: %s", f.FileName())
	}
}
