package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// An impl block on a GENERIC receiver, registered and built PER INSTANCE.
//
// # Why per instance
//
// generictype.go monomorphizes a user generic struct to one *typeDef per
// instantiation. `g.implsByIface[iface][kind]` is keyed on the `*typeDef`
// POINTER, so an instance def is a NEW pointer, and an impl registered only on
// the generic receiver would leave the table EMPTY for every instance. That
// would be a WRONG ANSWER with no diagnostic: noEquatableImpl(k) would return
// TRUE ("the builder has ESTABLISHED that no impl Equatable exists") and `==`
// would take the STRUCTURAL fallback while a hand-written `impl Equatable for
// Box<T>` sat unused. displayCode and the ordering arms would miss identically.
//
// So this file registers each impl block ONCE PER INSTANCE, under the
// template's substitution frame, so `implsByIface["Describe"][Wrapped<Point>]`
// is a real entry and every consultation that reads the table gets the user's
// own impl.
//
// # A THIRD file rather than an extension of either neighbour
//
// genericmono.go monomorphizes a generic FUNCTION, whose instances are
// FUNCTIONS reached through funcDecl/directCall. generictype.go monomorphizes a
// generic TYPE, whose instances are TYPE DEFS reached through typeOf. This
// mechanism produces neither: it populates a DISPATCH TABLE, and it reads
// implDef, implsByIface and emitImpl, which are impl.go's. Folding it into either
// neighbour would put two headers' worth of reasoning under one.
//
// What the three genuinely share is `g.genericSubst`, and they already share it.
// That is the single insertion point typeOf's *ast.SimpleType arm consults, which
// is why `Wrapped<T>` in a parameter, `List<Maybe<T>>` in a return type and
// `(T) -> Bool` in a field are all covered at every depth without an arm each.
//
// # Inherent blocks
//
// An inherent `impl Span<T> { ... }` is registered per instance the same way,
// under the empty interface name, which is where an inherent block on a
// concrete type lives. `Span.first(s)` selects the instance its receiver
// operand is; `Span.new(1, 5)`, whose operands name no instance, selects the
// one the checker's solved signature names (templateCallInstance).
//
// # The universal Debug
//
// The front end's synthesized `impl Debug for Box<T>` is registered per
// instance like any other block. `Debug.inspect(b)`, `io.inspect(b)` and
// `dbg b` over a `Box<Int>` read `implsByIface["Debug"]` and call its body,
// as they do for a non-generic struct's.
//
// # Type-parameter dispatch falls out
//
// In `impl Greet for Wrapper<T> { fn hello(v: Wrapper<T>): String {
// T.hello(v.value) } }`, `T` outside an instantiation is an unresolved
// type-parameter name at the position the qualified-call path reads it. A body
// built only INSIDE an instantiation has no such position: the frame is pushed,
// `T` resolves to `Dog`, and the site is an ordinary concrete `Dog.hello(x)`.
// No arm here handles it.
//
// A DECLARATION-level bound (`struct Range<T> where T: Comparable`) is not
// checked here: a violating instantiation never reaches the builder, because
// the front end rejects it. See registerInstanceImpls.
//
// # What stays refused, and why
//
//   - A block-level `where` whose SUBJECT is not one of the template's own
//     parameters. There is no argument to discharge it from.
//   - An impl function with its own type parameters, a decorator, or no body.
//     implItemSig already reports each at the declaration's own position, so
//     admitting it here would lower past a refusal.
//
// genericImplTemplate is one impl block whose receiver names a template,
// paired with the reason it cannot be instantiated.
//
// Collected at template-collection time rather than at mint time because the
// scan is over `g.nodes` and is paid once, which is templateWall's own reason for
// carrying its answer on the template.
type genericImplTemplate struct {
	decl *ast.ImplBlock
	// why and whyDetail are the STATICALLY decidable reason no instance of this
	// block can be built -- a block-level `where`, a non-FuncDef item. Empty
	// when instantiation is admitted, and an argument-specific failure is then
	// reported by the ARGUMENT's own position rather than here.
	why       string
	whyDetail string
}

