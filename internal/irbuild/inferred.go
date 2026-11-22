package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The analyzer's answers, projected into this builder's own type lattice.
//
// # Why there is a channel at all
//
// native.go's second invariant says a type must be known before a value can be
// lowered. The checked AST carries only the annotations the programmer wrote,
// but the analysis carries more: a lambda parameter with no annotation IS
// solved — checkLambdaExpecting (analysis/checker.go) takes the expected
// FuncType at the call site and attachParamType records the answer on the
// parameter's SymbolParam in fa.Definitions, keyed by the parameter's own
// source position. That is the fact LSP hover renders, and this file reads it.
//
// So this file is PLUMBING plus one projection. It is not a second inference
// pass: nothing here derives a type. Re-deriving an expected type inside the
// builder would be a second type checker that can disagree with the real one, and the
// failure mode of a builder that disagrees with the front end is a program
// that checks and behaves differently — the exact outcome the refusal
// machinery exists to prevent.
//
// # The projection is where the judgement is, and its rule is subtractive
//
// `kind` is this builder's representation lattice: six scalars, a declared
// named type, and three structural shapes (see native.go). `analysis.Type` is
// the whole language's type system, which is strictly larger. Projecting one
// into the other can go wrong in exactly one direction that matters — widening
// `kind` to make a projection succeed — because that turns `kind` into a
// second, disagreeing model of `analysis.Type` and the two then drift with
// nothing failing.
//
// The rule is therefore subtractive and it has no default arm: every
// analysis.Type this builder cannot already represent returns kindInvalid, and
// the CALLER refuses by name. Nothing here guesses, nothing here extends
// `kind`, and no arm exists because a projection was wanted — every arm below
// mirrors a representation types.go, collections.go or lambda.go already
// builds.
//
// # Nominal identity is checked, not assumed
//
// A named type projects only when it is THIS module's own declaration (or a
// sibling's, through a mirror), and "own" is the analyzer's own identity rule —
// `(Origin, Name)` — not a name match. A projection that accepted `Point` from
// any module would match a type identity by its name, and its symptom would be
// a kind mismatch at best and a wrong field read at worst. The
// declaration SHAPE is cross-checked too (an analysis struct against a
// struct def, an enum against an enum), so a disagreement refuses rather than
// lowering against the wrong layout.
//
// # The safety net, stated because it is load-bearing
//
// A projected parameter kind is not trusted on its own. A lambda only reaches
// a lowered body through a position whose kind the builder derived
// independently: an argument is checked against the callee's declared
// parameter kind, an annotated binding
// against its annotation, a `return` against the declared result. So a
// wrong projection is caught as a kind mismatch and refused. That is what makes this cheap: the
// projection has to be right to LOWER, and only has to be honest to be SAFE.

// boolOrigin and boolName are the nominal identity of the one type this
// builder special-cases.
//
// `Bool` is not a primitive in Nomi — it is `pub enum Bool { embeds False;
// embeds True }` (std/bool.nomi), so the checker hands back an *EnumType and
// not a *PrimitiveType. native.go's kind comment states `Bool → bool` as a
// compiler special case rather than a table row precisely so it stays visible;
// this is the same special case on the projection side, and it is spelled as
// the type's nominal identity for the reason the section above gives.
const (
	boolOrigin = "std/bool"
	boolName   = "Bool"
)

// paramSymbolType is the type the checker gave a parameter written with no
// annotation and no pattern, which it records on the parameter's symbol
// (analysis's attachParamType), or nil. The checker gives every such
// parameter of a program it accepts a type: the function type its lambda's
// position expects, the parameter at the same place in another element of
// its collection literal, or its default's; a lambda parameter nothing
// types is its "cannot infer type for parameter" error. A discard
// parameter (`fn _ -> 7`) has a symbol named "_" too.
func paramSymbolType(fa *analysis.FileAnalysis, p ast.Param) analysis.Type {
	if fa == nil {
		return nil
	}
	// Position, kind and name, as attachParamType records it, so no other
	// symbol at the position is read.
	sym, found := fa.Definitions[analysis.Pos{Line: p.Line, Col: p.Col}]
	if !found || sym.Kind != analysis.SymbolParam || sym.Name != p.Name {
		return nil
	}
	return sym.Type
}

