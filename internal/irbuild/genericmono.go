package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A generic FUNCTION whose type parameters are mentioned at DEPTH,
// MONOMORPHIZED per call site.
//
// # The line between this file and generic.go, and why there are two
//
// generic.go lowers a bound-free generic `fn` by ERASURE: one Go body whose
// type-parameter positions are `any`, the concrete type argument bound at the
// call and the boxed answer asserted back there. That is complete for a
// declaration whose every type-parameter mention is a BARE `T`, and it is
// refused the moment one is not — `List<T>`, `Maybe<T>`, `Half<T>`, `(T, Int)`,
// `(A) -> B`. generic.go's header states the reason: the box would be at the
// wrong DEPTH, because the body builds a `*rt.List[any]` while the caller wants
// a `*rt.List[int64]`, so an assertion would fail at run time rather than refuse
// at compile time.
//
// That reason is not a reason to refuse the shape; it is a reason not to lower
// it BY ERASURE. This file lowers it by monomorphization instead.
//
// # Why erasure is kept
//
// Monomorphizing everything and deleting the erasure path is the wrong trade.
// An all-bare bound-free generic lowers by erasure as ONE body, and
// monomorphizing it would emit N copies for no gain while putting it under the
// instantiation guard below. So the rule is one sentence with a fast path:
// COPY ONLY WHEN THE BOX WOULD BE AT THE WRONG DEPTH. Two mechanisms for two
// disjoint populations under one stated rule.
//
// # The instantiation guard
//
//	fn deep_grow<T>(x: T, depth: Int): Int {
//	  if depth <= 0 { 0 } else { deep_grow(Box{inner: x}, depth - 1) + 1 }
//	}
//
// has an infinite instantiation set: the recursive call binds `T := Box<T>`,
// so monomorphizing it produces `Box<Int>`, `Box<Box<Int>>`, … without end.
// Without monomorphization `deep_grow` refuses at `generic type | Box<T>` —
// generictype.go's population C, an instantiation at a type PARAMETER, walled
// because there is no concrete argument to monomorphize AT. Monomorphization is
// what supplies that argument, so it is what makes the unbounded chain
// reachable, and the guard (monoInstCap) is what stops it.
//
// It is not a language change, which is why there is no spec section and no
// front-end diagnostic. The front end accepts `deep_grow`; a BUILDER refusal
// under a named key changes no language rule, like every other key in this
// package.
//
// # PER-GEN, and the Go name is minted against the PROGRAM-WIDE set
//
// The intern table is `g.monoInsts`, per gen, for genericInstance's reason: an
// instance's parameter types can name a type ONE module's package declares.
//
// For types, routing through `g.reg.byDecl[decl]` would give every
// instantiation one def, because that map is keyed on a DECLARATION NODE and
// every instance of one template shares it. A monomorphized FUNCTION has
// exactly the same shape — one
// `*ast.FuncDef`, N instances — and a strongly-connected component of the Nomi
// import graph becomes ONE Go package, so two co-tenant files each declaring
// `fn get<T>` and each instantiating it at `Int` would both mint
// `Nomi_get_int64`.
//
// # What stays refused, and why
//
//   - A BOUNDED generic that dict.go's seam claims. The dictionary is asked
//     first and this file takes only its residue; see monoTemplateFor.
//   - A tail-cycle member. refuseGenericTailCycles owns it and keeps
//     `recursive tail call`. Each instance is a distinct Go function, so a
//     same-`T` self-call IS a self-call of the instance and TCO would be
//     available, but it needs its own mechanism and claiming it without one
//     would grow the stack without bound.
//   - A NESTED generic `fn`. nestedfn.go's clause stands; a nested `fn` is a Go
//     func literal and MarkTailCalls never enters one.
//   - A type parameter no parameter mentions, a defaulted parameter, a
//     destructuring parameter, a decorator, `main`, an impl function, a missing
//     body. Each already has a funcDecl clause that reports at the declaration's
//     own position, so admitting it here would lower past a refusal.
//   - A type argument that is itself a type parameter, or one of the untyped
//     literal kinds. bindableTypeArg owns both and is reused verbatim: the first
//     is not a concrete type to bind (it resolves to an unresolved
//     type-parameter name), and the second would fix an artifact's element type from a literal whose real type
//     the program decides elsewhere.
//
// # THE DECLARATION REFUSES NOTHING, AND THAT IS cascade.go's DISCIPLINE
//
// funcDecl lowers nothing and REFUSES nothing for a template. A template is not
// a function; it is a rule for making them. So:
//
//   - Called at a monomorphizable type argument: the instance is lowered and
//     nothing is refused.
//   - Called at one this builder cannot represent: THE CALL refuses, at the
//     position that has the information. That is cascade.go's rule — refuse
//     where the answer is known — and it is why `generic function` at the
//     declaration is the wrong key for it.
//   - Never called: nothing is lowered and nothing is refused, because a
//     template with no instantiation is a rule nobody applied. Dead generic code
//     costs the program nothing.

