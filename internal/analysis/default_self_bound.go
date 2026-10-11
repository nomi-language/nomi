package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// An interface default runs once per implementor, with `self` replaced by the
// implementing type. A conformance its body demands of `self` (a call of
// another interface's function on a `self` value, an interpolation, an
// operator, a generic call bounding it) must hold for every implementor that
// gets the default. The interface's own functions always do, and Debug is
// universal; anything else must be named by the function's `where self: X`.
// An implementor without X must then write the function itself, so a call of
// the function never reaches a default its implementor cannot run.

// enterDefaultSelf marks the start of checking default `method` of `iface`:
// its `where self` bounds bound `self` while the body is checked, and every
// conformance demanded of `self` is held to them. It returns the restore.
func (c *checker) enterDefaultSelf(iface *InterfaceType, method string) func() {
	prev, prevFn := c.defaultSelf, c.defaultSelfFn
	if iface == nil || iface.SelfParam == nil {
		return func() {}
	}
	sig := interfaceMethodSig(iface, method)
	var restoreBounds func()
	if sig != nil && len(sig.SelfBounds) > 0 {
		restoreBounds = c.pushWhereBounds([]WhereBound{{Param: iface.SelfParam, Bounds: sig.SelfBounds}})
	}
	c.defaultSelf, c.defaultSelfFn = iface.SelfParam, method
	return func() {
		if restoreBounds != nil {
			restoreBounds()
		}
		c.defaultSelf, c.defaultSelfFn = prev, prevFn
	}
}

func interfaceMethodSig(iface *InterfaceType, method string) *MethodSig {
	for i := range iface.Methods {
		if iface.Methods[i].Name == method {
			return &iface.Methods[i]
		}
	}
	return nil
}

// checkDefaultSelfDemand reports a conformance to `ifaceName` demanded of
// t by rec, when t is the `self` of the default body being checked and
// nothing guarantees it. The interface itself, Debug, and the function's
// `where self` bounds do. Inside a default body a `self` value has the
// interface's own type (interfaceDefaultMethodFuncType), and the bare type
// parameter appears where a signature names `self` directly.
func (c *checker) checkDefaultSelfDemand(t Type, ifaceName string, rec Recording) {
	// `==` falls back to structural equality for a type with no Equatable,
	// so it demands nothing an implementor can lack.
	if c.defaultSelf == nil || ifaceName == "Debug" || ifaceName == c.currentInterface || rec.Kind == RecordingKindEqualityOperator {
		return
	}
	pos := rec.Pos
	if !c.isDefaultSelfType(t) {
		return
	}
	iface, _ := c.reg.Lookup(c.currentInterface).(*InterfaceType)
	if iface == nil {
		return
	}
	if sig := interfaceMethodSig(iface, c.defaultSelfFn); sig != nil {
		for _, b := range sig.SelfBounds {
			if b != nil && b.Name == ifaceName {
				return
			}
		}
	}
	subject := "this value"
	for _, arg := range c.selfDemandSubjects {
		if na, ok := arg.(*ast.NamedArg); ok {
			arg = na.Value
		}
		if !c.isDefaultSelfType(c.fa.ExprTypes[arg]) {
			continue
		}
		pos.Line, pos.Col = nodeLineCol(arg)
		if id, ok := arg.(*ast.Ident); ok {
			subject = "`" + id.Name + "`"
		}
		break
	}
	if sym := c.fa.References[pos]; subject == "this value" && sym != nil && (sym.Kind == SymbolParam || sym.Kind == SymbolBinding) {
		subject = "`" + sym.Name + "`"
	}
	msg := fmt.Sprintf("%s is a `self`, and %s does not require %s of its implementors", subject, c.currentInterface, ifaceName)
	for _, e := range c.errors {
		if e.Line == pos.Line && e.Col == pos.Col && e.Message == msg {
			return
		}
	}
	c.report(TypeError{Line: pos.Line, Col: pos.Col, Message: msg}.WithHint(
		fmt.Sprintf("require it for this default only, so an implementor without %s writes the function itself: `%s`", ifaceName, defaultSignatureWithSelfBound(iface, c.defaultSelfFn, ifaceName))))
}

// isDefaultSelfType reports whether t is the `self` of the default body
// being checked.
func (c *checker) isDefaultSelfType(t Type) bool {
	if c.defaultSelf == nil || t == nil {
		return false
	}
	switch ty := resolveTypeVar(t).(type) {
	case *TypeParam_:
		return ty == c.defaultSelf
	case *InterfaceType:
		return ty.SelfParam == c.defaultSelf && ty.Name == c.currentInterface
	}
	return false
}

// isDefaultSelfValue reports whether t is the type of a `self` value of the
// default body being checked, which `self(p)` may take apart; a value
// annotated with the interface's name is an existential and is not one.
func (c *checker) isDefaultSelfValue(t Type) bool {
	switch ty := resolveTypeVar(t).(type) {
	case *TypeParam_:
		return c.isDefaultSelfType(ty)
	case *InterfaceType:
		return ty.DefaultSelf && c.isDefaultSelfType(ty)
	}
	return false
}

