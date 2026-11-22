package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A generic function, lowered once per type-argument tuple.
//
// # Templates and instances
//
// A generic `fn` declaration is a template (monoTemplate). A call or a
// function value of it lowers an instance: the declaration's body, lowered by
// funcDecl with each type parameter resolved to the type argument the call
// bound (pushMonoSubst). Instances are interned per (declaration, argument
// tuple), so two calls at the same type arguments call one function.
//
// monoTemplateFor derives the template. It is asked from two places.
// declaredSignature (native.go) asks it when genericSignature (generic.go)
// declined the declaration, a type parameter mentioned inside `List<T>`,
// `Maybe<T>`, `Half<T>`, `(T, Int)` or `(A) -> B` among the reasons, and
// records the answer as `sig.mono`. irMonoTemplate (irfuncbody.go) asks it for
// a declaration dictSignature or genericSignature claimed, so a call to one of
// those lowers an instance too.
//
// # The instantiation guard
//
//	fn deep_grow<T>(x: T, depth: Int) -> Int {
//	  if depth <= 0 { 0 } else { deep_grow(Box{inner: x}, depth - 1) + 1 }
//	}
//
// has an infinite instantiation set: the recursive call binds `T := Box<T>`,
// so instantiating it produces `Box<Int>`, `Box<Box<Int>>`, … without end.
// The guard (monoInstCap) stops the chain.
//
// It is not a language change, which is why there is no spec section and no
// front-end diagnostic. The front end accepts `deep_grow`; a builder refusal
// under a named key changes no language rule, like every other key in this
// package.
//
// # Per gen
//
// The intern table is `g.monoInsts`, per gen, as genericInstance's is: an
// instance's parameter kinds are this gen's kinds, and a type another file
// declares is this gen's mirror of it (foreign.go).
//
// # What monoTemplateFor declines
//
//   - A decorator, `main`, a missing body. Each has a funcDecl clause that
//     reports at the declaration's own position, so admitting it here would
//     lower past a refusal.
//   - A parameter with neither an annotation nor a default.
//   - A type parameter that no parameter annotation, return annotation or
//     `where` bound mentions (boundMentionedParams).
//
// # The declaration refuses nothing
//
// funcDecl lowers nothing and refuses nothing for a declaration whose
// `sig.mono` is set. A template is not a function; it is a rule for making
// them. So:
//
//   - Called at a type argument the builder can represent: the instance is
//     lowered and nothing is refused.
//   - Called at one this builder cannot represent: the call refuses, at the
//     position that has the information. That is cascade.go's rule, refuse
//     where the answer is known, and it is why `generic function` at the
//     declaration is the wrong key for it.
//   - Never called: nothing is lowered and nothing is refused, because a
//     template with no instantiation is a rule nobody applied.

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
}

// monoInst is one instantiation: the concrete signature a call site calls.
type monoInst struct {
	tpl  *monoTemplate
	args []kind
	// types is args as the checker's types, nil where the call that made
	// the instance had none (substFrame).
	types []analysis.Type
	// reads is where each of args holds the subtypes the instance reads,
	// or nil when it reads none (subtypeinst.go).
	reads []string
	// sig is an ordinary monomorphic signature: `tps` nil, `dict` nil,
	// `lowerable` true, `genericWhy` empty. The call path swaps it in and
	// every downstream step (arity, named arguments, defaults, coercion) runs
	// unchanged, so a call to an instance and a call to a hand-written
	// monomorphic function check their arguments with the same code.
	sig *fnSig
	// depth is the length of the instantiation chain that reached this
	// instance. Recorded rather than re-derived so the guard reads one number.
	depth int
	// emitted guards the worklist against lowering one instance twice, which
	// a diamond in the call graph would otherwise do.
	emitted bool
}

