package vmhost

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// SourceSpan is the extent of the call, lambda or name written at
// file:line:col in the text the program was loaded from: where it starts,
// and its end, one past its last token. A call is found by its `(` or its
// first token, which is where the IR positions a call and a lambda. ok is
// false when the file does not parse or nothing of those starts there. A
// debugger's step-in targets are ranged by it.
func (p *Program) SourceSpan(file string, line, col int) (startLine, startCol, endLine, endCol int, ok bool) {
	src, err := p.sourceOf(file)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		return 0, 0, 0, 0, false
	}
	var found ast.Span
	for _, n := range nodes {
		ast.Inspect(n, func(m ast.Node) bool {
			if !found.IsZero() {
				return false
			}
			var s ast.Span
			at := false
			switch t := m.(type) {
			case *ast.Call:
				s = t.GetSpan()
				at = (t.Line == line && t.Col == col) || (s.StartLine == line && s.StartCol == col)
			case *ast.Lambda:
				s = t.GetSpan()
				at = (t.Line == line && t.Col == col) || (s.StartLine == line && s.StartCol == col)
			case *ast.Ident:
				s = t.GetSpan()
				at = t.Line == line && t.Col == col
				if s.IsZero() && at {
					s = ast.Span{StartLine: line, StartCol: col, EndLine: line, EndCol: col + len(t.Name)}
				}
			}
			if at && !s.IsZero() {
				found = s
				return false
			}
			return true
		})
		if !found.IsZero() {
			break
		}
	}
	if found.IsZero() {
		return 0, 0, 0, 0, false
	}
	return found.StartLine, found.StartCol, found.EndLine, found.EndCol, true
}
