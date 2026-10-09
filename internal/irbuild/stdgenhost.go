package irbuild

import (
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/rt"

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
// — the whole of each def is a Go type name in rt. They differ in ARITY and in
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
// Anchoring on a name plus a shape instead would redirect a user's own
// `pub host type Sender<T>` to `rt.Sender`: the builder has NO declaration
// path for `*ast.ExternType`, so `g.types["Sender"]` is never populated and
// nothing downstream would catch it.
//
// The non-generic half of that arm is a `*PrimitiveType`, stamped with its
// Origin unless it is a built-in singleton; see buildExternTypeShell.
//
// # The Go types are in rt, for the reason every family above gives
//
// A `*typeDef` reached from two generated packages must render to the same Go
// type text in both, so `Sender<Int>` cannot be a per-package generated type.
// See rt/channel.go, and note the one thing that file adds beyond a name: the
// two halves are two GO types over one runtime object, so a wrong-direction
// lowering is a Go compile error rather than a wrong answer.
//
// # SIBLING LOOKUPS: every arm, enumerated
//
// A std nominal type is reached through five separate lookups and they must all
// agree. For these two families:
//
//	channel                       arm                          covered
//	an ANNOTATION                 typeOf / namedType           yes
//	an INFERRED type              inferred.go's project        yes
//	a CROSS-FILE component        foreign.go's importNamed     yes (rtDeclared)
//	a stdlib SIGNATURE            stdlib.go's stdTypeKind      n/a — see below
//	a REFUSAL REASON              sigreason.go                 yes
//
// The stdlib-SIGNATURE arm is absent DELIBERATELY and it costs nothing here:
// every declaration in std/channels.nomi is `pub host fn …<T>`, so
// `stdCandidateFor` short-circuits at `case generic:` before any signature
// question is asked, and no channel function can reach `stdTypeKind` at all.
// (`genStructSigKind` in `stdgensig.go` is the generic-struct family's
// signature arm. It changes nothing here: all five channel host fns are
// generic over `T`, so `sigNamesTypeParam` claims each one before
// `genStructSigKind` is reached.)
// TestStdGenHostChannelsAreAllGeneric evaluates the precondition
// rather than stating it, so if std ever declares a non-generic channel
// function this comment fails instead of rotting.

// --- a GENERIC stdlib `pub host type` ---------------------------------------

// stdGenHostSpec is one generic stdlib host type: what this builder believes
// std declares, and how rt spells it.
type stdGenHostSpec struct {
	// origin is the declaring file's build key, the analyzer's own identity
	// half; nomi is the other half. See analysis.DistinctType.Origin.
	origin string
	nomi   string
	// rtType is the Go generic type in rt, WITHOUT its type arguments —
	// stdGenStructSpec.rtType's form, and a string for its reason: a
	// reflect.Type cannot name an uninstantiated generic.
	rtType string
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
	{origin: "std/channels", nomi: "Sender", rtType: "rt.Sender", params: []string{"T"}},
	{origin: "std/channels", nomi: "Receiver", rtType: "rt.Receiver", params: []string{"T"}},
	{origin: "std/vectors", nomi: "Vector", rtType: "rt.Vector", params: []string{"T"}},
	// `Task<T>` is the fourth row and the second that is not a channel half.
	// A `pub host type Task<T>` with no bound, so it satisfies the same
	// three-clause identity every row here does. Its OPERATION surface is
	// concurrent.go's, against `rt.Task[T]` — which is a one-field struct over
	// an unexported pointer for `Sender[T]`'s reason: the value is copyable, so
	// the Go type this row names needs no `*` at any position, and the state
	// stays unreachable from a generated package.
	{origin: "std/tasks", nomi: "Task", rtType: "rt.Task", params: []string{"T"}},
	// `Type<T>` is the fifth row and the only one whose type parameter is
	// PHANTOM: `rt.Type[T]` is one `*rt.TypeID` whatever T is, where every row
	// above stores a T. It satisfies the same three-clause identity — a `pub
	// host type Type<T>` with no bound, in std/type.nomi, which declares that
	// line and nothing else — and the phantom parameter is on the Go type
	// anyway, for rt/channel.go's stated reason: two Go types over one runtime
	// object make a wrong-direction lowering a Go COMPILE ERROR rather than a
	// wrong answer, so a `Type<Marker>` cannot arrive where a `Type<TraceId>`
	// is wanted.
	//
	// Its OPERATION surface is EMPTY, which no other row here can say: std/type
	// declares no function, no interface and no constructor, so there is nothing
	// to lower on the type itself. What consults a witness is `std/context`'s
	// value store — see contextvalue.go — and the value position is the only way
	// a `Type<T>` is ever produced.
	//
	// A test that needs an unrepresentable type must not use `Type<Int>`, which
	// this row makes representable.
	{origin: "std/type", nomi: "Type", rtType: "rt.Type", params: []string{"T"}},
}

// stdGenHostAnchor is one generic std host type as THIS compilation's analysis
// sees it. decl IS the identity, for prelude.go's reason.
type stdGenHostAnchor struct {
	spec *stdGenHostSpec
	decl *ast.ExternType
}

// stdGenHostValidated is, per spec, whether STD ITSELF declares the type in the
// shape the spec describes — resolved once against std.Load()'s own analysis of
// the declaring module.
//
// Asked in the DECLARING module for stdGenStructValidated's reason, and here the
// check is purely SYNTACTIC (a declaration's clause, publicness and type
// parameters against the spec's), so it needs no scope at all.
var stdGenHostValidated = sync.OnceValue(func() []bool {
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
//   - The type parameter names and their ORDER, because `rt.Sender[T]`'s
//     argument list is written from them.
//   - The BOUNDS, against the row's own expectation, so a std edit that adds a
//     constraint this builder discharges nowhere produces no anchor rather
//     than lowering past it.
//   - NO FOREIGN BINDING (`ForeignAlias`, `ForeignName`). A `host type`
//     bound to a Go package is an FFI handle whose representation is the
//     EMBEDDER's; rt's `Sender[T]` is not that, and redirecting one to the
//     other would hand a generated package a type somebody else owns.
//   - NO body item and NO attached test, for stdGenStructSpec.matches' reason:
//     each is a construct declModifiers refuses on its own terms, and
//     redirecting the type to rt would silently drop that refusal.
//
// A DECORATOR other than `derive` is refused for opaqueSpec.matches' reason:
// declModifiers refuses it on its own terms and redirecting the type to rt
// would silently drop that refusal.
func (s *stdGenHostSpec) matches(decl *ast.ExternType) bool {
	if decl.Name != s.nomi || !decl.Public {
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
	if decl.ForeignAlias != "" || decl.ForeignName != "" {
		return false
	}
	// A body or a type body item is layout-relevant. An attached `//!` test is
	// not, and is deliberately not checked — see stdStructSpec.matches.
	if decl.HasBody || len(decl.Items) > 0 {
		return false
	}
	for _, dec := range decl.Decorators {
		if dec.Name != "derive" {
			return false
		}
	}
	return true
}

// stdGenHostAnchorsOf resolves the specs against one module's analysis, by Nomi
// name. IDENTITY ONLY; the shape is stdGenHostValidated's question.
func stdGenHostAnchorsOf(fa *analysis.FileAnalysis) map[string]*stdGenHostAnchor {
	out := map[string]*stdGenHostAnchor{}
	if fa == nil || fa.ModuleScope == nil {
		return out
	}
	validated := stdGenHostValidated()
	for i := range stdGenHostSpecs {
		if !validated[i] {
			continue
		}
		spec := &stdGenHostSpecs[i]
		decl := stdGenHostDeclIn(fa, spec)
		if decl == nil {
			continue
		}
		out[spec.nomi] = &stdGenHostAnchor{spec: spec, decl: decl}
	}
	return out
}

// loadStdGenHosts resolves the generic std host types against this module's
// analysis, once.
func (g *gen) loadStdGenHosts() {
	if g.genHosts != nil {
		return
	}
	g.genHosts = stdGenHostAnchorsOf(g.fa)
}

var (
	sharedGenHostMu sync.Mutex
	// sharedGenHostDefs is keyed on the FULL instantiation's spelling —
	// `Sender<Int>`, not `Sender` — for sharedGenStructDefs' reason:
	// keying on the base name is a silent overwrite that hands one
	// instantiation another's element type.
	sharedGenHostDefs = map[string][]*typeDef{}
)

// genHostNames renders one instantiation's Go type and its Nomi spelling from
// ONE walk, so they cannot disagree about which arguments an instance has.
// genStructNames' rule and its shape.
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
// wrong without `rtOpaque` and it is not hypothetical: four arms written for a
// zero-sized marker answered for a value with a payload, so `a == b` on one
// emitted `return true`.
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

// stdGenHostTypeOf reads the annotation `Sender<Int>`, reporting whether the
// generic type named a generic std host type at all — so an ordinary
// unrepresentable `Foo<Bar>` keeps falling through to its own refusal.
//
// stdGenStructTypeOf's body over a different anchor table, including its
// refusal: a type argument no instance can be built over is CLAIMED and refused
// by naming the argument, because declining would report
// `generic type (Sender)` and name the container, which lowers. A
// PACKAGE-RELATIVE argument is not such an argument: it reaches this
// module's own instance table.
func (g *gen) stdGenHostTypeOf(t *ast.GenericType) (kind, bool) {
	g.loadStdGenHosts()
	a := g.genHosts[t.Name]
	if a == nil {
		return kindInvalid, false
	}
	if _, declaredHere := g.types[t.Name]; declaredHere {
		// A module declaring its own `Sender` wins, which is the analyzer's
		// shadowing rule. Unreachable while this builder has no declaration
		// path for `*ast.ExternType` and so never populates g.types for a host
		// type, and honoured rather than asserted, because if it grows one this
		// must not silently redirect.
		return kindInvalid, false
	}
	if len(t.Params) != len(a.spec.params) {
		return kindInvalid, false
	}
	args := make([]kind, len(t.Params))
	for i, p := range t.Params {
		args[i] = g.typeOf(p)
	}
	k, ok := g.genHostInstance(a.spec, args...)
	if !ok {
		g.reject("generic std host type at a package-relative type argument",
			typeText(t), t)
		return kindInvalid, true
	}
	return k, true
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
// A per-gen instance has no fields to build — a genHost def is a LEAF,
// `isDistinct` with `inner: kindInvalid` plus `rtOpaque` — so unlike a generic
// std STRUCT there is nothing here that can fail after the arguments are
// admitted. That is why this function has no field guard and its neighbour does.
func (g *gen) genHostInstance(spec *stdGenHostSpec, args ...kind) (kind, bool) {
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
	// on its own account — but its type ARGUMENT can be unlowerable, and an
	// emitted `rt.Sender[NomiT_Bad]` would name a type nobody declared. Noting
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
// than asserted, exactly as the `Maybe<T>` field beside it honours its own.
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
	// goType is rt's Go type, held as the TYPE rather than as its spelling so a
	// rename or a deletion in rt is a Go compile error here. opaqueSpec.goType's
	// reason.
	goType reflect.Type
}

// stdMarkerSpecs is the whole set.
//
// # Why this is not a row in opaqueSpecs
//
// opaque.go's `matches` ends with `scalarKind(decl.InnerTypeExpr) == s.inner`,
// and its comment states the exclusion as a mechanical fact rather than a
// policy: "A `type Name` marker has a nil InnerTypeExpr, which scalarKind
// answers kindInvalid for, so it cannot match either."
//
// A row over there with `inner: kindInvalid` WOULD match a marker — and it
// would also match `pub type X SomeStruct`, because scalarKind answers
// kindInvalid for any non-scalar inner just as readily. That is the one
// direction every identity check in this package must not fail in, so the
// nil-inner case needs a check of its own (`decl.InnerTypeExpr == nil`) rather
// than a widened tolerance in a check built for scalars.
var stdMarkerSpecs = []stdMarkerSpec{
	{origin: "std/channels", nomi: "ChannelClosed", form: formDistinct, goType: reflect.TypeFor[rt.ChannelClosed]()},
}

// stdMarkerDefs is the process-wide *typeDef per spec, keyed by spec index.
//
// A genuine zero-sized MARKER, so `rtOpaque` is deliberately NOT set: there is
// nothing inside one, two of them ARE always equal, the hash IS the unit hash
// and `zeroSized()` IS true. That is the exact opposite of stdhost.go's `Bytes`,
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
var stdMarkerValidated = sync.OnceValue(func() []bool {
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
// declaration NODE it reached each spec through, and the spec index under the
// Nomi name. opaqueAnchors' body over the marker table.
func stdMarkerAnchors(fa *analysis.FileAnalysis) (map[*ast.TypeDef]int, map[string]int) {
	byDecl := map[*ast.TypeDef]int{}
	byName := map[string]int{}
	if fa == nil || fa.ModuleScope == nil {
		return byDecl, byName
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
		byName[s.nomi] = i
	}
	return byDecl, byName
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
	if decl.Name != s.nomi || s.form != want || !decl.Public {
		return false
	}
	if decl.InnerTypeExpr != nil {
		return false
	}
	// A body or a type body item is layout-relevant. An attached `//!` test is
	// not, and is deliberately not checked — see stdStructSpec.matches.
	if decl.HasBody || len(decl.Items) > 0 {
		return false
	}
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
	g.stdMarkerByDecl, g.stdMarkerByName = stdMarkerAnchors(g.fa)
}

// stdMarkerDeclared reports the spec index for a marker declaration this module
// CARRIES, or -1. Read by buildTypes: an anchored declaration adopts the shared
// def instead of getting its own shell. opaqueDeclared's counterpart, and the
// reason it is needed is that std/channels' own generated package declares
// `ChannelClosed` — without this the module would emit a second Go type for it.
func (g *gen) stdMarkerDeclared(decl *ast.TypeDef) int {
	g.loadStdMarkers()
	if i, anchored := g.stdMarkerByDecl[decl]; anchored {
		return i
	}
	return -1
}

// stdMarkerNamed resolves a type NAME to the shared def, for a module that
// MENTIONS a marker without declaring it. opaqueNamed's reasoning exactly.
func (g *gen) stdMarkerNamed(name string) (*typeDef, bool) {
	g.loadStdMarkers()
	i, anchored := g.stdMarkerByName[name]
	if !anchored {
		return nil, false
	}
	return stdMarkerDefs()[i], true
}

// projectStdMarker is inferred.go's projection for a marker: the DistinctType
// the checker solved for a position the program never annotated.
//
// The two DOORS — this one and stdMarkerNamed — are deliberately NOT symmetric,
// and the asymmetry is the opposite direction to the one projectStdOpaque warns
// about. That warning is about an ANNOTATION door that works while inference
// refuses, which is two answers to one question. Here inference is WIDER: it
// resolves through stdMarkerOfType, which needs no module anchor, because a
// marker reached as a std function's error type is never spelled by the program
// that reaches it. The annotation door keeps the anchor, because `c:
// ChannelClosed` IS a spelling and the module's scope is what says which
// declaration it names.
//
// The two cannot disagree on anything reachable: writing the name without
// importing it is a front-end error, so every position where the annotation door
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
