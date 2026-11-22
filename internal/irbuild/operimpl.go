package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Operator-interface impls: `impl Add<Days, Day> for Day`.
//
// # Why operator impls need their own key
//
// Nomi's operator overloading is per-operation and ASYMMETRIC — Rust's
// `Add<Rhs, Output>` — and the point of the asymmetry is DIMENSIONAL
// CORRECTNESS: `Duration` implements `Add<Duration> -> Duration`,
// `Multiply<Int> -> Duration` and `Divide<Duration> -> Int`, and pointedly NOT
// `Multiply<Duration>`, because time squared is not a Duration. That property is
// exactly what requires SEVERAL `Add` impls for one receiver, and the analyzer
// permits them: `std/calendar` ships TWELVE `Add` impls for `NaiveDateTime`,
// four for `Date` and four for `OffsetDateTime`.
//
// `g.implsByIface` is `map[string]map[kind]*implDef`, keyed by RECEIVER
// KIND ALONE. There, `byRecv[d.recv] = d` would collapse three `Add` impls for
// `Day` into one slot, and resolveImplDef's duplicate rule would refuse all
// three.
//
// # A SECOND INDEX, NOT A WIDER KEY
//
// Widening `implsByIface`'s key to carry the interface's type arguments changes
// a map that `bindsImpl`, `debugerased.go`, `debuginspect.go`,
// `irinterfacecall.go`, `irqualcall.go` and `genericimpl.go` all read, each
// asking for one receiver's impl of an interface. Under a widened key each
// lookup becomes "a row for (iface, k, <which rhs?>)", and the honest rewrite
// is an EXISTENTIAL over the rhs axis. A mechanical rewrite that kept the shape
// of a single lookup would answer "no impl" for a type that HAS one, at a site
// a fixture may never reach.
//
// A SECOND INDEX cannot produce that failure, and the reason is structural
// rather than careful: no operator impl is registered in `implsByIface` at all,
// so every lookup in it is unaffected by operator impls. The claim is
// checkable in one line — `resolveImplDef` returns before `byRecv[d.recv] = d`
// for an operator impl — and TestOperImpl_OperatorImplsNeverEnterImplsByIface
// asserts it.
//
// # ONE RULE, TWO INDICES
//
// `std/calendar`'s twelve `Add` impls never enter `implsByIface`. A stdlib impl
// is resolved through the stdlib index, which carries the right-hand type.
// This file is the user-side mirror of `g.std.byIface`, not a new mechanism.
// An operator reaches the impl its record names (recordedOperatorImpl).
//
// # NO `*ifaceDef`, AND NO TABLE
//
// `Add`/`Subtract`/`Multiply`/`Divide` are excluded from `stdIfaceSpecs`
// because `stdIfaceSpec.matches` returns false on `len(decl.TypeParams) > 0`, so
// std's shared defs hold none of them and there is no declaration to
// instantiate. An operator impl therefore carries `iface == nil`, and that is
// sufficient rather than a compromise: dispatch is from a STATICALLY KNOWN left
// operand at every one of the three call shapes below, `bindImpl` returns
// immediately for a nil iface so no rt table is bound, and each of the four
// interfaces declares exactly ONE method with no default body — so there is
// nothing an erased receiver could need and nothing to inherit.
//
// The cost of a nil `iface` is that `implItemSig`'s "is this method declared on
// the interface" check does not run. `operShapeRefusal` below is that check,
// written against the operator interface's own signature rather than against a
// resolved `*ifaceDef`: one method, the declared name, `self` at argument 0, the
// header's `Rhs` at argument 1 and the header's `Out` as the result. A block
// whose shape disagrees is REFUSED by name rather than called against a
// signature nobody wrote — stdiface.go's `matches` discipline, applied to the
// one family that has no spec row.
//
// # IDENTITY
//
// The interface must be STD's: the one the checker recorded for the impl
// block (implBlockRecord) is the spec's (module, name), and std declares it
// in the shape `operIfaceSpec` records (`operIfaceOf`). A user's own
// `interface Add<R, O>` is another interface, so it is no spec's and takes
// `resolveImplDef`'s generic-interface-template route instead.

