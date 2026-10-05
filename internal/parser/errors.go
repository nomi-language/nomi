package parser

import "fmt"

// ParseError represents a single parse error with position information.
type ParseError struct {
	Line    int
	Col     int
	Message string
	// Hints say how to fix it, one `help:` each; a hint may run over
	// several lines.
	Hints []string
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
