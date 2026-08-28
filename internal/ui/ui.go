// Package ui owns every decision about how output looks: whether colour is
// allowed, whether the terminal can render UTF-8, and how a table is aligned.
//
// It is decided once, here, because the alternative is each view re-deciding it
// and getting it subtly different. The rules come from design §6.3: colour only
// on a TTY, honour NO_COLOR, drop ANSI and box-drawing when piped, fall back to
// ASCII on a non-UTF-8 terminal.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

// Caps is what the destination can render.
type Caps struct {
	Color bool
	UTF8  bool
	TTY   bool
}

// Detect works out what the writer can handle. forceNoColor comes from
// --no-color or config; everything else is observed.
func Detect(w io.Writer, forceNoColor bool) Caps {
	tty := isTerminal(w)
	c := Caps{TTY: tty, UTF8: utf8Locale()}

	switch {
	case forceNoColor:
	case os.Getenv("NO_COLOR") != "": // https://no-color.org — presence, not value
	case os.Getenv("TERM") == "dumb":
	case !tty: // piped or redirected: never emit escape codes
	default:
		c.Color = true
	}
	return c
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// utf8Locale reports whether the environment claims a UTF-8 locale. Windows
// terminals don't set these but have defaulted to UTF-8 since Windows Terminal,
// so an absent locale is treated as capable.
func utf8Locale() bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			u := strings.ToUpper(v)
			return strings.Contains(u, "UTF-8") || strings.Contains(u, "UTF8")
		}
	}
	return true
}

// Symbols returns the status glyphs this terminal can actually draw. Mojibake in
// the moment you're asking a stranger to trust the binary is not a good look.
func (c Caps) OK() string {
	if c.UTF8 {
		return "✓"
	}
	return "ok"
}

func (c Caps) Warn() string {
	if c.UTF8 {
		return "⚠"
	}
	return "!"
}

func (c Caps) Fail() string {
	if c.UTF8 {
		return "✗"
	}
	return "x"
}

// Dash is the "we could not see this" marker from design §6.2. It is never a
// zero, and it is never an omitted row.
func (c Caps) Dash() string {
	if c.UTF8 {
		return "—"
	}
	return "-"
}

// Sep is the interpunct that separates facts on one line. It is non-ASCII, so
// it needs the same fallback as the status glyphs — a footer full of mojibake
// undoes the work the glyph fallback did.
func (c Caps) Sep() string {
	if c.UTF8 {
		return "·"
	}
	return "|"
}

// Dim renders muted text — used for noise-level information that should not
// compete with the numbers.
func (c Caps) Dim(s string) string {
	if !c.Color {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

// Bold renders emphasis, used for column headers only.
func (c Caps) Bold(s string) string {
	if !c.Color {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
}

// Table is a column-aligned block.
//
// Cells are never styled. text/tabwriter measures column width in runes and has
// no idea an ANSI escape is zero-width, so a coloured cell silently misaligns
// the whole column — on a TTY, which is the only place colour appears, which is
// the only place anyone would notice. Emphasis belongs on whole lines outside a
// table, where nothing is being measured.
type Table struct {
	caps    Caps
	tw      *tabwriter.Writer
	headers []string
}

// NewTable starts a table with the given column headers.
func NewTable(w io.Writer, caps Caps, headers ...string) *Table {
	t := &Table{
		caps:    caps,
		tw:      tabwriter.NewWriter(w, 0, 0, 2, ' ', 0),
		headers: headers,
	}
	if len(headers) > 0 {
		fmt.Fprintf(t.tw, "  %s\n", strings.Join(headers, "\t"))
	}
	return t
}

// Row adds one row. Cell count is not enforced: a short row simply ends early,
// which is what you want for a totals line.
func (t *Table) Row(cells ...string) {
	fmt.Fprintf(t.tw, "  %s\n", strings.Join(cells, "\t"))
}

// Flush writes the aligned table out.
func (t *Table) Flush() error { return t.tw.Flush() }
