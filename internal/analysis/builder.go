package analysis

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
	"path/filepath"
	"sort"
	"strings"
)

// ImplTypeArgs is the type-argument template for an `impl Iface for T<...>` or
// `impl Iface<...> for T<...>`. `TypeParamDefs` are the impl'd type's formal
// type-param pointers (e.g. List's `T`); `Args` are the interface's type
// arguments expressed in terms of those pointers. Args may be written in the
// impl header (`Add<Days, Day>`) or solved from method signatures (`Iter<T>`
// from `next`). To get the concrete interface type args for a use site like
// `List<Int>`, substitute TypeParamDefs[i] -> use-site TypeArgs[i] into each
// Args entry by pointer identity. The unifier consults this (via
// unifyInterfaceAgainstConcrete) to bind any generic interface's type
// parameters from a concrete receiver.
type ImplTypeArgs struct {
	TypeParamDefs []*TypeParam_
	Args          []Type
	Receiver      Type
}

// InherentMethodRecord captures one compiler-synthesized type-owned function for
// the project-level inherent index + coherence checks. Receiver is the owning type's
// base name; Module is the import-form module key the IMPL BLOCK lives in ("" for
// the project entry). Sym is the function's symbol
// (carrying OwningType + visibility) so the inherent index can record the
// owning type.
//
// ReceiverOrigin is the receiver TYPE's declaring build key — its nominal
// identity, which `Module` is not: `impl Iter for List` lives in std/iter.nomi
// while `List` is declared in std/lists.nomi. It is empty
// (`OriginUnresolved`) until `PopulateInherentReceiverOrigins` runs, which it
// cannot before BuildTypes; see inherent_identity.go for why it exists and
// what an unresolved value means to the collision check.
type InherentMethodRecord struct {
	Receiver       string
	ReceiverOrigin string
	Method         string
	Module         string
	Fn             *ast.FuncDef
	Sym            *Symbol
}

// InherentImplBlockRecord captures one compiler-synthesized inherent block for
// block-level coherence checks. Method records are still collected separately
// because the inherent method index is function-based; this record lets the
// project reject duplicate lowered blocks and cross-file inherent extensions.
type InherentImplBlockRecord struct {
	Receiver        string
	Signature       string
	Module          string
	Line            int
	Col             int
	TypeVarReceiver bool
}

// IndexImplBlockFuncDefs walks `nodes` for top-level ImplBlock declarations and
// records their items into the dispatch index:
//
//   - Interface-impl blocks (`impl Iface for T { fn m }`) feed `index`
//     (iface → method → []fn) and `files` (fn → module key), so the
//     collision/orphan back-end sees them. The header receiver is recorded in
//     `receivers` (fn →
//     receiver base name) because the item's first parameter is `self`, which
//     funcDefReceiverBaseName cannot resolve.
//   - Compiler-synthesized type-owned method blocks are collected into
//     `inherents` for the inherent-specific collision + orphan checks and the
//     inherent index.
//
// `receivers` is also populated for inherent methods so a single receiver
// lookup covers both. `modulePath` is the import-form key ("" for the entry).
// fileDefs is the file's Definitions map, used to recover each item's Symbol.
func IndexImplBlockFuncDefs(
	nodes []ast.Node,
	modulePath string,
	fileDefs map[Pos]*Symbol,
	index map[string]map[string][]*ast.FuncDef,
	files map[*ast.FuncDef]string,
	receivers map[*ast.FuncDef]string,
	interfaceKeys map[*ast.FuncDef]string,
	externIndex map[string]map[string][]*ast.ExternFunc,
	externFiles map[*ast.ExternFunc]string,
	externInterfaceKeys map[*ast.ExternFunc]string,
	inherents *[]InherentMethodRecord,
) {
	for _, n := range nodes {
		blk, ok := n.(*ast.ImplBlock)
		if !ok {
			continue
		}
		recv := TypeExprBaseName(blk.Receiver)
		if recv == "" {
			continue
		}
		if blk.Interface != nil {
			ifaceName := TypeExprBaseName(blk.Interface)
			if ifaceName == "" {
				continue
			}
			for _, fn := range implBlockFuncDefs(blk) {
				if !implItemClaimsInterface(fn, ifaceName) {
					continue
				}
				if index[ifaceName] == nil {
					index[ifaceName] = make(map[string][]*ast.FuncDef)
				}
				index[ifaceName][fn.Name] = append(index[ifaceName][fn.Name], fn)
				files[fn] = modulePath
				receivers[fn] = recv
				if interfaceKeys != nil {
					interfaceKeys[fn] = blk.Interface.TypeString()
				}
			}
			// `host fn` interface impls feed the parallel extern index +
			// files so go-to-def / hover can navigate to them; the receiver
			// is recorded per-file in defineImplBlockAnnotations.
			for _, ext := range implBlockExternFuncs(blk) {
				if !implItemClaimsInterface(ext, ifaceName) {
					continue
				}
				if externIndex[ifaceName] == nil {
					externIndex[ifaceName] = make(map[string][]*ast.ExternFunc)
				}
				externIndex[ifaceName][ext.Name] = append(externIndex[ifaceName][ext.Name], ext)
				externFiles[ext] = modulePath
				if externInterfaceKeys != nil {
					externInterfaceKeys[ext] = blk.Interface.TypeString()
				}
			}
		} else {
			for _, fn := range implBlockFuncDefs(blk) {
				receivers[fn] = recv
				var sym *Symbol
				if fileDefs != nil {
					sym = fileDefs[Pos{Line: fn.Line, Col: fn.Col}]
				}
				*inherents = append(*inherents, InherentMethodRecord{
					Receiver: recv,
					Method:   fn.Name,
					Module:   modulePath,
					Fn:       fn,
					Sym:      sym,
				})
			}
		}
	}
}

func importNameLeaf(name string) string {
	parts := strings.Split(name, ".")
	return parts[len(parts)-1]
}

func lookupImportOwnerSymbol(modScope *Scope, ownerSegments []string) *Symbol {
	if modScope == nil || len(ownerSegments) == 0 {
		return nil
	}
	var ownerSym *Symbol
	ownerScope := modScope
	for i, ownerName := range ownerSegments {
		ownerSym = nil
		if dotted := modScope.LookupLocal(strings.Join(ownerSegments[:i+1], ".")); dotted != nil {
			ownerSym = dotted
		} else if ownerScope != nil {
			ownerSym = ownerScope.LookupLocal(ownerName)
		}
		if ownerSym == nil {
			return nil
		}
		if ownerSym.ModuleScope != nil {
			ownerScope = ownerSym.ModuleScope
		} else {
			ownerScope = nil
		}
	}
	return ownerSym
}

func collectInherentImplBlocks(nodes []ast.Node, modulePath string, out *[]InherentImplBlockRecord) {
	for _, n := range nodes {
		blk, ok := n.(*ast.ImplBlock)
		if !ok || blk.Interface != nil {
			continue
		}
		recv := TypeExprBaseName(blk.Receiver)
		if recv == "" {
			continue
		}
		*out = append(*out, InherentImplBlockRecord{
			Receiver:        recv,
			Signature:       inherentImplBlockSignature(blk),
			Module:          modulePath,
			Line:            blk.Line,
			Col:             blk.Col,
			TypeVarReceiver: implBlockReceiverIsTypeVar(blk),
		})
	}
}

func collectDeclaredTypeNames(nodes []ast.Node, out map[string]bool) {
	for _, n := range nodes {
		switch t := n.(type) {
		case *ast.StructDef:
			out[t.Name] = true
		case *ast.EnumDef:
			out[t.Name] = true
		case *ast.TypeDef:
			out[t.Name] = true
		case *ast.ExternType:
			out[t.Name] = true
		case *ast.TypeAlias:
			out[t.Name] = true
		case *ast.InterfaceDef:
			out[t.Name] = true
		}
	}
}

func inherentImplBlockSignature(blk *ast.ImplBlock) string {
	if blk == nil || blk.Receiver == nil {
		return ""
	}
	s := blk.Receiver.TypeString()
	if len(blk.WhereClauses) == 0 {
		return s
	}
	clauses := make([]string, 0, len(blk.WhereClauses))
	for _, wc := range blk.WhereClauses {
		clause := wc.Name
		bounds := make([]string, 0, len(wc.Bounds))
		for _, bound := range wc.Bounds {
			bounds = append(bounds, bound.TypeString())
		}
		sort.Strings(bounds)
		for j := range bounds {
			if j == 0 {
				clause += ": "
			} else {
				clause += " and "
			}
			clause += bounds[j]
		}
		clauses = append(clauses, clause)
	}
	sort.Strings(clauses)
	s += " where "
	for i, clause := range clauses {
		if i > 0 {
			s += ", "
		}
		s += clause
	}
	return s
}

func implBlockReceiverIsTypeVar(blk *ast.ImplBlock) bool {
	st, ok := blk.Receiver.(*ast.SimpleType)
	if !ok {
		return false
	}
	for _, g := range blk.Generics {
		if g.Name == st.Name {
			return true
		}
	}
	return false
}

func implItemClaimsInterface(item ast.Node, ifaceName string) bool {
	switch it := item.(type) {
	case *ast.FuncDef:
		return TypeExprBaseName(it.ImplIface) == ifaceName
	case *ast.ExternFunc:
		return TypeExprBaseName(it.ImplIface) == ifaceName
	default:
		return false
	}
}

// RecordingKind classifies where a (Iface, Type) recording originated.
// Surfaced through Recording.Kind so the missing-impl diagnostic can
// describe the demand site in user-relevant terms ("required by a tagged
// literal slot", "required by `@derive Display` on …") instead of just
// dumping a position. The checker stores the kind; DetectMissingImpls
// renders it.
type RecordingKind int

const (
	// RecordingKindCallSite is the catch-all for plain call-site
	// recordings — argMatchesParam, recordInterfaceConformanceFromParam,
	// etc. The Recording's
	// Pos is the call expression's source position.
	RecordingKindCallSite RecordingKind = iota
	// RecordingKindTypedLiteralSlot marks a recording produced inside
	// a `<tag>"..."` Dynamic slot's conformance check against the tag
	// module's per-slot interface. Pos is the slot expression's site.
	RecordingKindTypedLiteralSlot
	// RecordingKindGenericBoundCheck marks a recording produced when a
	// generic call's solved type-param binding is checked against the
	// param's interface bound (`fn show<T>(x: T): String where T: Display`
	// called with `T := Int` records (Int, Display) here). Pos is the call
	// expression.
	RecordingKindGenericBoundCheck
	// RecordingKindInterfaceTypedParam marks a recording produced when
	// a concrete value flows into an interface-typed parameter or
	// interface-typed struct field (`Effects { logger: ProdLogger {} }`,
	// `Iter.to_list(xs)` where to_list takes `Iter<T>`). Pos is the
	// argument or field-value expression.
	RecordingKindInterfaceTypedParam
	// RecordingKindDeriveSynth marks a recording produced by
	// CheckDeriveBounds when it walks a `@derive Iface` type's component
	// types (struct fields, enum variant payloads, distinct inner). The
	// Recording's Derive carries the @derive decorator's position plus
	// the offending component's name+position. Pos is the @derive arg's
	// site (matches the existing diagnostic anchor for these checks).
	RecordingKindDeriveSynth
	// RecordingKindDerivePayload marks the catch-all (String, Display)
	// recording the Display/Debug @derive synthesizer emits because the
	// synth body's StringInterp literals re-dispatch Display.to_string
	// on every interpolated part. The Recording's Derive carries the
	// @derive decorator's position; the synthesizer doesn't know which
	// component triggered the StringInterp, so FieldName / FieldPos
	// stay empty.
	RecordingKindDerivePayload
	// RecordingKindEqualityOperator marks a *weak* demand recorded by
	// an equality operator (`==`, `!=`) on a concrete type. It wires
	// up the runtime dispatch table when an `impl Equatable` block
	// exists — that's how `DateTime`'s custom equality reaches the
	// `==` operator — but does NOT require one: when no impl exists,
	// equality falls back to structural `rt.Equal` with no
	// semantic change (equality has exactly one canonical structural
	// meaning — same shape, equal parts — so the fallback is almost
	// always what the user meant). `DetectMissingImpls` skips groups
	// whose recordings are *all* of this kind so distinct types and
	// enums without an `impl Equatable` block (e.g. `pub type Id Int`)
	// keep working with `==`.
	RecordingKindEqualityOperator
	// RecordingKindOrderingOperator marks an *ordinary (strong)*
	// demand recorded by an ordering operator (`<`, `>`, `<=`, `>=`)
	// on a concrete type. Unlike equality there is no structural
	// fallback for ordering — which field orders first is an
	// arbitrary choice, not invariant under field reordering, and a
	// wrong implicit order fails silently (a sort mis-sorts rather
	// than erroring) — so the demand requires a declared `impl
	// Comparable` (hand-written or `@derive`d) and flows through the
	// ordinary missing-impl diagnostic. The runtime's "cannot
	// compare" trap in `evalComparison` remains only as a backstop.
	RecordingKindOrderingOperator
	// RecordingKindOperator marks an ordinary (strong) demand recorded by
	// an operator that dispatches through a standard interface for nominal
	// types. The interface impl determines both the accepted right-hand
	// operand type and the result type.
	RecordingKindOperator
)

// DeriveCtx attaches `@derive`-specific provenance to a Recording. It
// names the interface being derived, the type carrying the @derive
// decorator, the decorator's source position, and (when known) the
// offending component's field / payload name + position. Populated for
// Recordings whose Kind is RecordingKindDeriveSynth or
// RecordingKindDerivePayload; nil otherwise.
type DeriveCtx struct {
	Iface     string
	TypeName  string
	DerivePos Pos
	FieldName string
	FieldPos  Pos
}

// Recording captures the provenance of one (Iface, Type) entry in
// FileAnalysis.ImplManifest. Pos is the source position the recording
// was triggered from (the call site, slot expression, struct-literal
// field value, etc.). Kind classifies the recording so the
// missing-impl diagnostic can describe the demand site precisely.
// Derive is set only for `@derive`-triggered recordings. Op is set
// only for operator-kind recordings (RecordingKindEqualityOperator /
// RecordingKindOrderingOperator / RecordingKindOperator) and carries the operator token
// (`==`, `<`, …) so the missing-impl diagnostic can name it.
type Recording struct {
	Pos    Pos
	Kind   RecordingKind
	Derive *DeriveCtx
	Op     string
	// TypeOrigin is the nominal identity of the type the demand is FOR: the
	// build key of the file that declared it (`std/calendar`, OriginEntry),
	// or OriginUnresolved when the demanded type is a primitive, a type
	// parameter, or anything else `nominalOrigin` reports no origin for.
	//
	// ImplManifest is keyed by BARE type name, so without this the three
	// stdlib types named `Error` record indistinguishable demands and
	// DetectMissingImpls' supply lookup lets one of them borrow another's
	// conformance. This is the demand half of that identity; the supply half
	// is ProjectImplIndex.ImplsByIdentity. See missing_impl_identity.go.
	//
	// Part of the struct rather than a side table because RecordManifest
	// deduplicates by full Recording equality, so two demands for two
	// same-named types at one position must not collapse into one entry.
	TypeOrigin string
}

// FileAnalysis holds the analysis results for a single file.
type FileAnalysis struct {
	ModuleScope *Scope
	// FilePath is the absolute path of the file this analysis describes.
	// Set by BuildProject for every project-loaded file (so its value
	// matches the SourceFile imported sibling files set on shared
	// symbols). Empty for the project entry and for standalone single-
	// file analysis (BuildFile). Used by the opaque-types boundary check
	// to tell same-module access apart from out-of-module access.
	FilePath string
	// Origin is the nominal-identity key for types this file declares —
	// the build's own module-path key (`std/calendar`, `shapes`,
	// `stringkit/pad`), or OriginEntry for the project entry file. It is
	// build-relative and stable, unlike FilePath, which is absolute and
	// (for stdlib) points into the per-process materialization cache.
	// A nominal type's identity is (Origin, Name); see nominal_identity.go.
	Origin string
	// References maps each identifier's position to the symbol it refers to.
	References map[Pos]*Symbol
	// PunnedFieldLabels maps the position of a punned struct-literal field,
	// the `context` of `App{context}` or `{context}`, to the field it
	// labels. One token there is both the field's label and a read of the
	// variable, and References holds the variable. Find References and
	// rename read this map for the field. Nil until a punned label is
	// recorded (setPunnedFieldLabel).
	PunnedFieldLabels map[Pos]*Symbol
	// Definitions maps each definition's position to its symbol.
	Definitions map[Pos]*Symbol
	// BlockImportScopes maps an `import` at the top of a block to the
	// block scope it binds into. The checker registers the types it binds
	// for the rest of that block, as it registers a file-level import's for
	// the whole file. Nil until a block import is walked.
	BlockImportScopes map[*ast.ImportStmt]*Scope
	// LambdaPatternTypes maps a lambda's destructuring parameter, keyed by
	// its pattern node (`(_, v)` in `|(_, v)| v`), to the type the checker
	// gave the whole parameter. The parameter has no name and so no symbol
	// in Definitions, and a wildcard part binds nothing, so the names the
	// pattern introduces cannot always say what the parameter is. Nil until
	// a pattern is recorded (recordLambdaPatternType).
	LambdaPatternTypes map[ast.Node]Type
	// ExprTypes maps every node the checker checked to the type it gave the
	// node, and ExpectedTypes maps every node checked against an expected
	// type to that expected type: the declared parameter type at an argument,
	// the annotation at a binding's value, the enclosing function's return
	// type at its tail, the subject's type at a case arm. Both are keyed by
	// node, as LambdaPatternTypes is, and hold the checker's types as it left
	// them, so a type variable inside one resolves through TypeVar.Resolved.
	// The editor's completion reads them to know what a value is and what a
	// position wants. Writing them changes nothing the checker decides.
	// Nodes in the derive-synthesis line band are not recorded. Nil until
	// the first record.
	ExprTypes     map[ast.Node]Type
	ExpectedTypes map[ast.Node]Type
	// TargetStructs maps a brace literal with no type name and no spread
	// (`{street: "1 Main", city: "Bath"}`) to the nominal struct it builds
	// because the position expected that struct (checkTargetTypedStructLit).
	// The IR builder lowers such a literal exactly as the named literal
	// `Address{...}`; a brace literal absent from this map is an anonymous
	// struct. Generic structs carry their solved type arguments. Nil until
	// the first record.
	TargetStructs map[*ast.StructLit]*StructType
	// FieldAccessors maps a field accessor (`.name`, `.address.city`) the
	// checker accepted to the type it reads from: the parameter type of the
	// function type its position expected. The IR builder lowers the
	// accessor as a one-parameter closure over that type. Nil until the
	// first record.
	FieldAccessors map[*ast.FieldAccessor]Type
	// DispatchNames tracks function names defined by impl/extend blocks.
	// These are runtime-dispatched and should not be type-checked at call sites.
	DispatchNames map[string]bool
	// Impls tracks which types implement which interfaces, collected from
	// `impl I for T` and `extend T as I` blocks. Map: typeName → interfaceName → true.
	// Used by the type checker to validate interface-typed struct fields.
	Impls map[string]map[string]bool
	// ImplManifest records (interfaceName, typeName) pairs the file's
	// call sites statically demand impls for, along with the list of
	// Recording entries that each demanded the pair. Populated by the
	// checker on every successful interface-conformance check; consumed
	// by DetectMissingImpls. Keyed interface→type (not type→interface
	// like Impls): walk interfaces, look up `impl Iface for T` block
	// methods by their receiver type.
	//
	// Each Recording carries the source Pos of the demand site, a
	// RecordingKind that classifies the site (plain call, typed-literal
	// slot, generic where bound check, derive synth, …), and — when the
	// recording was triggered by `@derive Iface` walking a component
	// type — a DeriveCtx with the @derive decorator's position and the
	// offending field's name+position. The recording list is preserved
	// (not collapsed to a bool) so DetectMissingImpls can
	// surface every demand site individually when a pair has no impl.
	// RecordManifest deduplicates by full Recording equality, so repeated
	// recordings from the same site contribute exactly one entry.
	ImplManifest map[string]map[string][]Recording
	// InferredTypeParamBounds is the interface bound a generic function's own
	// type parameter is USED AT, per declaration node and type-parameter name,
	// deduped and in first-seen order.
	//
	// # Why this exists, and why it is not a `where` clause
	//
	// `Debug` is UNIVERSAL: `SynthesizeUniversalDebug` writes an
	// `impl Debug for T` for every declared type, so `typeImplementsInterface`
	// answers TRUE for it at every concrete type AND at a bare type variable.
	// That is what lets a developer write
	//
	//	fn show<T>(x: T): String { Debug.inspect(x) }
	//
	// with no annotation at all — and it is also why nothing ever recorded that
	// `T` is used at a Debug bound. The conformance check cannot reject
	// anything, so there was nothing to enforce and no reason to write it down.
	//
	// A CONSUMER that compiles the body rather than walking it needs it anyway,
	// because a bound is what tells it which dispatch table the body's
	// `Debug.inspect(x)` goes through. So this records the bound the checker
	// INFERS rather than the one it enforces, and the two are deliberately
	// separate fields: an inferred entry must never make a program fail to
	// check, and `ft.WhereBounds` is where a failure would come from.
	//
	// SOUNDNESS, which is the whole justification for inferring at all: every
	// type satisfies `Debug`, so adding it to a type parameter rejects nothing
	// that would otherwise be accepted. That is a property of Debug and not of
	// interfaces, which is why only the universal ones are recorded here —
	// inferring `Display` would turn a legal program into an error.
	//
	// Keyed by the DECLARATION NODE rather than by function name: a nested `fn`
	// beside a module-level one of the same name is a different function, and
	// two impl blocks may declare the same method name.
	InferredTypeParamBounds map[*ast.FuncDef]map[string][]string
	// TypeMethodModules is the set of provider module paths for cross-module
	// type-promoted methods this file's call sites USE (resolved via the
	// project-level ProjectImpls.TypeMethods table — e.g. `List.concat` resolves
	// to std/lists). The runtime type-method bridge force-loads
	// these so the inherent method registers even when the file never imported
	// the provider; without it `List.concat` only works when something else
	// happens to load std/lists. Unioned across siblings by mergeImplManifest.
	// Keyed by the loadable module path (the filesByKey key); never "" (the
	// entry's own methods need no provider).
	TypeMethodModules map[string]bool
	// ImplTypeArgs records the first interface type-argument template for each
	// (implementing-type, interface) pair. ImplTypeArgSets records every
	// template for the same pair, which matters for operator-like interfaces
	// such as Add where interface arguments are part of impl identity.
	// Populated after BuildTypes so the unifier can bind any generic
	// interface's type params from a concrete receiver.
	ImplTypeArgs    map[string]map[string]*ImplTypeArgs
	ImplTypeArgSets map[string]map[string][]*ImplTypeArgs
	// ScopedFunctionFiles maps every function of the project to the file
	// that declares it, and BootFunctions holds every entry boot (a top-level
	// `fn boot` in a file that defines `fn main`), so the application-field
	// root check can follow calls across files (app_fields.go). AppTypes are
	// the structs those boots return: `T.field` is an application-field read
	// exactly when T is one of them.
	ScopedFunctionFiles map[*ast.FuncDef]*FileAnalysis
	BootFunctions       map[*ast.FuncDef]bool
	AppTypes            []*StructType
	// BootErrors are this file's boot errors from the project build: a
	// `fn boot` outside an entry file, or a boot signature or application
	// struct that is not valid. The checker reports them with the file.
	BootErrors []TypeError
	// Onces is the project's module-level `once` bindings, shared by every
	// file of one project build. See OnceTable.
	Onces *OnceTable
	// AppReads holds each checked application-field read (`MyApp.logger`)
	// and each `with` override's target, keyed by the *ast.FieldAccess, and
	// AppReadsByPos the same keyed by the field name's position.
	AppReads      map[ast.Node]AppRead
	AppReadsByPos map[Pos]AppRead
	// ContextType is the canonical std/context.Context identity.
	ContextType Type
	// StdlibModuleScopes maps each stdlib module's short name ("iter",
	// "maps", ...) to its defining module Scope. Stdlib symbols carry no
	// SourceFile (resolveStdlibImport returns prebuilt scopes, unlike the
	// user-module resolveImport path), so this map is how a pass finds the
	// scope that defines a stdlib symbol (the iter-sensitivity pass's
	// slotsForStdlibCallbackSymbol, derive synthesis, the IR builder).
	// Populated by BuildFileWithStdlib from the caller's stdlib modules
	// map, and by buildProjectWithCache from the cache's "std/<name>" FAs
	// (broadcast to every file's FA, same pattern as ContextType). Nil for
	// raw BuildFile callers.
	StdlibModuleScopes map[string]*Scope
	// IfaceMethodImpls indexes every `impl Iface for T { fn method(...) }`
	// method FuncDef
	// in the project, keyed by interface name → method name → list of impl
	// FuncDefs. Populated by BuildProject and broadcast across every file's
	// FA so cross-file impl-discovery works regardless of which file's
	// checker is running. Nil for raw BuildFile callers (tests, REPL).
	IfaceMethodImpls map[string]map[string][]*ast.FuncDef
	// IfaceMethodImplFiles maps each impl FuncDef pointer (from
	// IfaceMethodImpls) to the import-form module key of the file it
	// came from (e.g. "foo" or "sub/log"; "" for the project entry
	// file). Stdlib FAs populate the same shape via std.Load() with
	// "std/<name>" keys, so a consumer can split either on '/' to
	// recover the module path segments. The LSP
	// `textDocument/implementation` handler reconstructs the absolute
	// path via projectRoot + key + ".nomi" — IfaceMethodImpls's FuncDef
	// AST nodes carry only line/col, not path. Populated by BuildProject
	// in the same pass that builds IfaceMethodImpls, broadcast to every
	// file's FA the same way.
	IfaceMethodImplFiles map[*ast.FuncDef]string
	// ProjectImpls back-points to the project-level impl index so
	// code that receives an FA can reach project-wide impl data without a
	// separate *Project param. Populated by BuildProjectWithCache;
	// nil for raw BuildFile / BuildFileWithStdlib callers (consumers
	// check + fall back to per-FA Impls / IfaceMethodImpls).
	ProjectImpls *ProjectImplIndex
	// TypeMethods is the per-file type-qualified method table:
	// receiver-type base name → method name → Symbol. Populated by
	// defineImplBlock for compiler-synthesized type-owned functions and
	// interface (`impl Iface for Type { fn foo }`) blocks. The
	// Symbol carries OwningType = the receiver type. The precise table powers
	// receiver-specific lookup even when a module exposes one flattened name
	// such as `users.inspect(...)` for several receiver types.
	TypeMethods map[string]map[string]*Symbol
	// ImplBlockReceiver records, for each impl-block item FuncDef, the receiver
	// type base name taken from the BLOCK HEADER (not the first parameter).
	// Coherence (detectImplCollisions / detectOrphanImpls) keys interface-impl
	// methods on the receiver; for block forms the first parameter is the
	// concrete receiver type. Bodyless marker impls have no function item at
	// all, so funcDefReceiverBaseName can't recover it — this override map
	// supplies the header receiver when a function item exists. Also lets
	// the inherent collision check group an inherent block's methods by their
	// owning type. Nil for files with no impl blocks.
	ImplBlockReceiver     map[*ast.FuncDef]string
	ImplBlockInterfaceKey map[*ast.FuncDef]string
	// IfaceMethodImplExterns / IfaceMethodImplExternFiles / ImplBlockReceiverExtern
	// are the `host fn` parallel of IfaceMethodImpls / IfaceMethodImplFiles /
	// ImplBlockReceiver — those three are FuncDef-typed and so exclude the
	// `host fn` items of an `impl Iface for Type` block. Carrying externs here
	// lets resolveConcreteImpl resolve an extern interface impl to its concrete
	// source for go-to-def / hover (the FuncDef maps stay untouched because the
	// type-checker and the IR builder that read them genuinely want FuncDefs).
	IfaceMethodImplExterns      map[string]map[string][]*ast.ExternFunc
	IfaceMethodImplExternFiles  map[*ast.ExternFunc]string
	ImplBlockReceiverExtern     map[*ast.ExternFunc]string
	ImplBlockInterfaceKeyExtern map[*ast.ExternFunc]string
	// TypeErrors holds type-checking errors discovered after analysis.
	TypeErrors []TypeError
	// ImplImports are this file's imports of whole project files that no
	// name in the file uses. Such an import may still be needed for the
	// impl blocks the file brings into the program, and that is decided
	// over the whole program once every file is type-checked
	// (CheckImplImports). See unused_imports.go.
	ImplImports []*ImplImport
}

