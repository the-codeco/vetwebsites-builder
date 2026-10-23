package export

import (
	"strings"
	"testing"
)

func TestRedirectRulesForOldJoomlaAddresses(t *testing.T) {
	b := &builder{base: "/site", redirs: []Redirect{
		{From: "/index.php/*", To: "/*", Permanent: true},
		{From: "/index.php", To: "/", Permanent: true},
		{From: "/index.php?id=5&option=com_content", To: "/animal-control-poles", Permanent: true},
		{From: "/index.php?id=1&option=com_contact", To: "/contact-us"},
		{From: "/index.php/about-us/staff", To: "/staff", Permanent: true},
		{From: "/bad?x=\"y", To: "/x"},
	}}
	got := b.redirectRules()
	want := []string{
		// query rules first: each value as a condition, in any order, and the old query dropped
		"RewriteCond %{QUERY_STRING} (^|&)id=5(&|$) [NC]\nRewriteCond %{QUERY_STRING} (^|&)option=com_content(&|$) [NC]\nRewriteRule ^index\\.php$ /site/animal-control-poles? [R=301,L,NC]\n",
		"RewriteCond %{QUERY_STRING} (^|&)id=1(&|$) [NC]\nRewriteCond %{QUERY_STRING} (^|&)option=com_contact(&|$) [NC]\nRewriteRule ^index\\.php$ /site/contact-us? [R=302,L,NC]\n",
		"RewriteRule ^index\\.php/?$ /site/ [R=301,L,NC]\n",
		"RewriteRule ^index\\.php/about-us/staff/?$ /site/staff [R=301,L,NC]\n",
		// the group last: the rest of the address, without .html
		"RewriteRule ^index\\.php(?:/(.*?))?(?:\\.html?)?/?$ /site/$1 [R=301,L,NC]\n",
	}
	at := 0
	for _, w := range want {
		i := strings.Index(got[at:], w)
		if i < 0 {
			t.Fatalf("rules lack (in this order) %q\n%s", w, got)
		}
		at += i + len(w)
	}
	if strings.Contains(got, `"`) || !strings.Contains(got, "x=%22y") { // a quote in a value is encoded, never written as is
		t.Errorf("an unsafe value was written:\n%s", got)
	}
}
