package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The type-parameter dispatch seam: `T.method(x)` where `T` is a BOUNDED type
// parameter of the enclosing function.
//
// # The seam has two parts, and conflating them is a bug
//
// Part (a), WHICH INTERFACE. A type parameter's interface bound names decide
// which interface a `T.method` call goes through. That is a compile-time fact:
// the bound picks the *ifaceDef, and therefore the dispatch table, before
// anything runs.
//
// Part (b), WHICH IMPL. Bound names can never answer this. For
// `FromJson.from_json(json: Json): Result<self, …>`, self occurs only in the
// RETURN, so a dispatch on the first argument's runtime type probes `Json`
// and every `impl FromJson for List<T>` is unreachable. The answer needs a
// SEPARATE binding carrying the concrete type ARGUMENT.
//
// That binding is a parameter: one `*rt.TypeID` per bounded type
// parameter, immediately after `fr`. `rt.Method.At(tid)` is the lookup, beside
// the receiver-driven `Get(d Dyn)` and delegated to by it, so the runtime has
// exactly one map probe and one trap message.
//
// # Why a bounded type parameter is an EXISTENTIAL over its bound
//
// `T` lowers to the kind an interface-typed parameter already has —
// `existential(bound)`, i.e. `rt.Dyn`. Not a new tag, and that is the whole
// reason: `coerce` already boxes a concrete value into an existential at the
// position the checker accepted the widening, table dispatch already handles
// one, and `zeroSized`, `nomi`, `packageNeutral`, inspect and equality all
// already answer for `tagIface`. A `tagTypeParam` would have to be added to
// each of those switches correctly, and the failure mode of missing one is a
// silent wrong answer rather than a refusal.
//
// # The dictionary is required at EVERY call, and the fallback is REFUSED
//
// There is one route, because the choice can be made statically and the
// residue is small enough to refuse:
//
//   - a turbofish gives the type argument directly (`origin<Meters>()`);
//   - with no turbofish, `T` is solved from the ARGUMENTS' static kinds, which
//     is more precise than probing a receiver's runtime type;
//   - inside another generic, an argument already erased to `T`'s bound
//     resolves to the ENCLOSING seam's own dictionary, resolving each argument
//     through the caller's own bindings first, and it is what makes a bounded
//     generic self-recursive;
//   - and when none of those answers, the call is REFUSED by name rather than
//     emitted against a nil dictionary. Such a program checks clean and has no
//     type argument to dispatch on (`Zero.zero()` with no argument), so a
//     refusal is the fail-safe direction and not a lost capability.
//
// One route means no run-time branch in the lowered body, and — more
// importantly — no hand-written walk over the body to discover whether the
// dictionary is needed. A walk that skips a node kind looks finished from every
// angle except the one it skips.
//
// # Scope
//
// Exactly ONE type parameter, with exactly ONE bound that resolves to an
// interface this gen can see. Two or more type parameters DECLINE — they do not
// get a refusal of their own — because a turbofish's positional order is by
// first appearance across the parameter and return types, which is a no-op
// only for a single parameter. Getting that ordering wrong binds the wrong type
// to the wrong parameter, in silence. Declining leaves the `generic function`
// refusal in charge.
//
// Generic IMPL blocks, generic impl functions, generic interfaces and generic
// type declarations keep their own refusals. So the impl half of part (b),
// unifying an impl's receiver type-expression against a call-site type
// argument, is NOT handled here.

// dictTypeParam is one bounded type parameter as the builder carries it.
type dictTypeParam struct {
	// nomi is the type parameter's Nomi name, `T`.
	nomi string
	// bounds are the interfaces the type parameter's bounds resolved to, in
	// SOURCE order. Part (a): they decide the dispatch table, at compile time.
	// Never empty, and every element non-nil.
	//
	// A LIST rather than one interface because `where T: Display and Greet` is
	// one type parameter with two contracts. The bounds are scanned for one
	// whose interface declares the method, so the method decides which table,
	// per call, and the list is the thing scanned.
	bounds []*ifaceDef
}

