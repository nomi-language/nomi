package analysis

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A checked literal's type may have type arguments its handler leaves open:
// `impl CheckedLiteral<String, Route<P>> for Route<P>`. Each literal gets
// fresh ones (freshHandlerSignature), which the type the literal's position
// expects solves; the compiler then hands the literal's `check_type` each
// argument's spelling (spec §18 *Checked literals*), so the text can be held
// to them.
//
// std/http's `Route<P>` is the one checked literal whose type follows from its
// text when nothing else gives it, as a Regex literal's captures do
// (regex_literal.go): a route with no expected type has a `String` field per
// wildcard, `Route<.{id: String}>`, and `Route<Unit>` with none.

// freshHandlerSignature is ft with each type parameter it mentions replaced
// by a fresh type variable, so two literals of one generic handler are typed
// apart. A signature with none is ft itself.
func (c *checker) freshHandlerSignature(ft *FuncType) *FuncType {
	params := map[*TypeParam_]bool{}
	collectTypeParamPtrs(ft, params)
	if len(params) == 0 {
		return ft
	}
	subs := make(map[*TypeParam_]Type, len(params))
	for p := range params {
		subs[p] = c.freshTypeVar()
	}
	fresh, ok := substituteWith(ft, subs, nil).(*FuncType)
	if !ok {
		return ft
	}
	return fresh
}

// checkedLiteralTypeArgs solves the type arguments of ret, a checked
// literal's type, that its handler left open: from the expected type when it
// is the same type, and for a Route from its wildcards.
func (c *checker) checkedLiteralTypeArgs(n *ast.TaggedString, ret Type) Type {
	dt, isDistinct := resolveTypeVar(ret).(*DistinctType)
	if !isDistinct || len(dt.TypeArgs) == 0 {
		return ret
	}
	if exp, ok := resolveTypeVar(c.expectedAt).(*DistinctType); ok && !ContainsTypeParam(exp) && exp.Name == dt.Name && exp.Origin == dt.Origin && len(exp.TypeArgs) == len(dt.TypeArgs) {
		_ = c.unify(exp, dt, nil)
	}
	if dt.Origin == "std/http" && dt.Name == "Route" && len(dt.TypeArgs) == 1 {
		if tv, open := dt.TypeArgs[0].(*TypeVar); open && resolveTypeVar(tv) == Type(tv) {
			_ = c.unify(tv, routeParamsType(literalStatics(n)), nil)
		}
		// A route's parameters are read from the path and written into
		// links, field by field: the bound Route.to_with and
		// Route.path_with hold them to, checked at the literal that names
		// them.
		if p := resolveTV(dt.TypeArgs[0]); !containsTypeVar(p) && !ContainsTypeParam(p) {
			if help, bad := c.parseFieldsHelp(p); bad {
				c.addError(n.Line, max(n.Col-len(n.Tag), 1), "a route's parameters are read from its path and written into links as text: "+help)
			}
		}
	}
	return ret
}

// literalStatics is a typed literal's static text, slots left out.
func literalStatics(n *ast.TaggedString) string {
	var b strings.Builder
	for _, p := range n.Parts {
		if t, ok := p.(ast.StringText); ok {
			b.WriteString(t.Value)
		}
	}
	return b.String()
}

// routeParamsType is the parameters type a route pattern with no expected
// type has: an anonymous struct with a String field per wildcard, `{id}` and
// `{rest...}` alike, or Unit when it has none.
func routeParamsType(pattern string) Type {
	var fields []FieldDef
	for _, name := range RouteWildcards(pattern) {
		fields = append(fields, FieldDef{Name: name, Type: TypeString})
	}
	if len(fields) == 0 {
		return TypeUnit
	}
	slices.SortFunc(fields, func(a, b FieldDef) int { return strings.Compare(a.Name, b.Name) })
	return &AnonStructType{Fields: fields}
}

// RouteWildcards is the names of a route pattern's wildcards, in order: each
// path segment `{name}` or `{name...}`; `{$}` names none.
func RouteWildcards(pattern string) []string {
	path := pattern
	if i := strings.IndexByte(path, '/'); i >= 0 {
		path = path[i:]
	} else {
		return nil
	}
	var names []string
	for _, seg := range strings.Split(path, "/") {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := strings.TrimSuffix(seg[1:len(seg)-1], "...")
		if name == "" || name == "$" {
			continue
		}
		names = append(names, name)
	}
	return names
}

