package analysis

import "github.com/nomi-language/nomi/internal/ast"

// wholeResultHint explains a name holding a `Result<A, E>` given where a
// `Result<B, E>` is expected, or a `Maybe<A>` where a `Maybe<B>` is: the
// name was usually bound by an arm that matched the failure
// (`err -> err` after `Ok(v) -> ...`, or `Err(_) as err -> err`), and the
// author meant to pass the failure on. The name still has the scrutinee's
// whole type, so its `Ok` type is the one it was matched from. The hint
// fires only when the two types differ in the `Ok` or `Some` type alone and
// the expression is a plain name; any other mismatch, or any other
// expression, gets "".
func (c *checker) wholeResultHint(expected, got Type, expr ast.Node) string {
	id, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	want, ok := resolveTypeVar(expected).(*EnumType)
	if !ok {
		return ""
	}
	have, ok := resolveTypeVar(got).(*EnumType)
	if !ok || want.Name != have.Name || len(want.TypeArgs) != len(have.TypeArgs) {
		return ""
	}
	switch {
	case want.Name == "Result" && len(want.TypeArgs) == 2:
		if !TypesEqual(resolveTypeVar(want.TypeArgs[1]), resolveTypeVar(have.TypeArgs[1])) ||
			TypesEqual(resolveTypeVar(want.TypeArgs[0]), resolveTypeVar(have.TypeArgs[0])) {
			return ""
		}
		return c.typef("`%s` is the whole `%s`, so its `Ok` type is still `%s`; "+
			"to pass the error on, write `Err(e) -> Err(e)`, or propagate it with `?`",
			id.Name, have, have.TypeArgs[0])
	case want.Name == "Maybe" && len(want.TypeArgs) == 1:
		if TypesEqual(resolveTypeVar(want.TypeArgs[0]), resolveTypeVar(have.TypeArgs[0])) {
			return ""
		}
		return c.typef("`%s` is the whole `%s`, so its `Some` type is still `%s`; "+
			"to pass the absence on, write `None -> None`, or propagate it with `?`",
			id.Name, have, have.TypeArgs[0])
	}
	return ""
}

// unwrappedOneShortHint explains a name holding a `Maybe<T>` given where a
// `T` is expected, when a pattern bound the name: the pattern unwrapped one
// level short, as `Ok(input) = io.read_line() else { break }` does over a
// `Result<Maybe<String>, Error>`, and the error otherwise stands at each
// use, far from the pattern. The help names the pattern's line and the
// `Some(...)` to write there. Any other expression, a name bound by
// `name = value`, or a `Maybe` whose payload is not what is expected gets
// "".
func (c *checker) unwrappedOneShortHint(expected, got Type, expr ast.Node) string {
	id, ok := expr.(*ast.Ident)
	if !ok || c.fa == nil {
		return ""
	}
	have, ok := resolveTypeVar(got).(*EnumType)
	if !ok || have.Name != "Maybe" || len(have.TypeArgs) != 1 {
		return ""
	}
	inner := resolveTypeVar(have.TypeArgs[0])
	if want := resolveTypeVar(expected); want == nil || !TypeAssignable(want, inner) {
		return ""
	}
	sym := c.fa.References[Pos{Line: id.Line, Col: id.Col}]
	if sym == nil || sym.Kind != SymbolBinding || !c.patternBound[Pos{Line: sym.Pos.Line, Col: sym.Pos.Col}] {
		return ""
	}
	return c.typef("`%s` is a `%s`, bound one level short by the pattern on line %d; "+
		"write `Some(%s)` in its place there to bind the `%s`",
		id.Name, have, sym.Pos.Line, id.Name, inner)
}