// monoInstCap is the deepest chain of DISTINCT instantiations one call graph may
// require before this builder refuses it.
//
// A same-`T` recursion does not consume depth at all: it re-finds the interned
// instance, so the chain has length one however many times it recurses. Depth
// grows only when a call binds a type parameter to something STRICTLY LARGER
// than the caller's own binding, which is `deep_grow`'s shape and nothing else.
//
// The value is a fence, not a tuning knob: across `std/` and `tests/`, every
// self-recursive generic recurses at its own type argument,
// so the observed maximum depth is 1 and any small number refuses nothing
// real. Eight leaves room for a legitimate chain of nested generic helpers —
// `f` calling `g` calling `h`, each wrapping its parameter once — while still
// terminating.
const monoInstCap = 8

// monoTemplate is one generic `fn` declaration this file may instantiate.
//
// Kept BESIDE the fnSig rather than instead of it, for genericTemplate's
// reason: `g.funcs[name]` keeps its ordinary entry, so a funcref, a partial
// application and a placeholder each keep reporting their own reason rather
// than silently acquiring a lowering none of them can use.
type monoTemplate struct {
	decl *ast.FuncDef
	// params are the type-parameter names in declaration order. Positional,
	// because a turbofish is positional.
	params []string
	// paramSet is `params` as a set, built once for unifyTypeParams' leaf arm.
	paramSet map[string]bool
}

// monoInst is one instantiation: the concrete signature a call site calls.
type monoInst struct {
	tpl  *monoTemplate
	args []kind
	// sig is an ORDINARY MONOMORPHIC signature — `tps` nil, `dict` nil,
	// `lowerable` true, `genericWhy` empty. That is the whole trick: the call
	// path swaps it in and every downstream step (arity, named arguments, defaults,
	// coercion, the tail hop) runs unchanged, so a call to an instance and a
	// call to a hand-written monomorphic function agree about argument checking
	// by construction rather than by a second copy of it.
	sig *fnSig
	// depth is the length of the instantiation chain that reached this
	// instance. Recorded rather than re-derived so the guard reads one number.
	depth int
	// emitted guards the worklist against emitting one instance twice, which
	// a diamond in the call graph would otherwise do.
	emitted bool
}

