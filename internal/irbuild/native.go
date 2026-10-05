package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// The builder's kind layer: checked Nomi in, one ir.Func per body out.
//
// # Three invariants
//
// **Every Nomi expression is a statement sequence producing a value.** `and`
// and `or` must not evaluate their right operand early, and a Nomi block is an
// expression. So expr() lowers whatever statements it needs and returns an
// expression naming the result.
//
// **A type must be known before a value can be built.** Every expression
// carries a kind, every kind is derived from a declared annotation or inferred
// from a literal, and a construct whose kind cannot be established is REFUSED
// rather than guessed. The checked AST carries no inferred types (only the
// annotations the programmer wrote), so this inference is the builder's own
// and its limits are the blocker set.
//
// **A rejection never hides a subtree.** See unsupported.go: reject() records
// one blocker and keeps going, rejectWhole() records one and probes the
// children with lowering suppressed. A builder that stopped at the outermost
// refusal would report an incomplete blocker set.
//
// # Faults
//
// A Nomi runtime fault (integer overflow, a zero divisor) is an rt trap, which
// the VM reports as a fault. See rt/trap.go.

// kind is a Nomi type as this builder represents it.
//
// Six scalar rows and no table: the mechanical part of the representation is
// `Int`→`int64`, `Float`→`float64`, `String`→`string`, `Unit`→`rt.Unit`, and
// then Bool, which only looks mechanical. `Bool` is not a primitive — it is
// `pub enum Bool { embeds False; embeds True }` (std/bool.nomi:27-30), so it is
// an enum value like any other. `Bool → bool` is a
// compiler special case, and it is spelled here as one row of an enum rather
// than as a lookup in internal/ffitypes precisely so the special case is
// visible instead of implied. (It is the general rule taken to its limit:
// `embeds False` and `embeds True` are zero-sized payloads, which contribute
// nothing, so a two-variant enum with no storage is exactly one bit.)
//
// The seventh row is a NAMED type — a struct or enum declared in the module —
// and it is what makes `kind` a struct rather than an integer. A named kind
// carries the `*typeDef` its declaration produced, so kind equality is Go
// POINTER equality on the declaration: two same-named structs from two modules
// are two pointers and cannot be confused, and `kindInt == kindFloat` is false
// no matter how identical their layouts are. The opposite choice, identity as
// a string assembled at construction sites and re-parsed at lookup sites,
// fails silently. See types.go.
type kind struct {
	tag tag
	// def is the declaration a named type's identity IS; nil for every scalar.
	def *typeDef
	// comp is the interned identity of a STRUCTURAL type — one with no
	// declaration to point at, and whose tag covers more than one Go type.
	// nil for every other kind. See composite.go.
	comp *compKind
	// iface is the declaration an EXISTENTIAL's interface identity is — a
	// value whose static type is an interface and whose concrete type is
	// erased. Nominal, not structural, and deliberately not interned: every
	// interface is held the same way, so interning on storage would collapse
	// `Speaker` and `Greeter` into one kind: sound for storage and lossy for
	// DISPATCH, where nothing would fail and the wrong table would answer.
	// See impl.go.
	iface *ifaceDef
	// tp is the DECLARATION a BOUND-FREE type parameter's identity is, inside
	// the body of the generic function that declares it. Nominal like iface
	// and for the same reason: interning on the spelling would let one
	// function's `T` be bound from another's.
	// A BOUNDED type parameter is not this — it is an existential over its
	// bound and carries `iface`. See generic.go and dict.go.
	tp *typeParamDef
}

type tag uint8

const (
	// tagInvalid is "no native representation". A kind with it is returned
	// rather than panicked so a refusal can be recorded and the walk
	// continued; the lowering is discarded whenever any refusal was recorded.
	tagInvalid tag = iota
	tagUnit
	tagInt
	tagFloat
	tagString
	tagBool
	tagNamed
	// tagFunc is a Nomi function value — a lambda, or anything else of a
	// declared `(A, B) -> C` type. Structural: its identity is its interned
	// *compKind, not its tag.
	tagFunc
	// tagIface is an existential. Its identity is the *ifaceDef, so two
	// interfaces are two kinds however identically they lower.
	tagIface
	// tagList is `List<T>` and tagTuple `(A, B, …)`. Both structural: the
	// identity is the interned *compKind, not the tag, because one tag covers
	// unboundedly many Go types. See collections.go.
	tagList
	tagTuple
	// tagAnonStruct is `{x: Int, y: String}` — a RECORD, structural like the
	// tuple beside it and canonicalized by field name rather than by position.
	// Its identity is the interned *compKind, which additionally carries the
	// field NAMES: a tuple's components are addressed by index and a record's
	// by name, so the names are part of what the interned entry has to answer.
	// See anonstruct.go.
	tagAnonStruct
	// tagEmptyList is the untyped `[]` — a list literal with no elements and
	// therefore no element type. Its own tag rather than a `List<Unit>` in
	// disguise, so nothing can mistake it for a real list of anything; coerce
	// widens it wherever a `List<T>` is wanted. See collections.go.
	tagEmptyList
	// tagBareNone is the prelude `None` written without a type argument. Its
	// own tag, and not a `Maybe<Unit>` in disguise, for the reason
	// tagEmptyList has one: it is an UNTYPED literal whose type the checker
	// solves from context, and coerce discharges it at the one place that
	// context is known. See prelude.go.
	tagBareNone
	// tagMap is `Map<K, V>`. Structural, like tagList: the identity is the
	// interned *compKind. See maps.go — and note that its key operations are
	// EMITTED function values rather than anything the tag carries, because a
	// Nomi map key is compared structurally and not by any user impl.
	tagMap
	// tagEmptyMap is `Map.empty()` before its type arguments are known — the
	// third untyped literal, alongside tagEmptyList and tagBareNone, and it
	// exists for the same reason: the checker solves the type from context and
	// coerce discharges it at the one place that context is known.
	tagEmptyMap
	// tagEmptySet is the untyped `#{}` — the FOURTH untyped literal, alongside
	// tagEmptyList, tagBareNone and tagEmptyMap, and it exists for exactly
	// their reason: a set literal with no elements has no element type, the
	// checker solves it from context, and coerce discharges it at the one place
	// that context is known.
	//
	// It is NOT `Set<Unit>`: `Set<Int>` is a NAMED kind (a generic std struct
	// instance, tagNamed), so an untyped `#{}` cannot be an instance of it with
	// a placeholder argument the way `[]` cannot be a `List<Unit>`. See
	// stdgenstruct.go and sets.go.
	tagEmptySet
	// tagEmptyVector is the untyped `#[]` — the FIFTH untyped literal, alongside
	// tagEmptyList, tagBareNone, tagEmptyMap and tagEmptySet, and it exists for
	// exactly their reason: a vector literal with no elements has no element
	// type, the checker solves it from context, and coerce discharges it at the
	// one place that context is known. `06-collections/vectors_test.nomi` writes
	// `empty: Vector<Int> = #[]`, so the annotation IS that context.
	//
	// It is NOT `Vector<Unit>`: `Vector<Int>` is a NAMED kind (a generic std HOST
	// type instance, tagNamed), so an untyped `#[]` cannot be an instance of it
	// with a placeholder argument the way `[]` cannot be a `List<Unit>`. See
	// stdgenhost.go and vectors.go.
	tagEmptyVector
	// tagSeq is a lowered `Iter<T>`: the push protocol's `Seq<T>`, which is
	// `rt.Seq[T]`. Structural, like tagList — the identity is the interned
	// *compKind.
	//
	// NOT tagIface, even though `Iter<T>` is an interface-typed position and
	// every other existential is `rt.Dyn` keyed on an *ifaceDef. The push
	// protocol has no `Self` anywhere in its signature, so an `Iter<T>` is
	// exhausted by its `each_while` — nothing about the concrete source is
	// observable through it, and a closure IS that function. Erasing to
	// `rt.Dyn` instead would put a table probe on the per-element path for a
	// dispatch with exactly one possible answer. See iter.go and rt/seq.go.
	tagSeq
	// tagTypeParam is a BOUND-FREE type parameter, inside the body of the
	// generic function that declares it. Its Go type is `any` — one body, one
	// instantiation, value-passing — and its identity is the *typeParamDef,
	// which is what keeps two functions' `T` apart. It never escapes that
	// body: a call site substitutes the concrete kind the arguments bound, so
	// no signature outside a generic `fn` ever holds one. See generic.go.
	tagTypeParam
)

