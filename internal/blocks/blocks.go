// Package blocks defines the block type registry: each block type declares a
// props schema. The same schema validates stored data (Clean) and drives the
// admin editor form, so adding a block type is a schema plus a theme template.
package blocks

import (
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
)

type Kind string

const (
	KindText      Kind = "text"
	KindTextarea  Kind = "textarea"
	KindRichText  Kind = "richtext"
	KindImage     Kind = "image"
	KindLink      Kind = "link"
	KindSelect    Kind = "select"
	KindColor     Kind = "color"
	KindNumber    Kind = "number"
	KindBool      Kind = "bool"
	KindImageList Kind = "imagelist"
	KindRepeater  Kind = "repeater" // a list of items, each made of the Item fields
)

type Option struct{ Value, Label string }

type Field struct {
	Key, Label string
	Kind       Kind
	Options    []Option // KindSelect
	Default    any
	Min, Max   float64 // KindNumber; bounds apply when Max > Min

	// KindRepeater: the fields of each item, what one item is called in the editor, and an optional
	// lower limit than MaxRepeaterItems.
	Item      []Field
	ItemLabel string
	MaxItems  int
}

type Type struct {
	Name, Label string
	Fields      []Field
	// Internal types are never offered in the "add block" lists; they are made by other actions.
	Internal bool
	// Upgrade, when set, turns props saved by an older version of the block into the current shape
	// before they are cleaned (it must not change the map it is given).
	Upgrade func(map[string]any) map[string]any
}

type Block struct {
	ID    int64
	Type  string
	Props map[string]any
	Style Style
	// Children are the blocks in each column of a layout block (nil for every other kind).
	Children [][]Block
}

var (
	registry = map[string]*Type{}
	order    []string
)

func Register(t *Type) {
	if _, dup := registry[t.Name]; dup {
		panic("blocks: duplicate type " + t.Name)
	}
	registry[t.Name] = t
	order = append(order, t.Name)
}

func Get(name string) (*Type, bool) { t, ok := registry[name]; return t, ok }

// All returns block types in registration order.
func All() []*Type {
	out := make([]*Type, len(order))
	for i, n := range order {
		out[i] = registry[n]
	}
	return out
}

const (
	maxText     = 300
	maxTextarea = 5000
	maxRichText = 50000
	maxURL      = 2000
	maxImages   = 24
)

// Clean returns a props map containing exactly the schema's keys, with every
// value coerced to the field's type and unsafe values replaced by defaults.
func (t *Type) Clean(raw map[string]any) map[string]any {
	if t.Upgrade != nil {
		raw = t.Upgrade(raw)
	}
	return cleanFields(t.Fields, raw)
}

func cleanFields(fields []Field, raw map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		out[f.Key] = f.clean(raw[f.Key])
	}
	return out
}

func (f Field) clean(v any) any {
	switch f.Kind {
	case KindText:
		return truncate(str(v, f.Default), maxText)
	case KindTextarea:
		return truncate(str(v, f.Default), maxTextarea)
	case KindRichText:
		return NormalizeRichText(truncate(str(v, f.Default), maxRichText))
	case KindLink:
		return SafeURL(truncate(str(v, f.Default), maxURL))
	case KindImage:
		return SafeImageURL(truncate(str(v, f.Default), maxURL))
	case KindSelect:
		s, _ := v.(string)
		for _, o := range f.Options {
			if o.Value == s {
				return s
			}
		}
		d, _ := f.Default.(string)
		return d
	case KindColor:
		s, ok := v.(string)
		if !ok {
			s, _ = f.Default.(string)
		}
		return cleanColor(s)
	case KindNumber:
		return f.cleanNumber(v)
	case KindBool:
		switch x := v.(type) {
		case bool:
			return x
		case string:
			return x == "on" || x == "true" || x == "1"
		case nil:
			d, _ := f.Default.(bool)
			return d
		}
		return false
	case KindImageList:
		return cleanImageList(v)
	case KindRepeater:
		return f.cleanItems(v)
	}
	return nil
}

func str(v, def any) string {
	if s, ok := v.(string); ok {
		return s
	}
	d, _ := def.(string)
	return d
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

func (f Field) cleanNumber(v any) float64 {
	n, _ := f.Default.(float64)
	switch x := v.(type) {
	case float64:
		n = x
	case int:
		n = float64(x)
	case string:
		if p, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			n = p
		}
	}
	if n != n { // NaN
		n, _ = f.Default.(float64)
	}
	if f.Max > f.Min {
		n = min(max(n, f.Min), f.Max)
	}
	return n
}

func cleanImageList(v any) []string {
	var in []string
	switch x := v.(type) {
	case []string:
		in = x
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok {
				in = append(in, s)
			}
		}
	case string:
		in = strings.Split(x, "\n")
	}
	out := []string{}
	for _, s := range in {
		if u := SafeImageURL(truncate(s, maxURL)); u != "" {
			out = append(out, u)
		}
		if len(out) == maxImages {
			break
		}
	}
	return out
}

var (
	htmlPolicy = bluemonday.UGCPolicy()
	textPolicy = bluemonday.StrictPolicy()
)

// SanitizeHTML strips scripts, event handlers and unsafe URLs from rich text.
func SanitizeHTML(s string) string { return htmlPolicy.Sanitize(s) }