// LookupTypeMethod returns the impl-block method symbol for (typeName,
// methodName) from the file's type-method table, or nil if absent. Inherent
// and interface-impl methods both land here; inherent wins on a name clash
// (registered with that precedence by defineImplBlock).
func (fa *FileAnalysis) LookupTypeMethod(typeName, methodName string) *Symbol {
	if fa == nil || fa.TypeMethods == nil {
		return nil
	}
	if byName, ok := fa.TypeMethods[typeName]; ok {
		return byName[methodName]
	}
	return nil
}

// RecordManifest appends `rec` to the file's ImplManifest entry for
// (ifaceName, typeName), deduplicating by full Recording equality.
// Two recordings from genuinely different sites (different Pos, Kind,
// or DeriveCtx) both land in the slice; a duplicate write from the
// same site is a no-op. The `*DeriveCtx` pointer compares by identity
// — recorders pass a fresh DeriveCtx per Recording so structurally-
// equal DeriveCtxs from different recorders do NOT collapse; the
// dedup rule mirrors the structural intent because in practice each
// recorder allocates one DeriveCtx and reuses the pointer across all
// the recordings it emits for that derive (see derive_synthesis.go).
func (fa *FileAnalysis) RecordManifest(typeName, ifaceName string, rec Recording) {
	if typeName == "" || ifaceName == "" {
		return
	}
	// Drop recordings whose Pos.Line lies in the derive-synth fake-AST band
	// (Line >= synthLineBase). Synth bodies are generated to make the
	// type-checker happy; their internal call sites record (Iface, T) demands
	// with bogus line numbers that would leak into user-facing diagnostics.
	// The DeriveSynth/DerivePayload recording emitted at checkDeriveComponentTypes
	// already carries the real @derive/field source position for the same
	// (Iface, T) pair, so dropping the synth-body record loses no information
	// the user can act on. See synthLineBase in derive_synthesis.go.
	if rec.Pos.Line >= synthLineBase {
		return
	}
	if fa.ImplManifest == nil {
		fa.ImplManifest = make(map[string]map[string][]Recording)
	}
	if fa.ImplManifest[ifaceName] == nil {
		fa.ImplManifest[ifaceName] = make(map[string][]Recording)
	}
	existing := fa.ImplManifest[ifaceName][typeName]
	for _, prev := range existing {
		if prev == rec {
			return
		}
	}
	fa.ImplManifest[ifaceName][typeName] = append(existing, rec)
}

// RecordInferredTypeParamBound notes that `fn`'s own type parameter `param` is
// used at a bound of `ifaceName`.
//
// Additive and never an error: see the field comment for why inferring a
// UNIVERSAL interface's bound cannot reject a program that would otherwise
// check. Nothing in this package reads it; it exists for a consumer that
// compiles the body.
//
// Unlike RecordManifest this does NOT drop the derive-synth line band. A
// synthesized `impl Debug for Box<T>` really does use its `T` at a Debug bound
// — `boundedTypeParams` writes that bound explicitly — and a consumer lowering
// that body needs the same answer it needs for a hand-written one. The band
// exists to keep fabricated POSITIONS out of user-facing diagnostics, and there
// is no position here.
func (fa *FileAnalysis) RecordInferredTypeParamBound(fn *ast.FuncDef, param, ifaceName string) {
	if fa == nil || fn == nil || param == "" || ifaceName == "" {
		return
	}
	if fa.InferredTypeParamBounds == nil {
		fa.InferredTypeParamBounds = make(map[*ast.FuncDef]map[string][]string)
	}
	byParam := fa.InferredTypeParamBounds[fn]
	if byParam == nil {
		byParam = make(map[string][]string)
		fa.InferredTypeParamBounds[fn] = byParam
	}
	for _, prev := range byParam[param] {
		if prev == ifaceName {
			return
		}
	}
	byParam[param] = append(byParam[param], ifaceName)
}

// findContextType returns the Type of `Context` exported by the `context`
// stdlib module from a given modules map (module-name -> scope), or nil if
// the module is absent or `Context` isn't defined. Used by both
// BuildFileWithStdlib and BuildProject to populate FileAnalysis.ContextType.
func findContextType(modules map[string]*Scope) Type {
	ctxMod, ok := modules["context"]
	if !ok {
		return nil
	}
	sym := ctxMod.Lookup("Context")
	if sym == nil {
		return nil
	}
	if sym.Resolved != nil && sym.Resolved.Type != nil {
		return sym.Resolved.Type
	}
	return sym.Type
}

// checkReservedTypeName errors when `name` shadows a language-provided
// type the program can see — i.e. a name already defined in a parent
// (prelude) scope. `kind` is the user-facing description of what's
// being declared ("struct", "enum", "typealias", "distinct type") —
// surfaced in the error message. The caller should still register the
// symbol so downstream analysis produces fewer cascading errors.
//
// Pre-stdlib-as-package: this guarded against a curated `reservedTypeNames`
// set (Int / Float / String / List / Maybe / Result / Display / …).
// Post-cutover: the parent scope is the auto-prepended prelude, so the
// parent-scope check IS the prelude-shadowing check — no curated list
// needed. Any name in scope.Parent is, by construction, a prelude
// re-export. The reserved-name diagnostic is now self-derived from
// whatever prelude.nomi actually re-exports rather than a hand-
// maintained Go constant the spec had to track.
//
// The parent-scope lookup
// fires for every name prelude re-exports. Enum-variant re-exports
// now fire too: `True`, `False`, `Some`, `None`,
// `Ok`, `Err`, `Equal`, `Greater`, `Less`,
// `Fragment` — plus the formerly-curated type names like
// `Int`, `Display`, `Maybe`, `Result`. A user file declaring
// `struct Some { ... }` errors where it didn't pre-
// cutover. The broadening matches the spec's intent ("prelude names
// are reserved") — the curated set was lossy by accident, not by
// design. See analysis/reserved_names_test.go for the pinned set.
//
// Test fixtures and minimal embedded use cases that load no prelude
// (parent scope nil) bypass the check entirely — they're free to
// declare their own Int / Display / etc.
//
// To define a domain-specific Int-like type, use Nomi's distinct-type
// machinery (`type UserId Int`) — that creates a real new type rather
// than confusingly shadowing the built-in.
//
// # Interfaces, and why this restriction is PROVISIONAL
//
// Reserving interface names (defineInterfaceStub) closes a soundness gap;
// it is not a language design decision. Without it,
// `pub interface Display { fn render(value: self): String }` plus
// `impl Display for Point { fn render }` makes `where T: Display`
// STATICALLY PASS, and the program then dies at runtime with
// `Display.to_string: no implementation for type 'Point'` — where the
// same program without the local declaration is rejected at analysis
// time. A missing check that converts a static type error into a
// runtime crash is the whole reason interfaces are reserved here.
//
// The more Nomi-like answer is to ALLOW the shadow and distinguish the
// two interfaces by identity, because nominal identity is (declaring
// file, name) and two files may each declare their own `Point`.
//
// That answer needs TWO things and only one of them exists. Identity is
// the first, and it landed: analysis.InterfaceType now carries an Origin
// like StructType, EnumType and DistinctType. The second is a SCOPE SLOT, and it is strictly upstream —
// measured, with the reservation lifted and Origin present, a file
// declaring `pub interface Display` resolves `Display` in its own
// ModuleScope to std/display's declaration, Origin "std/display". The
// prelude's injected import owns the name. So:
//
//   - `Display.render(p)` reports "type 'Display' has no member
//     'render'" — the declaration is accepted and then permanently
//     unnameable, i.e. accepted dead code; and
//   - the original soundness gap REPRODUCES unchanged with the field in
//     place — `where T: Display` passes and the program dies at runtime
//     with `Display.to_string: no implementation for type 'Point'` —
//     because BOTH the bound and the `impl Display for Point` header
//     resolve through the same scope slot to std's declaration. There is
//     no second interface for identity to distinguish, so the field
//     cannot fire at all.
//
// That is why identity alone does not lift this, and why the field is
// necessary rather than sufficient: the missing piece is syntax that
// disambiguates a local declaration from a prelude re-export, which is a
// language design decision and not a plumbing gap.
//
// So: reserving the 13 prelude-injected interface names (Add, Comparable,
// Debug, Discrete, Display, Divide, Equatable, Hashable, Iter, Multiply,
// Steppable, Struct, Subtract) stays correct, and the remaining unblocker
// is the scope slot rather than InterfaceType.Origin.
//
// The 8 stdlib interfaces that are NOT prelude-injected — App, Assertable,
// DateParts, TimeParts, Anchored, ToJson, FromJson, Literal — stay freely
// shadowable, and that is the principled line rather than an accident: a
// prelude re-export occupies every user file's scope unasked and Nomi has
// no syntax that disambiguates a local declaration from it, whereas a name
// the user must import is a name the user chose and can stop choosing.
func (b *builder) checkReservedTypeName(scope *Scope, name, kind string, line, col int) {
	if strings.Contains(name, ".") {
		return
	}
	if scope == nil || scope.Parent == nil {
		return
	}
	if existing := scope.Parent.Lookup(name); existing == nil {
		return
	}
	article, remedy := reservedNameRemedy(kind, name)
	b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
		Line: line,
		Col:  col,
		Message: fmt.Sprintf(
			"type name '%s' is reserved by the language and cannot be redeclared as %s %s; %s",
			name, article, kind, remedy,
		),
	})
}

// reservedNameRemedy picks the article and the actionable advice for a
// reserved-name diagnostic. The distinct-type escape hatch is real for the
// data kinds and meaningless for an interface — `type MyDisplay Display`
// does not wrap a contract — so pointing an interface author at it would be
// advice that cannot be followed.
func reservedNameRemedy(kind, name string) (article, remedy string) {
	if kind == "interface" {
		return "an", fmt.Sprintf("pick a different name (e.g. `My%s`)", name)
	}
	return "a", fmt.Sprintf("use a distinct type (`type My%s %s`) for a domain-specific variant", name, name)
}

func splitQualifiedDeclName(name string) (string, string, bool) {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[:idx], name[idx+1:], true
	}
	return "", name, false
}

// declaresHere reports whether sym is a declaration of the file this builder
// analyzes, rather than one it reaches through its scope's parents (the
// prelude) or an import.
func (b *builder) declaresHere(sym *Symbol) bool {
	return sym != nil && b.file.Definitions[sym.Pos] == sym
}

func (b *builder) defineQualifiedTypeMember(scope *Scope, sym *Symbol, line, col int) {
	if scope == nil || sym == nil {
		return
	}
	parentName, memberName, ok := splitQualifiedDeclName(sym.Name)
	if !ok {
		return
	}
	parent := scope.Lookup(parentName)
	if parent == nil {
		// Nothing to hang the name off, and nothing that needs hanging: the
		// declaration is already an ordinary symbol under its full
		// dotted name, and imports drill by that name rather than through
		// this map. Requiring the prefix to resolve treated one name as two,
		// which is what made `pub enum Probe.Reading` above an unrelated
		// a synthesized file API object named `Probe` an error for no
		// reason a reader could see.
		return
	}
	real := parent
	if real.Resolved != nil {
		real = real.Resolved
	}
	if !b.declaresHere(real) {
		// A parent this file does not declare (a prelude type, an imported
		// one) is not this file's to extend: it belongs to another file's
		// analysis, and a stdlib one to every check in the process. The
		// dotted declaration stays an ordinary symbol under its full name.
		return
	}
	switch real.Kind {
	case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
		if real.Members == nil {
			real.Members = make(map[string]*Symbol)
		}
		if prior := real.Members[memberName]; prior != nil {
			b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
				Line:    line,
				Col:     col,
				Message: fmt.Sprintf("duplicate qualified member '%s.%s' (first declared at line %d)", parentName, memberName, prior.Pos.Line),
			})
			return
		}
		real.Members[memberName] = sym
		if real.ModuleScope != nil {
			real.ModuleScope.Symbols[memberName] = sym
		}
	default:
		b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
			Line:    line,
			Col:     col,
			Message: fmt.Sprintf("qualified type declaration '%s' requires '%s' to be a type, interface, or module", sym.Name, parentName),
		})
	}
}

// FileLoader loads AST nodes for a user module given the project root and module path segments.
type FileLoader func(projectRoot string, modulePath []string) ([]ast.Node, error)

// BuildFileWithStdlib builds analysis with a prelude/parent scope and stdlib module scopes.
// The `primitives` parameter is historical naming; today it holds the scope produced by
// loading `stdlib/prelude.nomi`, whose imports define every name a user file gets implicitly.
// Pass `nil` for files that should not receive the prelude (stdlib modules themselves).
func BuildFileWithStdlib(nodes []ast.Node, primitives *Scope, modules map[string]*Scope, projectRoot string, loader FileLoader) *FileAnalysis {
	return buildFileWithStdlibAtPath(nodes, primitives, modules, projectRoot, loader, "")
}

func BuildFileWithStdlibAtPath(nodes []ast.Node, primitives *Scope, modules map[string]*Scope, projectRoot string, loader FileLoader, filePath string) *FileAnalysis {
	return buildFileWithStdlibAtPath(nodes, primitives, modules, projectRoot, loader, filePath)
}

func buildFileWithStdlibAtPath(nodes []ast.Node, primitives *Scope, modules map[string]*Scope, projectRoot string, loader FileLoader, filePath string) *FileAnalysis {
	moduleScope := NewScope(primitives) // prelude/parent scope (can be nil)
	modSyms := make(map[string]*Symbol)
	// Clone `modules` into a per-file copy so import-alias mutations (adding
	// an alias key, removing the original) stay local to this file's builder
	// and don't leak into the shared stdlib map.
	localModules := make(map[string]*Scope, len(modules))
	// Separate copy for FileAnalysis.StdlibModuleScopes: localModules is
	// mutated by import-alias handling, and the FA field must keep naming
	// the defining scopes under their canonical module names.
	stdlibScopes := make(map[string]*Scope, len(modules))
	for name, scope := range modules {
		modSyms[name] = &Symbol{
			Name: name,
			Kind: SymbolModule,
			Pos:  Pos{Line: 1, Col: 1},
		}
		localModules[name] = scope
		stdlibScopes[name] = scope
	}
	b := &builder{
		file: &FileAnalysis{
			ModuleScope:        moduleScope,
			FilePath:           filePath,
			Origin:             originForStandalonePath(filePath),
			References:         make(map[Pos]*Symbol),
			Definitions:        make(map[Pos]*Symbol),
			ContextType:        findContextType(modules),
			StdlibModuleScopes: stdlibScopes,
		},
		modules:     localModules,
		moduleSyms:  modSyms,
		loader:      loader,
		projectRoot: projectRoot,
		moduleAlias: moduleAliasForFile("", filePath),
		cache:       make(map[string]*FileAnalysis),
		loading:     make(map[string]bool),
	}
	b.buildModule(nodes, moduleScope)
	return b.file
}

type builder struct {
	file              *FileAnalysis
	modules           map[string]*Scope        // module name -> scope (stdlib + resolved user modules)
	moduleSyms        map[string]*Symbol       // module name -> synthetic symbol for go-to-def
	imported          map[string]*Scope        // module name -> scope, populated only by explicit user imports (or the prelude)
	blockImports      map[*ast.ImportStmt]bool // imports at the top of a block, which bind only inside it
	loader            FileLoader
	projectRoot       string
	currentModuleName string                   // [module].name from nomi.toml; "" when no manifest
	moduleIndex       ModuleIndex              // cross-module short-name -> filesystem root; nil/empty when no deps
	cache             map[string]*FileAnalysis // shared cache for resolved modules
	loading           map[string]bool          // cycle detection: modules currently being loaded
	moduleAlias       string                   // implicit file import alias (usually the file basename)
	// stdlibFAs is the stdlib FA map handed in by the caller (typically
	// std.Load()'s StdLib.Files, keyed by short name — "int", "list", ...).
	// Threaded through BuildProject(WithCache) for the stdlib-globals
	// retirement so resolveStdlibImport's b.modules fast-path can write
	// stdlib FAs through to b.cache on first reference (under key
	// "std/<short-name>"). After write-through, the post-Sweep
	// proj.Files = cache ∪ entry carries stdlib FAs uniformly with user-
	// module FAs, which the project-level impl index consumes.
	stdlibFAs map[string]*FileAnalysis

	// fileScope is the module-level scope for the file being built.
	// Used by the const-shadow check to know where the file boundary
	// is when walking up the scope chain (so prelude/builtins aren't
	// counted as shadowable). Set on entry to buildModule.
	fileScope *Scope

	// implOverrides tracks which (typeName, ifaceName, fnName) triples
	// have been claimed by `impl Iface for T { fn fnName }` blocks in this file.
	// Populated by recordInterfaceImpl as each block stub is defined and
	// consumed by registerImplDefaults, which registers any interface default
	// methods NOT in this set.
	//
	// It is file-wide rather than per-block because several `impl Iface for T`
	// blocks in one file can collectively claim methods of the same interface;
	// the override set has to span all of them before defaults are registered.
	implOverrides map[string]map[string]map[string]bool

	// inherentTypeMethods marks the file.TypeMethods slots whose incumbent was
	// written by an INHERENT `impl T { ... }` block, so defineImplBlockStub can
	// apply inherent-beats-interface-impl precedence at the moment it writes.
	//
	// Bookkeeping rather than a rival index: nothing looks a method up through
	// it. It exists because the flag that would answer the same question,
	// Symbol.IsImplMethod, is stamped in the LATER annotations pass, so at
	// registration time every symbol looks inherent. Reading the flag here
	// instead is how the precedence silently became arrival order — see
	// defineImplBlockStub.
	inherentTypeMethods map[typeMethodSlot]bool
	// paramPatternOf is the function, lambda or method whose destructured
	// parameter definePattern is binding, and nil otherwise (Symbol.ParamOf).
	paramPatternOf ast.Node
	// patternBodyOf is the body an arm or condition pattern definePattern is
	// binding scopes over, and nil otherwise (Symbol.PatternBody).
	patternBodyOf ast.Node
}

// typeMethodSlot is one (receiver, method) key of FileAnalysis.TypeMethods.
type typeMethodSlot struct {
	recv, name string
}

// validateInlineOpaqueDecls walks all top-level declarations and validates
// any node carrying an `Opaque == true` flag set inline by the parser
// (`pub opaque type ...` or `opaque type ...`). The parser preserves the
// keyword on whatever node it produced — TypeDef for distinct types,
// StructDef for brace-bodied structs, EnumDef for sum types — so this
// function is the single point where the rule "opaque applies to any
// distinct type with a representation" is enforced.
//
// Valid: TypeDef with non-nil InnerTypeExpr (primitive-distinct form),
// StructDef (inline struct body — equivalent to wrapping an unnamed
// struct), EnumDef (inline enum body — equivalent to wrapping an unnamed
// sum type). For struct/enum bodies, the Opaque flag is propagated to
// the symbol (and onward to StructType.Opaque / EnumType.Opaque) by
// defineStructStub / defineEnumStub, so this function only needs to
// reject the lone invalid case.
//
// Invalid: TypeDef with nil InnerTypeExpr (zero-sized distinct, no
// representation to hide). The parser already rejects `opaque` on fn /
// once / interface / typealias / extern by only accepting OPAQUE before
// TYPE, so those cannot reach this point.
func (b *builder) validateInlineOpaqueDecls(nodes []ast.Node) {
	for _, node := range nodes {
		td, ok := node.(*ast.TypeDef)
		if !ok || !td.Opaque {
			continue
		}
		if td.InnerTypeExpr != nil {
			// Valid opaque distinct type. The TypeDef's Public flag is
			// independent of opacity — private opaque types are permitted
			// (opaque is about the construction surface, not export
			// visibility).
			continue
		}
		b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
			Line:    td.Line,
			Col:     td.Col,
			Message: fmt.Sprintf("cannot declare '%s' as opaque — zero-sized distinct types have no representation to hide", td.Name),
		})
	}
}

// importerModRel returns the importing file's module-relative path
// (slash-separated, no .nomi extension) — the form
// checkInternalAccess expects. Derived from b.file.FilePath against
// b.projectRoot, so:
//   - the project entry file (FilePath empty by convention) yields ""
//     and is treated as module-root for the parent-subtree rule;
//   - a sibling file at <projectRoot>/tools/seed.nomi yields
//     "tools/seed";
//   - any file whose FilePath doesn't sit under projectRoot (raw
//     BuildFile callers / tests) yields "".
func (b *builder) importerModRel() string {
	if b.file == nil || b.file.FilePath == "" || b.projectRoot == "" {
		return ""
	}
	prefix := b.projectRoot + string(filepath.Separator)
	if !strings.HasPrefix(b.file.FilePath, prefix) {
		return ""
	}
	rel := strings.TrimSuffix(strings.TrimPrefix(b.file.FilePath, prefix), ".nomi")
	// importeeModRel is always "/"-joined (importsOf walks segments and
	// joins with "/"). On Windows, b.file.FilePath uses backslashes — normalize
	// here so the two sides compare correctly in checkInternalAccess.
	return filepath.ToSlash(rel)
}

// resolveImport attempts to resolve a user module import via the loader.
// Returns the module's scope and the resolved file path, or nil if unresolved.
//
// Cross-module routing: when modulePath's head matches a sibling-module
// short-name in b.moduleIndex, the load target is rerouted to that
// module's filesystem root with the rest-of-path. A self-name head
// (matches the current module's manifest name) is stripped and treated
// as an intra-module import from the project root. A `std/...` path is
// not resolved here; resolveStdlibImport owns it.
//
// The cache key follows the user-facing import path (strings.Join of
// modulePath as written) so two different sites importing the same
// module via the same spelling share one cache entry. The
// self-name-stripping case keys under the bare rest-of-path so a
// self-named import and a bare-named one of the same module
// deduplicate.
func (b *builder) resolveImport(modulePath []string) (*Scope, string) {
	scope, path, _ := b.resolveImportOrMiss(modulePath)
	return scope, path
}

// resolveImportOrMiss is resolveImport that also says, when it finds no
// module, that the loader had no file for the path: the miss is non-nil only
// then. A path the loader is still loading (an import cycle) and a builder
// with no loader are not misses.
func (b *builder) resolveImportOrMiss(modulePath []string) (*Scope, string, *importMiss) {
	if b.loader == nil {
		return nil, "", nil
	}
	modulePath = b.stdlibSiblingPath(modulePath)
	loadRoot := b.projectRoot
	loadPath := modulePath
	cacheKey := strings.Join(modulePath, "/")
	dep := ""
	if len(modulePath) > 0 {
		head := modulePath[0]
		if rest, self := SelfNameImport(modulePath, b.currentModuleName); self {
			// Self-name reference. Strip the head and resolve against
			// the current root; the cache key follows the stripped
			// form so it dedups with bare-name imports of the same
			// module.
			loadPath = rest
			cacheKey = strings.Join(loadPath, "/")
		} else if otherRoot, ok := b.moduleIndex[head]; ok {
			// Cross-module reference: route to the dep's root.
			loadRoot = otherRoot
			loadPath = modulePath[1:]
			dep = head
		}
	}
	if len(modulePath) == 1 && modulePath[0] == "std" {
		dep = "std"
		loadPath = nil
	}
	if len(loadPath) == 0 {
		if dep != "" {
			// The path is a module's short name and nothing below it:
			// `import std`, or `import std.io`, which selects `io` from a
			// file `std`. A module is a directory, so there is no file.
			return nil, "", &importMiss{root: loadRoot, dep: dep, std: dep == "std"}
		}
		return nil, "", nil
	}
	filePath := ResolveModulePath(loadRoot, loadPath)

	// Check cache first
	if cached, ok := b.cache[cacheKey]; ok {
		mergeImpls(b.file, cached)
		mergeImplManifest(b.file, cached)
		mergeTypeMethods(b.file, cached, filePath)
		return cached.ModuleScope, filePath, nil
	}

	// Cycle detection
	if b.loading[cacheKey] {
		return nil, "", nil
	}
	b.loading[cacheKey] = true
	defer func() { delete(b.loading, cacheKey) }()

	nodes, err := b.loader(loadRoot, loadPath)
	if err != nil {
		return nil, "", &importMiss{root: loadRoot, path: loadPath, dep: dep, std: modulePath[0] == "std"}
	}
	if nodes == nil {
		return nil, "", nil
	}

	// Create a sub-builder sharing cache and loading sets. The sub-
	// builder inherits the entry's moduleIndex/currentModuleName: this
	// matches the rule that the entry's go.mod is the
	// single source of cross-module truth (siblings don't get their
	// own per-file index). For files inside a sibling module, that
	// means transitive intra-sibling imports work via the sibling's
	// own root (loadRoot here) AND any short-name in the entry's
	// index keeps resolving — the documented out-of-scope is a
	// sibling reaching a dep that's only in the sibling's go.mod.
	moduleScope := NewScope(nil)
	sub := &builder{
		file: &FileAnalysis{
			ModuleScope: moduleScope,
			References:  make(map[Pos]*Symbol),
			Definitions: make(map[Pos]*Symbol),
			FilePath:    filePath,
			Origin:      originForKey(cacheKey),
		},
		modules:           make(map[string]*Scope),
		moduleSyms:        make(map[string]*Symbol),
		loader:            b.loader,
		projectRoot:       loadRoot,
		moduleAlias:       moduleAliasForFile(cacheKey, filePath),
		currentModuleName: b.currentModuleName,
		moduleIndex:       b.moduleIndex,
		cache:             b.cache,
		loading:           b.loading,
	}
	// Lower derive declarations BEFORE BuildTypes so any synthesized impl blocks
	// get FuncTypes attached consistently. buildModule lowers its own local copy
	// internally (idempotent), but BuildTypes runs on the nodes handed here, so
	// both passes need the same lowered nodes.
	loweredNodes, lowerErrs := LowerDerives(nodes)
	sub.file.TypeErrors = append(sub.file.TypeErrors, lowerErrs...)
	sub.buildModule(loweredNodes, moduleScope)
	BuildTypes(sub.file, loweredNodes)
	b.cache[cacheKey] = sub.file
	mergeImpls(b.file, sub.file)
	mergeImplManifest(b.file, sub.file)
	mergeTypeMethods(b.file, sub.file, filePath)
	return moduleScope, filePath, nil
}