var (
	kindInvalid = kind{}
	kindUnit    = kind{tag: tagUnit}
	kindInt     = kind{tag: tagInt}
	kindFloat   = kind{tag: tagFloat}
	kindString  = kind{tag: tagString}
	kindBool    = kind{tag: tagBool}
	// kindBareNone is the prelude `None` before its type argument is known.
	// See prelude.go: it exists only to be discharged by coerce.
	kindBareNone = kind{tag: tagBareNone}
	// kindEmptyMap is `Map.empty()` before its type arguments are known. Its
	// Go type is the only one an entry-less map can be given, and it is never
	// wrong because there is no entry to observe. See maps.go.
	kindEmptyMap = kind{tag: tagEmptyMap}
	// kindEmptySet is `#{}` before its element type is known — the sentinel
	// kindEmptyMap is one level up. See sets.go.
	kindEmptySet = kind{tag: tagEmptySet}
	// kindEmptyVector is `#[]` before its element type is known. See vectors.go.
	kindEmptyVector = kind{tag: tagEmptyVector}
)

// named is the kind of values of a declared struct or enum.
func named(d *typeDef) kind { return kind{tag: tagNamed, def: d} }

// key is k's identity as a string, for a map keyed on a tuple of kinds: the
// tag and the declaration, interned entry, interface or type parameter that
// is the kind's identity.
func (k kind) key() string {
	return fmt.Sprintf("%d/%p/%p/%p/%p", k.tag, k.def, k.comp, k.iface, k.tp)
}

// zeroSized reports whether values of this kind occupy no storage, which is
// what lets a zero-sized enum payload be stored nowhere at all.
//
// Deliberately NOT generic over structural kinds: whether one stores nothing
// depends on the tag. A Go func value is a pointer pair and never vanishes,
// while a hypothetical `(Unit, Unit)` genuinely stores nothing — so each
// structural tag answers for itself rather than inheriting a guess.
func (k kind) zeroSized() bool {
	return k.zeroSizedOn(nil)
}

// zeroSizedOn is zeroSized inside a walk already standing on path. See
// types.go's typePath for why the guard is a parameter and not a field.
func (k kind) zeroSizedOn(path typePath) bool {
	switch k.tag {
	case tagUnit:
		return true
	case tagNamed:
		return k.def.zeroSizedOn(path)
	case tagFunc:
		return false
	case tagList, tagEmptyList:
		// A cons cell behind a pointer: one word, never nothing.
		return false
	case tagMap, tagEmptyMap:
		// A trie root pointer plus a count and a sequence: three words.
		return false
	case tagEmptySet:
		// A one-field struct over the map above.
		return false
	case tagEmptyVector:
		// A slice header plus two ints: five words.
		return false
	case tagSeq:
		// A one-field struct holding a Go func value: a pointer pair.
		return false
	case tagTuple, tagAnonStruct:
		// A tuple or record of nothing but zero-sized parts genuinely stores
		// nothing, which is the same rule that makes `Bool` one bit. Field
		// NAMES cost no storage, so the record answers by the same walk.
		for _, p := range k.comp.parts {
			if !p.zeroSizedOn(path) {
				return false
			}
		}
		return true
	}
	return false
}

// nomi is the type's Nomi name, for a refusal's detail text.
func (k kind) nomi() string {
	if k.comp != nil {
		return k.comp.nomi
	}
	switch k.tag {
	case tagUnit:
		return "Unit"
	case tagInt:
		return "Int"
	case tagFloat:
		return "Float"
	case tagString:
		return "String"
	case tagBool:
		return "Bool"
	case tagNamed:
		return k.def.nomi
	case tagIface:
		return k.iface.nomi
	case tagTypeParam:
		return k.tp.nomi
	case tagEmptyList:
		return "List<_>"
	case tagBareNone:
		return "Maybe<_>.None"
	case tagEmptyMap:
		return "Map<_, _>"
	case tagEmptySet:
		return "Set<_>"
	case tagEmptyVector:
		return "Vector<_>"
	}
	return "an unsupported type"
}

// expr is a value a destructuring prologue works on: the temporary that holds
// it, when it is held in one yet, and its kind. `t` is ir.NoTemp for a value
// no instruction computes (a zero-sized payload, a refused value), which
// irHold names with a fresh temporary.
type expr struct {
	t ir.Temp
	k kind
}

// fnSig is a module function as its call sites see it.
type fnSig struct {
	params []kind
	result kind
	// decl is the declaration itself, read for the parameter shapes a call
	// site has to reproduce: a DEFAULT is evaluated at the call rather than
	// stored, so its expression has to be reachable from there (see
	// defaults.go), and a DESTRUCTURING parameter's pattern is what names the
	// locals its body reads.
	decl *ast.FuncDef
	// lowerable is false for a function whose own declaration was refused. A
	// call to one is refused too, rather than emitted against a signature that
	// was never written.
	lowerable bool
	// dict is the type-parameter dispatch seam for a BOUNDED generic function,
	// nil for everything else. It is read at a call site for two things a
	// non-generic signature has neither of: the `*rt.TypeID` arguments that
	// precede the value arguments, and which type parameter each one answers
	// for. See dict.go.
	dict *dictSeam
	// tps are the type parameters of a BOUND-FREE generic function, in
	// declaration order; nil for every monomorphic one and for every bounded
	// one, which uses `dict` instead. Non-nil is what makes `params` and
	// `result` a TEMPLATE rather than a signature: a tagTypeParam slot is
	// substituted per call site from the arguments that bound it. See
	// generic.go.
	tps []*typeParamDef
	// genericWhy is the construct funcDecl reports for a generic declaration
	// the builder does not lower, or "" when there is nothing to report.
	// Recorded here rather than re-derived so signature() and funcDecl cannot
	// disagree about which of them decided.
	genericWhy string
	// mono is the monomorphization template this declaration is, or nil.
	//
	// Non-nil means the declaration is NOT a function at all — it is a rule for
	// making them — so funcDecl emits nothing and refuses nothing for it, and
	// directCall resolves each call to a concrete instance instead. Set only
	// where genericSignature declined on the DEPTH rule; every other reason it
	// declines for keeps its funcDecl refusal. See genericmono.go.
	mono *monoTemplate
	// irMono is the template irMonoTemplate derived for an ERASED generic
	// (tps or dict set), nil when it has none; irMonoAsked says it was asked.
	irMono      *monoTemplate
	irMonoAsked bool
	// instance identifies the concrete function produced from a mono template.
	// Its source declaration is shared by every specialization.
	instance *monoInst
}

// local is a Nomi name bound to a Go local.
type local struct {
	k kind
}

