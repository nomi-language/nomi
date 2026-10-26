package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// `Distinct<T>` and the `self(...)` forms it admits.
//
// Every distinct type `type X T` satisfies std's `Distinct<T>` for exactly
// the T it wraps, the way every struct satisfies `Struct`: conformance is a
// predicate on the type, never a registered impl, so `impl Distinct for X`
// is rejected (builder.go) and a (X, Distinct) demand is never missing
// (missing_impl.go).
//
// An interface default bounded by `where self: Distinct<T>` may take the
// implementing type apart with the pattern `self(p)` (a parameter, a `case`
// arm, a binding) and build one with `self(x)`; `self` alone is that
// constructor as a function value. The default is lowered once per
// implementor, so each form is that implementor's own destructuring or
// constructor. Anywhere else the forms are errors.
//
// An opaque type implemented outside its own file would have its
// representation opened by such a default, so it does not get one: it
// writes the function itself (checkOpaqueSelfDefault).

// isStdDistinct reports whether iface is std's `Distinct`.
func isStdDistinct(iface *InterfaceType) bool {
	return iface != nil && iface.Name == "Distinct" && (iface.Origin == "std/structs" || iface.Origin == "")
}

// distinctInnerOf is the type a distinct type wraps, with its own type
// arguments applied, or nil for a zero-sized one.
func distinctInnerOf(dt *DistinctType) Type {
	if dt == nil || dt.Inner == nil {
		return nil
	}
	if len(dt.TypeParamDefs) > 0 && len(dt.TypeArgs) == len(dt.TypeParamDefs) {
		subs := make(map[*TypeParam_]Type, len(dt.TypeParamDefs))
		for i, def := range dt.TypeParamDefs {
			subs[def] = dt.TypeArgs[i]
		}
		return Substitute(dt.Inner, subs)
	}
	return dt.Inner
}

// distinctConforms reports whether concrete satisfies the Distinct bound
// iface: it is a distinct type wrapping exactly iface's type argument. same
// decides that equality; a caller that may bind a type variable or
// parameter in the argument passes a unifier.
func distinctConforms(concrete Type, iface *InterfaceType, same func(want, inner Type) bool) bool {
	dt, ok := resolveTypeVar(concrete).(*DistinctType)
	if !ok {
		return false
	}
	inner := distinctInnerOf(dt)
	if inner == nil {
		return false
	}
	if len(iface.TypeArgs) == 0 || iface.TypeArgs[0] == nil {
		return true
	}
	return same(iface.TypeArgs[0], inner)
}

// distinctSatisfies is distinctConforms with the checker's unifier for a
// bound whose argument is still open (`Distinct<T>` with T an interface's
// type parameter), and type identity otherwise.
func (c *checker) distinctSatisfies(concrete Type, iface *InterfaceType) bool {
	return distinctConforms(concrete, iface, func(want, inner Type) bool {
		if ContainsTypeParam(want) || containsTypeVar(want) {
			return c.unify(want, inner, map[*TypeParam_]Type{}) == nil
		}
		return TypesEqual(want, inner)
	})
}

// distinctSelfBound is the `Distinct<T>` among the `where self` bounds of
// the default body being checked, or nil.
func (c *checker) distinctSelfBound() *InterfaceType {
	if c.defaultSelf == nil {
		return nil
	}
	iface, _ := c.reg.Lookup(c.currentInterface).(*InterfaceType)
	sig := interfaceMethodSig(iface, c.defaultSelfFn)
	if sig == nil {
		return nil
	}
	for _, b := range sig.SelfBounds {
		if isStdDistinct(b) {
			return b
		}
	}
	return nil
}

// selfInner admits a `self(...)` form at line:col and returns the type the
// implementor wraps, the T of the default's `where self: Distinct<T>`. Where
// the form is not admitted it reports why and returns false.
func (c *checker) selfInner(line, col int) (Type, bool) {
	if c.defaultSelf == nil {
		e := TypeError{Line: line, Col: col, Message: "`self(...)` only appears in an interface's default function"}
		if c.selfTypeName != "" {
			e = e.WithHint(fmt.Sprintf("an impl names its own type: write `%s(...)`", c.selfTypeName))
		}
		c.reportErrorOnce(e)
		return nil, false
	}
	bound := c.distinctSelfBound()
	if bound == nil {
		c.reportErrorOnce(TypeError{Line: line, Col: col,
			Message: "`self(...)` needs `where self: Distinct<T>` on this function"}.WithHint(
			fmt.Sprintf("with T the type every implementor wraps: `fn %s(...) where self: Distinct<T>`", c.defaultSelfFn)))
		return nil, false
	}
	if len(bound.TypeArgs) == 0 || bound.TypeArgs[0] == nil {
		return nil, false
	}
	return bound.TypeArgs[0], true
}