// monoTemplateFor answers the template fd is, or nil.
//
// Every clause is a NEGATIVE mirror of a funcDecl clause that reports at the
// declaration, plus the one generic.go owns. They are re-stated here rather
// than read off `genericWhy` because `genericWhy` is one string for eight
// different reasons and this file may only claim ONE of them: the depth rule.
// Reading the string would make a decorator on a generic `fn` silently
// monomorphizable.
//
// # A BOUNDED generic reaches here
//
// A concrete type argument discharges the bound statically. The dictionary and
// an instance are required to agree on OBSERVABLE BEHAVIOUR, which the golden
// records check, not on mechanism.
//
// # This takes nothing from the dictionary
//
// This function is asked from ONE place: `signature`, at native.go, and only
// after `dictSignature` has already DECLINED to claim the declaration. So the
// ordering implements "dictionary first, monomorphization for its residue" and
// no clause here is what protects it — a bounded generic whose
// bound resolves to a lowerable `*ifaceDef` never reaches this function at all.
//
// The residue it does reach is exactly `dictSeamFor`'s three declines: two or
// more BOUNDED type parameters, a bound naming an interface this builder cannot
// erase (a GENERIC interface, `Iter<T>` and `Add<T, T>`), and a bound that
// resolves to nothing. In an instance every one of those dissolves rather than
// being handled: the frame is pushed, `T` is `Score`, and `Add.add(a, b)` is an
// ordinary concrete dispatch with no dictionary to pass and no erased table to
// demand. That is genericimpl.go's own observation about `T.hello(v.value)`,
// one construct over.
func (g *gen) monoTemplateFor(fd *ast.FuncDef) *monoTemplate {
	switch {
	case fd == nil, len(fd.TypeParams) == 0:
		return nil
	case fd.ImplFunction, len(fd.Decorators) > 0, fd.Body == nil, fd.Name == "main":
		// funcDecl reports each of these at the declaration's own position, so
		// claiming the declaration here would lower past a refusal.
		return nil
	}
	tpl := &monoTemplate{decl: fd, params: make([]string, 0, len(fd.TypeParams))}
	tpl.paramSet = make(map[string]bool, len(fd.TypeParams))
	for _, tp := range fd.TypeParams {
		tpl.params = append(tpl.params, tp.Name)
		tpl.paramSet[tp.Name] = true
	}
	// Every parameter must be an annotated slot: a destructuring or an
	// unannotated parameter reports at funcDecl's own clause, so claiming the
	// declaration here would lower past a refusal. A default is evaluated at
	// the call against the instance's signature, as for any `fn`.
	mentioned := make(map[string]bool, len(tpl.params))
	for _, p := range fd.Params {
		if p.Destructure != nil || p.TypeAnnotation == nil {
			return nil
		}
		markMentionedParams(p.TypeAnnotation, tpl.paramSet, mentioned)
	}
	// And every type parameter must be NAMED somewhere in the SIGNATURE — a
	// parameter annotation or the RETURN annotation.
	//
	// Erasure (generic.go) requires a PARAMETER to mention every type
	// parameter, because it has no other source to bind `T` from.
	// Monomorphization has a third source: the analyzer's own solved return
	// type at the call (see monoTypeArgs). A return-position parameter such as
	// `fn decode<T>(json: Json): Result<T, ShapeError> where T: FromJson`
	// produces a value of `T` by dispatching on it, and `fn plus<L, R,
	// Out>(lhs: L, rhs: R): Out where L: Add<R, Out>`
	// (`12-derives-and-standard-interfaces/add_test.nomi`) names `Out` only in
	// the return.
	//
	// A type parameter named nowhere the CALL can reach is still refused — see
	// boundMentionedParams, which adds only the `where`-bound positions a
	// READ-BACK can close and gates each on its subject being solvable.
	if fd.ReturnTypeExpr != nil {
		markMentionedParams(fd.ReturnTypeExpr, tpl.paramSet, mentioned)
	}
	boundMentionedParams(fd, tpl.paramSet, mentioned)
	for _, name := range tpl.params {
		if !mentioned[name] {
			return nil
		}
	}
	return tpl
}

// markMentionedParams records every member of `params` that te names, at any
// depth.
//
// namesTypeParam answers the same question with a BOOLEAN, which is not enough
// here: `fn pair<A, B>(a: A): B` has a parameter that names A, so a boolean over
// the whole list would admit it and leave B unsolvable. The per-name answer is
// what makes the check per-PARAMETER rather than per-signature.
func markMentionedParams(te ast.TypeExpr, params, out map[string]bool) {
	switch t := te.(type) {
	case *ast.SimpleType:
		if params[t.Name] {
			out[t.Name] = true
		}
	case *ast.GenericType:
		for _, a := range t.Params {
			markMentionedParams(a, params, out)
		}
	case *ast.FuncType:
		for _, a := range t.Params {
			markMentionedParams(a, params, out)
		}
		markMentionedParams(t.Return, params, out)
	}
}