// isRouteType reports whether t is std/http's Route, and its parameters
// type.
func isRouteType(t Type) (Type, bool) {
	dt, ok := resolveTV(t).(*DistinctType)
	if !ok || dt.Origin != "std/http" || dt.Name != "Route" || len(dt.TypeArgs) != 1 {
		return nil, false
	}
	return resolveTV(dt.TypeArgs[0]), true
}

// routeCallHint is the help for a route with parameters passed where a
// `Route<Unit>` is expected, to `Route.path` or `Route.to`: the function
// that takes the parameters. "" for any other mismatch.
func routeCallHint(call *ast.Call, want, have Type) string {
	return routeCalleeHint(call.Func, want, have)
}

// routeCalleeHint is routeCallHint for the callee callee, and the reverse
// pick too: a handler of one parameter given to `Route.to_with` with a
// route that has none, which `Route.to` takes.
func routeCalleeHint(callee ast.Node, want, have Type) string {
	fn := ""
	if fa, ok := callee.(*ast.FieldAccess); ok && fa.Field != nil {
		fn = fa.Field.Name
		if ti, ok := fa.Object.(*ast.TypeIdent); !ok || ti.Name != "Route" {
			return ""
		}
	}
	if wf, ok := resolveTV(want).(*FuncType); ok && fn == "to_with" {
		hf, ok := resolveTV(have).(*FuncType)
		if ok && len(wf.Params) == 2 && len(hf.Params) == 1 && resolveTV(wf.Params[1]) == TypeUnit {
			return "this route has no parameters; use `Route.to(route, fn request -> ...)`, whose handler takes only the request"
		}
		return ""
	}
	wantP, ok := isRouteType(want)
	if !ok || wantP != TypeUnit {
		return ""
	}
	haveP, ok := isRouteType(have)
	if !ok || haveP == TypeUnit {
		return ""
	}
	switch fn {
	case "path":
		return fmt.Sprintf("this route has parameters, `%s`; use `Route.path_with(route, params)` to fill them into the link", haveP)
	case "to":
		return fmt.Sprintf("this route has parameters, `%s`; use `Route.to_with(route, fn request, params -> ...)` to hand them to the handler", haveP)
	}
	return fmt.Sprintf("this route has parameters, `%s`; a `Route<Unit>` has none", haveP)
}

// deferCheckedLiterals moves to the end of order each argument that is a
// checked literal (tagged, or untagged where its parameter's type has a
// CheckedLiteral impl) whose parameter's type holds a type parameter of
// the call that another argument solves on its own. The literal is then
// checked against the solved type, as an annotation would give it:
// `Route.to_with("PATCH /todos/{id}", update)` takes P from update's
// `.{id: Int}`, and the route is a `Route<.{id: Int}>` held to it, where
// checked first it would have defaulted to a String field per wildcard and
// refused the handler. An argument solves a parameter on its own when its
// type does not hang on the slot it fills: a name of a value whose type has
// no type parameter, or a lambda whose parameters are all annotated. A
// lambda that leaves a parameter's type to the call (`fn _, p -> ...`)
// takes it from the literal, which is then checked first, as written.
func (c *checker) deferCheckedLiterals(order []int, args []ast.Node, posSlots []int, defParams []string, params []Type) []int {
	slotOf := func(i int) int {
		if na, ok := args[i].(*ast.NamedArg); ok {
			return namedArgSlot(na.Name, defParams)
		}
		if i < len(posSlots) && posSlots[i] >= 0 {
			return posSlots[i]
		}
		return i
	}
	var deferred []int
	for _, i := range order {
		slot := slotOf(i)
		if slot < 0 || slot >= len(params) || !c.isCheckedLiteralArg(args[i], params[slot]) {
			continue
		}
		mentioned := map[*TypeParam_]bool{}
		collectTypeParamPtrs(params[slot], mentioned)
		for _, j := range order {
			other := slotOf(j)
			if j == i || other < 0 || other >= len(params) || !c.typedOnItsOwn(args[j]) {
				continue
			}
			solves := map[*TypeParam_]bool{}
			collectTypeParamPtrs(params[other], solves)
			if anyShared(mentioned, solves) {
				deferred = append(deferred, i)
				break
			}
		}
	}
	if len(deferred) == 0 {
		return order
	}
	out := make([]int, 0, len(order))
	for _, i := range order {
		if !slices.Contains(deferred, i) {
			out = append(out, i)
		}
	}
	return append(out, deferred...)
}