// repr is the bound a value of this type parameter is BOXED under.
//
// The first, and the choice is arbitrary on purpose rather than for want of a
// better one: `existential(d)` renders `rt.Dyn` for every d — `{TID, V}`, which
// carries no interface identity at run time — so all of a type parameter's
// bounds produce the same Go type and the same value. The iface in the KIND is
// compile-time bookkeeping about which contract a position was accepted under,
// and every site that compares it is taught below to accept any bound of the
// same type parameter.
//
// What is NOT arbitrary is that there is exactly one of them. Two boxes for one
// value would be two kinds for one variable, and `coerce` would then have a
// wanted type that depends on which bound a reader happened to name.
func (p *dictTypeParam) repr() *ifaceDef { return p.bounds[0] }

// dictSeam is one generic function's type parameters.
type dictSeam struct {
	params []dictTypeParam
}

// at resolves a type-parameter NAME, or nil.
func (s *dictSeam) at(name string) *dictTypeParam {
	if s == nil {
		return nil
	}
	for i := range s.params {
		if s.params[i].nomi == name {
			return &s.params[i]
		}
	}
	return nil
}

// --- part (a): collecting the bounds ----------------------------------------

// typeParamBound is one type parameter's name and the base names of its
// interface bounds.
type typeParamBound struct {
	name   string
	bounds []string
}

// typeParamBoundsOf merges a function's `<T>` header with its `where` clauses
// into one bound list per type parameter, deduped, in declaration order.
//
// Both are read because the two fields are not interchangeable and neither is the whole answer: `ast.TypeParam.Bounds`
// carries INTERNAL/SYNTHESIZED constraints only (ast.go's own comment), source
// constraints live in WhereClauses, and the parser REJECTS the inline spelling
// outright — `fn f<T: Shout>(x: T)` is a parse error advising `<T>` plus
// `where T: Shout`. So reading either field alone answers for a population that
// does not exist.
func typeParamBoundsOf(tps []ast.TypeParam, where []ast.WhereConstraint) []typeParamBound {
	if len(tps) == 0 {
		return nil
	}
	out := make([]typeParamBound, 0, len(tps))
	index := make(map[string]int, len(tps))
	for _, tp := range tps {
		if _, seen := index[tp.Name]; seen {
			continue
		}
		index[tp.Name] = len(out)
		out = append(out, typeParamBound{name: tp.Name})
	}
	add := func(name, bound string) {
		i, ok := index[name]
		if !ok || bound == "" {
			return
		}
		for _, existing := range out[i].bounds {
			if existing == bound {
				return
			}
		}
		out[i].bounds = append(out[i].bounds, bound)
	}
	for _, tp := range tps {
		for _, b := range tp.Bounds {
			add(tp.Name, analysis.TypeExprBaseName(b))
		}
	}
	for _, wc := range where {
		for _, b := range wc.Bounds {
			add(wc.Name, analysis.TypeExprBaseName(b))
		}
	}
	return out
}

// withInferredBounds adds the bounds the CHECKER inferred for fd's type
// parameters to the ones its source spells.
//
// The analyzer's list is authoritative about which interface a type parameter is
// USED at, and it exists because a `Debug` bound rejects nothing so nobody ever
// had to write one. See analysis.FileAnalysis.InferredTypeParamBounds for the
// soundness argument, which is specific to a UNIVERSAL interface and does not
// generalise.
//
// A module with no analysis — the single-module fixture path — gets the written
// bounds unchanged, so every existing caller behaves as before.
func (g *gen) withInferredBounds(fd *ast.FuncDef, bounds []typeParamBound) []typeParamBound {
	if g.fa == nil || len(bounds) == 0 {
		return bounds
	}
	inferred := g.fa.InferredTypeParamBounds[fd]
	if len(inferred) == 0 {
		return bounds
	}
	for i := range bounds {
		for _, name := range inferred[bounds[i].name] {
			if name == "" {
				continue
			}
			dup := false
			for _, existing := range bounds[i].bounds {
				if existing == name {
					dup = true
					break
				}
			}
			if !dup {
				bounds[i].bounds = append(bounds[i].bounds, name)
			}
		}
	}
	return bounds
}