// operIfaceSpec is one stdlib OPERATOR interface: `pub interface Add<Rhs, Out>`
// with its single method.
//
// A table of its own rather than a row in `stdIfaceSpecs`, and that is a
// requirement rather than a preference: a `stdIfaceSpec` row IS an rt dispatch
// table, `stdIfaceSpec.matches` rejects `len(decl.TypeParams) > 0` outright, and
// these four interfaces need no table at all. Adding a row would mean relaxing
// that rejection for every family that reads it.
type operIfaceSpec struct {
	// module is the DECLARING module's import path — the identity half
	// analysis.InterfaceType's missing Origin would otherwise supply.
	module string
	// nomi is the interface name as written.
	nomi string
	// method is the single function the interface declares.
	method string
	// op is the surface operator that dispatches to it, or "" for an
	// interface no operator spells (Steppable).
	op string
	// maybeSelf marks an interface with ONE type parameter whose method
	// returns `Maybe<self>`: `Steppable<S>`'s `step_by(value: self, by: S):
	// Maybe<self>`. The arithmetic four have two, `Rhs` and `Out`, and return
	// `Out`.
	maybeSelf bool
	// unary marks an interface with ONE type parameter, `Out`, whose method
	// takes `self` alone and returns it: `BitwiseNot<Out>`'s
	// `bitwise_not(value: self) -> Out`, the prefix `~~~`.
	unary bool
}

// operIfaceSpecs is the whole set: the four arithmetic operators std declares an
// interface for.
//
// APPEND-ONLY, on stdEnumSpecs' and opaqueSpecs' footing: `operIfaceByOp` and
// `operIfaceByName` hold pointers into this slice and nothing keys on
// position.
//
// `Steppable` is here for the same reasons as the four, though no operator
// spells it: one method with no default, every call shape dispatches on a
// statically known receiver (`Steppable.step_by(x, by)`, `Meters.step_by(x,
// by)`, and `Range.step_by`, which hands the body to the VM as a function
// value), and a receiver may implement it at several step types.
var operIfaceSpecs = []operIfaceSpec{
	{module: "std/add", nomi: "Add", method: "add", op: "+"},
	{module: "std/subtract", nomi: "Subtract", method: "subtract", op: "-"},
	{module: "std/multiply", nomi: "Multiply", method: "multiply", op: "*"},
	{module: "std/divide", nomi: "Divide", method: "divide", op: "/"},
	{module: "std/remainder", nomi: "Remainder", method: "remainder", op: "%"},
	{module: "std/steppable", nomi: "Steppable", method: "step_by", maybeSelf: true},
	{module: "std/bitwise", nomi: "BitwiseAnd", method: "bitwise_and", op: "&&&"},
	{module: "std/bitwise", nomi: "BitwiseOr", method: "bitwise_or", op: "|||"},
	{module: "std/bitwise", nomi: "BitwiseXor", method: "bitwise_xor", op: "^^^"},
	{module: "std/bitwise", nomi: "ShiftLeft", method: "shift_left", op: "<<<"},
	{module: "std/bitwise", nomi: "ShiftRight", method: "shift_right", op: ">>>"},
	{module: "std/bitwise", nomi: "BitwiseNot", method: "bitwise_not", unary: true},
}

// operIfaceByOp indexes operIfaceSpecs by the surface operator.
var operIfaceByOp = func() map[string]*operIfaceSpec {
	out := make(map[string]*operIfaceSpec, len(operIfaceSpecs))
	for i := range operIfaceSpecs {
		if operIfaceSpecs[i].op != "" {
			out[operIfaceSpecs[i].op] = &operIfaceSpecs[i]
		}
	}
	return out
}()