// enterSelfDemandSubjects sets the expressions a demand raised next may be
// about, and returns the restore.
func (c *checker) enterSelfDemandSubjects(nodes []ast.Node) func() {
	prev := c.selfDemandSubjects
	c.selfDemandSubjects = nodes
	return func() { c.selfDemandSubjects = prev }
}

// defaultSignatureWithSelfBound renders `method`'s signature with
// `where self: ...` naming its present self bounds and `add`.
func defaultSignatureWithSelfBound(iface *InterfaceType, method, add string) string {
	var sig *MethodSig
	if iface != nil {
		sig = interfaceMethodSig(iface, method)
	}
	if sig == nil {
		return fmt.Sprintf("fn %s(...) where self: %s", method, add)
	}
	params := make([]string, len(sig.Params))
	for i, p := range sig.Params {
		name := "_"
		if i < len(sig.ParamNames) {
			name = sig.ParamNames[i]
		}
		params[i] = name + ": " + typeString(p)
	}
	ret := ""
	if sig.Return != nil && !isUnitLike(sig.Return) {
		ret = ": " + typeString(sig.Return)
	}
	bounds := make([]string, 0, len(sig.SelfBounds)+1)
	for _, b := range sig.SelfBounds {
		bounds = append(bounds, b.String())
	}
	bounds = append(bounds, add)
	return fmt.Sprintf("fn %s(%s)%s where self: %s", method, strings.Join(params, ", "), ret, strings.Join(bounds, " and "))
}

// missingSelfBounds lists the `where self` bounds of default `sig` that
// recvTy does not implement, with iface's type arguments applied. An implementor missing one does not get the
// default.
func (c *checker) missingSelfBounds(sig *MethodSig, iface *InterfaceType, recvTy Type, pos Pos) []*InterfaceType {
	if sig == nil || !sig.HasDefault || recvTy == nil {
		return nil
	}
	// A bound may name the interface's type parameters (`where self:
	// Distinct<T>`); the impl header's arguments stand for them.
	subs := ifaceArgSubs(iface)
	var missing []*InterfaceType
	for _, b := range sig.SelfBounds {
		bound := b
		if len(subs) > 0 {
			if sb, ok := Substitute(b, subs).(*InterfaceType); ok {
				bound = sb
			}
		}
		if !typeImplementsInterface(c, recvTy, bound, pos, RecordingKindGenericBoundCheck) {
			missing = append(missing, bound)
		}
	}
	return missing
}

// selfBoundsText renders bounds as `Display` or `Display and Hashable`.
func selfBoundsText(bounds []*InterfaceType) string {
	parts := make([]string, len(bounds))
	for i, b := range bounds {
		parts[i] = b.String()
	}
	return strings.Join(parts, " and ")
}

// defaultSelfConforms reports whether t is a `self` value of the default
// body being checked and the default's `where self` names iface, so the value
// enters an iface-typed position (`shown(scale)` where `fn shown(x:
// Display)`). Each implementor that gets the default implements iface, and
// the default is lowered per implementor, so the value is boxed at the
// implementor's type.
func (c *checker) defaultSelfConforms(t Type, iface *InterfaceType) bool {
	at, ok := resolveTypeVar(t).(*InterfaceType)
	if !ok || !at.DefaultSelf || iface == nil || !c.isDefaultSelfType(at) {
		return false
	}
	owner, _ := c.reg.Lookup(c.currentInterface).(*InterfaceType)
	if owner == nil {
		return false
	}
	sig := interfaceMethodSig(owner, c.defaultSelfFn)
	if sig == nil {
		return false
	}
	for _, b := range sig.SelfBounds {
		if b == nil || b.Name != iface.Name {
			continue
		}
		if len(iface.TypeArgs) == 0 || TypesEqual(b, iface) {
			return true
		}
	}
	return false
}

// defaultSelfOperator answers the right-hand and output types of an operator
// whose left operand is a `self` value of the default being checked, from the
// default's `where self: Add<R, Out>` bound: `scale + scale` under
// `where self: Add<self, self>`. A `self` in the bound's arguments is the
// `self` value's own type.
func (c *checker) defaultSelfOperator(lt *InterfaceType, ifaceName string) (Type, Type, bool) {
	if lt == nil || !lt.DefaultSelf || !c.isDefaultSelfType(lt) {
		return nil, nil, false
	}
	owner, _ := c.reg.Lookup(c.currentInterface).(*InterfaceType)
	if owner == nil {
		return nil, nil, false
	}
	sig := interfaceMethodSig(owner, c.defaultSelfFn)
	if sig == nil {
		return nil, nil, false
	}
	for _, b := range sig.SelfBounds {
		if b == nil || b.Name != ifaceName || len(b.TypeArgs) != 2 {
			continue
		}
		subs := map[*TypeParam_]Type{owner.SelfParam: interfaceSelfType(owner)}
		return Substitute(b.TypeArgs[0], subs), Substitute(b.TypeArgs[1], subs), true
	}
	return nil, nil, false
}
