package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A value whose static type is interface I may hold any implementor of I.
// Nomi has no supertraits, so I guarantees nothing of its implementors but
// I's own functions. A conformance demanded of such a value (an
// interface-qualified call or pipe, an interpolation, an ordering or
// arithmetic operator, a generic call bounding it) must therefore be I
// itself. Debug is universal and `==` falls back to structural equality, so
// neither needs one. Without the rule a program using Display on a `Scale`
// value was accepted and then could not run. This is the existential
// counterpart of type_param_bound.go and default_self_bound.go.

// checkExistentialDemand reports a conformance to ifaceName demanded of a
// value of interface type it by rec.
func (c *checker) checkExistentialDemand(it *InterfaceType, ifaceName string, rec Recording) {
	if it == nil || it.Name == "" || it.Name == ifaceName || ifaceName == "Debug" || rec.Kind == RecordingKindEqualityOperator {
		return
	}
	if _, isIface := c.reg.Lookup(ifaceName).(*InterfaceType); !isIface {
		return
	}
	matches := func(t Type) bool {
		at, ok := resolveTypeVar(t).(*InterfaceType)
		return ok && !at.DefaultSelf && at.Name == it.Name
	}
	pos := rec.Pos
	subject, verb := "this value", "is"
	var param string
	if arg, ok := c.demandSubject(matches); ok {
		pos.Line, pos.Col = exprStartLineCol(arg)
		if text, ok := exprPathText(arg); ok {
			subject = "`" + text + "`"
			if id, isIdent := arg.(*ast.Ident); isIdent {
				param = id.Name
			}
		}
	} else if arg, ok := c.demandSubject(func(t Type) bool { return t != nil && mentionsInterface(t, it.Name) }); ok {
		// `"${xs}"` with xs a List<Scale>: the demand reaches Scale through xs.
		pos.Line, pos.Col = exprStartLineCol(arg)
		verb = "holds"
		if text, ok := exprPathText(arg); ok {
			subject = "`" + text + "`"
		}
	} else if sym := c.fa.References[pos]; sym != nil && (sym.Kind == SymbolParam || sym.Kind == SymbolBinding) {
		subject = "`" + sym.Name + "`"
		param = sym.Name
	}
	article := "a"
	if verb == "holds" {
		article = "values of"
	}
	msg := fmt.Sprintf("%s %s %s `%s`, and %s does not require %s of its implementors", subject, verb, article, it.String(), it.String(), ifaceName)
	for _, e := range c.errors {
		if e.Line == pos.Line && e.Col == pos.Col && e.Message == msg {
			return
		}
	}
	c.report(TypeError{Line: pos.Line, Col: pos.Col, Message: msg}.WithHint(c.existentialDemandHelp(it, ifaceName, param, rec.Kind == RecordingKindOrderingOperator || rec.Kind == RecordingKindOperator)))
}

// existentialDemandHelp suggests a type parameter bounded by both
// interfaces in place of the interface type. An operator's operands must be
// one type, so for one every parameter of that type becomes T.
func (c *checker) existentialDemandHelp(it *InterfaceType, ifaceName, param string, operator bool) string {
	bound := fmt.Sprintf("T: %s and %s", it.String(), ifaceName)
	if sig, ok := c.sigWithTypeParamFor(it, param, operator); ok {
		return fmt.Sprintf("take a type parameter bounded by both: `%s where %s`", sig, bound)
	}
	return fmt.Sprintf("take a type parameter bounded by both instead of `%s`: `where %s`", it.String(), bound)
}

// sigWithTypeParamFor renders the enclosing function's signature with
// parameter param, declared with interface type it, given a new type
// parameter T in its place (every parameter of that type when all is set).
// It reports false when param is not such a parameter of a plain function.
func (c *checker) sigWithTypeParamFor(it *InterfaceType, param string, all bool) (string, bool) {
	fn := c.currentFnDecl
	if fn == nil || param == "" || c.currentInterface != "" {
		return "", false
	}
	at := -1
	for i, p := range fn.Params {
		if p.Name == param {
			at = i
		}
	}
	if at < 0 {
		return "", false
	}
	sym := c.fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
	ft, _ := symType(sym).(*FuncType)
	if ft == nil || at >= len(ft.Params) {
		return "", false
	}
	isIt := func(t Type) bool {
		pt, ok := resolveTypeVar(t).(*InterfaceType)
		return ok && !pt.DefaultSelf && pt.Name == it.Name && len(pt.TypeArgs) == 0
	}
	if !isIt(ft.Params[at]) {
		return "", false
	}
	tps := []string{}
	for _, p := range fn.TypeParams {
		if p.Name == "T" {
			return "", false
		}
		tps = append(tps, p.Name)
	}
	tps = append(tps, "T")
	ps := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		ty := "_"
		if i < len(ft.Params) {
			ty = typeString(ft.Params[i])
			if all && isIt(ft.Params[i]) {
				ty = "T"
			}
		}
		if i == at {
			ty = "T"
		}
		ps[i] = p.Name + ": " + ty
	}
	sig := "fn " + fn.Name + "<" + strings.Join(tps, ", ") + ">(" + strings.Join(ps, ", ") + ")"
	if ft.Return != nil && !isUnitLike(ft.Return) {
		sig += " -> " + typeString(ft.Return)
	}
	return sig, true
}