// mergeImpls copies the source file's Impls table into the destination's,
// so an importing file sees the impls declared in any imported file.
// Without this, the type checker rejects assignments like
// `Effects { logger: ProdLogger {} }` whenever the impl
// `impl Logger for ProdLogger` lives in a different file from the
// Effects struct definition. Stdlib impls reach files the same way
// (post-stdlib-as-package they flow through the regular import path),
// supplemented by the project-level ImplIndex for impls the importing
// file never imported directly. Idempotent — repeated calls leave the
// dst table unchanged.
func mergeImpls(dst, src *FileAnalysis) {
	if dst == nil || src == nil || src.Impls == nil {
		return
	}
	if dst.Impls == nil {
		dst.Impls = make(map[string]map[string]bool)
	}
	for typeName, ifaces := range src.Impls {
		if dst.Impls[typeName] == nil {
			dst.Impls[typeName] = make(map[string]bool)
		}
		for ifaceName := range ifaces {
			dst.Impls[typeName][ifaceName] = true
		}
	}
}

// mergeTypeMethods copies an imported USER module's own public impl methods
// into the importing file's per-file TypeMethods table, so single-file analysis
// (the LSP path, where ProjectImpls is nil) resolves a type-qualified call to
// an imported user type's interface method.
// It is the per-file sibling of mergeImpls, scoped narrowly to keep go-to-def
// correct:
//
//   - Stdlib modules are skipped. Their type-qualified methods already resolve
//     in single-file mode via the References table / prebuilt scopes, and
//     go-to-def routes stdlib symbols through the embedded-stdlib FileURI
//     machinery — which requires SourceFile to stay empty. Merging them would
//     inject a competing TypeMethods entry that mis-attributes go-to-def
//     (e.g. `List.head` → lists.nomi).
//   - Only a module's OWN methods are merged — SourceFile empty, or already
//     equal to srcPath — not ones it transitively merged from its own imports
//     (those reach the importer through its own direct import of the defining
//     module). This keeps first-wins from picking a non-authoritative source.
//   - A merged symbol's SourceFile is stamped with srcPath when empty, so
//     cross-file go-to-def lands in the defining user file.
//
// Idempotent; an existing entry (the importing file's own method) is not
// overwritten, matching mergeImpls.
func mergeTypeMethods(dst, src *FileAnalysis, srcPath string) {
	if dst == nil || src == nil || src.TypeMethods == nil || srcPath == "" {
		return
	}
	if _, std := stdlibModuleForPath(srcPath); std {
		return
	}
	for typeName, byName := range src.TypeMethods {
		for method, sym := range byName {
			if sym == nil {
				continue
			}
			// Module-private methods stay callable only within their defining
			// module. Project-wide analysis applies the same public-only filter
			// when it builds ProjectImpls.TypeMethods.
			if !sym.Public {
				continue
			}
			// Non-transitive: skip a method the source itself merged from
			// elsewhere (its SourceFile names a different file).
			if sym.SourceFile != "" && sym.SourceFile != srcPath {
				continue
			}
			if sym.SourceFile == "" {
				sym.SourceFile = srcPath
			}
			if dst.TypeMethods == nil {
				dst.TypeMethods = make(map[string]map[string]*Symbol)
			}
			if dst.TypeMethods[typeName] == nil {
				dst.TypeMethods[typeName] = make(map[string]*Symbol)
			}
			if _, present := dst.TypeMethods[typeName][method]; !present {
				dst.TypeMethods[typeName][method] = sym
			}
		}
	}
}

// MergeImplManifest is the exported alias of mergeImplManifest, used by
// internal/frontend's Checker.Analyze and the stdcompiler package to fold each
// sibling's manifest (populated by per-sibling CheckTypes runs) into
// the entry's manifest. The export contract is intentionally narrow:
// merge ImplManifest only — no error fold-in, no Impls fold-in (those
// already happen inside BuildProject's sweeps).
func MergeImplManifest(dst, src *FileAnalysis) { mergeImplManifest(dst, src) }

// mergeImplManifest copies the source file's ImplManifest into the
// destination's, unioning Recording slices on collision and
// deduplicating by full Recording equality. Used during cross-file
// analysis the same way mergeImpls is used to fold static "type
// implements interface" facts. Idempotent — re-merging the same src
// leaves dst unchanged.
func mergeImplManifest(dst, src *FileAnalysis) {
	if dst == nil || src == nil {
		return
	}
	// Type-method provider demands union independently of ImplManifest — a
	// file may use cross-module type-promoted methods without any interface
	// dispatch (so src.ImplManifest could be nil while TypeMethodModules isn't).
	if src.TypeMethodModules != nil {
		if dst.TypeMethodModules == nil {
			dst.TypeMethodModules = make(map[string]bool)
		}
		for path := range src.TypeMethodModules {
			dst.TypeMethodModules[path] = true
		}
	}
	if src.ImplManifest == nil {
		return
	}
	if dst.ImplManifest == nil {
		dst.ImplManifest = make(map[string]map[string][]Recording)
	}
	for ifaceName, types := range src.ImplManifest {
		if dst.ImplManifest[ifaceName] == nil {
			dst.ImplManifest[ifaceName] = make(map[string][]Recording)
		}
		for typeName, recs := range types {
			existing := dst.ImplManifest[ifaceName][typeName]
			for _, rec := range recs {
				dup := false
				for _, prev := range existing {
					if prev == rec {
						dup = true
						break
					}
				}
				if !dup {
					existing = append(existing, rec)
				}
			}
			dst.ImplManifest[ifaceName][typeName] = existing
		}
	}
}

// stdlibSiblingPath is QualifyIntraStdlibImport bound to the file this
// builder is building. A stdlib source file names its siblings bare — they are
// the same Nomi module — and every module key in the toolchain spells the
// stdlib `std/<name>`, so the three resolution entry points below qualify
// before they answer.
//
// The path is qualified INSIDE those three and not at modPathStrings, because
// defineImport indexes n.ModulePath by the length of a slice derived from it
// (the drill-through owner walk, `n.ModulePath[len(modulePath)+i]`) and puts
// the same slice into user-facing diagnostics. A prepended segment would shift
// the first and read wrong in the second.
func (b *builder) stdlibSiblingPath(modulePath []string) []string {
	if b.file == nil {
		return modulePath
	}
	return QualifyIntraStdlibImport(b.file.Origin, modulePath)
}

// isStdlibImport checks if an import path starts with "std" and the module
// is already loaded in the builder's modules map.
func (b *builder) isStdlibImport(modulePath []string) bool {
	modulePath = b.stdlibSiblingPath(modulePath)
	if len(modulePath) < 2 || modulePath[0] != "std" {
		return false
	}
	relPath := strings.Join(modulePath[1:], "/")
	if _, ok := b.modules[relPath]; ok {
		return true
	}
	name := modulePath[len(modulePath)-1]
	_, ok := b.modules[name]
	return ok
}

// resolveStdlibImport returns the stdlib module scope for a "std/X" import path,
// or nil if this is not a stdlib import.
//
// Write-through to b.cache: when the b.modules fast-path hits, the
// corresponding stdlib FA is also written under key "std/<name>" so the
// project-level impl index assembled in BuildProjectWithCache sees stdlib FAs
// uniformly with user-module FAs, including for programs that use only
// prelude-implicit names (no explicit `import std/X`): every stdlib import,
// explicit or prelude-implicit, populates cache.
func (b *builder) resolveStdlibImport(modulePath []string) *Scope {
	modulePath = b.stdlibSiblingPath(modulePath)
	if len(modulePath) < 2 || modulePath[0] != "std" {
		return nil
	}
	key := strings.Join(modulePath, "/")
	relPath := strings.Join(modulePath[1:], "/")
	name := modulePath[len(modulePath)-1]

	if fa := b.cache[key]; fa != nil && fa.ModuleScope != nil {
		return fa.ModuleScope
	}
	if fa := b.stdlibFAs[relPath]; fa != nil && fa.ModuleScope != nil {
		if b.cache != nil {
			b.cache[key] = fa
		}
		return fa.ModuleScope
	}
	if relPath != name {
		if fa := b.stdlibFAs[name]; fa != nil && fa.ModuleScope != nil {
			if b.cache != nil {
				b.cache[key] = fa
			}
			return fa.ModuleScope
		}
	}

	// Legacy single-file callers may only pass the short stdlib module map.
	// Keep that path, but consult it last so an explicit `std/io` import cannot
	// be shadowed by a project-local `io.nomi` that happens to occupy the same
	// short key in the mixed module map.
	if scope, ok := b.modules[relPath]; ok {
		return scope
	}
	scope, ok := b.modules[name]
	if !ok {
		return nil
	}
	return scope
}

// nodeCol extracts the Col from an *Ident or *TypeIdent node.
func (b *builder) nodeCol(n ast.Node) int {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Col
	case *ast.TypeIdent:
		return v.Col
	default:
		return 1
	}
}

// TypeExprBaseName returns the identity name for a type expression, stripping
// lowercase import qualifiers and generic parameters. Uppercase-leading
// qualified names are nominal API members (`Probe.Reading`) and keep their
// qualifier as part of the type identity.
//
//	SimpleType("Iter")                  -> "Iter"
//	GenericType("Iter", [T])            -> "Iter"
//	QualifiedType("users", SimpleType "U")      -> "U"
//	QualifiedType("Probe", SimpleType "Reading") -> "Probe.Reading"
//
// Exported so internal/irbuild, internal/frontend and the LSP can share this
// single implementation rather than maintain a parallel copy.
func TypeExprBaseName(te ast.TypeExpr) string {
	switch t := te.(type) {
	case *ast.SimpleType:
		return t.Name
	case *ast.GenericType:
		return t.Name
	case *ast.QualifiedType:
		if ast.IsPublic(t.Module) {
			// The base name, so `Probe.Reading<Int>` identifies the same
			// declaration as `Probe.Reading`. TypeString() bakes the type
			// arguments into the string, which is not a name anything is
			// registered under — the doc above promises generic parameters
			// are stripped, and only the undotted branches were keeping it.
			return t.Module + "." + TypeExprBaseName(t.Member)
		}
		return TypeExprBaseName(t.Member)
	}
	return ""
}

func (b *builder) buildModule(nodes []ast.Node, scope *Scope) {
	// Track the file-scope boundary so the const-shadow check knows
	// where to stop walking up the parent chain (prelude/builtins are
	// above the file scope and may always be shadowed).
	b.fileScope = scope
	// Lowering desugars source derive declarations into top-level impl blocks.
	// It must run BEFORE SynthesizeDerives / SynthesizeUniversalDebug (both
	// scan top-level impl blocks).
	// Idempotent —
	// BuildProjectWithCache lowers project files before this path sees
	// them; lowering consumes the decls' Items, so a second pass no-ops.
	// Errors are internal parser-regression guards only.
	nodes, lowerErrs := LowerDerives(nodes)
	b.file.TypeErrors = append(b.file.TypeErrors, lowerErrs...)
	// `derive` synthesis runs before symbol registration so the synthesized
	// impl-block nodes are visible to defineSymbolStub /
	// defineImplBlockStub like hand-written impls. Validation errors
	// (unknown protocol, duplicate derive Foo) surface here as TypeErrors;
	// the per-protocol synthesizers emit the impl bodies.
	nodes = b.SynthesizeDerives(nodes)
	// Universal default Debug: after derive synthesis, eagerly synthesize an
	// `impl Debug` block for every declared type that lacks ANY explicit Debug
	// (impl or derive). Opaque types get a name-only `<opaque T>` body;
	// every other type gets the structural derive body. This makes Debug a
	// universal interface — `io.inspect(x)` works for any value with no
	// `@derive`/explicit-impl required — and is what lets `where T: Debug` bounds drop.
	// Runs per-file (same shape as SynthesizeDerives) so the orphan rule
	// attributes each auto-impl to its receiver type's home module. The
	// "skip if explicit" guard inside the pass enforces precedence and keeps
	// detectImplCollisions quiet.
	//
	// Unconditional and import-independent: the synthesized blocks' `impl
	// Debug` headers resolve via the compiler-known route in
	// defineImplBlockAnnotations, so synthesis works in any file regardless
	// of imports — stdlib files (no prelude) and bare BuildFile inputs (no
	// stdlib at all) included.
	nodes = b.SynthesizeUniversalDebug(nodes)
	// First pass: define all top-level names. Per-node stub+annotations is
	// retained here (rather than splitting across the whole file) because
	// the import-collision check (collidesWithImportedModule, run in each
	// declaration's stub) needs the imports above the declaration to have
	// completed their annotation pass (defineImport is annotation-side).
	// The project-wide orchestrator (BuildProject) handles cross-file
	// ordering differently, by pre-creating every file's ModuleScope
	// before any stubs run.
	//
	// EXCEPTION — impl-block annotations are DEFERRED to a follow-up loop.
	// defineImplBlockAnnotations is the one pass-1 step that hard-errors on
	// a name not yet stubbed (`impl block: undefined interface`), so running
	// it per-node makes `impl X for Y` order-sensitive against a later
	// `interface X` in the same file — for hand-written top-level blocks and
	// for synthesized blocks lowered out of type bodies alike.
	// Declaration order must never matter (the analyzer is two-phase /
	// whole-program; BuildProject's all-stubs-first sweeps already get this
	// right), so impl block annotation walking runs only after every declaration in the
	// file has its stub. Nothing else in pass 1 consumes what they produce
	// (Impls / DispatchNames / visibility stamping are read by
	// registerImplDefaults and later passes, all below this loop).
	for _, node := range nodes {
		if _, isImpl := node.(*ast.ImplBlock); isImpl {
			b.defineSymbolStub(node, scope)
			continue
		}
		b.defineTopLevel(node, scope)
	}
	b.defineImplicitSelfModule(scope)
	for _, node := range nodes {
		if _, isImpl := node.(*ast.ImplBlock); isImpl {
			b.defineSymbolAnnotations(node, scope)
		}
	}
	// Interface default-method registration runs after every impl block in the
	// file has been defined. It needs the file-wide override set to know which
	// interface defaults are genuinely unmet across all impl blocks for a
	// (type, iface).
	b.registerImplDefaults(scope)
	// derive field/payload bound check (spec §38.1, *Field-type
	// requirements*). Runs AFTER
	// defineTopLevel so b.file.Impls is fully populated; the (T, Iface)
	// entries for the @derive'd types themselves are also registered by
	// now (defineImplBlockStub on the synthesizer's emitted
	// impl block), which lets recursive and mutually-recursive types
	// pass without special-casing. Note: BuildFileWithStdlib (single-file
	// callers, e.g. LSP raw-document analysis, white-box tests) reaches
	// CheckDeriveBounds with fa.ProjectImpls == nil — the guard inside
	// CheckDeriveBounds short-circuits in that case so primitive-typed
	// fields aren't falsely flagged.
	b.file.TypeErrors = append(b.file.TypeErrors, CheckDeriveBounds(b.file, nodes)...)
	// Validate inline `pub opaque type ...` / `opaque type ...`
	// declarations — the parser preserves the keyword on whatever node it
	// produced (TypeDef / StructDef / EnumDef); this is where the rule
	// "opaque only applies to distinct types with a representation" gets
	// enforced.
	b.validateInlineOpaqueDecls(nodes)
	// Second pass: walk bodies for inner scopes and references.
	b.buildModuleBodies(nodes, scope)
	// Unused checks: every use kind is recorded in fa.References by the
	// passes above (annotations, impl headers, bounds, @derive args,
	// bodies), and CheckTypes hasn't yet replaced any entry with proxy
	// copies — so this is the one correct point on the single-file path.
	// Project builds run the same checks as a sweep in buildProjectWithCache.
	b.file.TypeErrors = append(b.file.TypeErrors, CheckUnusedImports(b.file, nodes)...)
	b.file.TypeErrors = append(b.file.TypeErrors, CheckRedundantPreludeImports(b.file, nodes)...)
	b.file.TypeErrors = append(b.file.TypeErrors, CheckUnusedBindings(b.file)...)
	b.file.TypeErrors = append(b.file.TypeErrors, CheckUselessReturns(nodes)...)
	b.file.TypeErrors = append(b.file.TypeErrors, CheckRepeatedFields(nodes)...)
}

