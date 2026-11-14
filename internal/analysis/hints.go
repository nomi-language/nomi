package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Codes of the hints FileHints reports.
const (
	HintRedundantAscription = "redundant-ascription"
	HintRestatedValue       = "restated-current-value"
)

// FileHints are the hints the check of the file whose analysis is fa, from
// nodes, recorded: each `value as Type` whose value has the type without it
// (FileAnalysis.RedundantAscriptions), spanning `as Type`, then each
// operand in a field's new value that reads the field's current value by
// its path (FileAnalysis.RestatedPaths), spanning the path. The program
// means the same either way. Columns count bytes. The language server
// publishes them as hints and `nomi check --hints` reports them; both
// read them here.
func FileHints(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	if fa == nil || (len(fa.RedundantAscriptions) == 0 && len(fa.RestatedPaths) == 0) {
		return nil
	}
	var out []TypeError
	if len(fa.RedundantAscriptions) > 0 {
		for _, node := range nodes {
			ast.Inspect(node, func(n ast.Node) bool {
				a, ok := n.(*ast.Ascription)
				if !ok || !fa.RedundantAscriptions[a] || a.Line <= 0 {
					return true
				}
				text := a.Type.TypeString()
				out = append(out, TypeError{
					Line: a.Line, Col: a.Col, EndLine: a.Line, EndCol: a.Col + len("as ") + len(text),
					Code:    HintRedundantAscription,
					Message: fmt.Sprintf("`as %s` is redundant: the value already has type `%s`", text, text),
				})
				return true
			})
		}
	}
	if len(fa.RestatedPaths) > 0 {
		for _, node := range nodes {
			ast.Inspect(node, func(n ast.Node) bool {
				p, ok := n.(*ast.FieldAccess)
				if !ok {
					return true
				}
				name, restated := fa.RestatedPaths[p]
				if !restated || p.Field == nil {
					return true
				}
				root := ast.Node(p)
				for {
					inner, ok := root.(*ast.FieldAccess)
					if !ok {
						break
					}
					root = inner.Object
				}
				id, ok := root.(*ast.Ident)
				if !ok || id.Line <= 0 || p.Field.Line != id.Line {
					return false
				}
				out = append(out, TypeError{
					Line: id.Line, Col: id.Col, EndLine: id.Line, EndCol: p.Field.Col + len(p.Field.Name),
					Code:    HintRestatedValue,
					Message: fmt.Sprintf("this reads the value `%s` already holds here, which `..` writes", name),
				})
				return false
			})
		}
	}
	return out
}