type gen struct {
	nomiPath string
	pkg      string
	// stdUnlowered names, for a stdlib gen, each function whose body the IR
	// did not lower, with the reason. See irnative.go.
	stdUnlowered map[string]string
	// irPrebuilding is set while a stdlib module's graphs are built ahead of
	// its module walk; see irPrebuildStdBodies.
	irPrebuilding bool
	// irStdPending holds, during that prebuild, the bodies assumed to build,
	// so a recursive or forward sibling call links before its callee's graph
	// is in the module (irStdSiblingSym). Keyed by declaration key.
	irStdPending map[string]bool
	// stdModule is the stdlib module's short name ("iter", "list", …) when this
	// gen is lowering a stdlib module, and "" for every user gen. Set by
	// newStdGen and by nothing else, so it is the CALLER's statement of which
	// embedded stdlib module is being lowered rather than a fact re-derived
	// from a path — which is what makes it usable as an identity.
	//
	// Read for exactly one thing: a stdlib module is the DECLARING module of
	// the types and interfaces it declares, so a provenance rule written for
	// "a user file reaching std" has to be able to recognise the one file the
	// rule cannot sensibly be applied to. See loadIter's declaresStdIter.
	stdModule string
	// fa is the module's own analysis, or nil. Read for exactly one thing:
	// the type the checker SOLVED for a construct the AST does not annotate.
	// See inferred.go — every read goes through inferredKind, so there is one
	// place that decides what the analyzer is allowed to answer for.
	fa *analysis.FileAnalysis

	// nomiLine is the Nomi line the next lowered statement is attributed to.
	nomiLine int

	tmp    int
	scopes []map[string]local
	funcs  map[string]*fnSig
	// irValTypeMemo is the IR value type of each kind this unit has mapped;
	// see irvaltype.go. A declared type is entered before its fields are
	// mapped, which is what lets a recursive type reach itself.
	irValTypeMemo map[kind]*ir.ValType
	irValTypeBusy map[kind]bool
	// irMod is this compilation unit's `ir.Module`: the declarations the
	// IR holds for it, the retained `ir.Func`s and the module-scoped storage
	// cells. Minted on first use, like `irTable` and for its reason: a
	// container nobody asked for is an allocation nobody reads.
	//
	// A module rather than a list of functions, so "every declaration this
	// module names, each named once" has a place to be checked:
	// `ir.LintModule`.
	//
	// PER GEN, WHICH IS THE SAME SCOPE AS `irTable` AND FOR THE SAME REASON.
	// `ir/table.go` requires one table per LOWERING rather than process-wide,
	// because the producer's identity tokens include pointers this package
	// shares process-wide under a mutex. A module container is one per
	// lowering too. What would contradict that argument is an `ir.Program`,
	// which this is not — see ir/module.go on the three side tables that need
	// program scope and why the table argues against supplying it.
	//
	// Retained past the module walk, so the VM can read the graphs after the
	// walk finishes.
	//
	// Not restored by `snapshot()`, matching `irTable` and `tmp`
	// above — and its function list cannot grow on a discarded run anyway,
	// because `irScalarBuild` declines while `g.probing > 0`.
	irMod *ir.Module
	// irHostKeys are the extern keys this unit's Go-bound `host fn` bodies
	// cross under. See irhostfn.go.
	irHostKeys []string
	// irTestBoots are the `tests` group boots this unit has offered for VM
	// retention, in the order cases first staged them.
	irTestBoots []*ir.Symbol
	// irTestRetries are the ungrouped cases whose VM-only attempt runs
	// after the program's module walk; see irRetryWalkOnlyTestBodies.
	irTestRetries []irTestRetry
	// irBoot is set while irBootRetain builds the program boot's body. That
	// body admits Context values and is retained for the VM only.
	irBoot bool
	// irPro is the shell whose DESTRUCTURING PROLOGUE is being emitted, or
	// nil. While it is installed the construct-and-consume lowering of an
	// irrefutable parameter pattern is also RECORDED into the retained
	// function: `irHold` mints from the function's namespace and `irAppend`
	// puts each node in the entry block. See irparam.go, which argues why the
	// prologue is recorded rather than rebuilt.
	irPro *irFuncShell
	// irTable is the shared IR's type and declaration table for this
	// compilation unit: the interning arena that gives a Nomi type and a Nomi
	// declaration an identity the representation itself can compare, so a
	// selection rule over operand types is expressible in `internal/ir`
	// instead of only over this package's own `kind` and `*implItem`.
	//
	// PER GEN AND LAZILY POPULATED. Per gen because its identity tokens
	// include `*typeDef` pointers this package shares process-wide under a
	// mutex, so a shared table would be a map two lowerings mutate. Lazily
	// because whether a type implements an interface is answered by
	// `bindsImpl` over a registry built during lowering, and mirroring that
	// eagerly would be a second implementation of a question that already has
	// one. See ir/table.go's header and irtable.go.
	//
	// Not restored by `snapshot()`, matching `tmp` above. An
	// entry a discarded run interned is a type or a declaration that really
	// exists in the program, and nothing reads the table by enumeration: an
	// entry nobody asks for again costs a map slot and cannot change an
	// answer, whereas rolling it back would mint a second identity for one
	// declaration on the next attempt, which is the one thing an identity
	// table must not do.
	irTable *ir.Table
	// onces is the module's `once` bindings by Nomi name, and onceOrder the
	// same in declaration order. Typed before any body is emitted, because a
	// `once` may be read from above its own declaration. See once.go.
	//
	// oncesByDecl is the same defs under their *ast.OnceBinding, which is what
	// a reference from ANOTHER file resolves to: `import lib.{config as cfg}`
	// binds `cfg` locally and the declaration is `config`, so the local
	// spelling is not a key into this table and the DECLARED spelling is not
	// available to the reference site without the node. See
	// bareSiblingOnceRef.
	onces       map[string]*onceDef
	oncesByDecl map[ast.Node]*onceDef
	onceOrder   []*onceDef
	// types is the module's struct and enum declarations, by Nomi name. The
	// *typeDef a name resolves to IS the type's identity everywhere downstream
	// (see types.go), so this map is the only place a type NAME is looked up.
	types map[string]*typeDef
	// typeOrder is the declarations in source order, for the size probe.
	typeOrder []*typeDef
	// The type declarations written INSIDE a body. blockTypes is one entry per
	// *ast.Block that declares any, blockTypeOrder the same blocks in source
	// order, and blockLocalOrder every such declaration flat — which is what
	// settleLowerable relaxes over and what flushBlockLocalTypes emits.
	//
	// Kept OUT of `types` above, which is keyed on the bare name and therefore
	// cannot hold two bodies' `Point`. typeScopes is the active overlay stack,
	// pushed and popped with the block being emitted; namedType and typeOf
	// consult it ahead of `types`. See blocklocaltype.go.
	blockTypes      map[*ast.Block]*blockTypeDecls
	blockTypeOrder  []*ast.Block
	blockLocalOrder []*typeDef
	typeScopes      []*blockTypeDecls
	// The USER generic struct declarations and their monomorphized instances.
	// genericTemplates is by Nomi name, built from this module's nodes;
	// genericInsts interns one *typeDef per (template, argument tuple) and
	// genericInstOrder keeps them in creation order. genericSubst is the
	// active substitution stack, pushed while an instance's fields are
	// resolved and consulted by typeOf ahead of every other name lookup.
	//
	// PER-GEN and not process-wide, unlike preludeInsts' shared table: a user
	// instance's field type names a type ONE generated package declares, so an
	// instance cannot be shared across packages. See generictype.go.
	genericTemplates map[string]*genericTemplate
	genericInsts     map[string][]*typeDef
	genericInstOrder []*typeDef
	// genericImplQueue is one implDef per (impl block, instance) pair, drained
	// by flushGenericImpls at the end of the module walk. A QUEUE rather than a
	// map because an entry is emitted exactly once and emitting one can append
	// more. See genericimpl.go.
	genericImplQueue []*genericImplInst
	// genericImplsDone is how much of genericImplQueue flushGenericImpls has
	// built.
	genericImplsDone int
	// methodInsts interns one item per instantiation of an impl function
	// with its own type parameters; methodInstQueue holds them for
	// flushMethodInsts. See methodinst.go.
	methodInsts     map[string]*methodInst
	methodInstQueue []*methodInst
	// fieldDefaults interns one accessor per field default another file's
	// literal omitted; fieldDefaultQueue holds them for flushFieldDefaults.
	// See foreignfielddefault.go.
	fieldDefaults     map[irFieldDefaultFill]*irFieldDefaultReq
	fieldDefaultQueue []*irFieldDefaultReq
	genericSubst      []map[string]kind
	// ifaceInsts is one monomorphized *ifaceDef per (impl block, receiver) of a
	// GENERIC interface. PER-GEN for genericInsts' reason: an instance's method
	// kinds can name a type one generated package declares. The key needs its
	// RECEIVER half; see ifaceinst.go.
	ifaceInsts map[ifaceInstKey]*ifaceInst
	// The generic FUNCTION templates' monomorphized instances. monoInsts
	// interns one instance per (declaration, argument tuple), monoQueue is the
	// lowering worklist, monoSig is the instance funcDecl is currently
	// lowering, and monoDepth is the length of the instantiation chain that
	// reached it — the number the instantiation guard reads.
	//
	// PER-GEN for genericInsts' reason: an instance's parameter kinds can name
	// a type ONE generated package declares. See genericmono.go.
	monoInsts map[string]*monoInst
	monoQueue []*monoInst
	monoSig   *fnSig
	monoDepth int
	// stdInstName overrides the function name emitStdFunc uses, for the
	// length of one instance's lowering.
	//
	// `monoSig` above is the precedent and the shape is the same: funcDecl is
	// REUSED rather than mirrored, with the one thing that differs handed over
	// on the gen. A copy of emitStdFunc's tail would be a second place
	// for the destructuring, the return-kind check and the refusal reporting to
	// drift out of agreement with the first.
	stdInstName string
	// stdInsts is the program's table of generic stdlib instances, shared by
	// every gen one GenerateIR call builds. See stdinst.go.
	stdInsts *stdInstances
	// stdInstCur is the instance an instance gen builds.
	stdInstCur *stdInst
	// comps interns the module's structural types, grouped by Nomi spelling
	// and separated within a group by component kinds; see composite.go.
	comps map[string][]*compKind
	// The prelude enums, resolved against this module's analysis. See
	// prelude.go: preludeByDecl is keyed by the *ast.EnumDef the analyzer
	// resolved to, which IS the identity; preludeByName is the same anchors
	// under their Nomi spelling, for a pattern head's qualifier. preludeInsts
	// interns one *typeDef per instantiation and preludeOrder keeps them in
	// creation order so unlowerability propagates deterministically.
	preludesLoaded bool
	preludeByDecl  map[*ast.EnumDef]*preludeAnchor
	preludeByName  map[string]*preludeAnchor
	preludeInsts   map[string][]*typeDef
	preludeOrder   []*typeDef
	// The GENERIC STD types' per-gen instances — `Channel<Request>`,
	// `Sender<Request>`, `Set<Point>`. Interned here rather than process-wide
	// exactly when a type ARGUMENT is package-relative, which is the split
	// preludeInsts above has and for the same reason: `rt.Channel[int64]`
	// renders identically in every generated package and
	// `rt.Channel[NomiT_Request]` does not.
	//
	// Two tables and not one because the two families are two `*typeDef`
	// SHAPES: a generic std struct instance has resolved FIELDS, a generic std
	// host instance is an opaque leaf. See stdgenstruct.go and stdgenhost.go.
	genStructInsts map[string][]*typeDef
	genStructOrder []*typeDef
	genHostInsts   map[string][]*typeDef
	genHostOrder   []*typeDef
	// The stdlib opaque newtypes, resolved against this module's analysis. See
	// opaque.go: opaqueByDecl is keyed by the *ast.TypeDef the analyzer
	// resolved to, which IS the identity, and opaqueByName is the same anchors
	// under their Nomi spelling for a module that MENTIONS one without
	// declaring it. Both values are an index into opaqueSpecs; the *typeDef
	// itself is process-wide rather than per-gen, because a stdFunc's kinds are
	// built once and compared by pointer from a different gen.
	opaquesLoaded bool
	opaqueByDecl  map[*ast.TypeDef]int
	opaqueByName  map[string]int
	// The monomorphic stdlib enums, resolved against this module's analysis.
	// Same two maps and the same process-wide-def reasoning as the opaque
	// anchors above, keyed on *ast.EnumDef instead. See stdenum.go.
	stdEnumsLoaded bool
	stdEnumByDecl  map[*ast.EnumDef]int
	stdEnumByName  map[string]int
	// The stdlib opaque STRUCTS, on the same footing again and keyed on
	// *ast.StructDef. Distinct from the opaque family above because the
	// subjects are records with named fields rather than newtypes over a
	// scalar, so the shape check has to pin the field set. See stdstruct.go.
	stdStructsLoaded bool
	stdStructByDecl  map[*ast.StructDef]int
	stdStructByName  map[string]int
	// The GENERIC stdlib structs (`Set<T>`), resolved against this module's
	// analysis. ONE map, by Nomi name, and no `loaded` flag beside it: a nil map
	// IS the unloaded state, because stdGenStructAnchorsOf never returns nil.
	// The instances themselves are interned process-wide only — there is no
	// per-gen fallback, so there is no per-gen order to keep either. See
	// stdgenstruct.go.
	genStructs map[string]*stdGenStructAnchor
	// The GENERIC stdlib HOST types (`Sender<T>`, `Receiver<T>`), resolved
	// against this module's analysis. ONE map by Nomi name, and no `loaded`
	// flag, for genStructs' reason exactly. See stdgenhost.go.
	genHosts map[string]*stdGenHostAnchor
	// The stdlib nil-inner MARKERS (`ChannelClosed`). Two maps and the same
	// process-wide-def reasoning as the opaque anchors above, keyed on
	// *ast.TypeDef — the SAME declaration kind, distinguished by having no
	// inner type at all rather than a scalar one. See stdgenhost.go.
	stdMarkersLoaded bool
	stdMarkerByDecl  map[*ast.TypeDef]int
	stdMarkerByName  map[string]int
	// The BLESSED stdlib primitives (`Byte`, `Bytes`), resolved against this
	// module's analysis. ONE map where the three families above have two,
	// because there is no declaration node to key on: the type is an analyzer
	// singleton and the builder has no path for the `pub host type` surface
	// declaration at all. See stdhost.go.
	stdHostsLoaded bool
	stdHostByName  map[string]int
	// The stdlib INTERFACE anchors. ONE map, where the four families above
	// have two, and deliberately: this family's anchor is gated on an IMPORT of
	// the declaring std module, so a declaration-keyed index could only ever hold
	// a node some OTHER module carries. The declaration-node question its four
	// siblings answer ("is the declaration in front of buildTypes one of these")
	// is therefore unanswerable from it. See stdiface.go.
	stdIfacesLoaded bool
	stdIfaceByName  map[string]int
	// structIfaceOK is whether this module reaches std's `Struct` — the
	// universal structural marker, which has no spec row because it is no
	// dispatch table. Loaded beside the anchors above so one `loadStdIfaces`
	// answers for the whole family. See structiface.go.
	structIfaceOK bool
	// ifaces is the module's interface declarations, by Nomi name. The
	// *ifaceDef a name resolves to IS the interface's identity, which is what
	// keeps `Greeter.greet` and `Farewell.greet` two different tables on a
	// type implementing both. See impl.go.
	ifaces map[string]*ifaceDef
	// ifaceOrder is those declarations in source order, so signature
	// resolution and lowering never depend on map iteration order.
	ifaceOrder []*ifaceDef
	// dict is the type-parameter dispatch seam of the generic function whose
	// SIGNATURE or BODY is being lowered right now, nil everywhere else. It is
	// the bounds of each type parameter plus its concrete type argument: typeOf
	// reads it to answer what `T` means, and a `T.method(x)` call reads it for
	// both the table and the dictionary. Saved and restored rather than
	// cleared, because a nested `fn` inside a generic is its own boundary and a
	// generic signature may be resolved while another body is open. See
	// dict.go.
	dict *dictSeam
	// implOrder is every impl block in source order, and implsByIface indexes
	// them by (interface spelling, receiver kind) for dispatch.
	implOrder    []*implDef
	implsByIface map[string]map[kind]*implDef
	// operImpls is the PARALLEL index for the four stdlib operator interfaces,
	// keyed (interface, receiver, RIGHT-HAND type) — the axis implsByIface's key
	// drops. A receiver legally carries SEVERAL impls of one operator interface
	// (std/calendar ships twelve `Add` impls for NaiveDateTime), so that map
	// collapses them and refuses all of them as duplicates.
	//
	// A second index rather than a wider shared key, and the argument is about
	// one consultation site rather than about effort: `noEquatableImpl` reads
	// `implsByIface["Equatable"][k]` to decide whether `==` may take the
	// STRUCTURAL fallback, and under a widened key a lookup that lost its
	// existential over the rhs axis would answer "no impl" for a type that has
	// one — a wrong answer on a comparison that lowers, not a refusal.
	// Nothing operator-shaped is registered in implsByIface at all, so every
	// lookup in it is unaffected by construction. See operimpl.go.
	//
	// Used for the duplicate rule. SELECTION scans operOrder.
	operImpls map[operKey]*implDef
	// operOrder is every registered operator impl in registration order: the
	// blocks on concrete receivers, which also sit in implOrder, and the
	// per-instance blocks of a generic receiver (`impl Add<T, Box<T>> for
	// Box<T>` at Box<Int>), which registerImplAt never puts in implOrder.
	operOrder []*implDef
	// operAnchors memoizes "is this name the stdlib operator interface", per
	// module. Nil until the first impl block asks.
	operAnchors map[string]*operIfaceSpec
	// implBlock is the USER impl block whose function body is being lowered
	// right now, or nil outside one. It exists for exactly one question: a
	// bare `each_while(left, yield)` inside `impl Iter for Tree` names a
	// member of THAT block, and a member is in neither g.funcs nor the file's
	// module scope, so without this the call would be refused as
	// `call to an unlowered function`, a name that describes neither the
	// construct nor the obstacle. Saved and restored around the body rather than
	// set once, because an impl function may lower a lambda that lowers
	// another call, and because a nested `fn` is its own boundary. See
	// implselfcall.go.
	//
	// `implSelf` further down is the STDLIB counterpart of the same idea and
	// is the precedent this follows: it names the receiver of the std impl
	// block being emitted, so a bare `to_string(x)` inside
	// `impl Debug for Float` resolves to that type's own method. Two fields
	// rather than one because the two carry different things — a receiver
	// SPELLING there, the block itself here — and because they are live in
	// disjoint phases, so merging them would create a shared field with two
	// meanings and no way to assert which one is set.
	implBlock *implDef
	// tids is each type this file declares that has a runtime identity for
	// existential dispatch.
	tids map[kind]bool
	// anonTids is each structural type this file has asked rt's identity of.
	anonTids map[kind]bool
	// tailUnits is one node per lowered function — free or impl — and the
	// module's tail-call graph over them. See tail.go. Keyed per function
	// rather than by free-function NAME, so an impl function is in the graph.
	tailUnits  []*tailUnit
	tailOfFn   map[string]int
	tailOfItem map[*implItem]int
	// tailPlanOf is the cycle each unit belongs to, or nil when it cannot
	// recur through tail calls.
	tailPlanOf []*tailPlan
	// tail is the driver loop currently being emitted, or nil. A tail hop is
	// emitted only when the callee is a member of THIS plan, and this field is
	// cleared at every boundary that opens a Go func literal — a lambda, a
	// nested `fn`, a `once` initializer, an inlined loop callback — because a
	// `continue` cannot cross one.
	tail *tailPlan
	// result is the result kind of the function being emitted: the declared
	// one for a `fn`, and for a lambda the one inferred so far (see lambda.go
	// — a lambda declares no return type, so its `return`s and its final
	// expression establish it between them).
	result kind
	// inferResult is non-nil while a LAMBDA body is being emitted, and is
	// what makes `return` lambda-scoped rather than function-scoped: it
	// carries the innermost lambda's result inference, so a `return` checks
	// against the lambda's answer and not the enclosing `fn`'s.
	inferResult *resultInference
	// wantOf is the coercion target an expression will be CHECKED AGAINST at its
	// own position, keyed by that expression's NODE.
	//
	// It is not a general expected-type channel and must not become one. What
	// keeps it from being one is the two properties below, and the ENUMERATION —
	// which is the safety argument, so adding a writer or a reader is an edit to
	// this comment. A stale enumeration reads as a completed audit.
	//
	//	WRITERS  binding                 — an ANNOTATED binding's initializer
	//	         stmtInto's default arm   — a statement's value position: a
	//	                                    function's declared result, a `case`
	//	                                    or `if` arm's reconciled kind
	//	READERS  channelWantedInstance    — `Channel.unbuffered()`, which records
	//	                                    no instantiation (concurrent.go)
	//	         annotatedList            — a list literal whose element type only
	//	                                    the target names (collections.go)
	//	         preludeWantedArgs        — a prelude instantiation the CHECKER
	//	                                    left unsolved (prelude.go)
	//
	// PROPERTY 1, NODE-KEYED. Identity on the node rather than a current
	// expectation, which is pipedCall's and pipedCase's discipline: no scope to
	// push, no boundary to clear, and no way for a nested lowering to resolve
	// against an outer expectation by accident.
	//
	// PROPERTY 2, A READER MAY ONLY CLOSE WHAT THE BUILDER WOULD HAVE GUESSED.
	// Every reader uses it to type a value the builder cannot type on its own —
	// an unbound channel instantiation, an untyped list, an unsolved prelude
	// argument — and never to overrule something it CAN. That is what makes the
	// two writers interchangeable despite deriving their kinds differently:
	// binding's comes from the ANALYZER's solved type, projected, and stmtInto's
	// from the builder's own reconciliation, and both are exactly the kind
	// gen.assign is about to coerce and compare against. So a wrong want costs a
	// refusal at the same position and never a silently different lowering.
	wantOf map[ast.Node]kind
	// lambdaWantFor is the lambda a binding is lowering and lambdaWant the
	// result type the checker solved for it (lambdaResultWant). One slot:
	// the binding lowers its own lambda before anything else can ask.
	lambdaWantFor *ast.Lambda
	lambdaWant    kind
	// expanding is the PATH of structural comparators and hashers currently
	// being assembled, so a type that can reach itself declines instead of
	// taking the builder to `fatal error: stack overflow`. Keyed on the
	// `*typeDef` POINTER, which is the type's identity. See maps.go.
	expanding map[*typeDef]bool

	errs []Unsupported
	// masked is every position at which a blocker was NOT reported because the
	// operand being judged had no kind. It is the blocker set's own statement
	// of its incompleteness; see suppression.go.
	masked []Suppression

	// tests is the module's lowered `test` declarations, in declaration order,
	// and declaresTests says this module is the one whose tests run. inTest
	// and traceVar are the two facts an `assert` needs about where it sits.
	// See tests.go.
	tests         []loweredTest
	declaresTests bool
	inTest        bool
	// std is the lowered standard library as this module's call sites see it,
	// or nil for a module lowered without one. See stdlib.go.
	std *stdlibIndex
	// stdSiblings and implSelf are set only while a STDLIB module is being
	// lowered: the first is every sibling function in that module keyed
	// `<receiver>.<name>` (receiver empty for a top-level declaration), the
	// second is the receiver of the impl block whose item is being emitted.
	// Together they are how a bare `to_string(x)` inside `impl Debug for Float`
	// resolves to that type's own method, through the receiver's method table.
	stdSiblings map[string]*stdFunc
	implSelf    string
	// aliasResolving is the stack of MODULE-LEVEL `typealias` names currently
	// being expanded, so a cycle is refused rather than recursed. The front end
	// rejects a cyclic alias before the builder sees it, so this is
	// a crash guard over a provably empty population. See typealias.go.
	aliasResolving []string
	// stdOnces is every `once` THIS stdlib module declares, keyed `<Type>.<name>`,
	// and is set only while a stdlib module is being lowered. Apart from g.std
	// because bindStdSiblings rebuilds that index for the recursive phase and a
	// `once` takes no part in the phase split. See stdonce.go.
	stdOnces map[string]*stdOnce
	// files is every user file in the program as this module's call sites see
	// them, or nil for a module lowered alone. fileUnit is this module's own
	// index in it, or -1. usedFiles names the other files the lowered body
	// referenced. See siblings.go.
	files    *fileIndex
	fileUnit int
	// reg is the program-wide type registry, or nil for a module lowered
	// alone. foreignDefs interns this package's MIRROR of another package's
	// type, keyed by the declaration node — which IS the type's identity, so
	// two names for one declaration reach one mirror. importWhy carries the
	// reason the last import refused. typesBusy and typesDone drive the
	// demand-driven type-table build. See foreign.go.
	reg         *typeRegistry
	foreignDefs map[ast.Node]*typeDef
	// foreignInstDefs is foreignDefs for a MONOMORPHIZED instance of another
	// package's generic template, keyed by the OWNER's instance def rather
	// than by a declaration node — because every instance of one template
	// shares that node and keying on it would hand `Holder<Int>` and
	// `Holder<String>` one mirror. The owner's def pointer IS the
	// instantiation's identity there, which is the property genericInstKey
	// establishes. See foreign.go's importGenericInstance.
	foreignInstDefs map[*typeDef]*typeDef
	importWhy       string
	typesBusy       bool
	typesDone       bool
	// funcsDone marks declareFuncs having finished, which is the earliest this
	// package's impl table is COMPLETE. A cross-package question about whether
	// the program declares an impl at all — noEquatableImpl's foreign arm —
	// answers "unknown" while any gen is short of it rather than reading a
	// half-built table, and unknown over-refuses. See foreign.go.
	funcsDone bool
	// nodes is the module's checked AST, held so the type table can be built
	// on demand from another file's resolution rather than only in order.
	nodes []ast.Node
	// typeParamNames is every TYPE VARIABLE any declaration in this module
	// introduces, gathered lazily and only when a refusal has to decide
	// whether an unresolvable name is a type parameter or a typo. Nil until
	// then, because most modules never ask. See sigreason.go.
	typeParamNames map[string]bool
	// nestedTypeKeys maps a type name this module declares in a NESTED position
	// — inside a `test` body, a function body, any block — onto the tally key
	// that declaration's OWN refusal carries, and "" when two nested
	// declarations of one name disagree on it. Gathered lazily beside
	// typeParamNames and only when an unresolvable type name has to be told
	// apart from a name nothing declares at all. See sigreason.go.
	nestedTypeKeys map[string]string
	// implEmitting is the impl block whose item `implFunc` is currently
	// emitting, or nil outside one.
	//
	// It exists for exactly one question and should not grow a second reader
	// without a reason: a bare call to a REQUIREMENT of that block's interface
	// — from an inherited default body monomorphized here, or from a function
	// the block wrote itself — resolves to this receiver's implementation, and
	// that answer is a property of the enclosing impl, which nothing in the
	// expression walk otherwise carries. See barecall.go's
	// ifaceSiblingRequirementCall.
	//
	// It is the RECEIVER being emitted rather than a map of calls inside
	// default bodies: a map of calls could say which default body a call sat
	// in but not which receiver was being emitted, and would miss a bare
	// requirement call in an impl's own function, which is what
	// testdata/iface_default_sibling.nomi exercises.
	//
	// DISTINCT FROM `implSelf` above, which is a receiver NAME set only while a
	// stdlib module is lowered. Two fields rather than one because they answer
	// different questions over disjoint populations, and merging them would
	// make a stdlib impl's receiver look like a user impl block.
	implEmitting *implDef
	// aliasOrder is one Go type alias per MIRROR this package built and
	// aliasPkgs the sibling packages those aliases name. Tracked apart from
	// usedFiles because an alias is emitted unconditionally — it is what makes
	// its own import used — while usedFiles is suppressed while probing. See
	// foreign.go.
	aliasOrder []foreignAlias
	aliasPkgs  map[string]bool
	// foreignIfaces interns this package's MIRROR of a sibling file's
	// interface, keyed by the declaration node — which IS the interface's
	// identity, so two names for one declaration reach one mirror. impls is
	// the program's whole impl index, which is the scope a boxing site has to
	// ask about. census records what was boxed and which way each
	// interface-qualified call went; it is a diagnostic and changes nothing
	// that is emitted. See existential.go.
	foreignIfaces map[ast.Node]*ifaceDef
	impls         *implIndex
	census        dispatchCensus
	// iter is std/iter's `Iter` interface as this module's analysis resolved
	// it, shape-checked against the push protocol rt implements, or nil. The
	// declaration node IS the identity, exactly as it is for a prelude enum.
	// iterLoaded distinguishes "not resolved yet" from "resolved to nothing",
	// so a module without the protocol asks once. See iter.go.
	iter       *iterAnchor
	iterLoaded bool
	// ctrl is the innermost inlined `Iter.loop` being emitted, or nil. It is
	// what makes a `break` non-local to the nearest LOWERED loop and nothing
	// else: saved and CLEARED by lambda() and funcDecl(), exactly as
	// pendingTry/result/inferResult are, so a `break` inside a nested lambda
	// finds no boundary and keeps its refusal. See ctrlflow.go.
	ctrl *ctrlBoundary
	// hostPkgs is this module's `gopkg` declarations by NOMI alias, and
	// hostTypes its Go-bound `host type` declarations by Nomi name. Both nil
	// for a module with no FFI, which is nearly all of them — a nil map reads
	// as the empty one, so no `loaded` flag is needed beside either. The
	// *typeDef values are process-wide, because a `host fn`'s kinds are
	// compared by pointer from another gen. See hostpkg.go.
	hostPkgs  map[string]*hostPkg
	hostTypes map[string]*typeDef
}

