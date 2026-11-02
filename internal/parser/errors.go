package parser

import (
	"errors"
	"fmt"
	"sort"
)

// ParseError represents a single parse error with position information.
type ParseError struct {
	Line    int
	Col     int
	Message string
	// Hints say how to fix it, one `help:` each; a hint may run over
	// several lines.
	Hints []string
	// Fix, when set, is an edit that repairs the source.
	Fix *Fix
}

func (e ParseError) Error() string {
	return fmt.Sprintf("line %d, col %d: %s", e.Line, e.Col, e.Message)
}

// errorAt is a ParseError at line and col, with its message formatted from
// format and args.
func errorAt(line, col int, format string, args ...any) error {
	return ParseError{Line: line, Col: col, Message: fmt.Sprintf(format, args...)}
}

// errorOnLine is a ParseError about a construct that began on line, placed
// at the first token the parser has read on that line.
func (p *Parser) errorOnLine(line int, format string, args ...any) error {
	col := 1
	for i := min(p.pos, len(p.tokens)-1); i >= 0; i-- {
		tok := p.tokens[i]
		if tok.Line < line {
			break
		}
		if tok.Line == line {
			col = tok.Col
		}
	}
	return errorAt(line, col, format, args...)
}

// Fix is an edit that repairs a ParseError or a type error, for an editor
// to offer and for `nomi check --format json` to report. Each field is one
// kind of edit; at most one is set.
type Fix struct {
	// Wrap puts braces around a body that runs on below its first line.
	Wrap *BodyWrap
	// Markers are comment markers (`///` or `//!`) that become `//`, each
	// at the position of its first slash.
	Markers []Pos
	// Edits replace text: applied together, they turn the source into
	// what the diagnostic says Nomi writes. Title says what they do
	// ("Write `case`").
	Edits []Edit
	Title string
}

// Edit replaces the text from Start up to End (1-based lines, byte
// columns; End one past the last byte replaced) with Text. Start == End
// inserts.
type Edit struct {
	Start, End Pos
	Text       string
}

// Replace is a Fix titled title of the one edit putting text in place of
// the bytes from (line, col) up to (endLine, endCol).
func Replace(title string, line, col, endLine, endCol int, text string) *Fix {
	return &Fix{Title: title, Edits: []Edit{{Start: Pos{line, col}, End: Pos{endLine, endCol}, Text: text}}}
}

// Pos is a 1-based line and byte column.
type Pos struct {
	Line, Col int
}

// BodyWrap is a body written without braces over several lines. The body
// runs from just after After (the end of a case arm's `->` or a lambda's
// closing `|`) to End (the end of its last token). The braces go around
// it: `{` after After, `}` on a line of its own at the indentation of
// After's line.
type BodyWrap struct {
	After, End Pos
}

// softError records e and lets the parse go on as if the source were
// written as e's fix says. A strict parse reports it as it would a returned
// error. The same error recorded twice, as when a speculative parse that
// rewound and the real parse both read it, is kept once.
func (p *Parser) softError(e ParseError) {
	for _, old := range p.errors {
		if old.Line == e.Line && old.Col == e.Col && old.Message == e.Message {
			return
		}
	}
	p.errors = append(p.errors, e)
	p.softErrors = true
}

// firstError is the earliest of the parse's soft errors and err, the error
// a parse that stops at the first one reports.
func (p *Parser) firstError(err error) error {
	if len(p.errors) == 0 {
		return err
	}
	first := p.errors[0]
	for _, e := range p.errors[1:] {
		if e.Line < first.Line || e.Line == first.Line && e.Col < first.Col {
			first = e
		}
	}
	var pe ParseError
	if err != nil && errors.As(err, &pe) && (pe.Line < first.Line || pe.Line == first.Line && pe.Col < first.Col) {
		return err
	}
	return first
}

// ApplyEdits is src with edits applied. The edits must not overlap; they
// apply as one change, each at its position in src.
func ApplyEdits(src string, edits []Edit) string {
	lineStarts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	offset := func(p Pos) int {
		if p.Line < 1 || p.Line > len(lineStarts) {
			return len(src)
		}
		return min(lineStarts[p.Line-1]+p.Col-1, len(src))
	}
	sorted := append([]Edit(nil), edits...)
	sort.SliceStable(sorted, func(i, j int) bool { return offset(sorted[i].Start) > offset(sorted[j].Start) })
	for _, e := range sorted {
		start, end := offset(e.Start), offset(e.End)
		src = src[:start] + e.Text + src[end:]
	}
	return src
}