// collectGenericImpls fills tpl.impls, and returns the reason the TEMPLATE is
// walled when one of its blocks cannot be instantiated at any argument at all.
//
// The split between "walls the template" and "declines this instantiation" is
// the same one cascade.go draws everywhere else: a reason that does not depend on
// the type arguments belongs at the declaration, and one that does belongs at the
// use site that has the arguments.
func (g *gen) collectGenericImpls(tpl *genericTemplate, nodes []ast.Node) (string, string) {
	for _, n := range nodes {
		ib, isImpl := n.(*ast.ImplBlock)
		if !isImpl || implReceiverBaseName(ib.Receiver) != tpl.nomi {
			continue
		}
		if ib.Interface == nil && implAllHostBound(ib) {
			// An inherent block of Go-bound functions only: none of them
			// depends on T, so a call crosses to its Go function directly.
			tpl.hostImpls = append(tpl.hostImpls, ib)
			continue
		}
		it := &genericImplTemplate{decl: ib}
		if !implWhereNamesTemplateParams(ib, tpl) {
			// A constraint whose SUBJECT is not one of the template's own
			// parameters. There is no argument to discharge it from, so it
			// cannot be decided at any instantiation.
			it.why, it.whyDetail = "generic impl block", typeText(ib.Receiver)
		}
		tpl.impls = append(tpl.impls, it)
	}
	return "", ""
}

// implAllHostBound reports whether every item of ib is a Go-bound function.
func implAllHostBound(ib *ast.ImplBlock) bool {
	if len(ib.Items) == 0 {
		return false
	}
	for _, item := range ib.Items {
		if ef, isExt := item.(*ast.ExternFunc); !isExt || !hostBoundImplFn(ef) {
			return false
		}
	}
	return true
}

// unboundTypeParams reports whether any of tps is a type parameter the builder
// still has to solve.
//
// A name the ACTIVE SUBSTITUTION FRAME already binds is NOT one. That sounds like
// a relaxation and is the opposite: an impl item built inside an instantiation
// has every one of the template's parameters bound to a concrete kind before its
// signature is resolved, so `fn equal?<T>(a: Box<T>, b: Box<T>)` inside
// `Box<Int>`'s registration is a MONOMORPHIC function whose annotations happen to
// spell `T`. Reporting it as generic would refuse a signature every position of
// which the builder can name.
//
// The front end's synthesized `derive Equatable for Box` emits its `equal?`
// WITH the template's type parameters attached to the FUNCTION, while a
// hand-written `impl Equatable for Box<T>` attaches them to the BLOCK and
// leaves the item's list empty. So the two provenances of one declaration
// disagree about where the binder sits, and a gate reading
// `len(fd.TypeParams) > 0` would refuse every DERIVE on a generic type while
// admitting the identical hand-written form. It is the same disagreement
// implReceiverBaseName absorbs one node higher.
//
// Outside any instantiation `g.genericSubst` is empty and every name is unbound,
// so an ordinary generic impl function refuses.
func (g *gen) unboundTypeParams(tps []ast.TypeParam) bool {
	for _, tp := range tps {
		if _, bound := g.genericSubstKind(tp.Name); !bound {
			return true
		}
	}
	return false
}

// implBinderNamesTemplateParams reports whether every name in an
// `impl<A, B> ... for Name<A, B>` binder is one of the template's own
// parameters.
//
// `impl<T> Iface for Box<T>` and `impl Iface for Box<T>` are the SAME
// declaration -- the first merely spells the binder -- so `ib.Generics` is not a
// gap when it introduces exactly the names the receiver's argument list uses.
// The front end emits the bound spelling for every synthesized `derive` on a
// generic type, so refusing on `len(ib.Generics) > 0` would refuse every derive
// while admitting the identical hand-written form, which is two answers to one
// question.
//
// A binder naming something the receiver does NOT use is a different declaration
// -- a method-level parameter hoisted to the block -- and has no argument to be
// solved from, so it is declined.
func implBinderNamesTemplateParams(ib *ast.ImplBlock, tpl *genericTemplate) bool {
	if len(ib.Generics) == 0 {
		return true
	}
	own := map[string]bool{}
	for _, p := range tpl.params {
		own[p] = true
	}
	for _, tp := range ib.Generics {
		if !own[tp.Name] {
			return false
		}
	}
	return true
}

