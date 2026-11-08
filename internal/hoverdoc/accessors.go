package hoverdoc

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Hover on a field accessor, `.name`, `.address.city` or a tuple's
// `.second`: the function it is, with the types the checker solved where it
// stands (`.second: ((Int, String)) -> String`), and the doc of the field
// under the cursor, or of the last one on a dot. The whole accessor
// answers, its dots included.

// fieldAccessor claims the accessor's text, from its leading dot through
// its last name.
func (s *shapeSearch) fieldAccessor(n *ast.FieldAccessor) {
	if len(n.Path) == 0 {
		return
	}
	last := n.Path[len(n.Path)-1]
	if last.Line != n.Line {
		return
	}
	length := last.Col + len(last.Name) - n.Col
	s.claim(n.Line, n.Col, length, length, func() string { return s.renderFieldAccessor(n) })
}

// renderFieldAccessor is the accessor's signature and the doc of the field
// the hover is on, or "" for an accessor the checker did not accept.
func (s *shapeSearch) renderFieldAccessor(n *ast.FieldAccessor) string {
	param, ok := s.fa.FieldAccessors[n]
	if !ok {
		return ""
	}
	last := n.Path[len(n.Path)-1]
	result := s.fa.References[analysis.Pos{Line: last.Line, Col: last.Col}]
	ft, _ := analysis.ResolveTypeVar(s.fa.ExprTypes[n]).(*analysis.FuncType)
	if ft == nil || len(ft.Params) != 1 {
		if result == nil || result.Type == nil {
			return ""
		}
		ft = &analysis.FuncType{Params: []analysis.Type{param}, Return: result.Type}
	}
	on := last
	for _, seg := range n.Path {
		if s.pos.Col >= seg.Col && s.pos.Col < seg.Col+len(seg.Name) {
			on = seg
		}
	}
	field := s.fa.References[analysis.Pos{Line: on.Line, Col: on.Col}]
	out := fmt.Sprintf("```nomi\n%s: %s\n```", n.Spelling(), displayValueType(ft))
	if field != nil && field.Kind == analysis.SymbolField && field.Doc != "" {
		out += "\n\n" + field.Doc
	}
	return out
}