// newGen builds one module's generator and declares nothing.
//
// Construction is split from declaration because a PROGRAM declares in three
// waves rather than one file at a time: every file's types, then every file's
// cross-file signatures, then every file's own signatures and impls. A
// signature in file B may name a type declared in file A and Nomi has no
// forward-declaration rule in either direction, so no per-file order can
// satisfy both. See Generate and foreign.go.
func newGen(m *Module, pkg string, std *stdlibIndex, files *fileIndex, unit int, reg *typeRegistry) *gen {
	g := &gen{nomiPath: m.Path, pkg: pkg, fa: m.FA, funcs: map[string]*fnSig{}, onces: map[string]*onceDef{}, oncesByDecl: map[ast.Node]*onceDef{}, types: map[string]*typeDef{}, blockTypes: map[*ast.Block]*blockTypeDecls{}, ifaces: map[string]*ifaceDef{}, implsByIface: map[string]map[kind]*implDef{}, tids: map[kind]bool{}, anonTids: map[kind]bool{}, std: std, files: files, fileUnit: unit, reg: reg, foreignDefs: map[ast.Node]*typeDef{}, foreignIfaces: map[ast.Node]*ifaceDef{}, nodes: m.Nodes}
	g.declaresTests = m.DeclaresTests
	g.pushScope()
	return g
}

