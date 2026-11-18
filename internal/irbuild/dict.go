package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Bounded generic functions: `T.method(x)` where `T` is a bounded type
// parameter of the enclosing function.
//
// # What the seam holds
//
// A type parameter's interface bound names decide which interface a
// `T.method` call goes through. That is a compile-time fact: the bound picks
// the *ifaceDef before anything runs.
//
// Bound names cannot say which impl. For
// `FromJson.from_json(json: Json) -> Result<self, …>`, self occurs only in
// the return, so the first argument's runtime type is `Json` and selects no
// `impl FromJson for List<T>`. The impl follows from the concrete type
// argument: a call or a function value lowers an instance of the function at
// that argument (irMonoTemplate, irfuncbody.go), and the body under the
// seam's own signature has no IR (funcDecl, native.go).
//
// # A bounded type parameter is an existential over its bound
//
// In the seam's signature `T` has the kind an interface-typed parameter has,
// `existential(bound)`. `zeroSized`, `nomi`, `packageNeutral` and equality
// already answer for `tagIface`.
//
// # Scope
//
// Exactly one type parameter, whose bounds each resolve to an interface this
// gen can see. Two or more type parameters decline, and they get no refusal
// of their own: declining leaves genericSignature (generic.go) to decide the
// signature.
//
// Generic impl blocks, generic impl functions, generic interfaces and generic
// type declarations are not handled here.

// dictTypeParam is one bounded type parameter as the builder carries it.
type dictTypeParam struct {
	// nomi is the type parameter's Nomi name, `T`.
	nomi string
	// bounds are the interfaces the type parameter's bounds resolved to, in
	// source order. Never empty, and every element non-nil.
	//
	// A list and not one interface because `where T: Display and Greet` is
	// one type parameter with two contracts.
	bounds []*ifaceDef
}

// repr is the bound the type parameter's kind is an existential over: the
// first. The iface in the kind records which contract a position was
// accepted under, and there is exactly one per type parameter, so a variable
// of type `T` has one kind whichever bound a reader names.
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

// --- collecting the bounds ---------------------------------------------------

// typeParamBound is one type parameter's name and the base names of its
// interface bounds.
type typeParamBound struct {
	name   string
	bounds []string
}

// typeParamBoundsOf merges a function's `<T>` header with its `where` clauses
// into one bound list per type parameter, deduped, in declaration order.
//
// Both are read because neither is the whole answer: `ast.TypeParam.Bounds`
// carries internal and synthesized constraints only (internal/ast/ast.go's own comment),
// source constraints live in WhereClauses, and the parser rejects the inline
// spelling: `fn f<T: Shout>(x: T)` is a parse error advising `<T>` plus
// `where T: Shout`.
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
// It declines and never refuses: declaredSignature (native.go) asks
// genericSignature next, which records the construct funcDecl reports, and a
// second key for the same declaration would report one refusal twice.
//
// One type parameter, with any number of bounds each resolving to a lowerable
// interface. Two or more type parameters decline.
func (g *gen) dictSeamFor(fd *ast.FuncDef) (*dictSeam, bool) {
	bounds := typeParamBoundsOf(fd.TypeParams, fd.WhereClauses)
	// The bounds nobody wrote. `Debug` is universal, so a developer inspecting a
	// value writes `fn f<T>(x: T) { io.inspect(x) }` with no annotation and the
	// checker rejects nothing. Without the inferred bound this function would
	// have no bound to resolve.
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
	// A bounded type parameter at depth in a parameter annotation: `m: Maybe<T>`
	// and not `x: T`. The seam's signature cannot serve the shape at all.
	//
	// A bounded type parameter is an existential over its bound
	// (dictTypeParamKind), so `m: Maybe<T>` resolves to `Maybe<Display>` and the
	// signature demands a container whose PAYLOAD is already erased. For
	// `fn label<T>(prefix: String, m: Maybe<T>) -> String where T: Display`:
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
	// shape (`fn wrap<T>(m: Maybe<T>) -> Int`): in an instance the bound
	// dissolves — `T` is `Int` and `Display.to_string(v)` is an ordinary
	// concrete dispatch. The `where T: Struct` arm below declines for the same
	// reason.
	//
	// PARAMETERS ONLY. A type parameter at depth in the RETURN (`fn decode<T>(
	// json: Json) -> Result<T, ShapeError> where T: FromJson`) is NOT declined
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
		if d.decl != nil && len(d.decl.Methods) == 0 {
			// A bound that declares nothing to dispatch: no function
			// requirement. The seam exists for a `T.method(x)` inside the
			// body, and such a bound has no method to call.
			//
			// Declining leaves the declaration to monomorphization:
			// declaredSignature asks `dictSignature` first and
			// `genericSignature` and `monoTemplateFor` on decline, so a
			// decline here is a handoff and not a refusal.
			//
			// THE DECLARATION AND NOT `d.order`. `d.order` is filled by
			// `resolveIfaces`, which runs AFTER `declareFuncs`, and
			// `declareFuncs` is where `dictSeamFor` is called from. So a
			// `len(d.order) == 0` test reads zero for EVERY interface at this
			// point in the pipeline and would decline a plain
			// `interface Shout { fn shout(v: self) -> String }`. The declaration
			// is available at declare time; the resolution is not.
			//
			// `std/structs.Struct` is the case this arm exists for: a universal
			// structural marker whose one declared function is not
			// dispatchable, so structiface.go's canonical declaration carries no
			// methods. Without this decline `where T: Struct` would give a shape
			// like `11-interfaces-and-impls/structural_interfaces` a seam
			// signature with no method behind it.
			return nil, false
		}
		defs = append(defs, d)
	}
	return &dictSeam{params: []dictTypeParam{{
		nomi:   bounds[0].name,
		bounds: defs,
	}}}, true
}

// dictBoundIface resolves a type parameter's bound to its *ifaceDef: this
// module's own declaration, a sibling file's mirror, or a stdlib interface's
// shared def, in that order.
//
// The order is `stdIfaceNamed`'s own and is asked for through it, so a user
// declaration wins here for the same reason it wins at every other position.
// See stdiface.go, which states why the analyzer's module scope is not a
// shadow oracle for interfaces.
//
// A bound needs the stdlib arm as every other interface position does.
// `Display` and `Comparable` carry `stdIfaceSpecs` rows and a process-wide
// `*ifaceDef`, read by an annotation (types.go), an `impl` header (impl.go),
// a cross-file kind (foreign.go) and a refusal reason (sigreason.go).
// `ifaceNamed` alone knows nothing about specs, so without it
// `where T: Display` would decline the seam while the same interface in a
// parameter position lowered.
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
// A bounded type parameter is an existential over its bound. typeOf calls
// this, so every composed spelling, `T`, `List<T>` or `(T) -> Bool`, follows
// from it and needs no arm of its own.
//
// With several bounds it is the repr bound, for the reason `repr` states.
func (g *gen) dictTypeParamKind(name string) (kind, bool) {
	tp := g.dict.at(name)
	if tp == nil {
		return kindInvalid, false
	}
	return existential(tp.repr()), true
}
