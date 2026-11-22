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
// itself. Debug is universal, and `==` and hashing compare the held values by
// their own types (ir.ValIface, vm/keys.go), so neither Debug nor Equatable
// nor Hashable needs one (existentialSatisfies). Comparable does: values of
// two types have no order. Without the rule a program using Display on a
// `Scale` value was accepted and then could not run. This is the existential
// counterpart of type_param_bound.go and default_self_bound.go.
//
// Holding I is not enough at a bound `T: I`, since a generic body may call
// any of I's functions on T's values: an interface type meets the bound
// only when none of them needs the implementor's own type
// (existentialMeets, over the selfUse a direct call is held to), so
// Comparable, whose `compare` takes two `self` values, is met by no
// interface type. The checker's bound checks ask it
// (checkExistentialBound), and so does impl matching
// (typeSatisfiesInterfaceForImplMatch). A generic refinement's declaration
// check is the one exception: it types the rule's `try_from` at its
// bounds' interfaces as a witness for every implementor, which the IR
// builder lowers for the effect walk and never runs (ruleWitness).

// existentialSatisfies reports whether every value of interface type it
// satisfies the std interface ifaceName, declared by origin ("" when
// unknown), whatever it holds: Debug, which every type implements, and
// Equatable and Hashable, which compare and hash the held value by its
// run-time type (unequal across types, through that type's own equality and
// hash otherwise). An `Iter<T>` value is a function and has neither
// (equality_domain.go).
func existentialSatisfies(it *InterfaceType, ifaceName, origin string) bool {
	switch ifaceName {
	case "Debug":
		return true
	case "Equatable", "Hashable":
		return !isStdIterType(it) && (origin == "" || origin == deriveProtocolHomeModule[ifaceName])
	}
	return false
}

// existentialSatisfiesNamed is existentialSatisfies for the interface
// ifaceName as this file resolves it.
func (c *checker) existentialSatisfiesNamed(it *InterfaceType, ifaceName string) bool {
	origin := ""
	if bound, ok := c.reg.Lookup(ifaceName).(*InterfaceType); ok {
		origin = bound.Origin
	}
	return existentialSatisfies(it, ifaceName, origin)
}