// onceSymbolType is the type the checker gave a `once` written with no
// annotation, which checkOnce records on the binding's symbol (the record
// hover reads), or nil.
func onceSymbolType(fa *analysis.FileAnalysis, ob *ast.OnceBinding) analysis.Type {
	if fa == nil {
		return nil
	}
	// Position, kind and name, as checkOnce records it.
	sym, found := fa.Definitions[analysis.Pos{Line: ob.Line, Col: ob.Col}]
	if !found || sym.Kind != analysis.SymbolOnce || sym.Name != ob.Name {
		return nil
	}
	return sym.Type
}

// project is the analysis.Type → kind projection. See the file comment for why
// it has no default arm.
func (g *gen) project(t analysis.Type) kind {
	switch ty := t.(type) {
	case nil:
		return kindInvalid

	case *analysis.TypeVar:
		// An inference variable that got solved is its solution, and one
		// the checker closed (a type no value is made of,
		// internal/analysis/closed_vars.go)
		// is its closed type.
		if ty.Resolved != nil {
			return g.project(ty.Resolved)
		}
		if ty.Closed != nil {
			return g.project(ty.Closed)
		}
		return kindInvalid

	case *analysis.TypeParam_:
		// A type parameter has no representation OF ITS OWN, which is why
		// typeOf has no arm for one either (generic.go:179-192 states that as
		// the depth rule). TWO things can say what it stands for, and this arm
		// must ask BOTH:
		//
		//   - inside a MONOMORPHIZED instance, a concrete argument, and the
		//     substitution frame is what says which;
		//   - inside a BOUNDED generic function, an EXISTENTIAL over its bound,
		//     and the dictionary seam is what says which.
		//
		// ONE arm here rather than one at each projected position, so the
		// annotation and the inferred position cannot disagree. Without the
		// frame half, `Maybe.Some(r.slot)` inside `fn recv<T>(r: Half<T>):
		// Maybe<T>` would refuse `generic enum over a type parameter` in a
		// monomorphized instance, because prelude.go's prelude projection asks
		// project(): the type argument would be concrete at the annotation and
		// abstract at the constructor. Without the dictionary half, `fn
		// wrap<T>(x: T): Maybe<T> where T: Display { Some(x) }` would refuse at
		// the constructor while its return ANNOTATION `Maybe<T>` lowered.
		// types.go's `*ast.SimpleType` arm consults both, and so does this one.
		//
		// Same ORDER as types.go's, so the two doors cannot disagree about
		// which wins: a substitution frame is pushed only inside an instance
		// and a dictionary seam only inside a bounded declaration, and where
		// both are somehow live the frame is the more specific answer.
		//
		// Keyed by NAME, which is generictype.go's own rule for the frame it
		// shares with this: the frame is pushed only for the length of one
		// instance's walk, and a nested generic declaration inside that walk is
		// refused, so no unrelated `T` is live inside the window.
		//
		// That argument is about the window and says nothing about the
		// OPERAND. So the rule for a caller: pass a type the ENCLOSING
		// declaration owns. A CONSTRUCTOR's own declared type parameters —
		// `Some: (T) -> Maybe<T>` — are a different declaration's and share
		// nothing with the frame but a letter; handing one in would make `fn
		// wrap<T>(x: T): Maybe<T> { Some(x) }` lower and `fn wrap<A>(x: A):
		// Maybe<A> { Some(x) }` refuse, on the spelling alone. Every caller
		// takes its operand from the checker's own solved type at this
		// position, which satisfies the rule by construction.
		//
		// With no gen (projectShared) there is no frame, no default and no
		// dictionary, so a type parameter has no kind.
		if g == nil {
			return kindInvalid
		}
		// A type parameter an abstract body leaves open is held as its
		// bounds (abstract.go), told by its identity, which no frame's
		// name can shadow.
		if open, isOpen := g.dc.openType(ty); isOpen {
			// This gen's kind of its bounds, or where this gen's view names
			// no bound (a std module's that does not import it), the kind
			// where it was opened, which for a std interface is the same
			// in every gen.
			// kindInvalid: sentinel — no kind of the bounds here falls to the opener's.
			if k := g.abstractHeldKind(open); k != kindInvalid {
				return k
			}
			return g.dc.open[open]
		}
		if k, bound := g.genericSubstKind(ty.Name_); bound {
			return k
		}
		// An interface's `self` (the checker's type for the `self` an
		// interface signature writes) is the implementor while one of its
		// defaults is lowered for it (irImplLower, emitIfaceDefault), and
		// has no kind anywhere else.
		if ty.Name_ == "self" {
			if ds := g.defaultSelf; ds != nil {
				return ds.recv
			}
			return kindInvalid
		}
		if k, isTypeParam := g.dictTypeParamKind(ty.Name_); isTypeParam {
			return k
		}
		return kindInvalid

	case *analysis.PrimitiveType:
		// Named one at a time rather than by exclusion: Decimal, Byte, Bytes
		// and Any are all *PrimitiveType and are answered by their own arms
		// below or not at all, so a default-to-scalar arm would give the
		// wrong kind to four of the eleven rows.
		switch ty {
		case analysis.TypeInt:
			return kindInt
		case analysis.TypeFloat:
			return kindFloat
		case analysis.TypeString:
			return kindString
		case analysis.TypeUnit:
			return kindUnit
		case analysis.TypeInfallible:
			return kindNever
		}
		// A BLESSED primitive with an rt type. Named through the same
		// singleton-pointer identity the annotation channel uses, so
		// `fn b -> Byte.to_int(b)` and `fn f(b: Byte)` cannot become two rows.
		// See stdhost.go.
		// kindInvalid: lookup — origin-backed hosts need a declaration anchor.
		if k := stdHostKindOfPrimitive(ty); k != kindInvalid {
			return k
		}
		return g.projectOriginHost(ty)

	case *analysis.StructType:
		if len(ty.TypeArgs) > 0 {
			// A GENERIC STD STRUCT — `Set<Int>`, `Range<Int>`, `Channel<Int>` —
			// which DOES have a `*typeDef`, interned process-wide per
			// instantiation. Asked before the refusal below because that
			// refusal's reason ("types.go refuses a generic declaration
			// outright") is true of a USER generic struct and false of these
			// three, so without this arm they would lower as an ANNOTATION and
			// refuse under INFERENCE — the asymmetry the Map arm below says
			// must not happen. See stdgenstruct.go's genStructOfType.
			args := make([]kind, len(ty.TypeArgs))
			for i, ta := range ty.TypeArgs {
				args[i] = g.project(ta)
			}
			if k, isGenStruct := g.genStructOfType(ty.Origin, ty.Name, args); isGenStruct {
				return k
			}
			// A generic instantiation the checker solved — `fn b -> b.item` over a
			// `Box<Inner>`. Answered so the annotated `b: Box<Inner>` and the
			// inferred one are not two different answers about one position,
			// which is the rule the Map arm below states. A template this
			// module does not declare, or an argument with no representation,
			// still returns kindInvalid, so the arm does not widen `kind`. See
			// generictype.go.
			//
			// AFTER the std arm above, matching typeOf's precedence: a program
			// declaring its own `Set` cannot shadow the std family through this
			// door either.
			if k, isUserGeneric := g.projectGenericInstance(ty.TypeParamDefs, ty.Origin, ty.Name, ty.TypeArgs); isUserGeneric {
				return k
			}
			// A generic instantiation with no representation. types.go refuses a
			// generic declaration outright (declModifiers), so there is no
			// *typeDef for `Pair<Int, String>` to be the identity of.
			return kindInvalid
		}
		// A stdlib RECORD struct — `Diagnostic`, `DateTime` — whose Go type rt
		// declares. Answered before localNamed and by (Origin, Name) rather
		// than through module scope, because the checker solves this type for
		// positions where the program never writes the NAME: `fn diagnostic ->`
		// over `compiler.check(src)`. Same rule as the EnumType arm's
		// projectStdEnum below. See stdstruct.go's stdStructOfType.
		if d, isStdStruct := stdStructOfType(ty.Origin, ty.Name); isStdStruct {
			return named(d)
		}
		return g.localNamed(ty, func(d *typeDef) bool {
			return !d.isEnum && !d.isDistinct
		})

	case *analysis.EnumType:
		if ty.Origin == boolOrigin && ty.Name == boolName {
			return kindBool
		}
		// The prelude enums are generic and have a representation: a def
		// interned per type-argument tuple. See prelude.go, whose identity
		// rule is this file's own.
		if k, isPrelude := g.projectPreludeEnum(ty); isPrelude {
			return k
		}
		// A MONOMORPHIC stdlib enum — `Ordering` — whose Go type rt also
		// declares. Answered here so the annotated `o: Ordering` and the
		// inferred `fn o ->` over one are not two different answers about one
		// parameter, which is the rule this file's Map arm below states.
		if k, isStdEnum := g.projectStdEnum(ty); isStdEnum {
			return k
		}
		if len(ty.TypeArgs) > 0 {
			// A user generic enum the checker solved (`Wrapper<Int>`), by
			// the struct arm's rule.
			if k, isUserGeneric := g.projectGenericInstance(ty.TypeParamDefs, ty.Origin, ty.Name, ty.TypeArgs); isUserGeneric {
				return k
			}
			return kindInvalid
		}
		return g.localNamed(ty, func(d *typeDef) bool { return d.isEnum })

	case *analysis.DistinctType:
		if ty.Subtype {
			// A subtype is its base (refinement.go), a generic
			// one's at its type arguments.
			return g.project(analysis.RefinementBase(ty))
		}
		// A GENERIC std HOST type — `Sender<Int>` — asked FIRST because it is
		// the one distinct family with type arguments, and the clause below
		// rejects every argument-carrying distinct outright. Same rule as the
		// EnumType arm above, where projectPreludeEnum precedes the
		// `len(TypeArgs) > 0` refusal for the same reason. See stdgenhost.go.
		if k, isGenHost := g.projectStdGenHost(ty); isGenHost {
			return k
		}
		// `Regex<(String)>`: a std host type whose type parameter is
		// phantom, one leaf for every argument. See stdhost.go.
		if k, isPhantom := projectPhantomHost(ty); isPhantom {
			return k
		}
		if len(ty.TypeArgs) > 0 {
			// A user generic distinct the checker solved (`Tagged<Int>`),
			// by the struct arm's rule.
			if k, isUserGeneric := g.projectGenericInstance(ty.TypeParamDefs, ty.Origin, ty.Name, ty.TypeArgs); isUserGeneric {
				return k
			}
			return kindInvalid
		}
		// A stdlib nil-inner MARKER — `ChannelClosed`. Beside the opaque arm
		// below and for its reason: an annotation channel without an inference
		// channel is two answers to one question about one type. Reached when
		// the checker solves `Result<Unit, ChannelClosed>`'s second argument
		// for a position the program never spelled. See stdgenhost.go.
		if k, isMarker := g.projectStdMarker(ty); isMarker {
			return k
		}
		// A std DISTINCT with an rt Go type — `Duration`, `Days` —
		// answered here for the reason the StructType and EnumType arms above
		// give for theirs: every `opaqueSpecs` row is reachable by ANNOTATION,
		// so it must be reachable by INFERENCE too.
		//
		// A miss here does not name this file or the type: `Some(Days(3))`
		// would refuse as `non-scalar type argument (Maybe<Days>)` through
		// prelude.go's preludeArgs, which calls project() on each type argument
		// and reports whatever comes back invalid.
		if k, isStd := g.projectStdOpaque(ty); isStd {
			return k
		}
		return g.localNamed(ty, func(d *typeDef) bool { return d.isDistinct })

	case *analysis.ListType:
		return g.listKind(g.project(ty.Elem))

	case *analysis.MapType:
		// The annotated spelling lowers through maps.go's `Map<K, V>`; this
		// arm stops the two spellings of one parameter from disagreeing.
		// `m: Map<String, Int>` lowering while `fn m ->` over the same value
		// refuses would not be a smaller subset, it would be two answers to
		// one question.
		return g.mapKind(g.project(ty.Key), g.project(ty.Val))

	case *analysis.TupleType:
		parts := make([]kind, len(ty.Elems))
		for i, e := range ty.Elems {
			parts[i] = g.project(e)
		}
		return g.tupleKind(parts)

	case *analysis.AnonStructType:
		// A RECORD the checker solved for an unannotated position — a lambda
		// parameter, a binding. Answered so the annotated `p: {x: Int}` and
		// the inferred one are not two different answers about one position,
		// which is the rule the Map arm above states.
		names := make([]string, len(ty.Fields))
		parts := make([]kind, len(ty.Fields))
		for i, f := range ty.Fields {
			names[i], parts[i] = f.Name, g.project(f.Type)
		}
		return g.anonStructKind(names, parts)

	case *analysis.FuncType:
		// A function type with defaults is its full parameter list: a call
		// that omits one goes through a declaration (a declared fn, a lambda
		// or partial bound to a name), whose suppliers the builder finds from
		// the value (callablePlan), and the checker requires every argument
		// of any other call through a function value. A function VALUE with
		// a method-level `where` bound over a parameter no frame binds is
		// dispatch, and is refused; inside an instance that binds each
		// bounded parameter (`Bag.add` passed in `Bag<Int>`'s impl), the
		// instance meets the bound and the value is its plain function.
		if ty.Return == nil {
			return kindInvalid
		}
		for _, wb := range ty.WhereBounds {
			if wb.Param == nil {
				return kindInvalid
			}
			if _, bound := g.genericSubstKind(wb.Param.Name_); !bound {
				return kindInvalid
			}
		}
		params := make([]kind, len(ty.Params))
		for i, prm := range ty.Params {
			params[i] = g.project(prm)
		}
		return g.funcKind(params, g.project(ty.Return))

	case *analysis.ConjunctionType:
		// A conjunction's members are interfaces, which have a kind only in
		// a gen (below).
		if g == nil {
			return kindInvalid
		}
		return g.projectConjunction(ty)

	case *analysis.InterfaceType:
		// An EXISTENTIAL the checker solved for an unannotated position, and
		// the larger of the two doors onto a stdlib interface: most
		// `Display`-in-a-type-role sites arrive here rather than through
		// typeOf. Answered so that the annotated `d: Display` and the inferred
		// one are not two different answers about one position, which is the
		// rule the Map arm above states.
		//
		// A USER interface is answered too: impl.go builds a dispatch table
		// for every lowerable user interface, so refusing here would refuse a
		// type typeOf lowers in the same gen. See solvedIface, which resolves
		// by `(Origin, Name)` rather than by name for the identity reason
		// siblingNamed gives below.
		// A `self` value of the default being lowered (gen.defaultSelf) is
		// the implementor: the default is lowered once per implementor, so
		// every position the checker typed as this default's `self` (a
		// lambda's parameter, a binding, an element of `[t, t]`, a pipe
		// stage) takes the implementor's kind, as the `self` parameters
		// themselves do. A value annotated with the interface's name is not
		// DefaultSelf and stays an existential.
		//
		// With no gen (projectShared) only an interface with a process-wide
		// def has a kind, a stdIfaceSpecs row found by its identity; each gen
		// holds its own defs of the other interfaces it reaches.
		if g == nil {
			return projectSharedStdIface(ty)
		}
		if k, isSelf := g.projectDefaultSelf(ty); isSelf {
			return k
		}
		// A std instance's own module's interface is its caller's def
		// (stdProgOwnIface), as another module's is.
		if d, isOwn := g.solvedIface(ty.Origin, ty.Name); isOwn && len(ty.TypeArgs) == 0 && g.stdModule != "" && ty.Origin == "std/"+g.stdModule {
			if pd := g.stdProgOwnIface(d); pd != nil {
				return existential(pd)
			}
		}
		if k, isStd := g.projectStdIface(ty); isStd {
			return k
		}
		// std's `Iter<T>` is the lowered sequence over T (seqElemOf).
		if elem, isSeq := g.seqElemOf(ty); isSeq {
			// kindInvalid: propagates — an element with no kind is a sequence with none.
			if elem == kindInvalid {
				return kindInvalid
			}
			return seqKindIn(g, elem)
		}
		if len(ty.TypeArgs) > 0 {
			// A GENERIC interface instantiation, `Gets<String>`: the
			// existential of the template's instance at the arguments, as
			// typeOf answers the annotation (instimpl.go). The template
			// itself is no lowerable def.
			return g.projectIfaceInst(ty)
		}
		if d, found := g.solvedIface(ty.Origin, ty.Name); found && d.lowerable {
			return existential(d)
		}
		if d := g.stdProgSolved(ty); d != nil && d.lowerable {
			return existential(d)
		}
		return kindInvalid
	}

	// PartialType and AliasTemplate, which no recorded type is (projectExempt,
	// TestProjectNamesEveryTypeKind).
	return kindInvalid
}

