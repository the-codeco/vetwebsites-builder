package contact

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	good := Input{Name: "  Ada   Lovelace ", Email: " ada@example.com ", Subject: "Hi", Body: "  Hello\r\nthere  "}
	got, problem := Validate(good)
	if problem != "" || got.Name != "Ada Lovelace" || got.Email != "ada@example.com" || got.Body != "Hello\nthere" {
		t.Errorf("good input: %+v %q", got, problem)
	}
	if _, p := Validate(Input{Name: "A", Email: "a@b.co", Body: "x"}); p != "" {
		t.Errorf("subject is optional: %q", p)
	}

	for name, in := range map[string]Input{
		"no name":        {Email: "a@b.co", Body: "x"},
		"blank name":     {Name: "   ", Email: "a@b.co", Body: "x"},
		"long name":      {Name: strings.Repeat("n", MaxName+1), Email: "a@b.co", Body: "x"},
		"bad email":      {Name: "A", Email: "nope", Body: "x"},
		"long subject":   {Name: "A", Email: "a@b.co", Subject: strings.Repeat("s", MaxSubject+1), Body: "x"},
		"no message":     {Name: "A", Email: "a@b.co", Body: " \n "},
		"long message":   {Name: "A", Email: "a@b.co", Body: strings.Repeat("m", MaxBody+1)},
		"header in mail": {Name: "A", Email: "a@b.co\nBcc: x@y.co", Body: "x"},
	} {
		if _, p := Validate(in); p == "" {
			t.Errorf("%s: accepted", name)
		}
	}

	// control characters can't survive into names or subjects, so they can't reach an email header
	in, _ := Validate(Input{Name: "Eve\r\nBcc: x@y.co", Email: "e@b.co", Subject: "Hi\r\nBcc: x@y.co\x00", Body: "ok\x07 fine"})
	if strings.ContainsAny(in.Name+in.Subject, "\r\n\x00") || strings.Contains(in.Body, "\x07") {
		t.Errorf("control characters kept: %q %q %q", in.Name, in.Subject, in.Body)
	}
	if _, p := Validate(Input{Name: strings.Repeat("é", MaxName), Email: "e@b.co", Body: "x"}); p != "" {
		t.Errorf("limits count characters, not bytes: %q", p)
	}
}