// unboundWhereSubjects reports whether any `where` clause names a subject the
// active substitution frame has not made concrete.
//
// The frame-aware twin of `len(wcs) > 0`, and the same predicate
// unboundTypeParams is for type parameters: a clause over a BOUND subject is a
// question about a ground kind, decided by the front end before the builder was
// handed the tree. With no frame open every subject is unbound and every clause
// refuses, so this can only turn a refusal into a lowering.
func (g *gen) unboundWhereSubjects(wcs []ast.WhereConstraint) bool {
	for _, wc := range wcs {
		if _, bound := g.genericSubstKind(wc.Name); !bound {
			return true
		}
	}
	return false
}

// implWhereNamesTemplateParams reports whether every constraint an impl BLOCK
// declares has one of the template's own parameters as its subject.
//
// A block-level `where` is common: `derive Display for DisplayBox<T> where T:
// Display` synthesizes an impl block carrying exactly that clause
// (`11-interfaces-and-impls/interface_defaults/interface_defaults_test.nomi`).
//
// # Why admitting it is sound
//
// It is registerInstanceImpls' argument for a DECLARATION-level bound: a
// violating instantiation never reaches the builder, because the front end
// rejects it. A block-level bound is the same kind of claim about the same
// parameter, checked by the same front-end rule, and at `T := String` the
// constraint `T: Display` is decided before the builder is handed the tree.
//
// The SUBJECT check is what stays, and it is the same shape as
// implBinderNamesTemplateParams above: a constraint on a name the receiver does
// not use has no argument to be solved from, so it remains declined. If the
// front end ever stopped checking the bound, the failure here would be a
// call against a type without the method: a lookup that finds nothing rather
// than a wrong answer. That is the safe direction and the same one that header relies on.
func implWhereNamesTemplateParams(ib *ast.ImplBlock, tpl *genericTemplate) bool {
	if len(ib.WhereClauses) == 0 {
		return true
	}
	own := make(map[string]bool, len(tpl.params))
	for _, p := range tpl.params {
		own[p] = true
	}
	for _, wc := range ib.WhereClauses {
		if !own[wc.Name] {
			return false
		}
	}
	return true
}

// registerInstanceImpls registers every one of tpl's impl blocks against the
// instance def, under the substitution frame, and queues each to be built.
//
// Reports the reason the INSTANCE cannot be admitted, so genericInstance can
// decline rather than build a def whose dispatch table is half-populated -- which
// is the state templateWall existed to prevent and which this must not
// reintroduce by a different route.
//
// # The declaration-level bound is not checked here
//
// A violating instantiation NEVER REACHES THE BUILDER. The front end rejects it,
// and type_parameter_dispatch_test.nomi asserts the exact message
// (`no impl of `Display` for `Plain“) -- from INSIDE a compiler.check string,
// i.e. the violating program is the test's SUBJECT and is never a program
// irbuild is asked to lower. So an instantiation-time bound check here would be
// a second implementation of a front-end rule over an empty reachable
// population. With no check, the builder cannot disagree with the front end in
// either direction, because it is never asked.
//
// If the front end ever stopped checking it, the failure here would be a
// `T.hello` call against a type with no `hello`: a lookup that finds nothing,
// not a wrong answer. That is the safe direction and it is why relying on the
// front end is acceptable rather than merely convenient.
func (g *gen) registerInstanceImpls(tpl *genericTemplate, d *typeDef, args []kind) (string, string) {
	if len(tpl.impls) == 0 {
		return "", ""
	}
	recv := named(d)
	g.pushGenericSubst(tpl, args)
	defer g.popGenericSubst()
	for _, it := range tpl.impls {
		if it.why != "" {
			return it.why, it.whyDetail
		}
		if !implBinderNamesTemplateParams(it.decl, tpl) {
			return "generic impl block", typeText(it.decl.Receiver)
		}
		id := g.registerImplAt(it.decl, recv)
		id.typeScope = tpl.scope
		if !id.lowerable {
			return id.why, id.whyDetail
		}
		// The frame is carried on the queue entry, not left to be re-derived at
		// build time. flushGenericImpls runs at the END of the module walk, long
		// after this frame has been popped, and the BODY is where `T` has to
		// resolve — `T.hello(value.value)`, `Describe.describe(b.value)`. An
		// entry without its frame builds a body in which every type parameter is
		// an unresolved name again, which is the refusal this mechanism exists
		// to dissolve, arriving one phase later.
		g.genericImplQueue = append(g.genericImplQueue,
			&genericImplInst{impl: id, tpl: tpl, args: args})
	}
	return "", ""
}

