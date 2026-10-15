package analysis

import (
	"maps"
	"slices"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ast"
)

// FileAnalysis holds the analysis results for a single file, in two parts.
//
// FileDecls is what the file declares: its module scope and the symbols in
// it, its types and signatures, its impl headers and the methods they bring.
// A project build makes it in Sweep A, Sweep B-ann and B-bounds, and then,
// after Sweep B-bodies has walked the bodies, in the type shells and
// BuildTypes' work on declarations. Walking and checking a body never
// changes it.
//
// FileBodies is what walking and checking the file's bodies found: every
// reference and definition inside a body, the type the checker gave every
// expression, the demands for impls the bodies make, the imports nothing
// names, and the diagnostics they draw. It can be thrown away and made again
// against FileDecls and the program's other files (rebuildBodies).
//
// Six fields of FileBodies start with the declarations' share, which the
// bodies then add to: References, Definitions, PunnedFieldLabels,
// ImplManifest (a derive's demands), TypeErrors, and ImportedModules (an
// import at the top of a block records the module it reaches). A project
// build keeps each share (keptDecls) for a rebuild to start from.
type FileAnalysis struct {
	FileDecls
	FileBodies
}

// FileDecls is the declaration part of a file's analysis. See FileAnalysis.
type FileDecls struct {
	ModuleScope *Scope
	// FilePath is the absolute path of the file this analysis describes.
	// Set by BuildProject for every project-loaded file. A user file's
	// symbols carry it as their SourceFile (builder.define). Empty for an
	// entry whose caller gave no path and for standalone single-file
	// analysis (BuildFile). Used by the opaque-types boundary check to tell
	// same-module access apart from out-of-module access.
	FilePath string
	// Origin is the nominal-identity key for types this file declares —
	// the build's own module-path key (`std/calendar`, `shapes`,
	// `stringkit/pad`, the entry's own key), or OriginEntry for an entry
	// file the build has no key for. It is
	// build-relative and stable, unlike FilePath, which is absolute and
	// (for stdlib) points into the per-process materialization cache.
	// A nominal type's identity is (Origin, Name); see nominal_identity.go.
	Origin string
	// DispatchNames tracks function names defined by impl/extend blocks.
	// These are runtime-dispatched and should not be type-checked at call sites.
	DispatchNames map[string]bool
	// Impls tracks which types implement which interfaces, collected from
	// `impl I for T` and `extend T as I` blocks. Map: typeName → interfaceName → true.
	// Used by the type checker to validate interface-typed struct fields.
	Impls map[string]map[string]bool
	// ImplTypeArgs records the first interface type-argument template for each
	// (implementing-type, interface) pair. ImplTypeArgSets records every
	// template for the same pair, which matters for operator-like interfaces
	// such as Add where interface arguments are part of impl identity.
	// Populated after BuildTypes so the unifier can bind any generic
	// interface's type params from a concrete receiver.
	ImplTypeArgs    map[string]map[string]*ImplTypeArgs
	ImplTypeArgSets map[string]map[string][]*ImplTypeArgs
	// BootErrors are this file's boot errors from the project build: a
	// `fn boot` outside an entry file, or a boot signature or application
	// struct that is not valid. The checker reports them with the file.
	BootErrors []TypeError
	// program is the program this file belongs to; read it with Program().
	// A build sets it once the Program is complete, and never changes it.
	program *Program
	// TypeMethods is the per-file type-qualified method table:
	// receiver-type base name → method name → Symbol. Populated by
	// defineImplBlock for compiler-synthesized type-owned functions and
	// interface (`impl Iface for Type { fn foo }`) blocks. The
	// Symbol carries OwningType = the receiver type. The precise table powers
	// receiver-specific lookup even when a module exposes one flattened name
	// such as `users.inspect(...)` for several receiver types.
	TypeMethods map[string]map[string]*Symbol
	// ImplBlockReceiver records, for each impl-block item FuncDef this file
	// declares, the receiver type base name taken from the BLOCK HEADER (not
	// the first parameter). Coherence (detectImplCollisions /
	// detectOrphanImpls) keys interface-impl methods on the receiver; for
	// block forms the first parameter is the concrete receiver type.
	// Bodyless marker impls have no function item at all, so
	// funcDefReceiverBaseName can't recover it — this override map supplies
	// the header receiver when a function item exists. Also lets the
	// inherent collision check group an inherent block's methods by their
	// owning type. Nil for files with no impl blocks. These are the file's
	// own; Program() holds the build's.
	ImplBlockReceiver     map[*ast.FuncDef]string
	ImplBlockInterfaceKey map[*ast.FuncDef]string
	// ImplBlockReceiverExtern and ImplBlockInterfaceKeyExtern are the
	// `host fn` parallel of ImplBlockReceiver and ImplBlockInterfaceKey,
	// which are FuncDef-typed and so exclude the `host fn` items of an
	// `impl Iface for Type` block. Carrying externs lets resolveConcreteImpl
	// resolve an extern interface impl to its concrete source for go-to-def
	// and hover.
	ImplBlockReceiverExtern     map[*ast.ExternFunc]string
	ImplBlockInterfaceKeyExtern map[*ast.ExternFunc]string
	// kept is what a project build kept of the declarations for
	// rebuildBodies; nil for a file built any other way and for a stdlib
	// file.
	kept *keptDecls
	// ownNodes is how many of a project build's nodes for the file are the
	// file's own: its declarations as lowered, after the prelude imports the
	// build put before them and before the impls synthesis put after them.
	ownNodes int
}