// solvedCallReturn is the kind of the RESULT type the checker solved for this
// call, or kindInvalid when it solved none.
//
// The instantiated signature lives on the call-site symbol's `CallType`, keyed
// by the CALLEE's position — the `*ast.Call` node's own Line/Col is the
// argument list's, and `g.fa.References` records a reference at the identifier.
// Read through the same channel channels.go and prelude.go read, so all three
// agree about which call was instantiated.
//
// WHICH identifier is calleeRefPos' question, and getting it wrong is a silent
// miss rather than an error: a `Type.member` callee records its reference at
// the MEMBER. On `Map.empty<String, Int>()` the reference at `Map` carries no
// CallType at all while the one at `empty` carries `() -> Map<String, Int>`,
// which is what preludeCall reads (it is handed `callee.Field.Line,
// callee.Field.Col` and never the FieldAccess').
//
// kindInvalid is the ordinary "nothing to say" answer and never a refusal: the
// caller unifies with it only when it is not kindInvalid, so a call the checker
// left uninstantiated is not affected.
func (g *gen) solvedCallReturn(t *ast.Call) kind {
	ft := g.checkedCallSignature(t)
	if ft == nil || ft.Return == nil {
		return kindInvalid
	}
	return g.project(ft.Return)
}

func (g *gen) checkedCallSignature(t *ast.Call) *analysis.FuncType {
	if g.fa == nil || t.Func == nil {
		return nil
	}
	line, col := calleeRefPos(t.Func)
	sym := g.fa.References[analysis.Pos{Line: line, Col: col}]
	if sym == nil || sym.CallType == nil {
		return nil
	}
	ft, _ := sym.CallType.(*analysis.FuncType)
	return ft
}

// checkedArgSignature is a signature for a call the builder synthesized,
// which the checker recorded none for: the checker's type of each argument,
// with no result. `"${c}"` lowers to `Display.to_string(c)` with c's own
// node as the argument, so a hole in c's type (`Result<Int, ?E>` for
// `c = Ok(3)`) is still the checker's and irCheckerHoles finds it. nil when
// an argument has no recorded type.
func (g *gen) checkedArgSignature(t *ast.Call) *analysis.FuncType {
	if g.fa == nil || len(t.Args) == 0 {
		return nil
	}
	params := make([]analysis.Type, len(t.Args))
	for i, a := range t.Args {
		ty, ok := g.fa.ExprTypes[a]
		if !ok || ty == nil {
			return nil
		}
		params[i] = ty
	}
	return &analysis.FuncType{Params: params}
}

// checkedMonoTypeArgs reads a fully concrete retained signature from the
// checker. It evaluates no argument expression. Other signatures, including
// bounds whose result is still unresolved, answer false.
func (g *gen) checkedMonoTypeArgs(t *ast.Call, tpl *monoTemplate) ([]kind, bool) {
	params, result, ok := g.checkedMonoCallKinds(t, len(tpl.decl.Params))
	if !ok {
		return nil, false
	}
	return g.monoSolve(tpl, params, result)
}

// checkedMonoCallKinds is the checker's instantiated signature at the call t
// in this gen's kinds: one kind per parameter, and the result's, which is
// kindInvalid when the checker left it open. It solves nothing, so the kinds
// can be handed to the gen that declares the callee (siblinggeneric.go).
func (g *gen) checkedMonoCallKinds(t *ast.Call, arity int) ([]kind, kind, bool) {
	ft := g.checkedCallSignature(t)
	if ft == nil || len(ft.Params) != arity {
		return nil, kindInvalid, false
	}
	params := make([]kind, arity)
	for i, pt := range ft.Params {
		// kindInvalid: sentinel — no kind read yet for this parameter.
		k := kindInvalid
		if irUnsolvedType(pt) {
			// The checker left this parameter's instantiation open at the
			// call (`label(p, first(xs))`, where `first`'s own type argument
			// is solved only through its bound): the argument's own
			// instance answers it.
			if i >= len(t.Args) {
				return nil, kindInvalid, false
			}
			if namedArgNode(t.Args[:i+1]) != nil || (len(t.Args) < arity && i == len(t.Args)-1) {
				// Only a positional argument written before any named one
				// fills the slot it is written at, and a short call's last
				// positional may move to the last slot (argSlotPlan).
				return nil, kindInvalid, false
			}
			k = g.monoArgKind(t.Args[i])
		} else {
			k = g.project(pt)
		}
		if !irCallableValueKind(k) {
			return nil, kindInvalid, false
		}
		params[i] = k
	}
	// kindInvalid: sentinel — a result the checker left open solves nothing.
	result := kindInvalid
	if !irUnsolvedType(ft.Return) {
		result = g.project(ft.Return)
		if result != kindUnit && !irCallableValueKind(result) {
			return nil, kindInvalid, false
		}
	}
	return params, result, true
}