// PlainText strips all markup and returns real text (entities decoded), so
// callers must escape it again when writing HTML.
func PlainText(s string) string {
	return strings.TrimSpace(html.UnescapeString(textPolicy.Sanitize(s)))
}

var (
	hasParagraphRe = regexp.MustCompile(`(?i)<p[\s>/]`)
	blockStartRe   = regexp.MustCompile(`(?i)^<(p|h[1-6]|ul|ol|blockquote|pre|div|table|hr|figure)[\s>/]`)
)

// NormalizeRichText prepares author-written rich text for storage, then
// sanitises it. Text with no <p> tags is treated as written in plain paragraphs
// (blank line = new paragraph, single newline = line break), so authors can mix
// plain text with toolbar formatting such as <strong> or <h2>. Text that already
// has <p> tags is assumed to be structured and is left alone.
func NormalizeRichText(s string) string {
	if !hasParagraphRe.MatchString(s) {
		s = autoParagraphs(s)
	}
	return SanitizeHTML(s)
}

// autoParagraphs wraps each blank-line-separated chunk in <p> unless it already
// starts with a block-level element. Unescaped on purpose: the sanitiser that
// runs next encodes stray "&" and "<" and strips anything unsafe.
func autoParagraphs(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var out []string
	for _, chunk := range strings.Split(s, "\n\n") {
		chunk = strings.TrimSpace(chunk)
		switch {
		case chunk == "":
		case blockStartRe.MatchString(chunk):
			out = append(out, chunk)
		default:
			out = append(out, "<p>"+strings.ReplaceAll(chunk, "\n", "<br>")+"</p>")
		}
	}
	return strings.Join(out, "\n")
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func cleanColor(s string) string {
	s = strings.TrimSpace(s)
	if colorRe.MatchString(s) {
		return strings.ToLower(s)
	}
	return ""
}

// telRe is a phone number a "tel:" link may hold: an optional +, then a digit or bracket and digits with spaces, brackets, dots or dashes.
var telRe = regexp.MustCompile(`^tel:\+?[0-9(][0-9 ().\-]{2,28}$`)

// SafeURL allows site-relative paths, anchors, and http(s), mailto and tel links.
// Anything else (javascript:, data:, protocol-relative, backslash tricks) becomes "".
func SafeURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, `\`) {
		return ""
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(s, "//"):
		return ""
	case s[0] == '/' || s[0] == '#':
		return s
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "mailto:"):
		return s
	case telRe.MatchString(lower):
		return s
	}
	return ""
}

func SafeImageURL(s string) string {
	u := SafeURL(s)
	if l := strings.ToLower(u); strings.HasPrefix(l, "mailto:") || strings.HasPrefix(l, "tel:") {
		return ""
	}
	return u
}

// --- per-block style ---

type Style struct {
	Background, Text string // hex colours or ""
	Padding, Align   string
	Width            string
}

var (
	PaddingOptions = []Option{{"none", "None"}, {"sm", "Small"}, {"md", "Medium"}, {"lg", "Large"}}
	AlignOptions   = []Option{{"left", "Left"}, {"center", "Centre"}, {"right", "Right"}}
	WidthOptions   = []Option{{"narrow", "Narrow"}, {"normal", "Normal"}, {"wide", "Wide"}, {"full", "Full"}}
)

// StyleFields is the schema shared by every block (rendered in the editor's Style tab).
func StyleFields() []Field {
	return []Field{
		{Key: "background", Label: "Background colour", Kind: KindColor},
		{Key: "text", Label: "Text colour", Kind: KindColor},
		{Key: "padding", Label: "Vertical padding", Kind: KindSelect, Options: PaddingOptions, Default: "md"},
		{Key: "align", Label: "Alignment", Kind: KindSelect, Options: AlignOptions, Default: "left"},
		{Key: "width", Label: "Content width", Kind: KindSelect, Options: WidthOptions, Default: "normal"},
	}
}

// CleanStyleMap is the storable form of a block's style.
func CleanStyleMap(raw map[string]any) map[string]any {
	return cleanFields(StyleFields(), raw)
}

func CleanStyle(raw map[string]any) Style {
	c := CleanStyleMap(raw)
	return Style{
		Background: c["background"].(string),
		Text:       c["text"].(string),
		Padding:    c["padding"].(string),
		Align:      c["align"].(string),
		Width:      c["width"].(string),
	}
}

// Classes are the block's layout classes, plus a marker when its colours were set by hand:
// "pinned" for a custom background (the block keeps its light colours inside, in dark mode, so what
// the owner designed stays readable) and "text-only" for a custom text colour with no background
// (which would be unreadable on a dark page, so dark mode ignores it).
func (s Style) Classes() string {
	c := fmt.Sprintf("pad-%s align-%s width-%s", s.Padding, s.Align, s.Width)
	switch {
	case colorRe.MatchString(s.Background):
		c += " pinned"
	case colorRe.MatchString(s.Text):
		c += " text-only"
	}
	return c
}

// CSSVars returns validated custom properties for the block's style attribute.
func (s Style) CSSVars() template.CSS {
	var b strings.Builder
	if colorRe.MatchString(s.Background) {
		b.WriteString("--bg:" + s.Background + ";")
	}
	if colorRe.MatchString(s.Text) {
		b.WriteString("--fg:" + s.Text + ";")
	}
	return template.CSS(b.String())
}
