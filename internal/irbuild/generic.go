package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// A bound-free generic function lowered as ONE body, value-passing, with no
// specialization.
//
// # The line it draws
//
// Runtime dispatch is the mechanism: a single body operates on the runtime
// value model, and type parameters are live runtime entities carrying their
// bounds, which is dictionary-passing in all but name.
//
// That splits `generic function` in two, and the split is DERIVED rather than
// conventional. `T.method(x)` resolves by scanning the type parameter's bounds
// for one whose interface declares the method; with ZERO bounds there is
// nothing to scan, so a bound-free type parameter provably cannot host a
// type-parameter-qualified call. A bound-free `T` is therefore value movement and nothing else — there is no
// interface to go through and no impl to look up.
//
//   - BOUND-FREE, every mention a bare type parameter: lowered here.
//   - Anything with a bound or a `where` clause: refused as `generic function`,
//     because it needs the dictionary seam (the bounds naming WHICH interface,
//     the concrete type argument naming WHICH impl).
//
// typeParamsNeedDictionary is that line, written once so the two halves cannot
// drift apart.
//
// # Why the Go function is NOT generic
//
// The obvious lowering — `func Nomi_first[T any](fr *rt.Frame, x T) T` — does
// not work, because monomorphization rejects programs that are valid:
//
//	fn tail_grow<T>(x: T, depth: Int): Int {
//	  if depth <= 0 { 0 } else { tail_grow(Box{inner: x}, depth - 1) }
//	}
//
// which `nomi check`s clean and runs, and whose instantiation set is infinite.
// A Go generic function recursing at a different type argument is a compile-time
// `instantiation cycle` error, so emitting one would make a valid program fail
// to build. Go generics ARE monomorphization for this purpose.
//
// So the Go signature is monomorphic and the type-parameter positions are `any`:
//
//	func Nomi_first(fr *rt.Frame, x any) any { ... }
//
// One body, one instantiation, no compile-time explosion — and a tail self-call
// stays a self-call. `any` and not
// `rt.Dyn`: `rt.Dyn` carries a `*TypeID` for DISPATCH, and a bound-free type
// parameter has nothing to dispatch on, so the TID would be a word of dead
// weight on every call.
//
// # The concrete type argument lives at the CALL
//
// The CALLER is where the concrete type argument is known, so the call site
// binds each type parameter from its own arguments —
// checked against an explicit turbofish when one is written — and asserts the
// boxed result back to the bound Go type at the one place the concrete type is
// known. The callee never learns it, and never needs to.
//
// # What stays refused here, and why
//
//   - A type parameter mentioned anywhere but bare: `List<T>`, `Maybe<T>`,
//     `Pair<A, B>`, `(T, Int)`, `(A) -> C`. The box would be at the wrong
//     DEPTH — the body builds a `*rt.List[any]` and the caller wants a
//     `*rt.List[int64]`, so an assertion would fail at run time rather than
//     refuse at compile time.
//   - A type parameter no parameter mentions: nothing can produce a value of
//     it, so the body could not use it even with a turbofish.
//   - A tail-cycle member. tailMemberSig already excludes a generic `fn` from a
//     MULTI-member driver, but decideTailPlan settles a single-member component
//     `ok` without consulting it — so a self-recursive generic would emit
//     ordinary Go recursion and grow the stack without bound. It is refused as
//     `recursive tail call` at the declaration.
//   - A default parameter value: a default is evaluated at the CALL in the
//     callee's scope (sugar.go), and argSlots would have to place it against a
//     signature whose slots are not yet instantiated.
//   - A type parameter bound to another type parameter, or to one of the three
//     untyped literal kinds. Both refused at the call site — see
//     bindableTypeArg.
//
// # NESTED generic functions are deliberately untouched
//
// nestedfn.go has its own `generic function` clause and keeps it. A nested `fn`
// is a Go func literal, MarkTailCalls never enters one, and nestedfn.go's own
// header records that teaching it to would arm the valueCall trap.

// typeParamDef is one type parameter's identity.
//
// A DECLARATION, so kind equality is Go pointer equality and two functions'
// `T` are two kinds however identically they spell and lower — the rule
// native.go's `kind` comment states for named types, applied here for the same
// reason. Interning on the spelling would collapse two functions' `T`s, and a
// call inside a generic body would then be able to bind one function's `T`
// from another's.
type typeParamDef struct {
	// nomi is the source spelling, for a refusal's detail text.
	nomi string
	// fn is the declaring function's name. Diagnostics only; identity is the
	// pointer.
	fn string
}

// typeParamKind is the kind of values of a bound-free type parameter.
func typeParamKind(d *typeParamDef) kind { return kind{tag: tagTypeParam, tp: d} }

// typeParamsNeedDictionary reports whether fd's type parameters carry any
// constraint that makes its body dispatch-dependent, and so belongs to the
// dictionary seam rather than here.
//
// TRUE for any bound on any type parameter and for any `where` clause. Both
// halves are read, not just one: inline `<T: Comparable>` is a PARSE ERROR in
// Nomi (parseTypeParams rejects it by name and tells the programmer to write a
// `where` clause), so every source-written bound arrives in WhereClauses — but
// `TypeParam.Bounds` carries bounds the ANALYZER synthesized
// (`analysis/builder.go`, `analysis/derive_synthesis.go`), and irbuild reads the
// AST after analysis. Reading only WhereClauses would therefore admit a
// synthesized bound as bound-free.
//
// `Debug` is not special-cased even though the analyzer erases it as universal.
// Any bound means the seam, so there is one rule instead of a rule plus an
// exception nobody re-checks.
func typeParamsNeedDictionary(fd *ast.FuncDef) bool {
	if len(fd.WhereClauses) > 0 {
		return true
	}
	for _, tp := range fd.TypeParams {
		if len(tp.Bounds) > 0 {
			return true
		}
	}
	return false
}

