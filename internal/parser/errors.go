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

// softError records e and lets the parse go on as if the source were
// written as e's fix says. A strict parse reports it as it would a returned
// error. The same error recorded twice, as when a speculative parse that
// rewound and the real parse both read it, is kept once.
func (p *Parser) softError(e ParseError) {
	key := errorKey{e.Line, e.Col, e.Message}
	if p.soft == nil {
		p.soft = map[errorKey]bool{}
	}
	p.soft[key] = true
	for _, old := range p.errors {
		if old.Line == e.Line && old.Col == e.Col && old.Message == e.Message {
			return
		}
	}
	p.errors = append(p.errors, e)
	p.softErrors = true
}

// errorKey is an error's place and message.
type errorKey struct {
	line, col int
	message   string
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

// parseMark is the parser's state a speculative parse rewinds to: the
// cursor, and the soft errors and recoveries recorded since. A soft error
// read by a parse that is then discarded (the `True {x}` of `if True {x}`,
// tried as a pattern) is no error of the source, and the parse that reads
// those tokens again records any that is.
type parseMark struct {
	pos, errors, recoveries int
	softErrors              bool
}

func (p *Parser) mark() parseMark {
	return parseMark{pos: p.pos, errors: len(p.errors), recoveries: p.recoveries, softErrors: p.softErrors}
}

func (p *Parser) rewind(m parseMark) {
	p.pos, p.errors, p.recoveries, p.softErrors = m.pos, p.errors[:m.errors], m.recoveries, m.softErrors
}