// boundedParamAtDepth reports whether `name` appears inside a PARAMETER
// annotation at any position other than the whole annotation being that bare
// name.
//
// `x: T` is bare and answers false; `m: Maybe<T>`, `xs: List<T>` and
// `f: (T) -> Bool` all answer true. See dictSeamFor's depth decline for why.
//
// The traversal is markMentionedParams, genericmono.go's, rather than another
// walk over ast.TypeExpr: it already answers "does this annotation name this
// type parameter at any depth" per name, and the two callers are asking one
// question about one grammar. A private walk here would be a second answer that
// drifts the first time the type grammar grows an arm.
func boundedParamAtDepth(fd *ast.FuncDef, name string) bool {
	set := map[string]bool{name: true}
	for _, p := range fd.Params {
		if p.TypeAnnotation == nil {
			continue
		}
		if st, bare := p.TypeAnnotation.(*ast.SimpleType); bare && st.Name == name {
			continue
		}
		mentioned := map[string]bool{}
		markMentionedParams(p.TypeAnnotation, set, mentioned)
		if mentioned[name] {
			return true
		}
	}
	return false
}

// dictSeamFor builds the seam for a generic function, or declines.
//
// Declines rather than refuses, in every arm and on purpose: everything it turns
// down is already refused as `generic function` at the declaration, and a second
// key for the same declaration would report one refusal twice. See
// unsupported.go, and the file comment above for why 2+ type parameters is one
// of those arms rather than a capability.
//
// ONE type parameter, with ANY NUMBER of bounds each resolving to a lowerable
// interface. The two limits have different reasons: 2+ type parameters
// decline because a TURBOFISH's positional order is
// `orderTypeParamsForTurbofish`'s and re-encoding it here would be a second
// copy of a rule whose failure mode is silent, whereas a second BOUND on one
// type parameter adds no position to order — it adds a contract, and
// `dictTypeParam.declares` picks the table per method exactly as
// `typeParamDispatchIface` does.
func (g *gen) dictSeamFor(fd *ast.FuncDef) (*dictSeam, bool) {
	bounds := typeParamBoundsOf(fd.TypeParams, fd.WhereClauses)
	// The bounds nobody WROTE. `Debug` is universal, so a developer inspecting a
	// value writes `fn f<T>(x: T) { io.inspect(x) }` with no annotation and the
	// checker rejects nothing. Without the inferred bound this function would
	// have no bound to resolve, and the body's `Debug.inspect(x)` would refuse
	// at a `tagTypeParam` receiver that carries no identity to dispatch on.
	//
	// The checker records the bound its own conformance check inferred
	// (`recordInferredBoundOnTypeParam`), keyed on the DECLARATION NODE. Merged
	// here rather than inside `typeParamBoundsOf` on purpose: that function is
	// the AST-only derivation, and keeping an analyzer-sourced bound out of it
	// keeps it a function of the source alone.
	//
	// ADDITIVE and per-parameter: a written bound still decides, and a
	// declaration the checker recorded nothing for gets its written bounds
	// only. So this can only turn a refusal into a lowering, never the reverse.
	bounds = g.withInferredBounds(fd, bounds)
	if len(bounds) != 1 || len(bounds[0].bounds) == 0 {
		return nil, false
	}
	// A BOUNDED TYPE PARAMETER AT DEPTH IN A PARAMETER ANNOTATION — `m: Maybe<T>`
	// rather than `x: T`. The dictionary cannot serve the shape at all.
	//
	// Part (a) makes a bounded type parameter an existential over its bound
	// (dictTypeParamKind), so `m: Maybe<T>` resolves to `Maybe<Display>` and the
	// signature demands a container whose PAYLOAD is already erased. For
	// `fn label<T>(prefix: String, m: Maybe<T>): String where T: Display`:
	//
	//	label("a: ", Some(1))        argument type mismatch | Maybe<Display> got Maybe<Int>
	//	label<Int>("a: ", Some(1))   IDENTICAL — the turbofish changes what SOLVES
	//	                             the parameter, not what the signature declares
	//	m: Maybe<Display> = Some(1)  binding type mismatch — source cannot even
	//	                             CONSTRUCT the value the signature wants
	//	both<T>(x: T, m: Maybe<T>)   still refuses, so a bare occurrence elsewhere
	//	                             does not rescue the depth one
	//
	// So the served population is EMPTY, and the fourth row is why the test is
	// "any depth occurrence" rather than "no bare occurrence": one depth position
	// is enough to make the call unwritable.
	//
	// Declining hands the declaration to monomorphization, which lowers the
	// shape (`fn wrap<T>(m: Maybe<T>): Int`): in an instance the bound
	// dissolves — `T` is `Int` and `Display.to_string(v)` is an ordinary
	// concrete dispatch. The `where T: Struct` arm below declines for the same
	// reason.
	//
	// PARAMETERS ONLY. A type parameter at depth in the RETURN (`fn decode<T>(
	// json: Json): Result<T, ShapeError> where T: FromJson`) is NOT declined
	// here, because the argument above is about what a caller must pass.
	if boundedParamAtDepth(fd, bounds[0].name) {
		return nil, false
	}
	// A BOUND ALIAS is a NAME for a conjunction, expanded to the names it
	// spells before the resolver sees any of them. `where T: ShowAndTag` and
	// `where T: Showable and Tagged` then reach `dictBoundIface` with the same
	// list, which is the whole of it — the conjunction itself already has a
	// representation here, because `bounds` is a list. See typealias.go.
	//
	// Names only, and stopping at `dictBoundIface`: whatever that resolver
	// turns down is turned down identically whichever spelling produced the
	// name, so the expansion cannot admit a bound the direct spelling refuses.
	names := g.expandBoundAliases(bounds[0].bounds)
	if len(names) == 0 {
		return nil, false
	}
	defs := make([]*ifaceDef, 0, len(names))
	for _, name := range names {
		d, found := g.dictBoundIface(name)
		if !found || !d.lowerable {
			return nil, false
		}
		if d.decl != nil && len(d.decl.Methods) == 0 && len(d.decl.Fields) == 0 {
			// A bound that DECLARES NOTHING TO DISPATCH — no function and no
			// field requirement. A dictionary exists to carry the identity a
			// `T.method(x)` or a `x.field` inside the body dispatches on, so
			// such a bound gives it nothing to do: the seam would thread a
			// `*rt.TypeID` no emitted line reads.
			//
			// Declining leaves the declaration to monomorphization, which is the
			// right owner for it — `signature()` asks `dictSignature` first and
			// `monoTemplateFor` on decline, so a decline here is a HANDOFF and
			// not a refusal.
			//
			// THE DECLARATION AND NOT `d.order`. `d.order` is filled by
			// `resolveIfaces`, which runs AFTER `declareFuncs`, and
			// `declareFuncs` is where `dictSeamFor` is called from. So a
			// `len(d.order) == 0` test reads zero for EVERY interface at this
			// point in the pipeline and would decline a plain
			// `interface Shout { fn shout(v: self): String }`. The declaration
			// is available at declare time; the resolution is not.
			//
			// `std/structs.Struct` is the case this arm exists for: a universal
			// structural marker whose one declared function is not
			// dispatchable, so structiface.go's canonical declaration carries no
			// methods. Without this decline `where T: Struct` would move a shape
			// like `11-interfaces-and-impls/structural_interfaces` off
			// monomorphization and onto a dictionary with nothing in it.
			return nil, false
		}
		defs = append(defs, d)
	}
	return &dictSeam{params: []dictTypeParam{{
		nomi:   bounds[0].name,
		bounds: defs,
	}}}, true
}