// genericInstRefusal is the reason the INTERNED instance for this annotation was
// declined, or no reason.
//
// # Why this arm has to exist
//
// genericRefusal already has an arm for a WALLED template (`tpl.why != ""`) and
// one for a failing type ARGUMENT (genericArgRefusal). Per-instance impl
// registration creates a third case neither answers: every argument resolves,
// the template is admitted, and the INSTANCE still declined -- because one of its
// impl blocks did.
//
// Without this arm genericArgRefusal returns no-reason, and the use site reports
// whatever its fallback is, so one refusal would get a different name depending
// on where it was reached.
func (g *gen) genericInstRefusal(t *ast.GenericType, tpl *genericTemplate) (string, string, bool) {
	if len(t.Params) != len(tpl.params) {
		return "", "", false
	}
	args := make([]kind, 0, len(t.Params))
	for _, p := range t.Params {
		k := g.typeOf(p)
		// Declines so genericArgRefusal names the ARGUMENT, which is the answer
		// when an argument is what failed rather than the instance.
		// kindInvalid: cascade — the argument's own position reports it.
		if k == kindInvalid {
			return "", "", false
		}
		args = append(args, k)
	}
	key := genericInstKey(tpl, args)
	for _, d := range g.genericInsts[key] {
		if !samePartsSlice(d.genericArgs, args) {
			continue
		}
		if d.lowerable {
			return "", "", false
		}
		return declRefusal(d)
	}
	return "", "", false
}

// isGenericImplTemplate reports whether ib is an impl block this file owns --
// one whose receiver names a generic template that admitted it.
//
// Asked by implDecl, which must emit and refuse NOTHING for such a block. The
// membership test is the template's own `impls` list rather than a re-derivation
// from the receiver spelling, because the two could disagree: a template WALLED
// by collectGenericImpls keeps its blocks out of the list, and those blocks must
// keep reporting at their own position rather than falling silent. A spelling
// test would silence them.
func (g *gen) isGenericImplTemplate(ib *ast.ImplBlock) bool {
	tpl := g.genericTemplates[implReceiverBaseName(ib.Receiver)]
	if tpl == nil || tpl.why != "" {
		return false
	}
	for _, it := range tpl.impls {
		if it.decl == ib {
			return true
		}
	}
	return false
}

// flushGenericImpls emits the Go functions and dispatch bindings for every
// instance impl this module registered.
//
// A WORKLIST rather than a range, for flushMonoInstances' reason: emitting one
// body can queue more, because a body may construct a FURTHER instantiation whose
// own impls are registered at that moment. The loop re-reads len() each turn.
//
// # The queue is deliberately not rolled back by snapshot()
//
// `genericStructLit` lowers a literal's field values TWICE -- once into a probe
// run discarded with `g.snapshot()`, once for real -- and a probe run can MINT an
// instance, which registers that instance's impls onto this queue. snapshot does
// not undo it, and must not.
//
// The reason is that the INSTANCE is not rolled back either: `g.genericInsts` is
// an intern table and a discarded run's entry survives, which is correct (a
// second mention of one instantiation must get one answer). Truncating the
// queue while leaving the instance interned would leave an interned instance
// whose impls were never built, and the next real use would find it interned,
// skip registration, and dispatch through a table entry that does not exist:
// a TRAP at run time.
//
// So the two must be rolled back together or neither, and neither is the safe
// choice. What it costs is that a probe run whose outer literal then refuses can
// leave one instance's impl functions built and unreferenced: DEAD but
// harmless, since nothing calls them. The opposite hazard, speculation dropping
// a real refusal, does not apply here, because this queue holds build work and
// never a refusal.
func (g *gen) flushGenericImpls() {
	// From where the last flush stopped: a VM-only test body built after the
	// module walk can mint an instance, and its impls are built by a second
	// flush without building the earlier ones again.
	for ; g.genericImplsDone < len(g.genericImplQueue); g.genericImplsDone++ {
		e := g.genericImplQueue[g.genericImplsDone]
		if e.subst != nil {
			g.pushIfaceSubst(e.subst)
			g.emitImpl(e.impl)
			g.popIfaceSubst()
			continue
		}
		g.pushGenericSubst(e.tpl, e.args)
		g.emitImpl(e.impl)
		g.popGenericSubst()
	}
}