// declareTypes builds the module's interface and type tables.
//
// Re-entrant through typeRegistry.ensureTypes: resolving a field whose type
// lives in another file builds that file's table first. Guarded there, not
// here.
func (g *gen) declareTypes() {
	if g.typesDone {
		return
	}
	// FFI handles FIRST, ahead of the interface shells: a `gopkg`-bound
	// `host type` is a named type a struct field or an interface function's
	// parameter may hold, and it resolves through no table any later wave
	// builds. It reads only `*ast.ExternPackage` and `*ast.ExternType` nodes,
	// so a module with no FFI pays one walk over its own declarations and
	// records nothing. See hostpkg.go.
	g.collectHostPkgs()

	// Interface SHELLS before types: a struct field or an enum payload may be
	// interface-typed — an existential — and typeOf can only answer that once
	// the name resolves to an *ifaceDef.
	g.declareIfaces(g.nodes)

	// Types before signatures: a signature may name a declared type, and Nomi
	// has no forward-declaration rule in either direction.
	g.buildTypes(g.nodes)

	// Interface SIGNATURES after types, for the mirror-image reason: an
	// interface function's parameter may name a declared struct.
	g.resolveIfaces()

	// Runtime identities last, once lowerability is settled. Eager, because a
	// sibling file's erasure of one of these types is discovered while THAT
	// file's body is emitted — possibly after this package has been rendered.
	// See existential.go.
	g.mintTypeIDs()
	g.typesDone = true
}