func moduleAliasForFile(key, filePath string) string {
	if key != "" {
		return lastSegment(key)
	}
	if filePath == "" {
		return ""
	}
	base := filepath.Base(filePath)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

func (b *builder) defineImplicitSelfModule(scope *Scope) {
	if b == nil || scope == nil || b.moduleAlias == "" {
		return
	}
	if scope.LookupLocal(b.moduleAlias) != nil {
		return
	}
	sym := &Symbol{
		Name:        b.moduleAlias,
		Kind:        SymbolModule,
		Pos:         Pos{Line: 1, Col: 1},
		ModuleScope: scope,
	}
	scope.Define(sym)
	if b.moduleSyms == nil {
		b.moduleSyms = make(map[string]*Symbol)
	}
	b.moduleSyms[b.moduleAlias] = sym
}

// buildModuleBodies runs the second pass of buildModule — walking
// function bodies, resolving inner scopes, and recording references in
// expressions. Bindings are defined sequentially in this pass (not in
// pass 1) so each binding is only visible to statements after it.
//
// Assumes pass 1 (defineSymbolStub + defineSymbolAnnotations) has already
// run for every file in the project.
func (b *builder) buildModuleBodies(nodes []ast.Node, scope *Scope) {
	for _, node := range nodes {
		b.walkAttachedTests(node, scope)
		switch s := node.(type) {
		case *ast.OnceBinding:
			b.walkNode(s.Value, scope)

		case *ast.Binding:
			if s.TypeAnnotation != nil {
				b.walkTypeExpr(s.TypeAnnotation, scope)
			}
			b.walkNode(s.Value, scope)
			sym := &Symbol{
				Name: s.Name,
				Kind: SymbolBinding,
				Pos:  Pos{Line: s.Line, Col: s.Col},
				Node: s,
			}
			if !ast.IsDiscardName(s.Name) {
				if !b.checkRedeclareInScope(scope, s.Name, s.Line, s.Col) {
					scope.Define(sym)
				}
			}
			b.file.Definitions[sym.Pos] = sym

		case *ast.StructDestructure:
			b.walkNode(s.Value, scope)
			for _, f := range s.Fields {
				if f.Binding != "" {
					line := structPatternFieldBindingLine(f, s.Line)
					sym := &Symbol{
						Name: f.Binding,
						Kind: SymbolBinding,
						Pos:  Pos{Line: line, Col: f.BindingCol},
						Node: s,
					}
					if !ast.IsDiscardName(f.Binding) {
						scope.Define(sym)
					}
					b.file.Definitions[sym.Pos] = sym
				}
			}

		case *ast.PatternDestructure:
			b.walkNode(s.Value, scope)
			b.definePatternRoot(s.Pattern, scope, s)

		case *ast.PatternBinding:
			b.walkPatternBinding(s, scope)

		case *ast.TupleDestructure:
			b.walkNode(s.Value, scope)
			for _, binding := range s.Bindings {
				if binding != nil {
					sym := &Symbol{
						Name: binding.Name,
						Kind: SymbolBinding,
						Pos:  Pos{Line: binding.Line, Col: binding.Col},
						Node: s,
					}
					if !ast.IsDiscardName(binding.Name) {
						scope.Define(sym)
					}
					b.file.Definitions[sym.Pos] = sym
				}
			}

		case *ast.MapDestructure:
			b.walkNode(s.Value, scope)
			for _, entry := range s.Entries {
				b.walkNode(entry.Key, scope)
			}
			for _, entry := range s.Entries {
				if ip, ok := entry.Pattern.(*ast.IdentPattern); ok {
					sym := &Symbol{
						Name: ip.Name,
						Kind: SymbolBinding,
						Pos:  Pos{Line: ip.Line, Col: ip.Col},
						Node: s,
					}
					if !ast.IsDiscardName(ip.Name) {
						scope.Define(sym)
					}
					b.file.Definitions[sym.Pos] = sym
				}
			}

		case *ast.DistinctDestructure:
			// Register a Reference at the type-name position (`Id` in
			// `Id(unwrapped) = id`) so hover shows the distinct type.
			if sym := scope.Lookup(s.TypeName); sym != nil {
				b.file.References[Pos{Line: s.Line, Col: s.Col}] = sym
			}
			b.walkNode(s.Value, scope)
			if s.Binding != nil {
				sym := &Symbol{
					Name: s.Binding.Name,
					Kind: SymbolBinding,
					Pos:  Pos{Line: s.Binding.Line, Col: s.Binding.Col},
					Node: s,
				}
				if !ast.IsDiscardName(s.Binding.Name) {
					scope.Define(sym)
				}
				b.file.Definitions[sym.Pos] = sym
			}

		case *ast.ImportStmt:
			// Already fully handled by defineTopLevel in pass 1. Skip here so
			// walkNode's nested-import case (which also calls defineImport)
			// doesn't double-register at the top level.

		case *ast.ImportBlock:
			// Same: handled by defineTopLevel in pass 1.

		default:
			b.walkNode(node, scope)
		}
	}
}

func (b *builder) walkTypeExpr(te ast.TypeExpr, scope *Scope) {
	if te == nil {
		return
	}
	// Synthesized impls (explicit @derive and universal-default Debug) carry
	// positions in the synthLineBase band. Their type references — e.g. the
	// `User` receiver in a synthesized `impl Debug for User { fn inspect(value: User) }` —
	// are not navigable source, so they must not enter the LSP
	// References/Definitions maps; otherwise rename / find-references emit
	// bogus edits at fabricated positions (and every type gets such an impl
	// under universal Debug). Type-checking of synth bodies happens via the
	// checker, not this LSP-facing walk, so skipping the whole subtree is safe.
	if IsSynthesizedLine(te.LineNum()) {
		return
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		if sym := scope.Lookup(t.Name); sym != nil {
			b.file.References[Pos{Line: t.Line, Col: t.Col}] = sym
		}
	case *ast.QualifiedType:
		// A dotted type name is one declaration, not a traversal into its
		// prefix: `Probe.Reading` is its own symbol sitting beside `Probe`,
		// and `Probe` may have no members to reach through. Resolving
		// the whole name first is what lets both halves of the span point at
		// it — otherwise hovering `Probe` describes a different symbol and
		// hovering `Reading` describes nothing, because the branches below
		// look for a file API object to drill into and find neither. Only an
		// upper-case prefix makes a dotted name: under a lower-case one
		// (a module) TypeExprBaseName is the member alone, and `ror.HttpError`
		// would resolve to this file's own `HttpError`.
		if sym := scope.Lookup(TypeExprBaseName(t)); sym != nil && ast.IsPublic(t.Module) {
			b.file.References[Pos{Line: t.ModuleLine, Col: t.ModuleCol}] = sym
			switch m := t.Member.(type) {
			case *ast.SimpleType:
				b.file.References[Pos{Line: m.Line, Col: m.Col}] = sym
			case *ast.GenericType:
				b.file.References[Pos{Line: m.Line, Col: m.Col}] = sym
				for _, param := range m.Params {
					b.walkTypeExpr(param, scope)
				}
			}
			return
		}
		// Register reference to the module name
		if sym := scope.Lookup(t.Module); sym != nil {
			b.file.References[Pos{Line: t.ModuleLine, Col: t.ModuleCol}] = sym
		}
		// Look up the member in the qualified owner scope. That owner may
		// be a real imported file (`std/calendar`) or a synthesized stdlib
		// file API object.
		modScope := b.lookupModuleScope(t.Module, scope)
		if modScope == nil {
			if stdScope, ok := b.modules[t.Module]; ok {
				modScope = stdScope
			}
		}
		if modScope != nil {
			switch m := t.Member.(type) {
			case *ast.SimpleType:
				if sym := modScope.LookupLocal(m.Name); sym != nil {
					b.file.References[Pos{Line: m.Line, Col: m.Col}] = sym
				}
			case *ast.GenericType:
				if sym := modScope.LookupLocal(m.Name); sym != nil {
					b.file.References[Pos{Line: m.Line, Col: m.Col}] = sym
				}
				// Walk generic params in current scope (they're local types, not module types)
				for _, param := range m.Params {
					b.walkTypeExpr(param, scope)
				}
			}
		} else if enumSym := scope.Lookup(t.Module); enumSym != nil && enumSym.Kind == SymbolEnum {
			// Enum-qualified variant: Shape.Circle in patterns,
			// Expr.Let in struct-literal type position.
			b.file.References[Pos{Line: t.ModuleLine, Col: t.ModuleCol}] = enumSym
			if m, ok := t.Member.(*ast.SimpleType); ok {
				var variantSym *Symbol
				if vs := scope.Lookup(m.Name); vs != nil && vs.Kind == SymbolEnumVariant {
					variantSym = vs
				} else {
					// Selectively-imported enum: variants reachable only through
					// the enum's Members map (or its Resolved alias's).
					real := enumSym
					if real.Resolved != nil {
						real = real.Resolved
					}
					if vs, ok := real.Members[m.Name]; ok && vs.Kind == SymbolEnumVariant {
						variantSym = vs
					}
				}
				if variantSym != nil {
					b.file.References[Pos{Line: m.Line, Col: m.Col}] = variantSym
				}
			}
		}
	case *ast.GenericType:
		if sym := scope.Lookup(t.Name); sym != nil {
			b.file.References[Pos{Line: t.Line, Col: t.Col}] = sym
		}
		for _, param := range t.Params {
			b.walkTypeExpr(param, scope)
		}
	case *ast.FuncType:
		for _, param := range t.Params {
			b.walkTypeExpr(param, scope)
		}
		b.walkTypeExpr(t.Return, scope)
	case *ast.SelfType:
		if sym := scope.Lookup("self"); sym != nil {
			b.file.References[Pos{Line: t.Line, Col: t.Col}] = sym
		}
	case *ast.AnonStructType:
		// Walk each field's type to register references inside it (e.g.,
		// `String` → std.string symbol). The field NAMES are registered
		// later, in checker phase, by ResolveTypeExpr's AnonStructType
		// branch — see analysis/type_resolver.go. That mirrors how
		// checkStructLit registers self-referential SymbolField entries
		// for anon struct *literals*: field names own their own Pos and
		// Type because the literal/type position is the declaration site.
		for _, f := range t.Fields {
			b.walkTypeExpr(f.TypeAnnotation, scope)
		}
	}
}

func (b *builder) recordImportedTypeQualifiedAccess(n *ast.FieldAccess, scope *Scope) {
	obj, ok := n.Object.(*ast.TypeIdent)
	if !ok {
		return
	}
	typeSym := b.lookupImportedTypeSymbol(obj.Name, scope)
	if typeSym == nil {
		return
	}
	b.file.References[Pos{Line: obj.Line, Col: obj.Col}] = typeSym

	real := typeSym
	for real.Resolved != nil {
		real = real.Resolved
	}
	if real.Members != nil {
		if member := real.Members[n.Field.Name]; member != nil {
			b.file.References[Pos{Line: n.Field.Line, Col: n.Field.Col}] = symbolWithCallReceiver(member, obj.Name)
			return
		}
	}
	if method := b.file.LookupTypeMethod(real.Name, n.Field.Name); method != nil {
		b.file.References[Pos{Line: n.Field.Line, Col: n.Field.Col}] = symbolWithCallReceiver(method, obj.Name)
	}
}

func symbolWithCallReceiver(sym *Symbol, receiver string) *Symbol {
	if sym == nil || receiver == "" {
		return sym
	}
	proxy := *sym
	proxy.CallReceiver = receiver
	return &proxy
}

func (b *builder) recordImportedTypeRefs(te ast.TypeExpr, scope *Scope) {
	if te == nil || IsSynthesizedLine(te.LineNum()) {
		return
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		if sym := b.lookupImportedTypeSymbol(t.Name, scope); sym != nil {
			b.file.References[Pos{Line: t.Line, Col: t.Col}] = sym
		}
	case *ast.GenericType:
		if sym := b.lookupImportedTypeSymbol(t.Name, scope); sym != nil {
			b.file.References[Pos{Line: t.Line, Col: t.Col}] = sym
		}
		for _, param := range t.Params {
			b.recordImportedTypeRefs(param, scope)
		}
	case *ast.FuncType:
		for _, param := range t.Params {
			b.recordImportedTypeRefs(param, scope)
		}
		b.recordImportedTypeRefs(t.Return, scope)
	case *ast.AnonStructType:
		for _, f := range t.Fields {
			b.recordImportedTypeRefs(f.TypeAnnotation, scope)
		}
	case *ast.QualifiedType:
		switch member := t.Member.(type) {
		case *ast.GenericType:
			for _, param := range member.Params {
				b.recordImportedTypeRefs(param, scope)
			}
		}
	}
}

func (b *builder) lookupImportedTypeSymbol(name string, scope *Scope) *Symbol {
	// Enum variants are ordinary scope symbols, so a variant named the same as
	// its payload type (`variant String String`) can shadow an imported type
	// binding in the general reference recorder. For known type-annotation
	// sites, fall back through import definitions so unused-import and
	// go-to-def see the same type reference the checker sees. An import at
	// the top of a block binds only inside that block, so it answers only
	// where scope sees it.
	for _, sym := range b.file.Definitions {
		if sym == nil || sym.Name != name || !isTypeRefSymbol(sym) {
			continue
		}
		imp, ok := sym.Node.(*ast.ImportStmt)
		if !ok {
			continue
		}
		if b.blockImports[imp] && !scopeDefines(scope, sym) {
			continue
		}
		return sym
	}
	return nil
}

// scopeDefines reports that sym is defined in scope or an enclosing scope.
func scopeDefines(scope *Scope, sym *Symbol) bool {
	for s := scope; s != nil; s = s.Parent {
		if s.Symbols[sym.Name] == sym {
			return true
		}
	}
	return false
}

func isTypeRefSymbol(sym *Symbol) bool {
	if sym == nil {
		return false
	}
	real := sym
	for real.Resolved != nil {
		real = real.Resolved
	}
	switch real.Kind {
	case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
		return true
	default:
		return false
	}
}

// defineTypeParams creates a child scope with type parameters defined as symbols.
// Returns the child scope (or the original scope if there are no type params).
//
// Type params are defined before any `where` bounds are walked by callers. That
// lets a bound's interface resolve through the parent fallback while sibling
// params inside the bound (`U`) resolve in the child scope for hover/go-to-def.
func (b *builder) defineTypeParams(typeParams []ast.TypeParam, parent *Scope) *Scope {
	if len(typeParams) == 0 {
		return parent
	}
	child := NewScope(parent)
	for _, tp := range typeParams {
		sym := &Symbol{
			Name: tp.Name,
			Kind: SymbolType,
			Pos:  Pos{Line: tp.Line, Col: tp.Col},
		}
		child.Define(sym)
		b.file.Definitions[sym.Pos] = sym
	}
	for _, tp := range typeParams {
		sym := child.LookupLocal(tp.Name)
		if sym == nil {
			continue
		}
		for _, bound := range tp.Bounds {
			if bn := TypeExprBaseName(bound); bn != "" {
				sym.TypeParamBounds = append(sym.TypeParamBounds, bn)
			}
			b.walkTypeExpr(bound, child)
		}
	}
	return child
}

func mergeImplScopeTypeParams(inferred []ast.TypeParam, explicit []ast.TypeParam) []ast.TypeParam {
	if len(inferred) == 0 {
		return explicit
	}
	if len(explicit) == 0 {
		return inferred
	}
	out := make([]ast.TypeParam, 0, len(inferred)+len(explicit))
	index := map[string]int{}
	for _, tp := range inferred {
		index[tp.Name] = len(out)
		out = append(out, tp)
	}
	for _, tp := range explicit {
		if i, ok := index[tp.Name]; ok {
			merged := tp
			merged.Bounds = append(append([]ast.TypeExpr{}, inferred[i].Bounds...), tp.Bounds...)
			out[i] = merged
			continue
		}
		index[tp.Name] = len(out)
		out = append(out, tp)
	}
	return out
}

func inferReceiverTypeParams(receiver ast.TypeExpr, scope *Scope) []ast.TypeParam {
	if receiver == nil || scope == nil {
		return nil
	}
	args := receiverGenericArgs(receiver)
	if len(args) == 0 {
		return nil
	}
	declParams, declWhere := receiverDeclTypeParamsAndWhere(receiver, scope)
	if len(declParams) == 0 {
		return nil
	}
	subNames := map[string]string{}
	out := make([]ast.TypeParam, 0, len(args))
	outIndex := map[string]int{}
	for i, arg := range args {
		st, ok := arg.(*ast.SimpleType)
		if !ok || st.Name == "" || scope.Lookup(st.Name) != nil || i >= len(declParams) {
			continue
		}
		subNames[declParams[i].Name] = st.Name
		tp := ast.TypeParam{Name: st.Name, Line: st.Line, Col: st.Col}
		for _, bound := range declParams[i].Bounds {
			tp.Bounds = append(tp.Bounds, substituteTypeExprNames(bound, subNames))
		}
		outIndex[declParams[i].Name] = len(out)
		out = append(out, tp)
	}
	for _, wc := range declWhere {
		i, ok := outIndex[wc.Name]
		if !ok {
			continue
		}
		for _, bound := range wc.Bounds {
			out[i].Bounds = append(out[i].Bounds, substituteTypeExprNames(bound, subNames))
		}
	}
	return out
}

func receiverGenericArgs(receiver ast.TypeExpr) []ast.TypeExpr {
	switch t := receiver.(type) {
	case *ast.GenericType:
		return t.Params
	case *ast.QualifiedType:
		return receiverGenericArgs(t.Member)
	default:
		return nil
	}
}

func receiverDeclTypeParamsAndWhere(receiver ast.TypeExpr, scope *Scope) ([]ast.TypeParam, []ast.WhereConstraint) {
	name := TypeExprBaseName(receiver)
	if name == "" {
		return nil, nil
	}
	sym := scope.Lookup(name)
	if sym == nil {
		return nil, nil
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	switch n := sym.Node.(type) {
	case *ast.StructDef:
		return n.TypeParams, n.WhereClauses
	case *ast.EnumDef:
		return n.TypeParams, n.WhereClauses
	case *ast.InterfaceDef:
		return n.TypeParams, n.WhereClauses
	case *ast.ExternType:
		return n.TypeParams, n.WhereClauses
	default:
		return nil, nil
	}
}

func substituteTypeExprNames(expr ast.TypeExpr, names map[string]string) ast.TypeExpr {
	if expr == nil || len(names) == 0 {
		return expr
	}
	switch t := expr.(type) {
	case *ast.SimpleType:
		if name, ok := names[t.Name]; ok {
			cp := *t
			cp.Name = name
			return &cp
		}
	case *ast.GenericType:
		cp := *t
		cp.Params = make([]ast.TypeExpr, len(t.Params))
		for i, p := range t.Params {
			cp.Params[i] = substituteTypeExprNames(p, names)
		}
		return &cp
	case *ast.QualifiedType:
		cp := *t
		cp.Member = substituteTypeExprNames(t.Member, names)
		return &cp
	case *ast.FuncType:
		cp := *t
		cp.Params = make([]ast.TypeExpr, len(t.Params))
		for i, p := range t.Params {
			cp.Params[i] = substituteTypeExprNames(p, names)
		}
		cp.Return = substituteTypeExprNames(t.Return, names)
		return &cp
	case *ast.AnonStructType:
		cp := *t
		cp.Fields = make([]ast.StructField, len(t.Fields))
		for i, f := range t.Fields {
			cp.Fields[i] = f
			cp.Fields[i].TypeAnnotation = substituteTypeExprNames(f.TypeAnnotation, names)
		}
		return &cp
	}
	return expr
}

// defineInterfaceMethodSymbols fills iface.Members with one Symbol per
// declared method, and registers each method's params so hovering a param
// name inside the interface declaration shows `name: Type`. Params of a
// default impl also need to be in the body's child scope; walkNode does that
// separately.
//
// ONE copy, called from defineInterfaceStub and defineInterface. It was two
// byte-identical copies, and that is exactly how `Doc` came to be missing: a
// field on a method symbol had to be written twice and was written zero
// times, so a `///` above a method inside an interface body reached nothing.
// Four std methods had one — Struct.update, Iter.known_count,
// Steppable.step_by, Discrete.steps_between — and all four rendered as a bare
// signature on hover and on their reference page.
func (b *builder) defineInterfaceMethodSymbols(n *ast.InterfaceDef, iface *Symbol) {
	for i := range n.Methods {
		m := &n.Methods[i]
		methodSym := &Symbol{
			Name:            m.Name,
			Kind:            SymbolInterfaceMethod,
			Pos:             Pos{Line: m.Line, Col: m.Col},
			Doc:             m.Doc,
			Public:          n.Public,
			Node:            m,
			OwningInterface: n.Name,
		}
		iface.Members[m.Name] = methodSym
		b.file.Definitions[methodSym.Pos] = methodSym
		for _, p := range m.Params {
			if p.Line <= 0 {
				continue
			}
			ppos := Pos{Line: p.Line, Col: p.Col}
			b.file.Definitions[ppos] = &Symbol{
				Name: p.Name,
				Kind: SymbolParam,
				Pos:  ppos,
				Node: m,
			}
		}
	}
}

// defineInterface registers an InterfaceDef's symbol (with method member
// symbols) in the given scope and walks each method's param/return type
// expressions in a type-param-extended inner scope. Returns the inner
// scope so callers can chain default-impl body scopes off it. Does NOT
// walk default-impl method bodies — callers handle that separately
// (top-level via pass 2's default-walk; walkNode inline). Idempotent on
// Definitions[pos]. Both top-level and nested interface decls reuse this.
func (b *builder) defineInterface(n *ast.InterfaceDef, scope *Scope) *Scope {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; !alreadyDefined {
		sym := &Symbol{
			Name:    n.Name,
			Kind:    SymbolInterface,
			Pos:     pos,
			Public:  n.Public,
			Doc:     n.Doc,
			Node:    n,
			Members: make(map[string]*Symbol),
		}
		b.defineInterfaceMethodSymbols(n, sym)
		if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
			scope.Define(sym)
		}
		b.file.Definitions[pos] = sym
	}
	inner := b.defineTypeParams(n.TypeParams, scope)
	// Define `self` in the method-signature scope so `self` in type position
	// (`fn hello(value: self): String`) records a hover / go-to-def reference
	// via walkTypeExpr's SelfType case. It resolves to the interface itself —
	// `self` here stands for "any implementor of this interface." Mirrors the
	// impl-block `self` symbol, which resolves to the concrete receiver.
	if ifaceSym, ok := b.file.Definitions[pos]; ok {
		inner.Define(&Symbol{
			Name:     "self",
			Kind:     ifaceSym.Kind,
			Pos:      pos,
			Node:     n,
			Resolved: ifaceSym,
		})
	}
	for _, m := range n.Methods {
		methodInner := b.defineTypeParams(m.TypeParams, inner)
		for _, p := range m.Params {
			b.walkTypeExpr(p.TypeAnnotation, methodInner)
		}
		b.walkTypeExpr(m.ReturnTypeExpr, methodInner)
		b.walkWhereClauses(m.WhereClauses, methodInner)
	}
	return inner
}

// defineTypeDef registers a TypeDef's symbol in the given scope and walks
// the inner type expression. Idempotent on Definitions[pos]. Both top-level
// and nested distinct-type decls reuse this.
func (b *builder) defineTypeDef(n *ast.TypeDef, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; !alreadyDefined {
		sym := &Symbol{
			Name:   n.Name,
			Kind:   SymbolType,
			Pos:    pos,
			Public: n.Public,
			Doc:    n.Doc,
			Node:   n,
		}
		if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
			scope.Define(sym)
		}
		b.file.Definitions[pos] = sym
	}
	b.walkTypeExpr(n.InnerTypeExpr, scope)
	b.walkNamespaceItems(n.Items, scope)
}

// defineTypeAlias registers a TypeAlias's symbol in the given scope and
// walks the target type expression. Idempotent on Definitions[pos].
// Both top-level and nested type-alias decls reuse this.
func (b *builder) defineTypeAlias(n *ast.TypeAlias, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; !alreadyDefined {
		sym := &Symbol{
			Name:   n.Name,
			Kind:   SymbolTypeAlias,
			Pos:    pos,
			Public: n.Public,
			Doc:    n.Doc,
			Node:   n,
		}
		if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
			scope.Define(sym)
		}
		b.file.Definitions[pos] = sym
	}
	// For a bound alias (`typealias Foo A and B`), walk every entry in
	// the bound list so each interface name registers a Reference. Without
	// this, hover and go-to-def don't work on `Showable`/`Tagged` inside
	// the alias declaration.
	if len(n.Bounds) > 0 {
		for _, bound := range n.Bounds {
			b.walkTypeExpr(bound, scope)
		}
	} else {
		b.walkTypeExpr(n.TargetTypeExpr, scope)
	}
}

// defineEnum registers an EnumDef's symbol and one variant symbol per
// variant in the given scope, then walks variant data/field types and
// defaults in a type-param-extended inner scope. Variants are top-level
// names in the module's scope (not nested under the enum). Idempotent on
// Definitions[pos]. Both top-level and nested enum decls reuse this.
func (b *builder) defineEnum(n *ast.EnumDef, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	enumSym := b.file.Definitions[pos]
	if enumSym == nil {
		enumSym = &Symbol{
			Name:    n.Name,
			Kind:    SymbolEnum,
			Pos:     pos,
			Public:  n.Public,
			Doc:     n.Doc,
			Node:    n,
			Members: make(map[string]*Symbol),
		}
		if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
			scope.Define(enumSym)
		}
		b.file.Definitions[pos] = enumSym
	} else if enumSym.Members == nil {
		enumSym.Members = make(map[string]*Symbol)
	}
	inner := b.defineTypeParams(n.TypeParams, scope)

	// Enum variants are defined as separate symbols in the enclosing scope.
	for _, v := range n.Variants {
		vpos := Pos{Line: v.Line, Col: v.Col}
		if vpos.Line == 0 {
			vpos = Pos{Line: n.Line, Col: n.Col}
		}
		// Walk embedded type expr BEFORE defining the variant symbol —
		// embedded variants share the same name as the type they embed; if
		// we define first, scope.Lookup finds the variant instead of the type.
		b.walkTypeExpr(v.EmbeddedTypeExpr, inner)

		if _, alreadyDefined := b.file.Definitions[vpos]; !alreadyDefined {
			vsym := &Symbol{
				Name:   v.Name,
				Kind:   SymbolEnumVariant,
				Pos:    vpos,
				Public: n.Public,
				Doc:    n.Doc,
				Node:   n,
			}
			if len(v.Fields) > 0 {
				vsym.Members = make(map[string]*Symbol)
				for _, f := range v.Fields {
					fieldSym := &Symbol{
						Name: f.Name,
						Kind: SymbolField,
						Pos:  Pos{Line: f.Line, Col: f.Col},
						Doc:  f.Doc,
					}
					vsym.Members[f.Name] = fieldSym
					b.file.Definitions[fieldSym.Pos] = fieldSym
				}
			}
			// Embed variants share their name with the underlying type
			// they wrap (struct or distinct). Defining the variant in scope
			// would shadow the type's bare-name entry, so bare uses like
			// `Circle{radius: 5.0}` or `UserId("alice")` would resolve to
			// the variant constructor instead of the type itself. Keep the
			// variant reachable only through qualified access via
			// `enumSym.Members[v.Name]`; bare patterns inside `case` are
			// resolved by scrutinee type rather than scope, so they still
			// dispatch correctly.
			if v.Kind != "embedded" && variantMayTakeScopeSlot(scope, v.Name) {
				scope.Define(vsym)
			}
			b.file.Definitions[vsym.Pos] = vsym
			// Also expose the variant via the enum's Members so qualified
			// access (`Error.HttpError`) can resolve through the enum
			// without doing its own scope lookup.
			enumSym.Members[v.Name] = vsym
		}

		b.walkTypeExpr(v.DataTypeExpr, inner)
		b.recordImportedTypeRefs(v.DataTypeExpr, inner)
		for _, f := range v.Fields {
			b.walkTypeExpr(f.TypeAnnotation, inner)
			b.recordImportedTypeRefs(f.TypeAnnotation, inner)
			if f.Default != nil {
				b.walkNode(f.Default, inner)
			}
		}
	}
	b.walkNamespaceItems(n.Items, inner)
}

// defineStruct registers a StructDef's symbol (with field member symbols)
// in the given scope and walks field type annotations + defaults in a
// type-param-extended inner scope. Idempotent on Definitions[pos]. Both
// top-level and nested struct decls reuse this.
func (b *builder) defineStruct(n *ast.StructDef, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; !alreadyDefined {
		sym := &Symbol{
			Name:    n.Name,
			Kind:    SymbolStruct,
			Pos:     pos,
			Public:  n.Public,
			Doc:     n.Doc,
			Node:    n,
			Members: make(map[string]*Symbol),
		}
		for _, f := range n.Fields {
			fieldSym := &Symbol{
				Name: f.Name,
				Kind: SymbolField,
				Pos:  Pos{Line: f.Line, Col: f.Col},
				Doc:  f.Doc,
			}
			sym.Members[f.Name] = fieldSym
			b.file.Definitions[fieldSym.Pos] = fieldSym
		}
		if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
			scope.Define(sym)
		}
		b.file.Definitions[pos] = sym
	}
	inner := b.defineTypeParams(n.TypeParams, scope)
	for _, f := range n.Fields {
		b.walkTypeExpr(f.TypeAnnotation, inner)
		if f.Default != nil {
			b.walkNode(f.Default, inner)
		}
	}
	b.walkNamespaceItems(n.Items, inner)
}

// defineOnce registers a OnceBinding's symbol and marks it public when
// the parser set n.Public from an inline `pub` modifier.
func (b *builder) defineOnce(n *ast.OnceBinding, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; alreadyDefined {
		return
	}
	sym := &Symbol{
		Name:   n.Name,
		Kind:   SymbolOnce,
		Pos:    pos,
		Public: n.Public,
		Doc:    n.Doc,
		Node:   n,
	}
	b.collidesWithImportedModule(scope, n.Name, n.Line, n.Col)
	if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
		scope.Define(sym)
	}
	if n.Public {
		b.defineInterfaceModuleMethods(sym, scope)
	}
	b.file.Definitions[pos] = sym
}

// defineFunc registers a FuncDef's symbol in the given scope and walks its
// param + return type expressions in a type-param-extended inner scope.
// Returns the inner scope (with type params defined) so callers can chain a
// body scope off it. Does NOT walk the function body — callers handle that
// separately (defineTopLevel relies on pass 2's default-walk; walkNode
// handles the body inline for nested fns). Idempotent on Definitions[pos]
// so it's safe to call from both paths without double-registration.
func (b *builder) defineFunc(n *ast.FuncDef, scope *Scope) *Scope {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; !alreadyDefined {
		sym := &Symbol{
			Name:   n.Name,
			Kind:   SymbolFunction,
			Pos:    pos,
			Public: n.Public,
			Doc:    n.Doc,
			Node:   n,
		}
		if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
			scope.Define(sym)
		}
		b.file.Definitions[pos] = sym
	}
	inner := b.defineTypeParams(n.TypeParams, scope)
	for _, p := range n.Params {
		b.walkTypeExpr(p.TypeAnnotation, inner)
	}
	b.walkTypeExpr(n.ReturnTypeExpr, inner)
	return inner
}

// defineTopLevel runs the two passes back-to-back so existing per-node
// behavior is preserved. The two halves are exposed as separate methods so
// the project-wide orchestrator can run defineSymbolStub across every file
// before any file's defineSymbolAnnotations sweep — that's what makes
// cross-file type cycles resolvable.
func (b *builder) defineTopLevel(node ast.Node, scope *Scope) {
	b.defineSymbolStub(node, scope)
	b.defineSymbolAnnotations(node, scope)
}

// defineSymbolStub registers the top-level symbol(s) for `node` in `scope`
// without walking the node's type annotations. Companion to
// defineSymbolAnnotations, which walks the annotations and records
// cross-module References in a separate pass.
//
// Pure registration; no scope.Lookup of names that depend on imports
// having already been resolved (those lookups belong in the Annotations
// pass). The ImplBlock case is stub-pure too: interface resolution and
// visibility stamping live in defineImplBlockAnnotations, which buildModule
// defers until every declaration in the file has its stub (so an impl
// block before its interface resolves order-independently).
func (b *builder) defineSymbolStub(node ast.Node, scope *Scope) {
	switch n := node.(type) {
	case *ast.FuncDef:
		b.defineFuncStub(n, scope)

	case *ast.StructDef:
		b.defineStructStub(n, scope)

	case *ast.EnumDef:
		b.defineEnumStub(n, scope)

	case *ast.TypeDef:
		b.defineTypeDefStub(n, scope)

	case *ast.TypeAlias:
		b.defineTypeAliasStub(n, scope)

	case *ast.InterfaceDef:
		b.defineInterfaceStub(n, scope)

	case *ast.ExternFunc:
		sym := &Symbol{
			Name:   n.Name,
			Kind:   SymbolFunction,
			Pos:    Pos{Line: n.Line, Col: n.Col},
			Public: n.Public,
			Doc:    n.Doc,
			Node:   n,
		}
		applyNodeDefinitionOverride(sym, n)
		if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
			scope.Define(sym)
		}
		b.file.Definitions[sym.Pos] = sym

	case *ast.ExternType:
		sourceFile := ""
		if n.Opaque {
			sourceFile = b.file.FilePath
		}
		sym := &Symbol{
			Name:       n.Name,
			Kind:       SymbolType,
			Pos:        Pos{Line: n.Line, Col: n.Col},
			Public:     n.Public,
			Opaque:     n.Opaque,
			Doc:        n.Doc,
			Node:       n,
			SourceFile: sourceFile,
			Members:    make(map[string]*Symbol),
		}
		applyNodeDefinitionOverride(sym, n)
		if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
			scope.Define(sym)
		}
		b.defineQualifiedTypeMember(scope, sym, n.Line, n.Col)
		b.file.Definitions[sym.Pos] = sym

	case *ast.OnceBinding:
		b.defineOnce(n, scope)

	case *ast.ImplBlock:
		b.defineImplBlockStub(n, scope)

	case *ast.ImportStmt:
		// Imports are tangled — the recursive load triggered by resolveImport
		// is what populates b.modules and the imported module's symbols. The
		// stub-side cannot register the local alias binding without that
		// resolution because the alias symbol carries the resolved
		// ModuleScope, so the entire body stays in defineSymbolAnnotations.

	case *ast.ImportBlock:
		// Same rationale as ImportStmt — handled in defineSymbolAnnotations.

	case *ast.ExternPackage:
		pos := Pos{Line: n.AliasLine, Col: n.AliasCol}
		sym := &Symbol{
			Name: n.Alias,
			Kind: SymbolModule,
			Pos:  pos,
			Node: n,
		}
		if !b.checkRedeclareInScope(scope, n.Alias, n.AliasLine, n.AliasCol) {
			scope.Define(sym)
		}
		b.file.Definitions[pos] = sym

	case *ast.Binding:
		// Bindings are NOT defined in pass 1 — they need sequential visibility
		// (each binding is only visible to statements after it). Handled in
		// buildModule's second pass instead.
	}
}

// defineSymbolAnnotations walks `node`'s type annotations and records
// cross-file References. Assumes defineSymbolStub has already run for this
// node (and, in the project-wide orchestrator, for every other top-level
// declaration that this node's annotations might reference).
func (b *builder) defineSymbolAnnotations(node ast.Node, scope *Scope) {
	switch n := node.(type) {
	case *ast.FuncDef:
		b.defineFuncAnnotations(n, scope)

	case *ast.StructDef:
		b.defineStructAnnotations(n, scope)
		// Internal synthetic derive markers on the struct register references
		// for each interface name so editor go-to-def works from the source
		// derive declaration.
		b.walkTypeDeclDecoratorArgs(n.Decorators, scope)

	case *ast.EnumDef:
		b.defineEnumAnnotations(n, scope)
		b.walkTypeDeclDecoratorArgs(n.Decorators, scope)

	case *ast.TypeDef:
		b.walkTypeExpr(n.InnerTypeExpr, scope)
		b.walkTypeDeclDecoratorArgs(n.Decorators, scope)

	case *ast.TypeAlias:
		// For a bound alias (`typealias Foo A and B`), walk every entry in
		// the bound list so each interface name registers a Reference.
		if len(n.Bounds) > 0 {
			for _, bound := range n.Bounds {
				b.walkTypeExpr(bound, scope)
			}
		} else {
			b.walkTypeExpr(n.TargetTypeExpr, scope)
		}

	case *ast.InterfaceDef:
		b.defineInterfaceAnnotations(n, scope)

	case *ast.ExternFunc:
		b.defineForeignBindingRefs(n, scope)
		inner := b.defineTypeParams(n.TypeParams, scope)
		for _, p := range n.Params {
			b.walkTypeExpr(p.TypeAnnotation, inner)
		}
		b.walkTypeExpr(n.ReturnTypeExpr, inner)
		b.walkWhereClauses(n.WhereClauses, inner)

	case *ast.ExternType:
		b.defineForeignBindingRefs(n, scope)
		// Internal synthetic derive markers are the only annotations an extern
		// type declaration carries.
		inner := b.defineTypeParams(n.TypeParams, scope)
		b.walkWhereClauses(n.WhereClauses, inner)
		b.walkTypeDeclDecoratorArgs(n.Decorators, scope)

	case *ast.OnceBinding:
		// Walk the type annotation so its type identifiers are tracked as references.
		b.walkTypeExpr(n.TypeAnnotation, scope)

	case *ast.ImplBlock:
		// A synthesized block's header errors name the declaration it
		// was synthesized for, not its synth-band position.
		from := len(b.file.TypeErrors)
		b.defineImplBlockAnnotations(n, scope)
		repointSynthErrors(n, b.file.TypeErrors[from:])

	case *ast.ImportStmt:
		b.defineImport(n, scope, false /* nested */)

	case *ast.ImportBlock:
		// Block-form import: each entry is a regular ImportStmt. Process
		// each like a standalone import — they're semantically identical;
		// the block is just syntactic sugar for grouping.
		for _, entry := range n.Entries {
			b.defineImport(entry, scope, false)
		}

	case *ast.Binding:
		// Bindings are NOT defined in pass 1 — handled in buildModule's
		// second pass.
	}
}

