package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
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
// `g.implsByIface` is `map[iface]map[recvKind]*implDef`, keyed by RECEIVER
// KIND ALONE. There, `byRecv[d.recv] = d` would collapse three `Add` impls for
// `Day` into one slot, and resolveImplDef's duplicate rule would refuse all
// three.
//
// # A SECOND INDEX, NOT A WIDER KEY, because of one consultation site
//
// Widening `implsByIface`'s key to carry the interface's type arguments changes
// a map that `foreignIfaceCall`, `interfaceCall`, `bindsImpl`, `noEquatableImpl`,
// `debugerased.go`, `debuginspect.go`, `assertable.go` and `genericimpl.go` all
// read. Most of those would merely need a mechanical edit. `noEquatableImpl` is
// the one that would not, and it decides the shape of this file:
//
//	if g.implsByIface["Equatable"][k] != nil { return false }
//
// It answers "this builder has ESTABLISHED that no impl Equatable exists", and a
// TRUE answer sends `==` to the STRUCTURAL comparator. Under a widened key that
// lookup becomes "no row for (Equatable, k, <which rhs?>)", and the honest
// rewrite is an EXISTENTIAL over the rhs axis — "no row for any rhs". A
// mechanical rewrite that kept the shape of a single lookup would answer TRUE
// for a type that HAS an impl, and `==` would silently take the structural
// fallback: a WRONG ANSWER, not a refusal, on a comparison that lowers.
// Eight sites, and the failure at one of them is invisible to the corpus unless
// a fixture happens to compare two values of a type whose Equatable impl exists.
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
// is resolved through the stdlib index, and `stdlibOperator` selects on
// `stdlibImplOf(key, left.k, left.k, right.k)`, which carries the right-hand
// type. This file is the user-side mirror of `g.std.byIface`, not a new
// mechanism.
//
// # NO `*ifaceDef`, AND NO TABLE
//
// `Add`/`Subtract`/`Multiply`/`Divide` are excluded from `stdIfaceSpecs`
// because `stdIfaceSpec.matches` returns false on `len(decl.TypeParams) > 0`, so
// `stdIfaceNamed` answers nothing for them and there is no declaration to
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
// The interface must be STD's. `operIfaceAnchors` mirrors `stdIfaceAnchors`
// exactly: `fa.ModuleScope.Lookup(name)` must yield an IMPORT symbol whose
// module path is the declaring std module (`stdImportModuleOf` reports the
// ORIGIN module even through the prelude's re-export), and the resolved
// declaration must have the shape `operIfaceSpec` records. A user's own
// `interface Add<R, O>` is not an import, so it anchors nothing here and takes
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
}

// operIfaceSpecs is the whole set: the four arithmetic operators std declares an
// interface for.
//
// `%` is absent because std declares no Modulo interface, which is the same
// reason `stdOperatorIfaces` omits it — `%` on a named type has nothing to
// dispatch to and keeps refusing.
//
// APPEND-ONLY, on stdEnumSpecs' and opaqueSpecs' footing: `operIfaceIndex`
// hands out indices into this slice and nothing else keys on position, but an
// insertion would still renumber every cached index within one run.
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
	{module: "std/steppable", nomi: "Steppable", method: "step_by", maybeSelf: true},
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
// and the RIGHT-HAND type — the axis `implsByIface` drops.
//
// The `Out` type is deliberately NOT in the key. Two impls of `Add<Days, X>` for
// one receiver differing only in their output would be a coherence error the
// analyzer rejects, and keying on the output as well would admit them silently
// while leaving the CALL site — which knows the operands and not the result — no
// way to choose. So a collision on (iface, recv, rhs) is refused as a duplicate
// exactly as `implsByIface` refuses a collision on (iface, recv).
type operKey struct {
	iface string
	recv  kind
	rhs   kind
}

