package irbuild

import (
	"fmt"
	"slices"
	"strings"

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
// A Nomi runtime fault (a zero divisor, a failed host function) is an rt trap, which
// the VM reports as a fault. See rt/trap.go.

// kind is a Nomi type as this builder represents it.
//
// The scalar rows are Int, Float, String, Unit and Bool. `Bool` is not a
// primitive in the language: it is `pub enum Bool { embeds False; embeds True }`
// (std/bool.nomi). The builder gives it a row of its own (tagBool) rather
// than a named enum kind.
//
// The named row is a struct or enum declared in the module, and it is what
// makes `kind` a struct rather than an integer. A named kind carries the
// `*typeDef` its declaration produced, so kind equality is Go pointer equality
// on the declaration: two same-named structs from two modules are two
// pointers and cannot be confused. The opposite choice, identity as a string
// assembled at construction sites and re-parsed at lookup sites, fails
// silently. See types.go.
type kind struct {
	tag tag
	// def is the declaration a named type's identity IS; nil for every scalar.
	def *typeDef
	// comp is the interned identity of a structural type: one with no
	// declaration to point at, and whose tag covers more than one type.
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
	// unboundedly many types. See collections.go.
	tagList
	tagTuple
	// tagAnonStruct is `{x: Int, y: String}` — a RECORD, structural like the
	// tuple beside it and canonicalized by field name rather than by position.
	// Its identity is the interned *compKind, which additionally carries the
	// field NAMES: a tuple's components are addressed by index and a record's
	// by name, so the names are part of what the interned entry has to answer.
	// See anonstruct.go.
	tagAnonStruct
	// tagMap is `Map<K, V>`. Structural, like tagList: the identity is the
	// interned *compKind. See maps.go.
	tagMap
	// tagSeq is a lowered `Iter<T>`: the push protocol's `Seq<T>`. Structural,
	// like tagList: the identity is the interned *compKind.
	//
	// Not tagIface, even though `Iter<T>` is an interface-typed position. The
	// push protocol has no `Self` anywhere in its signature, so an `Iter<T>`
	// is exhausted by its `each_while`: nothing about the concrete source is
	// observable through it, and a closure is that function. See iter.go and
	// rt/seq.go.
	tagSeq
	// tagTypeParam is a bound-free type parameter, in the signature and body
	// of the generic function that declares it (genericSignature). Its IR
	// type is `ir.AnyType` and its identity is the *typeParamDef, which keeps
	// two functions' `T` apart. A call lowers an instance of the function's
	// template, whose signature holds none. See generic.go.
	tagTypeParam
	// tagNever is `Infallible`, the type with no values. See kindNever.
	tagNever
)

var (
	kindInvalid = kind{}
	kindUnit    = kind{tag: tagUnit}
	kindInt     = kind{tag: tagInt}
	kindFloat   = kind{tag: tagFloat}
	kindString  = kind{tag: tagString}
	kindBool    = kind{tag: tagBool}
	// kindNever is `Infallible`: the checker's type for an expression that
	// never produces a value (`todo`, a call to a function declared to return
	// `Infallible`) and the type argument that makes a variant unreachable
	// (`Result<Int, Infallible>`). Its IR type is `ir.NeverType`, which
	// stores nothing and which every type accepts, so a value of it may stand
	// wherever any value goes: no execution ever reads one. irnever.go has
	// the rules.
	kindNever = kind{tag: tagNever}
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
// Each structural tag answers for itself: a function value always stores
// something, while a `(Unit, Unit)` stores nothing.
func (k kind) zeroSized() bool {
	return k.zeroSizedOn(nil)
}

// zeroSizedOn is zeroSized inside a walk already standing on path. See
// types.go's typePath for why the guard is a parameter and not a field.
func (k kind) zeroSizedOn(path typePath) bool {
	switch k.tag {
	case tagUnit:
		return true
	case tagNever:
		// No value of it exists to store, but a payload of it keeps its
		// slot: the variant is declared with the field, and an Infallible
		// field is a field no construction fills.
		return false
	case tagNamed:
		return k.def.zeroSizedOn(path)
	case tagFunc:
		return false
	case tagList:
		return false
	case tagMap:
		return false
	case tagSeq:
		return false
	case tagTuple, tagAnonStruct:
		// A tuple or record of nothing but zero-sized parts stores nothing.
		// Field names cost no storage, so the record answers by the same
		// walk.
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
	case tagNever:
		return "Infallible"
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
	}
	return "an unsupported type"
}

// fnSig is a module function as its call sites see it.
type fnSig struct {
	params []kind
	result kind
	// decl is the declaration itself, read for the parameter shapes a call
	// site has to reproduce: a DEFAULT is evaluated at the call rather than
	// stored, so its expression has to be reachable from there (see
	// irdefaults.go), and a DESTRUCTURING parameter's pattern is what names the
	// locals its body reads.
	decl *ast.FuncDef
	// lowerable is false for a function whose own declaration was refused. A
	// call to one is refused too.
	lowerable bool
	// dict is the type-parameter dispatch seam for a bounded generic function
	// dictSignature claimed, nil for everything else. funcDecl installs it
	// while it reports on the declaration and does not lower the
	// declaration's body; a call lowers an instance (irMonoTemplate). See
	// dict.go.
	dict *dictSeam
	// tps are the type parameters of a bound-free generic function
	// genericSignature claimed, in declaration order; nil for every
	// monomorphic one and for every bounded one, which uses `dict` instead.
	// Non-nil means `params` and `result` hold tagTypeParam kinds. See
	// generic.go.
	tps []*typeParamDef
	// genericWhy is the construct funcDecl reports for a generic declaration
	// the builder does not lower, or "" when there is nothing to report.
	// Recorded here rather than re-derived so signature() and funcDecl cannot
	// disagree about which of them decided.
	genericWhy string
	// mono is the monomorphization template this declaration is, or nil.
	//
	// Non-nil means the declaration is not a function, it is a rule for
	// making them, so funcDecl lowers nothing and refuses nothing for it, and
	// a call resolves to a concrete instance instead. Set where
	// genericSignature declined the declaration and monoTemplateFor claims
	// it; one neither claims keeps its funcDecl refusal. See genericmono.go.
	mono *monoTemplate
	// irMono is the template irMonoTemplate derived for a generic that
	// genericSignature or dictSignature claimed (tps or dict set), nil when
	// it has none; irMonoAsked says it was asked.
	irMono      *monoTemplate
	irMonoAsked bool
	// instance identifies the concrete function produced from a mono template.
	// Its source declaration is shared by every specialization.
	instance *monoInst
	// defaultFrame binds, for a call filling a generic callee's parameter
	// defaults, the callee's type parameters (an interface function's and
	// its interface's) at the type arguments the checker instantiated the
	// call at (ifaceDefaultFrameAt, callFrameAt); nil when the call fills
	// none or an instance of the callee binds them (instance).
	// defaultTypes is the same binding as the checker's types, nil where
	// it has none.
	defaultFrame map[string]kind
	defaultTypes map[string]analysis.Type
	// defaultSelfType is, for a call filling an interface function's
	// defaults, the checker's type of the implementor its `self` is: the
	// type the call's record is for (analysis.Callee.For), or for an
	// accessor, the implementing block's (implDef.recvType). Nil where
	// neither is recorded.
	defaultSelfType analysis.Type
}

type gen struct {
	// dc is the decline census of the lowering this gen builds in, which
	// every gen of that lowering shares and no other lowering does.
	dc       *declineCensus
	nomiPath string
	pkg      string
	// errorDecls is the program's declarations whose check reported an
	// error (Program.ErrorDecls), which errorDeclBody leaves unlowered.
	errorDecls map[ast.Node]bool
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
	// linked is set on the gen of a listed std module whose bodies the
	// program links from a kept lowering rather than lowering them
	// (programstdlink.go): the gen declares the module and lowers only
	// what the program asks of it that the kept lowering lacks.
	linked bool
	// fromObjects marks a project file's gen in a lowering of one other
	// file (perfile.go): the file is declared, its bodies are the kept
	// objects', and nothing of it is lowered here, not even what the
	// lowered file asks of it, since its nodes are its interface's.
	fromObjects bool
	// progLinks is, on a std instance's gen, the declaration names of the
	// program that asked for the instance (link.go).
	progLinks map[ast.Node]declName
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
	// stdUnitSyms is, while the shared lowering lowers a std import cycle
	// as one unit (lowerStdUnit), the symbol each declaration of the
	// unit's modules has, by linkage name, so a module lowered again in a
	// later pass defines the symbols the other members already call.
	stdUnitSyms map[string]*ir.Symbol
	// programStd is set when this gen lowers a std module listed in
	// programStdModules with the program (programstd.go): its positions are
	// stdlib positions, as a shared std module's are, so a trap inside it is
	// reported at the program's call (ownPos).
	programStd bool
	// stdTypesBuild is set on the gen that builds a std module's types for
	// the process (stdtypes.go), which lowers no body.
	stdTypesBuild bool
	// stdCases is set on the gen that lowers a shared std module's own
	// `//!` and `test` cases (stdtests.go): those are a program, which holds
	// std interfaces as values through its own defs (stdProgBuilds), though
	// the module's declarations are the shared lowering's.
	stdCases bool
	// skipLint is set on the gen lowering one module of the embedded stdlib
	// when lintEmbeddedStdlib is off; irLint and irLintAdded then do nothing.
	skipLint bool
	// fa is the module's own analysis, or nil. Read for exactly one thing:
	// the type the checker SOLVED for a construct the AST does not annotate.
	// See inferred.go — a solved type becomes a kind through project, so there
	// is one place that decides what the analyzer is allowed to answer for.
	fa *analysis.FileAnalysis
	// namesFrom is the unit, plus one, of the file whose names what is
	// being lowered was written under (0 outside): another file's
	// parameter default filled here (ifaceSlotDefault, fillCallDefaults),
	// or an interface signature read here. A bare name that file binds to
	// a `once` reads that file's cell (onceValue). See lowerNamesFrom.
	namesFrom int

	// nomiLine is the Nomi line the next lowered statement is attributed to.
	nomiLine int

	tmp   int
	funcs map[string]*fnSig
	// sigByDecl is the same signatures under the declaration each is of
	// (declareFunc): what a call the checker resolved to a declaration
	// looks its callee up by.
	sigByDecl map[ast.Node]*fnSig
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
	// irHostKeys are the extern keys this unit's `host fn` bodies
	// cross under. See irhostfn.go.
	irHostKeys []string
	// irTestBoots are the `group` boots this unit has offered for VM
	// retention, in the order cases first staged them.
	irTestBoots []*ir.Symbol
	// irTestRetries are the ungrouped cases whose bodies are built after
	// the program's module walk; see irRetryWalkOnlyTestBodies.
	irTestRetries []irTestRetry
	// irBoot is set while irBootRetain builds the program boot's body. That
	// body admits Context values.
	irBoot bool
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
	// onces is the module's `once` bindings by Nomi name. Typed before any
	// body is lowered, because a `once` may be read from above its own
	// declaration. See once.go.
	//
	// oncesByDecl is the same defs under their *ast.OnceBinding, which is what
	// a reference from ANOTHER file resolves to: `import lib.{config as cfg}`
	// binds `cfg` locally and the declaration is `config`, so the local
	// spelling is not a key into this table and the DECLARED spelling is not
	// available to the reference site without the node. See onceValue.
	onces       map[string]*onceDef
	oncesByDecl map[ast.Node]*onceDef
	// literalCells holds the lazy cell of each typed literal without slots this
	// unit lowered, by the literal's node, and
	// literalSites lists them in the order they were declared. See
	// irliteralcell.go.
	literalCells map[*ast.TaggedString]*ir.Symbol
	literalSites []LiteralSite
	// refinements and refinementRuleSites are the refinement literals this
	// unit's file checks and the refinements it declares (refinement.go).
	refinements         []RefinementSite
	refinementRuleSites []RefinementRuleSite
	// checkSites holds each checked literal whose check site this unit
	// declared (irchecked.go), and whether it lowered.
	checkSites map[*ast.TaggedString]bool
	// bangRenders holds the function that renders a failed `!` call's error,
	// by the error's kind (unwrap.go); nil for a kind with no rendering.
	bangRenders map[kind]*ir.Symbol
	// types is, by the name it was reached by, each struct, enum and
	// distinct type the module declares (or the process-wide def of a std
	// declaration it adopts). The *typeDef IS the type's identity
	// everywhere downstream (see types.go).
	types map[string]*typeDef
	// defsByDecl is, by its declaration node, the def of each type this
	// module declares: every def `types` holds under the declaration's own
	// name, and each block-local declaration's (blocklocaltype.go). It holds
	// no mirror. Another gen finds a declaration's def here through the
	// registry (typeRegistry.defOf).
	defsByDecl map[ast.Node]*typeDef
	// typeOrder is the declarations in source order, for the size probe.
	typeOrder []*typeDef
	// The type declarations written INSIDE a body. blockTypes is one entry per
	// *ast.Block that declares any, blockTypeOrder the same blocks in source
	// order, and blockLocalOrder every such declaration flat, which is what
	// settleLowerable relaxes over.
	//
	// Kept OUT of `types` above, which is keyed on the bare name and therefore
	// cannot hold two bodies' `Point`. typeScopes is the active overlay stack,
	// pushed and popped with the block being lowered. See blocklocaltype.go.
	blockTypes      map[*ast.Block]*blockTypeDecls
	blockTypeOrder  []*ast.Block
	blockLocalOrder []*typeDef
	typeScopes      []*blockTypeDecls
	// blockLocalByType is each non-generic block-local def by the checker's
	// type for its declaration, and blockLocalTemplates each generic one's
	// template by its declaration's first type parameter, built by
	// indexBlockLocals over the first blockLocalIndexed blocks of
	// blockTypeOrder.
	blockLocalByType    map[analysis.Type]*typeDef
	blockLocalTemplates map[*analysis.TypeParam_]*genericTemplate
	blockLocalIndexed   int
	// nestedCalls is the checker's instantiated signature at each call of a
	// generic fn, by the fn, indexed once (nestedGenericCalls in
	// irnestedfn.go).
	nestedCalls map[*ast.FuncDef][]nestedCallRef
	// nestedWithin is the innermost generic nested fn whose body holds each
	// identifier position (nestedGenericBodies), built with nestedCalls.
	nestedWithin map[analysis.Pos]*ast.FuncDef
	// The USER generic struct declarations and their monomorphized instances.
	// genericTemplates is by Nomi name, built from this module's nodes;
	// genericInsts interns one *typeDef per (template, argument tuple) and
	// genericInstOrder keeps them in creation order. genericSubst is the
	// active substitution stack, pushed while an instance's fields are
	// resolved and consulted by typeOf ahead of every other name lookup.
	//
	// Per gen and not process-wide, unlike preludeInsts' shared table: a user
	// instance's field kinds are this gen's defs, so an instance cannot be
	// shared across gens. See generictype.go.
	genericTemplates map[string]*genericTemplate
	// templatesByDecl is, by its declaration node, each template
	// genericTemplates holds. Another gen finds a declaration's template
	// here (foreignTemplateOf).
	templatesByDecl  map[ast.Node]*genericTemplate
	genericInsts     map[string][]*typeDef
	genericInstOrder []*typeDef
	// genericImplQueue is one implDef per (impl block, instance) pair, drained
	// by flushGenericImpls at the end of the module walk. A queue rather than a
	// map because an entry is built exactly once and building one can append
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
	// ifaceDefaults interns one instance per default of an interface this
	// file declares and implementing type of another file;
	// ifaceDefaultQueue holds them for flushIfaceDefaults. See
	// ifacedefault.go.
	ifaceDefaults     map[string]*ifaceDefaultInst
	ifaceDefaultQueue []*ifaceDefaultInst
	// fieldDefaults interns one accessor per field default another file's
	// literal omitted; fieldDefaultQueue holds them for flushFieldDefaults.
	// See foreignfielddefault.go.
	fieldDefaults     map[irFieldDefaultFill]*irFieldDefaultReq
	fieldDefaultQueue []*irFieldDefaultReq
	// instImplAccessors interns one request per generic instance's impl
	// function another file calls omitting a default it cannot name;
	// instImplAccessorQueue holds them for flushInstImplAccessors. See
	// instimplaccessor.go.
	instImplAccessors     map[*implItem]*instImplAccessorReq
	instImplAccessorQueue []*instImplAccessorReq
	// ifaceSlotDefaults interns one accessor per parameter default of an
	// interface function this file declares that another file's call
	// omitted; ifaceSlotQueue holds them for flushIfaceSlotDefaults. See
	// ifaceslotdefault.go.
	ifaceSlotDefaults map[irIfaceSlotDefault]*irIfaceSlotReq
	// slotDispatches holds, per impl function this gen lowered, the
	// accessor each defaulted parameter of its interface function
	// dispatches to (ifaceslotdispatch.go).
	slotDispatches map[*implItem][]irSlotDispatch
	// callDefaultsAt indexes callDefaultsFA's CallDefaults by position, for
	// a copy of a written call (callDefaultsOf).
	callDefaultsFA *analysis.FileAnalysis
	callDefaultsAt map[analysis.Pos]*analysis.DefaultedCall
	// resolvedCallsAt indexes resolvedCallsFA's Calls by position, for a
	// copy of a written call (resolvedCallOf).
	resolvedCallsFA *analysis.FileAnalysis
	resolvedCallsAt map[analysis.Pos]*ast.Call
	// carriedCalls is the record of each call the builder made that stands
	// for something the checker recorded under another node, or whose
	// arguments the builder wrote at their slots (carryCall).
	carriedCalls map[*ast.Call]*analysis.ResolvedCall
	// carriedRefs is the reference at the callee of each call the builder
	// made whose record names one (Callee.Ref), where the checker recorded
	// none (carryCall).
	carriedRefs map[ast.Node]*analysis.Symbol
	// carriedPatterns is whether each pattern the builder made in place of
	// a checked one always matches (carryPattern).
	carriedPatterns map[ast.Node]bool
	// carriedOperators is the record of each operator the builder wrote
	// for a call the checker recorded, or with a record of its own
	// (carryOperatorUse).
	carriedOperators map[ast.Node]*analysis.OperatorUse
	// carriedTypes is the checker's type of the source node each node the
	// builder made stands for (carryType), which recordedType reads.
	carriedTypes map[ast.Node]analysis.Type
	// synthDefaults is the record each call callFuncValueOf is lowering
	// fills from: the default-filled function value's, or the copied
	// function's.
	synthDefaults  map[*ast.Call]*analysis.DefaultedCall
	ifaceSlotQueue []*irIfaceSlotReq
	genericSubst   []substFrame
	// ifaceInsts is one monomorphized *ifaceDef per (impl block, receiver) of a
	// generic interface. Per gen for genericInsts' reason: an instance's method
	// kinds are this gen's kinds. The key needs its receiver half; see
	// ifaceinst.go.
	ifaceInsts map[ifaceInstKey]*ifaceInst
	// stdProgIfaces is this gen's def of each std interface it names that
	// has no rt table, and stdProgHeld each std type a value of one was
	// held at unboxed: a builtin scalar, or a std struct or enum. See
	// stdprogiface.go.
	stdProgIfaces map[*ast.InterfaceDef]*ifaceDef
	stdProgHeld   map[*ifaceDef]map[kind]bool
	// erasures is checkedErasures' answer, read once from the checker's
	// record of this unit's source.
	erasures []checkedErasure
	// coercionsAt indexes coercionsFA's Coercions by position, for a copy
	// of a written expression (coercionAt).
	coercionsFA *analysis.FileAnalysis
	coercionsAt map[irCoercionPos]*analysis.Coercion
	// iterViewsAt indexes iterViewsFA's IterViews by position, for a copy
	// of a written source (iterViewAt).
	iterViewsFA *analysis.FileAnalysis
	iterViewsAt map[irCoercionPos]*analysis.IterView
	// conversionsAt indexes conversionsFA's Conversions by position, for a
	// copy of a written expression (conversionAt).
	conversionsFA *analysis.FileAnalysis
	conversionsAt map[irCoercionPos]*analysis.Conversion
	// exprTypesAt indexes the positions of exprTypesFA's ExprTypes, for
	// the expression kind census to tell a copy of a checked expression
	// from a node the builder writes (recordedAt).
	exprTypesFA *analysis.FileAnalysis
	exprTypesAt map[irCoercionPos]bool
	// debugImplsVisiting holds the existential kinds whose implementers'
	// Debug bodies existentialDebugImpls is naming, so a box holding the
	// existential itself does not walk it again.
	debugImplsVisiting map[kind]bool
	// holeFor maps a name the builder binds in place of a written
	// expression (a pipe prefix's held value, pipeStageValues; a partial
	// capture's `&N`, openArg) to that expression, whose records it reads.
	holeFor map[ast.Node]ast.Node
	// stdProgCaller is, in a std instance gen, the program's gen whose call
	// asked for the instance. A std interface held as a value in the
	// instance's signature or body is that gen's def, and its dispatch
	// symbols are the program's (stdprogiface.go).
	stdProgCaller *gen
	// stdSrc is the analysis of the std file whose interface declaration
	// this gen is resolving, while it does (withStdSrc), and nil otherwise.
	// srcFA answers it: the declaration's nodes and positions are that
	// file's, so a position lookup here would read whatever this file wrote
	// at the same line and column.
	stdSrc *analysis.FileAnalysis
	// stdProgThunkFailed is each std interface dispatch entry whose
	// function did not build, so a later boxing site does not build it
	// again (stdProgThunk).
	stdProgThunkFailed map[stdProgThunkToken]bool
	// The generic FUNCTION templates' monomorphized instances. monoInsts
	// interns one instance per (declaration, argument tuple), monoQueue is the
	// lowering worklist, monoSig is the instance funcDecl is currently
	// lowering, and monoDepth is the length of the instantiation chain that
	// reached it — the number the instantiation guard reads.
	//
	// Per gen for genericInsts' reason: an instance's parameter kinds are
	// this gen's kinds. See genericmono.go.
	monoInsts map[string]*monoInst
	monoQueue []*monoInst
	monoSig   *fnSig
	monoDepth int
	// abstractQueue is each generic function the lowering answers for
	// every type on first use, through its abstract body (abstract.go).
	abstractQueue []abstractBody
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
	// resolved to, which IS the identity. preludeInsts interns one *typeDef per instantiation and preludeOrder keeps them in
	// creation order so unlowerability propagates deterministically.
	preludesLoaded bool
	preludeByDecl  map[*ast.EnumDef]*preludeAnchor
	preludeInsts   map[string][]*typeDef
	preludeOrder   []*typeDef
	// The generic std types' per-gen instances: `Channel<Request>`,
	// `Sender<Request>`, `Set<Point>`. Interned here rather than process-wide
	// when a type argument is not package-neutral (kind.packageNeutral,
	// stdprelude.go), which is the split preludeInsts above has and for the
	// same reason: a process-wide def may not reach a def one program's gen
	// owns.
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
	// resolved to, which IS the identity, and its value is an index into
	// opaqueSpecs; the *typeDef itself is process-wide rather than per-gen,
	// because a stdFunc's kinds are built once and compared by pointer from a
	// different gen.
	opaquesLoaded bool
	opaqueByDecl  map[*ast.TypeDef]int
	// The monomorphic stdlib enums, resolved against this module's analysis.
	// The same map and the same process-wide-def reasoning as the opaque
	// anchors above, keyed on *ast.EnumDef instead. See stdenum.go.
	stdEnumsLoaded bool
	stdEnumByDecl  map[*ast.EnumDef]int
	// dotOwners is the enum each `Enum.Variant` the builder writes for a
	// `.Variant` shorthand or a literal-attach form names: the type the
	// checker gave the written node. See dotvariant.go.
	dotOwners map[*ast.FieldAccess]*analysis.EnumType
	// funcValueRefs holds each reference callFuncValue is building the
	// function value of. Its body is the call of the same reference, so a
	// call the builder cannot lower would otherwise lower the callee as a
	// value again and recurse without end. See ownerfuncref.go.
	funcValueRefs map[ast.Node]bool
	// The stdlib opaque STRUCTS, on the same footing again and keyed on
	// *ast.StructDef. Distinct from the opaque family above because the
	// subjects are records with named fields rather than newtypes over a
	// scalar, so the shape check has to pin the field set. See stdstruct.go.
	stdStructsLoaded bool
	stdStructByDecl  map[*ast.StructDef]int
	// The GENERIC stdlib structs (`Set<T>`), resolved against this module's
	// analysis. ONE map, by Nomi name, and no `loaded` flag beside it: a nil map
	// IS the unloaded state, because stdGenStructAnchorsOf never returns nil.
	// The instances themselves are interned process-wide only — there is no
	// per-gen fallback, so there is no per-gen order to keep either. See
	// stdgenstruct.go.
	genStructs map[string]*stdGenStructAnchor
	// The stdlib nil-inner MARKERS (`ChannelClosed`). The same map and the same
	// process-wide-def reasoning as the opaque anchors above, keyed on
	// *ast.TypeDef — the SAME declaration kind, distinguished by having no
	// inner type at all rather than a scalar one. See stdgenhost.go.
	stdMarkersLoaded bool
	stdMarkerByDecl  map[*ast.TypeDef]int
	// stdIfaceDecls is the declaration each spec this module anchors (an
	// import of the declaring std module reaches it) was reached through,
	// by spec index, for the defaults it declares beside its table
	// (stdIfaceOverride). See stdiface.go.
	stdIfacesLoaded bool
	stdIfaceDecls   map[int]*ast.InterfaceDef
	// ifaces is the module's interface declarations, by Nomi name. The
	// *ifaceDef a name resolves to IS the interface's identity, which is what
	// keeps `Greeter.greet` and `Farewell.greet` two different tables on a
	// type implementing both. See impl.go.
	ifaces map[string]*ifaceDef
	// ifacesByDecl is defsByDecl for the module's interface declarations
	// (typeRegistry.ifaceDefOf).
	ifacesByDecl map[ast.Node]*ifaceDef
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
	// them by (interface identity, receiver kind) for dispatch: the
	// interface's origin and name (implDef.ifaceKey).
	implOrder    []*implDef
	implsByIface map[string]map[kind]*implDef
	// instImpls holds the impls of a generic interface a receiver implements
	// at several type arguments (`impl Contains<String> for Text` beside
	// `impl Contains<Regex> for Text`), keyed (interface, receiver), each
	// at its own instArgs. Such a receiver has no implsByIface entry for the
	// interface, so a lookup there never answers one of several; a call
	// selects among these (instimpl.go).
	instImpls map[string]map[kind][]*implDef
	// erasedInstances are the generic struct instances this unit holds as
	// an interface's existential (irErases), whose per-instance impls
	// irInstanceImpls records for the VM's dispatch.
	erasedInstances map[kind]bool
	// boxes is what this unit boxes into existentials (irboxed.go): each
	// (interface, kind) pair's plan, the impl entries the boxes dispatch
	// through, and the enums it boxes when the program has no registry.
	boxes irBoxes
	// ownerImpls is the owner functions of each inherent block on a generic
	// type or an interface, by the owner's name: the functions whose
	// signature names none of the block's type parameters, built once with
	// no receiver. See ownerimpl.go.
	ownerImpls map[string][]*implDef
	// operImpls is the PARALLEL index for the four stdlib operator interfaces,
	// keyed (interface, receiver, RIGHT-HAND type) — the axis implsByIface's key
	// drops. A receiver legally carries SEVERAL impls of one operator interface
	// (std/calendar ships twelve `Add` impls for NaiveDateTime), so that map
	// collapses them and refuses all of them as duplicates.
	//
	// A second index rather than a wider shared key: every lookup in
	// implsByIface asks for one receiver's impl of an interface, and under a
	// widened key a lookup that lost its existential over the rhs axis would
	// answer "no impl" for a type that has one. Nothing operator-shaped is
	// registered in implsByIface at all, so every lookup in it is unaffected
	// by construction. See operimpl.go.
	//
	// Used for the duplicate rule; an operator reaches its impl by its
	// record (recordedOperatorImpl).
	operImpls map[operKey]*implDef
	// implItemsByDecl is every impl function and owner function this gen
	// registered, by the declaration it lowers (implItem.source): its `fn`,
	// or for a default a block inherits, the interface's function. What a
	// checker record names (an operator's OperatorUse.Impl.Method, a
	// call's Callee.Decl) reaches its item through it, with no name or kind
	// lookup (recordedImplItem). A generic receiver's block is registered
	// once per instance, so one declaration holds an item per instance, and
	// an interface's default one per block that inherits it. Filed by
	// addImplItem, which every registration of an item goes through.
	implItemsByDecl map[ast.Node][]*implItem
	// implBlock is the USER impl block whose function body is being lowered
	// right now, or nil outside one. It exists for exactly one question: a
	// bare `each_while(left, yield)` inside `impl Iter for Tree` names a
	// member of THAT block, and a member is in neither g.funcs nor the file's
	// module scope, so without this the call would be refused as
	// `call to an unlowered function`, a name that describes neither the
	// construct nor the obstacle. Saved and restored around the body rather than
	// set once, because an impl function may lower a lambda that lowers
	// another call, and because a nested `fn` is its own boundary. See
	// barecallimpl.go.
	//
	// `implSelf` further down is the STDLIB counterpart of the same idea and
	// is the precedent this follows: it names the receiver of the std impl
	// block being lowered, so a bare `to_string(x)` inside
	// `impl Debug for Float` resolves to that type's own method. Two fields
	// rather than one because the two carry different things — a receiver
	// SPELLING there, the block itself here — and because they are live in
	// disjoint phases, so merging them would create a shared field with two
	// meanings and no way to assert which one is set.
	implBlock *implDef
	// tids is each type this file declares that has a runtime identity for
	// existential dispatch.
	tids map[kind]bool

	// result is the declared result kind of the function being lowered.
	result kind
	// lambdaWantFor is the lambda a binding is lowering and lambdaWant the
	// result type the checker solved for it (lambdaResultWant). One slot:
	// the binding lowers its own lambda before anything else can ask.
	lambdaWantFor *ast.Lambda
	lambdaWant    kind
	// holeSubst names, while a capture's body lowers, the parameter each
	// of its `&N`s is read as (capture.go).
	holeSubst map[ast.Node]madeName
	// unitFnLambda is the lambda a nested Unit `fn` is lowering as: its
	// body may end in a `dbg` observation, whose value is discarded.
	unitFnLambda *ast.Lambda
	// declaredFnLambda is the lambda a nested `fn` with a declared result
	// is lowering as: its result is that type (lambdaWant), never the
	// body's own.
	declaredFnLambda *ast.Lambda
	// rebuilding is the path of (have, want) pairs whose widening adapter is
	// being built (irembedsrebuild.go), each with the adapter's function and
	// its self parameter, so a generic enum that holds itself calls its own
	// adapter instead of building adapters forever.
	rebuilding map[[2]kind]irRebuildSelf
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

	// declaresTests says this module is the one whose tests run. inTest
	// is set while a test case is lowered. See tests.go.
	declaresTests bool
	inTest        bool
	// twins is the module's Module.twins.
	twins map[ast.Node]ast.Node
	// std is the lowered standard library as this module's call sites see it,
	// or nil for a module lowered without one. See stdlib.go.
	std *stdlibIndex
	// stdSiblingDecls and implSelf are set only while a STDLIB module is
	// being lowered: the first is every sibling function in that module under
	// its declaration, which a bare call the checker resolved to one finds it
	// by (`to_string(x)` inside `impl Debug for Float` is that type's own
	// method), the second is the receiver of the impl block whose item is
	// being lowered.
	stdSiblingDecls map[ast.Node]*stdFunc
	// unchecked holds the nodes of code the builder writes and lowers
	// without the checker having checked it (markUnchecked).
	unchecked map[ast.Node]bool
	// madeRefs is the key of the local each identifier the builder wrote
	// reads (madeRead), and madeKeys how many keys madeLocal has handed
	// out: a local the builder declares has no source declaration, so its
	// key is a position no source holds (irlocals.go).
	madeRefs map[*ast.Ident]analysis.Pos
	madeKeys int
	// madeParams is the key of each parameter of a lambda the builder
	// makes (a capture's, a partial application's, a wrapper's), whose
	// parameters stand at one written node's position (madeLambda).
	madeParams map[*ast.Lambda][]analysis.Pos
	implSelf   string
	// defaultSelf is set while an interface default is lowered for one
	// implementor: the interface, and the implementor's kind every `self`
	// position takes. See inheritDefaults.
	defaultSelf *irDefaultSelf
	// defaultInst is the instance whose body is being lowered while this
	// file lowers one of its interface's defaults for another file's
	// implementing type (ifacedefault.go).
	defaultInst *ifaceDefaultInst
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
	// index in it, or -1. See siblings.go.
	files    *fileIndex
	fileUnit int
	// reg is the program-wide type registry, or nil for a module lowered
	// alone. foreignDefs interns this gen's mirror of another file's
	// type, keyed by the declaration node — which IS the type's identity, so
	// two names for one declaration reach one mirror. importWhy carries the
	// reason the last import refused. typesBusy and typesDone drive the
	// demand-driven type-table build. See foreign.go.
	reg         *typeRegistry
	foreignDefs map[ast.Node]*typeDef
	// foreignInstDefs is foreignDefs for a monomorphized instance of another
	// file's generic template, keyed by the owner's instance def rather
	// than by a declaration node — because every instance of one template
	// shares that node and keying on it would hand `Holder<Int>` and
	// `Holder<String>` one mirror. The owner's def pointer IS the
	// instantiation's identity there, which is the property genericInstKey
	// establishes. See foreign.go's importGenericInstance.
	foreignInstDefs map[*typeDef]*typeDef
	// siblingInstDone is each (impl block, owner instance) pair registered
	// here for another file's template. See siblingtemplateimpl.go.
	siblingInstDone map[siblingInstKey]bool
	// stdRecvTemplates are this file's impl blocks over a std generic
	// container, and stdRecvDone each one registered at one of its kinds
	// (nil when it declined there). See stdrecvimpl.go.
	stdRecvTemplates []*stdRecvTemplate
	stdRecvDone      map[stdRecvKey]*implDef
	importWhy        string
	typesBusy        bool
	typesDone        bool
	// building is the type group this file's types are built in, a group
	// of this file alone when it is on no cycle; nil until the build starts
	// and for a stdlib module. See typecycle.go.
	building *typeGroup
	// nodes is the module's checked AST, held so the type table can be built
	// on demand from another file's resolution rather than only in order.
	nodes []ast.Node
	// nestedTypeKeys maps a type name this module declares in a NESTED position
	// — inside a `test` body, a function body, any block — onto the tally key
	// that declaration's OWN refusal carries, and "" when two nested
	// declarations of one name disagree on it. Gathered lazily, and only
	// when an unresolvable type name has to be told apart from a name
	// nothing declares at all. See sigreason.go.
	nestedTypeKeys map[string]string
	// implEmitting is the impl block whose item `implFunc` is currently
	// lowering, or nil outside one.
	//
	// It exists for exactly one question and should not grow a second reader
	// without a reason: a bare call to a REQUIREMENT of that block's interface
	// — from an inherited default body monomorphized here, or from a function
	// the block wrote itself — resolves to this receiver's implementation, and
	// that answer is a property of the enclosing impl, which nothing in the
	// expression walk otherwise carries.
	//
	// It is the receiver being lowered rather than a map of calls inside
	// default bodies: a map of calls could say which default body a call sat
	// in but not which receiver was being lowered, and would miss a bare
	// requirement call in an impl's own function, which is what
	// testdata/iface_default_sibling.nomi exercises.
	//
	// DISTINCT FROM `implSelf` above, which is a receiver NAME set only while a
	// stdlib module is lowered. Two fields rather than one because they answer
	// different questions over disjoint populations, and merging them would
	// make a stdlib impl's receiver look like a user impl block.
	implEmitting *implDef
	// foreignIfaces interns this gen's mirror of a sibling file's
	// interface, keyed by the declaration node — which IS the interface's
	// identity, so two names for one declaration reach one mirror. impls is
	// the program's whole impl index, which is the scope a boxing site has to
	// ask about. See existential.go.
	foreignIfaces map[ast.Node]*ifaceDef
	impls         *implIndex
	// iter is std/iter's `Iter` interface as this module's analysis resolved
	// it, shape-checked against the push protocol rt implements, or nil. The
	// declaration node IS the identity, exactly as it is for a prelude enum.
	// iterLoaded distinguishes "not resolved yet" from "resolved to nothing",
	// so a module without the protocol asks once. See iter.go.
	iter       *iterAnchor
	iterLoaded bool
	// hostTypes is a user file's `host type` declarations by Nomi name, nil
	// for a module that declares none, which is nearly all of them (a nil
	// map reads as the empty one). The *typeDef values are process-wide,
	// because a `host fn`'s kinds are compared by pointer from another gen.
	// See hostpkg.go.
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
	g := &gen{dc: newDeclineCensus(DeclineHooks{}), nomiPath: m.Path, pkg: pkg, fa: m.FA, funcs: map[string]*fnSig{}, onces: map[string]*onceDef{}, oncesByDecl: map[ast.Node]*onceDef{}, types: map[string]*typeDef{}, blockTypes: map[*ast.Block]*blockTypeDecls{}, ifaces: map[string]*ifaceDef{}, implsByIface: map[string]map[kind]*implDef{}, tids: map[kind]bool{}, std: std, files: files, fileUnit: unit, reg: reg, foreignDefs: map[ast.Node]*typeDef{}, foreignIfaces: map[ast.Node]*ifaceDef{}, nodes: m.Nodes}
	g.declaresTests = m.DeclaresTests
	g.twins = m.twins
	if name, inStd := strings.CutPrefix(m.Name, "std/"); inStd && isProgramStd(name) {
		g.programStd = true
	}
	// Loaded here rather than on first use, so no builder arm's answer
	// depends on whether an earlier expression happened to load them
	// (TestPreludeAnchorsDoNotDependOnOrder).
	g.loadPreludes()
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
	// Host handles FIRST, ahead of the interface shells: a user file's
	// `host type` is a named type a struct field or an interface function's
	// parameter may hold, and it resolves through no table any later wave
	// builds. See hostpkg.go.
	g.collectHostTypes()

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
	// sibling file's erasure of one of these types is discovered while that
	// file's body is lowered. See existential.go.
	g.mintTypeIDs()
	g.typesDone = true
}

// declareFuncs builds the module's function signatures and impl tables, which
// may name any type any file declares.
func (g *gen) declareFuncs() {
	// Signatures are collected before any body is lowered: Nomi has no
	// forward-declaration rule, so a call on line 3 to a function declared on
	// line 30 is legal and must resolve.
	for _, n := range g.nodes {
		fd, ok := n.(*ast.FuncDef)
		if !ok {
			continue
		}
		g.declareFunc(fd, fd.Name, g.signature(fd))
	}
	// A user file's `host fn`, on the same footing as a `fn` and for the
	// reason that matters: a call site resolves it through `g.funcs` like any
	// other, so the call lowering, the named-argument walk and the arity
	// check all run unchanged against a host callee. See hostpkg.go.
	for _, n := range g.nodes {
		ef, isExtern := n.(*ast.ExternFunc)
		if !isExtern || g.stdModule != "" || !hostTableFn(ef) {
			continue
		}
		g.declareFunc(ef, ef.Name, g.hostSignature(ef))
	}

	// `once` bindings alongside signatures, for the same reason: a function
	// declared above a `once` may read it, and so may another `once`. See
	// once.go.
	g.declareOnces()

	// Impls next: an impl item's signature may name any declared type. Only
	// registered here; settleImpls lowers the synthesized ones once every
	// file's impls are registered and indexed.
	g.buildImpls(g.nodes)
}

// settleImpls lowers the module's synthesized impl blocks speculatively.
//
// It runs after every file's declareFuncs and after the program's impl
// members are indexed (resolveImplMembers), because a synthesized body may
// call an impl another file declares: `derive Equatable for Rec` over a field
// of a type another file declares compares that field with the other file's
// `Equatable.equal`. Lowered before that index exists, the call found no
// candidate and the whole derived block was refused.
func (g *gen) settleImpls() {
	if g.linked || g.fromObjects {
		// The kept lowering speculated these blocks; its refusals stand
		// for them here (programstdlink.go).
		g.settleLinkedSynthImpls()
	} else {
		g.emitSynthImpls(g.implOrder)
	}

}

func (g *gen) emitModule(m *Module) ([]Unsupported, []Suppression) {
	if g.fromObjects {
		// Its bodies are the kept objects' (perfile.go).
		return nil, nil
	}
	if g.linked {
		// Its bodies are the kept lowering's; only the instances the
		// program's files asked of it are lowered here, and its refusals
		// are the kept lowering's (programstdlink.go).
		g.flushMonoInstances()
		g.flushGenericImpls()
		g.flushMethodInsts()
		g.flushIfaceDefaults()
		g.flushFieldDefaults()
		g.flushIfaceSlotDefaults()
		return nil, nil
	}
	// Collected before any body is lowered, because the duplicate-name
	// question has to be answered before a table exists and answering it needs
	// every case. Lowered from the walk below, so the table's order is the
	// declaration order the report uses. See tests.go.
	testCases := collectTestCases(m.Nodes, g.declaresTests)
	g.refuseDuplicateTestNames(testCases)
	byOwner := testCasesByOwner(testCases)
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
			// Includes the name-only `impl Debug for T` the front end
			// appends for every opaque and host type; implDecl drops a
			// synthesized block rather than blaming a fabricated position
			// for it.
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
			// so a front-end pass that starts producing one cannot go
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
			// `derive Equatable for Dog`. LowerDerives has already turned
			// it into an ordinary impl block, lowered above through exactly
			// the path a hand-written impl takes — so the declaration itself
			// has nothing left to lower and refusing it would report a gap
			// that does not exist.
		case *ast.TypeAlias:
			// `typealias Handler (String, String) -> Result<String, String>`.
			// Transparent: it introduces no type, so there is nothing to lower
			// and nothing to declare. An annotation naming it resolves to the
			// target's kind through typeOf. Its own arm rather than a silent
			// skip, because the DECLARATION is the position a programmer can
			// act on when the target is unrepresentable, exactly as
			// blocklocaltype.go says for the block-local form.
			// See typealias.go.
			g.moduleAliasDecl(t)
		case *ast.TestDecl:
			g.testDecl(byOwner[n])
		case *ast.OnceBinding:
			g.onceDecl(t)
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
	// The defaults of this file's interfaces another file's implementing
	// blocks asked for. See ifacedefault.go.
	g.flushIfaceDefaults()
	// The field-default accessors another file's literals asked for. See
	// foreignfielddefault.go.
	g.flushFieldDefaults()
	// The parameter-default accessors of this file's interface functions
	// another file's calls asked for. See ifaceslotdefault.go.
	g.flushIfaceSlotDefaults()
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
// blockers.
func (g *gen) rejectWhole(construct, detail string, n ast.Node) {
	g.reject(construct, detail, n)
}

// --- scopes ----------------------------------------------------------------

// --- declarations ----------------------------------------------------------

// declareFunc registers sig as the signature of the module function decl
// declares, under its name and under the declaration.
func (g *gen) declareFunc(decl ast.Node, name string, sig *fnSig) {
	g.funcs[name] = sig
	if g.sigByDecl == nil {
		g.sigByDecl = map[ast.Node]*fnSig{}
	}
	g.sigByDecl[decl] = sig
}

// signature resolves a function's declared types without lowering anything, so
// a call can be checked against a callee declared later in the file. It is
// the value domain's entry for a declared function: a parameter or result
// kind outside irValueDomainKind (a bound-free generic's own type
// parameters aside) leaves the signature unlowerable, so a call through it
// never sees one (irSigDomainKind).
func (g *gen) signature(fd *ast.FuncDef) *fnSig {
	sig := g.declaredSignature(fd)
	if sig.lowerable && !sig.inValueDomain() {
		sig.lowerable = false
	}
	return sig
}

// inValueDomain reports whether every parameter and the result of sig is
// in the value domain (irSigDomainKind). A kind no type gave (kindInvalid)
// is the signature's own refusal, reported by name in funcDecl.
func (sig *fnSig) inValueDomain() bool {
	for _, k := range append([]kind{sig.result}, sig.params...) {
		// kindInvalid: cascade — funcDecl rejects a kindless parameter or result by name; this check would echo it.
		if k != kindInvalid && !irSigDomainKind(sig, k) {
			return false
		}
	}
	return true
}

// declaredSignature is signature before the value domain is held.
func (g *gen) declaredSignature(fd *ast.FuncDef) *fnSig {
	// A bounded generic function has its own signature rule: each type
	// parameter is an existential over its bound. dictSignature declines
	// everything outside that, which keeps the clause below in charge of it.
	// See dict.go.
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
		// genericSignature declined. A declaration it declined for a type
		// parameter mentioned inside `List<T>`, `Half<T>` or `(T, Int)` is a
		// template instead, and monoTemplateFor re-derives its own
		// preconditions rather than reading `why`: that string is one
		// spelling for several reasons, so keying off it would make a
		// decorator on a generic `fn` a template. See genericmono.go.
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

// irSigDomainKind reports whether k, a parameter or the result of sig, is in
// the value domain (irValueDomainKind), or mentions a type parameter of a
// bound-free generic declaration's own signature, the one place such a
// kind is held.
func irSigDomainKind(sig *fnSig, k kind) bool {
	return irValueDomainKind(k) || sig.tps != nil && irMentionsTypeParam(k)
}

func (g *gen) funcDecl(fd *ast.FuncDef) {
	g.at(fd.Line)
	if g.errorDeclBody(fd) {
		return
	}
	// Guarded on the declaration POINTER, not just on nil: funcDecl also sees
	// impl functions, and g.funcs is keyed on a bare name that an impl method
	// may share with a free `fn`. Without this an impl `foo` would be reported
	// under the reason a free generic `foo` recorded.
	//
	// `g.monoSig` overrides both, and only while emitMonoInstance is lowering
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
	// A program's `fn boot` is not an ordinary function: the VM calls it once
	// before the entry function and publishes the fields of its result by
	// name. stageProgramApp hands it to irBootRetain (irappboot.go), which
	// builds its body under its own rules for the result and the parameters.
	// Ahead of every clause below because those clauses refuse a signature,
	// and this declaration's signature is not held to them. See appfield.go.
	if g.isProgramBoot(fd) {
		g.stageProgramApp(fd)
		return
	}
	switch {
	// A monomorphization template is not a function, it is a rule for making
	// them, so there is nothing to lower here and nothing to refuse: a call
	// resolves it to a concrete instance and a call the builder cannot
	// instantiate refuses at the CALL, which is where the type argument is
	// known. That is cascade.go's discipline — refuse where the answer is —
	// and it is why the declaration itself is not refused. A template nobody
	// calls builds nothing. Ahead of the generic clause because `genericWhy` is
	// still recorded on it. Its effects for every type are its abstract
	// body's (abstract.go).
	case sig.mono != nil:
		g.queueAbstract(fd, sig)
		return
	// The generic clause is `sig.dict == nil && sig.genericWhy != ""`: two
	// arms of one decision, both settled in the SIGNATURE and reported here
	// rather than re-derived. dict.go claims a bounded generic and generic.go
	// a bound-free one; whichever declines records the
	// construct, so this clause names it without knowing which arm answered.
	case sig.dict == nil && sig.genericWhy != "":
		g.rejectWhole(sig.genericWhy, fd.Name, fd)
		return
	case len(fd.Decorators) > 0:
		g.rejectWhole("decorator", fd.Name, fd)
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
			// introduces are bound in the function's prologue below.
			if _, resolved := g.patternParamKind(p, fd); !resolved {
				ok = false
			}
		// kindInvalid: reports — an unannotated parameter whose default gave no kind is rejected here by name.
		case p.TypeAnnotation == nil && (p.Default == nil || sig.params[i] == kindInvalid):
			g.reject("parameter without a declared type", p.Name, fd)
			ok = false
		// kindInvalid: reports — rejectTypeAnnotation names the type's own gap.
		case sig.params[i] == kindInvalid:
			g.rejectTypeAnnotation(p.TypeAnnotation, p.Name, fd)
			ok = false
		case !irSigDomainKind(sig, sig.params[i]):
			// The value domain's entry for a signature (irValueDomainKind).
			g.reject("parameter kind outside the value domain", p.Name+": "+sig.params[i].nomi(), fd)
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
	// kindInvalid: cascade — the arm above rejected a kindless result by name; this check would echo it.
	if sig.result != kindInvalid && !irSigDomainKind(sig, sig.result) {
		g.reject("result kind outside the value domain", sig.result.nomi(), fd)
		ok = false
	}
	if !ok {
		return
	}
	if sig.dict != nil {
		// A bounded generic's own body runs nowhere: a call and a function
		// value each lower an instance of the template irMonoTemplate
		// derives, and another file's call instantiates it in its own file
		// (siblinggeneric.go). Its effects for every type are its abstract
		// body's, lowered once the program is (abstract.go).
		g.queueAbstract(fd, sig)
		return
	}

	prev := g.result
	g.result = sig.result
	// The function's result slot and its parameters belong to the retained
	// `ir.Func` this shell opens. See irslot.go.
	shell := g.irFuncShellWithCallee(fd, irFuncSigOf(fd, sig), sig.params, g.irFunctionCallee(sig))
	// A body the builder declines retains nothing, and the VM reports it as
	// blocked if a program reaches it (unloweredBody, irnative.go).
	g.irBodyObserve(irBodyModuleFn, true)
	produced, retained := g.irScalarLower(fd, irFuncSigOf(fd, sig), shell, sig.result)
	if !retained {
		if sig.tps != nil {
			// A bound-free generic's erased body, which reads a value of a
			// type parameter: its abstract body answers for every type
			// (abstract.go).
			g.queueAbstract(fd, sig)
		}
		g.unloweredBody(irDeclined{})
		produced = sig.result
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
	// Another file's call that omits a defaulted parameter calls this file's
	// accessor for it (siblingdefault.go).
	g.irFileSlotDefaults(fd, sig)
}

// --- statements ------------------------------------------------------------

// --- expressions -----------------------------------------------------------

// --- calls -----------------------------------------------------------------

// printKey, writeKey and inspectKey are std/io's output primitives, which
// hostOutput answers directly rather than through the stdlib index.
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
// `io.inspect` is NOT a second Debug renderer. It renders a value as
// `Debug.inspect(x)` and `dbg` do (debugOver, irfuncbody.go). Reaching for
// the nearest renderer instead is a silent wrong string on four shapes (struct
// field order, a hand-written `impl Debug`, an opaque type, a function); see
// debugdisplay_test.go's mutation note.
const (
	printKey   = "io.print"
	writeKey   = "io.write"
	inspectKey = "io.inspect"
	// eprintKey and ewriteKey are the stderr pair: print and write to the
	// error stream, rendered the same way.
	eprintKey = "io.eprint"
	ewriteKey = "io.ewrite"
)

// outputKeys are std/io's output primitives.
var outputKeys = []string{printKey, writeKey, inspectKey, eprintKey, ewriteKey}

// isOutputKey reports whether key is one of std/io's output primitives.
func isOutputKey(key string) bool {
	return slices.Contains(outputKeys, key)
}
