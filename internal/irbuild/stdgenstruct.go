package irbuild

import (
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A GENERIC stdlib STRUCT, instantiated at package-neutral type arguments — the
// fifth stdlib type family, and the first that is a struct AND parameterized.
//
// # Why this is a family and not a row in one of the four
//
// stdstruct.go's subjects are MONOMORPHIC record structs, and it says so by
// refusing anything else in one condition:
//
//	if len(decl.TypeParams) > 0 || len(decl.WhereClauses) > 0 {
//	    // A generic std struct's identity depends on the instantiation, which
//	    // is prelude.go's problem and not this file's.
//	    return false
//	}
//
// That comment names the right owner and the wrong file. prelude.go DOES own
// "identity depends on the instantiation" — an (Origin, Name) anchor against a
// validated declaration shape, instantiated into a hand-written rt generic type,
// interned process-wide when every argument is package-neutral — but every line
// of it is about an ENUM: `*ast.EnumDef`, variants, tags, one payload field per
// variant. There is no struct in it to reuse. So this file is prelude.go's
// mechanism over a declaration kind it cannot read, which is exactly the
// relationship stdstruct.go has to stdenum.go one family down.
//
// # The gate is a DECLARED bound
//
// The generic std structs the corpus reaches include:
//
//	std/sets.nomi:16    pub opaque struct Set<T> { items: Map<T, Bool> }
//	std/ranges.nomi:24  pub opaque struct Range<T> where T: Comparable { … }
//
// A `where T: Comparable` on a std declaration is discharged at the call site
// by the CONCRETE type argument, for a type as for a function (iterext.go's
// iterMaxArity makes the same point for functions). `Discrete.next` is never
// resolved at a type parameter here: the builder knows the element kind
// statically at every site, so it builds the function a run-time lookup would
// find and passes it as an ARGUMENT, map.go's `hash, eq` arrangement one
// interface up. See rt/range.go, whose functions take `cmp`, `next`, `steps`
// and `step` as plain Go funcs and resolve nothing.
//
// So a spec states the `where` clause it expects and `matches` checks it: `Set`
// demands bound-freeness and `Range` demands exactly `where T: Comparable`. A
// std edit that widened, narrowed or removed either produces NO anchor, which
// is the same failure mode every other clause here has. A bound the spec did
// not name is not admitted; the relaxation is per row, not per family.
//
// This does not claim "the bound is never used", which is unprovable per body.
// It claims the weaker thing, that a named bound is DISCHARGED at a concrete
// instantiation, which is checkable at each call site and is checked there, by
// the builder refusing an element type it cannot build the methods for.
//
// # What makes an instance shareable is stdprelude.go's predicate, unchanged
//
// A def reached from two gens must render to the same Go type text in every
// module's package, so an instance is admitted only when every type argument is
// `packageNeutral`. `rt.Set[int64]` names rt and a Go builtin; `rt.Set[NomiT_Point]`
// names a type declared in one module's package. There is deliberately NO
// per-gen fallback here — unlike prelude.go, which has one — because a per-gen
// Set instance would need foreign.go's rebuild path and nothing in the corpus
// reaches a `Set<UserType>`. A non-neutral argument refuses by name instead, so
// the absence is a stated scope line rather than a silent miss.
//
// # The Go type is in rt, and that is the one thing that could not be avoided
//
// rt/set.go's header argues it: a Set is not a data structure, it is a one-field
// wrapper over the map rt already owns, so nothing new is IMPLEMENTED — but the
// Go TYPE has to exist in rt, because a per-package `NomiT_Set_Int` would make
// one Nomi type into N mutually unassignable Go types. Calling `Set<T>` "no new
// runtime type" is off by a struct declaration and off by nothing else.

// stdGenStructSpec is one generic stdlib struct: what this builder believes std
// declares, and how rt spells it.
type stdGenStructSpec struct {
	// origin is the declaring file's build key, the analyzer's own identity
	// half; nomi is the other half. See analysis.StructType.Origin.
	origin string
	nomi   string
	// rtType is the Go generic type in rt, without its type arguments.
	rtType string
	// params are the declared type parameter names, in declaration order.
	params []string
	// opaque is whether std declares this WITH `opaque`, checked in BOTH
	// directions for stdStructSpec.transparent's reason: the two are different
	// use-site surfaces over one representation and getting the direction wrong
	// is not a compile error either way.
	opaque bool
	// bounds is the `where` clause the DECLARATION must carry, as one entry per
	// type parameter in declaration order and each entry the interface names
	// that parameter is bounded by. Nil — the ordinary case — demands a
	// declaration with NO bound at all, in either spelling.
	//
	// A declared expectation rather than a relaxation, which is the distinction
	// the header's retraction turns on. `Set` names nothing here and a std edit
	// adding `where T: Hashable` to it produces no anchor; `Range` names
	// exactly `Comparable` and a std edit widening it to `Comparable and
	// Discrete` produces no anchor either. What the gate stopped admitting is
	// "any bound", not "a bound".
	//
	// Checked against BOTH spellings — `struct S<T: C>` and `struct S<T> where
	// T: C` — because those are one fact written two ways and admitting either
	// form loosely would defeat the check on the other.
	bounds [][]string
	fields []stdGenStructField
}

// stdGenStructField is one field: the Nomi name, the DECLARED type expression it must have, and the kind it holds under one
// instantiation.
type stdGenStructField struct {
	nomi string
	// decl is the field's type annotation as std must SPELL it, with the
	// spec's own type parameter names in it — `Map<T, Bool>`.
	//
	// A rendered string rather than a resolved kind, and the reason is
	// preludeSpec's payload check: the declaration is where two things that
	// render alike are still two different tokens. Resolving `Map<T, Bool>` to
	// a kind needs T bound, which means checking the shape per instantiation
	// instead of once; comparing the SOURCE spelling checks it against the one
	// thing that cannot vary. TestStdGenStructSpecsMatchStdSource is what keeps
	// it from being vacuous.
	decl string
	// kindOf builds the field's kind for one instantiation. It takes the gen
	// because a container kind has to be interned, and a NIL gen is what
	// internComp documents as the stdlib signature boundary — with every
	// component package-neutral it interns PROCESS-WIDE, which is the identity
	// rule this whole file rests on.
	//
	// The gen is also what lets a field REACH a per-gen instance of a nested
	// generic std type. `Channel<Request>`'s `sender` field is a
	// `Sender<Request>`, whose argument is package-relative for exactly the
	// reason the container's is, so the field has to go through the same
	// shared-then-per-gen door the container does or the container's per-gen
	// instance would be admitted with an invalid field.
	kindOf func(g *gen, args []kind) kind
	// deflt is the field's DEFAULT, when std declares one, and nil when the
	// field is required at every construction site.
	//
	// THE SAME CHANNEL stdstruct.go CARRIES FOR THE MONOMORPHIC FAMILY, with no
	// parameterization. A `stdFieldDefault` states its value in the builder's
	// own vocabulary and
	// resolves no names by construction, so it cannot mention a type
	// parameter — there is nothing for an instantiation to substitute into.
	// `fillFieldDefaults` reads `fieldDef.stdDeflt` off the def, and a def
	// this file builds is an ordinary `*typeDef`, so the fill is shared
	// rather than reimplemented.
	//
	// A spec and a declaration must agree in BOTH directions: a declared
	// default with no spec row for it produces no anchor, and so does a spec
	// default the declaration does not carry.
	deflt *stdFieldDefault
}

// stdGenStructSpecs is the whole set. APPEND-ONLY: ranges.go, channels.go and
// random.go each resolve their row by (origin, name) rather than by index, so a
// reorder cannot repoint one silently, but a mid-table INSERT would still
// disturb any reader that does index positionally.
//
// The `Range` row is what the header's declared-bound gate is about. The
// `Generator` row has the one field kind in either struct family that is a
// FUNCTION.
var stdGenStructSpecs = []stdGenStructSpec{
	{
		origin: "std/sets", nomi: "Set", rtType: "rt.Set",
		params: []string{"T"},
		opaque: true,
		fields: []stdGenStructField{{
			nomi: "items", decl: "Map<T, Bool>",
			kindOf: func(g *gen, args []kind) kind { return mapKindIn(g, args[0], kindBool) },
		}},
	},
	{
		// `std/ranges.Range<T>`, the row every `range literal` in the corpus
		// needs — 29 sites across 4 files, and the operand is ALWAYS EMPTY
		// because the key refuses a LITERAL and has no subject to name.
		//
		// Three fields and three different projections, which is why this is
		// the row that exercises the family rather than merely joining it:
		//
		//   - `start` is the type PARAMETER itself, bare. The first field in
		//     either struct family whose kind IS an argument rather than being
		//     built from one, so it is package-neutral exactly when the
		//     instantiation is — which sharedGenStructInstance already checks
		//     for every argument, so this field needs no guard of its own.
		//   - `end` is a `Maybe<T>`, a PRELUDE INSTANCE, so it goes through
		//     sharedPreludeInstance rather than mapKindIn. That reaches the same
		//     process-wide entry a call site's own `Maybe<Int>` annotation
		//     produces, which is the identity requirement stdprelude.go states;
		//     a per-gen instance here would be a kind no call site could match.
		//     Answering kindInvalid on a non-neutral argument is honoured rather
		//     than asserted, exactly as stdMaybeField honours its `!shared`.
		//   - `inclusive` is a Bool, and it is the only field of either family
		//     that is a bare scalar.
		origin: "std/ranges", nomi: "Range", rtType: "rt.Range",
		params: []string{"T"},
		opaque: true,
		bounds: [][]string{{"Comparable"}},
		fields: []stdGenStructField{{
			nomi: "start", decl: "T",
			kindOf: func(_ *gen, args []kind) kind { return args[0] },
		}, {
			nomi: "end", decl: "Maybe<T>",
			kindOf: func(g *gen, args []kind) kind {
				spec := preludeSpecFor("std/maybe", "Maybe")
				if g == nil {
					// The stdlib signature boundary: no gen, so only the
					// process-wide instance is reachable and a non-neutral
					// argument has no kind. Honoured rather than asserted.
					k, shared := sharedPreludeInstance(spec, []kind{args[0]})
					if !shared {
						return kindInvalid
					}
					return k
				}
				// preludeInstanceOf is shared-then-per-gen already, so a
				// `Range<Point>` field reaches the SAME `Maybe<Point>` def a
				// call site's own annotation produces in this gen — which is
				// the identity requirement, met rather than sidestepped.
				return g.preludeInstanceOf(spec, []kind{args[0]})
			},
		}, {
			nomi: "inclusive", decl: "Bool",
			kindOf: func(*gen, []kind) kind { return kindBool },
		}},
	},
	{
		// `std/channels.Channel<T>`, and it is the first row here that is NOT
		// opaque: `pub struct Channel<T>`, so `ch.sender` and `ch.receiver` are
		// ordinary field reads a program outside std may write. `Set` and
		// `Range` are both `pub opaque struct`, so the `opaque` field below is
		// load-bearing for the first time rather than uniformly true.
		//
		// Both fields are a GENERIC STD HOST type, which is a family that did
		// not exist when this table had two rows — see stdgenhost.go, and note
		// that this row cannot anchor without it, because `kindOf` would have
		// nothing to answer.
		//
		// The two halves are DELIBERATELY two types over one runtime object.
		// std puts the operations on the halves (`Sender.send`,
		// `Receiver.receive`, `Sender.close`) and declares no `Channel.send`,
		// so nothing may reach the channel through the pair; rt/channel.go
		// carries that argument in full.
		origin: "std/channels", nomi: "Channel", rtType: "rt.Channel",
		params: []string{"T"},
		fields: []stdGenStructField{{
			nomi: "sender", decl: "Sender<T>",
			kindOf: func(g *gen, args []kind) kind {
				return genHostFieldKind(g, senderSpec, args[0])
			},
		}, {
			nomi: "receiver", decl: "Receiver<T>",
			kindOf: func(g *gen, args []kind) kind {
				return genHostFieldKind(g, receiverSpec, args[0])
			},
		}},
	},
	{
		// `std/random.Generator<T>`, the row in either struct family whose field
		// kind is a FUNCTION: `(Seed) -> (T, Seed)`.
		//
		// `packageNeutral`'s structural arm names `tagFunc` beside `tagTuple` and
		// recurses into components, so a function over an rt-declared `Seed` and a
		// neutral tuple is neutral and interns process-wide exactly as a
		// `List<Int>` does. funcKindIn answers with a nil gen, at the stdlib
		// signature boundary that asks every field kind the same question.
		//
		// NO OPERATION ARM ACCOMPANIES THIS ROW, unlike Set, Range, Channel and
		// Vector. random.go carries the argument in full: those four have
		// `pub host fn` operations with no Nomi body, and `Generator`'s are
		// ordinary Nomi source over a seed-threading closure, so an rt
		// implementation would be a SECOND implementation of one draw sequence.
		origin: "std/random", nomi: "Generator", rtType: "rt.Generator",
		params: []string{"T"},
		opaque: true,
		fields: []stdGenStructField{{
			nomi: "run", decl: "(Seed) -> (T, Seed)",
			kindOf: func(g *gen, args []kind) kind {
				return generatorFieldKind(g, args[0])
			},
		}},
	},
	{
		// `std/io.Captured<T>`, what `io.capture` answers. A row here rather
		// than an ordinary generic declaration because a generic std body is
		// instantiated per program in a gen of its own: as an ordinary
		// template each of those gens, and the program's, would build its own
		// `Captured<Int>`, and a value built in `io.capture`'s instance would
		// match no other gen's kind (a parameter annotated `Captured<Int>`,
		// std's Debug instance). This family interns one instance per program.
		origin: "std/io", nomi: "Captured",
		params: []string{"T"},
		fields: []stdGenStructField{{
			nomi: "value", decl: "T",
			kindOf: func(_ *gen, args []kind) kind { return args[0] },
		}, {
			nomi: "output", decl: "String",
			kindOf: func(*gen, []kind) kind { return kindString },
		}, {
			nomi: "transcript", decl: "String",
			kindOf: func(*gen, []kind) kind { return kindString },
		}},
	},
}

// stdGenStructAnchor is one generic std struct as THIS compilation's analysis
// sees it. decl IS the identity, for prelude.go's reason.
type stdGenStructAnchor struct {
	spec *stdGenStructSpec
	decl *ast.StructDef
}

// --- the shape check, asked ONCE in the declaring module ---------------------

// stdGenStructValidated is, per spec, whether STD ITSELF declares the type in
// the shape the spec describes — resolved once against std.Load()'s own analysis
// of the declaring module.
//
// Asked in the DECLARING module for stdStructValidated's reason: a field's
// annotation names types that only the declaring module has in scope, and
// resolving it through an importer would make the anchor depend on which names
// the importer chose to import. Here the check is purely SYNTACTIC — the field's
// spelling against the spec's — so it needs no scope at all, which is a stronger
// version of the same property rather than a weaker one.
var stdGenStructValidated = sync.OnceValue(func() []bool {
	ok := make([]bool, len(stdGenStructSpecs))
	lib := stdAnchorLib()
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		fa := lib.Files[strings.TrimPrefix(s.origin, "std/")]
		decl := stdGenStructDeclIn(fa, s)
		ok[i] = decl != nil && s.matches(decl)
	}
	return ok
})