// checkExistentialDemand reports a conformance to ifaceName demanded of a
// value of interface type it by rec: an interface, or a conjunction, which
// guarantees each of its members and what every one of them guarantees.
func (c *checker) checkExistentialDemand(it Type, ifaceName string, rec Recording) {
	members := InterfaceMembers(it)
	if len(members) == 0 || members[0].Name == "" || rec.Kind == RecordingKindEqualityOperator {
		return
	}
	universal := true
	for _, m := range members {
		if sameNominalIdentity(m.Origin, m.Name, rec.IfaceOrigin, ifaceName) {
			if rec.Kind == RecordingKindGenericBoundCheck {
				c.checkExistentialBound(it, m, rec)
			}
			return
		}
		universal = universal && c.existentialSatisfiesNamed(m, ifaceName)
	}
	if universal {
		return
	}
	// The demand is of an interface: one this file names, or one the demand
	// names by its declaration, as a part's demand of an anonymous struct
	// read from text names std/parse's Parse (structuralDemands).
	if _, isIface := c.reg.Lookup(ifaceName).(*InterfaceType); !isIface && rec.IfaceOrigin == OriginUnresolved {
		return
	}
	matches := func(t Type) bool {
		return sameExistential(t, it)
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
	} else if arg, ok := c.demandSubject(func(t Type) bool { return t != nil && mentionsExistential(t, it) }); ok {
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
	article := ArticleFor(it.String())
	if verb == "holds" {
		article = "values of"
	}
	shown := shownBeside(ifaceIdentity{Origin: rec.IfaceOrigin, Name: ifaceName}, members)
	msg := fmt.Sprintf("%s %s %s `%s`, and %s does not require %s of its implementors", subject, verb, article, it.String(), it.String(), shown)
	if ifaceName == "Comparable" {
		// Equatable and Hashable hold of every interface value; an order
		// does not, and this says why.
		msg += ": values of different types have no order"
	}
	if c.alreadyReported(pos.Line, pos.Col, msg) {
		return
	}
	help := c.existentialDemandHelp(it, ifaceName, param, rec.Kind == RecordingKindOrderingOperator || rec.Kind == RecordingKindOperator || ifaceName == "Comparable")
	if rec.Kind == RecordingKindTypedLiteralSlot {
		// A slot cannot introduce a type parameter.
		help = fmt.Sprintf("a slot's value must have a type that implements %s", ifaceName)
		if isStdIterType(members[0]) {
			help = "an `Iter` is a lazy sequence; collect it with `Iter.to_list()` first"
		}
	}
	c.report(TypeError{Line: pos.Line, Col: pos.Col, Message: msg}.WithHint(help))
}

// existentialDemandHelp suggests a type parameter bounded by both
// interfaces in place of the interface type. An operator's operands must be
// one type, as are the values an order compares, so for either every
// parameter of that type becomes T.
func (c *checker) existentialDemandHelp(it Type, ifaceName, param string, operator bool) string {
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
func (c *checker) sigWithTypeParamFor(it Type, param string, all bool) (string, bool) {
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
		if ok && len(pt.TypeArgs) > 0 {
			return false
		}
		return sameExistential(t, it)
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
		ps[i] = paramHead(p) + ": " + ty
	}
	sig := "fn " + fn.Name + "<" + strings.Join(tps, ", ") + ">(" + strings.Join(ps, ", ") + ")"
	if ft.Return != nil && !isUnitLike(ft.Return) {
		sig += " -> " + typeString(ft.Return)
	}
	return sig, true
}

// A use of an interface's function `I.f`, the call `I.f(...)` or an
// operator that goes through it (`a < b` is `Comparable.compare(a, b)`),
// picks the implementor from its `self` arguments, or from where its result
// goes when `self` is only in the result. A value of interface type I, or of
// a conjunction holding I, names no implementor, so on one the function can
// run only when nothing else in its signature needs to be that
// implementor's own type: a `self` result, a second `self` argument, or a
// parameter holding `self` values would each need it. A function with no
// `self` parameter, called through I with nothing naming the implementor,
// has none to run. Inside one of I's own defaults the implementor is the one
// the default runs for. `==` and `!=` go through Equatable, which every
// interface value meets by comparing the held values (existentialSatisfies),
// so they are not such a use.

// InterfaceValueSelfCode tags the error for a use of an interface's
// function on an interface value where the function needs the
// implementor's own type beyond its receiver (checkExistentialSelfUse).
// Args holds the function, `Comparable.compare`, and for an operator the
// operator.
const InterfaceValueSelfCode = "interface-value-self"

// checkExistentialSelfCall reports an interface-qualified call of fa on
// iface that no implementor can answer. subs is the call's solved type
// parameters; subjects are its argument expressions.
func (c *checker) checkExistentialSelfCall(fa *ast.FieldAccess, iface *InterfaceType, subs map[*TypeParam_]Type, subjects []ast.Node, line, col int) {
	if fa == nil || fa.Field == nil || iface == nil || iface.SelfParam == nil {
		return
	}
	concrete, solved := subs[iface.SelfParam]
	if solved {
		concrete = resolveTypeVar(concrete)
		if _, open := concrete.(*TypeVar); open {
			solved = false
		}
	}
	c.checkExistentialSelfUse(iface, fa.Field.Name, "", concrete, solved, subjects, line, col)
}

// checkExistentialOrdering reports the ordering n, whose compared operand
// has type held, when held is an interface value: `a < b` is a use of
// `Comparable.compare`, whose two `self` arguments no interface value can
// answer (checkExistentialSelfUse). An interface value whose interfaces
// leave out Comparable is the demand checkExistentialDemand reports.
func (c *checker) checkExistentialOrdering(n *ast.Binary, held Type) {
	if !isInterfaceValue(held) {
		return
	}
	ifaceName, method, _ := operatorIface(n.Op)
	for _, m := range InterfaceMembers(resolveTypeVar(ResolveClosed(held))) {
		if m.Name == ifaceName && (m.Origin == "" || m.Origin == deriveProtocolHomeModule[ifaceName]) {
			c.checkExistentialSelfUse(m, method, n.Op, held, true, []ast.Node{n.Left, n.Right}, n.Line, n.Col)
			return
		}
	}
}

// checkExistentialSelfUse reports a use of the function method of iface,
// whose `self` the use solved to concrete (solved is false where it did
// not), that no implementor can answer: the call `I.f(...)` when op is "",
// else the operator op. subjects are the use's argument expressions.
func (c *checker) checkExistentialSelfUse(iface *InterfaceType, method, op string, concrete Type, solved bool, subjects []ast.Node, line, col int) {
	if iface == nil || iface.SelfParam == nil {
		return
	}
	sig := interfaceMethodSig(iface, method)
	if sig == nil {
		return
	}
	existential := false
	if solved && isInterfaceValue(concrete) {
		concrete = resolveTypeVar(ResolveClosed(concrete))
		for _, m := range InterfaceMembers(concrete) {
			existential = existential || m.Name == iface.Name
		}
	}
	use := selfUseOf(sig, iface.SelfParam)
	callee := iface.Name + "." + method
	inOwnDefault := c.defaultSelf == iface.SelfParam && c.currentInterface == iface.Name
	if len(use.at) == 0 {
		if (solved && !existential) || (!solved && inOwnDefault) {
			return
		}
		msg := fmt.Sprintf("`%s` has no `self` parameter, so this call does not say which implementor of %s runs", callee, iface.Name)
		c.reportOnce(line, col, msg, fmt.Sprintf("call it on the implementing type (`Type.%s(...)`), or on a type parameter bounded by %s (`T.%s(...)` with `where T: %s`)", method, iface.Name, method, iface.Name))
		return
	}
	if !existential || !use.needsImplementor() {
		return
	}
	subject, param := "a value", ""
	matches := func(t Type) bool {
		return sameExistential(t, concrete)
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
	// The value's type is named by its interface, or by the conjunction
	// that holds the interface; the bound is the interface at the value's
	// own arguments (`Scale<Int>` for an `s: Scale<Int>`).
	held, bound := iface.Name, concrete.String()
	if _, conj := concrete.(*ConjunctionType); conj {
		held = bound
	}
	a := ArticleFor(held)
	lead, args := fmt.Sprintf("`%s` cannot be called on", callee), []string{callee}
	selfArgs := ""
	if op != "" {
		lead, args = fmt.Sprintf("`%s` cannot compare", op), append(args, op)
		selfArgs = "its two operands"
	}
	msg := fmt.Sprintf("%s %s, %s `%s`: %s", lead, subject, a, held, use.why(held, selfArgs))
	help := fmt.Sprintf("take a type parameter bounded by %s instead, so the implementor is known: `where T: %s`", held, bound)
	if sigText, ok := c.sigWithTypeParamFor(concrete, param, len(use.at) > 1); ok {
		help = fmt.Sprintf("take a type parameter bounded by %s instead, so the implementor is known: `%s where T: %s`", held, sigText, held)
	}
	if c.alreadyReported(line, col, msg) {
		return
	}
	c.report(TypeError{Line: line, Col: col, Message: msg}.WithHint(help).coded(InterfaceValueSelfCode, args...))
}

// alreadyReported reports whether the checker holds the error msg at
// line:col. One site can reach the same demand twice (an operator and the
// container impl it reaches), and the second report is the first again:
// the same builder wrote both messages, so this compares an error with
// itself and names no wording.
func (c *checker) alreadyReported(line, col int, msg string) bool {
	for _, e := range c.errors {
		if e.Line == line && e.Col == col && e.Message == msg {
			return true
		}
	}
	return false
}

// reportOnce reports msg with hint at line:col unless the same message is
// already reported there.
func (c *checker) reportOnce(line, col int, msg, hint string) {
	if c.alreadyReported(line, col, msg) {
		return
	}
	c.report(TypeError{Line: line, Col: col, Message: msg}.WithHint(hint))
}

// sameExistential reports whether t is the interface value type it, an
// interface or a conjunction, and not a default's `self`.
func sameExistential(t, it Type) bool {
	t = resolveTypeVar(t)
	if at, ok := t.(*InterfaceType); ok {
		bt, ok := it.(*InterfaceType)
		return ok && !at.DefaultSelf && at.Name == bt.Name
	}
	_, conj := t.(*ConjunctionType)
	return conj && TypesEqual(t, it)
}

// mentionsExistential reports whether t is or holds the interface value
// type it.
func mentionsExistential(t, it Type) bool {
	if sameExistential(t, it) {
		return true
	}
	for _, a := range typeChildren(resolveTypeVar(t)) {
		if mentionsExistential(a, it) {
			return true
		}
	}
	return false
}

// selfUse is how a function of an interface uses the interface's `self`
// (spec §12 *Interface values*): the positions of its `self` parameters,
// the name of a parameter that holds `self` values in another type ("_"
// when it has none, "" when no parameter does), and whether its result
// holds `self`.
type selfUse struct {
	at          []int
	nested      string
	returnsSelf bool
}

// selfUseOf is how sig, a function of the interface whose `self` is self,
// uses it.
func selfUseOf(sig *MethodSig, self *TypeParam_) selfUse {
	var u selfUse
	for i, p := range sig.Params {
		if tp, ok := p.(*TypeParam_); ok && tp == self {
			u.at = append(u.at, i)
			continue
		}
		if u.nested == "" && mentionsTypeParam(p, self) {
			u.nested = "_"
			if i < len(sig.ParamNames) && sig.ParamNames[i] != "" {
				u.nested = sig.ParamNames[i]
			}
		}
	}
	u.returnsSelf = sig.Return != nil && mentionsTypeParam(sig.Return, self)
	return u
}

// needsImplementor reports whether a function that uses `self` as u does
// needs the implementor's own type beyond its one receiver, so that it
// cannot run on an interface value, which names no implementor: a `self`
// result, a second `self` argument, a parameter holding `self` values, or
// no `self` parameter at all.
func (u selfUse) needsImplementor() bool {
	return u.returnsSelf || len(u.at) != 1 || u.nested != ""
}

// why says why a function that uses `self` as u cannot run on a value of
// the interface type held. selfArgs names its `self` arguments where the
// use names them its own way (an operator's "its two operands"), "" for
// "its N `self` arguments".
func (u selfUse) why(held, selfArgs string) string {
	a := ArticleFor(held)
	if selfArgs == "" {
		selfArgs = fmt.Sprintf("its %d `self` arguments", len(u.at))
	}
	switch {
	case u.returnsSelf:
		return fmt.Sprintf("its result is `self`, the implementor's own type, and %s `%s` value does not say which implementor it holds", a, held)
	case len(u.at) > 1:
		return fmt.Sprintf("%s must be one implementing type, and `%s` values may hold different ones", selfArgs, held)
	case len(u.at) == 0:
		return fmt.Sprintf("it has no `self` parameter, and %s `%s` value does not say which implementor runs it", a, held)
	}
	return fmt.Sprintf("its parameter `%s` holds `self` values, which must be the implementor's own type, and %s `%s` value does not say which implementor it holds", u.nested, a, held)
}

// existentialBlocker is the first function of iface no value of an
// interface type holding iface can call (selfUse.needsImplementor), and
// false when every one can.
func existentialBlocker(iface *InterfaceType) (string, selfUse, bool) {
	if iface == nil || iface.SelfParam == nil {
		return "", selfUse{}, false
	}
	for i := range iface.Methods {
		if u := selfUseOf(&iface.Methods[i], iface.SelfParam); u.needsImplementor() {
			return iface.Methods[i].Name, u, true
		}
	}
	return "", selfUse{}, false
}

// existentialMeets reports whether the interface type it (an interface,
// or a conjunction of them) meets the bound `T: iface`, which is the one
// rule for an interface type at a bound (spec §12 *Interface values*). A
// generic body may call any of the bound's functions on T's values, so it
// meets the bound when the language defines each of them across
// implementors (existentialSatisfies: Debug, and Equatable and Hashable,
// which compare and hash the held values by their run-time types), or
// when it holds iface and no function of iface needs the implementor's own
// type beyond its receiver (existentialBlocker), the rule a call of one of
// them on an interface value is held to (checkExistentialSelfUse).
// Comparable is neither: `compare` takes two `self` values, and values of
// different types have no order.
func existentialMeets(it Type, iface *InterfaceType) bool {
	members := InterfaceMembers(it)
	if len(members) == 0 {
		return false
	}
	universal := true
	var held *InterfaceType
	for _, m := range members {
		if sameNominalIdentity(m.Origin, m.Name, iface.Origin, iface.Name) {
			held = m
		}
		universal = universal && existentialSatisfies(m, iface.Name, iface.Origin)
	}
	if universal {
		return true
	}
	if held == nil {
		return false
	}
	_, _, blocked := existentialBlocker(held)
	return !blocked
}

// checkExistentialBound reports the bound `T: bound` that rec makes of
// it, the type T is instantiated at, when it is an interface type that
// holds bound and does not meet it (existentialMeets), at the call or the
// operator that instantiates T, naming the function of bound that T's
// values could not run. An interface type that does not hold bound is
// checkExistentialDemand's to report. The checker's two bound checks ask
// it: a generic call's bounds (a RecordingKindGenericBoundCheck demand)
// and the bounds of the impl a use selects
// (recordImplReceiverBoundConformances).
func (c *checker) checkExistentialBound(it Type, bound *InterfaceType, rec Recording) {
	if c.ruleWitness || !isInterfaceValue(it) {
		return
	}
	it = resolveTypeVar(ResolveClosed(it))
	var held *InterfaceType
	for _, m := range InterfaceMembers(it) {
		if sameNominalIdentity(m.Origin, m.Name, bound.Origin, bound.Name) {
			held = m
		}
	}
	if held == nil || existentialMeets(it, held) {
		return
	}
	method, use, _ := existentialBlocker(held)
	shown := it.String()
	callee := held.Name + "." + method
	msg := fmt.Sprintf("`%s` does not meet the bound `%s`, since `%s` cannot be called on %s `%s` value: %s", shown, held.String(), callee, ArticleFor(shown), shown, use.why(shown, ""))
	help := fmt.Sprintf("take the values' own type, or a type parameter bounded by %s, so the implementor is known", held.Name)
	if held.Name == "Comparable" {
		help = "order values of one type: take their own type, or a type parameter bounded by Comparable, or order values of different types by a key of one type with `Iter.sort_by`"
	}
	if c.alreadyReported(rec.Pos.Line, rec.Pos.Col, msg) {
		return
	}
	c.report(TypeError{Line: rec.Pos.Line, Col: rec.Pos.Col, Message: msg}.WithHint(help).coded(InterfaceValueSelfCode, callee))
}