// genericSignature resolves a generic `fn`'s parameter and result kinds with its
// type parameters as kinds of their own, or names why it cannot.
//
// Answers why == "" and tps == nil for a NON-generic declaration, so signature()
// can call it unconditionally and keep one place that decides.
func (g *gen) genericSignature(fd *ast.FuncDef) (params []kind, result kind, tps []*typeParamDef, why string) {
	if len(fd.TypeParams) == 0 && len(fd.WhereClauses) == 0 {
		return nil, kindInvalid, nil, ""
	}
	refuse := func() ([]kind, kind, []*typeParamDef, string) {
		return nil, kindInvalid, nil, "generic function"
	}
	if typeParamsNeedDictionary(fd) || len(fd.TypeParams) == 0 {
		return refuse()
	}
	// A generic `fn` reaching funcDecl through any of these is refused by
	// funcDecl's own clause for that reason, at its own position — so it must
	// not be admitted here and lowered past those clauses.
	if fd.ImplFunction || len(fd.Decorators) > 0 || fd.Body == nil || fd.Name == "main" {
		return refuse()
	}
	byName := make(map[string]*typeParamDef, len(fd.TypeParams))
	tps = make([]*typeParamDef, len(fd.TypeParams))
	for i, tp := range fd.TypeParams {
		d := &typeParamDef{nomi: tp.Name, fn: fd.Name}
		tps[i] = d
		byName[tp.Name] = d
	}
	// bare answers the kind a signature position names, resolving a LONE type
	// parameter to its own kind and everything else through typeOf. A type
	// parameter mentioned INSIDE a larger type is not resolved: typeOf has no
	// arm for one and answers kindInvalid, which is the refusal. So the depth
	// rule holds by construction rather than by a second walk that could
	// disagree with it.
	bare := func(te ast.TypeExpr) kind {
		if st, isSimple := te.(*ast.SimpleType); isSimple {
			if d, isParam := byName[st.Name]; isParam {
				return typeParamKind(d)
			}
		}
		return g.typeOf(te)
	}
	used := make(map[*typeParamDef]bool, len(tps))
	params = make([]kind, len(fd.Params))
	for i, p := range fd.Params {
		switch {
		case p.Destructure != nil, p.TypeAnnotation == nil, p.Default != nil:
			return refuse()
		}
		k := bare(p.TypeAnnotation)
		// A type parameter mentioned inside a larger type lands here, and that
		// is the depth rule: funcDecl reports `generic function` for the whole
		// declaration at its own position.
		// kindInvalid: reports — funcDecl names `generic function` at the declaration.
		if k == kindInvalid {
			return refuse()
		}
		if k.tag == tagTypeParam {
			used[k.tp] = true
		}
		params[i] = k
	}
	// Every type parameter must be inferable from a parameter. One that is not
	// cannot be produced inside the body either, so admitting it would emit a
	// function with an argument nothing can supply.
	for _, d := range tps {
		if !used[d] {
			return refuse()
		}
	}
	result = kindUnit
	if fd.ReturnTypeExpr != nil {
		result = bare(fd.ReturnTypeExpr)
		// kindInvalid: reports — funcDecl names `generic function` at the declaration.
		if result == kindInvalid {
			return refuse()
		}
	}
	return params, result, tps, ""
}

// refuseGenericTailCycles clears lowerability for every generic function that
// sits on a tail cycle, once the graph exists to say so.
//
// Called from declareFuncs AFTER buildTailGraph, because that is the earliest
// the answer exists: signature() runs before the graph is built. Both sides then
// agree by construction — directCall reads `lowerable` and funcDecl reports
// `genericWhy`, and neither re-derives the decision.
//
// The obstacle is real and reachable rather than defensive.
// `fn count<T>(x: T, n: Int): Int { if n == 0 { 0 } else { count(x, n - 1) } }`
// is a tail self-call under Nomi's constant-stack guarantee (spec §12.7), and
// decideTailPlan marks a single-member component `ok` WITHOUT calling
// tailMemberSig — so nothing else would stop it becoming unbounded Go
// recursion. Pinned by testdata/generic_tail_self.nomi.
//
// TWO POPULATIONS. The erased population is `sig.tps != nil`; the
// MONOMORPHIZED one is `sig.mono != nil` and has `tps == nil`. Checking only
// the first would skip the second, and because funcDecl returns early for a
// template, a tail-recursive generic at depth would be claimed and emitted and
// grow the stack without bound with nothing reporting. Clearing `mono` puts funcDecl's `recursive tail call` report in
// charge, so the refusal stays at the DECLARATION rather than at the call.
func (g *gen) refuseGenericTailCycles() {
	for _, sig := range g.funcs {
		if sig.decl == nil {
			continue
		}
		generic := sig.lowerable && sig.tps != nil
		template := sig.mono != nil
		if !generic && !template {
			continue
		}
		if g.tailPlanForFn(sig.decl.Name) != nil {
			sig.lowerable = false
			sig.mono = nil
			sig.genericWhy = "recursive tail call"
		}
	}
}
