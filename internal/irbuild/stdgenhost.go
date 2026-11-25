package irbuild

import (
	"slices"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The two stdlib declaration kinds with NO NOMI-VISIBLE CONTENTS: a GENERIC
// `pub host type` (`Sender<T>`, `Receiver<T>`) and a nil-inner MARKER
// (`pub type ChannelClosed`).
//
// # Why these two are one file and not two
//
// Both are LEAVES. Neither has a field, a variant or an inner type this builder
// can look inside, so neither needs a layout, a component graph or a projection
// — the whole of each def is its name and its type arguments. They differ in
// arity and in
// the declaration node they are anchored against, and in nothing else: a marker
// is a zero-parameter leaf and `Sender<T>` is a one-parameter leaf, so the
// second needs the process-wide instance interning stdgenstruct.go states and
// the first needs one def per spec the way opaque.go has one.
//
// Splitting them would put two three-clause `matches` and two anchor walks in
// two files that share every argument for existing.
//
// # The generic host type is its own family
//
//	family              declaration node     subject
//	opaque.go           *ast.TypeDef         a distinct over a SCALAR
//	stdstruct.go        *ast.StructDef       a monomorphic record
//	stdgenstruct.go     *ast.StructDef       a GENERIC record
//	stdenum.go          *ast.EnumDef         a monomorphic enum
//	stdprelude.go       *ast.EnumDef         a GENERIC enum
//	stdhost.go          (none)               an analyzer PRIMITIVE singleton
//
// Nothing in that list reads `*ast.ExternType`, and stdhost.go's header says so
// outright — "nothing in this package handles `*ast.ExternType`" — because its
// own subjects are `primitiveTypes` singletons reached by pointer. A generic
// `pub host type` is the case none of the six covers: `buildExternTypeShell`
// (analysis/type_builder.go) builds a real `*analysis.DistinctType` for it,
// with type parameters and no inner.
//
// # IDENTITY
//
// Every family above keys on the analyzer's (Origin, Name) rule, and this one
// does too: `buildTypeShellInScope`'s `*ast.ExternType` arm stamps the origin
// on a generic host type's DistinctType. An unstamped `OriginUnresolved` would
// match ANY origin in `sameNominalIdentity`, making two unrelated modules'
// same-named generic host types one type to the checker.
//
// Anchoring on a name plus a shape instead would treat a user's own
// `pub host type Sender<T>` as std's: the builder has NO declaration
// path for `*ast.ExternType`, so `g.types["Sender"]` is never populated and
// nothing downstream would catch it.
//
// The non-generic half of that arm is a `*PrimitiveType`, stamped with its
// Origin unless it is a built-in singleton; see buildExternTypeShell.
//
// # SIBLING LOOKUPS: every arm, enumerated
//
// A std nominal type is reached through five separate lookups and they must all
// agree. For these two families:
//
//	channel                       arm                          covered
//	an ANNOTATION                 typeOf                       yes
//	an INFERRED type              inferred.go's project        yes
//	a CROSS-FILE component        foreign.go's importNamed     yes (rtDeclared)
//	a stdlib SIGNATURE            projectShared                yes
//	a REFUSAL REASON              sigreason.go                 yes
//
// Every declaration in std/channels.nomi is `pub host fn …<T>`, so
// `stdCandidateFor` files each one as `stdlib generic function` whatever its
// signature projects to. TestStdGenHostChannelsAreAllGeneric evaluates the
// precondition rather than stating it, so if std ever declares a non-generic
// channel function this comment fails instead of rotting.

// --- a GENERIC stdlib `pub host type` ---------------------------------------

// stdGenHostSpec is one generic stdlib host type: what this builder believes
// std declares.
type stdGenHostSpec struct {
	// origin is the declaring file's build key, the analyzer's own identity
	// half; nomi is the other half. See analysis.DistinctType.Origin.
	origin string
	nomi   string
	// params are the declared type parameter names, in declaration order.
	params []string
	// bounds is the `where` clause the DECLARATION must carry, one entry per
	// type parameter. Nil demands a declaration with no bound at all.
	//
	// A declared expectation rather than a relaxation, exactly as
	// stdGenStructSpec.bounds is: a std edit ADDING `where T: Hashable` to
	// `Sender` must produce NO anchor, because this builder would then be
	// admitting a constraint it has not discharged anywhere.
	bounds [][]string
}

// stdGenHostSpecs is the whole set, and both rows are one type.
//
// `Sender<T>` and `Receiver<T>` are the two halves of `Channel<T>`, and they
// are TWO ROWS rather than one parameterised entry for the reason opaque.go
// gives for its ten civil units: each is a distinct Nomi type with a distinct
// Go type in rt, and the whole point of the split is that a `Receiver` may not
// be passed where a `Sender` is wanted. One row serving both would make that
// the builder's convention instead of the type system's fact.
//
// A row here is a Go type rt declares by hand, so adding one is a decision
// rather than a mechanical extension — preludeSpecs' bar and opaqueSpecs'.
// `Vector` is the third row and the first that is NOT a channel half, which is
// the family absorbing an unrelated type at the cost of one line. It is a `pub
// host type Vector<T>` with no bound (verified by `matches`, whose `bounds` clause
// demands a declaration with no constraint at all), so it satisfies the same
// three-clause identity every row here does — and unlike the channel halves it has
// an OPERATION surface, which vectors.go supplies against `rt.Vector[T]`.
var stdGenHostSpecs = []stdGenHostSpec{
	{origin: "std/channels", nomi: "Sender", params: []string{"T"}},
	{origin: "std/channels", nomi: "Receiver", params: []string{"T"}},
	// `Arm<R>` is one case of `Channel.select` with its handler; R is the
	// handler's result, the select's own.
	{origin: "std/channels", nomi: "Arm", params: []string{"R"}},
	{origin: "std/vectors", nomi: "Vector", params: []string{"T"}},
	// `Task<T>` is the fourth row and the second that is not a channel half.
	// A `pub host type Task<T>` with no bound, so it satisfies the same
	// three-clause identity every row here does. Its operation surface is
	// concurrent.go's, over `rt.Task[T]`.
	{origin: "std/tasks", nomi: "Task", params: []string{"T"}},
	// `Type<T>` is the fifth row and the only one whose type parameter is
	// PHANTOM: `rt.Type[T]` is one `*rt.TypeID` whatever T is, where every row
	// above stores a T. It satisfies the same three-clause identity — a `pub
	// host type Type<T>` with no bound, in std/type.nomi. The parameter still
	// separates instances here: `Type<Marker>` and `Type<TraceId>` are two defs
	// with different genHostArgs, so one does not pass where the other is
	// wanted.
	//
	// Its OPERATION surface is its Debug alone: std/type declares no function
	// and no constructor, and its `impl Debug` renders the witness through the
	// machine (machineImpls). What consults a witness is `std/context`'s value
	// store — see ircontextvalue.go — and the value position is the only way a
	// `Type<T>` is ever produced.
	//
	// A test that needs an unrepresentable type must not use `Type<Int>`, which
	// this row makes representable.
	{origin: "std/type", nomi: "Type", params: []string{"T"}},
}

// stdGenHostValidated is, per spec, whether STD ITSELF declares the type in the
// shape the spec describes — resolved once against std.Load()'s own analysis of
// the declaring module.
//
// Asked in the DECLARING module for stdGenStructValidated's reason, and here the
// check is purely SYNTACTIC (a declaration's clause, publicness and type
// parameters against the spec's), so it needs no scope at all.
var stdGenHostValidated = perStd(func() []bool {
	ok := make([]bool, len(stdGenHostSpecs))
	lib := stdAnchorLib()
	for i := range stdGenHostSpecs {
		s := &stdGenHostSpecs[i]
		fa := lib.Files[strings.TrimPrefix(s.origin, "std/")]
		decl := stdGenHostDeclIn(fa, s)
		ok[i] = decl != nil && s.matches(decl)
	}
	return ok
})

// stdGenHostDeclIn is the declaration a spec's name resolves to in fa, or nil.
//
// IDENTITY ONLY — (Origin, Name) on the solved type plus "it is a host type
// declaration". stdGenStructDeclIn's question about a different declaration
// kind, and the analyzer rule they apply is one rule.
//
// The solved type is a `*analysis.DistinctType` whose TypeArgs are EMPTY: this
// is the DECLARATION's own shell, not an instantiation, and a use site's
// `Sender<Int>` is a clone with TypeArgs populated. Requiring the bare shell is
// what keeps this from matching a symbol that is really an instantiated alias.
func stdGenHostDeclIn(fa *analysis.FileAnalysis, s *stdGenHostSpec) *ast.ExternType {
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
	dt, isDistinct := sym.Type.(*analysis.DistinctType)
	if !isDistinct || dt.Origin != s.origin || dt.Name != s.nomi || len(dt.TypeArgs) > 0 {
		return nil
	}
	decl, isDecl := sym.Node.(*ast.ExternType)
	if !isDecl {
		return nil
	}
	return decl
}

// matches reports whether decl is the declaration this spec describes.
//
// Every clause decides REPRESENTATION and each fails toward NO anchor:
//
//   - `Public`, because a file-private declaration cannot be the type another
//     module names.
//   - The type parameter names and their order, because an instance's type
//     arguments are matched to them by position.
//   - The BOUNDS, against the row's own expectation, so a std edit that adds a
//     constraint this builder discharges nowhere produces no anchor rather
//     than lowering past it.
//
// A DECORATOR other than `derive` is refused for opaqueSpec.matches' reason:
// declModifiers refuses it on its own terms and redirecting the type to rt
// would silently drop that refusal.
func (s *stdGenHostSpec) matches(decl *ast.ExternType) bool {
	if decl.Name != s.nomi || decl.Visibility != ast.Public {
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
	if !boundsMatch(s.bounds, decl.TypeParams, decl.WhereClauses) {
		return false
	}
	// An attached `//!` test is not checked: see stdStructSpec.mismatch.
	for _, dec := range decl.Decorators {
		if dec.Name != "derive" {
			return false
		}
	}
	return true
}

var (
	sharedGenHostMu sync.Mutex
	// sharedGenHostDefs is keyed on the FULL instantiation's spelling —
	// `Sender<Int>`, not `Sender` — for sharedGenStructDefs' reason:
	// keying on the base name is a silent overwrite that hands one
	// instantiation another's element type.
	sharedGenHostDefs = map[string][]*typeDef{}
)

// genHostNomi renders one instantiation's Nomi spelling, `Sender<Int>`: the
// def's name and the key the intern tables group instances under.
// genStructNomi's rule and its shape.
func genHostNomi(spec *stdGenHostSpec, args []kind) string {
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

// sharedGenHostInstance answers the process-wide def for an instantiation whose
// arguments are all package-neutral, and reports whether it is one.
//
// The def is a LEAF: `isDistinct` with `inner: kindInvalid` — the shape
// typeDef.inner's own comment names — plus `rtOpaque`, because a `Sender<Int>`
// has CONTENTS this builder may not look inside. stdhost.go records what goes
// wrong without `rtOpaque`: the arms written for a zero-sized marker answer
// for a value with a payload, so `a == b` on one is always true.
func sharedGenHostInstance(spec *stdGenHostSpec, args []kind) (kind, bool) {
	for _, arg := range args {
		// kindInvalid: propagates — a refused type ARGUMENT was named at its own position; the caller reports the false.
		if arg == kindInvalid || !arg.packageNeutral() {
			return kindInvalid, false
		}
	}
	nomi := genHostNomi(spec, args)
	sharedGenHostMu.Lock()
	defer sharedGenHostMu.Unlock()
	for _, d := range sharedGenHostDefs[nomi] {
		if !samePartsSlice(d.genHostArgs, args) {
			continue
		}
		return named(d), true
	}
	d := &typeDef{
		nomi: nomi,
		// The spec and the arguments, so packageNeutral can answer about this
		// def LOCALLY rather than by trusting that no non-neutral instance was
		// ever built. genStructOf's arrangement, for its reason.
		genHostOf:   spec,
		genHostArgs: args,
		isDistinct:  true,
		inner:       kindInvalid,
		lowerable:   true,
		rtDeclared:  true,
		rtOpaque:    true,
	}
	sharedGenHostDefs[nomi] = append(sharedGenHostDefs[nomi], d)
	return named(d), true
}

// genHostOfType is inferred.go's projection: the instance the checker solved for
// a position the program never annotated.
//
// Resolved by (Origin, Name) against a VALIDATED spec rather than through the
// mentioning module's anchor, for genStructOfType's reason, and here the reason
// bites HARDER than it does there: `Sender.send`'s solved parameter type is
// `Sender<Int>`, and a program that reads a channel through `ch.sender` may
// never import `Sender` at all.
//
// Not a weaker identity than the annotation door's. An Origin on a SOLVED type
// is the analyzer's own answer to "which declaration is this" — which is
// exactly what the one-line analyzer fix this file's header describes made
// available for a generic host type — and the shape check
// stdGenHostValidated performs against std is the same one the anchor carries.
// What the anchor adds is confirmation of a SPELLING, and an inferred position
// has none.
func (g *gen) genHostOfType(origin, name string, args []kind) (kind, bool) {
	if origin == "" {
		return kindInvalid, false
	}
	validated := stdGenHostValidated()
	for i := range stdGenHostSpecs {
		s := &stdGenHostSpecs[i]
		if s.origin != origin || s.nomi != name || !validated[i] || len(s.params) != len(args) {
			continue
		}
		return g.genHostInstance(s, args...)
	}
	return kindInvalid, false
}

// projectStdGenHost is inferred.go's entry point: it projects the type
// ARGUMENTS and then asks genHostOfType.
func (g *gen) projectStdGenHost(ty *analysis.DistinctType) (kind, bool) {
	if len(ty.TypeArgs) == 0 {
		return kindInvalid, false
	}
	args := make([]kind, len(ty.TypeArgs))
	for i, ta := range ty.TypeArgs {
		args[i] = g.project(ta)
	}
	return g.genHostOfType(ty.Origin, ty.Name, args)
}

// genHostOf reads a kind back to the spec it instantiates and its type
// arguments, so an operation arm can ask "is this a Sender" by SPEC POINTER
// rather than by rendered name.
func genHostOf(k kind) (*stdGenHostSpec, []kind, bool) {
	if k.tag != tagNamed || k.def == nil || k.def.genHostOf == nil {
		return nil, nil, false
	}
	return k.def.genHostOf, k.def.genHostArgs, true
}

// genHostInstance is the kind of `spec<args>` for a MODULE being lowered: the
// process-wide def when every type argument is package-neutral, and a per-gen
// one otherwise.
//
// stdgenstruct.go's genStructInstance over a leaf def, and its whole argument
// applies unchanged — no new kind, no new tag, the same `*typeDef` shape still
// `rtDeclared` because rt still declares `rt.Sender[T]`, and only the intern
// table differing. `kind.packageNeutral`'s `genHostOf` arm already walks
// `genHostArgs` and stdprelude.go records that it is written for exactly this
// eventuality.
//
// A per-gen instance has no fields to build — a generic host def is a LEAF,
// `isDistinct` with `inner: kindInvalid` plus `rtOpaque` — so unlike a generic
// std STRUCT there is nothing here that can fail after the arguments are
// admitted. That is why this function has no field guard and its neighbour does.
func (g *gen) genHostInstance(spec *stdGenHostSpec, args ...kind) (kind, bool) {
	defer changingTypes()()
	if spec == nil {
		// A caller resolved by (origin, name) and the row is absent. Unreachable
		// while the table has both channel rows, and honoured rather than
		// asserted for setKindOf's stated reason: a missing row must refuse
		// everything, not panic the compiler.
		return kindInvalid, false
	}
	for _, arg := range args {
		// kindInvalid: propagates — a refused type ARGUMENT was named at its own position; the caller reports the false.
		if arg == kindInvalid {
			return kindInvalid, false
		}
	}
	if k, shared := sharedGenHostInstance(spec, args); shared {
		return k, true
	}
	if g == nil {
		// The stdlib signature boundary: a per-gen def would be a kind no call
		// site in any gen could match by pointer. See genStructInstance.
		return kindInvalid, false
	}
	nomi := genHostNomi(spec, args)
	for _, d := range g.genHostInsts[nomi] {
		if samePartsSlice(d.genHostArgs, args) {
			return named(d), true
		}
	}
	d := &typeDef{
		nomi:        nomi,
		genHostOf:   spec,
		genHostArgs: args,
		isDistinct:  true,
		inner:       kindInvalid,
		lowerable:   true,
		rtDeclared:  true,
		rtOpaque:    true,
	}
	// A leaf carries no `mentions`, so settleLowerable would never retract it
	// on its own account — but its type argument can be unlowerable, and a
	// `Sender<Bad>` over an unlowerable `Bad` must not stay lowerable. Noting
	// the argument is what puts that edge on the graph. The shared door needs
	// no such note because a package-neutral argument is lowerable by
	// construction.
	for _, arg := range args {
		d.mentions = appendMentionedDefs(d.mentions, arg)
	}
	if g.genHostInsts == nil {
		g.genHostInsts = map[string][]*typeDef{}
	}
	g.genHostInsts[nomi] = append(g.genHostInsts[nomi], d)
	g.genHostOrder = append(g.genHostOrder, d)
	return named(d), true
}

// genHostFieldKind is a generic std STRUCT field whose type is a generic std
// HOST type — `Channel<T>`'s `sender: Sender<T>`.
//
// Written once rather than twice inline because the two channel halves differ in
// nothing but the spec, and because the nil-gen case is the load-bearing half:
// at the stdlib signature boundary only a process-wide instance exists, so a
// non-neutral argument yields no kind and the field has none. Honoured rather
// than asserted, exactly as the `Option<T>` field beside it honours its own.
func genHostFieldKind(g *gen, spec *stdGenHostSpec, arg kind) kind {
	k, ok := g.genHostInstance(spec, arg)
	if !ok {
		// kindInvalid: reports — no instance for this argument, so the field has
		// no kind and buildGenStructDef declines the whole instantiation.
		return kindInvalid
	}
	return k
}

// genHostSpecFor is the row with this (origin, name), resolved once so a reorder
// of the table cannot repoint a caller silently. setSpec's form.
func genHostSpecFor(origin, nomi string) *stdGenHostSpec {
	for i := range stdGenHostSpecs {
		s := &stdGenHostSpecs[i]
		if s.origin == origin && s.nomi == nomi {
			return s
		}
	}
	return nil
}

var (
	senderSpec   = genHostSpecFor("std/channels", "Sender")
	receiverSpec = genHostSpecFor("std/channels", "Receiver")
	armSpec      = genHostSpecFor("std/channels", "Arm")
)

// --- a stdlib nil-inner MARKER ----------------------------------------------

// stdMarkerSpec is one stdlib marker distinct: `pub type ChannelClosed`, a
// nominal type with no inner and therefore no contents.
type stdMarkerSpec struct {
	origin string
	nomi   string
	// form is the declaration clause, required for declForm's reason: a bool
	// would default to one of the two answers and a row added without thinking
	// about the clause would silently claim it.
	form declForm
}

// stdMarkerSpecs is the whole set.
//
// # Why this is not a row in opaqueSpecs
//
// opaque.go's `matches` requires an inner type written as the row's `decl`,
// and a `type Name` marker has none. An opaque row is a distinct over a
// scalar with a def that holds the scalar; a marker holds nothing, so the
// nil-inner case is a family of its own with its own check
// (`decl.InnerTypeExpr == nil`) rather than a row with an empty spelling.
var stdMarkerSpecs = []stdMarkerSpec{
	{origin: "std/channels", nomi: "ChannelClosed", form: formDistinct},
	{origin: "std/channels", nomi: "TimedOut", form: formDistinct},
}

// stdMarkerDefs is the process-wide *typeDef per spec, keyed by spec index.
//
// A genuine zero-sized MARKER, so `rtOpaque` is deliberately NOT set: there is
// nothing inside one, two of them ARE always equal, the hash IS the unit hash
// and `irRetainedMarker` IS true. That is the exact opposite of stdhost.go's `Bytes`,
// which is a leaf WITH contents, and the two are one line apart on purpose.
var stdMarkerDefs = sync.OnceValue(func() []*typeDef {
	defs := make([]*typeDef, len(stdMarkerSpecs))
	for i := range stdMarkerSpecs {
		s := &stdMarkerSpecs[i]
		defs[i] = &typeDef{nomi: s.nomi, isDistinct: true, inner: kindInvalid, lowerable: true, rtDeclared: true}
	}
	return defs
})

// stdMarkerKind is the kind of the i'th spec's values.
func stdMarkerKind(i int) kind { return named(stdMarkerDefs()[i]) }

// stdMarkerValidated is, per spec, whether STD ITSELF declares the marker in the
// shape the spec describes — resolved once against std.Load()'s own analysis of
// the declaring module, exactly as stdGenHostValidated is.
//
// Needed because stdMarkerOfType resolves without a module anchor, so the shape
// check has to exist on its own rather than as a step inside stdMarkerAnchors.
// Asked in the DECLARING module for stdStructValidated's reason.
var stdMarkerValidated = perStd(func() []bool {
	ok := make([]bool, len(stdMarkerSpecs))
	lib := stdAnchorLib()
	for i := range stdMarkerSpecs {
		s := &stdMarkerSpecs[i]
		fa := lib.Files[strings.TrimPrefix(s.origin, "std/")]
		if fa == nil || fa.ModuleScope == nil {
			continue
		}
		sym := fa.ModuleScope.Lookup(s.nomi)
		if sym == nil {
			continue
		}
		if sym.Resolved != nil {
			sym = sym.Resolved
		}
		dt, isDistinct := sym.Type.(*analysis.DistinctType)
		if !isDistinct || dt.Origin != s.origin || dt.Name != s.nomi {
			continue
		}
		decl, isDecl := sym.Node.(*ast.TypeDef)
		ok[i] = isDecl && s.matches(decl)
	}
	return ok
})

// stdMarkerOfType is the def a solved (Origin, Name) names, for the INFERRED
// channel and for a position with no spelling at all.
//
// Resolved against the VALIDATED spec table rather than through a module's
// anchor, and here that is not an optimisation but the only workable rule:
// `ChannelClosed` is the `E` of `Sender.send`'s result, so
// `Sender.send(ch.sender, 7)` mentions it nowhere and `turbofish_test.nomi`
// does not import it. Requiring the caller's anchor would refuse every send in
// the corpus. stdStructOfType's rule and decimalKind's reason.
func stdMarkerOfType(origin, name string) (kind, bool) {
	if origin == "" {
		return kindInvalid, false
	}
	validated := stdMarkerValidated()
	for i := range stdMarkerSpecs {
		if stdMarkerSpecs[i].origin == origin && stdMarkerSpecs[i].nomi == name && validated[i] {
			return stdMarkerKind(i), true
		}
	}
	return kindInvalid, false
}

// stdMarkerAnchors resolves the marker specs against one module's analysis: the
// declaration NODE it reached each spec through. opaqueAnchors' body over the
// marker table.
func stdMarkerAnchors(fa *analysis.FileAnalysis) map[*ast.TypeDef]int {
	byDecl := map[*ast.TypeDef]int{}
	if fa == nil || fa.ModuleScope == nil {
		return byDecl
	}
	for i := range stdMarkerSpecs {
		s := &stdMarkerSpecs[i]
		sym := fa.ModuleScope.Lookup(s.nomi)
		if sym == nil {
			continue
		}
		if sym.Resolved != nil {
			sym = sym.Resolved
		}
		dt, isDistinct := sym.Type.(*analysis.DistinctType)
		if !isDistinct || dt.Origin != s.origin || dt.Name != s.nomi {
			continue
		}
		decl, isDecl := sym.Node.(*ast.TypeDef)
		if !isDecl || !s.matches(decl) {
			continue
		}
		byDecl[decl] = i
	}
	return byDecl
}

// matches reports whether decl is the declaration this spec describes.
//
// THE NIL INNER IS THE GATE, and it is checked directly rather than through
// scalarKind for the reason stdMarkerSpecs states: scalarKind answers
// kindInvalid for a non-scalar inner too, so leaning on it would let this table
// silently claim `pub type X SomeStruct`. Everything else is opaqueSpec.matches'
// clause for opaqueSpec.matches' reason.
func (s *stdMarkerSpec) matches(decl *ast.TypeDef) bool {
	want := formOpaque
	if !decl.Opaque {
		want = formDistinct
	}
	if decl.Name != s.nomi || s.form != want || decl.Visibility != ast.Public {
		return false
	}
	if decl.InnerTypeExpr != nil {
		return false
	}
	// An attached `//!` test is not checked: see stdStructSpec.mismatch.
	for _, dec := range decl.Decorators {
		if dec.Name != "derive" {
			return false
		}
	}
	return true
}

// loadStdMarkers resolves the marker specs against this gen's module, once.
func (g *gen) loadStdMarkers() {
	if g.stdMarkersLoaded {
		return
	}
	g.stdMarkersLoaded = true
	g.stdMarkerByDecl = stdMarkerAnchors(g.fa)
}

// stdMarkerDeclared reports the spec index for a marker declaration this module
// CARRIES, or -1. Read by buildTypes: an anchored declaration adopts the shared
// def instead of getting its own shell. opaqueDeclared's counterpart: std/channels'
// own gen declares `ChannelClosed`, and without this it would build a second
// def for the type.
func (g *gen) stdMarkerDeclared(decl *ast.TypeDef) int {
	g.loadStdMarkers()
	if i, anchored := g.stdMarkerByDecl[decl]; anchored {
		return i
	}
	return -1
}

// projectStdMarker is inferred.go's projection for a marker: the DistinctType
// the checker solved for a position the program never annotated.
//
// It resolves through stdMarkerOfType, which needs no module anchor, because a
// marker reached as a std function's error type is never spelled by the program
// that reaches it, and an annotation (`c: ChannelClosed`) reaches it the same
// way, through the type the checker recorded for it.
//
// Writing the name without importing it is a front-end error, so every
// position where an annotation
// is reachable is a position where the anchor exists.
func (g *gen) projectStdMarker(ty *analysis.DistinctType) (kind, bool) {
	if len(ty.TypeArgs) > 0 {
		return kindInvalid, false
	}
	return stdMarkerOfType(ty.Origin, ty.Name)
}

// --- shared -----------------------------------------------------------------

// boundsMatch reports whether a declaration's type-parameter bounds are exactly
// the ones a spec declares, reading BOTH spellings as one fact.
//
// Shared by stdGenStructSpec and this file's `*ast.ExternType` specs. The two
// node types share no interface, so a
// method on either could not serve the other; taking the two bound-carrying
// slices makes the RULE the shared thing rather than the traversal — and one
// implementation is the point, since a second copy is how the inline and
// `where` spellings come to be treated differently by two callers.
//
// `struct S<T: Comparable>` puts the interface on `TypeParams[i].Bounds` and
// `struct S<T> where T: Comparable` puts it on the where clauses; std writes
// `Range` the second way, `Set` neither way, and both channel halves neither
// way. Collecting both into one per-parameter set before comparing is what
// makes the check independent of which spelling std happens to use, so a std
// edit that MOVED a bound from a where clause to an inline one still anchors
// while one that CHANGED it does not.
//
// Order-insensitive within a parameter and exact on the set, so `Comparable and
// Discrete` neither matches a spec naming only `Comparable` nor is silently
// narrowed to it. A where clause naming a parameter the declaration does not
// have fails too, by landing in no bucket.
//
// Bounds are compared by BASE NAME, which is analysis.TypeExprBaseName's answer.
// A bound is an interface, interfaces are not generic in a bound position
// anywhere in std, and nothing here has a scope to resolve a qualified spelling
// against — so the name is what there is, and a std edit to a bound's MODULE
// with the name unchanged would not be detected. That is the one limit of this
// check.
//
// The final clause honours rather than asserts a spec naming a bound for a
// parameter the declaration does not have: each caller's param-count check
// already refuses that.
func boundsMatch(want [][]string, params []ast.TypeParam, wheres []ast.WhereConstraint) bool {
	got := make([][]string, len(params))
	for i := range params {
		for _, b := range params[i].Bounds {
			got[i] = append(got[i], analysis.TypeExprBaseName(b))
		}
	}
	for _, wc := range wheres {
		idx := -1
		for i := range params {
			if params[i].Name == wc.Name {
				idx = i
				break
			}
		}
		if idx < 0 {
			// A constraint on a name this declaration does not declare. Not
			// this spec's declaration, whatever else is true of it.
			return false
		}
		for _, b := range wc.Bounds {
			got[idx] = append(got[idx], analysis.TypeExprBaseName(b))
		}
	}
	for i := range got {
		var w []string
		if i < len(want) {
			w = want[i]
		}
		if len(got[i]) != len(w) {
			return false
		}
		g := slices.Clone(got[i])
		w = slices.Clone(w)
		slices.Sort(g)
		slices.Sort(w)
		if !slices.Equal(g, w) {
			return false
		}
	}
	return len(want) <= len(got)
}