// Revision counts the times a recheck rewrote the analysis's program in
// place (rebuildEditedBodies): this file's bodies, or another file's and
// the positions this file's facts hold of it. A cache of facts read from an
// analysis holds the analysis and its revision, since the analysis alone
// no longer names one set of facts.
func (fa *FileAnalysis) Revision() int {
	return fa.Program().revision
}

// FileBodies is the body part of a file's analysis. See FileAnalysis.
type FileBodies struct {
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
	// SubtestsRowTypes maps each `subtests` statement the checker accepted
	// to the type of its rows: the element type of what it iterates, which
	// its pattern binds. Nil until the first record.
	SubtestsRowTypes map[*ast.Subtests]Type
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
	// project-level Program().ImplIndex.TypeMethods table — e.g. `List.concat` resolves
	// to std/lists). The runtime type-method bridge force-loads
	// these so the inherent method registers even when the file never imported
	// the provider; without it `List.concat` only works when something else
	// happens to load std/lists. Each file records its own call sites'.
	// Keyed by the loadable module path (the filesByKey key); never "" (the
	// entry's own methods need no provider).
	TypeMethodModules map[string]bool
	// AppReads holds each checked application-field read (`MyApp.logger`)
	// and each `with` override's target, keyed by the *ast.FieldAccess, and
	// AppReadsByPos the same keyed by the field name's position.
	AppReads      map[ast.Node]AppRead
	AppReadsByPos map[Pos]AppRead
	// ImportedModules is the module key of every user file this file
	// imports, in the order its imports reach them. A type-qualified call
	// to an imported module's public function resolves through it
	// (LookupVisibleTypeMethod).
	ImportedModules []string
	// ImplImports are this file's imports of whole project files that no
	// name in the file uses. Such an import may still be needed for the
	// impl blocks the file brings into the program, and that is decided
	// over the whole program once every file is type-checked
	// (CheckImplImports). See unused_imports.go. Whether a name is used
	// depends on the bodies, so these are found with the bodies' references.
	ImplImports []*ImplImport
	// TypeErrors holds type-checking errors discovered after analysis.
	TypeErrors []TypeError
	// generation tells this body part from every other one the process
	// made for the same analysis: 0 for a build's, and a number no other
	// body part has for each walkBodiesAgain makes. A cache of what a
	// file's bodies contribute to a program-wide check holds the analysis
	// and its generation (fileReductions).
	generation uint64
}

