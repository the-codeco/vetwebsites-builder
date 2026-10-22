package content

import (
	"context"
	"strconv"
	"strings"

	"webbuilder/internal/blocks"
)

// Footer limits. Four columns is what fits across a page; the text is rich text, cleaned like any block's.
const (
	MaxFooterColumns = 4
	MaxFooterHeading = 60
	MaxFooterText    = 5000
	MaxFooterBottom  = 300
)

// DefaultFooterBottom is the line under the footer when the owner hasn't written their own.
// {year} and {title} are replaced with the current year and the site's title.
const DefaultFooterBottom = "© {year} {title}"

// FooterColumn is one block of footer content: a heading and some text (basic HTML, already cleaned).
type FooterColumn struct{ Heading, Text string }

// Footer is the owner's footer content: up to four columns above a bottom line.
type Footer struct {
	Columns []FooterColumn
	Bottom  string // "" means DefaultFooterBottom
}

// BottomLine is the text to show, with {year} and {title} filled in.
func (f Footer) BottomLine(title string, year int) string {
	b := f.Bottom
	if b == "" {
		b = DefaultFooterBottom
	}
	return strings.NewReplacer("{year}", strconv.Itoa(year), "{title}", title).Replace(b)
}

// clean returns the footer with every part limited and made safe: columns with neither a heading nor text
// are dropped, extra columns are ignored, and text is sanitised like rich text anywhere else.
func (f Footer) clean() Footer {
	out := Footer{Bottom: strings.TrimSpace(truncateRunes(strings.Join(strings.Fields(f.Bottom), " "), MaxFooterBottom))}
	for _, c := range f.Columns {
		c.Heading = strings.TrimSpace(truncateRunes(strings.Join(strings.Fields(c.Heading), " "), MaxFooterHeading))
		c.Text = blocks.NormalizeRichText(truncateRunes(c.Text, MaxFooterText))
		if c.Heading == "" && blocks.PlainText(c.Text) == "" {
			continue
		}
		if out.Columns = append(out.Columns, c); len(out.Columns) == MaxFooterColumns {
			break
		}
	}
	return out
}

func (f Footer) asJSON() map[string]any {
	f = f.clean()
	m := map[string]any{}
	if f.Bottom != "" {
		m["bottom"] = f.Bottom
	}
	if len(f.Columns) > 0 {
		cols := make([]any, 0, len(f.Columns))
		for _, c := range f.Columns {
			cols = append(cols, map[string]any{"heading": c.Heading, "text": c.Text})
		}
		m["columns"] = cols
	}
	return m
}

func footerFrom(m map[string]any) Footer {
	var f Footer
	f.Bottom, _ = m["bottom"].(string)
	cols, _ := m["columns"].([]any)
	for _, e := range cols {
		cm, _ := e.(map[string]any)
		var c FooterColumn
		c.Heading, _ = cm["heading"].(string)
		c.Text, _ = cm["text"].(string)
		f.Columns = append(f.Columns, c)
	}
	return f.clean() // whatever is stored, what reaches a page is clean
}

func (s *Store) SetFooter(ctx context.Context, f Footer) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET footer = $1 WHERE id = 1`, f.asJSON())
	return err
}

// CleanFooter makes any stored or imported footer JSON safe: only the known fields survive, within their limits.
func CleanFooter(m map[string]any) map[string]any { return footerFrom(m).asJSON() }