// An interface-qualified call `I.f(...)` picks the implementor from its
// `self` arguments, or from where its result goes when `self` is only in the
// result. A value of interface type I names no implementor, so on one the
// call can run only when nothing else in the signature needs to be that
// implementor's own type: a `self` result, a second `self` argument, or a
// parameter holding `self` values would each need it. A function with no
// `self` parameter, called through I with nothing naming the implementor,
// has none to run. Inside one of I's own defaults the implementor is the one
// the default runs for.

// checkExistentialSelfCall reports an interface-qualified call of fa on
// iface that no implementor can answer. subs is the call's solved type
// parameters; subjects are its argument expressions.
func (c *checker) checkExistentialSelfCall(fa *ast.FieldAccess, iface *InterfaceType, subs map[*TypeParam_]Type, subjects []ast.Node, line, col int) {
	if fa == nil || fa.Field == nil || iface == nil || iface.SelfParam == nil || len(iface.TypeParamDefs) > 0 {
		return
	}
	sig := interfaceMethodSig(iface, fa.Field.Name)
	if sig == nil {
		return
	}
	self := iface.SelfParam
	concrete, solved := subs[self]
	if solved {
		concrete = resolveTypeVar(concrete)
		if _, open := concrete.(*TypeVar); open {
			solved = false
		}
	}
	existential := false
	if at, ok := concrete.(*InterfaceType); solved && ok && !at.DefaultSelf && at.Name == iface.Name {
		existential = true
	}
	var selfAt []int
	nested := ""
	for i, p := range sig.Params {
		if tp, ok := p.(*TypeParam_); ok && tp == self {
			selfAt = append(selfAt, i)
			continue
		}
		if nested == "" && mentionsTypeParam(p, self) {
			nested = "_"
			if i < len(sig.ParamNames) && sig.ParamNames[i] != "" {
				nested = sig.ParamNames[i]
			}
		}
	}
	returnsSelf := sig.Return != nil && mentionsTypeParam(sig.Return, self)
	callee := iface.Name + "." + fa.Field.Name
	inOwnDefault := c.defaultSelf == self && c.currentInterface == iface.Name
	if len(selfAt) == 0 {
		if (solved && !existential) || (!solved && inOwnDefault) {
			return
		}
		msg := fmt.Sprintf("`%s` has no `self` parameter, so this call does not say which implementor of %s runs", callee, iface.Name)
		c.reportOnce(line, col, msg, fmt.Sprintf("call it on the implementing type (`Type.%s(...)`), or on a type parameter bounded by %s (`T.%s(...)` with `where T: %s`)", fa.Field.Name, iface.Name, fa.Field.Name, iface.Name))
		return
	}
	if !existential || (!returnsSelf && len(selfAt) < 2 && nested == "") {
		return
	}
	subject, param := "a value", ""
	matches := func(t Type) bool {
		at, ok := resolveTypeVar(t).(*InterfaceType)
		return ok && !at.DefaultSelf && at.Name == iface.Name
	}
	for _, arg := range subjects {
		if na, ok := arg.(*ast.NamedArg); ok {
			arg = na.Value
		}
		if arg == nil || !matches(c.fa.ExprTypes[arg]) {
			continue
		}
		line, col = exprStartLineCol(arg)
		if text, ok := exprPathText(arg); ok {
			subject = "`" + text + "`"
			if id, isIdent := arg.(*ast.Ident); isIdent {
				param = id.Name
			}
		}
		break
	}
	a := "a"
	if strings.ContainsAny(iface.Name[:1], "AEIOU") {
		a = "an"
	}
	var why string
	switch {
	case returnsSelf:
		why = fmt.Sprintf("its result is `self`, the implementor's own type, and %s `%s` value does not say which implementor it holds", a, iface.Name)
	case len(selfAt) > 1:
		why = fmt.Sprintf("its %d `self` arguments must be one implementor, and `%s` values may hold different ones", len(selfAt), iface.Name)
	default:
		why = fmt.Sprintf("its parameter `%s` holds `self` values, which must be the implementor's own type, and %s `%s` value does not say which implementor it holds", nested, a, iface.Name)
	}
	msg := fmt.Sprintf("`%s` cannot be called on %s, %s `%s`: %s", callee, subject, a, iface.Name, why)
	help := fmt.Sprintf("take a type parameter bounded by %s instead, so the implementor is known: `where T: %s`", iface.Name, iface.Name)
	if sigText, ok := c.sigWithTypeParamFor(iface, param, len(selfAt) > 1); ok {
		help = fmt.Sprintf("take a type parameter bounded by %s instead, so the implementor is known: `%s where T: %s`", iface.Name, sigText, iface.Name)
	}
	c.reportOnce(line, col, msg, help)
}

// reportOnce reports msg with hint at line:col unless the same message is
// already reported there.
func (c *checker) reportOnce(line, col int, msg, hint string) {
	for _, e := range c.errors {
		if e.Line == line && e.Col == col && e.Message == msg {
			return
		}
	}
	c.report(TypeError{Line: line, Col: col, Message: msg}.WithHint(hint))
}

// mentionsInterface reports whether t holds a value of interface name
// somewhere inside it (`List<Scale>`, `#(Int, Scale)`).
func mentionsInterface(t Type, name string) bool {
	t = resolveTypeVar(t)
	if at, ok := t.(*InterfaceType); ok && !at.DefaultSelf && at.Name == name {
		return true
	}
	for _, a := range typeChildren(t) {
		if mentionsInterface(a, name) {
			return true
		}
	}
	return false
}