// localNamed is the kind of a nominal type the analyzer solved that a
// program file declares: a block of this file, this file, or a sibling FILE,
// whose def is a mirror (programTypeDef); or, for one no program file
// declares, the process's def of a type a shared std module declares in
// Nomi (stdDeclaredDef), the only answer with no gen. kindInvalid for any
// other, and for a def the builder refuses: a refused mirror records its OWN
// reason, which declRefusal reports, and answering kindInvalid is what
// routes it there.
//
// The sibling's def keeps its type reachable by INFERENCE as it is by
// ANNOTATION. Without it project would answer kindInvalid, and the
// reason-walk behind it would name `unlowered type` for a mirror that is
// lowerable.
//
// shape cross-checks the declaration against what the analyzer says the type
// is. It cannot fail while `(Origin, Name)` is a real identity — which is the
// reason to check it: if it ever fails, the identity rule has broken somewhere
// upstream, and a refusal is the only answer that does not lower against a
// layout the analyzer disagrees with.
func (g *gen) localNamed(t analysis.Type, shape func(*typeDef) bool) kind {
	d, program := g.programTypeDef(t)
	if !program {
		// A type a shared std module declares, whose def is the process's
		// (stdtypes.go); with no gen (projectShared) the only one.
		d = g.stdDeclaredDef(t)
	}
	if d == nil || !d.lowerable || !shape(d) {
		return kindInvalid
	}
	return named(d)
}