// monoSolve binds tpl's type parameters by unifying its declared annotations
// against the call's parameter and result kinds, which are this gen's.
func (g *gen) monoSolve(tpl *monoTemplate, params []kind, result kind) ([]kind, bool) {
	if len(params) != len(tpl.decl.Params) {
		return nil, false
	}
	solved := map[string]kind{}
	for i, param := range tpl.decl.Params {
		g.unifyTypeParams(param.TypeAnnotation, params[i], tpl.paramSet, solved)
	}
	// kindInvalid: sentinel — checkedMonoCallKinds' open result.
	if result != kindInvalid && tpl.decl.ReturnTypeExpr != nil {
		g.unifyTypeParams(tpl.decl.ReturnTypeExpr, result, tpl.paramSet, solved)
	}
	g.monoSolveFromBounds(tpl, solved)
	args := make([]kind, len(tpl.params))
	for i, name := range tpl.params {
		k, found := solved[name]
		if !found || !irCallableValueKind(k) {
			return nil, false
		}
		args[i] = k
	}
	return args, true
}

// checkedMonoTypeArgsFilled is checkedMonoTypeArgs for a call whose checked
// signature holds an inference variable nothing solved: a type argument the
// checker admits undetermined because no value of it is made (spec,
// "Determined type arguments"), as in `ident(Ok(1))`, whose error type is
// open. The hole is read as Unit, as stdInstCallAt reads a std call's; when
// every hole is the element of an empty list literal (`count_all([])`), as
// Int, the representation `List.head([])` gets, since a type parameter
// solved to Unit itself has no instance. It reports false for a signature
// with no hole.
func (g *gen) checkedMonoTypeArgsFilled(t *ast.Call, tpl *monoTemplate) ([]kind, bool) {
	ft := g.checkedCallSignature(t)
	if ft == nil || len(ft.Params) != len(tpl.decl.Params) || !irUnsolvedType(ft) {
		return nil, false
	}
	fills := []analysis.Type{analysis.TypeUnit}
	if irOnlyEmptyListsUnsolved(t, ft) {
		fills = append(fills, analysis.TypeInt)
	}
	for _, fill := range fills {
		params := make([]kind, len(ft.Params))
		for i, p := range ft.Params {
			params[i] = g.project(irFillHoles(p, fill))
		}
		if args, ok := g.monoSolve(tpl, params, g.project(irFillHoles(ft.Return, fill))); ok {
			return args, true
		}
	}
	return nil, false
}

// monoArgKind is the kind an argument expression the checker left open
// produces: a call to a generic function is its instance's result, and a
// name is its binding's type. Anything else answers kindInvalid.
func (g *gen) monoArgKind(n ast.Node) kind {
	if c, isCall := n.(*ast.Call); isCall && len(c.TypeArgs) == 0 {
		if id, direct := c.Func.(*ast.Ident); direct {
			if _, shadowed := g.lookup(id.Name); !shadowed {
				if tpl := g.irMonoTemplate(g.funcs[id.Name]); tpl != nil && len(c.Args) == len(tpl.decl.Params) {
					if args, ok := g.checkedMonoTypeArgs(c, tpl); ok {
						if inst, why, _ := g.resolveMonoInstance(tpl, args, id.Name); why == "" && inst != nil {
							return inst.sig.result
						}
					}
				}
			}
		}
	}
	if at := g.checkedNodeType(n); at != nil && !irUnsolvedType(at) {
		return g.project(at)
	}
	return kindInvalid
}

