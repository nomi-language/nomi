package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// attachedVariant lowers a literal-attach form whose prefix names an enum
// variant (`Json.Obj{"k" => v}`, `.Arr[1, 2]`, `JArr[...]`) as the variant's
// positional call over the anonymous literal. A prefix that names a distinct type, or a
// variant the builder does not retain, declines.
func (bl *irScalarBuilder) attachedVariant(at ast.Node, prefix ast.TypeExpr, anon ast.Node) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	line, col := nodePos(at)
	call := &ast.Call{Args: []ast.Node{anon}, Line: line, Col: col}
	var fa *ast.FieldAccess
	switch tn := prefix.(type) {
	case *ast.QualifiedType:
		member, ok := tn.Member.(*ast.SimpleType)
		if !ok {
			return no()
		}
		fa = &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: tn.Module, Line: line, Col: col},
			Field:  &ast.Ident{Name: member.Name, Line: line, Col: col},
			Line:   line, Col: col,
		}
	case *ast.DotVariantType:
		if tn.ResolvedEnum == "" {
			return no()
		}
		fa = &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: bl.g.dotEnumName(tn.ResolvedEnum), Line: line, Col: col},
			Field:  &ast.Ident{Name: tn.Name, Line: line, Col: col},
			Line:   line, Col: col,
		}
	case *ast.SimpleType:
		// A bare std variant, bound by a selective or aliased import. The
		// analyzer records its reference at the prefix's own position.
		ti := &ast.TypeIdent{Name: tn.Name, Line: tn.Line, Col: tn.Col}
		call.Func = ti
		if d, found := bl.g.namedType(tn.Name); found && irCompositeDistinct(d) {
			// `Kvs{"a" => 1}`: the distinct's constructor over the literal.
			return bl.compositeDistinctMake(call, d)
		}
		return bl.stdVariantCall(call, ti)
	default:
		return no()
	}
	call.Func = fa
	return bl.variantCall(call, fa)
}
