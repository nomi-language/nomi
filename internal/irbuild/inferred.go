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

// inferredParamKind is the type the checker solved for an unannotated
// parameter, and that type projected into a kind.
//
// Three outcomes, and the caller has to distinguish all three because they are
// three different amounts of work:
//
//   - (nil, kindInvalid) — the checker recorded nothing. The parameter's type
//     is not known to anybody, so no builder change can lower it.
//   - (ty, kindInvalid) — the checker knows, and this builder has no
//     representation for the answer. That is representation work, and it is
//     the SAME work as an annotation naming the same type.
//   - (ty, k) — lowerable.
func (g *gen) inferredParamKind(p ast.Param) (analysis.Type, kind) {
	if g.fa == nil {
		return nil, kindInvalid
	}
	sym, found := g.fa.Definitions[analysis.Pos{Line: p.Line, Col: p.Col}]
	if !found || sym.Kind != analysis.SymbolParam || sym.Name != p.Name || sym.Type == nil {
		// The predicate mirrors attachParamType exactly — position, kind and
		// NAME — so this asks the same question that recording answered, and
		// cannot read some other symbol that happens to share a position.
		//
		// Despite the name match, a DISCARD parameter does NOT land here. `|_| 7` gets a real SymbolParam named
		// "_" carrying the caller's type, so it lowers, and
		// testdata/lambda_inferred.nomi pins that. What lands here is a lambda
		// in a position no expected type reaches — an element of a list or
		// tuple literal, a map value — where the checker also records nothing
		// because it solved nothing.
		return nil, kindInvalid
	}
	return sym.Type, g.project(sym.Type)
}