// operIfaceAnchoredIn reports whether `name` in this module is the stdlib
// operator interface of that name, with the declared shape.
//
// A free function taking the analysis rather than a gen method, on
// `stdIfaceAnchors`' and `structIfaceAnchored`' footing: one implementation of
// "is this the std interface", so there is nothing for a second to drift from.
func operIfaceAnchoredIn(fa *analysis.FileAnalysis, name string) (*operIfaceSpec, bool) {
	s, known := operIfaceByName[name]
	if !known || fa == nil || fa.ModuleScope == nil {
		return nil, false
	}
	sym := fa.ModuleScope.Lookup(name)
	if sym == nil {
		return nil, false
	}
	if mod, imported := stdImportModuleOf(sym, stdImporterOf(fa, sym)); !imported || mod != s.module {
		// Not reached through an import of the DECLARING std module: a local
		// declaration, a sibling's, or a dependency's. No anchor, and the
		// ordinary paths report. This is the identity check and it is the whole
		// of it — see stdiface.go's file comment.
		return nil, false
	}
	res := sym
	for res.Resolved != nil {
		res = res.Resolved
	}
	decl, isDecl := res.Node.(*ast.InterfaceDef)
	if !isDecl || !s.matchesDecl(decl) {
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
	if s.maybeSelf {
		typeParams = 1
	}
	switch {
	case decl.Name != s.nomi, !decl.Public:
		return false
	case len(decl.TypeParams) != typeParams, len(decl.WhereClauses) > 0:
		return false
	// An attached `//!` test is deliberately NOT a disqualifier — see
	// stdStructSpec.matches for why.
	case len(decl.Methods) != 1:
		return false
	}
	m := &decl.Methods[0]
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

// operIfaceAnchored is operIfaceAnchoredIn against this gen's module, memoized.
//
// Memoized because resolveImplDef asks once per impl block and the lookup walks
// a resolution chain; the answer is a property of the module, so one answer per
// gen is the whole of what is needed.
func (g *gen) operIfaceAnchored(name string) (*operIfaceSpec, bool) {
	if g.operAnchors == nil {
		g.operAnchors = map[string]*operIfaceSpec{}
	}
	if s, cached := g.operAnchors[name]; cached {
		return s, s != nil
	}
	s, ok := operIfaceAnchoredIn(g.fa, name)
	if !ok {
		s = nil
	}
	g.operAnchors[name] = s
	return s, s != nil
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
// Declines, leaving `generic interface impl` to report, for
// every header that is not one of the four anchored operator interfaces at
// exactly two type arguments, both of which resolve to kinds this builder
// represents.
func (g *gen) admitOperImpl(d *implDef, ib *ast.ImplBlock, name string) bool {
	s, anchored := g.operIfaceAnchored(name)
	if !anchored {
		return false
	}
	args, explicit := ifaceHeaderArgs(ib.Interface)
	if !explicit || (s.maybeSelf && len(args) != 1) || (!s.maybeSelf && len(args) != 2) {
		return false
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
// selectable: `operImplFor` reads `it.params` to choose between rival impls and
// `directImplCallAt` reads `it.result` as the operator's type.
//
// Checked against the HEADER's type arguments rather than against a resolved
// interface, which is the substitution the missing `*ifaceDef` would have
// performed: `self` is the receiver, `Rhs` is the first type argument and `Out`
// is the second.
func (g *gen) operShapeRefusal(d *implDef) (string, string) {
	label := d.oper.nomi + " for " + d.recv.nomi()
	if d.oper.maybeSelf {
		// `Maybe<self>`: the receiver resolved after admitOperImpl ran.
		a, anchored := g.preludeByName["Maybe"]
		if !anchored {
			return "operator impl shape", label + " needs the prelude Maybe"
		}
		d.operOut = g.preludeInstance(a, []kind{d.recv})
	}
	it := d.items[d.oper.method]
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
	key := operKey{iface: d.ifaceName, recv: d.recv, rhs: d.operRhs}
	if prior, dup := g.operImpls[key]; dup {
		prior.lowerable, prior.why, prior.whyDetail = false, "duplicate impl block", d.operLabel()
		d.lowerable, d.why, d.whyDetail = false, "duplicate impl block", d.operLabel()
		return
	}
	g.operImpls[key] = d
	g.operOrder = append(g.operOrder, d)
}

// operLabel names an operator impl the way its source spells it, which is what
// makes a duplicate report distinguish the three `Add` impls for one receiver.
func (d *implDef) operLabel() string {
	if d.oper.maybeSelf {
		return fmt.Sprintf("%s<%s> for %s", d.ifaceName, d.operRhs.nomi(), d.recv.nomi())
	}
	return fmt.Sprintf("%s<%s, %s> for %s",
		d.ifaceName, d.operRhs.nomi(), d.operOut.nomi(), d.recv.nomi())
}

// operImplFor selects the operator impl of `iface` for a receiver whose
// declared parameters fit `args`, reporting rivals separately from a miss, and
// hands back the IR declaration the selection chose.
//
// The scoring is `internal/ir`'s: the loop is `ir.Table.SelectOverload` and
// the acceptance relation is `ir.Table.Widens`. What stays here is the
// candidate FILTER, which is this package's knowledge of which impl blocks are
// operator impls at all. See irtable.go.
//
// The tiering is `foreignIfaceCall`'s: `argsFit` is a
// boolean and `Duration + Int` versus `Duration + Duration` needs the DEGREE,
// so an EXACT fit beats a WIDENED one and two fits at one tier are ambiguous.
// It is expressed once, in `ir.Fit`, which is what stops the operator route and
// the qualified-call route from coming to disagree about which impl `Day + Days`
// selects.
//
// THE THIRD RESULT is the chosen declaration as the IR names it. Only
// `userOperator` reads it, because only the operator SYNTAX is in the `arith`
// class and therefore only it builds an `ir.Arith`; `operCall` and
// `operTypeQualifiedCall` are the `call` class reached through a spelling and
// discard it. Returned from here rather than re-derived there so one selection
// produces one declaration identity.
//
// Every candidate scanned here is an operator impl, and `g.operImpls` holds
// nothing else, so this route cannot change the answer of a call site that
// resolves through `implsByIface`.
func (g *gen) operImplFor(iface string, recv kind, method string, args []kind) (chosen *implItem, decl *ir.Decl, rival bool) {
	return g.irSelectOperImpl(iface, recv, method, args)
}