// monoSolveFromBounds solves a type parameter named only inside a `where`
// bound (`fn first<T, I>(it: I): Maybe<T> where I: Iter<T>`) from the bound
// subject's solved kind: the interface arguments the subject implements the
// bound at, read back rather than inferred. A parameter something else
// already solved keeps its answer, and a bound this cannot read back
// contributes nothing. Iterated so a solved parameter can in turn be a
// subject.
func (g *gen) monoSolveFromBounds(tpl *monoTemplate, solved map[string]kind) {
	for range len(tpl.params) {
		before := len(solved)
		for _, wc := range tpl.decl.WhereClauses {
			subject, known := solved[wc.Name]
			if !known {
				continue
			}
			for _, b := range wc.Bounds {
				gt, generic := b.(*ast.GenericType)
				if !generic {
					continue
				}
				args, ok := g.boundTypeArgs(gt.Name, subject)
				if !ok || len(args) != len(gt.Params) {
					continue
				}
				for i, p := range gt.Params {
					g.unifyTypeParams(p, args[i], tpl.paramSet, solved)
				}
			}
		}
		if len(solved) == before {
			return
		}
	}
}

// boundTypeArgs is the type arguments at which k implements the generic
// interface iface: `Iter`'s element for a std collection, or the arguments a
// local impl block wrote for its receiver.
func (g *gen) boundTypeArgs(iface string, k kind) ([]kind, bool) {
	if iface == "Iter" {
		if _, local := g.ifaceNamed(iface); !local {
			if e, ok := irStdIterElem(g, k); ok {
				return []kind{e}, true
			}
		}
	}
	if d := g.implsByIface[iface][k]; d != nil && d.ifaceSubst != nil && d.iface != nil {
		if tpl := g.ifaceTemplate(iface); tpl != nil {
			args := make([]kind, 0, len(tpl.TypeParams))
			for _, p := range tpl.TypeParams {
				a, bound := d.ifaceSubst[p.Name]
				if !bound {
					return nil, false
				}
				args = append(args, a)
			}
			return args, true
		}
	}
	return nil, false
}

// calleeRefPos is the position `g.fa.References` keys a callee's symbol at.
//
// One rule, and it is the analyzer's rather than a guess: a reference is
// recorded at the IDENTIFIER that names the thing, so `f(x)` records at `f`
// and `Map.empty(...)` records at `empty` — not at `Map`, which names the
// qualifier. Everything else answers with its own position, which for a
// parenthesised or computed callee is the position no reference is recorded
// at, so the lookup misses and the caller's "nothing to say" answer stands.
func calleeRefPos(callee ast.Node) (int, int) {
	if fa, isField := callee.(*ast.FieldAccess); isField && fa.Field != nil {
		return fa.Field.Line, fa.Field.Col
	}
	return nodePos(callee)
}

// resolveMonoInstance owns interning and signature resolution. Reporting is
// separate so a retained attempt can decline without duplicating a refusal.
func (g *gen) resolveMonoInstance(tpl *monoTemplate, args []kind, name string) (*monoInst, string, string) {
	key := monoInstKey(tpl, args)
	if got, found := g.monoInsts[key]; found {
		return got, "", ""
	}
	// The chain is measured from the instance being emitted, so a same-`T`
	// recursion — which re-finds the interned instance above and never reaches
	// here — costs nothing, and only a STRICTLY GROWING argument advances it.
	depth := g.monoDepth + 1
	if depth > monoInstCap {
		return nil, "generic instantiation depth",
			fmt.Sprintf("%s<%s> is %d instantiations deep; the chain does not terminate",
				name, kindsNomi(args), depth)
	}
	inst := &monoInst{tpl: tpl, args: args, depth: depth}
	// Interned BEFORE the signature is resolved, so a self-referential
	// instantiation finds the same instance rather than recursing forever —
	// genericInstance's rule, one level up.
	if g.monoInsts == nil {
		g.monoInsts = map[string]*monoInst{}
	}
	g.monoInsts[key] = inst

	g.pushMonoSubst(tpl, args)
	sig := &fnSig{
		decl:      tpl.decl,
		instance:  inst,
		lowerable: true,
		params:    make([]kind, len(tpl.decl.Params)),
	}
	bad := false
	for i, p := range tpl.decl.Params {
		sig.params[i] = g.typeOf(p.TypeAnnotation)
		// The refusal below names the whole instantiation ONCE, rather than one
		// key per parameter position of a signature the program never wrote.
		// kindInvalid: propagates — `generic instantiation` names the whole instantiation below.
		if sig.params[i] == kindInvalid {
			bad = true
		}
	}
	sig.result = kindUnit
	if tpl.decl.ReturnTypeExpr != nil {
		sig.result = g.typeOf(tpl.decl.ReturnTypeExpr)
		// kindInvalid: propagates — as above.
		if sig.result == kindInvalid {
			bad = true
		}
	}
	g.popMonoSubst()

	if bad {
		// The instantiation is not representable. Refused at the CALL, because
		// the declaration is fine at every OTHER type argument and blaming it
		// would report a gap that does not exist there. Removed from the table
		// so a second call at the same arguments reports too rather than
		// silently reusing a broken instance.
		delete(g.monoInsts, key)
		return nil, "generic instantiation", fmt.Sprintf("%s<%s> is outside the subset", name, kindsNomi(args))
	}
	inst.sig = sig
	g.monoQueue = append(g.monoQueue, inst)
	return inst, "", ""
}