// genericImplInst is one impl block built for one instantiation, carrying the
// substitution frame its BODY needs.
//
// The frame travels with the work rather than being re-derived, because
// re-derivation would need the receiver kind mapped back to a (template,
// arguments) pair and `typeDef.genericOf`/`genericArgs` already hold that — so a
// second derivation would be a second answer to a question the def answers.
type genericImplInst struct {
	impl *implDef
	tpl  *genericTemplate
	args []kind
	// subst is an already-solved frame, for a queue entry whose specialization
	// is not a (template, arguments) pair. A METHOD instantiation is that case:
	// its frame is the receiver's substitution plus the method's own solved
	// parameters, and there is no genericTemplate whose params they are. Mutually
	// exclusive with tpl/args, and pushed through pushIfaceSubst, which writes
	// the SAME g.genericSubst stack — there is deliberately no fourth stack.
	subst map[string]kind
}

// templateCallInstance is the instance of tpl a type-qualified call
// `Span.new(1, 5)` or `Tree.value_or(Tree.Leaf, "none")` names when no
// operand is already one: the template's parameters solved from the item's
// declared parameter and result annotations against the signature the checker
// solved for the call.
func (g *gen) templateCallInstance(t *ast.Call, tpl *genericTemplate, method string) (kind, bool) {
	ft := g.checkedCallSignature(t)
	if ft == nil {
		return kindInvalid, false
	}
	var fd *ast.FuncDef
	for _, it := range tpl.impls {
		for _, item := range it.decl.Items {
			if f, ok := implItemFunc(item); ok && f.Name == method {
				if fd != nil {
					// Two blocks supply the name: the call is not this
					// question's to settle.
					return kindInvalid, false
				}
				fd = f
			}
		}
	}
	if fd == nil || len(fd.Params) != len(ft.Params) {
		return kindInvalid, false
	}
	params := templateParamSet(tpl)
	solved := map[string]kind{}
	for i, p := range fd.Params {
		if p.TypeAnnotation != nil {
			g.unifyTypeParams(p.TypeAnnotation, g.project(ft.Params[i]), params, solved)
		}
	}
	if fd.ReturnTypeExpr != nil {
		g.unifyTypeParams(fd.ReturnTypeExpr, g.project(ft.Return), params, solved)
	}
	args, ok := templateArgs(tpl, solved)
	if !ok {
		return kindInvalid, false
	}
	for _, a := range args {
		if a.tag == tagTypeParam {
			return kindInvalid, false
		}
	}
	k, ok := g.genericInstance(tpl, args)
	if !ok || k.tag != tagNamed {
		return kindInvalid, false
	}
	return k, true
}

// implsOf is every impl block registered for the receiver k: the module's own
// blocks in source order, and for a generic instance the blocks registered
// against it per instance (registerInstanceImpls), which implOrder does not
// hold.
func (g *gen) implsOf(k kind) []*implDef {
	if k.def == nil || k.def.genericOf == nil {
		return g.implOrder
	}
	var out []*implDef
	for _, it := range k.def.genericOf.impls {
		for _, byRecv := range g.implsByIface {
			if d := byRecv[k]; d != nil && d.decl == it.decl {
				out = append(out, d)
				break
			}
		}
	}
	return out
}