// defineFuncStub registers the FuncDef's symbol without walking its
// type-param declarations, where clauses, or param/return type expressions. Idempotent on
// Definitions[pos] so it's safe to call from both pass-1 paths without
// double-registration.
func (b *builder) defineFuncStub(n *ast.FuncDef, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; alreadyDefined {
		return
	}
	// A top-level FuncDef is always a plain module function — interface impl
	// methods live inside `impl` blocks (defineImplBlock), not here — so it
	// is never an impl method and takes the ordinary redeclare check.
	sym := &Symbol{
		Name:   n.Name,
		Kind:   SymbolFunction,
		Pos:    pos,
		Public: n.Public,
		Doc:    n.Doc,
		Node:   n,
	}
	b.collidesWithImportedModule(scope, n.Name, n.Line, n.Col)
	if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
		scope.Define(sym)
	}
	b.file.Definitions[pos] = sym
}

// defineFuncAnnotations walks the FuncDef's type-param declarations, where
// clauses, and param/return type expressions in a type-param-extended inner scope.
// Mirrors the walk-only half of defineFunc.
func (b *builder) defineFuncAnnotations(n *ast.FuncDef, scope *Scope) {
	inner := b.defineTypeParams(n.TypeParams, scope)
	for _, p := range n.Params {
		b.walkTypeExpr(p.TypeAnnotation, inner)
	}
	b.walkTypeExpr(n.ReturnTypeExpr, inner)
	b.walkWhereClauses(n.WhereClauses, inner)
}

func (b *builder) walkWhereClauses(clauses []ast.WhereConstraint, scope *Scope) {
	for _, wc := range clauses {
		if sym := scope.Lookup(wc.Name); sym != nil {
			b.file.References[Pos{Line: wc.Line, Col: wc.Col}] = sym
			for _, bound := range wc.Bounds {
				if bn := TypeExprBaseName(bound); bn != "" && !stringSliceContains(sym.TypeParamBounds, bn) {
					sym.TypeParamBounds = append(sym.TypeParamBounds, bn)
				}
			}
		}
		for _, bound := range wc.Bounds {
			b.walkTypeExpr(bound, scope)
		}
	}
}

func stringSliceContains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

// defineStructStub registers the StructDef's symbol with field member
// symbols. Does not walk type-param declarations, where clauses, or field type annotations.
//
// Opacity propagation: if the parser saw `opaque type ... { ... }` it
// set n.Opaque on the StructDef. Mirror that flag onto the symbol so
// downstream consumers (StructType.Opaque via type_builder, boundary
// checks for field access / construction outside the owning module) see
// it. The TypeDef counterpart of this is in defineTypeDefStub.
func (b *builder) defineStructStub(n *ast.StructDef, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; alreadyDefined {
		return
	}
	b.checkReservedTypeName(scope, n.Name, "struct", n.Line, n.Col)
	sourceFile := ""
	if n.Opaque {
		sourceFile = b.file.FilePath
	}
	sym := &Symbol{
		Name:       n.Name,
		Kind:       SymbolStruct,
		Pos:        pos,
		Public:     n.Public,
		Opaque:     n.Opaque,
		Doc:        n.Doc,
		Node:       n,
		SourceFile: sourceFile,
		Members:    make(map[string]*Symbol),
	}
	for _, f := range n.Fields {
		fieldSym := &Symbol{
			Name: f.Name,
			Kind: SymbolField,
			Pos:  Pos{Line: f.Line, Col: f.Col},
			Doc:  f.Doc,
		}
		sym.Members[f.Name] = fieldSym
		b.file.Definitions[fieldSym.Pos] = fieldSym
	}
	b.collidesWithImportedModule(scope, n.Name, n.Line, n.Col)
	if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
		scope.Define(sym)
	}
	b.file.Definitions[pos] = sym
}

func applyNodeDefinitionOverride(sym *Symbol, node ast.Node) {
	if sym == nil || node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.ExternFunc:
		applyDefinitionOverride(sym, n.DefinitionFile, n.DefinitionLine, n.DefinitionCol, n.DefinitionSpan)
	case *ast.ExternType:
		applyDefinitionOverride(sym, n.DefinitionFile, n.DefinitionLine, n.DefinitionCol, n.DefinitionSpan)
	}
}

func applyDefinitionOverride(sym *Symbol, file string, line, col, span int) {
	if sym == nil || file == "" || line <= 0 || col <= 0 {
		return
	}
	sym.DefinitionFile = file
	sym.DefinitionLine = line
	sym.DefinitionCol = col
	sym.DefinitionSpan = span
}

func (b *builder) defineForeignBindingRefs(node ast.Node, scope *Scope) {
	var alias string
	var pos Pos
	switch n := node.(type) {
	case *ast.ExternFunc:
		alias = n.ForeignAlias
		pos = Pos{Line: n.ForeignAliasLine, Col: n.ForeignAliasCol}
	case *ast.ExternType:
		alias = n.ForeignAlias
		pos = Pos{Line: n.ForeignAliasLine, Col: n.ForeignAliasCol}
	default:
		return
	}
	if alias == "" || pos.Line <= 0 || pos.Col <= 0 {
		return
	}
	if sym := scope.Lookup(alias); sym != nil {
		b.file.References[pos] = sym
	}
}

func (b *builder) walkNamespaceItems(items []ast.Node, scope *Scope) {
	for _, item := range items {
		b.walkAttachedTests(item, scope)
		switch n := item.(type) {
		case *ast.FuncDef:
			b.walkNode(n, scope)
		case *ast.ExternFunc:
			// Signature refs were handled in the annotations pass; externs
			// have no body to walk.
		case *ast.OnceBinding:
			if n.Value != nil {
				b.walkNode(n.Value, scope)
			}
		case *ast.ImportStmt, *ast.ImportBlock, *ast.ExternPackage:
			// Imports are file setup for the module scope; annotations
			// already resolved their bindings, and they have no body.
		case *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.TypeAlias, *ast.InterfaceDef, *ast.ImplBlock:
			b.walkNode(n, scope)
		case *ast.TestDecl:
			b.walkNode(n, scope)
		}
	}
}

// defineStructAnnotations walks field type annotations and field defaults
// in a type-param-extended inner scope.
func (b *builder) defineStructAnnotations(n *ast.StructDef, scope *Scope) {
	inner := b.defineTypeParams(n.TypeParams, scope)
	b.walkWhereClauses(n.WhereClauses, inner)
	for _, f := range n.Fields {
		b.walkTypeExpr(f.TypeAnnotation, inner)
		if f.Default != nil {
			b.walkNode(f.Default, inner)
		}
	}
}

// defineEnumStub registers the EnumDef's symbol and one variant symbol per
// variant in the enclosing scope, plus per-variant field symbols. Does not
// walk type-param declarations, where clauses, embedded type expressions, data
// type expressions, or field type annotations / defaults.
func (b *builder) defineEnumStub(n *ast.EnumDef, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	enumSym := b.file.Definitions[pos]
	if enumSym == nil {
		b.checkReservedTypeName(scope, n.Name, "enum", n.Line, n.Col)
		// Opacity propagation: parallel to defineStructStub, mirror n.Opaque
		// onto the enum symbol so EnumType.Opaque (set by type_builder from
		// sym.Opaque) and the destructuring boundary check fire.
		sourceFile := ""
		if n.Opaque {
			sourceFile = b.file.FilePath
		}
		enumSym = &Symbol{
			Name:       n.Name,
			Kind:       SymbolEnum,
			Pos:        pos,
			Public:     n.Public,
			Opaque:     n.Opaque,
			Doc:        n.Doc,
			Node:       n,
			SourceFile: sourceFile,
			Members:    make(map[string]*Symbol),
		}
		b.collidesWithImportedModule(scope, n.Name, n.Line, n.Col)
		if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
			scope.Define(enumSym)
		}
		b.defineQualifiedTypeMember(scope, enumSym, n.Line, n.Col)
		b.file.Definitions[pos] = enumSym
	} else if enumSym.Members == nil {
		enumSym.Members = make(map[string]*Symbol)
	}
	for _, v := range n.Variants {
		vpos := Pos{Line: v.Line, Col: v.Col}
		if vpos.Line == 0 {
			vpos = Pos{Line: n.Line, Col: n.Col}
		}
		if _, alreadyDefined := b.file.Definitions[vpos]; alreadyDefined {
			continue
		}
		vsym := &Symbol{
			Name:   v.Name,
			Kind:   SymbolEnumVariant,
			Pos:    vpos,
			Public: n.Public,
			Doc:    n.Doc,
			Node:   n,
		}
		if len(v.Fields) > 0 {
			vsym.Members = make(map[string]*Symbol)
			for _, f := range v.Fields {
				fieldSym := &Symbol{
					Name: f.Name,
					Kind: SymbolField,
					Pos:  Pos{Line: f.Line, Col: f.Col},
					Doc:  f.Doc,
				}
				vsym.Members[f.Name] = fieldSym
				b.file.Definitions[fieldSym.Pos] = fieldSym
			}
		}
		// Embed variants share their name with the underlying type they
		// wrap (struct or distinct). Defining the variant in scope would
		// shadow the type's bare-name entry, so bare uses like
		// `Circle{radius: 5.0}` or `UserId("alice")` would resolve to the
		// variant constructor instead of the type itself. Keep the variant
		// reachable only through qualified access via
		// `enumSym.Members[v.Name]`; bare patterns inside `case` are
		// resolved by scrutinee type rather than scope, so they still
		// dispatch correctly.
		if v.Kind != "embedded" && variantMayTakeScopeSlot(scope, v.Name) {
			scope.Define(vsym)
		}
		b.file.Definitions[vsym.Pos] = vsym
		// Also expose the variant via the enum's Members so qualified
		// access (`Error.HttpError`) can resolve through the enum without
		// doing its own scope lookup.
		enumSym.Members[v.Name] = vsym
	}
}

// defineEnumAnnotations walks each variant's embedded / data type
// expression and per-field annotations + defaults in a type-param-extended
// inner scope.
func (b *builder) defineEnumAnnotations(n *ast.EnumDef, scope *Scope) {
	inner := b.defineTypeParams(n.TypeParams, scope)
	b.walkWhereClauses(n.WhereClauses, inner)
	for _, v := range n.Variants {
		// Embedded variants are NOT scope.Define'd in the stub pass (their
		// name belongs to the embedded type), so walking the embedded type
		// expression here resolves to the type rather than the variant.
		b.walkTypeExpr(v.EmbeddedTypeExpr, inner)
		b.walkTypeExpr(v.DataTypeExpr, inner)
		b.recordImportedTypeRefs(v.DataTypeExpr, inner)
		for _, f := range v.Fields {
			b.walkTypeExpr(f.TypeAnnotation, inner)
			b.recordImportedTypeRefs(f.TypeAnnotation, inner)
			if f.Default != nil {
				b.walkNode(f.Default, inner)
			}
		}
	}
}

// defineTypeDefStub registers a TypeDef's symbol in scope. Does not walk
// the inner type expression.
//
// Opacity propagation: if the parser saw `opaque type ...` it set
// n.Opaque on the TypeDef. We mirror that flag onto the symbol so
// `pub opaque type Foo Int` exposes its opacity to downstream
// consumers. Validity (must be a distinct type with a representation)
// is enforced separately by validateInlineOpaqueDecls; an invalid
// inline `opaque` would have landed on a StructDef/EnumDef or a
// zero-sized TypeDef and produced a TypeError there. Setting the flag
// here for an invalid TypeDef (zero-sized) is harmless — the symbol
// still carries the validation error and downstream consumers don't
// trust opacity on a malformed type. Struct/enum symbols never get
// this propagation because their stub functions
// (defineStructStub/defineEnumStub) don't pass through here.
func (b *builder) defineTypeDefStub(n *ast.TypeDef, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; alreadyDefined {
		return
	}
	b.checkReservedTypeName(scope, n.Name, "distinct type", n.Line, n.Col)
	sourceFile := ""
	if n.Opaque {
		sourceFile = b.file.FilePath
	}
	sym := &Symbol{
		Name:       n.Name,
		Kind:       SymbolType,
		Pos:        pos,
		Public:     n.Public,
		Opaque:     n.Opaque,
		Doc:        n.Doc,
		Node:       n,
		SourceFile: sourceFile,
		Members:    make(map[string]*Symbol),
	}
	b.collidesWithImportedModule(scope, n.Name, n.Line, n.Col)
	if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
		scope.Define(sym)
	}
	b.defineQualifiedTypeMember(scope, sym, n.Line, n.Col)
	b.file.Definitions[pos] = sym
}

// defineTypeAliasStub registers a TypeAlias's symbol in scope. Does not
// walk bounds or target type expression.
func (b *builder) defineTypeAliasStub(n *ast.TypeAlias, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; alreadyDefined {
		return
	}
	b.checkReservedTypeName(scope, n.Name, "typealias", n.Line, n.Col)
	sym := &Symbol{
		Name:   n.Name,
		Kind:   SymbolTypeAlias,
		Pos:    pos,
		Public: n.Public,
		Doc:    n.Doc,
		Node:   n,
	}
	b.collidesWithImportedModule(scope, n.Name, n.Line, n.Col)
	if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
		scope.Define(sym)
	}
	b.defineQualifiedTypeMember(scope, sym, n.Line, n.Col)
	b.file.Definitions[pos] = sym
}

// defineInterfaceStub registers an InterfaceDef's symbol with method
// member symbols and per-method param Definitions. Does not walk type
// params or method param/return type expressions.
//
// The reserved-name check runs here for the same reason it runs in
// defineStructStub / defineEnumStub / defineTypeDefStub /
// defineTypeAliasStub: a prelude re-export occupies every user file's
// module scope unasked, and Nomi has no syntax that disambiguates a
// local declaration from it. Interfaces were the one declaring kind
// this check never reached — see the doc-comment on
// checkReservedTypeName for the rule and reserved_names_test.go for
// the measured consequences of the gap.
func (b *builder) defineInterfaceStub(n *ast.InterfaceDef, scope *Scope) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, alreadyDefined := b.file.Definitions[pos]; alreadyDefined {
		return
	}
	b.checkReservedTypeName(scope, n.Name, "interface", n.Line, n.Col)
	sym := &Symbol{
		Name:    n.Name,
		Kind:    SymbolInterface,
		Pos:     pos,
		Public:  n.Public,
		Doc:     n.Doc,
		Node:    n,
		Members: make(map[string]*Symbol),
	}
	b.defineInterfaceMethodSymbols(n, sym)
	b.collidesWithImportedModule(scope, n.Name, n.Line, n.Col)
	if !b.checkRedeclareInScope(scope, n.Name, n.Line, n.Col) {
		scope.Define(sym)
	}
	if n.Public {
		b.defineInterfaceModuleMethods(sym, scope)
	}
	b.file.Definitions[pos] = sym
}

func (b *builder) defineInterfaceModuleMethods(ifaceSym *Symbol, scope *Scope) {
	if ifaceSym == nil || scope == nil {
		return
	}
	for _, methodSym := range ifaceSym.Members {
		if methodSym == nil || methodSym.Kind != SymbolInterfaceMethod {
			continue
		}
		if prior := scope.LookupLocal(methodSym.Name); prior != nil {
			continue
		}
		scope.Define(methodSym)
	}
}

// defineInterfaceAnnotations walks type-param declarations, where clauses,
// and each method's param/return type expressions.
func (b *builder) defineInterfaceAnnotations(n *ast.InterfaceDef, scope *Scope) {
	inner := b.defineTypeParams(n.TypeParams, scope)
	b.walkWhereClauses(n.WhereClauses, inner)
	for _, m := range n.Methods {
		for _, p := range m.Params {
			b.walkTypeExpr(p.TypeAnnotation, inner)
		}
		b.walkTypeExpr(m.ReturnTypeExpr, inner)
		b.walkWhereClauses(m.WhereClauses, inner)
	}
}

// recordInterfaceImpl records one (typeName, ifaceName, methodName) interface-
// impl triple into the file's dispatch structures: Impls[type][iface] and the
// default-method override map. Called by the block form
// (defineImplBlockStub / defineImplBlockAnnotations) — the only impl front-end —
// which passes the header receiver. Idempotent on repeat writes.
func (b *builder) recordInterfaceImpl(typeName, ifaceName, methodName string) {
	if b.file.Impls == nil {
		b.file.Impls = make(map[string]map[string]bool)
	}
	if b.file.Impls[typeName] == nil {
		b.file.Impls[typeName] = make(map[string]bool)
	}
	b.file.Impls[typeName][ifaceName] = true

	if methodName == "" {
		return // pair-only recording (bodyless impl)
	}
	// Track the override: this method claims (typeName, ifaceName) for this
	// file. registerImplDefaults reads this to skip registering an
	// interface default whose name collides with a user-supplied method —
	// across ALL impls in the file, not just one.
	if b.implOverrides == nil {
		b.implOverrides = make(map[string]map[string]map[string]bool)
	}
	if b.implOverrides[typeName] == nil {
		b.implOverrides[typeName] = make(map[string]map[string]bool)
	}
	if b.implOverrides[typeName][ifaceName] == nil {
		b.implOverrides[typeName][ifaceName] = make(map[string]bool)
	}
	b.implOverrides[typeName][ifaceName][methodName] = true
}

// implBlockItems returns the FuncDef items of an impl block. ExternFunc items
// are wrapped enough for symbol registration but body-less; this helper yields
// only the FuncDefs (the body-walk / signature-type targets). ExternFunc items
// are handled separately where extern-shaped registration is needed.
func implBlockFuncDefs(n *ast.ImplBlock) []*ast.FuncDef {
	var out []*ast.FuncDef
	for _, item := range n.Items {
		if fn, ok := item.(*ast.FuncDef); ok {
			out = append(out, fn)
		}
	}
	return out
}

// implBlockExternFuncs is the `host fn` analogue of implBlockFuncDefs — the
// items implBlockFuncDefs filters out. Used to carry an interface block's
// extern impl methods through the dispatch-target index for go-to-def / hover.
func implBlockExternFuncs(n *ast.ImplBlock) []*ast.ExternFunc {
	var out []*ast.ExternFunc
	for _, item := range n.Items {
		if ext, ok := item.(*ast.ExternFunc); ok {
			out = append(out, ext)
		}
	}
	return out
}

// defineImplBlockStub registers the per-item symbols of an impl block into the
// file's type-method table (keyed by the block's receiver type). Pure
// registration; no scope.Lookup of names that depend on imports having resolved
// — that work is in defineImplBlockAnnotations. Idempotent on Definitions[pos].
func (b *builder) defineImplBlockStub(n *ast.ImplBlock, scope *Scope) {
	recv := TypeExprBaseName(n.Receiver)
	if recv == "" {
		return // unkeyable receiver; annotations pass reports the error
	}
	isInterface := n.Interface != nil
	ifaceName := ""
	if isInterface {
		ifaceName = TypeExprBaseName(n.Interface)
	}
	if scope == nil {
		scope = b.fileScope
	}
	if b.file.TypeMethods == nil {
		b.file.TypeMethods = make(map[string]map[string]*Symbol)
	}
	if b.file.TypeMethods[recv] == nil {
		b.file.TypeMethods[recv] = make(map[string]*Symbol)
	}
	var ownerSym *Symbol
	if scope != nil && !isInterface {
		ownerSym = scope.LookupLocal(recv)
		if ownerSym != nil && ownerSym.Resolved != nil {
			ownerSym = ownerSym.Resolved
		}
		// An inherent impl of a type declared elsewhere is an orphan
		// (reported by the impl checks); its methods are not hung on that
		// file's symbol, which another file's analysis owns, or for a stdlib
		// type every check in the process.
		if !b.declaresHere(ownerSym) {
			ownerSym = nil
		}
	}
	for _, item := range n.Items {
		var name string
		var pos Pos
		var public bool
		var doc string
		var node ast.Node
		var kind SymbolKind = SymbolFunction
		switch it := item.(type) {
		case *ast.FuncDef:
			name, pos, public, doc, node = it.Name, Pos{Line: it.Line, Col: it.Col}, it.Public, it.Doc, it
		case *ast.ExternFunc:
			name, pos, public, doc, node = it.Name, Pos{Line: it.Line, Col: it.Col}, it.Public, it.Doc, it
		case *ast.OnceBinding:
			name, pos, public, doc, node = it.Name, Pos{Line: it.Line, Col: it.Col}, it.Public, it.Doc, it
			kind = SymbolOnce
		default:
			continue
		}
		sym := b.file.Definitions[pos]
		if sym == nil {
			sym = &Symbol{
				Name:       name,
				Kind:       kind,
				Pos:        pos,
				Public:     public,
				Doc:        doc,
				Node:       node,
				OwningType: recv,
			}
			b.file.Definitions[pos] = sym
		}
		if sym.OwningType == "" {
			sym.OwningType = recv
		}
		// Inherent (`impl T { pub fn f }`) beats interface-impl
		// (`impl Iface for T { fn f }`) on a name clash for one type — the rule
		// preferTypeMethodSymbol states for the identity table.
		//
		// First-writer-wins is not enough here: source order puts the
		// interface block first whenever it is written first. With
		// `impl Literal for Pick` above `impl Pick`, the checker would type
		// `Pick.from_fragments([…])` by the INTERFACE method's return type while
		// the call runs the inherent one, so `n: Int = …` would type-check and
		// fault at run time with `cannot add String and Int`. The identity
		// table depends on this too: PopulateTypeMethodIdentities reads FROM
		// this table, so preferTypeMethodSymbol sees both symbols only if this
		// slot applies the same rule.
		//
		// Inherent vs inherent stays first-wins: a genuine duplicate, reported
		// by the collision check, and first-wins is the deterministic choice.
		// Interface-impl vs interface-impl also stays first-wins — two
		// interfaces may legally share one method name for one receiver and the
		// table keeps one representative (see defineImplBlockAnnotations).
		if kind == SymbolFunction {
			slot := typeMethodSlot{recv: recv, name: name}
			_, present := b.file.TypeMethods[recv][name]
			if !present || (!isInterface && !b.inherentTypeMethods[slot]) {
				b.file.TypeMethods[recv][name] = sym
				if !isInterface {
					if b.inherentTypeMethods == nil {
						b.inherentTypeMethods = make(map[typeMethodSlot]bool)
					}
					b.inherentTypeMethods[slot] = true
				}
			}
		}
		if ownerSym != nil && public {
			if ownerSym.Members == nil {
				ownerSym.Members = make(map[string]*Symbol)
			}
			if _, present := ownerSym.Members[name]; !present {
				ownerSym.Members[name] = sym
			}
		}
		_ = ifaceName
	}
}

