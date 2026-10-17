package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A function's default parameter values apply to a call of the function by
// name. They do not shorten its type: `fn parse(s: String, strict: Bool =
// False)` named as a value is a `(String, Bool) -> Maybe<Int>`, and passing or
// binding it where a `(String) -> Maybe<Int>` is expected is a mismatch (spec
// §5, *Default Arguments*: "Defaults apply to calls, not to function values").
// A call through the value passes every argument: after `g = parse`, `g("1")`
// is an error. A lambda that calls it states which arguments the defaults
// fill. A lambda's own defaults and a partial's open defaults belong to
// the name the value is bound to and its copies: a call through any other
// value with no declaration behind it passes every argument (checkCall,
// calleeIsNamelessValue, calleeCopiesADeclaration).

// defaultedFuncValueHint is the hint appended to a mismatch between an
// expected function type and a function value that has the expected
// parameters plus defaulted ones, or "" for any other mismatch. arg is the
// value's expression, used to name it in the suggested lambda and, for a
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
	params := make([]string, n)
	for i := range params {
		params[i] = string(rune('a' + i))
	}
	list := strings.Join(params, ", ")
	return "defaults do not apply to a function value, so pass a lambda that calls it: fn " +
		list + " -> " + name + "(" + list + ")"
}

// namedFuncParams answers the declared parameters of the function node names
// (`parse`, `Parser.parse`, `io.print`, `Display.to_string`): a `fn`, a
// `host fn` or an interface function. A binding or a parameter that holds a
// function is not one: a lambda's defaults and a partial's open defaults
// belong to its binding (spec §5), a named function's to the function.
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

// funcValueType is the type of node, a name checked in value position. A
// named function's type carries its defaults so that a call of it by name may
// omit them; named as a value it is the function with its full parameter
// list, so a call through that value (`g = parse`, then `g("ab")`) passes
// every argument. The callee of a direct call keeps them.
func (c *checker) funcValueType(node ast.Node, ty Type) Type {
	ft, ok := ty.(*FuncType)
	if !ok || ft.DefaultCount == 0 || node == c.calleeNode || node == c.qualifierNode {
		return ty
	}
	if _, named := c.namedFuncParams(node); !named {
		return ty
	}
	whole := *ft
	whole.DefaultCount = 0
	return &whole
}

// heldFuncDefaults finds the named function a call's callee holds as a value,
// when that function has defaults: `g` in `g("ab")` after `g = parse`,
// through a chain of bindings (`h = g`). It answers the callee's and the
// function's spelling and the function's parameters, or ok false.
func (c *checker) heldFuncDefaults(callee ast.Node) (holder, name string, params []ast.Param, ok bool) {
	holder = funcValueName(ungroupExpr(callee))
	if holder == "" {
		return "", "", nil, false
	}
	node := ungroupExpr(callee)
	for depth := 0; depth < maxPartialChain; depth++ {
		sym, found := c.callFuncSym(node)
		if !found || sym.Kind == SymbolParam {
			return "", "", nil, false
		}
		b, isBinding := sym.Node.(*ast.Binding)
		if !isBinding || b.Value == nil {
			return "", "", nil, false
		}
		node = ungroupExpr(b.Value)
		ps, named := c.namedFuncParams(node)
		if !named {
			continue
		}
		name = funcValueName(node)
		for _, p := range ps {
			if p.Default != nil && name != "" {
				return holder, name, ps, true
			}
		}
		return "", "", nil, false
	}
	return "", "", nil, false
}

// tooFewThroughFuncValue reports a call given too few arguments through a
// value that holds a named function with defaults, saying that the defaults
// apply only to a call of the function by name and naming the lambda to bind
// instead. It reports nothing and answers false for any other call.
func (c *checker) tooFewThroughFuncValue(call *ast.Call, want, got int, piped bool) bool {
	if got >= want {
		return false
	}
	holder, name, params, ok := c.heldFuncDefaults(call.Func)
	if !ok {
		return false
	}
	counting := ""
	if piped {
		counting = " (counting the piped value)"
	}
	args := make([]string, got)
	for i := range args {
		if i < len(params) && params[i].Destructure == nil && params[i].Name != "" && params[i].Name != "_" {
			args[i] = params[i].Name
		} else {
			args[i] = string(rune('a' + i))
		}
	}
	list := strings.Join(args, ", ")
	c.report(TypeError{Line: call.Line, Col: 1, Message: fmt.Sprintf(
		"expected %d arguments, got %d%s: `%s` holds the function `%s` as a value, and defaults apply only to a call of `%s` by name",
		want, got, counting, holder, name, name)}.WithHint(fmt.Sprintf(
		"call `%s` directly, or bind a lambda that leaves the rest to its defaults: `%s = fn %s -> %s(%s)`",
		name, holder, list, name, list)))
	return true
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