// monoTemplateFor answers the template fd is, or nil.
//
// The first clauses mirror funcDecl clauses that report at the declaration.
// They are stated here rather than read off `genericWhy` because `genericWhy`
// is one string for several reasons, and reading it would make a decorator on
// a generic `fn` an instantiable template.
//
// # Who asks
//
// declaredSignature (native.go), for a declaration genericSignature declined,
// and irMonoTemplate (irfuncbody.go), for one dictSignature or
// genericSignature claimed.
//
// # A bounded generic
//
// A concrete type argument discharges the bound statically. In an instance
// the frame is pushed, `T` is `Score`, and `Add.add(a, b)` is an ordinary
// concrete dispatch. That is genericimpl.go's own observation about
// `T.hello(v.value)`, one construct over.
func (g *gen) monoTemplateFor(fd *ast.FuncDef) *monoTemplate {
	switch {
	case fd == nil, len(fd.TypeParams) == 0:
		return nil
	case len(fd.Decorators) > 0, fd.Body == nil, fd.Name == "main":
		// funcDecl reports each of these at the declaration's own position, so
		// claiming the declaration here would lower past a refusal.
		return nil
	}
	tpl := &monoTemplate{decl: fd, params: make([]string, 0, len(fd.TypeParams))}
	paramSet := make(map[string]bool, len(fd.TypeParams))
	for _, tp := range fd.TypeParams {
		tpl.params = append(tpl.params, tp.Name)
		paramSet[tp.Name] = true
	}
	// Every parameter must be annotated: an unannotated one reports at
	// funcDecl's own clause, so claiming the declaration here would lower
	// past a refusal. An annotated destructuring parameter (`(a, b): (A, B)`)
	// is a slot of its annotation's type whose names the instance's prologue
	// binds, as funcDecl binds a non-generic function's. A default is
	// evaluated at the call against the instance's signature, as for any
	// `fn`.
	mentioned := make(map[string]bool, len(tpl.params))
	for _, p := range fd.Params {
		if p.TypeAnnotation == nil && p.Default != nil && p.Destructure == nil {
			// `n = 2`: its type comes from its default, which names no type
			// parameter of the function (paramKind reads it).
			continue
		}
		if p.TypeAnnotation == nil {
			return nil
		}
		markMentionedParams(p.TypeAnnotation, paramSet, mentioned)
	}
	// And every type parameter must be NAMED somewhere in the SIGNATURE — a
	// parameter annotation or the RETURN annotation.
	//
	// genericSignature (generic.go) requires a parameter to mention every
	// type parameter. A template has another source to bind `T` from: the
	// analyzer's own solved return type at the call. A return-position
	// parameter such as
	// `fn decode<T>(json: Json) -> Result<T, ShapeError> where T: FromJson`
	// produces a value of `T` by dispatching on it, and `fn plus<L, R,
	// Out>(lhs: L, rhs: R): Out where L: Add<R, Out>`
	// (`12-derives-and-standard-interfaces/add_test.nomi`) names `Out` only in
	// the return.
	//
	// A type parameter named nowhere the CALL can reach is still refused — see
	// boundMentionedParams, which adds only the `where`-bound positions a
	// READ-BACK can close and gates each on its subject being solvable.
	if fd.ReturnTypeExpr != nil {
		markMentionedParams(fd.ReturnTypeExpr, paramSet, mentioned)
	}
	boundMentionedParams(fd, paramSet, mentioned)
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
// here: `fn pair<A, B>(a: A) -> B` has a parameter that names A, so a boolean over
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

func (g *gen) checkedCallSignature(t *ast.Call) *analysis.FuncType {
	if g.fa == nil || t.Func == nil {
		return nil
	}
	line, col := calleeRefPos(t.Func)
	sym := g.srcFA().References[analysis.Pos{Line: line, Col: col}]
	if sym == nil || sym.CallType == nil {
		return nil
	}
	ft, _ := sym.CallType.(*analysis.FuncType)
	return ft
}

// checkedArgSignature is a signature for a call the builder synthesized,
// which the checker recorded none for: the checker's type of each argument,
// with no result: `"${c}"` lowers to `Display.to_string(c)` with c's own
// node as the argument. nil when an argument has no recorded type.
func (g *gen) checkedArgSignature(t *ast.Call) *analysis.FuncType {
	if g.fa == nil || len(t.Args) == 0 {
		return nil
	}
	params := make([]analysis.Type, len(t.Args))
	for i, a := range t.Args {
		ty, ok := g.srcFA().ExprTypes[a]
		if !ok || ty == nil {
			return nil
		}
		params[i] = ty
	}
	return &analysis.FuncType{Params: params}
}

// calleeRefPos is the position `g.fa.References` keys a callee's symbol at.
//
// One rule, and it is the analyzer's rather than a guess: a reference is
// recorded at the IDENTIFIER that names the thing, so `f(x)` records at `f`
// and `Map.get(...)` records at `get` — not at `Map`, which names the
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
func (g *gen) resolveMonoInstance(tpl *monoTemplate, args []kind, types []analysis.Type, name string) (*monoInst, string, string) {
	reads := subtypeReads(tpl.params, types, func() map[string][]*analysis.InterfaceType {
		return analysis.DeclTypeParamBounds(g.srcFA(), tpl.decl, nil)
	})
	key := openInstKey(g.dc, monoInstKey(tpl, args), types) + subtypeReadsKey(reads)
	if got, found := g.monoInsts[key]; found {
		return got, "", ""
	}
	// The chain is measured from the instance being lowered, so a same-`T`
	// recursion — which re-finds the interned instance above and never reaches
	// here — costs nothing, and only a STRICTLY GROWING argument advances it.
	depth := g.monoDepth + 1
	if depth > monoInstCap {
		return nil, "generic instantiation depth",
			fmt.Sprintf("%s<%s> is %d instantiations deep; the chain does not terminate",
				name, kindsNomi(args), depth)
	}
	inst := &monoInst{tpl: tpl, args: args, types: types, reads: reads, depth: depth}
	// Interned BEFORE the signature is resolved, so a self-referential
	// instantiation finds the same instance rather than recursing forever —
	// genericInstance's rule, one level up.
	if g.monoInsts == nil {
		g.monoInsts = map[string]*monoInst{}
	}
	g.monoInsts[key] = inst

	g.pushMonoSubst(inst)
	sig := &fnSig{
		decl:      tpl.decl,
		instance:  inst,
		lowerable: true,
		params:    make([]kind, len(tpl.decl.Params)),
	}
	bad := false
	for i, p := range tpl.decl.Params {
		sig.params[i] = g.paramKind(p)
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
	// The value domain's entry for an instance's signature, as signature
	// holds a declared one.
	if !bad && !sig.inValueDomain() {
		bad = true
	}

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
func (g *gen) pushMonoSubst(inst *monoInst) {
	g.pushSubst(substMono, inst.tpl, newSubstFrame(inst.tpl.params, inst.args, inst.types))
}

func (g *gen) popMonoSubst() { g.popSubst() }

// flushMonoInstances lowers every instance this module built, including
// instances discovered while an earlier instance's body was walked.
//
// A WORKLIST rather than a range, because lowering one body can queue more: a
// generic helper called from a generic caller is instantiated only when the
// caller's own instantiation is known. The loop re-reads len() each turn for
// that reason, and `emitted` guards a diamond in the call graph.
//
// At the end of the module walk: module-level functions have no order
// requirement, and an instance is discovered inside a body where there is no
// module-level position to interleave it at.
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
// funcDecl is reused rather than mirrored. `g.monoSig` overrides the signature
// funcDecl would look up, `g.genericSubst` makes `T` concrete inside the body,
// and everything else (the scope, the destructuring, the return-kind check,
// the refusal reporting) is the one implementation every other function goes
// through. A copy of funcDecl's lowering tail would be a second place for
// the return-kind check to drift out of agreement with the first.
//
// `g.monoDepth` is set for the length of the walk so a call inside this body
// that instantiates a FURTHER template measures its chain from here. That is
// what makes the guard count a chain rather than a total.
func (g *gen) emitMonoInstance(inst *monoInst) {
	prevSig, prevDepth := g.monoSig, g.monoDepth
	g.monoSig, g.monoDepth = inst.sig, inst.depth
	g.pushMonoSubst(inst)
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
		if sym := g.srcFA().References[analysis.Pos{Line: t.Line, Col: t.Col}]; sym != nil {
			return sym.Type
		}
	}
	return nil
}

// checkedExprType is the type the checker gave n: checkedNodeType's answer,
// or else the one it recorded for the node (FileAnalysis.ExprTypes), which
// covers a call with no instantiated signature (`{}`).
func (g *gen) checkedExprType(n ast.Node) analysis.Type {
	if t := g.checkedNodeType(n); t != nil {
		return t
	}
	if g.fa == nil {
		return nil
	}
	return g.srcFA().ExprTypes[n]
}

// checkedKind is the kind of the type the checker gave n, or kindInvalid.
func (g *gen) checkedKind(n ast.Node) kind {
	t := g.checkedExprType(n)
	if t == nil {
		return kindInvalid
	}
	return g.project(t)
}