// reportErrorOnce reports e unless the same message is already at its position.
func (c *checker) reportErrorOnce(e TypeError) {
	for _, prev := range c.errors {
		if prev.Line == e.Line && prev.Col == e.Col && prev.Message == e.Message {
			return
		}
	}
	c.report(e)
}

// defaultSelfValueType is the type a `self` value has inside the default
// body being checked: the interface's own type, as interfaceDefaultMethodFuncType
// gives its `self` parameters.
func (c *checker) defaultSelfValueType() Type {
	iface, _ := c.reg.Lookup(c.currentInterface).(*InterfaceType)
	return interfaceSelfType(iface)
}

// checkSelfPattern checks `self(p)` against the value it matches, which
// must be a `self`: p is checked against the wrapped type.
func (c *checker) checkSelfPattern(n *ast.EnumPattern, expectedTy Type) {
	inner, ok := c.selfInner(n.Line, n.Col)
	if ok && expectedTy != nil && !c.isDefaultSelfValue(expectedTy) {
		c.addError(n.Line, n.Col, fmt.Sprintf("`self(...)` takes apart a value of type `self`, got %s", formatTypeForError(expectedTy)))
		ok = false
	}
	if !ok {
		inner = nil
	}
	if n.Payload != nil {
		c.checkPattern(n.Payload, inner)
		return
	}
	if n.Binding != "" && inner != nil {
		if sym := c.fa.Definitions[Pos{Line: n.Line, Col: n.BindingCol}]; sym != nil {
			sym.Type = inner
		}
	}
}

// checkSelfValue types `self` named as a value: the implementor's
// constructor, a function from the wrapped type to `self`.
func (c *checker) checkSelfValue(n *ast.SelfExpr) Type {
	inner, ok := c.selfInner(n.Line, n.Col)
	if !ok {
		return nil
	}
	return &FuncType{Params: []Type{inner}, Return: c.defaultSelfValueType()}
}

// checkSelfCall checks `self(x)`: x is the wrapped value, and the call
// builds a `self`.
func (c *checker) checkSelfCall(n *ast.Call) Type {
	callee := n.Func.(*ast.SelfExpr)
	inner, ok := c.selfInner(callee.Line, callee.Col)
	if !ok {
		c.checkArgs(n.Args)
		return nil
	}
	selfTy := c.defaultSelfValueType()
	c.fa.recordExprType(callee, &FuncType{Params: []Type{inner}, Return: selfTy})
	if callHasPlaceholder(n.Args) {
		c.checkArgs(n.Args)
		c.addError(callee.Line, callee.Col, "`self` is already the constructor function; write `self` instead of `self(&1)`")
		return selfTy
	}
	if len(n.Args) != 1 || isNamedArg(n.Args[0]) {
		c.checkArgs(n.Args)
		c.addError(callee.Line, callee.Col, fmt.Sprintf(
			"`self` wraps %s, so self(...) takes one argument, %s %s; got %d",
			formatTypeForError(inner), articleFor(formatTypeForError(inner)), formatTypeForError(inner), len(n.Args)))
		return selfTy
	}
	argTy := c.checkNodeExpecting(n.Args[0], inner)
	if argTy == nil {
		return selfTy
	}
	line, col := startOf(n.Args[0])
	matches := false
	if ContainsTypeParam(inner) {
		matches = c.unify(inner, argTy, map[*TypeParam_]Type{}) == nil
	} else {
		matches = c.argMatchesParam(argTy, inner, c.recPos(line, col), RecordingKindCallSite)
	}
	if !matches {
		c.addError(line, col, fmt.Sprintf("`self` wraps %s, so self(...) takes %s %s; got %s",
			formatTypeForError(inner), articleFor(formatTypeForError(inner)), formatTypeForError(inner), formatTypeForError(argTy)))
	}
	return selfTy
}

// selfShapeOf reports whether an interface default takes `self` apart
// (a `self(p)` pattern) and whether it builds one (`self(x)` or `self` as
// a value), anywhere in its parameters or body.
func selfShapeOf(m *ast.InterfaceMethod) (takesApart, builds bool) {
	if m == nil || m.Body == nil {
		return false, false
	}
	visit := func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.EnumPattern:
			if _, ok := v.Variant.(*ast.SelfType); ok {
				takesApart = true
			}
		case *ast.SelfExpr:
			builds = true
		}
		return true
	}
	for _, p := range m.Params {
		if p.Destructure != nil {
			ast.Inspect(p.Destructure, visit)
		}
	}
	ast.Inspect(m.Body, visit)
	return takesApart, builds
}

// selfPatternParam reports whether a parameter's pattern is `self(p)`
// (under any `as` names), whose type is `self`.
func selfPatternParam(p ast.Param) bool {
	ep, ok := ast.WithoutAs(p.Destructure).(*ast.EnumPattern)
	if !ok {
		return false
	}
	_, isSelf := ep.Variant.(*ast.SelfType)
	return isSelf
}