// monoInstKey is the intern key: the declaration pointer plus every argument's
// kind identity (kind.key).
//
// genericInstKey's rule, verbatim and for its reason. Keyed on the FULL
// instantiation and never on the declaration alone, because a base-name key is
// a silent overwrite that hands one instantiation another's parameter types.
// The declaration pointer is in the key so two same-named templates from two
// files — which foreign.go can put in one gen — cannot collide.
func monoInstKey(tpl *monoTemplate, args []kind) string {
	key := fmt.Sprintf("%p", tpl.decl)
	for _, a := range args {
		key += "\x00" + a.key()
	}
	return key
}

// pushMonoSubst makes the template's type parameters resolve to the instance's
// arguments for the length of one signature resolution or one body walk.
//
// The SAME stack generictype.go pushes a struct template's frame onto, and that
// sharing is the point rather than a convenience: `g.genericSubst` is consulted
// from typeOf's `*ast.SimpleType` arm, so `Half<T>`, `List<Maybe<T>>` and
// `(T) -> Bool` are all covered at every depth by one insertion point that
// already exists. A separate stack for functions would be a second answer to
// "what does this name mean here", reachable from the same typeOf.
func (g *gen) pushMonoSubst(tpl *monoTemplate, args []kind) {
	frame := make(map[string]kind, len(tpl.params))
	for i, p := range tpl.params {
		frame[p] = args[i]
	}
	g.genericSubst = append(g.genericSubst, frame)
}

func (g *gen) popMonoSubst() {
	g.genericSubst = g.genericSubst[:len(g.genericSubst)-1]
}

// flushMonoInstances lowers every instance this module built, including
// instances discovered while an earlier instance's body was walked.
//
// A WORKLIST rather than a range, because lowering one body can queue more: a
// generic helper called from a generic caller is instantiated only when the
// caller's own instantiation is known. The loop re-reads len() each turn for
// that reason, and `emitted` guards a diamond in the call graph.
//
// At the END of the module walk, which is the licence flushBlockLocalTypes, flushTypeIDs and flushInspectors take:
// module-level functions have no order requirement, and an instance is
// discovered inside a body where there is no module-level position to
// interleave it at.
func (g *gen) flushMonoInstances() {
	// NOT `for i := range len(g.monoQueue)`: the body APPENDS to the queue it
	// is walking, so the bound has to be re-read each turn. `range` over an int
	// evaluates its operand once and would silently drop every instance
	// discovered inside another instance's body — which is the whole reason
	// this is a worklist.
	for i := 0; i < len(g.monoQueue); i++ {
		inst := g.monoQueue[i]
		if inst.emitted {
			continue
		}
		inst.emitted = true
		g.emitMonoInstance(inst)
	}
}

