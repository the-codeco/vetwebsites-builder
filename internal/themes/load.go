package themes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"

	themefiles "webbuilder/themes"
)

const maxExtendsDepth = 5

var tokenKinds = map[string]bool{"color": true, "font": true, "size": true}

// Load reads every embedded theme, resolves inheritance and parses templates.
func Load() (*Engine, error) {
	entries, err := fs.ReadDir(themefiles.FS, ".")
	if err != nil {
		return nil, err
	}
	metas := map[string]rawMeta{}
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(themefiles.FS, d.Name()+"/theme.json")
		if err != nil {
			return nil, fmt.Errorf("theme %q: %w", d.Name(), err)
		}
		var m rawMeta
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("theme %q: theme.json: %w", d.Name(), err)
		}
		metas[d.Name()] = m
	}
	if _, ok := metas[DefaultTheme]; !ok {
		return nil, fmt.Errorf("default theme missing")
	}

	e := &Engine{themes: map[string]*Theme{}}
	for name := range metas {
		t, err := buildTheme(name, metas)
		if err != nil {
			return nil, fmt.Errorf("theme %q: %w", name, err)
		}
		e.themes[name] = t
	}
	return e, nil
}

// chainOf returns the theme's ancestry, root (default) first, ending with name.
func chainOf(name string, metas map[string]rawMeta) ([]string, error) {
	var rev []string
	seen := map[string]bool{}
	for cur := name; ; {
		if seen[cur] || len(rev) > maxExtendsDepth {
			return nil, fmt.Errorf("extends loop or chain deeper than %d", maxExtendsDepth)
		}
		seen[cur] = true
		rev = append(rev, cur)
		if cur == DefaultTheme {
			break
		}
		parent := metas[cur].Extends
		if parent == "" {
			parent = DefaultTheme
		}
		if _, ok := metas[parent]; !ok {
			return nil, fmt.Errorf("extends unknown theme %q", parent)
		}
		cur = parent
	}
	chain := make([]string, len(rev))
	for i, n := range rev {
		chain[len(rev)-1-i] = n
	}
	return chain, nil
}

// mergeTokens applies child over parent: matching keys override the non-empty
// fields, new keys are appended.
func mergeTokens(parent, child []Token) ([]Token, error) {
	out := append([]Token(nil), parent...)
	for _, c := range child {
		found := false
		for i := range out {
			if out[i].Key != c.Key {
				continue
			}
			found = true
			if c.Label != "" {
				out[i].Label = c.Label
			}
			if c.Kind != "" {
				out[i].Kind = c.Kind
			}
			if c.Default != "" {
				out[i].Default = c.Default
				out[i].Dark = "" // the palette changed: the parent's dark value no longer fits it
			}
			if c.Dark != "" {
				out[i].Dark = c.Dark
			}
		}
		if !found {
			if c.Label == "" {
				c.Label = c.Key
			}
			out = append(out, c)
		}
	}
	for _, tk := range out {
		if !keyRe.MatchString(tk.Key) || !tokenKinds[tk.Kind] || !ValidToken(tk.Kind, tk.Default) {
			return nil, fmt.Errorf("invalid token %q", tk.Key)
		}
		if tk.Dark != "" && (tk.Kind != "color" || !ValidToken(tk.Kind, tk.Dark)) {
			return nil, fmt.Errorf("invalid dark value for token %q", tk.Key)
		}
	}
	return out, nil
}

// fingerprint is a short hash of a file's content, for cache-busting addresses.
func Fingerprint(b []byte) string { return fingerprint(b) }

func fingerprint(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:5])
}

func buildTheme(name string, metas map[string]rawMeta) (*Theme, error) {
	chain, err := chainOf(name, metas)
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(themefiles.FS, name)
	if err != nil {
		return nil, err
	}
	m := metas[name]
	t := &Theme{Name: name, Label: m.Label, Description: m.Description, FS: sub}
	if t.Label == "" {
		t.Label = name
	}

	// The menu script ships with the default theme and every layout loads it from there. Like the
	// stylesheets, its address carries a fingerprint so an updated script isn't hidden by the cache.
	t.menuJS = "/theme/" + DefaultTheme + "/assets/menu.js"
	if js, err := fs.ReadFile(themefiles.FS, DefaultTheme+"/assets/menu.js"); err == nil {
		t.menuJS += "?v=" + fingerprint(js)
	}

	t.consentJS = "/theme/" + DefaultTheme + "/assets/consent.js"
	if js, err := fs.ReadFile(themefiles.FS, DefaultTheme+"/assets/consent.js"); err == nil {
		t.consentJS += "?v=" + fingerprint(js)
	}
	t.trackJS = "/theme/" + DefaultTheme + "/assets/track.js"
	if js, err := fs.ReadFile(themefiles.FS, DefaultTheme+"/assets/track.js"); err == nil {
		t.trackJS += "?v=" + fingerprint(js)
	}
	t.colorJS = "/theme/" + DefaultTheme + "/assets/colormode.js"
	if js, err := fs.ReadFile(themefiles.FS, DefaultTheme+"/assets/colormode.js"); err == nil {
		t.colorJS += "?v=" + fingerprint(js)
	}

	t.tmpl = template.New(name).Funcs(funcs)
	for _, n := range chain {
		dir, err := fs.Sub(themefiles.FS, n)
		if err != nil {
			return nil, err
		}
		if t.Tokens, err = mergeTokens(t.Tokens, metas[n].Tokens); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		if err := parseInto(t.tmpl, dir); err != nil { // later themes override earlier definitions
			return nil, err
		}
		if css, err := fs.ReadFile(dir, "theme.css"); err == nil {
			// The file's fingerprint is part of the address, so browsers (which keep theme files for
			// an hour) fetch the new stylesheet as soon as a release changes it.
			t.Styles = append(t.Styles, "/theme/"+n+"/theme.css?v="+fingerprint(css))
		}
	}
	return t, nil
}