// fieldKind is a struct or record-variant field's kind: its annotation's,
// or, for a field whose type comes from its default (`misses = 0`), the
// type the checker gave it, which it records on the field's own symbol
// (analysis's attachInferredField) as hover reads it.
func (g *gen) fieldKind(f ast.StructField) kind {
	if f.TypeAnnotation != nil {
		return g.typeOf(f.TypeAnnotation)
	}
	if t := g.inferredFieldType(f); t != nil {
		return g.project(t)
	}
	return kindInvalid
}

// inferredFieldType is the type the checker gave a field written with no
// type, or nil.
func (g *gen) inferredFieldType(f ast.StructField) analysis.Type {
	if g.fa == nil || f.TypeAnnotation != nil {
		return nil
	}
	sym, found := g.srcFA().Definitions[analysis.Pos{Line: f.Line, Col: f.Col}]
	if !found || sym.Kind != analysis.SymbolField || sym.Name != f.Name {
		return nil
	}
	return sym.Type
}

// fieldTypeText is a field's type as a refusal names it: its annotation, or
// the type the checker gave it.
func (g *gen) fieldTypeText(f ast.StructField) string {
	if f.TypeAnnotation != nil {
		return typeText(f.TypeAnnotation)
	}
	if t := g.inferredFieldType(f); t != nil {
		return t.String()
	}
	return "?"
}

// projectShared is project with no gen: the stdlib signature pass
// (collectStdCandidates, collectStdOnces), which runs
// before any gen exists and keeps only kinds every gen shares. It answers a
// scalar, a type std declares with a process-wide def (a std host, opaque,
// enum, struct, marker or generic struct or host instance, or a type std
// declares in Nomi, stdtypes.go), and a prelude or structural type over
// those, a std interface with an rt table (`Display`), and `Infallible`. A
// type parameter, any other interface, a conjunction, and a type a program
// or a generic template declares, have a kind only in a gen, and answer
// kindInvalid.
func projectShared(t analysis.Type) kind {
	var g *gen
	k := g.project(t)
	if k == kindNever {
		// Infallible: a leaf with no def, which no gen owns. packageNeutral
		// does not list it, so it is not admitted INSIDE a shared kind
		// (`Result<String, Infallible>` refuses); at the top it is the kind
		// of `impl Display for Infallible`'s receiver.
		return k
	}
	// kindInvalid: propagates — project's answer for a type with no shared kind.
	if k == kindInvalid || !k.packageNeutral() {
		return kindInvalid
	}
	return k
}