// defineImplBlockAnnotations resolves an impl block's header (receiver +
// optional interface), walks item signatures with `self` bound to the receiver,
// records interface-impl dispatch into the shared structures (Impls,
// DispatchNames, override tracking), stamps method visibility from the
// interface, and records the header receiver for each item so coherence keys on
// it. Assumes defineImplBlockStub already ran (the item symbols exist).
func (b *builder) defineImplBlockAnnotations(n *ast.ImplBlock, scope *Scope) {
	recv := TypeExprBaseName(n.Receiver)
	if recv == "" {
		b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
			Line: n.Line, Col: n.Col,
			Message: "impl block: cannot resolve implementing type",
		})
		return
	}

	// A child scope carries the block's generic params while walking the
	// receiver, optional interface, and item signatures. Concrete impl blocks
	// do not bind `self`; implementations spell the receiver type explicitly.
	inner := b.defineTypeParams(mergeImplScopeTypeParams(inferReceiverTypeParams(n.Receiver, scope), n.Generics), scope)
	if inner == scope {
		inner = NewScope(scope)
	}
	// Walk the receiver + interface type-exprs so their identifiers register
	// as references (hover / go-to-def on the header).
	b.walkTypeExpr(n.Receiver, inner)
	if n.Interface != nil {
		b.walkTypeExpr(n.Interface, inner)
		// `Struct` is a compiler-managed structural marker: every struct (named
		// or anonymous) satisfies it automatically by shape — conformance is a
		// predicate (isStructShaped), not a registered impl — and its `update`
		// method is a host-backed final default. It is therefore not
		// user-implementable: a hand-written `impl Struct for T` can't change
		// what satisfies `Struct` (the structural check decides) and would be
		// silently inert, so reject it explicitly.
		if TypeExprBaseName(n.Interface) == "Struct" {
			b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
				Line: n.Line, Col: n.Col,
				Message: fmt.Sprintf(
					"`Struct` is a built-in structural marker and cannot be implemented; every struct satisfies it automatically — remove this `impl Struct for %s`",
					TypeExprBaseName(n.Receiver)),
			})
		}
	}
	b.walkWhereClauses(n.WhereClauses, inner)

	// Walk each item's signature in the self-aware scope.
	//
	// Nested impl blocks carry their interface on the lowered ImplBlock header,
	// which was walked above. `ImplIface` is reserved for synthetic/internal
	// methods; walking it here is harmless for ordinary source methods, where
	// it is nil.
	for _, item := range n.Items {
		switch it := item.(type) {
		case *ast.FuncDef:
			itemInner := b.defineTypeParams(it.TypeParams, inner)
			for _, p := range it.Params {
				b.walkTypeExpr(p.TypeAnnotation, itemInner)
			}
			b.walkTypeExpr(it.ReturnTypeExpr, itemInner)
			b.walkWhereClauses(it.WhereClauses, itemInner)
			b.walkTypeExpr(it.ImplIface, itemInner)
		case *ast.ExternFunc:
			itemInner := b.defineTypeParams(it.TypeParams, inner)
			for _, p := range it.Params {
				b.walkTypeExpr(p.TypeAnnotation, itemInner)
			}
			b.walkTypeExpr(it.ReturnTypeExpr, itemInner)
			b.walkWhereClauses(it.WhereClauses, itemInner)
			b.walkTypeExpr(it.ImplIface, itemInner)
			b.defineForeignBindingRefs(it, scope)
		}
	}

	if b.file.ImplBlockReceiver == nil {
		b.file.ImplBlockReceiver = make(map[*ast.FuncDef]string)
	}
	if b.file.ImplBlockInterfaceKey == nil {
		b.file.ImplBlockInterfaceKey = make(map[*ast.FuncDef]string)
	}
	ifaceKey := ""
	if n.Interface != nil {
		ifaceKey = n.Interface.TypeString()
	}
	for _, fn := range implBlockFuncDefs(n) {
		b.file.ImplBlockReceiver[fn] = recv
		if ifaceKey != "" {
			b.file.ImplBlockInterfaceKey[fn] = ifaceKey
		}
	}
	if exts := implBlockExternFuncs(n); len(exts) > 0 {
		if b.file.ImplBlockReceiverExtern == nil {
			b.file.ImplBlockReceiverExtern = make(map[*ast.ExternFunc]string)
		}
		if b.file.ImplBlockInterfaceKeyExtern == nil {
			b.file.ImplBlockInterfaceKeyExtern = make(map[*ast.ExternFunc]string)
		}
		for _, ext := range exts {
			b.file.ImplBlockReceiverExtern[ext] = recv
			if ifaceKey != "" {
				b.file.ImplBlockInterfaceKeyExtern[ext] = ifaceKey
			}
		}
	}

	if n.Interface == nil {
		// Inherent block: nothing more to register (no interface, no dispatch
		// marking). Type-qualified exports are already in TypeMethods.
		return
	}

	// Interface-impl block: resolve + validate the interface, then record the
	// (recv, iface) dispatch into the shared dispatch structures.
	ifaceName := TypeExprBaseName(n.Interface)
	if ifaceName == "" {
		b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
			Line: n.Line, Col: n.Col,
			Message: "impl block: cannot resolve interface name",
		})
		return
	}
	ifaceLookupName := ifaceName
	if n.Interface != nil {
		if _, ok := n.Interface.(*ast.QualifiedType); ok {
			ifaceLookupName = n.Interface.TypeString()
		}
	}
	ifaceSym := scope.Lookup(ifaceLookupName)
	if ifaceSym == nil {
		if q, ok := n.Interface.(*ast.QualifiedType); ok {
			if owner := scope.Lookup(q.Module); owner != nil {
				realOwner := owner
				if realOwner.Resolved != nil {
					realOwner = realOwner.Resolved
				}
				if realOwner.ModuleScope != nil {
					ifaceSym = realOwner.ModuleScope.Lookup(q.Member.TypeString())
				}
			}
		}
	}
	if ifaceSym == nil && ifaceLookupName != ifaceName {
		ifaceSym = scope.Lookup(ifaceName)
	}
	// Compiler-known header routes — an impl header resolves without the
	// interface name being in file scope in exactly two cases:
	//
	//   - Debug, on ANY block: the checker treats every type as
	//     Debug-conformant, DetectMissingImpls exempts it, and registration
	//     is eager/universal. Universal auto-synthesis plants an `impl Debug`
	//     block in EVERY file that declares a type, imports or no imports
	//     (stdlib files get no prelude; bare single-file builds get no
	//     stdlib at all), so the header must not depend on `std/debug`
	//     being imported.
	//   - Any derivable protocol, on a SYNTHESIZED block (position in the
	//     synth band): derive synthesis is compiler machinery, so its output
	//     resolves like compiler output. The `@derive` arg that produced the
	//     block is the visible text — walkTypeDeclDecoratorArgs enforces
	//     that IT is in scope — so a missing import surfaces once, at the
	//     arg, not again here at a fabricated position. Hand-written blocks
	//     (top-level or nested; both keep real source positions) still
	//     require the interface in scope.
	//
	// All five protocols are `pub interface`s; with no scope symbol to read
	// visibility from, methods are stamped public directly.
	ifacePublic := true
	if ifaceSym == nil {
		if ifaceName != "Debug" && !(IsSynthesizedLine(n.Line) && deriveSupported[ifaceName]) {
			b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
				Line: n.Line, Col: n.Col,
				Message: fmt.Sprintf("impl block: undefined interface '%s'", ifaceName),
			})
			return
		}
	} else {
		ifaceReal := ifaceSym
		if ifaceReal.Resolved != nil {
			ifaceReal = ifaceReal.Resolved
		}
		if ifaceReal.Kind != SymbolInterface {
			b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
				Line: n.Line, Col: n.Col,
				Message: fmt.Sprintf("impl block: '%s' is not an interface", ifaceName),
			})
			return
		}
		ifacePublic = ifaceReal.Public
	}

	// Record the (T, Iface) pair even for bodyless impls. Passing
	// an empty method name records the pair without touching the override map.
	b.recordInterfaceImpl(recv, ifaceName, "")
	if b.file.DispatchNames == nil {
		b.file.DispatchNames = make(map[string]bool)
	}
	// Iterate ALL items — `fn` and `host fn` alike. An extern item IS the
	// interface method (there is no pure-Nomi wrapper), so it must feed the
	// same override map + DispatchNames + visibility stamping a `fn` item does.
	for _, item := range n.Items {
		var name string
		var pos Pos
		var explicitIface ast.TypeExpr
		var inferred bool
		var public bool
		switch it := item.(type) {
		case *ast.FuncDef:
			name, pos, explicitIface, inferred, public = it.Name, Pos{Line: it.Line, Col: it.Col}, it.ImplIface, it.ImplIfaceInferred, it.Public
		case *ast.ExternFunc:
			name, pos, explicitIface, inferred, public = it.Name, Pos{Line: it.Line, Col: it.Col}, it.ImplIface, it.ImplIfaceInferred, it.Public
		default:
			continue
		}
		methodDeclared := ifaceSym == nil || interfaceSymbolHasMethod(ifaceSym, name)
		if explicitIface != nil {
			if TypeExprBaseName(explicitIface) != ifaceName {
				if inferred && methodDeclared {
					b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
						Line: pos.Line, Col: pos.Col,
						Message: fmt.Sprintf("function '%s' cannot be shared between `%s` and `%s` for '%s'; write one `impl ... for ...` block per interface", name, TypeExprBaseName(explicitIface), ifaceName, recv),
					})
					if sym := b.file.Definitions[pos]; sym != nil {
						sym.IsImplMethod = false
						sym.ImplInterface = ""
					}
				}
				continue
			}
			if !methodDeclared {
				continue
			}
		} else {
			if !methodDeclared {
				continue
			}
			setImplItemInterface(item, n.Interface, n.InferInterfaceMethods)
			explicitIface = n.Interface
			inferred = n.InferInterfaceMethods
		}
		// Record each method into the override map so registerImplDefaults
		// knows which interface defaults are met.
		if public {
			b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
				Line: pos.Line, Col: pos.Col,
				Message: fmt.Sprintf("`pub` is not allowed on implementation function `%s`; visibility comes from `%s`", name, ifaceName),
			})
		}
		b.recordInterfaceImpl(recv, ifaceName, name)
		b.file.DispatchNames[name] = true
		// Visibility inheritance (spec §3): stamp the method symbol's
		// Public from the interface.
		if sym := b.file.Definitions[pos]; sym != nil {
			sym.Public = ifacePublic
			sym.IsImplMethod = true
			sym.ImplInterface = ifaceName
			// Keep the type-method-table entry's visibility in sync when it is
			// this exact symbol. A receiver can implement two interfaces that
			// share one method name (`fn A.foo`, `fn B.foo`); TypeMethods keeps
			// only one representative for type-qualified lookup, so never stamp
			// that representative with another method's interface.
			if tm := b.file.TypeMethods[recv]; tm != nil {
				if tmSym := tm[name]; tmSym == sym {
					tmSym.Public = ifacePublic
					tmSym.IsImplMethod = true
					tmSym.ImplInterface = ifaceName
				}
			}
		}
	}
}

func interfaceSymbolHasMethod(sym *Symbol, method string) bool {
	if sym == nil {
		return false
	}
	real := sym
	for real.Resolved != nil {
		real = real.Resolved
	}
	if real.Members == nil {
		return false
	}
	member := real.Members[method]
	return member != nil && member.Kind == SymbolInterfaceMethod
}

func setImplItemInterface(item ast.Node, iface ast.TypeExpr, inferred bool) {
	switch it := item.(type) {
	case *ast.FuncDef:
		it.ImplIface = iface
		it.ImplIfaceInferred = inferred
	case *ast.ExternFunc:
		it.ImplIface = iface
		it.ImplIfaceInferred = inferred
	}
}

// walkImplBlockBodies walks the function bodies of an impl block's items in a
// scope where the block's generic params are visible. Mirrors walkNode's
// *ast.FuncDef arm, run per item, but rooted at the block-level generics scope
// rather than module scope.
func (b *builder) walkImplBlockBodies(n *ast.ImplBlock, scope *Scope) {
	receiverDisplay := ""
	if n.Receiver != nil {
		receiverDisplay = n.Receiver.TypeString()
	}
	if IsSynthesizedLine(n.Line) {
		scope = synthSupportScope(b.file, scope)
	}
	inner := b.defineTypeParams(mergeImplScopeTypeParams(inferReceiverTypeParams(n.Receiver, scope), n.Generics), scope)
	if inner == scope {
		inner = NewScope(scope)
	}
	inner.ReceiverDisplay = receiverDisplay
	// The self/generics scope spans the whole impl block.
	setScopeSpan(inner, n.Line, n.Col, n.EndLine, n.EndCol)
	for _, item := range n.Items {
		b.walkAttachedTests(item, inner)
		if once, ok := item.(*ast.OnceBinding); ok {
			b.walkTypeExpr(once.TypeAnnotation, inner)
			if once.Value != nil {
				b.walkNode(once.Value, inner)
			}
		}
	}
	for _, fn := range implBlockFuncDefs(n) {
		// Chain a type-param scope off the self/generics scope so item-level
		// `<U>` and the block's `<T>` are both visible.
		itemInner := b.defineTypeParams(fn.TypeParams, inner)
		b.walkWhereClauses(fn.WhereClauses, itemInner)
		child := NewScope(itemInner)
		child.ReceiverDisplay = receiverDisplay
		b.defineParams(fn.Params, child, fn)
		if fn.Body != nil {
			setScopeSpan(child, fn.Body.Line, fn.Body.Col, fn.Body.EndLine, fn.Body.EndCol)
			if itemInner != inner {
				setScopeSpan(itemInner, fn.Line, fn.Col, fn.Body.EndLine, fn.Body.EndCol)
			}
			b.walkBlock(fn.Body, child)
		}
	}
}

// walkTypeDeclDecoratorArgs walks `@derive` decorators on a
// type declaration (struct / enum / type), registers each interface-
// name argument as a Reference (so editor go-to-def / find-references /
// hover on the interface name jumps to the interface declaration), and
// enforces the derive-arg scope rule: a `@derive` arg NAMES its protocol
// in visible source text, so the protocol must be in scope — an arg
// whose name isn't errors here, with the exact import to add. Debug is
// exempt (compiler-known universally; `@derive Debug` works with no
// import). User files get all five protocols from the prelude, so only
// prelude-less (stdlib) files ever see this diagnostic.
// `impl Iface for T` blocks record their own interface-name references
// during the block walk; this helper covers the type-decl `@derive` side.
//
// Other validation (unknown protocol, struct doesn't satisfy the
// contract, non-interface target) is owned by the synthesizer and
// checker. Args that aren't TypeExprs are silently skipped — only
// `@derive` carries interface-name args on a type declaration.
func (b *builder) walkTypeDeclDecoratorArgs(decorators []ast.Decorator, scope *Scope) {
	for i := range decorators {
		d := &decorators[i]
		if d.Name != "derive" {
			continue
		}
		for _, arg := range d.Args {
			te, ok := arg.(ast.TypeExpr)
			if !ok {
				continue
			}
			b.walkTypeExpr(te, scope)
			name := TypeExprBaseName(te)
			if !deriveSupported[name] || name == "Debug" {
				continue // unknown protocols error in the synthesizer; Debug needs no import
			}
			if scope.Lookup(name) == nil {
				line, col := te.LineNum(), 1
				if st, ok := te.(*ast.SimpleType); ok {
					line, col = st.Line, st.Col
				}
				b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
					Line: line, Col: col,
					Message: fmt.Sprintf("`%s` is not in scope — import `%s.%s`",
						name, deriveProtocolHomeModule[name], name),
				})
			}
		}
		if d.Options != nil {
			b.walkNode(d.Options, scope)
		}
	}
}

// registerImplDefaults registers each interface's default methods on every
// type that has at least one `impl Iface for T` block in this file — but only
// methods NOT overridden by any user-supplied method in the file.
//
// It operates over the file-wide override set b.implOverrides (populated by
// recordInterfaceImpl as the block stubs are defined) rather than a single
// block's method list, so default registration sees every impl block for a
// given (type, iface) before deciding which defaults to register.
func (b *builder) registerImplDefaults(scope *Scope) {
	for _, ifaceMap := range b.implOverrides {
		for ifaceName, overrides := range ifaceMap {
			ifaceSym := scope.Lookup(ifaceName)
			if ifaceSym == nil {
				continue
			}
			real := ifaceSym
			if real.Resolved != nil {
				real = real.Resolved
			}
			if real.Kind != SymbolInterface {
				continue
			}
			ifaceDef, ok := real.Node.(*ast.InterfaceDef)
			if !ok {
				continue
			}
			for i := range ifaceDef.Methods {
				m := &ifaceDef.Methods[i]
				if m.Body == nil {
					continue // not a default
				}
				if overrides[m.Name] {
					continue // user-implemented somewhere in this file
				}
				methodSym, ok := real.Members[m.Name]
				if !ok {
					continue
				}
				// NOTE: must Define the SAME *Symbol pointer that lives in
				// real.Members[m.Name]. bareNameDispatchInterface in
				// checker.go (shape 2, the pointer-identity scan) relies on
				// `ic.Members[real.Name] == real` to find a default
				// method's owning interface. Cloning/defensively copying
				// the symbol here would silently break bare-name dispatch
				// marking for default methods.
				scope.Define(methodSym)
				if b.file.DispatchNames == nil {
					b.file.DispatchNames = make(map[string]bool)
				}
				b.file.DispatchNames[m.Name] = true
			}
		}
	}
}

// IsPunnedField reports whether a struct-literal field is punned, `{x}` for
// `{x: x}`: the parser gives the label and the variable read one position.
func IsPunnedField(f ast.StructFieldVal) bool {
	id, ok := f.Value.(*ast.Ident)
	return ok && f.Line > 0 && id.Line == f.Line && id.Col == f.Col
}

// setPunnedFieldLabel records the field a punned label names.
func (fa *FileAnalysis) setPunnedFieldLabel(pos Pos, field *Symbol) {
	if fa.PunnedFieldLabels == nil {
		fa.PunnedFieldLabels = map[Pos]*Symbol{}
	}
	fa.PunnedFieldLabels[pos] = field
}

// registerStructLitFieldRefs walks a StructLit's fields and registers a
// Reference at each field name's source position pointing at the struct
// (or enum struct-variant) field's defining Symbol. Lets hover on a
// field name in a literal show the field's declared type.
//
// Skips fields whose Line is zero (parser didn't track the position —
// e.g. for synthesised literals from anonymous-struct inference).
func (b *builder) registerStructLitFieldRefs(n *ast.StructLit, scope *Scope) {
	if n.TypeName == nil {
		return // anonymous struct literal — no declaration to point at
	}
	var holder *Symbol // the struct (or variant) that owns the fields
	switch tn := n.TypeName.(type) {
	case *ast.SimpleType:
		if sym := scope.Lookup(tn.Name); sym != nil {
			real := sym
			if real.Resolved != nil {
				real = real.Resolved
			}
			if real.Kind == SymbolStruct {
				holder = real
			}
		}
	case *ast.QualifiedType:
		// e.g. Error.HttpError{...} — resolve via the enum's Members.
		if enumSym := scope.Lookup(tn.Module); enumSym != nil {
			real := enumSym
			if real.Resolved != nil {
				real = real.Resolved
			}
			if real.Kind == SymbolEnum {
				if vname, ok := tn.Member.(*ast.SimpleType); ok {
					if variant, ok := real.Members[vname.Name]; ok {
						holder = variant
					}
				}
			}
		}
	}
	// Embedded enum variants (`embeds Foo` in an enum body) hold no field
	// Members of their own — the fields live on the embedded struct itself.
	// Look up the embedded struct via Definitions (scope.Lookup would find
	// the variant, since it shares the embedded type's name) so
	// `Drawable.Circle2{radius: …}` can hover the field name.
	if holder != nil && holder.Kind == SymbolEnumVariant && len(holder.Members) == 0 {
		for _, defSym := range b.file.Definitions {
			if defSym.Name == holder.Name && defSym.Kind == SymbolStruct {
				holder = defSym
				break
			}
		}
	}
	if holder == nil || holder.Members == nil {
		return
	}
	for _, f := range n.Fields {
		if f.Line == 0 {
			continue
		}
		if fieldSym, ok := holder.Members[f.Name]; ok {
			if IsPunnedField(f) {
				b.file.setPunnedFieldLabel(Pos{Line: f.Line, Col: f.Col}, fieldSym)
				continue
			}
			b.file.References[Pos{Line: f.Line, Col: f.Col}] = fieldSym
		}
	}
}

// stampImportedSymbol records filePath, the user file an import resolved to,
// as the file that declares sym and its members, where they name none yet.
// Cross-file go-to-definition, related-information locations and the
// module-private checks read it.
//
// A stdlib import resolves to no file path and stamps nothing. Its symbols
// belong to the process's one stdlib analysis (std.Shared), which every
// check reads at once, and they keep an empty SourceFile: go-to-definition
// finds a stdlib symbol through the stdlib's own module scopes instead.
func stampImportedSymbol(sym *Symbol, filePath string) {
	if sym == nil || filePath == "" {
		return
	}
	if sym.SourceFile == "" {
		sym.SourceFile = filePath
	}
	for _, m := range sym.Members {
		if m != nil && m.SourceFile == "" {
			m.SourceFile = filePath
		}
	}
}

// stampImportedScope is stampImportedSymbol for every symbol a user module
// declares at its top level.
func stampImportedScope(modScope *Scope, filePath string) {
	if modScope == nil || filePath == "" {
		return
	}
	for _, s := range modScope.Symbols {
		stampImportedSymbol(s, filePath)
	}
}