// bodyGenerations numbers the body parts walkBodiesAgain makes.
var bodyGenerations atomic.Uint64

// keptDecls is what a project build keeps of a file's declarations so its
// bodies can be rebuilt (rebuildBodies): the declarations' share of each
// FileBodies field that mixes the two, taken when the declarations are done
// and before any body is walked (keepDecls), the facts BuildTypes later works
// out from declarations about what the bodies walk defines, and the builder
// and type registry the bodies are walked and typed with.
type keptDecls struct {
	// The declarations' share of the mixed fields, and of the module scope:
	// a file-level statement, an error the checker reports, binds its names
	// there while its bodies are walked.
	references   map[Pos]*Symbol
	definitions  map[Pos]*Symbol
	punned       map[Pos]*Symbol
	manifest     map[string]map[string][]Recording
	imported     []string
	errors       []TypeError
	blockImports map[*ast.ImportStmt]*Scope
	symbols      map[string]*Symbol
	scopes       int
	// builder built the file. builderImported and builderBlockImports are its
	// tables the bodies walk adds to: a block's `import m.{self}` binds m in
	// imported, and every block import is noted in blockImports.
	builder             *builder
	builderImported     map[string]*Scope
	builderBlockImports map[*ast.ImportStmt]bool
	// typing is set while BuildTypes works on the declarations, after the
	// bodies are walked: what it writes then is recorded here, so a rebuild
	// can write it again over bodies walked anew.
	typing bool
	// typeRefs are the references BuildTypes records for the fields of an
	// anonymous struct type written in a declaration.
	typeRefs []keptRef
	// typed are the types BuildTypes gives symbols the bodies walk defines:
	// a function's parameters and type parameters, which are defined again,
	// as new symbols, where the body is walked.
	typed []keptType
	// typeErrors are BuildTypes' errors about declarations, and types the
	// registry it built the file's types with, which types the bodies.
	typeErrors []TypeError
	types      *TypeRegistry
}

// keptRef is one reference BuildTypes recorded.
type keptRef struct {
	pos Pos
	sym *Symbol
}

// keptType is one type BuildTypes gave the symbol at pos with a name and
// kind.
type keptType struct {
	pos  Pos
	name string
	kind SymbolKind
	typ  Type
}

// keepDecls keeps fa's declarations, built by b, for rebuildBodies. A
// project build calls it for each of its own files once Sweep B-bounds is
// done, just before Sweep B-bodies.
func (fa *FileAnalysis) keepDecls(b *builder) {
	fa.kept = &keptDecls{
		references:          maps.Clone(fa.References),
		definitions:         maps.Clone(fa.Definitions),
		punned:              maps.Clone(fa.PunnedFieldLabels),
		manifest:            cloneManifest(fa.ImplManifest),
		imported:            slices.Clone(fa.ImportedModules),
		errors:              slices.Clone(fa.TypeErrors),
		blockImports:        maps.Clone(fa.BlockImportScopes),
		symbols:             maps.Clone(fa.ModuleScope.Symbols),
		scopes:              len(fa.ModuleScope.Children),
		builder:             b,
		builderImported:     maps.Clone(b.imported),
		builderBlockImports: maps.Clone(b.blockImports),
	}
}

// cloneManifest copies an ImplManifest down to its recording lists.
func cloneManifest(m map[string]map[string][]Recording) map[string]map[string][]Recording {
	if m == nil {
		return nil
	}
	out := make(map[string]map[string][]Recording, len(m))
	for iface, byType := range m {
		inner := make(map[string][]Recording, len(byType))
		for typeName, recs := range byType {
			inner[typeName] = slices.Clone(recs)
		}
		out[iface] = inner
	}
	return out
}