// dictBoundIface resolves a type parameter's BOUND to the *ifaceDef whose
// dispatch table part (a) selects: this module's own declaration, a sibling
// file's mirror, or a stdlib interface's shared def, in that order.
//
// The order is `stdIfaceNamed`'s own and is asked for through it rather than
// re-spelled, so a user declaration wins here for the same reason it wins at
// every other position — see stdiface.go, which states why the analyzer's
// module scope is not a shadow oracle for interfaces.
//
// A bound needs the stdlib arm as every other interface position does.
// `Display` and `Comparable` carry `stdIfaceSpecs` rows and a process-wide
// `*ifaceDef`, read by an annotation (types.go), an `impl` header (impl.go), an
// inferred position (`projectStdIface`), a cross-file kind (foreign.go) and a
// refusal reason (sigreason.go). `ifaceNamed` alone knows nothing about specs,
// so `where T: Display` would decline the seam while the SAME interface in a
// PARAMETER position lowered.
//
// Source-level tests cannot see that asymmetry, because another spelling of
// the same question works.
func (g *gen) dictBoundIface(name string) (*ifaceDef, bool) {
	if d, user := g.ifaceNamed(name); user {
		return d, true
	}
	return g.stdIfaceNamed(name)
}

// dictSignature resolves a bounded generic function's signature, or declines.
//
// The seam is installed while the parameter and return annotations are
// resolved, so `T` and `List<T>` both answer through typeOf's type-parameter
// arm rather than through a second projection of their own.
func (g *gen) dictSignature(fd *ast.FuncDef) (*fnSig, bool) {
	seam, ok := g.dictSeamFor(fd)
	if !ok {
		return nil, false
	}
	sig := &fnSig{decl: fd, lowerable: true, dict: seam}
	prev := g.dict
	g.dict = seam
	defer func() { g.dict = prev }()
	for _, p := range fd.Params {
		k := g.paramKind(p)
		// kindInvalid: reports — funcDecl rejects the parameter's own reason at its own position.
		if k == kindInvalid {
			sig.lowerable = false
		}
		sig.params = append(sig.params, k)
	}
	sig.result = kindUnit
	if fd.ReturnTypeExpr != nil {
		sig.result = g.typeOf(fd.ReturnTypeExpr)
		// kindInvalid: reports — funcDecl rejects the return type's own reason at its own position.
		if sig.result == kindInvalid {
			sig.lowerable = false
		}
	}
	if len(fd.Decorators) > 0 || fd.Body == nil {
		sig.lowerable = false
	}
	return sig, true
}

// dictTypeParamKind is the kind a type-parameter NAME denotes, and reports
// whether the name is one.
//
// Part (a) in one line: a bounded type parameter IS an existential over its
// bound. typeOf calls this, so every composed spelling — `T`, `List<T>`,
// `(T) -> Bool` — follows from it rather than needing an arm of its own.
//
// With several bounds it is the REPR bound, for the reason `repr` states: every
// bound renders the same `rt.Dyn` and carries no interface identity at run time,
// so the choice is bookkeeping and there must be exactly one of it.
func (g *gen) dictTypeParamKind(name string) (kind, bool) {
	tp := g.dict.at(name)
	if tp == nil {
		return kindInvalid, false
	}
	return existential(tp.repr()), true
}

// --- part (b): the emitted dictionary ---------------------------------------

// --- the identity a self position must carry ---------------------------------

// --- call sites -------------------------------------------------------------