// operIfaceByName indexes operIfaceSpecs by the interface's Nomi name.
var operIfaceByName = func() map[string]*operIfaceSpec {
	out := make(map[string]*operIfaceSpec, len(operIfaceSpecs))
	for i := range operIfaceSpecs {
		out[operIfaceSpecs[i].nomi] = &operIfaceSpecs[i]
	}
	return out
}()

// operKey identifies one operator-interface impl: the interface, the receiver,
// and its two type arguments, the right-hand type (the axis `implsByIface`
// drops) and the output. Two impls of `Add<Days, X>` for one receiver that
// differ only in their output are two impls; the front end chooses between
// them by the type the operator's result is expected to have, and the
// operator's record names the one it chose.
type operKey struct {
	iface string
	recv  kind
	rhs   kind
	out   kind
}

// operIfaceOf is the operator interface spec of std interface id, when std
// declares it in the spec's shape (matchesDecl). The zero id, a program's
// interface, is none.
func (g *gen) operIfaceOf(id analysis.StdType) (*operIfaceSpec, bool) {
	s, known := operIfaceByName[id.Name]
	if !known || s.module != id.Module {
		return nil, false
	}
	decl := stdInterfaceDecl(g.fa, id)
	if decl == nil || !s.matchesDecl(decl) {
		return nil, false
	}
	return s, true
}

// matchesDecl reports whether decl is the declaration this spec describes.
//
// Everything checked here decides whether the CALL SHAPES below may assume
// `fn <method>(lhs: self, rhs: Rhs): Out`. A std edit that renamed the method,
// added a second one, gave it a default body, made it host-backed, moved the
// self position or changed the type-parameter count produces NO anchor, and
// every user impl of it then refuses under `generic interface impl`, rather
// than lowering against a signature std does not have.
//
// That is the one failure this file's identity machinery cannot otherwise
// detect: a shape drift is a wrong answer, not a compile error. Same reasoning
// as stdIfaceSpec.matches, and the parameter TYPES are checked at the impl
// instead of here, because `self`, `Rhs` and `Out` are exactly what the impl
// block's header instantiates. See operShapeRefusal.
func (s *operIfaceSpec) matchesDecl(decl *ast.InterfaceDef) bool {
	typeParams := 2
	if s.maybeSelf || s.unary {
		typeParams = 1
	}
	switch {
	case decl.Name != s.nomi, !decl.Public:
		return false
	case len(decl.TypeParams) != typeParams, len(decl.WhereClauses) > 0:
		return false
	// An attached `//!` test is deliberately NOT a disqualifier — see
	// stdStructSpec.mismatch for why.
	case len(decl.Methods) != 1:
		return false
	}
	m := &decl.Methods[0]
	if s.unary {
		return m.Name == s.method && m.Body == nil && len(m.TypeParams) == 0 && len(m.WhereClauses) == 0 &&
			len(m.Params) == 1 && isSelfTypeExpr(m.Params[0].TypeAnnotation) &&
			isNamedTypeExpr(m.ReturnTypeExpr, decl.TypeParams[0].Name)
	}
	switch {
	case m.Name != s.method, m.Body != nil:
		return false
	case len(m.TypeParams) > 0, len(m.WhereClauses) > 0:
		return false
	case len(m.Params) != 2:
		return false
	case !isSelfTypeExpr(m.Params[0].TypeAnnotation):
		return false
	case !isNamedTypeExpr(m.Params[1].TypeAnnotation, decl.TypeParams[0].Name):
		return false
	case s.maybeSelf:
		return isMaybeSelfTypeExpr(m.ReturnTypeExpr)
	case !isNamedTypeExpr(m.ReturnTypeExpr, decl.TypeParams[1].Name):
		return false
	}
	return true
}

// isMaybeSelfTypeExpr reports whether te is the annotation `Maybe<self>`.
func isMaybeSelfTypeExpr(te ast.TypeExpr) bool {
	gt, generic := te.(*ast.GenericType)
	return generic && gt.Name == "Maybe" && len(gt.Params) == 1 && isSelfTypeExpr(gt.Params[0])
}