// defineImport registers the symbols and module bindings for an import
// declaration. Called from defineTopLevel for module-level imports and from
// walkNode for nested imports inside a function body.
//
// nested=false (top-level): for file aliases, b.modules is re-keyed
// under the alias and the original key removed.
//
// nested=true (inside a function body): b.modules is NEVER mutated.
// Aliases are only registered as Symbols in the local scope. b.modules
// is process-global and would otherwise wipe out module-level entries.
func (b *builder) defineImport(n *ast.ImportStmt, scope *Scope, nested bool) {
	modPathStrings := make([]string, len(n.ModulePath))
	for i, seg := range n.ModulePath {
		modPathStrings[i] = ast.ImportNodeName(seg)
	}

	// internal/ access check. Path-only: doesn't depend on whether
	// resolveImport actually finds the file (an unresolved import
	// outside the parent subtree is still a contract violation). Skip
	// stdlib paths — `std/...` is the language's own surface and has
	// no `internal/` subtree convention.
	//
	// For a drill-through selective import (`import foo/Enum.{V}`),
	// the trailing TypeIdent (PascalCase) names an enum within `foo`,
	// not a directory segment — the file actually resolved is
	// foo.nomi. Strip the trailing TypeIdent before path-checking so
	// the importee path matches what resolveImport will load (mirrors
	// the same strip the selective-import branch entry does before
	// calling resolveImport further down).
	//
	// sameModule classification: an import is "same module"
	// iff its head is NOT a cross-module short-name. Concretely:
	//   - head == currentModuleName → same module (self-name spelling)
	//   - head in moduleIndex      → DIFFERENT module (cross-module)
	//   - otherwise (including no manifest / single-file mode) → same module
	// The intra-module case feeds through the parent-subtree rule;
	// cross-module flips sameModule=false so any internal/ segment in
	// the importee is rejected unconditionally.
	if len(modPathStrings) > 0 && modPathStrings[0] != "std" {
		checkPath := modPathStrings
		if len(n.Names) > 0 {
			for len(checkPath) > 1 {
				if _, isType := n.ModulePath[len(checkPath)-1].(*ast.TypeIdent); !isType {
					break
				}
				checkPath = checkPath[:len(checkPath)-1]
			}
		}
		head := checkPath[0]
		sameModule := true
		importeeModRel := strings.Join(checkPath, "/")
		if rest, self := SelfNameImport(checkPath, b.currentModuleName); self {
			// Self-name spelling: strip the head for the check's
			// importeeModRel so it compares like a bare intra-module
			// import. sameModule stays true.
			importeeModRel = strings.Join(rest, "/")
		} else if _, ok := b.moduleIndex[head]; ok {
			sameModule = false
		}
		if err := checkInternalAccess(b.importerModRel(), importeeModRel, sameModule); err != nil {
			b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
				Line:    n.Line,
				Col:     n.Col,
				Message: err.Error(),
			})
		}
	}

	if len(n.Names) == 0 {
		// File API import: define the module symbol under the final
		// path segment or explicit alias.
		last := n.ModulePath[len(n.ModulePath)-1]
		origName := ast.ImportNodeName(last)
		bindName := origName
		bindNode := last
		if n.ModuleAlias != nil {
			bindName = ast.ImportNodeName(n.ModuleAlias)
			bindNode = n.ModuleAlias
		}
		// Register every path segment as a clickable Reference so hover
		// works on `std`, `list`, etc. in `import std.list[ as col]`.
		// Previously only the aliased branch did this; bare imports left
		// the path segments without symbols.
		for _, seg := range n.ModulePath {
			segName := ast.ImportNodeName(seg)
			pathSym := &Symbol{
				Name: segName,
				Kind: SymbolModule,
				Pos:  Pos{Line: seg.LineNum(), Col: b.nodeCol(seg)},
				Node: n,
			}
			b.file.References[pathSym.Pos] = pathSym
		}
		sym := &Symbol{
			Name: bindName,
			Kind: SymbolModule,
			Pos:  Pos{Line: bindNode.LineNum(), Col: b.nodeCol(bindNode)},
			Node: n,
		}
		// Resolve the module's scope and attach it so `alias.Member`
		// field-access can find members regardless of b.modules state
		// (notably for nested aliases that don't touch b.modules).
		var modScope *Scope
		var filePath string
		if stdScope := b.resolveStdlibImport(modPathStrings); stdScope != nil {
			modScope = stdScope
		} else {
			var miss *importMiss
			modScope, filePath, miss = b.resolveImportOrMiss(modPathStrings)
			b.reportMissingModule(n, n.ModulePath, miss)
		}
		sym.ModuleScope = modScope
		scope.Define(sym)
		b.file.Definitions[sym.Pos] = sym

		// Top-level imports also update b.modules so qualified-type
		// resolution and FieldAccess (which currently consult it) use the
		// alias. Nested imports skip these mutations — see the comment on
		// defineImport — and instead rely on sym.ModuleScope, which is set
		// above for both nested and top-level cases.
		if nested {
			// Tag user symbols with source path even for nested user
			// imports so go-to-def works.
			stampImportedScope(modScope, filePath)
		} else if b.isStdlibImport(modPathStrings) {
			b.moduleSyms[bindName] = sym
			if bindName != origName {
				if origScope, ok := b.modules[origName]; ok {
					b.modules[bindName] = origScope
					delete(b.modules, origName)
				}
				delete(b.moduleSyms, origName)
			}
			// Track explicit import for the function-shadow fallback.
			if modScope != nil {
				if b.imported == nil {
					b.imported = make(map[string]*Scope)
				}
				b.imported[bindName] = modScope
			}
		} else if modScope != nil {
			if b.modules == nil {
				b.modules = make(map[string]*Scope)
			}
			b.modules[bindName] = modScope
			b.moduleSyms[bindName] = sym
			stampImportedScope(modScope, filePath)
			// Track explicit import for the function-shadow fallback.
			if b.imported == nil {
				b.imported = make(map[string]*Scope)
			}
			b.imported[bindName] = modScope
		}
	} else {
		// Selective import: import models.{User, Point} (with optional aliases).
		// Drill-through form: `import shape.Shape.{Circle}` or
		// `import api.Module.Type.{self, method}`. Resolve the longest real
		// module prefix first; any remaining PascalCase path segments are owners
		// walked inside that module.
		modulePath := modPathStrings
		var ownerSegments []string

		// Register module path segments as references so they are clickable.
		// Owner segments are overwritten with their resolved owner symbols below.
		for _, seg := range n.ModulePath {
			name := ast.ImportNodeName(seg)
			pathSym := &Symbol{
				Name: name,
				Kind: SymbolModule,
				Pos:  Pos{Line: seg.LineNum(), Col: b.nodeCol(seg)},
				Node: n,
			}
			b.file.References[pathSym.Pos] = pathSym
		}

		var modScope *Scope
		var filePath string
		// The `/`-joined segments name the file, so the module is never a
		// shorter prefix of them: `import utils/inner.Thing` with no
		// utils/inner.nomi is a missing file, not an owner `inner` in
		// utils.nomi.
		minSplit := max(1, n.FileSegments)
		for split := len(modPathStrings); split >= minSplit; split-- {
			candidate := modPathStrings[:split]
			if stdScope := b.resolveStdlibImport(candidate); stdScope != nil {
				modulePath = candidate
				ownerSegments = modPathStrings[split:]
				modScope = stdScope
				break
			}
			if scope, path := b.resolveImport(candidate); scope != nil {
				modulePath = candidate
				ownerSegments = modPathStrings[split:]
				modScope = scope
				filePath = path
				break
			}
		}
		if modScope == nil {
			// No prefix of the path is a module. The file part of the path
			// is what precedes its owner segments (`Shape` in
			// `shape.Shape.{Circle}`), and that is the file that is
			// missing. An import the parser did not write records no
			// file part; its trailing PascalCase segments are the owners.
			filePart := n.ModulePath
			if n.FileSegments > 0 && n.FileSegments <= len(filePart) {
				filePart = filePart[:n.FileSegments]
			} else {
				for len(filePart) > 1 {
					if _, isType := filePart[len(filePart)-1].(*ast.TypeIdent); !isType {
						break
					}
					filePart = filePart[:len(filePart)-1]
				}
			}
			modulePath = modPathStrings[:len(filePart)]
			ownerSegments = nil
			var miss *importMiss
			modScope, filePath, miss = b.resolveImportOrMiss(modulePath)
			b.reportMissingModule(n, filePart, miss)
		}
		stampImportedScope(modScope, filePath)

		// For drill-through, look up the owner path within modScope and
		// register references so hover/go-to-def works on every owner segment.
		var ownerSym *Symbol
		ownerScope := modScope
		for i, ownerName := range ownerSegments {
			ownerSym = nil
			// Dotted declarations such as `pub enum Probe.Reading` are
			// exported as one module symbol named `Probe.Reading`, not as a
			// nested symbol inside `Probe`. Prefer that progressively dotted
			// module symbol while walking the owner so drill-through imports
			// like `import telemetry.Probe.Reading.{Steady}` can select
			// members from the real owner.
			if modScope != nil {
				ownerSym = modScope.LookupLocal(strings.Join(ownerSegments[:i+1], "."))
			}
			if ownerSym == nil && ownerScope != nil {
				ownerSym = ownerScope.LookupLocal(ownerName)
			}
			if ownerSym == nil {
				break
			}
			seg := n.ModulePath[len(modulePath)+i]
			// Without braces the owner segment is not a path step — it is
			// the front half of one name. `import telemetry.Probe.Reading`
			// imports `Probe.Reading`, so pointing this position at
			// `Probe` would describe a different symbol. With
			// braces the segment really is navigated and keeps its own
			// meaning, which is what makes `Probe.{self, Reading}` hover
			// `Probe` as its own file API object.
			if !n.Braced && i == len(ownerSegments)-1 && len(n.Names) == 1 {
				whole := strings.Join(ownerSegments, ".") + "." + ast.ImportNodeName(n.Names[0])
				if wholeSym := modScope.LookupLocal(whole); wholeSym != nil {
					b.file.References[Pos{Line: seg.LineNum(), Col: b.nodeCol(seg)}] = wholeSym
					continue
				}
			}
			b.file.References[Pos{Line: seg.LineNum(), Col: b.nodeCol(seg)}] = ownerSym
			if !ownerSym.Public {
				b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
					Line:    seg.LineNum(),
					Col:     b.nodeCol(seg),
					Message: fmt.Sprintf("'%s' is private and cannot be imported through", ownerSym.Name),
				})
			}
			stampImportedSymbol(ownerSym, filePath)
			if ownerSym.ModuleScope != nil {
				ownerScope = ownerSym.ModuleScope
			} else {
				ownerScope = nil
			}
		}

		// resolveName looks up the imported name. For drill-through it walks
		// the owner's exported member scope; otherwise it walks the module
		// scope directly.
		resolveName := func(origName string) *Symbol {
			if modScope == nil {
				return nil
			}
			if len(ownerSegments) > 0 {
				nameOwnerSegments := ownerSegments
				lookupName := origName
				if parts := strings.Split(origName, "."); len(parts) > 1 {
					nameOwnerSegments = append(append([]string(nil), ownerSegments...), parts[:len(parts)-1]...)
					lookupName = parts[len(parts)-1]
				}
				nameOwnerSym := ownerSym
				if len(nameOwnerSegments) != len(ownerSegments) {
					nameOwnerSym = lookupImportOwnerSymbol(modScope, nameOwnerSegments)
				}
				// `import telemetry.Probe.Reading` names one module
				// symbol that happens to contain a dot, not a member reached
				// by traversing `Probe`. Try the whole name first; the paths
				// below still serve enums and namespaces, where the segments
				// really are a traversal.
				if whole := modScope.LookupLocal(strings.Join(nameOwnerSegments, ".") + "." + lookupName); whole != nil {
					return whole
				}
				if nameOwnerSym != nil && nameOwnerSym.Members != nil {
					if member := nameOwnerSym.Members[lookupName]; member != nil {
						return member
					}
				}
				if nameOwnerSym != nil && nameOwnerSym.ModuleScope != nil {
					return nameOwnerSym.ModuleScope.LookupLocal(lookupName)
				}
				if nameOwnerSym != nil {
					if byMethod := b.file.TypeMethods[nameOwnerSym.Name]; byMethod != nil {
						return byMethod[lookupName]
					}
				}
				return nil
			}
			return modScope.LookupLocal(origName)
		}

		// Define each imported name, honoring aliases.
		for i, nameNode := range n.Names {
			origName := ast.ImportNodeName(nameNode)
			real := resolveName(origName)

			bindName := origName
			bindNode := nameNode
			if n.Braced && len(ownerSegments) > 0 {
				bindName = importNameLeaf(origName)
			}
			// Unbraced, a dotted name binds under the whole name: there is
			// no other spelling for it. Braced is a selection —
			// `Probe.{self, Reading}` says bring `Reading` in from `Probe` — so it binds
			// what was selected, the same as any other brace list.
			if real != nil && !n.Braced && len(ownerSegments) > 0 &&
				real.Name == strings.Join(ownerSegments, ".")+"."+origName {
				bindName = real.Name
			}
			if i < len(n.Aliases) && n.Aliases[i] != nil {
				bindName = ast.ImportNodeName(n.Aliases[i])
				bindNode = n.Aliases[i]
				// The original name still needs to be a clickable reference
				// to the real symbol in the source module. Register it.
				origPathSym := &Symbol{
					Name: origName,
					Kind: SymbolBinding,
					Pos:  Pos{Line: nameNode.LineNum(), Col: b.nodeCol(nameNode)},
					Node: n,
				}
				if real != nil {
					origPathSym.Resolved = real
					origPathSym.Kind = real.Kind
				}
				b.file.References[origPathSym.Pos] = origPathSym
			}
			sym := &Symbol{
				Name: bindName,
				Kind: SymbolBinding,
				Pos:  Pos{Line: bindNode.LineNum(), Col: b.nodeCol(bindNode)},
				Node: n,
			}
			if real != nil {
				// Reject flat selective variant imports — `import x.{Variant}`.
				// Drill-through (`import x.Enum.{Variant}`) is the explicit
				// form and is permitted; the bare flat form hides the
				// originating enum and is rejected.
				if len(ownerSegments) == 0 && real.Kind == SymbolEnumVariant {
					modName := strings.Join(modulePath, "/")
					enumName := ""
					for _, s := range modScope.Symbols {
						if s.Kind == SymbolEnum && s.Members != nil {
							if m, ok := s.Members[origName]; ok && m == real {
								enumName = s.Name
								break
							}
						}
					}
					hint := ""
					if enumName != "" {
						hint = fmt.Sprintf(" — use 'import %s.%s.{%s}' to lift specific variants, or 'import %s.{%s}' and qualify as '%s.%s'",
							modName, enumName, origName, modName, enumName, enumName, origName)
					}
					b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
						Line:    nameNode.LineNum(),
						Col:     b.nodeCol(nameNode),
						Message: fmt.Sprintf("cannot import variant '%s' directly%s", origName, hint),
					})
				}
				stampImportedSymbol(real, filePath)
				sym.Resolved = real
				sym.Kind = real.Kind
			} else if modScope != nil {
				// Module loaded fine, but the name isn't there. Either a typo
				// or a removed/renamed export at the source. The local symbol
				// is still defined so downstream references don't cascade
				// into "undefined" errors that obscure the real cause.
				modName := strings.Join(modulePath, "/")
				if len(ownerSegments) > 0 {
					modName += "." + strings.Join(ownerSegments, ".")
				}
				b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
					Line:    nameNode.LineNum(),
					Col:     b.nodeCol(nameNode),
					Message: fmt.Sprintf("file '%s' has no exported name '%s'", modName, origName),
				}.Spanning(nameNode).WithHint(importSuggestion(origName, modScope, ownerSegments)))
			}

			// Per-item re-export: `import mod.{a export [as Pub]}` or the
			// line-level shorthand `import mod.{a, b} export`. The item is
			// re-exported when either the per-item flag is set or the
			// line-level ExportAll flag is set on a selective import.
			//
			// The publicly-exposed name is ExportAliases[i] if present,
			// otherwise the local bind name (Aliases[i] ?? Names[i]).
			//
			//   - If the export name matches the local bind name (the
			//     common case, including the line-level shorthand), just
			//     mark the local binding's Public flag true. Consumers see
			//     the same name.
			//   - If the export name differs (export-rename case), register
			//     an additional public symbol under the export name. The
			//     local sym stays private — the importer uses the local
			//     name, downstream consumers use the export name.
			perItemExport := i < len(n.ExportFlags) && n.ExportFlags[i]
			itemExported := n.ExportAll || perItemExport
			if itemExported {
				exportName := bindName
				exportNode := bindNode
				if i < len(n.ExportAliases) && n.ExportAliases[i] != nil {
					exportName = ast.ImportNodeName(n.ExportAliases[i])
					exportNode = n.ExportAliases[i]
				}
				if exportName == bindName {
					sym.Public = true
				} else {
					pubSym := &Symbol{
						Name:   exportName,
						Kind:   sym.Kind,
						Pos:    Pos{Line: exportNode.LineNum(), Col: b.nodeCol(exportNode)},
						Public: true,
						Node:   n,
					}
					if real != nil {
						pubSym.Resolved = real
					} else if sym.Resolved != nil {
						pubSym.Resolved = sym.Resolved
					}
					scope.Define(pubSym)
					b.file.Definitions[pubSym.Pos] = pubSym
				}
			}

			// A user-written selective import and a local DECLARATION of the
			// same name are a genuine collision, and this Define is where the
			// import silently won it: BuildProject runs every file's
			// declaration stubs before any file's annotations, and
			// defineImport is annotation-side, so the import always wrote
			// last. That is what made FileAnalysis.ModuleScope unable to
			// answer "which declaration does this name mean in this file?" —
			// it returned the import for a name the file declares itself, for
			// every declaring kind (interface, struct, enum, function).
			// Routing through the same redeclare check every declaration site
			// uses keeps the local declaration in the slot and says why.
			//
			// Two exemptions, both about NOT reporting something the user did
			// not write or did not get wrong:
			//
			//   - Synthesized imports (the auto-prepended prelude chain,
			//     which lands in the synth band) have no user-visible
			//     position to report at. A local declaration of a prelude
			//     name is diagnosed at its own site by checkReservedTypeName.
			//   - An import colliding with another IMPORT is left exactly as
			//     it was. `import std/maybe.{Maybe}` beside the prelude's own
			//     injected `Maybe` is the explicit-is-better idiom, not an
			//     error, and stdlib files must write it. Import-vs-import
			//     precedence is a separate open question; this check does not
			//     touch it.
			if b.importMayTakeScopeSlot(scope, bindName, sym.Pos) {
				scope.Define(sym)
			}
			b.file.Definitions[sym.Pos] = sym

			// A dotted name is one name that happens to contain a dot — the
			// declaration `pub enum Probe.Reading` is an ordinary module
			// symbol called "Probe.Reading", sitting beside `Probe` rather
			// than inside it. The dot's only power is drilling, so importing
			// `Probe` carries the names spelled under it. Nothing bare enters
			// scope (the use site still writes `Probe.Reading`), so there is
			// no collision to guard against — and without this, the type
			// a dotted declaration is unnameable at every call site.
			if modScope != nil && len(ownerSegments) == 0 {
				b.defineDottedImportsForOwner(scope, modScope, origName, bindName, sym.Pos, n)
			}
		}

		// `import path.Owner.{self, ...}` — the `self` marker also
		// binds the explicit owner in addition to its selected members.
		// Parser-recovered or synthesized ASTs may still carry the old
		// path-level shape; keep that branch defensive, but source syntax
		// rejects it.
		if n.IncludeParent {
			if len(ownerSegments) > 0 {
				// Drill-through: bind the owner itself in addition to its members.
				if ownerSym != nil {
					ownerSeg := n.ModulePath[len(n.ModulePath)-1]
					ownerName := ownerSegments[len(ownerSegments)-1]
					sym := &Symbol{
						Name:     ownerName,
						Kind:     ownerSym.Kind,
						Pos:      Pos{Line: ownerSeg.LineNum(), Col: b.nodeCol(ownerSeg)},
						Node:     n,
						Resolved: ownerSym,
					}
					if ownerSym.SourceFile != "" {
						sym.SourceFile = ownerSym.SourceFile
					}
					scope.Define(sym)
					b.file.Definitions[sym.Pos] = sym
					b.defineDottedImportsForOwner(scope, modScope, ownerSym.Name, ownerName, sym.Pos, n)
					// Register a hover/go-to-def target at the `self` token
					// itself so clicking on `self` behaves like clicking on
					// the LHS enum segment. Not added to scope — `self` is a
					// marker, not a binding name.
					if n.SelfLine > 0 {
						selfSym := &Symbol{
							Name:     "self",
							Kind:     ownerSym.Kind,
							Pos:      Pos{Line: n.SelfLine, Col: n.SelfCol},
							Node:     n,
							Resolved: ownerSym,
						}
						if ownerSym.SourceFile != "" {
							selfSym.SourceFile = ownerSym.SourceFile
						}
						b.file.Definitions[selfSym.Pos] = selfSym
					}
				}
			} else {
				// A file import with `self` (`import std/io.{self, IOError}`):
				// bind the last path segment as the file API object, as
				// `import std/io` does.
				last := n.ModulePath[len(n.ModulePath)-1]
				bindName := ast.ImportNodeName(last)
				sym := &Symbol{
					Name:        bindName,
					Kind:        SymbolModule,
					Pos:         Pos{Line: last.LineNum(), Col: b.nodeCol(last)},
					Node:        n,
					ModuleScope: modScope,
				}
				scope.Define(sym)
				b.file.Definitions[sym.Pos] = sym
				if b.imported == nil {
					b.imported = map[string]*Scope{}
				}
				b.imported[bindName] = modScope
				// As a plain `import std/io` does, the binding replaces
				// the module table's placeholder for the name, which
				// carries no scope: a `file.member` reference resolved to
				// the placeholder, so `io.no_such_function(1)` was never
				// reported as a missing member.
				if !nested && modScope != nil {
					if b.moduleSyms == nil {
						b.moduleSyms = make(map[string]*Symbol)
					}
					b.moduleSyms[bindName] = sym
				}
				// Register a hover/go-to-def target at the `self` token
				// itself so clicking on `self` behaves like clicking on
				// the trailing module-path segment. Not added to scope —
				// `self` is a marker, not a binding name.
				if n.SelfLine > 0 {
					selfSym := &Symbol{
						Name:        "self",
						Kind:        SymbolModule,
						Pos:         Pos{Line: n.SelfLine, Col: n.SelfCol},
						Node:        n,
						ModuleScope: modScope,
					}
					b.file.Definitions[selfSym.Pos] = selfSym
				}
			}
		}
	}
}

func (b *builder) defineDottedImportsForOwner(scope, modScope *Scope, sourceOwner, bindOwner string, pos Pos, node ast.Node) {
	if scope == nil || modScope == nil || sourceOwner == "" || bindOwner == "" {
		return
	}
	prefix := sourceOwner + "."
	for name, member := range modScope.Symbols {
		if !strings.HasPrefix(name, prefix) || !member.Public {
			continue
		}
		dotted := bindOwner + strings.TrimPrefix(name, sourceOwner)
		if scope.LookupLocal(dotted) != nil {
			continue
		}
		drilled := &Symbol{
			Name:     dotted,
			Kind:     member.Kind,
			Pos:      pos,
			Node:     node,
			Resolved: member,
			Type:     member.Type,
		}
		scope.Define(drilled)
	}
}

func (b *builder) walkNode(node ast.Node, scope *Scope) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.OnceBinding:
		// `once` is module-level only. A nested `once` inside a function
		// body is rejected — function bodies use ordinary `name = value`
		// bindings instead. We still walk the type annotation and value
		// so references inside them are recorded for hover / go-to-def.
		b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
			Line:    n.Line,
			Col:     n.Col,
			Message: fmt.Sprintf("'once %s' must appear at module top-level — function bodies use ordinary 'name = value' bindings", n.Name),
		})
		b.walkTypeExpr(n.TypeAnnotation, scope)
		if n.Value != nil {
			b.walkNode(n.Value, scope)
		}

	case *ast.FuncDef:
		// defineFunc handles symbol registration + param/return type-expr
		// walking. The returned scope has the fn's type params defined; chain
		// the body scope off it so lambda annotations inside the body can
		// resolve them.
		inner := b.defineFunc(n, scope)
		b.walkWhereClauses(n.WhereClauses, inner)
		child := NewScope(inner)
		b.defineParams(n.Params, child, n)
		if n.Body != nil {
			// Body scope spans the body block; the type-param scope (when the
			// fn is generic) spans the whole declaration so `<T>` resolves
			// across signature + body.
			setScopeSpan(child, n.Body.Line, n.Body.Col, n.Body.EndLine, n.Body.EndCol)
			if inner != scope {
				setScopeSpan(inner, n.Line, n.Col, n.Body.EndLine, n.Body.EndCol)
			}
			b.walkBlock(n.Body, child)
		}

	case *ast.ImplBlock:
		from := len(b.file.TypeErrors)
		b.walkImplBlockBodies(n, scope)
		repointSynthErrors(n, b.file.TypeErrors[from:])

	case *ast.StructDef:
		b.defineStruct(n, scope)

	case *ast.EnumDef:
		b.defineEnum(n, scope)

	case *ast.TypeDef:
		b.defineTypeDef(n, scope)

	case *ast.TypeAlias:
		b.defineTypeAlias(n, scope)

	case *ast.ExternType:
		inner := b.defineTypeParams(n.TypeParams, scope)
		b.walkNamespaceItems(n.Items, inner)

	case *ast.InterfaceDef:
		// defineInterface handles symbol registration + method type-expr
		// walking. Then walk default-impl bodies (interfaces with method
		// bodies) in fresh per-method scopes chained off the inner scope so
		// type params remain visible.
		ifaceInner := b.defineInterface(n, scope)
		// When the interface is generic, its type-param scope spans the whole
		// declaration so default-method bodies can resolve `<T>`.
		if ifaceInner != scope {
			setScopeSpan(ifaceInner, n.Line, n.Col, n.EndLine, n.EndCol)
		}
		for i := range n.Methods {
			m := &n.Methods[i]
			if m.Body == nil {
				continue
			}
			methodInner := b.defineTypeParams(m.TypeParams, ifaceInner)
			b.walkWhereClauses(m.WhereClauses, methodInner)
			child := NewScope(methodInner)
			// Walk param type annotations and default values in the interface
			// method scope (not param scope), before defining the params — a
			// default can't reference a sibling param. This records references
			// for names used in defaults (e.g. `loud: Bool = False`), so the
			// checker's validation of default values
			// (checkInterfaceDefaultParamDefaults) can resolve them; without it
			// bare-name defaults read as undefined.
			for _, p := range m.Params {
				b.walkTypeExpr(p.TypeAnnotation, methodInner)
				if p.Default != nil {
					b.walkNode(p.Default, methodInner)
				}
			}
			for _, p := range m.Params {
				if p.Destructure != nil {
					b.defineParamPattern(p.Destructure, child, m)
					continue
				}
				sym := &Symbol{
					Name: p.Name,
					Kind: SymbolParam,
					Pos:  Pos{Line: p.Line, Col: p.Col},
					Node: m,
				}
				if !ast.IsDiscardName(p.Name) {
					child.Define(sym)
				}
				if p.Line > 0 {
					b.file.Definitions[sym.Pos] = sym
				}
			}
			if block, ok := m.Body.(*ast.Block); ok {
				setScopeSpan(child, block.Line, block.Col, block.EndLine, block.EndCol)
				if methodInner != ifaceInner {
					setScopeSpan(methodInner, m.Line, m.Col, block.EndLine, block.EndCol)
				}
				b.walkBlock(block, child)
			} else {
				b.walkNode(m.Body, child)
			}
		}
	case *ast.Lambda:
		child := NewScope(scope)
		setScopeSpan(child, n.Line, n.Col, n.EndLine, n.EndCol)
		b.defineParams(n.Params, child, n)
		for _, p := range n.Params {
			b.walkTypeExpr(p.TypeAnnotation, child)
		}
		if n.Body != nil {
			b.walkBlock(n.Body, child)
		}

	case *ast.If:
		b.walkNode(n.Cond, scope)
		if n.Then != nil {
			if n.CondPattern != nil {
				child := NewScope(scope)
				setScopeSpan(child, n.Line, n.Col, blockEndLine(n.Then), blockEndCol(n.Then))
				b.definePatternIn(n.CondPattern, child, n.Then)
				b.walkBlock(n.Then, child)
			} else {
				b.walkBlock(n.Then, scope)
			}
		}
		if n.Else != nil {
			b.walkNode(n.Else, scope)
		}

	case *ast.Case:
		if n.Value != nil {
			b.walkNode(n.Value, scope)
		}
		for _, branch := range n.Branches {
			child := NewScope(scope)
			setScopeSpan(child, branch.Line, branch.Col, branch.EndLine, branch.EndCol)
			b.definePatternIn(branch.Pattern, child, branch.Body)
			// Walk the pattern as an expression too — for ad-hoc conditionals
			// (no value), the pattern IS an expression containing identifiers
			// that need reference resolution (e.g., x > 10 -> "big").
			b.walkNode(branch.Pattern, child)
			if branch.Guard != nil {
				b.walkNode(branch.Guard, child)
			}
			if branch.Body != nil {
				b.walkNode(branch.Body, child)
			}
		}

	case *ast.Block:
		b.walkBlock(n, scope)

	case *ast.GroupedExpr:
		b.walkNode(n.Expr, scope)

	case *ast.ConcurrentBlock:
		// `concurrent { body }` is the structured-concurrency scope —
		// for the symbol/reference walk it's a child scope wrapping
		// the body, same as a lambda body. The structural rules
		// (spawn-inside-concurrent, task-must-be-awaited,
		// task-cannot-escape) live in analysis/concurrent_scope.go
		// and analysis/task_lifetime.go, which run as separate passes
		// after BuildTypes/CheckTypes.
		if n.Body != nil {
			child := NewScope(scope)
			setScopeSpan(child, n.Body.Line, n.Body.Col, n.Body.EndLine, n.Body.EndCol)
			b.walkBlock(n.Body, child)
		}

	case *ast.Binding:
		// Walk value first, then define (handled in walkBlock for blocks)
		b.walkNode(n.Value, scope)

	case *ast.PatternBinding:
		b.walkPatternBinding(n, scope)

	case *ast.With:
		// The target is an owner-qualified field access and the value an
		// ordinary expression.
		b.walkNode(n.Target, scope)
		b.walkNode(n.Value, scope)

	case *ast.Defer:
		b.walkNode(n.Call, scope)

	case *ast.Assertion:
		b.walkNode(n.Expr, scope)

	case *ast.Dbg:
		b.walkNode(n.Expr, scope)

	case *ast.TestDecl:
		b.walkTestDecl(n, scope)

	case *ast.TupleDestructure:
		b.walkNode(n.Value, scope)

	case *ast.StructDestructure:
		b.walkNode(n.Value, scope)

	case *ast.MapDestructure:
		b.walkNode(n.Value, scope)

	case *ast.DistinctDestructure:
		// Register a Reference at the type-name position (`Id` in
		// `Id(unwrapped) = id`) so hover shows the distinct type.
		if n.TypeNameExpr != nil {
			b.walkTypeExpr(n.TypeNameExpr, scope)
		} else if sym := scope.Lookup(n.TypeName); sym != nil {
			b.file.References[Pos{Line: n.Line, Col: n.Col}] = sym
		}
		b.walkNode(n.Value, scope)

	case *ast.Ident:
		sym := scope.Lookup(n.Name)
		if sym != nil {
			b.file.References[Pos{Line: n.Line, Col: n.Col}] = sym
		}

	case *ast.TypeIdent:
		sym := scope.Lookup(n.Name)
		if sym != nil {
			b.file.References[Pos{Line: n.Line, Col: n.Col}] = sym
		}

	case *ast.Call:
		b.walkNode(n.Func, scope)
		// Turbofish type args are type references — resolve them so the LSP
		// registers hover / go-to-def on each (e.g. `Int` in `f<Int>(…)`).
		for _, ta := range n.TypeArgs {
			b.walkTypeExpr(ta, scope)
		}
		for _, arg := range n.Args {
			b.walkNode(arg, scope)
		}

	case *ast.Binary:
		b.walkNode(n.Left, scope)
		b.walkNode(n.Right, scope)

	case *ast.Unary:
		b.walkNode(n.Right, scope)

	case *ast.FieldAccess:
		b.walkNode(n.Object, scope)
		b.recordImportedTypeQualifiedAccess(n, scope)
		// Resolve qualified names: io.inspect, Iter.map, etc.
		// When a local binding in scope shadows the module name, preserve the
		// local reference for the object identifier so the checker can resolve
		// struct/enum field access against the shadowing value's actual type.
		// The qualified-member reference is still registered so the checker's
		// fallback (and hover/go-to-def) can fall back to the module export when
		// the local binding has no such field.
		{
			var moduleName string
			switch obj := n.Object.(type) {
			case *ast.Ident:
				moduleName = obj.Name
			case *ast.TypeIdent:
				moduleName = obj.Name
			}
			if moduleName != "" {
				// Resolve which module-scope this name refers to. lookupModuleScope
				// handles the full set: closest binding when it is a
				// SymbolModule with a ModuleScope; the function-
				// shadow scope-walk past a local binding; b.imported as the final
				// fallback for same-scope shadowing. There is no silent fallback to
				// `b.modules` — module access in user code requires explicit
				// visibility (prelude or import). `b.modules` itself is still
				// used elsewhere (resolveStdlibImport, defineImport, qualified
				// type references).
				modScope := b.lookupModuleScope(moduleName, scope)
				if modScope != nil {
					// When the object identifier resolves to a SymbolModule in
					// scope, register references for both the object name (so
					// hover lands on the module) and the qualified member (so
					// hover lands on the member symbol). Other identifier kinds
					// — values, functions, etc. — do not get speculative member
					// registration; their dot-syntax is plain field access,
					// resolved by the checker against the value's type.
					if localSym := scope.Lookup(moduleName); localSym != nil && localSym.Kind == SymbolModule {
						refSym := localSym
						// A block's own `import std/io` binds `io` in that
						// block only; its use resolves to that binding,
						// whatever this project's module table holds under
						// the name.
						if modSym, ok := b.moduleSyms[moduleName]; ok && !b.isBlockImportBinding(localSym, scope) {
							refSym = modSym
						}
						if refSym != nil {
							switch obj := n.Object.(type) {
							case *ast.Ident:
								b.file.References[Pos{Line: obj.Line, Col: obj.Col}] = refSym
							case *ast.TypeIdent:
								b.file.References[Pos{Line: obj.Line, Col: obj.Col}] = refSym
							}
						}
						if sym := modScope.LookupLocal(n.Field.Name); sym != nil {
							b.file.References[Pos{Line: n.Field.Line, Col: n.Field.Col}] = sym
						}
					}
				}
			}
		}
		// Resolve enum-qualified names: Direction.North, Option.Some, etc.
		// Always look the variant up through the named enum's Members map.
		// Variants for in-file enums are also defined in the enclosing scope
		// for unqualified use, but `EnumName.Variant` must dispatch through
		// the enum being qualified — a bare-name scope lookup would happily
		// return any same-name variant from a different enum in the file
		// (e.g. resolving `Status.Done` to a sibling `Command.Done` defined
		// at module level). Selective imports route through `Resolved` so
		// the imported enum's Members are reachable.
		if obj, ok := n.Object.(*ast.TypeIdent); ok {
			if enumSym := scope.Lookup(obj.Name); enumSym != nil && enumSym.Kind == SymbolEnum {
				real := enumSym
				if real.Resolved != nil {
					real = real.Resolved
				}
				b.file.References[Pos{Line: obj.Line, Col: obj.Col}] = real
				if vs, ok := real.Members[n.Field.Name]; ok && vs.Kind == SymbolEnumVariant {
					b.file.References[Pos{Line: n.Field.Line, Col: n.Field.Col}] = vs
				}
			}
		}
		if obj, ok := n.Object.(*ast.TypeIdent); ok {
			if typeSym := scope.Lookup(obj.Name); typeSym != nil {
				real := typeSym
				if real.Resolved != nil {
					real = real.Resolved
				}
				b.file.References[Pos{Line: obj.Line, Col: obj.Col}] = real
				if real.Members != nil {
					if member, ok := real.Members[n.Field.Name]; ok {
						switch member.Kind {
						case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
							b.file.References[Pos{Line: n.Field.Line, Col: n.Field.Col}] = member
						}
					}
				}
			}
		}
		// Resolve interface-qualified names: Greeting.greet, Showable.show, etc.
		// The reference targets the interface method itself, not any specific
		// impl: at the syntactic level `Iface.method(...)` describes an abstract
		// method call whose concrete dispatch depends on the argument's type at
		// the call site. Picking one impl at build time gave wrong hover in
		// generic code (where no impl is statically chosen) and was arbitrary
		// when multiple impls existed (last-write-wins on the implMethods map).
		// Default-method bodies provided by the interface itself are exposed as
		// the interface method symbol too, so default dispatch still resolves.
		if obj, ok := n.Object.(*ast.TypeIdent); ok {
			if ifaceSym := scope.Lookup(obj.Name); ifaceSym != nil && ifaceSym.Kind == SymbolInterface {
				real := ifaceSym
				if real.Resolved != nil {
					real = real.Resolved
				}
				b.file.References[Pos{Line: obj.Line, Col: obj.Col}] = real
				if methodSym, ok := real.Members[n.Field.Name]; ok {
					b.file.References[Pos{Line: n.Field.Line, Col: n.Field.Col}] = methodSym
				}
			}
		}
		// Type-parameter-qualified method: `T.method` where T is a generic
		// parameter bounded by an interface. Point the method-field reference
		// at the bound interface's method symbol — the same target the
		// interface-qualified form `Iface.method` resolves to — so hover and
		// go-to-def on the method work. The `T` qualifier itself keeps its own
		// type-parameter reference (recorded via walkTypeExpr on the bound),
		// so resolving the method here doesn't disturb hover on `T`. Method-
		// aware: only a bound that actually declares the method is used; an
		// ambiguous call across two bounds is a checker error, so recording
		// the first match here is immaterial.
		if obj, ok := n.Object.(*ast.TypeIdent); ok {
			if tpSym := scope.Lookup(obj.Name); tpSym != nil && len(tpSym.TypeParamBounds) > 0 {
				for _, boundName := range tpSym.TypeParamBounds {
					ifaceSym := scope.Lookup(boundName)
					if ifaceSym == nil || ifaceSym.Kind != SymbolInterface {
						continue
					}
					real := ifaceSym
					if real.Resolved != nil {
						real = real.Resolved
					}
					if methodSym, ok := real.Members[n.Field.Name]; ok {
						b.file.References[Pos{Line: n.Field.Line, Col: n.Field.Col}] = methodSym
						break
					}
				}
			}
		}

	case *ast.ExprStmt:
		b.walkNode(n.Expr, scope)

	case *ast.Return:
		if n.Value != nil {
			b.walkNode(n.Value, scope)
		}

	case *ast.Break:
		if n.Value != nil {
			b.walkNode(n.Value, scope)
		}

	case *ast.Continue:
		// continue takes no value; the checker rejects one that was
		// written, and walking it keeps its names resolved meanwhile.
		if n.Value != nil {
			b.walkNode(n.Value, scope)
		}

	case *ast.TryOp:
		b.walkNode(n.Expr, scope)

	case *ast.Then:
		b.walkNode(n.Lambda, scope)

	case *ast.Tap:
		b.walkNode(n.Lambda, scope)

	case *ast.StructLit:
		b.walkTypeExpr(n.TypeName, scope)
		// Register the field name's source position as a Reference to the
		// struct's field Symbol so hover on `x` in `Point{x: 1}` shows the
		// field's declared type. Resolution: SimpleType points to the
		// struct directly; QualifiedType (e.g. Error.HttpError{...}) goes
		// through the variant in the enum's Members.
		b.registerStructLitFieldRefs(n, scope)
		// The struct-update spread's head is an ordinary expression, so
		// it binds and resolves like any other — this is what makes
		// `{..p, x: 9}` count as a read of `p`, and what gives hover on
		// the head.
		if n.Spread != nil {
			b.walkNode(n.Spread, scope)
		}
		for _, f := range n.Fields {
			b.walkNode(f.Value, scope)
		}

	case *ast.ListLit:
		for _, item := range n.Items {
			b.walkNode(item, scope)
		}

	case *ast.VectorLit:
		for _, item := range n.Items {
			b.walkNode(item, scope)
		}

	case *ast.SetLit:
		for _, item := range n.Items {
			b.walkNode(item, scope)
		}

	case *ast.RangeLit:
		if n.Start != nil {
			b.walkNode(n.Start, scope)
		}
		if n.End != nil {
			b.walkNode(n.End, scope)
		}

	case *ast.TupleLit:
		for _, item := range n.Items {
			b.walkNode(item, scope)
		}

	case *ast.MapLit:
		// Type-prefixed map literals (`Kvs{"a" => 1}`) attach a TypeName for
		// the map-distinct's name; resolve it like StructLit does so hover
		// and goto-definition work on the type name.
		if n.TypeName != nil {
			b.walkTypeExpr(n.TypeName, scope)
		}
		for _, entry := range n.Entries {
			b.walkNode(entry.Key, scope)
			b.walkNode(entry.Value, scope)
		}

	case *ast.NamedArg:
		b.walkNode(n.Value, scope)

	case *ast.StringInterp:
		for _, part := range n.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				b.walkNode(se.Expr, scope)
			}
		}

	case *ast.TaggedString:
		// Resolve the tag identifier as a module reference using the same
		// scope-chain + b.imported fallback that FieldAccess uses for
		// `<module>.<member>` lookups. The full type-check (signature
		// shape, slot conformance, return type) lives in checker.go's
		// checkTaggedString — here we just register the references the
		// LSP / hover need, and walk slot expressions so identifiers
		// inside them get resolved.
		b.resolveTaggedStringTag(n, scope)
		for _, part := range n.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				b.walkNode(se.Expr, scope)
			}
		}

	case *ast.ListSpreadLit:
		for _, head := range n.Heads {
			b.walkNode(head, scope)
		}
		if n.TailSpread != nil {
			b.walkNode(n.TailSpread, scope)
		}

	case *ast.ImportStmt:
		// Nested import (inside a function body): nested=true to avoid
		// re-keying b.modules globally. The alias is only visible via the
		// Symbol registered in the local scope.
		if b.blockImports == nil {
			b.blockImports = map[*ast.ImportStmt]bool{}
		}
		b.blockImports[n] = true
		if b.file.BlockImportScopes == nil {
			b.file.BlockImportScopes = map[*ast.ImportStmt]*Scope{}
		}
		b.file.BlockImportScopes[n] = scope
		b.defineImport(n, scope, true /* nested */)

	// Nodes that need no traversal: literals, patterns (handled via definePattern),
	// Placeholder, WildcardPattern. (StructDef/EnumDef/TypeDef/ExternType/
	// TypeAlias/InterfaceDef/ImportStmt are handled above for nested-decl support.)
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.CodepointLit, *ast.StringLit, *ast.Placeholder,
		*ast.WildcardPattern,
		*ast.IdentPattern, *ast.AsPattern, *ast.EnumPattern, *ast.StructPattern,
		*ast.TuplePattern, *ast.ListPattern, *ast.MapPattern,
		*ast.ExternFunc, *ast.ExternPackage:
		// nothing to walk
	}
}

