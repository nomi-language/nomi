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
			"to pass the error on, write `Err(e) -> Err(e)`, or unwrap with `try`",
			id.Name, have, have.TypeArgs[0])
	case want.Name == "Maybe" && len(want.TypeArgs) == 1:
		if TypesEqual(resolveTypeVar(want.TypeArgs[0]), resolveTypeVar(have.TypeArgs[0])) {
			return ""
		}
		return c.typef("`%s` is the whole `%s`, so its `Some` type is still `%s`; "+
			"to pass the absence on, write `None -> None`, or unwrap with `try`",
			id.Name, have, have.TypeArgs[0])
	}
	return ""
}
