package admin

import "testing"

// Every screen is either a pre-login screen (no sidebar) or belongs to a sidebar section;
// forgetting to register a new screen would leave the sidebar with nothing highlighted.
func TestEveryScreenHasASidebarSection(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range screens {
		if seen[name] {
			t.Errorf("screen %q listed twice", name)
		}
		seen[name] = true
		inShell, auth := sectionOf[name] != "", authScreens[name]
		if inShell == auth {
			t.Errorf("screen %q must be exactly one of: in the sidebar shell (%v) or a pre-login screen (%v)", name, inShell, auth)
		}
	}
	for name := range sectionOf {
		if !seen[name] {
			t.Errorf("sectionOf mentions %q, which is not a screen", name)
		}
	}
	for name := range authScreens {
		if !seen[name] {
			t.Errorf("authScreens mentions %q, which is not a screen", name)
		}
	}
}

func TestTemplatesParse(t *testing.T) {
	if _, err := New(Deps{}); err != nil {
		t.Fatal(err)
	}
}

func TestInitialIsRuneSafe(t *testing.T) {
	f := funcs["initial"].(func(string) string)
	for in, want := range map[string]string{"stephen@example.com": "S", "élodie@example.com": "É", "": "?", "9lives@x.io": "9", "日本@x.jp": "日"} {
		if got := f(in); got != want {
			t.Errorf("initial(%q) = %q, want %q", in, got, want)
		}
	}
}