func anyShared(a, b map[*TypeParam_]bool) bool {
	for p := range a {
		if b[p] {
			return true
		}
	}
	return false
}

// isCheckedLiteralArg reports whether arg is a string literal read as a
// checked literal of param, a named type with type parameters of the call.
func (c *checker) isCheckedLiteralArg(arg ast.Node, param Type) bool {
	if na, ok := arg.(*ast.NamedArg); ok {
		arg = na.Value
	}
	arg = ungroupedArg(arg)
	if !ContainsTypeParam(param) {
		return false
	}
	if _, isParam := param.(*TypeParam_); isParam {
		return false
	}
	_, isTagged := arg.(*ast.TaggedString)
	if _, _, _, _, _, _, untagged := untaggedString(arg); !isTagged && !untagged {
		return false
	}
	_, build := c.targetLiteralType(param)
	return build != nil
}

// typedOnItsOwn reports whether arg's type is the same whatever its slot
// expects: a name of a value whose type holds no type parameter, or a
// lambda whose parameters are all annotated.
func (c *checker) typedOnItsOwn(arg ast.Node) bool {
	if na, ok := arg.(*ast.NamedArg); ok {
		arg = na.Value
	}
	switch v := ungroupedArg(arg).(type) {
	case *ast.Ident:
		return c.namesMonomorphicValue(Pos{Line: v.Line, Col: v.Col})
	case *ast.FieldAccess:
		return v.Field != nil && c.namesMonomorphicValue(Pos{Line: v.Field.Line, Col: v.Field.Col})
	case *ast.Lambda:
		for _, p := range v.Params {
			if p.TypeAnnotation == nil {
				return false
			}
		}
		return true
	}
	return false
}

// namesMonomorphicValue reports whether the name at pos refers to a symbol
// whose type is known and holds no type parameter.
func (c *checker) namesMonomorphicValue(pos Pos) bool {
	return c.monomorphicValueType(pos) != nil
}

// monomorphicValueType is the type of the symbol the name at pos refers
// to, when it is known and holds no type parameter; nil otherwise.
func (c *checker) monomorphicValueType(pos Pos) Type {
	sym := c.fa.References[pos]
	if sym == nil {
		return nil
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym.Type == nil || ContainsTypeParam(sym.Type) || containsTypeVar(sym.Type) {
		return nil
	}
	return sym.Type
}

// pipedLiteralExpected is the type a checked literal piped into call, a
// stage calling calleeTy, is expected to have when the stage's other
// arguments solve its parameter's type parameters on their own: `"PATCH
// /todos/{id}" |> Route.to_with(update)` checks the route as
// `Route<.{id: Int}>`, as `Route.to_with("PATCH /todos/{id}", update)`
// does (deferCheckedLiterals). The head is checked before the stage, so
// only an argument naming a value of known type counts; its type is read
// from its symbol, and the argument is checked with the stage. nil when
// head is no such literal or nothing solves its parameter.
func (c *checker) pipedLiteralExpected(head ast.Node, call *ast.Call, calleeTy Type) Type {
	ft, ok := resolveTypeVar(calleeTy).(*FuncType)
	if !ok || len(ft.Params) == 0 || !c.isCheckedLiteralArg(head, ft.Params[0]) {
		return nil
	}
	subs := map[*TypeParam_]Type{}
	defParams := c.callDefParams(call.Func)
	positional := 1
	for _, a := range call.Args {
		value, slot := a, 0
		if na, named := a.(*ast.NamedArg); named {
			value, slot = na.Value, namedArgSlot(na.Name, defParams)
		} else {
			slot = positional
			positional++
		}
		if slot < 1 || slot >= len(ft.Params) {
			continue
		}
		var t Type
		switch v := ungroupedArg(value).(type) {
		case *ast.Ident:
			t = c.monomorphicValueType(Pos{Line: v.Line, Col: v.Col})
		case *ast.FieldAccess:
			if v.Field != nil {
				t = c.monomorphicValueType(Pos{Line: v.Field.Line, Col: v.Field.Col})
			}
		}
		if t == nil {
			continue
		}
		trial := make(map[*TypeParam_]Type, len(subs))
		for k, v := range subs {
			trial[k] = v
		}
		if c.unifyInto(ft.Params[slot], t, trial) == nil {
			subs = trial
		}
	}
	if len(subs) == 0 {
		return nil
	}
	return Substitute(ft.Params[0], subs)
}
