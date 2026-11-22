package analysis

import "github.com/nomi-language/nomi/internal/ast"

// writtenParts is the parts of te, a variant's payload or a distinct type's
// inner type, when the declaration writes it as a tuple of them, `(Tree,
// Int, Tree)`, and nil when it writes one type, a tuple's name among them
// (`T`, a typealias of a tuple). The parser groups `(T)` into T, so a tuple
// written has two parts or more.
//
// It is the one reading of the fact from syntax: the type builder records
// it (VariantDef.WrittenAsParts, DistinctType.WrittenAsParts), and derive
// synthesis, which writes its bodies before any type is built, takes the
// parts from it.
func writtenParts(te ast.TypeExpr) []ast.TypeExpr {
	ft, ok := te.(*ast.FuncType)
	if !ok || ft.Return != nil || len(ft.Params) < 2 {
		return nil
	}
	return ft.Params
}

// DeclaredType is the type the checker built for decl, a struct, enum or
// distinct type declaration of fa's file or of a body in it: its
// definition's type, nil when the checker built none.
func (fa *FileAnalysis) DeclaredType(decl ast.Node) Type {
	if fa == nil || decl == nil {
		return nil
	}
	if _, isExtern := decl.(*ast.ExternType); isExtern {
		return nil
	}
	line, col := declPos(decl)
	if line == 0 {
		return nil
	}
	sym := fa.Definitions[Pos{Line: line, Col: col}]
	if sym == nil || sym.Node != decl {
		return nil
	}
	return sym.Type
}