// opaqueHere reports whether recvTy is an opaque distinct type whose
// construction surface this file cannot see.
func (c *checker) opaqueHere(recvTy Type) (*DistinctType, bool) {
	dt, ok := resolveTypeVar(recvTy).(*DistinctType)
	if !ok || !dt.Opaque {
		return nil, false
	}
	if c.fa != nil && c.fa.FilePath == dt.OwningSourceFile {
		return nil, false
	}
	return dt, true
}

// opaqueSelfDefaultMessage is the error at an impl whose opaque receiver
// would get default m, which takes `self` apart or builds one.
func opaqueSelfDefaultMessage(recv string, iface string, m *MethodSig) string {
	uses := "builds and takes apart"
	switch {
	case m.TakesSelfApart && !m.BuildsSelf:
		uses = "takes apart"
	case m.BuildsSelf && !m.TakesSelfApart:
		uses = "builds"
	}
	return fmt.Sprintf("`%s` is opaque here, so it can't use `%s.%s`'s default, which %s `self`; write `fn %s` in this impl",
		recv, iface, m.Name, uses, m.Name)
}

// defaultWithheld reports whether default m is not the receiver's: its
// `where self` bounds are unmet, or it opens an opaque receiver from outside
// its file. Writing the function then overrides nothing, even when the
// default is final.
func (c *checker) defaultWithheld(m *MethodSig, iface *InterfaceType, recvTy Type, pos Pos) bool {
	if len(c.missingSelfBounds(m, iface, recvTy, pos)) > 0 {
		return true
	}
	_, opaque := c.opaqueHere(recvTy)
	return opaque && (m.TakesSelfApart || m.BuildsSelf)
}

// ifaceArgSubs maps an interface instance's type parameters to its
// arguments, so a default's `where self: Distinct<T>` names the T an impl
// header wrote.
func ifaceArgSubs(iface *InterfaceType) map[*TypeParam_]Type {
	if iface == nil || len(iface.TypeParamDefs) == 0 || len(iface.TypeArgs) != len(iface.TypeParamDefs) {
		return nil
	}
	subs := make(map[*TypeParam_]Type, len(iface.TypeParamDefs))
	for i, def := range iface.TypeParamDefs {
		if arg := iface.TypeArgs[i]; arg != nil && arg != Type(def) {
			subs[def] = arg
		}
	}
	return subs
}

// substitutedBounds is bounds with subs applied, for a message that names
// the bound an impl must meet.
func substitutedBounds(bounds []*InterfaceType, subs map[*TypeParam_]Type) []*InterfaceType {
	if len(subs) == 0 {
		return bounds
	}
	out := make([]*InterfaceType, len(bounds))
	for i, b := range bounds {
		out[i] = b
		if sb, ok := Substitute(b, subs).(*InterfaceType); ok {
			out[i] = sb
		}
	}
	return out
}

// selfBoundHint is the help at an impl whose receiver lacks some of a
// default's `where self` bounds. A `Distinct<T>` cannot be implemented, so
// for one the help says what the receiver is instead.
func selfBoundHint(missing []*InterfaceType, recvName string, recvTy Type, fn string) string {
	write := fmt.Sprintf("write `fn %s` in this impl", fn)
	var others []*InterfaceType
	var distinct *InterfaceType
	for _, b := range missing {
		if isStdDistinct(b) {
			distinct = b
		} else {
			others = append(others, b)
		}
	}
	if distinct == nil {
		return fmt.Sprintf("implement %s for %s, or %s", selfBoundsText(missing), recvName, write)
	}
	what := fmt.Sprintf("`%s` is not a distinct type", recvName)
	if dt, ok := resolveTypeVar(recvTy).(*DistinctType); ok {
		if inner := distinctInnerOf(dt); inner != nil {
			what = fmt.Sprintf("`%s` wraps %s", recvName, formatTypeForError(inner))
		} else {
			what = fmt.Sprintf("`%s` wraps nothing", recvName)
		}
	}
	if len(others) > 0 {
		return fmt.Sprintf("%s, so it is not a %s; %s (implementing %s alone is not enough)", what, distinct, write, selfBoundsText(others))
	}
	return fmt.Sprintf("%s, so it is not a %s; %s", what, distinct, write)
}

// selfColumnInner is the type `self` wraps when t is the `self` of the
// default being checked, bounded by `where self: Distinct<T>`, and a pattern
// in column takes it apart; coverage then splits it as a distinct type.
func (c *checker) selfColumnInner(t *InterfaceType, column []ast.Node) (Type, bool) {
	if !c.isDefaultSelfValue(t) {
		return nil, false
	}
	bound := c.distinctSelfBound()
	if bound == nil || len(bound.TypeArgs) == 0 || bound.TypeArgs[0] == nil {
		return nil, false
	}
	for _, p := range column {
		if ep, ok := ast.WithoutAs(p).(*ast.EnumPattern); ok {
			if _, isSelf := ep.Variant.(*ast.SelfType); isSelf {
				return bound.TypeArgs[0], true
			}
		}
	}
	return nil, false
}
