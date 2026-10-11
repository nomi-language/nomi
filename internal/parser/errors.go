package parser

import (
	"errors"
	"fmt"
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

// Fix is an edit that repairs a ParseError, for an editor to offer. Each
// field is one kind of edit; at most one is set.
type Fix struct {
	// Wrap puts braces around a body that runs on below its first line.
	Wrap *BodyWrap
	// Markers are comment markers (`///` or `//!`) that become `//`, each
	// at the position of its first slash.
	Markers []Pos
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