// project is the analysis.Type → kind projection. See the file comment for why
// it has no default arm.
func (g *gen) project(t analysis.Type) kind {
	switch ty := t.(type) {
	case nil:
		return kindInvalid

	case *analysis.TypeVar:
		// An inference variable that got solved is its solution; an unsolved
		// one is a type nobody knows, which is not a representation gap.
		if ty.Resolved == nil {
			return kindInvalid
		}
		return g.project(ty.Resolved)

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
		// monomorphized instance, because prelude.go's preludeArgKind asks
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
		// refused (nestedfn.go), so no unrelated `T` is live inside the window.
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
		if k, bound := g.genericSubstKind(ty.Name_); bound {
			return k
		}
		if k, isTypeParam := g.dictTypeParamKind(ty.Name_); isTypeParam {
			return k
		}
		return kindInvalid

	case *analysis.PrimitiveType:
		// Named one at a time rather than by exclusion: Decimal, Byte, Bytes,
		// Any and Infallible are all *PrimitiveType and none of them has a
		// representation here, so a default-to-scalar arm would emit the
		// wrong Go type for five of the eleven rows.
		switch ty {
		case analysis.TypeInt:
			return kindInt
		case analysis.TypeFloat:
			return kindFloat
		case analysis.TypeString:
			return kindString
		case analysis.TypeUnit:
			return kindUnit
		}
		// A BLESSED primitive with an rt type. Named through the same
		// singleton-pointer identity the annotation channel uses, so
		// `|b| Byte.to_int(b)` and `fn f(b: Byte)` cannot become two rows.
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
			// A generic instantiation the checker solved — `|b| b.item` over a
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
			if k, isUserGeneric := g.projectGenericInstance(ty.Origin, ty.Name, ty.TypeArgs); isUserGeneric {
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
		// positions where the program never writes the NAME: `|diagnostic|`
		// over `compiler.check(src)`. Same rule as the EnumType arm's
		// projectStdEnum below. See stdstruct.go's stdStructOfType.
		if d, isStdStruct := stdStructOfType(ty.Origin, ty.Name); isStdStruct {
			return named(d)
		}
		return g.localNamed(ty.Origin, ty.Name, func(d *typeDef) bool {
			return !d.isEnum && !d.isDistinct
		})

	case *analysis.EnumType:
		if ty.Origin == boolOrigin && ty.Name == boolName {
			return kindBool
		}
		// The two prelude enums are generic and DO have a representation:
		// a Go generic type in rt, instantiated per type-argument tuple.
		// See prelude.go, whose identity rule is this file's own.
		if k, isPrelude := g.projectPreludeEnum(ty); isPrelude {
			return k
		}
		// A MONOMORPHIC stdlib enum — `Ordering` — whose Go type rt also
		// declares. Answered here so the annotated `o: Ordering` and the
		// inferred `|o|` over one are not two different answers about one
		// parameter, which is the rule this file's Map arm below states.
		if k, isStdEnum := g.projectStdEnum(ty); isStdEnum {
			return k
		}
		if len(ty.TypeArgs) > 0 {
			// A user generic enum the checker solved (`Wrapper<Int>`), by
			// the struct arm's rule.
			if k, isUserGeneric := g.projectGenericInstance(ty.Origin, ty.Name, ty.TypeArgs); isUserGeneric {
				return k
			}
			return kindInvalid
		}
		return g.localNamed(ty.Origin, ty.Name, func(d *typeDef) bool { return d.isEnum })

	case *analysis.DistinctType:
		// A GENERIC std HOST type — `Sender<Int>` — asked FIRST because it is
		// the one distinct family with type arguments, and the clause below
		// rejects every argument-carrying distinct outright. Same rule as the
		// EnumType arm above, where projectPreludeEnum precedes the
		// `len(TypeArgs) > 0` refusal for the same reason. See stdgenhost.go.
		if k, isGenHost := g.projectStdGenHost(ty); isGenHost {
			return k
		}
		if len(ty.TypeArgs) > 0 {
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
		// A std DISTINCT with an rt Go type — `Toml`, `Duration`, `Days` —
		// answered here for the reason the StructType and EnumType arms above
		// give for theirs: every `opaqueSpecs` row is reachable by ANNOTATION
		// (namedType consults opaqueNamed), so it must be reachable by
		// INFERENCE too.
		//
		// A miss here does not name this file or the type: `Some(Toml"…")`
		// would refuse as `non-scalar type argument (Maybe<Toml>)` through
		// prelude.go's preludeArgs, which calls project() on each type argument
		// and reports whatever comes back invalid.
		if k, isStd := g.projectStdOpaque(ty); isStd {
			return k
		}
		return g.localNamed(ty.Origin, ty.Name, func(d *typeDef) bool { return d.isDistinct })

	case *analysis.ListType:
		return g.listKind(g.project(ty.Elem))

	case *analysis.MapType:
		// The annotated spelling lowers through maps.go's `Map<K, V>`; this
		// arm stops the two spellings of one parameter from disagreeing.
		// `m: Map<String, Int>` lowering while `|m|` over the same value
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
		// A function VALUE with defaults or a method-level `where` bound is
		// not a plain Go func: the first needs a call-site arity rule the
		// builder does not encode in a kind, the second is dispatch. Both are
		// refused rather than flattened away, because flattening them
		// produces a kind that lowers and then loses an argument.
		if ty.Return == nil || ty.DefaultCount > 0 || len(ty.WhereBounds) > 0 {
			return kindInvalid
		}
		params := make([]kind, len(ty.Params))
		for i, prm := range ty.Params {
			params[i] = g.project(prm)
		}
		return g.funcKind(params, g.project(ty.Return))

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
		if k, isStd := g.projectStdIface(ty); isStd {
			return k
		}
		// std's `Iter<T>` is the lowered sequence, as typeOf answers the
		// annotation (structuralTypeOf).
		if k, isSeq := g.irSeqKindOf(ty); isSeq {
			return k
		}
		if len(ty.TypeArgs) > 0 {
			// A GENERIC interface instantiation. Its declaration is refused
			// `generic interface` (impl.go's declareIfaces), so there is no
			// lowerable def for an existential to take its shape from, and the
			// resolution below would answer with the unlowerable shell.
			return kindInvalid
		}
		if d, found := g.solvedIface(ty.Origin, ty.Name); found && d.lowerable {
			return existential(d)
		}
		return kindInvalid
	}

	// AnonStructType (structural and UNORDERED in Nomi, ordered in Go),
	// TypeParam_ and BoundAliasType (generics, which need the dictionary).
	return kindInvalid
}

// localNamed resolves a nominal type the analyzer solved — declared in THIS
// module, or mirrored from a sibling FILE — or kindInvalid.
//
// shape cross-checks the declaration against what the analyzer says the type
// is. It cannot fail while `(Origin, Name)` is a real identity — which is the
// reason to check it: if it ever fails, the identity rule has broken somewhere
// upstream, and a refusal is the only answer that does not emit against a
// layout the analyzer disagrees with.
//
// The SIBLING branch keeps a sibling's type reachable by INFERENCE as it is by
// ANNOTATION (namedType consults foreignType). Without it project would answer
// kindInvalid, and the reason-walk behind it would name `unlowered type` for a
// mirror that is lowerable.
func (g *gen) localNamed(origin, name string, shape func(*typeDef) bool) kind {
	if bd, isBlockLocal := g.blockLocalNamed(name); isBlockLocal && (origin == "" || origin == g.fa.Origin) {
		// A type the enclosing block declares, which the checker stamps with
		// no origin or with this file's. Its identity there is the name, so
		// a module-level type of the same name would be the same answer to
		// it: that case declines rather than guess.
		if _, found := g.types[name]; found || !bd.lowerable || !shape(bd) {
			return kindInvalid
		}
		return named(bd)
	}
	if origin == "" || g.fa.Origin == "" {
		return kindInvalid
	}
	if origin != g.fa.Origin {
		return g.siblingNamed(origin, name, shape)
	}
	d, found := g.types[name]
	if !found || !d.lowerable || !shape(d) {
		return kindInvalid
	}
	return named(d)
}

// siblingNamed is localNamed for a type another USER FILE declares.
//
// Resolved by ORIGIN rather than by this file's imports, because the positions
// that reach here are exactly the ones where the program never writes the name
// — `|w| w.name` over a list a sibling's function returned. There is no import
// binding to consult.
func (g *gen) siblingNamed(origin, name string, shape func(*typeDef) bool) kind {
	o := g.siblingOwner(origin, name)
	if o == nil {
		return kindInvalid
	}
	d := g.mirrorOf(o)
	if !d.lowerable || !shape(d) {
		// A mirror the declaring file or the import graph refuses.
		// It records its OWN reason, which nominalRefusal reports; answering
		// kindInvalid here is what routes it there.
		return kindInvalid
	}
	return named(d)
}