func (b *builder) walkAttachedTests(node ast.Node, scope *Scope) {
	for _, test := range attachedTestsOf(node) {
		if test.Body == nil {
			continue
		}
		child := NewScope(scope)
		setScopeSpan(child, test.Body.Line, test.Body.Col, blockEndLine(test.Body), blockEndCol(test.Body))
		b.walkBlock(test.Body, child)
	}
}

// resolveTaggedStringTag looks up a TaggedString's tag identifier in
// the regular name-lookup scope chain and registers a Reference for
// hover / go-to-def at the tag identifier's source position. The
// TaggedString AST node's Line/Col is the opening quote (the lexer
// captures position at the `"` after consuming the tag identifier);
// the tag itself starts `len(n.Tag)` columns earlier.
//
// The tag must name a type in scope whose handler is an
// `impl Literal for <Tag> { fn from_fragments }` block. The impl's
// method FuncTypes aren't populated until after BuildTypes, so this pass
// only registers a Reference at the type symbol; checker.checkTaggedString
// resolves the impl, runs the List<Fragment<I>> -> R contract + slot
// check, and emits the "no handler" diagnostic when the impl is absent.
func (b *builder) resolveTaggedStringTag(n *ast.TaggedString, scope *Scope) {
	tagLine := n.Line
	tagCol := max(n.Col-len(n.Tag), 1)
	tagPos := Pos{Line: tagLine, Col: tagCol}
	sym := scope.Lookup(n.Tag)
	if sym == nil {
		b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
			Line:    tagLine,
			Col:     tagCol,
			Message: fmt.Sprintf("tag '%s' is not in scope — a typed literal `%s\"...\"` requires `%s` to be a type with an `impl Literal for %s` block", n.Tag, n.Tag, n.Tag, n.Tag),
		})
		return
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	switch real.Kind {
	case SymbolStruct, SymbolEnum, SymbolType:
		// The tag names a type; its handler is an
		// `impl Literal for <Tag>` block, resolved by the checker.
		b.file.References[tagPos] = &Symbol{
			Name: n.Tag,
			Kind: real.Kind,
			Pos:  tagPos,
			Node: real.Node,
		}
	default:
		if kind, ok := typedLiteralTagKind(real.Type, n.Tag); ok {
			// Selectively imported types can reach this point as import
			// bindings whose Type carries the resolved named type. Register a
			// type-shaped proxy so the checker treats the tag the same as a
			// directly visible type declaration.
			b.file.References[tagPos] = &Symbol{
				Name: n.Tag,
				Kind: kind,
				Pos:  tagPos,
				Node: real.Node,
				Type: real.Type,
			}
			return
		}
		b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
			Line:    tagLine,
			Col:     tagCol,
			Message: fmt.Sprintf("'%s' is a %s, not a typed-literal tag — a tag must be a type with an `impl Literal for %s` block", n.Tag, kindLabel(real), n.Tag),
		})
	}
}

func typedLiteralTagKind(ty Type, tag string) (SymbolKind, bool) {
	switch t := ty.(type) {
	case *StructType:
		return SymbolStruct, t.Name == tag
	case *EnumType:
		return SymbolEnum, t.Name == tag
	case *DistinctType:
		return SymbolType, t.Name == tag
	default:
		return SymbolBinding, false
	}
}

// isBlockImportBinding reports that sym, as scope sees it, is bound by an
// `import` at the top of a block rather than at file level.
func (b *builder) isBlockImportBinding(sym *Symbol, scope *Scope) bool {
	if _, isImport := sym.Node.(*ast.ImportStmt); !isImport {
		return false
	}
	for s := scope; s != nil; s = s.Parent {
		if s.Symbols[sym.Name] == sym {
			return s != b.file.ModuleScope
		}
	}
	return false
}

// lookupModuleScope returns the *Scope of the module bound to `name` in
// the given scope, or nil if no such module is reachable. It mirrors the
// resolution path FieldAccess uses for `<module>.<member>`: closest
// scope binding when it is itself a SymbolModule with a ModuleScope,
// then walk up the scope chain past any shadow looking for a parent
// SymbolModule with the same name, then b.imported as the final
// fallback for same-scope shadowing.
func (b *builder) lookupModuleScope(name string, scope *Scope) *Scope {
	localSym := scope.Lookup(name)
	if ms := moduleScopeOfSym(localSym); ms != nil {
		return ms
	}
	if localSym != nil {
		for s := scope; s != nil; s = s.Parent {
			if sym, ok := s.Symbols[name]; ok && sym != localSym {
				if ms := moduleScopeOfSym(sym); ms != nil {
					return ms
				}
			}
		}
		if b.imported != nil {
			if imp, ok := b.imported[name]; ok {
				return imp
			}
		}
	}
	return nil
}

// moduleScopeOfSym returns sym's ModuleScope when sym is a file API object
// (a SymbolModule), or nil.
func moduleScopeOfSym(sym *Symbol) *Scope {
	if sym == nil || sym.Kind != SymbolModule {
		return nil
	}
	return sym.ModuleScope
}

// walkBlock walks statements in a block, handling Binding specially:
// the value is walked first, then the binding is defined.
func (b *builder) walkBlock(block *ast.Block, scope *Scope) {
	b.walkBlockWithEnd(block, scope, blockEndLine(block), blockEndCol(block))
}

func (b *builder) walkBlockWithEnd(block *ast.Block, scope *Scope, endLine, endCol int) *Scope {
	if block == nil {
		return scope
	}
	if te, ok := checkUnreachable(block); ok {
		b.file.TypeErrors = append(b.file.TypeErrors, te)
	}
	for _, stmt := range block.Stmts {
		switch s := stmt.(type) {
		case *ast.Binding:
			// Walk type annotation first so its identifiers (Int, Map, etc.)
			// land in fa.References before any same-name shadow defined by
			// the binding could mask them.
			if s.TypeAnnotation != nil {
				b.walkTypeExpr(s.TypeAnnotation, scope)
			}
			// Walk value in current scope.
			b.walkNode(s.Value, scope)
			// Then define the binding so later statements can see it.
			sym := &Symbol{
				Name: s.Name,
				Kind: SymbolBinding,
				Pos:  Pos{Line: s.Line, Col: s.Col},
				Node: s,
			}
			if !ast.IsDiscardName(s.Name) {
				if !b.checkRedeclareInScopeBinding(scope, s.Name, s.Line, s.Col) {
					scope.Define(sym)
				}
			}
			b.file.Definitions[sym.Pos] = sym

		case *ast.Defer:
			b.walkNode(s.Call, scope)

		case *ast.TestDecl:
			b.walkTestDecl(s, scope)

		case *ast.TupleDestructure:
			b.walkNode(s.Value, scope)
			for _, binding := range s.Bindings {
				if binding != nil {
					sym := &Symbol{
						Name: binding.Name,
						Kind: SymbolBinding,
						Pos:  Pos{Line: binding.Line, Col: binding.Col},
						Node: s,
					}
					if !ast.IsDiscardName(binding.Name) {
						scope.Define(sym)
					}
					b.file.Definitions[sym.Pos] = sym
				}
			}

		case *ast.StructDestructure:
			b.walkNode(s.Value, scope)
			for _, f := range s.Fields {
				if f.Binding != "" {
					line := structPatternFieldBindingLine(f, s.Line)
					sym := &Symbol{
						Name: f.Binding,
						Kind: SymbolBinding,
						Pos:  Pos{Line: line, Col: f.BindingCol},
						Node: s,
					}
					if !ast.IsDiscardName(f.Binding) {
						scope.Define(sym)
					}
					b.file.Definitions[sym.Pos] = sym
				}
			}

		case *ast.PatternDestructure:
			b.walkNode(s.Value, scope)
			b.definePatternRoot(s.Pattern, scope, s)

		case *ast.PatternBinding:
			b.walkPatternBinding(s, scope)

		case *ast.MapDestructure:
			b.walkNode(s.Value, scope)
			for _, entry := range s.Entries {
				b.walkNode(entry.Key, scope)
			}
			for _, entry := range s.Entries {
				if ip, ok := entry.Pattern.(*ast.IdentPattern); ok {
					sym := &Symbol{
						Name: ip.Name,
						Kind: SymbolBinding,
						Pos:  Pos{Line: ip.Line, Col: ip.Col},
						Node: s,
					}
					if !ast.IsDiscardName(ip.Name) {
						scope.Define(sym)
					}
					b.file.Definitions[sym.Pos] = sym
				}
			}

		case *ast.DistinctDestructure:
			// Register a Reference at the type-name position (`Id` in
			// `Id(unwrapped) = id`) so hover shows the distinct type.
			if s.TypeNameExpr != nil {
				b.walkTypeExpr(s.TypeNameExpr, scope)
			} else if sym := scope.Lookup(s.TypeName); sym != nil {
				b.file.References[Pos{Line: s.Line, Col: s.Col}] = sym
			}
			b.walkNode(s.Value, scope)
			if s.Binding != nil {
				sym := &Symbol{
					Name: s.Binding.Name,
					Kind: SymbolBinding,
					Pos:  Pos{Line: s.Binding.Line, Col: s.Binding.Col},
					Node: s,
				}
				if !ast.IsDiscardName(s.Binding.Name) {
					scope.Define(sym)
				}
				b.file.Definitions[sym.Pos] = sym
			}

		default:
			b.walkNode(stmt, scope)
		}
	}
	return scope
}

// walkPatternBinding resolves `Pattern = value [else { ... }]`. The value and
// the else run before the pattern's names exist, so neither sees them; the
// names are then defined in scope for the statements after the binding. Each
// else arm is its own scope, as a case arm is.
func (b *builder) walkPatternBinding(n *ast.PatternBinding, scope *Scope) {
	b.walkNode(n.Value, scope)
	if e := n.Else; e != nil {
		if e.Block != nil {
			child := NewScope(scope)
			setScopeSpan(child, e.Block.Line, e.Block.Col, e.Block.EndLine, e.Block.EndCol)
			b.walkBlock(e.Block, child)
		}
		for _, arm := range e.Arms {
			child := NewScope(scope)
			setScopeSpan(child, arm.Line, arm.Col, arm.EndLine, arm.EndCol)
			b.definePatternIn(arm.Pattern, child, arm.Body)
			b.walkNode(arm.Pattern, child)
			if arm.Guard != nil {
				b.walkNode(arm.Guard, child)
			}
			if arm.Body != nil {
				b.walkNode(arm.Body, child)
			}
		}
	}
	b.definePatternRoot(n.Pattern, scope, n)
}

func blockEndLine(block *ast.Block) int {
	if block == nil {
		return 0
	}
	return block.EndLine
}

func blockEndCol(block *ast.Block) int {
	if block == nil {
		return 0
	}
	return block.EndCol
}

func (b *builder) walkTestDecl(n *ast.TestDecl, scope *Scope) {
	if n.Body == nil {
		return
	}
	child := NewScope(scope)
	setScopeSpan(child, n.Body.Line, n.Body.Col, n.Body.EndLine, n.Body.EndCol)
	if !n.Group {
		b.definePatternIn(n.ContextPattern, child, n.Body)
		b.walkBlock(n.Body, child)
		return
	}
	// The clock expression is ordinary code naming a testing.Clock
	// variant, so it gets ordinary scope resolution: `clock Clock.Virtual`
	// without importing std/testing is an unknown name here exactly as it
	// would be anywhere else.
	if n.Clock != nil {
		b.walkNode(n.Clock, child)
	}
	if n.Boot != nil {
		// The `boot` line's call is ordinary code in the group's scope.
		b.walkNode(n.Boot, child)
	}
	if n.Setup != nil {
		setupScope := NewScope(child)
		if block, ok := n.Setup.(*ast.Block); ok {
			setScopeSpan(setupScope, block.Line, block.Col, block.EndLine, block.EndCol)
		} else {
			setScopeSpan(setupScope, n.Setup.LineNum(), 1, n.Setup.LineNum(), 1)
		}
		b.walkNode(n.Setup, setupScope)
	}
	for _, stmt := range n.Body.Stmts {
		b.walkNode(stmt, child)
	}
}

// setScopeSpan records a lexical scope's source extent so FileAnalysis.ScopeAt
// can resolve a cursor into it. Called only from the bodies pass; signature-
// pass scaffolding scopes are deliberately left span-less and thus skipped by
// ScopeAt. A zero endLine (e.g. an as-yet-unspanned construct) leaves the scope
// span-less rather than recording a bogus extent.
func setScopeSpan(s *Scope, startLine, startCol, endLine, endCol int) {
	if s == nil || endLine == 0 {
		return
	}
	s.Start = Pos{Line: startLine, Col: startCol}
	s.End = Pos{Line: endLine, Col: endCol}
}

func (b *builder) defineParams(params []ast.Param, scope *Scope, parentNode ast.Node) {
	for _, p := range params {
		// A parameter's default is evaluated before that parameter is bound.
		if p.Default != nil {
			b.walkNode(p.Default, scope)
		}
		if p.Destructure != nil {
			b.defineParamPattern(p.Destructure, scope, parentNode)
			continue
		}
		sym := &Symbol{
			Name:            p.Name,
			Kind:            SymbolParam,
			Pos:             Pos{Line: p.Line, Col: p.Col},
			Node:            parentNode,
			ReceiverDisplay: scope.ReceiverTypeDisplay(),
		}
		if !ast.IsDiscardName(p.Name) {
			scope.Define(sym)
		}
		if p.Line > 0 {
			b.file.Definitions[sym.Pos] = sym
		}
	}
}

func structPatternFieldBindingLine(f ast.StructPatternField, fallback int) int {
	if f.BindingLine > 0 {
		return f.BindingLine
	}
	if f.NameLine > 0 {
		return f.NameLine
	}
	return fallback
}

func (b *builder) definePattern(node ast.Node, scope *Scope) {
	b.definePatternRoot(node, scope, nil)
}

// definePatternRoot binds a whole pattern's names (definePatternOwned) and
// reports a name it binds twice: `(a, a)` and `Some(n) as n` would leave the
// first binding unreachable.
func (b *builder) definePatternRoot(node ast.Node, scope *Scope, owner ast.Node) {
	b.definePatternOwned(node, scope, owner)
	seen := map[string]bool{}
	for _, site := range patternSites(node) {
		if ast.IsDiscardName(site.name) {
			continue
		}
		if seen[site.name] {
			b.file.TypeErrors = append(b.file.TypeErrors, TypeError{
				Line: site.pos.Line, Col: site.pos.Col, EndLine: site.pos.Line, EndCol: site.pos.Col + len(site.name),
				Message: fmt.Sprintf("`%s` is bound twice in one pattern; give each binding its own name", site.name),
			})
		}
		seen[site.name] = true
	}
}

// definePatternIn binds the pattern of a `case` arm, an `else` arm, an `if`
// condition or a test's setup binding, whose names are in scope in body
// (Symbol.PatternBody).
func (b *builder) definePatternIn(node ast.Node, scope *Scope, body ast.Node) {
	prev := b.patternBodyOf
	b.patternBodyOf = body
	defer func() { b.patternBodyOf = prev }()
	b.definePattern(node, scope)
}

// defineParamPattern binds a destructured parameter of fn, a function, lambda
// or interface method, marking each name it binds as that function's
// (Symbol.ParamOf).
func (b *builder) defineParamPattern(node ast.Node, scope *Scope, fn ast.Node) {
	prev := b.paramPatternOf
	b.paramPatternOf = fn
	defer func() { b.paramPatternOf = prev }()
	b.definePattern(node, scope)
}

func (b *builder) definePatternOwned(node ast.Node, scope *Scope, owner ast.Node) {
	if node == nil {
		return
	}
	bindingNode := node
	if owner != nil {
		bindingNode = owner
	}

	switch n := node.(type) {
	case *ast.IdentPattern:
		sym := &Symbol{
			Name:            n.Name,
			Kind:            SymbolBinding,
			Pos:             Pos{Line: n.Line, Col: n.Col},
			Node:            bindingNode,
			ReceiverDisplay: scope.ReceiverTypeDisplay(),
			ParamOf:         b.paramPatternOf,
			PatternBody:     b.patternBodyOf,
		}
		if !ast.IsDiscardName(n.Name) {
			scope.Define(sym)
		}
		b.file.Definitions[sym.Pos] = sym

	case *ast.AsPattern:
		// The inner pattern's names first, then the name for the whole
		// value: source order, so a duplicate reports at the later one.
		b.definePatternOwned(n.Pattern, scope, owner)
		sym := &Symbol{
			Name:            n.Name,
			Kind:            SymbolBinding,
			Pos:             Pos{Line: n.NameLine, Col: n.NameCol},
			Node:            bindingNode,
			ReceiverDisplay: scope.ReceiverTypeDisplay(),
			ParamOf:         b.paramPatternOf,
			PatternBody:     b.patternBodyOf,
		}
		if !ast.IsDiscardName(n.Name) {
			scope.Define(sym)
		}
		b.file.Definitions[sym.Pos] = sym

	case *ast.Binary:
		b.definePatternOwned(n.Left, scope, owner)
		b.definePatternOwned(n.Right, scope, owner)

	case *ast.EnumPattern:
		b.walkTypeExpr(n.Variant, scope)
		if n.Payload != nil {
			b.definePatternOwned(n.Payload, scope, owner)
		} else if n.Binding != "" {
			sym := &Symbol{
				Name:            n.Binding,
				Kind:            SymbolBinding,
				Pos:             Pos{Line: n.Line, Col: n.BindingCol},
				Node:            bindingNode,
				ReceiverDisplay: scope.ReceiverTypeDisplay(),
				ParamOf:         b.paramPatternOf,
				PatternBody:     b.patternBodyOf,
			}
			if !ast.IsDiscardName(n.Binding) {
				scope.Define(sym)
			}
			b.file.Definitions[sym.Pos] = sym
		}

	case *ast.StructPattern:
		b.walkTypeExpr(n.TypeName, scope)
		for _, f := range n.Fields {
			if f.Pattern != nil {
				b.definePatternOwned(f.Pattern, scope, owner)
			} else if f.Binding != "" {
				line := structPatternFieldBindingLine(f, n.Line)
				sym := &Symbol{
					Name:            f.Binding,
					Kind:            SymbolBinding,
					Pos:             Pos{Line: line, Col: f.BindingCol},
					Node:            bindingNode,
					ReceiverDisplay: scope.ReceiverTypeDisplay(),
					ParamOf:         b.paramPatternOf,
					PatternBody:     b.patternBodyOf,
				}
				if !ast.IsDiscardName(f.Binding) {
					scope.Define(sym)
				}
				b.file.Definitions[sym.Pos] = sym
			}
		}

	case *ast.TuplePattern:
		for _, p := range n.Patterns {
			b.definePatternOwned(p, scope, owner)
		}

	case *ast.ListPattern:
		for _, h := range n.Heads {
			b.definePatternOwned(h, scope, owner)
		}
		if n.TailSpread != nil {
			b.definePatternOwned(n.TailSpread, scope, owner)
		}

	case *ast.MapPattern:
		// Type-prefixed map patterns (`Kvs{"a" => v}`) resolve the type
		// name like StructPattern does — supports hover/goto on the prefix.
		if n.TypeName != nil {
			b.walkTypeExpr(n.TypeName, scope)
		}
		for _, entry := range n.Entries {
			b.walkNode(entry.Key, scope)
		}
		for _, entry := range n.Entries {
			b.definePatternOwned(entry.Pattern, scope, owner)
		}
	}
}