// declareFuncs builds the module's function signatures and impl tables, which
// may name any type any file declares.
func (g *gen) declareFuncs() {
	// Signatures are collected before any body is emitted: Nomi has no
	// forward-declaration rule, so a call on line 3 to a function declared on
	// line 30 is legal and must resolve.
	decls := map[string]*ast.FuncDef{}
	for _, n := range g.nodes {
		fd, ok := n.(*ast.FuncDef)
		if !ok || fd.ImplFunction {
			continue
		}
		decls[fd.Name] = fd
		g.funcs[fd.Name] = g.signature(fd)
	}
	// A Go-bound `host fn`, on the same footing as a `fn` and for the reason
	// that matters: a call site resolves it through `g.funcs` like any other,
	// so directCall, the named-argument walk and the arity check all run
	// unchanged against a foreign callee. Deliberately NOT added to `decls`,
	// which is buildTailGraph's input — a `host fn` has no Nomi body and
	// cannot tail-recur. See hostpkg.go.
	for _, n := range g.nodes {
		ef, isExtern := n.(*ast.ExternFunc)
		if !isExtern || !(hostBoundFn(ef) || (g.stdModule == "" && hostTableFn(ef))) {
			continue
		}
		g.funcs[ef.Name] = g.hostSignature(ef)
	}

	// `once` bindings alongside signatures, for the same reason: a function
	// declared above a `once` may read it, and so may another `once`. See
	// once.go.
	g.declareOnces()

	// Impls next: an impl item's signature may name any declared type, and a
	// synthesized block's body — which buildImpls lowers speculatively — may
	// call any module function. Both have to be resolvable first.
	g.buildImpls(g.nodes)

	// The tail-call graph LAST, because an impl block's items are half its
	// nodes and buildImpls is what creates them. Built before buildImpls, or
	// keyed on `decls` alone, it would miss an impl function's tail self-call.
	// See tail.go.
	g.buildTailGraph(decls)

	// A BOUND-FREE generic function on a tail cycle is refused, and this is the
	// earliest the graph can say so. decideTailPlan settles a SINGLE-member
	// component `ok` without consulting tailMemberSig — the clause that
	// excludes a generic `fn` — and for a MULTI-member cycle it sets `why` to
	// "" on the stated ground that every member is refused at its own
	// declaration, which does not hold for a lowerable generic. See generic.go.
	g.refuseGenericTailCycles()
	g.funcsDone = true
}

func (g *gen) emitModule(m *Module) ([]Unsupported, []Suppression) {
	// Collected before any body is emitted, because the duplicate-name
	// question has to be answered before a table exists and answering it needs
	// every case. Emitted from the walk below, so the table's order is the
	// declaration order the report uses. See tests.go.
	testCases := collectTestCases(m.Nodes, g.declaresTests)
	g.refuseDuplicateTestNames(testCases)
	byOwner := testCasesByOwner(testCases)
	// The Go types for declarations written inside a body, LIFTED to package
	// level. Ahead of the walk so a function body's emitted text can name one,
	// and in one group rather than interleaved because a block-local
	// declaration has no module-level position to be interleaved AT. See
	// blocklocaltype.go.
	for _, n := range m.Nodes {
		// A `//!` prompt case is collected AT its declaration — before the
		// declaration itself and after everything above it, which is the
		// order `nomi test` reports (collectAttachedTestCases runs before the
		// descent). AHEAD of every `continue` below, because an impl block and
		// a synthesized node can carry prompts too and a case dropped here is
		// a case the reference RUNS. A node never owns both a prompt case and
		// a `test` declaration's cases, so this and the TestDecl arm cannot
		// interleave for one node.
		g.attachedTestDecls(byOwner[n])
		if ib, isImpl := n.(*ast.ImplBlock); isImpl {
			// Includes the universal-default `impl Debug for T` the front end
			// appends for every declared type; implDecl drops a synthesized
			// block rather than blaming a fabricated position for it.
			g.implDecl(ib)
			// Owner-level `once` bindings lower on their own, whatever
			// happened to the block's functions: a `once` takes no receiver
			// and no type parameter, so a generic block's template status
			// does not reach it. declareOnces filed them.
			for _, item := range ib.Items {
				if ob, isOnce := item.(*ast.OnceBinding); isOnce && g.oncesByDecl[ob] != nil {
					g.onceDecl(ob)
				}
			}
			continue
		}
		switch n.(type) {
		case *ast.ImportStmt, *ast.ImportBlock:
			// Ahead of the synthesized check, because a SIBLING file's
			// imports are largely synthesized: the analyzer auto-prepends the
			// prelude into every project-loaded file, and the entry — whose
			// nodes the caller owns — is the one file that does not get them.
			// Refusing those cost every sibling file 60 `ImportStmt`
			// refusals plus a `type reference` per prelude name the probe
			// then descended into, which is 133 blockers for two functions.
			//
			// Safe for the same reason a written import is: imports are
			// resolved by the front end and a lowered module runs none.
			// Their being fabricated changes nothing, because nothing is
			// built from them and no position is blamed.
			continue
		}
		if isSynthesized(n) {
			// A synthesized node of any OTHER kind is still refused by name,
			// so a front-end pass that starts emitting one cannot go
			// unnoticed here.
			g.rejectWhole(constructName(n), "", n)
			continue
		}
		switch t := n.(type) {
		case *ast.FuncDef:
			g.funcDecl(t)
		case *ast.StructDef, *ast.EnumDef, *ast.TypeDef:
			g.typeDecl(n)
		case *ast.InterfaceDef:
			g.ifaceDecl(t)
		case *ast.ImplConformance:
			// `derive Equatable for Dog`. SynthesizeDerives has already turned
			// it into an ordinary impl block, lowered above through exactly
			// the path a hand-written impl takes — so the declaration itself
			// has nothing left to lower and refusing it would report a gap
			// that does not exist.
		case *ast.TypeAlias:
			// `typealias Handler (String, String) -> Result<String, String>`.
			// TRANSPARENT: it introduces no type, so there is nothing to emit
			// and nothing to declare — an annotation naming it resolves to the
			// TARGET's kind through typeOf. Its own arm rather than a silent
			// skip, because the DECLARATION is the position a programmer can
			// act on when the target is unrepresentable, exactly as
			// blocklocaltype.go's blockAliasDecl says for the block-local form.
			// See typealias.go.
			g.moduleAliasDecl(t)
		case *ast.TestDecl:
			g.testDecl(byOwner[n])
		case *ast.OnceBinding:
			g.onceDecl(t)
		case *ast.ExternPackage:
			// `gopkg "example.com/app/ffi" as ffi`. Emits nothing: the Go
			// import it authorizes is written by header(), from the text that
			// actually spells the alias. See hostpkg.go.
			g.hostPkgDecl(t)
		case *ast.ExternFunc:
			g.hostFnDecl(t)
		case *ast.ExternType:
			g.hostTypeDecl(t)
		default:
			g.rejectWhole(constructName(n), "", n)
		}
	}
	// The entry's `main failure` renderer, before the flushes below, since
	// rendering a generic error type can ask for an instance.
	// See mainfailure.go.
	g.irBuildMainFailure()

	// Monomorphized generic function instances, BEFORE every flush below.
	// Lowering an instance body is ordinary body lowering, so it can intern a
	// new generic STRUCT instance (`Half<Int>`). See genericmono.go.
	g.flushMonoInstances()

	// The functions and dispatch bindings for every IMPL registered against a
	// generic instance, and the method instances. See genericimpl.go.
	g.flushGenericImpls()
	g.flushMethodInsts()
	// The field-default accessors another file's literals asked for. See
	// foreignfielddefault.go.
	g.flushFieldDefaults()
	return g.errs, g.masked
}