// isSelfTypeExpr reports whether te is the annotation `self`.
//
// `self` is its OWN node — `*ast.SelfType` — and not a `*ast.SimpleType` named
// "self". Checking for the latter would make `matchesDecl` return false for
// all four interfaces with no error anywhere: an identity check that fails
// CLOSED is fail-safe by design and therefore silent, which is why it has a
// positive control, TestOperImpl_TheFourInterfacesAnchor.
func isSelfTypeExpr(te ast.TypeExpr) bool {
	_, isSelf := te.(*ast.SelfType)
	return isSelf
}

// isNamedTypeExpr reports whether te is the bare type name `name`.
func isNamedTypeExpr(te ast.TypeExpr, name string) bool {
	st, simple := te.(*ast.SimpleType)
	return simple && st.Name == name
}

// admitOperImpl claims `impl Add<Days, Day> for Day` for this file's parallel
// index, reporting whether it did.
//
// Called from resolveImplDef at the one arm that otherwise refuses every user impl
// of a stdlib generic interface by name. It runs BEFORE the receiver and the
// items are resolved, because it only decides ROUTE: whether this block is an
// operator impl at all. The shape agreement is checked afterwards, by
// operShapeRefusal, since it compares the block's own parameter kinds against
// the header's type arguments.
//
// iface is the std interface the checker recorded for the block. Declines,
// leaving `generic interface impl` to report, for every header that is not
// one of the operator interfaces at
// exactly two type arguments, both of which resolve to kinds this builder
// represents.
func (g *gen) admitOperImpl(d *implDef, ib *ast.ImplBlock, name string, iface analysis.StdType) bool {
	s, anchored := g.operIfaceOf(iface)
	if !anchored {
		return false
	}
	args, explicit := ifaceHeaderArgs(ib.Interface)
	one := s.maybeSelf || s.unary
	if !explicit || (one && len(args) != 1) || (!one && len(args) != 2) {
		return false
	}
	if s.unary {
		// `BitwiseNot<Out>`: no right-hand operand. kindUnit stands in for
		// it in the index's key.
		out := g.typeOf(args[0])
		// An unrepresentable `Out` is reported by `generic interface impl`.
		// kindInvalid: lookup — no kind, no operator impl.
		if out == kindInvalid {
			return false
		}
		d.oper, d.operRhs, d.operOut, d.ifaceName = s, kindUnit, out, name
		return true
	}
	rhs := g.typeOf(args[0])
	// The header's `Out`, or for a maybeSelf interface `Maybe<self>`, which
	// names the receiver and is completed once the receiver resolves
	// (operShapeRefusal). kindUnit stands in until then.
	out := kindUnit
	if !s.maybeSelf {
		out = g.typeOf(args[1])
	}
	// An unrepresentable type argument is not an operator impl this builder can
	// serve, and `generic interface impl` reports it at the header. This is a
	// FENCE rather than a route: `Add` over an interface type argument and over
	// a function type argument both LOWER, so nothing in the corpus reaches it.
	// kindInvalid: lookup — a miss declines and `generic interface impl` reports.
	if rhs == kindInvalid || out == kindInvalid {
		return false
	}
	d.oper, d.operRhs, d.operOut, d.ifaceName = s, rhs, out, name
	return true
}

