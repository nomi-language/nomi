package frontend

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/termcolor"
	"github.com/nomi-language/nomi/rt"
)

// DiagnosticsFormatEnv names the environment variable that selects how the
// command line prints diagnostics: "short" prints the one-line
// `path:line:col: message` form (Diagnostic.String); anything else prints
// each diagnostic with its source line (Render). An environment variable
// rather than a flag alone so an FFI program's wrapper process, which
// prints its own diagnostics, prints them the same way.
const DiagnosticsFormatEnv = "NOMI_DIAGNOSTICS"

// ShortDiagnostics reports whether the short form is selected.
func ShortDiagnostics() bool {
	return strings.EqualFold(os.Getenv(DiagnosticsFormatEnv), "short")
}

// Render writes the diagnostics to w as the command line shows them: each
// with its source line and the span underlined, then its hints and related
// locations, in colour when w is a terminal (rt.ColorEnabledFor). With
// NOMI_DIAGNOSTICS=short it writes the short form, one line each.
//
//	error: type mismatch: expected Int, got String
//	  --> main.nomi:12:9
//	   |
//	12 |     total = name
//	   |             ^^^^
//	   = help: did you mean 'count'?
//	   = note: 'count' is declared here: main.nomi:3:5
func (ds Diagnostics) Render(w io.Writer) {
	if ShortDiagnostics() {
		fmt.Fprintln(w, ds.Error())
		return
	}
	for i, d := range ds {
		if i > 0 {
			fmt.Fprintln(w)
		}
		d.render(w)
	}
}

func (d Diagnostic) render(w io.Writer) {
	fmt.Fprintf(w, "%s %s\n", rt.RedFor(w, "error:"), rt.BoldFor(w, d.Message))
	gutter := ""
	if d.Line > 0 && d.SourceLine != "" {
		gutter = strings.Repeat(" ", len(strconv.Itoa(d.Line)))
	}
	pad := gutter
	if pad == "" {
		pad = " "
	}
	bar := rt.CyanFor(w, "|")
	fmt.Fprintf(w, "%s%s %s\n", pad, rt.CyanFor(w, "-->"), location(d.Path, d.Line, d.Col))
	if gutter != "" {
		fmt.Fprintf(w, "%s %s\n", gutter, bar)
		fmt.Fprintf(w, "%s %s %s\n", rt.CyanFor(w, strconv.Itoa(d.Line)), bar, termcolor.NomiLineFor(w, d.SourceLine))
		fmt.Fprintf(w, "%s %s %s\n", gutter, bar, d.underline(w))
	}
	for _, h := range d.Hints {
		fmt.Fprintf(w, "%s %s %s %s\n", pad, rt.CyanFor(w, "="), rt.BoldFor(w, "help:"), h)
	}
	for _, r := range d.Related {
		fmt.Fprintf(w, "%s %s %s %s: %s\n", pad, rt.CyanFor(w, "="), rt.BoldFor(w, "note:"), r.Message, location(r.Path, r.Line, r.Col))
	}
}

// underline is the caret line under SourceLine: spaces (and the line's own
// tabs) up to the span's start, then a caret for each character of the span
// on that line. A span that runs onto later lines is underlined to the end
// of its first line and says where it ends.
func (d Diagnostic) underline(w io.Writer) string {
	line := d.SourceLine
	start := d.Col - 1
	if start < 0 {
		start = 0
	}
	if start > len(line) {
		start = len(line)
	}
	var lead strings.Builder
	for _, r := range line[:start] {
		if r == '\t' {
			lead.WriteByte('\t')
		} else {
			lead.WriteByte(' ')
		}
	}
	end := start + 1
	multiline := d.EndLine > d.Line
	switch {
	case multiline:
		end = len(line)
	case d.EndLine == d.Line && d.EndCol-1 > start:
		end = d.EndCol - 1
	}
	if end > len(line) {
		end = len(line)
	}
	width := utf8.RuneCountInString(line[start:end])
	if width < 1 {
		width = 1
	}
	marks := rt.RedFor(w, strings.Repeat("^", width))
	if multiline {
		marks += " " + rt.DimFor(w, fmt.Sprintf("(through line %d)", d.EndLine))
	}
	return lead.String() + marks
}
