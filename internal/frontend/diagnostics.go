package frontend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/std"
)

// Diagnostic is one error the front end found in a file.
type Diagnostic struct {
	// Path is the file's absolute path, or an in-memory file's name
	// ("main.nomi").
	Path string
	// Line and Col are 1-based and start the span the error is about. Col
	// is 0 when only the line is known. EndLine and EndCol are one past the
	// span's last byte; EndLine is 0 when the error names only a point.
	// Columns count bytes.
	Line, Col       int
	EndLine, EndCol int
	// Message is the error's primary sentence.
	Message string
	// Hints are standalone suggestions ("did you mean 'count'?").
	Hints []string
	// Related are other locations the error refers to.
	Related []Related
	// SourceLine is the text of line Line, so the error can be shown with
	// its source without reading the file. Empty when the file was not
	// available.
	SourceLine string
	// Code is an optional machine-readable class, for a host that reports
	// the error under one (the language server): "invalid-literal" for a
	// backtick typed literal that fails its compile-time check. Empty for
	// the rest.
	Code string
}

// Related is a location a diagnostic refers to: the declaration a
// requirement comes from, the first definition of a redeclared name.
type Related struct {
	Path            string
	Line, Col       int
	EndLine, EndCol int
	Message         string
}

// String is the diagnostic in its short form, the `path:line:col: message`
// compilers use, so a terminal or an editor can jump to it. Each hint
// follows on a line of its own as `path:line:col: help: ...` and each
// related location as `path:line:col: note: ...`, so every line of the form
// parses the same way. An absolute path is shown relative to the working
// directory when it is under it.
func (d Diagnostic) String() string {
	var b strings.Builder
	at := location(d.Path, d.Line, d.Col)
	b.WriteString(at + ": " + d.Message)
	for _, h := range d.Hints {
		b.WriteString("\n" + at + ": help: " + OneLineHint(h))
	}
	for _, r := range d.Related {
		b.WriteString("\n" + location(r.Path, r.Line, r.Col) + ": note: " + r.Message)
	}
	return b.String()
}

// OneLineHint is hint on one line, for the short form: its lines trimmed and
// joined with "; ", except that a line ending in ':' or '{', or a line that
// is only '}', joins with a space. The import-block hint reads
// `... an import block: import { std/io; std/regex.Regex }`.
func OneLineHint(hint string) string {
	var b strings.Builder
	prev := ""
	for _, line := range strings.Split(hint, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if prev != "" {
			if strings.HasSuffix(prev, ":") || strings.HasSuffix(prev, "{") || line == "}" {
				b.WriteString(" ")
			} else {
				b.WriteString("; ")
			}
		}
		b.WriteString(line)
		prev = line
	}
	return b.String()
}

// location is `path:line:col`, with the parts that are known.
func location(path string, line, col int) string {
	if filepath.IsAbs(path) {
		path = rt.DisplayPath(path)
	}
	switch {
	case line <= 0:
		return path
	case col <= 0:
		return fmt.Sprintf("%s:%d", path, line)
	}
	return fmt.Sprintf("%s:%d:%d", path, line, col)
}

// Diagnostics is the error the front end answers for a program it rejects:
// every diagnostic, in its short form, one after another.
type Diagnostics []Diagnostic

func (ds Diagnostics) Error() string {
	lines := make([]string, len(ds))
	for i, d := range ds {
		lines[i] = d.String()
	}
	return strings.Join(lines, "\n")
}

// OwnLines marks the error's text as lines that each stand on their own, so
// a report prints them below the failing file's name (rt.TestReporter).
func (ds Diagnostics) OwnLines() bool { return true }

// typeDiagnostics are errs, found in the file at path, whose text src is
// when known ("" otherwise).
func typeDiagnostics(path, src string, errs []analysis.TypeError) Diagnostics {
	if src != "" {
		errs = analysis.CompleteSpans(src, errs)
	}
	lines := sourceLines(src)
	ds := make(Diagnostics, len(errs))
	for i, e := range errs {
		d := Diagnostic{
			Path: path, Line: e.Line, Col: e.Col, EndLine: e.EndLine, EndCol: e.EndCol,
			Message: e.Message, Hints: e.Hints,
		}
		for _, r := range e.Related {
			rp := r.File
			if rp == "" {
				rp = path
			}
			d.Related = append(d.Related, Related{Path: rp, Line: r.Line, Col: r.Col, EndLine: r.EndLine, EndCol: r.EndCol, Message: r.Message})
		}
		if e.Line >= 1 && e.Line <= len(lines) {
			d.SourceLine = lines[e.Line-1]
		}
		ds[i] = d
	}
	return ds
}

// NewDiagnostic is one error at line and col of the file at path, with its
// span and source line completed from the file's text as the front end's own
// diagnostics are: src when given, and otherwise the file as fileSource reads
// it. For an error found past the front end, such as a body the compiler
// accepted and cannot lower (vmhost.Program.Unsupported).
func NewDiagnostic(path, src string, line, col int, message string) Diagnostic {
	if src == "" {
		src = fileSource(path)
	}
	return typeDiagnostics(path, src, []analysis.TypeError{{Line: line, Col: col, Message: message}})[0]
}

// parseDiagnostics are a parse's errors in the file at path, whose text src
// is when known.
func parseDiagnostics(path, src string, errs []parser.ParseError) Diagnostics {
	tes := make([]analysis.TypeError, len(errs))
	for i, e := range errs {
		tes[i] = analysis.TypeError{Line: e.Line, Col: e.Col, Message: e.Message, Hints: e.Hints}
	}
	return typeDiagnostics(path, src, tes)
}

// sourceLines splits src into lines, without their line endings.
func sourceLines(src string) []string {
	if src == "" {
		return nil
	}
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// fileSource is the text of the file a diagnostic names: a stdlib module
// ("std/<name>.nomi") from the embedded library, any other path from disk.
// "" when it cannot be read.
func fileSource(path string) string {
	if rest, ok := strings.CutPrefix(path, "std/"); ok && !filepath.IsAbs(path) {
		if data, ok := std.ReadFile(strings.TrimSuffix(rest, ".nomi")); ok {
			return string(data)
		}
		return ""
	}
	if !filepath.IsAbs(path) {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// located gives err the file at path, whose text is src, when it is a
// positioned parse error; any other error is answered as it is.
func located(path, src string, err error) error {
	var pe parser.ParseError
	if errors.As(err, &pe) {
		return parseDiagnostics(path, src, []parser.ParseError{pe})
	}
	return err
}