// operShapeRefusal is `implItemSig`'s "is this method declared on the
// interface" check for a block whose interface has no `*ifaceDef`.
//
// It returns the refusal, or two empty strings. Every clause is what one of the
// three call shapes below assumes, so a block that fails any of them must not be
// called: `userOperatorCall` reads `it.params` as the operands' kinds and
// `it.result` as the operator's type.
//
// Checked against the HEADER's type arguments rather than against a resolved
// interface, which is the substitution the missing `*ifaceDef` would have
// performed: `self` is the receiver, `Rhs` is the first type argument and `Out`
// is the second.
func (g *gen) operShapeRefusal(d *implDef) (string, string) {
	label := d.oper.nomi + " for " + d.recv.nomi()
	if d.oper.maybeSelf {
		// `Maybe<self>`: the receiver resolved after admitOperImpl ran.
		a, anchored := preludeSpecAnchor(preludeSpecNamed("Maybe"))
		if !anchored {
			return "operator impl shape", label + " needs the prelude Maybe"
		}
		d.operOut = g.preludeInstance(a, []kind{d.recv})
	}
	it := d.items[d.oper.method]
	if d.oper.unary && it != nil && len(d.order) == 1 {
		switch {
		case len(it.params) != 1:
			return "operator impl shape",
				fmt.Sprintf("%s.%s takes %d parameter(s), want 1", label, it.name, len(it.params))
		case it.params[0] != d.recv:
			return "operator impl shape",
				label + "." + it.name + " parameter 1 is " + it.params[0].nomi() + ", want " + d.recv.nomi()
		case it.result != d.operOut:
			return "operator impl shape",
				label + "." + it.name + " returns " + it.result.nomi() + ", want " + d.operOut.nomi()
		}
		return "", ""
	}
	switch {
	case len(d.order) != 1 || it == nil:
		// The interface declares exactly one method, so a block supplying a
		// second or a differently-named one is not an implementation of it.
		return "operator impl shape", label + " must declare exactly " + d.oper.method
	case len(it.params) != 2:
		return "operator impl shape",
			fmt.Sprintf("%s.%s takes %d parameter(s), want 2", label, it.name, len(it.params))
	case it.params[0] != d.recv:
		return "operator impl shape",
			label + "." + it.name + " parameter 1 is " + it.params[0].nomi() + ", want " + d.recv.nomi()
	case it.params[1] != d.operRhs:
		return "operator impl shape",
			label + "." + it.name + " parameter 2 is " + it.params[1].nomi() + ", want " + d.operRhs.nomi()
	case it.result != d.operOut:
		return "operator impl shape",
			label + "." + it.name + " returns " + it.result.nomi() + ", want " + d.operOut.nomi()
	}
	return "", ""
}

// registerOperImpl files d in the parallel index, refusing a genuine duplicate.
//
// The duplicate rule is `resolveImplDef`'s verbatim, one axis wider: two impls
// of one interface for one receiver AT ONE RIGHT-HAND TYPE is a coherence error
// the analyzer rejects, and refusing both rather than picking is the rule
// `rt.Method.Bind` holds at run time — never let ordering decide.
func (g *gen) registerOperImpl(d *implDef) {
	if g.operImpls == nil {
		g.operImpls = map[operKey]*implDef{}
	}
	key := operKey{iface: d.ifaceName, recv: d.recv, rhs: d.operRhs, out: d.operOut}
	if prior, dup := g.operImpls[key]; dup {
		prior.lowerable, prior.why, prior.whyDetail = false, "duplicate impl block", d.operLabel()
		d.lowerable, d.why, d.whyDetail = false, "duplicate impl block", d.operLabel()
		return
	}
	g.operImpls[key] = d
}

// operLabel names an operator impl the way its source spells it, which is what
// makes a duplicate report distinguish the three `Add` impls for one receiver.
func (d *implDef) operLabel() string {
	if d.oper.unary {
		return fmt.Sprintf("%s<%s> for %s", d.ifaceName, d.operOut.nomi(), d.recv.nomi())
	}
	if d.oper.maybeSelf {
		return fmt.Sprintf("%s<%s> for %s", d.ifaceName, d.operRhs.nomi(), d.recv.nomi())
	}
	return fmt.Sprintf("%s<%s, %s> for %s",
		d.ifaceName, d.operRhs.nomi(), d.operOut.nomi(), d.recv.nomi())
}
