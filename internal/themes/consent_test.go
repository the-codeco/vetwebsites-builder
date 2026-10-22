package themes

import (
	"bytes"
	"strings"
	"testing"
)

func renderWith(t *testing.T, theme string, info SiteInfo) string {
	t.Helper()
	e, _ := Load()
	th, _ := e.Lookup(theme)
	var buf bytes.Buffer
	if err := th.RenderPage(&buf, PageData{Site: info, Content: "<p>x</p>", Year: 2026}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestConsentBannerMarkup(t *testing.T) {
	e, _ := Load()
	for _, name := range []string{"default", "journal", "midnight"} {
		th, _ := e.Lookup(name)
		off := renderWith(t, name, th.Info("T", false))
		for _, not := range []string{`id="consent"`, "consent.js", "consent-reopen"} {
			if strings.Contains(off, not) {
				t.Errorf("%s: a site with the notice off contains %q", name, not)
			}
		}

		info := th.Info("T", false)
		info.Consent = &ConsentInfo{Text: `We use <b>cookies</b> & such`, Version: "abc123", LinkLabel: "Privacy", LinkURL: "/privacy"}
		notice := renderWith(t, name, info)
		for _, want := range []string{
			`<div class="consent" id="consent" role="region" aria-label="Cookie notice" data-consent-version="abc123" hidden>`,
			`We use &lt;b&gt;cookies&lt;/b&gt; &amp; such`, `<a href="/privacy">Privacy</a>`, `data-consent="ok">Got it</button>`,
			`<script src="` + info.ConsentJS + `" defer></script>`,
		} {
			if !strings.Contains(notice, want) {
				t.Errorf("%s: notice lacks %q", name, want)
			}
		}
		for _, not := range []string{`data-consent="accepted"`, `data-consent="declined"`, "data-consent-reopen"} {
			if strings.Contains(notice, not) {
				t.Errorf("%s: a plain notice contains %q", name, not)
			}
		}
		if strings.Index(notice, `id="consent"`) > strings.Index(notice, "</body>") {
			t.Errorf("%s: the notice must be inside <body>", name)
		}

		info.Consent.Ask, info.Consent.LinkURL = true, ""
		choice := renderWith(t, name, info)
		for _, want := range []string{`data-consent="declined">Decline</button>`, `data-consent="accepted">Accept</button>`, `data-consent-reopen hidden>Cookie settings</button>`} {
			if !strings.Contains(choice, want) {
				t.Errorf("%s: choice mode lacks %q", name, want)
			}
		}
		if strings.Contains(choice, "Got it") || strings.Contains(choice, `>Privacy</a>`) {
			t.Errorf("%s: choice mode shows the wrong buttons or a link without an address", name)
		}
	}
}