// typingDecls runs typeDecls, BuildTypes' work on fa's declarations, and
// when fa is kept records the references it writes. They go in a map of
// their own, which is then laid over fa.References: nothing typeDecls does
// reads References.
func (fa *FileAnalysis) typingDecls(typeDecls func()) {
	k := fa.kept
	if k == nil {
		typeDecls()
		return
	}
	refs := fa.References
	fa.References = map[Pos]*Symbol{}
	k.typing = true
	typeDecls()
	k.typing = false
	for pos, sym := range fa.References {
		k.typeRefs = append(k.typeRefs, keptRef{pos, sym})
		refs[pos] = sym
	}
	fa.References = refs
}

// attachDeclType gives sym, the symbol at pos in fa.Definitions, the type
// BuildTypes worked out for it from a declaration, and records it when the
// symbol is one the bodies walk defined.
func (fa *FileAnalysis) attachDeclType(pos Pos, sym *Symbol, typ Type) {
	sym.Type = typ
	if k := fa.kept; k != nil && k.typing && k.definitions[pos] != sym {
		k.typed = append(k.typed, keptType{pos, sym.Name, sym.Kind, typ})
	}
}

// rebuildBodies throws away fa's body part and makes it again from its kept
// declarations and the program's other files as they are: it walks the
// bodies (Sweep B-bodies), runs the unused checks, types what the bodies
// declare (BuildTypes' pass 2b), and type-checks the file, as the build and
// the check did for nodes, the file's nodes. It answers CheckTypes' errors,
// and false when fa was not built by a project build that kept it.
//
// What the program reduces over every file's bodies is not redone:
// CheckAppRoots, CheckImplImports and FinalizeCoherence, and an entry's
// iteration and concurrency checks, read the rebuilt bodies and have to run
// again, and so does MarkTailCalls on nodes.
func (fa *FileAnalysis) rebuildBodies(nodes []ast.Node) ([]TypeError, bool) {
	if !fa.walkBodiesAgain(nodes) {
		return nil, false
	}
	return CheckTypes(fa, nodes), true
}

// walkBodiesAgain is rebuildBodies up to CheckTypes: it throws the body part
// away, walks the bodies, runs the unused checks and types what the bodies
// declare. It answers false when fa was not kept.
func (fa *FileAnalysis) walkBodiesAgain(nodes []ast.Node) bool {
	k := fa.kept
	if k == nil {
		return false
	}
	fa.FileBodies = FileBodies{
		References:        maps.Clone(k.references),
		Definitions:       maps.Clone(k.definitions),
		PunnedFieldLabels: maps.Clone(k.punned),
		ImplManifest:      cloneManifest(k.manifest),
		ImportedModules:   slices.Clone(k.imported),
		TypeErrors:        slices.Clone(k.errors),
		BlockImportScopes: maps.Clone(k.blockImports),
		generation:        bodyGenerations.Add(1),
	}
	fa.ModuleScope.Symbols = maps.Clone(k.symbols)
	fa.ModuleScope.Children = fa.ModuleScope.Children[:k.scopes]
	b := k.builder
	b.imported = maps.Clone(k.builderImported)
	b.blockImports = maps.Clone(k.builderBlockImports)
	b.fileScope = fa.ModuleScope
	b.buildModuleBodies(nodes, fa.ModuleScope)
	fa.TypeErrors = append(fa.TypeErrors, checkUnused(fa, nodes)...)
	for _, r := range k.typeRefs {
		fa.References[r.pos] = r.sym
	}
	for _, t := range k.typed {
		if sym := fa.Definitions[t.pos]; sym != nil && sym.Name == t.name && sym.Kind == t.kind {
			sym.Type = t.typ
		}
	}
	fa.TypeErrors = append(fa.TypeErrors, k.typeErrors...)
	fa.TypeErrors = append(fa.TypeErrors, buildBodyTypes(fa, k.types, nodes)...)
	return true
}