// --- positions -------------------------------------------------------------

// emittableLine reports whether a Nomi line is a real source position the
// builder may attribute a node or a decline to. It is the one gate every
// recorded position passes through.
//
// The known source of a line that is not one is derive synthesis, which
// allocates from a band around 2^30. The class is wider (a node that never
// got a position, a pass that invents one), so this gates the VALUE rather
// than any one origin. Skipping a position keeps the previous attribution,
// which is stale rather than invalid.
func emittableLine(line int) bool {
	// The bottom of the synthesized band. Anything at or above it is not a
	// line anybody wrote.
	const maxGoLine = 1 << 30
	return line > 0 && line < maxGoLine && !analysis.IsSynthesizedLine(line)
}

// at sets the Nomi line subsequent statements are attributed to.
//
// A SYNTHESIZED position is ignored rather than recorded. Derive synthesis
// allocates positions from a band around 2^30, which name no line the
// programmer wrote. Ignoring it leaves the attribution wherever
// the caller last set it, and implFunc sets that to the RECEIVER type's own
// declaration for a synthesized impl, which is the position the programmer
// actually wrote the `derive` at.
func (g *gen) at(line int) {
	if emittableLine(line) {
		g.nomiLine = line
	}
}

// --- refusals --------------------------------------------------------------

// reject records one construct the builder cannot lower, and keeps lowering.
func (g *gen) reject(construct, detail string, n ast.Node) {
	line, col := nodePos(n)
	g.errs = append(g.errs, Unsupported{
		Construct: construct,
		Detail:    detail,
		File:      g.nomiPath,
		Line:      line,
		Col:       col,
	})
}

// rejectWhole refuses a construct. The body under it is not walked for further
// blockers: a refused declaration's body has no Go either way.
func (g *gen) rejectWhole(construct, detail string, n ast.Node) {
	g.reject(construct, detail, n)
}

// --- scopes ----------------------------------------------------------------

func (g *gen) pushScope()            { g.scopes = append(g.scopes, map[string]local{}) }
func (g *gen) popScope()             { g.scopes = g.scopes[:len(g.scopes)-1] }
func (g *gen) top() map[string]local { return g.scopes[len(g.scopes)-1] }

func (g *gen) bind(name string, l local) { g.top()[name] = l }

func (g *gen) lookup(name string) (local, bool) {
	for i := len(g.scopes) - 1; i >= 0; i-- {
		if l, ok := g.scopes[i][name]; ok {
			return l, true
		}
	}
	return local{}, false
}

// --- declarations ----------------------------------------------------------

// signature resolves a function's declared types without emitting anything, so
// a call can be checked against a callee declared later in the file.
func (g *gen) signature(fd *ast.FuncDef) *fnSig {
	// A BOUNDED generic function has its own signature rule: each type
	// parameter is an existential over its bound and each contributes a
	// leading `*rt.TypeID`. dictSignature declines everything outside that,
	// which keeps the clause below in charge of it. See dict.go.
	if sig, claimed := g.dictSignature(fd); claimed {
		return sig
	}
	sig := &fnSig{decl: fd, lowerable: true}
	// A BOUND-FREE generic function has its own rule too, and generic.go owns
	// the whole bound-vs-bound-free decision: one whose every type-parameter
	// mention is bare gets its type parameters as kinds and stays lowerable,
	// and everything else records the construct funcDecl will report. Asked
	// beside dictSignature rather than after the walk below, because a type
	// parameter is not a type paramKind can resolve — `T` would answer
	// kindInvalid and the walk would clear lowerability for the wrong reason.
	if params, result, tps, why := g.genericSignature(fd); tps != nil || why != "" {
		sig.params, sig.result, sig.tps, sig.genericWhy = params, result, tps, why
		sig.lowerable = why == ""
		// Erasure declined. The DEPTH residue — a type parameter mentioned
		// inside `List<T>`, `Half<T>`, `(T, Int)` — is monomorphizable per call
		// site instead, and monoTemplateFor re-derives its OWN preconditions
		// rather than reading `why`: that string is one spelling for eight
		// different reasons, so keying off it would make a decorator on a
		// generic `fn` silently monomorphizable. See genericmono.go.
		if !sig.lowerable {
			sig.mono = g.monoTemplateFor(fd)
		}
		return sig
	}
	for _, p := range fd.Params {
		k := g.paramKind(p)
		// kindInvalid: reports — records lowerable=false; funcDecl rejects `non-scalar parameter type`.
		if k == kindInvalid {
			sig.lowerable = false
		}
		sig.params = append(sig.params, k)
	}
	if len(fd.Decorators) > 0 || fd.Body == nil {
		sig.lowerable = false
	}
	// A `fn` with no declared return type returns Unit. A body's last
	// expression produces its value regardless of the annotation, so a body that ends in a value under a missing annotation is
	// refused by funcDecl rather than silently retyped.
	sig.result = kindUnit
	if fd.ReturnTypeExpr != nil {
		sig.result = g.typeOf(fd.ReturnTypeExpr)
		// kindInvalid: reports — records lowerable=false; funcDecl rejects `non-scalar return type`.
		if sig.result == kindInvalid {
			sig.lowerable = false
		}
	}
	return sig
}