// emitMonoInstance lowers one instance.
//
// funcDecl is REUSED rather than mirrored, and that is the difference between
// this file and a second builder. `g.monoSig` overrides the signature funcDecl
// would look up, `g.genericSubst` makes `T` concrete inside the body, and
// everything else — the scope, the destructuring, the tail plan, the return-kind
// check, the refusal reporting — is the one implementation every other function
// goes through. A copy of funcDecl's lowering tail would be a second place for
// the return-kind check to drift out of agreement with the first.
//
// `g.monoDepth` is set for the length of the walk so a call inside this body
// that instantiates a FURTHER template measures its chain from here. That is
// what makes the guard count a chain rather than a total.
func (g *gen) emitMonoInstance(inst *monoInst) {
	prevSig, prevDepth := g.monoSig, g.monoDepth
	g.monoSig, g.monoDepth = inst.sig, inst.depth
	g.pushMonoSubst(inst.tpl, inst.args)
	defer func() {
		g.popMonoSubst()
		g.monoSig, g.monoDepth = prevSig, prevDepth
	}()
	g.funcDecl(inst.tpl.decl)
}

// checkedNodeType is the type the checker solved for an argument expression
// it records one for: a call's instantiated result, or a name's binding.
// Anything else answers nil.
func (g *gen) checkedNodeType(n ast.Node) analysis.Type {
	if g.fa == nil {
		return nil
	}
	switch t := n.(type) {
	case *ast.Call:
		if ft := g.checkedCallSignature(t); ft != nil {
			return ft.Return
		}
	case *ast.Ident:
		if sym := g.fa.References[analysis.Pos{Line: t.Line, Col: t.Col}]; sym != nil {
			return sym.Type
		}
	}
	return nil
}

// checkedExprType is the type the checker gave n: checkedNodeType's answer,
// or else the one it recorded for the node (FileAnalysis.ExprTypes), which
// covers a call with no instantiated signature (`Map.empty()`).
func (g *gen) checkedExprType(n ast.Node) analysis.Type {
	if t := g.checkedNodeType(n); t != nil {
		return t
	}
	if g.fa == nil {
		return nil
	}
	return g.fa.ExprTypes[n]
}

// irUnsolvedType reports whether an inference variable the checker never
// solved appears anywhere in t.
func irUnsolvedType(t analysis.Type) bool {
	switch ty := t.(type) {
	case nil:
		return false
	case *analysis.TypeVar:
		return ty.Resolved == nil || irUnsolvedType(ty.Resolved)
	case *analysis.ListType:
		return irUnsolvedType(ty.Elem)
	case *analysis.MapType:
		return irUnsolvedType(ty.Key) || irUnsolvedType(ty.Val)
	case *analysis.TupleType:
		for _, e := range ty.Elems {
			if irUnsolvedType(e) {
				return true
			}
		}
	case *analysis.AnonStructType:
		for _, f := range ty.Fields {
			if irUnsolvedType(f.Type) {
				return true
			}
		}
	case *analysis.FuncType:
		for _, p := range ty.Params {
			if irUnsolvedType(p) {
				return true
			}
		}
		return irUnsolvedType(ty.Return)
	case *analysis.EnumType:
		for _, a := range ty.TypeArgs {
			if irUnsolvedType(a) {
				return true
			}
		}
	case *analysis.StructType:
		for _, a := range ty.TypeArgs {
			if irUnsolvedType(a) {
				return true
			}
		}
	case *analysis.DistinctType:
		for _, a := range ty.TypeArgs {
			if irUnsolvedType(a) {
				return true
			}
		}
	}
	return false
}

// irStdIterElem is the element a std collection implements `Iter` at: a
// List's, Vector's or Set's element, a Map's `(key, value)` pair, and a
// Range's element.
func irStdIterElem(g *gen, k kind) (kind, bool) {
	switch {
	case k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1:
		return k.comp.parts[0], true
	case k.tag == tagMap && k.comp != nil && len(k.comp.parts) == 2:
		return g.tupleKind([]kind{k.comp.parts[0], k.comp.parts[1]}), true
	}
	if e, ok := vectorElem(k); ok {
		return e, true
	}
	if e, ok := setElem(k); ok {
		return e, true
	}
	if e, ok := rangeElem(k); ok {
		return e, true
	}
	return kindInvalid, false
}