// stdGenStructDeclIn is the declaration a spec's name resolves to in fa, or nil.
//
// Identity only — (Origin, Name) and "it is a struct declaration". stdstruct.go's
// stdStructDeclIn is the same question about the monomorphic family, and the two
// are separate functions because they index separate spec tables; the analyzer
// rule they apply is one rule.
func stdGenStructDeclIn(fa *analysis.FileAnalysis, s *stdGenStructSpec) *ast.StructDef {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	sym := fa.ModuleScope.Lookup(s.nomi)
	if sym == nil {
		return nil
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	st, isStruct := sym.Type.(*analysis.StructType)
	if !isStruct || st.Origin != s.origin || st.Name != s.nomi {
		return nil
	}
	decl, isDecl := sym.Node.(*ast.StructDef)
	if !isDecl {
		return nil
	}
	return decl
}

// matches reports whether decl is the declaration this spec describes.
//
// THE BOUND CHECK IS THE GATE, and it is the one clause here that is a design
// decision rather than a shape assertion; see the file header. Everything else
// decides REPRESENTATION on
// stdStructSpec.matches' terms: the builder lowers constructions and field
// reads from the spec, so a rename, a reorder or a widened field type must
// produce NO anchor rather than lower against a layout nobody wrote, and a
// permuted field is a wrong ANSWER rather than an error.
//
// A field DEFAULT is MODELLED: see stdGenStructField.deflt, which is
// stdstruct.go's channel unchanged, because a `stdFieldDefault` resolves no
// names and therefore has no type parameter to carry. The check here is the
// both-direction agreement every other clause in this function has: a
// declared default with no spec row produces no anchor, and a spec default
// the declaration does not carry produces no anchor either.
func (s *stdGenStructSpec) matches(decl *ast.StructDef) bool {
	if decl.Name != s.nomi || decl.Opaque != s.opaque || !decl.Public {
		return false
	}
	if len(decl.TypeParams) != len(s.params) {
		return false
	}
	for i, p := range s.params {
		if decl.TypeParams[i].Name != p {
			return false
		}
	}
	if !s.boundsMatch(decl) {
		// THE GATE. A spec names the bound it expects and this is where the
		// declaration has to agree; `Set` names none and `Range` names exactly
		// `Comparable`. Both spellings are checked, so neither can be used to
		// smuggle a bound past the other.
		return false
	}
	if len(decl.Items) > 0 {
		// A type body item is layout-relevant. An attached `//!` test is not,
		// and is deliberately not checked — see stdStructSpec.matches.
		return false
	}
	for _, dec := range decl.Decorators {
		// `derive` is metadata about work already done — it is also synthesized
		// into an ordinary impl block. Any other decorator is a real refusal.
		if dec.Name != "derive" {
			return false
		}
	}
	if len(decl.Fields) != len(s.fields) {
		return false
	}
	for i, f := range decl.Fields {
		want := s.fields[i]
		if f.Name != want.nomi || typeText(f.TypeAnnotation) != want.decl {
			return false
		}
		if (f.Default != nil) != (want.deflt != nil) {
			// BOTH DIRECTIONS. The forward half is the old clause: std
			// growing a default this builder does not know about would make
			// every omitting construction site lower a zero value where the
			// language evaluates an expression. The reverse half is the one
			// the old clause could not state — a spec claiming a default std
			// does not declare would INVENT a value at every omitting site,
			// and nothing else here would notice, because a missing field is
			// otherwise a loud `struct literal missing field`.
			return false
		}
		if f.Default != nil && !want.deflt.shape(f.Default) {
			// The default is the RIGHT one. `shape` is structural rather than
			// textual for stdFieldDefault's reason, and it is what keeps the
			// agreement above from being satisfied by any two defaults at all.
			return false
		}
	}
	return true
}

// boundsMatch reports whether decl's type-parameter bounds are exactly the ones
// the spec declares.
//
// The RULE lives in stdgenhost.go's `boundsMatch`, which takes the two
// bound-carrying slices rather than a node: `*ast.StructDef` and
// `*ast.ExternType` share no interface, so a method on either could not serve
// the other, and a second copy is how the inline and `where` spellings come to
// be treated differently by two callers. See that function for why both
// spellings are collected, why the comparison is order-insensitive within a
// parameter and exact on the set, and why a bound is compared by BASE NAME.
func (s *stdGenStructSpec) boundsMatch(decl *ast.StructDef) bool {
	return boundsMatch(s.bounds, decl.TypeParams, decl.WhereClauses)
}

// --- anchoring, per module ---------------------------------------------------

// stdGenStructAnchorsOf resolves the specs against one module's analysis, by
// Nomi name.
//
// A free function rather than only a gen method for preludeAnchorsOf's reason,
// even though nothing outside a gen needs it: two implementations of "is
// this the std type" is the drift the identity rules exist to prevent, so there
// is one.
//
// IDENTITY ONLY. The shape is stdGenStructValidated's question, asked once
// against the declaring module.
func stdGenStructAnchorsOf(fa *analysis.FileAnalysis) map[string]*stdGenStructAnchor {
	out := map[string]*stdGenStructAnchor{}
	if fa == nil || fa.ModuleScope == nil {
		return out
	}
	validated := stdGenStructValidated()
	for i := range stdGenStructSpecs {
		if !validated[i] {
			continue
		}
		spec := &stdGenStructSpecs[i]
		decl := stdGenStructDeclIn(fa, spec)
		if decl == nil {
			continue
		}
		out[spec.nomi] = &stdGenStructAnchor{spec: spec, decl: decl}
	}
	return out
}

// loadStdGenStructs resolves the generic std structs against this module's
// analysis, once.
func (g *gen) loadStdGenStructs() {
	if g.genStructs != nil {
		return
	}
	g.genStructs = stdGenStructAnchorsOf(g.fa)
}

// --- the process-wide intern table ------------------------------------------

var (
	sharedGenStructMu sync.Mutex
	// sharedGenStructDefs is keyed on the FULL instantiation's spelling
	// — `Set<Int>`, not `Set` — for sharedPreludeDefs' reason: keying on
	// the base name is a silent overwrite that hands one instantiation another's
	// payload type.
	sharedGenStructDefs = map[string][]*typeDef{}
)

// genStructNames renders one instantiation's Go type and its Nomi spelling, from
// ONE walk so they cannot disagree about which arguments an instance has — the
// Go name is the intern key and the Nomi name is what a collision is detected
// against. preludeNames' rule and its shape.
func genStructNomi(spec *stdGenStructSpec, args []kind) string {
	var nomi strings.Builder
	nomi.WriteString(spec.nomi)
	nomi.WriteByte('<')
	for i, arg := range args {
		if i > 0 {
			nomi.WriteString(", ")
		}
		nomi.WriteString(arg.nomi())
	}
	nomi.WriteByte('>')
	return nomi.String()
}

// buildGenStructDef constructs one instantiation's typeDef, reporting whether
// every field had a kind.
//
// An ordinary struct typeDef with two differences, both consequences of the Go
// type living in rt: `rtDeclared` so typeDecl emits nothing for it, and the
// field's name coming from the spec, because rt
// has to export it for another package to name.
//
// g is NIL for a process-wide instance and the gen for a per-gen one, and that
// is the only difference between the two doors below: a field's kind is interned
// wherever its container is, or the container's identity and its field's would
// live in different tables.
//
// THE `bool` IS NOT DEFENSIVE. A field kind can be kindInvalid — `Set<T>`'s is
// `Map<T, Bool>`, which needs a hash for T — and a def carrying one would be
// `lowerable` with a field naming nothing, which lowers against a type that
// was never declared. The per-gen door below admits non-neutral arguments, so
// this check is required.
func buildGenStructDef(g *gen, spec *stdGenStructSpec, args []kind) (*typeDef, bool) {
	nomi := genStructNomi(spec, args)
	d := &typeDef{
		nomi: nomi,
		// The spec and the arguments, so packageNeutral can answer about this
		// def LOCALLY rather than by trusting that no non-neutral instance was
		// ever built. See kind.packageNeutral's rtDeclared arm.
		genStructOf:   spec,
		genStructArgs: args,
		lowerable:     true,
		rtDeclared:    true,
	}
	for _, f := range spec.fields {
		fk := f.kindOf(g, args)
		// The field has no kind, so there is no instance and nothing was
		// declined without a name: the caller refuses by naming the type
		// argument at the annotation's own position.
		// kindInvalid: reports — no instance; the caller names the type argument.
		if fk == kindInvalid {
			return nil, false
		}
		// `stdDeflt` and never `deflt`: a stdlib field's default is stated in
		// the builder's own vocabulary so it resolves no names, and a field
		// carrying the AST form would take types.go's declaring-scope path
		// instead. stdstruct_test.go asserts that exclusivity for the
		// monomorphic family and TestStdGenStructDefaultsAreStdDefaults does
		// it here.
		d.fields = append(d.fields, fieldDef{nomi: f.nomi, k: fk, stdDeflt: f.deflt})
		// The component graph, so a field kind that cannot be LOWERED (as
		// opposed to absent, above) is visible to settleLowerable and a
		// self-reference would be boxed. Recorded rather than assumed for
		// buildPreludeDef's reason.
		d.note(fk)
	}
	return d, true
}

// sharedGenStructInstance answers the process-wide def for an instantiation whose
// arguments are all package-neutral, and reports whether it is one.
func sharedGenStructInstance(spec *stdGenStructSpec, args []kind) (kind, bool) {
	for _, arg := range args {
		// kindInvalid: propagates — a refused type ARGUMENT was named at its own position; the caller reports the false.
		if arg == kindInvalid || !arg.packageNeutral() {
			return kindInvalid, false
		}
	}
	nomi := genStructNomi(spec, args)
	sharedGenStructMu.Lock()
	defer sharedGenStructMu.Unlock()
	for _, d := range sharedGenStructDefs[nomi] {
		if !samePartsSlice(d.genStructArgs, args) {
			continue
		}
		return named(d), true
	}
	// Nil gen: every component is package-neutral here by the loop above, so
	// internComp's process-wide table is the right one. See buildGenStructDef.
	d, ok := buildGenStructDef(nil, spec, args)
	if !ok {
		return kindInvalid, false
	}
	sharedGenStructDefs[nomi] = append(sharedGenStructDefs[nomi], d)
	return named(d), true
}

// genStructInstance is the kind of `spec<args>` for a MODULE being lowered:
// the process-wide def when every type argument is package-neutral, and a
// per-gen one otherwise.
//
// # Why the per-gen half exists
//
// `16-concurrency/message_loop` writes `Channel<Request>` in a field and in a
// parameter, and `Request` is a struct ONE module's package declares, so the
// instance cannot be process-wide.
//
// No new `kind` and no new tag. An instance is the same `*typeDef` shape,
// carrying `genStructOf` and `genStructArgs`, still `rtDeclared` because rt
// declares `rt.Channel[T]`. What differs is WHICH TABLE it is interned in,
// which is exactly prelude.go's split: `rt.Maybe[int64]` process-wide,
// `rt.Maybe[NomiT_Point]` per gen.
//
// `kind.packageNeutral`'s `genStructOf` arm answers by walking
// `genStructArgs` (see stdprelude.go), so it answers false for
// `rt.Channel[NomiT_Request]`.
func (g *gen) genStructInstance(spec *stdGenStructSpec, args ...kind) (kind, bool) {
	for _, arg := range args {
		// kindInvalid: propagates — a refused type ARGUMENT was named at its own position; the caller reports the false.
		if arg == kindInvalid {
			return kindInvalid, false
		}
	}
	if k, shared := sharedGenStructInstance(spec, args); shared {
		return k, true
	}
	if g == nil {
		// No module to intern into: the stdlib signature boundary, where a
		// per-gen def would be a kind no call site in any gen could match by
		// pointer. Refusing is the honest answer and stdgensig.go names it.
		return kindInvalid, false
	}
	nomi := genStructNomi(spec, args)
	// Grouped by Nomi spelling and separated by the type ARGUMENTS, for
	// preludeInstanceOf's reason.
	for _, d := range g.genStructInsts[nomi] {
		if samePartsSlice(d.genStructArgs, args) {
			return named(d), true
		}
	}
	d, ok := buildGenStructDef(g, spec, args)
	if !ok {
		return kindInvalid, false
	}
	if g.genStructInsts == nil {
		g.genStructInsts = map[string][]*typeDef{}
	}
	g.genStructInsts[nomi] = append(g.genStructInsts[nomi], d)
	g.genStructOrder = append(g.genStructOrder, d)
	return named(d), true
}

// stdGenStructDeclaringModule reports whether this gen is lowering the very
// module the anchor's declaration lives in.
//
// The analyzer's (Origin, Name) identity, which is what stdGenStructAnchorsOf
// built the anchor from — so this asks the same question one step further rather
// than introducing a second notion of "same type": gen origin `std/random`
// against spec origin `std/random`, and likewise for `std/sets` and
// `std/ranges`.
//
// TWO CALLERS, AND THEY ARE ONE RULE. A generic std struct has to mean the same
// Go type in the declaring module's SIGNATURES (stdGenStructTypeOf) and in its
// struct LITERALS (stdGenStructLit). Otherwise the signature path would hide
// the anchor behind a shadowing check the declaring module trips by
// definition, and the literal path would mint its own instance: one Nomi type,
// two Go types, and a function refused for disagreeing with itself.
func (g *gen) stdGenStructDeclaringModule(a *stdGenStructAnchor) bool {
	return a != nil && g.fa != nil && g.fa.Origin != "" && g.fa.Origin == a.spec.origin
}

// --- reading a type annotation and reading a kind back ----------------------

// stdGenStructTypeOf reads the annotation `Set<Int>`, reporting whether the
// generic type named a generic std struct at all — so an ordinary
// unrepresentable `Foo<Bar>` keeps falling through to its own refusal.
//
// The identity check is the ANCHOR, not the name: `g.genStructs` was built from
// this module's own scope by stdGenStructAnchorsOf, which requires the analyzer's
// (Origin, Name) rule AND the declaration's validated shape.
//
// A type ARGUMENT is resolved by the ordinary g.typeOf, so `Set<List<Int>>` works
// and `Set<SomeUserStruct>` reaches this module's own instance table rather than
// the process-wide one. What still refuses is an argument no instance can be
// built over at all — one whose own kind is invalid, or one for which a field's
// kind cannot be built.
func (g *gen) stdGenStructTypeOf(t *ast.GenericType) (kind, bool) {
	g.loadStdGenStructs()
	a := g.genStructs[t.Name]
	if a == nil {
		return kindInvalid, false
	}
	if _, declaredHere := g.types[t.Name]; declaredHere && !g.stdGenStructDeclaringModule(a) {
		// A module declaring its own `Set` wins, which is the analyzer's own
		// shadowing rule. Unreachable while std re-exports `Set` through the
		// prelude — checkReservedTypeName rejects the redeclaration outright —
		// and honoured rather than asserted, because the reservation is a
		// front-end property this file does not own.
		//
		// THE DECLARING MODULE IS EXEMPT. `std/random` declares `Generator`,
		// so `declaredHere` is TRUE throughout the module's own gen, and without
		// the exemption every `Generator<T>` in its own signatures would resolve
		// to kindInvalid and each member would refuse for naming its OWN type.
		//
		// A module is not shadowing itself: its declaration IS the anchor's
		// subject, validated as such by stdGenStructAnchorsOf against the
		// analyzer's (Origin, Name) identity. So the exemption is the same
		// identity rule the anchor was built from, read one step further.
		return kindInvalid, false
	}
	if len(t.Params) != len(a.spec.params) {
		return kindInvalid, false
	}
	args := make([]kind, len(t.Params))
	for i, p := range t.Params {
		args[i] = g.typeOf(p)
	}
	k, ok := g.genStructInstance(a.spec, args...)
	if !ok {
		// Claimed and refused, rather than declined: declining would report
		// `generic type (Set)`, naming the container — which lowers — instead of
		// the argument, which does not. That is the mis-naming prelude.go's
		// preludeSpecFor exists to correct.
		g.reject("generic std struct at a package-relative type argument",
			typeText(t), t)
		return kindInvalid, true
	}
	return k, true
}

// genStructOf reads a kind back to the spec it instantiates and its type
// arguments, so an operation arm can ask "is this a Set" by SPEC POINTER rather
// than by rendered name.
//
// Pointer identity against the spec table and never a string: a user type
// spelled `Set` has its own def and answers false here, which is the half a name
// check gets wrong.
func genStructOf(k kind) (*stdGenStructSpec, []kind, bool) {
	if k.tag != tagNamed || k.def == nil || k.def.genStructOf == nil {
		return nil, nil, false
	}
	return k.def.genStructOf, k.def.genStructArgs, true
}

// genStructOfType is inferred.go's projection: the instance the checker solved
// for a position the program never annotated.
//
// `stdGenStructTypeOf` is the ANNOTATION door and this is the INFERENCE door.
// These rows have a process-wide `*typeDef` each, so without this arm
// `Set<Int>` would lower as an annotation and refuse under inference, the
// asymmetry inferred.go's own Map arm says must not happen.
//
// Resolved by (Origin, Name) against a VALIDATED spec rather than through the
// mentioning module's anchor, which is stdStructOfType's rule and its reason:
// the checker solves this type for positions where the program never writes the
// NAME. `Channel.buffered<Int>(4)`'s solved RESULT is the case in hand — the
// type appears nowhere in the statement.
//
// Not a weaker identity than the annotation door's. An Origin on a SOLVED type
// is the analyzer's own answer to "which declaration is this", and the shape
// check `stdGenStructValidated` performs against std is the same one the anchor
// carries. What the anchor adds is confirmation of a SPELLING, and there is no
// spelling here to confirm — decimalKind's rule, two families over.
func (g *gen) genStructOfType(origin, name string, args []kind) (kind, bool) {
	if origin == "" {
		return kindInvalid, false
	}
	validated := stdGenStructValidated()
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		if s.origin != origin || s.nomi != name || !validated[i] || len(s.params) != len(args) {
			continue
		}
		return g.genStructInstance(s, args...)
	}
	return kindInvalid, false
}

// setSpec is the `Set` row, resolved by (origin, name) so a reorder of the table
// cannot repoint it silently — stdstruct.go's spec-index constants, in the form a
// one-row table wants.
var setSpec = func() *stdGenStructSpec {
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		if s.origin == "std/sets" && s.nomi == "Set" {
			return s
		}
	}
	return nil
}()
