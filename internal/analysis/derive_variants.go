package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// variantsDeriveError says why `derive Variants for <decl>` cannot be
// synthesized, or "" when it can. `Variants.variants()` builds every value of
// the type, so the type must be an enum without type parameters whose
// variants all carry no data.
func variantsDeriveError(decl ast.Node) string {
	name, _ := typeDeclName(decl)
	e, ok := decl.(*ast.EnumDef)
	if !ok {
		return fmt.Sprintf("`derive Variants for %s`: `Variants` lists an enum's variants, and `%s` is %s, not an enum; write `impl Variants for %s { ... }` to list its values by hand", name, name, typeDeclKindPhrase(decl), name)
	}
	if len(e.TypeParams) > 0 {
		return fmt.Sprintf("`derive Variants for %s`: `%s` has type parameters, and `Variants` lists the variants of an enum without them", name, name)
	}
	for _, v := range e.Variants {
		if v.Kind != "bare" {
			return fmt.Sprintf("`derive Variants for %s`: variant `%s.%s` carries data, and `Variants` lists only variants that carry none; remove its data, or write `impl Variants for %s { ... }` by hand", name, name, v.Name, name)
		}
	}
	return ""
}

// typeDeclKindPhrase names a type declaration's kind with its article, for a
// diagnostic that says what a type is.
func typeDeclKindPhrase(decl ast.Node) string {
	switch d := decl.(type) {
	case *ast.StructDef:
		return "a struct"
	case *ast.TypeDef:
		if d.InnerTypeExpr == nil {
			return "a type with no data"
		}
		return "a distinct type"
	case *ast.ExternType:
		return "a host type"
	}
	return "not a type"
}

// synthesizeDeriveVariants emits `fn variants() -> List<E>` whose body lists
// every variant of the enum in declaration order, the order derived
// Comparable ranks them in: `[E.A, E.B, ...]`. The caller has checked the
// shape with variantsDeriveError; anything else synthesizes nothing.
func synthesizeDeriveVariants(typeNode ast.Node, site synthSite) []ast.Node {
	e, ok := typeNode.(*ast.EnumDef)
	if !ok || variantsDeriveError(typeNode) != "" {
		return nil
	}
	line := site.base(synthKindVariants)
	col := 1
	items := make([]ast.Node, len(e.Variants))
	for i, v := range e.Variants {
		items[i] = fieldAccess(&ast.TypeIdent{Name: e.Name, Line: line, Col: col}, v.Name, line, col)
	}
	list := &ast.ListLit{Items: items}
	list.Line, list.Col = line, col
	fn := &ast.FuncDef{
		Name: "variants",
		ReturnTypeExpr: &ast.GenericType{
			Name:   "List",
			Params: []ast.TypeExpr{receiverTypeExpr(e.Name, nil, line, col)},
			Line:   line,
			Col:    col,
		},
		Body: &ast.Block{Stmts: []ast.Node{exprStmt(list, line, col)}, Line: line, Col: col},
		Line: line,
		Col:  col,
	}
	uniquifyPositions(fn, line)
	return []ast.Node{fn}
}
