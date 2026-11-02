package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A function's default parameter values apply to a call of the function by
// name, and where a function type with fewer parameters is expected: `fn
// parse(s: String, strict: Bool = False)` named as a value is a `(String,
// Bool) -> Maybe<Int>`, and where a `(String) -> Maybe<Int>` is expected it is
// that function, whose call fills `strict` (spec §7, *Default Arguments*:
// "Defaults fill a function value only where a shorter type is expected";
// defaultFilledFuncValue). Its defaults, a lambda's own and a partial's open
// ones belong to the name and its local copies: after `g = parse`, `g("1")`
// fills `strict`, as `h()` does after `h = fn x = 1 -> x`. A call through any
// other value with no declaration behind it (a parameter, a field, a list
// element, a `once`) passes every argument (checkCall, calleeIsNamelessValue,
// calleeCopiesADeclaration).

// defaultFilledFuncValue is the type of node, a named function checked as a
// value of type ty where expected is expected: when expected is a function
// type with fewer parameters, and each of the function's parameters past
// them has a default, the value is the function of those first parameters,
// and the IR builder makes it a function whose body calls node by name, so
// the call fills the rest (callFuncValue). The shorter type is the
// reference's CallType, which is how the IR builder tells it from the
// function's own. `Iter.map(words, parse)` passes
// `(String) -> Maybe<Int>`. Any other value keeps ty, and a mismatch is
// reported where the value is used.
func (c *checker) defaultFilledFuncValue(node ast.Node, ty, expected Type) Type {
	ft, ok := ty.(*FuncType)
	if !ok {
		return ty
	}
	want, ok := resolveTypeVar(expected).(*FuncType)
	if !ok || len(want.Params) >= len(ft.Params) {
		return ty
	}
	params, named := c.namedFuncParams(node)
	if !named || len(params) != len(ft.Params) {
		return ty
	}
	for _, p := range params[len(want.Params):] {
		if p.Default == nil {
			return ty
		}
	}
	short := *ft
	short.Params = ft.Params[:len(want.Params):len(want.Params)]
	short.DefaultCount = 0
	c.attachCallType(node, &short)
	return &short
}

// defaultedFuncValueHint is the hint appended to a mismatch between an
// expected function type and a function value that has the expected
// parameters plus defaulted ones, or "" for any other mismatch. A value
// checked where the shorter type is expected takes it
// (defaultFilledFuncValue), so the mismatch arises where nothing was
// expected when the value was checked: a pipe's head, checked before the
// stage it feeds. arg is the
// value's expression, used to name it in the suggested capture and, for a
// named function, to count its defaults, which its value's type does not
// carry (funcValueType).
func (c *checker) defaultedFuncValueHint(expected, got Type, arg ast.Node) string {
	ef, ok := expected.(*FuncType)
	if !ok {
		return ""
	}
	gf, ok := got.(*FuncType)
	if !ok {
		return ""
	}
	defaults := gf.DefaultCount
	if defaults == 0 {
		if params, named := c.namedFuncParams(arg); named {
			for _, p := range params {
				if p.Default != nil {
					defaults++
				}
			}
		}
	}
	if defaults == 0 {
		return ""
	}
	n := len(ef.Params)
	if n >= len(gf.Params) || n < len(gf.Params)-defaults {
		return ""
	}
	name := funcValueName(arg)
	if name == "" {
		name = "f"
	}
	if n == 0 {
		// A capture uses at least `&1`, so a function of no arguments is a
		// lambda.
		return "a function value takes its defaults only where its shorter type is expected, so pass a lambda that calls it: fn -> " + name + "()"
	}
	holes := make([]string, n)
	for i := range holes {
		holes[i] = fmt.Sprintf("&%d", i+1)
	}
	return "a function value takes its defaults only where its shorter type is expected, so pass a capture that calls it: &" +
		name + "(" + strings.Join(holes, ", ") + ")"
}

// namedFuncParams answers the declared parameters of the function node names
// (`parse`, `Parser.parse`, `io.print`, `Display.to_string`): a `fn`, a
// `host fn` or an interface function. A binding or a parameter that holds a
// function is not one: a lambda's defaults and a partial's open defaults
// belong to its binding (spec §7), a named function's to the function.
func (c *checker) namedFuncParams(node ast.Node) ([]ast.Param, bool) {
	if na, ok := node.(*ast.NamedArg); ok {
		node = na.Value
	}
	sym, ok := c.callFuncSym(ungroupExpr(node))
	if !ok || sym.Kind == SymbolParam {
		return nil, false
	}
	switch def := sym.Node.(type) {
	case *ast.FuncDef:
		return def.Params, true
	case *ast.ExternFunc:
		return def.Params, true
	case *ast.InterfaceMethod:
		return def.Params, true
	}
	return nil, false
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