func (g *gen) funcDecl(fd *ast.FuncDef) {
	g.at(fd.Line)
	// A nested `fn` is its own boundary for the same reason a lambda is, and
	// funcDecl is reached from probe() while a body is being walked. Without
	// this a `break` inside a refused nested `fn` would find the enclosing
	// loop's boundary and go unreported — a masked blocker, not a wrong answer,
	// and the tally must count every blocker. See ctrlflow.go.
	prevCtrl, prevTail := g.ctrl, g.tail
	g.ctrl, g.tail = nil, nil
	defer func() { g.ctrl, g.tail = prevCtrl, prevTail }()
	// Guarded on the declaration POINTER, not just on nil: funcDecl also sees
	// impl functions, and g.funcs is keyed on a bare name that an impl method
	// may share with a free `fn`. Without this an impl `foo` would be reported
	// under the reason a free generic `foo` recorded.
	//
	// `g.monoSig` overrides both, and only while emitMonoInstance is emitting
	// one instantiation of a generic template. It is an override rather than a
	// swap of `g.funcs[fd.Name]` on purpose: a RECURSIVE call inside the
	// instance body still resolves through `g.funcs`, finds the template there,
	// and is interned at its own argument tuple — where swapping the table
	// would have made every recursive call reuse this instance whatever its
	// arguments. See genericmono.go.
	sig := g.monoSig
	if sig == nil {
		sig = g.funcs[fd.Name]
		if sig == nil || sig.decl != fd {
			sig = g.signature(fd)
		}
	}
	// A PROGRAM'S `fn boot` is not an ordinary function: the runtime calls it
	// once before the entry function and publishes its result as the active app
	// value, so it lowers to a staging function the generated `main` calls
	// rather than to a Go function anything could call. AHEAD of every clause
	// below because those clauses refuse a SIGNATURE, and this declaration's
	// signature is the one thing about it that is never lowered — `fn boot():
	// App` names a stdlib interface with no method table, and stageProgramApp's
	// comment gives the whole reason that annotation does not have to be
	// represented. See appfield.go.
	if g.isProgramBoot(fd) {
		g.stageProgramApp(fd)
		return
	}
	switch {
	// A MONOMORPHIZATION TEMPLATE is not a function, it is a rule for making
	// them, so there is nothing to emit here and nothing to refuse: a call
	// resolves it to a concrete instance and a call the builder cannot
	// instantiate refuses at the CALL, which is where the type argument is
	// known. That is cascade.go's discipline — refuse where the answer is —
	// and it is why the declaration itself is not refused. A template nobody
	// calls builds nothing. Ahead of the generic clause because `genericWhy` is
	// still recorded on it.
	case sig.mono != nil:
		return
	// The generic clause is `sig.dict == nil && sig.genericWhy != ""`: two
	// arms of one decision, both settled in the SIGNATURE and reported here
	// rather than re-derived. dict.go claims a bounded generic it can lower
	// and generic.go a bound-free one; whichever declines records the
	// construct, so this clause names it without knowing which arm answered.
	// `recursive tail call` arrives the same way, from
	// refuseGenericTailCycles.
	case sig.dict == nil && sig.genericWhy != "":
		g.rejectWhole(sig.genericWhy, fd.Name, fd)
		return
	case len(fd.Decorators) > 0:
		g.rejectWhole("decorator", fd.Name, fd)
		return
	case fd.ImplFunction:
		g.rejectWhole("impl function", fd.Name, fd)
		return
	case fd.Body == nil:
		g.reject("function without a body", fd.Name, fd)
		return
	case fd.Name == "main" && len(fd.Params) > 0:
		g.rejectWhole("main with parameters", "", fd)
		return
	}
	// The seam is installed for everything below, INCLUDING the probe path a
	// refused parameter takes: `T` has to resolve while a body is walked for
	// reporting, or a refused generic would report every mention of its own
	// type parameter as a second gap. See dict.go and cascade.go.
	prevDict := g.dict
	g.dict = sig.dict
	defer func() { g.dict = prevDict }()

	// Parameter and return types are refused individually so the blocker set
	// names every one, not just the first — and each is refused under the
	// TYPE's own reason rather than under the position's shape. A
	// position-shaped key such as `non-scalar parameter type` would name no
	// work anybody could do: every type the builder can represent is already
	// admitted here, so such a row is always another type's obstacle seen from
	// a signature. See sigreason.go.
	ok := true
	for i, p := range fd.Params {
		switch {
		case p.Destructure != nil:
			// One value, several names. patternParamKind names the reason when
			// the pattern's own type cannot be established; the names it
			// introduces are emitted inside the body below.
			if _, resolved := g.patternParamKind(p, fd); !resolved {
				ok = false
			}
		case p.TypeAnnotation == nil:
			g.reject("parameter without a declared type", p.Name, fd)
			ok = false
		// kindInvalid: reports — rejectTypeAnnotation names the type's own gap.
		case sig.params[i] == kindInvalid:
			g.rejectTypeAnnotation(p.TypeAnnotation, p.Name, fd)
			ok = false
		}
		// p.Default is deliberately NOT checked here. A default is evaluated at
		// the CALL, in the callee's own scope (see sugar.go), so a call that
		// supplies the argument never evaluates it and a default nothing omits
		// costs nothing.
		// Its blockers are reported at the call site that needs it, which is
		// also the position a reader would look for them.
	}
	// kindInvalid: reports — rejectTypeAnnotation names the type's own gap.
	if fd.ReturnTypeExpr != nil && sig.result == kindInvalid {
		g.rejectTypeAnnotation(fd.ReturnTypeExpr, "", fd)
		ok = false
	}
	if !ok {
		// The body is probed inside a scope holding the names any destructuring
		// parameter declared, bound at an invalid kind: the names are in scope
		// whether or not this builder resolved the pattern, so
		// leaving them out reports one parameter's gap once per mention in the
		// body. See cascade.go.
		g.pushScope()
		for _, p := range fd.Params {
			if p.Destructure != nil {
				g.bindPatternNamesInvalid(p.Destructure)
			}
		}
		g.popScope()
		return
	}

	g.pushScope()
	// A destructuring parameter's names come out of the parameter's value in
	// the function's prologue; a plain parameter's name is bound here.
	type destructuring struct {
		pat ast.Node
		at  int
	}
	var patterns []destructuring
	for i, p := range fd.Params {
		if p.Destructure != nil {
			// The synthesized `__destr_<line>_<col>` name the parser gave this
			// parameter is not spellable in Nomi, so nothing can read it and
			// nothing binds it — only the pattern's own names reach the body.
			patterns = append(patterns, destructuring{p.Destructure, i})
			continue
		}
		if !ast.IsDiscardName(p.Name) {
			g.bind(p.Name, local{k: sig.params[i]})
		}
	}

	prev := g.result
	g.result = sig.result
	// The function's result slot and its parameters belong to the retained
	// `ir.Func` this shell opens. See irslot.go.
	shell := g.irFuncShellWithCallee(fd, irFuncSigOf(fd, sig), sig.params, g.irFunctionCallee(sig))
	// The names a destructuring parameter introduces are read out of the
	// parameter's value in the function's prologue, before the body, so they
	// are in scope for it. See irparam.go.
	destructured := true
	if len(patterns) > 0 {
		closePrologue := g.irPrologueOpen(shell)
		for _, d := range patterns {
			if !g.destructure(d.pat, shell.paramValue(d.at, sig.params[d.at]), fd) {
				destructured = false
				g.bindPatternNamesInvalid(d.pat)
			}
		}
		closePrologue(destructured)
	}
	// The body's Go is read from its graph. A body the builder declines has
	// no Go: the program is refused wherever it is reached (irnative.go).
	g.irBodyObserve(irBodyModuleFn, true)
	produced, retained := g.irScalarLower(fd, irFuncSigOf(fd, sig), nil, shell, sig.result)
	if !retained {
		g.unloweredBody(irDeclined{})
		produced = sig.result
	}
	if !destructured {
		produced = kindInvalid
	}
	switch {
	case produced == kindInvalid:
		// Declines `return type mismatch` / `function without a declared
		// return type`: the body produced no kind to compare the declaration
		// against, so the check cannot run and cannot report.
		g.suppress(fd)
	case produced != sig.result:
		if fd.ReturnTypeExpr == nil {
			g.reject("function without a declared return type",
				fd.Name+" returns "+produced.nomi(), fd)
		} else {
			g.reject("return type mismatch",
				fd.Name+" declares "+sig.result.nomi()+" and returns "+produced.nomi(), fd)
		}
	}
	g.result = prev
	g.popScope()
	// Another file's call that omits a defaulted parameter calls this file's
	// accessor for it (siblingdefault.go).
	g.irFileSlotDefaults(fd, sig)
}

// --- statements ------------------------------------------------------------

// --- expressions -----------------------------------------------------------

// --- calls -----------------------------------------------------------------

// printKey, writeKey and inspectKey are std/io's output primitives, which
// stdFreeCall answers directly rather than through the stdlib index.
// `io.write` is `io.print` without the trailing newline: the same Display
// rendering, a different host.
//
// Static names rather than a query against a host table. Each becomes a direct
// rt call (no dispatch, no table, no boxing), and every other module-qualified
// call is refused by name.
//
// They are a pair in the declaration and a pair here. The tour reaches
// `io.inspect` early (modules-and-imports.md's first runnable example and
// bindings-and-expressions.md), while the corpus mostly reaches the same
// rendering through `Debug.inspect(x)` and `dbg`.
//
// `io.inspect` is NOT a second Debug renderer. It is a third CALLER of
// debugRendering, which is inspectcall.go's single decision procedure — the
// same one `Debug.inspect(x)` and `dbg` call. Reaching for the nearest
// renderer instead is a silent wrong string on four shapes (struct
// field order, a hand-written `impl Debug`, an opaque type, a function); see
// that file's header and debugdisplay_test.go's mutation note.
const (
	printKey   = "io.print"
	writeKey   = "io.write"
	inspectKey = "io.inspect"
)

// isOutputKey reports whether key is one of std/io's output primitives.
func isOutputKey(key string) bool {
	return key == printKey || key == writeKey || key == inspectKey
}
