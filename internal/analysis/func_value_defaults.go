package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A function's default parameter values apply to a call. They do not shorten
// its type: `fn parse(s: String, strict: Bool = False)` named as a value is a
// `(String, Bool) -> Maybe<Int>`, and passing or binding it where a
// `(String) -> Maybe<Int>` is expected is a mismatch (spec §5, *Default
// Arguments*: "Defaults apply to calls, not to function values"). A lambda that calls it states which
// arguments the defaults fill.

// defaultedFuncValueHint is the hint appended to a mismatch between an
// expected function type and a function value that has the expected
// parameters plus defaulted ones, or "" for any other mismatch. arg is the
// value's expression, used to name it in the suggested lambda.
func defaultedFuncValueHint(expected, got Type, arg ast.Node) string {
	ef, ok := expected.(*FuncType)
	if !ok {
		return ""
	}
	gf, ok := got.(*FuncType)
	if !ok || gf.DefaultCount == 0 {
		return ""
	}
	n := len(ef.Params)
	if n >= len(gf.Params) || n < len(gf.Params)-gf.DefaultCount {
		return ""
	}
	name := funcValueName(arg)
	if name == "" {
		name = "f"
	}
	params := make([]string, n)
	for i := range params {
		params[i] = string(rune('a' + i))
	}
	list := strings.Join(params, ", ")
	return "defaults do not apply to a function value, so pass a lambda that calls it: |" +
		list + "| " + name + "(" + list + ")"
}

// funcValueName spells a function named as a value (`parse`,
// `Parser.parse`, `io.print`), or "" for any other expression.
func funcValueName(n ast.Node) string {
	switch x := n.(type) {
	case *ast.NamedArg:
		return funcValueName(x.Value)
	case *ast.Ident:
		return x.Name
	case *ast.TypeIdent:
		return x.Name
	case *ast.FieldAccess:
		if x.Field == nil {
			return ""
		}
		if owner := funcValueName(x.Object); owner != "" {
			return owner + "." + x.Field.Name
		}
	}
	return ""
}

// funcArityDiffers reports a function value whose parameter count is not the
// expected function type's. Unification already refuses it; the generic call
// path reports it rather than leaving it to a later check that never runs.
func funcArityDiffers(expected, got Type) bool {
	ef, ok := expected.(*FuncType)
	if !ok {
		return false
	}
	gf, ok := got.(*FuncType)
	return ok && len(ef.Params) != len(gf.Params)
}
