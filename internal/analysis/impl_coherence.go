package analysis

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// ProjectImplIndex aggregates per-FA impl recordings into a project-
// level view that consumers (checker, derive synthesis, coherence checks,
// the LSP) read uniformly. Single source of truth for "which Ts implement
// which Ifaces" across the whole reachable program post-stdlib-
// globals-retirement; cache write-through plus the
// stdlibFAs eager fold in BuildProjectWithCache ensure every reachable
// stdlib FA contributes its impls, and AttachStdlibProjectImpls (below)
// provides the single-file path with a stdlib-only equivalent.
type ProjectImplIndex struct {
	// T → Iface → bool. Union of every FA's per-file Impls.
	Impls map[string]map[string]bool

	// Iface → T → []Recording. Union of every FA's per-file
	// ImplManifest, deduplicated by full Recording equality on
	// collision. Same shape as FileAnalysis.ImplManifest.
	// DetectMissingImpls reads from this project-level view rather
	// than the per-file maps so a pair recorded in one file but only
	// missing an impl at the project level surfaces uniformly.
	ImplManifest map[string]map[string][]Recording

	// Iface → method → []*ast.FuncDef. Union of every FA's per-file
	// IfaceMethodImpls. Read by the collision detector and the LSP.
	IfaceMethodImpls map[string]map[string][]*ast.FuncDef

	// FuncDef pointer → home module path (e.g. "std/int", "foo/bar",
	// "" for the project entry). Union of every FA's per-file
	// IfaceMethodImplFiles. Read by the LSP to locate an impl's file.
	ImplFiles map[*ast.FuncDef]string

	// ImplTypeArgs aggregates per-FA ImplTypeArgs (general interface
	// type-argument templates) into a project-level view, keyed
	// [implTypeName][interfaceName]. Read by unify.go::lookupImplTypeArgs as the
	// fallback after the file-local ImplTypeArgs map; the union follows a
	// last-write-wins-on-collision semantic.
	ImplTypeArgs    map[string]map[string]*ImplTypeArgs
	ImplTypeArgSets map[string]map[string][]*ImplTypeArgs
	// ImplBlocks is every file's FileAnalysis.ImplBlocks, by receiver and
	// then interface base name.
	ImplBlocks map[string]map[string][]*ImplTypeArgs

	// ImplFuncTypes maps each impl FuncDef pointer to its resolved
	// FuncType (param + return types), pulled from the owning file's
	// Definitions during buildProjectImplIndex. Read by the checker's
	// module-qualified dispatch path to recover the actually-resolved
	// impl's return type — module exports collapse multi-impl methods
	// to one (last-defined) signature, so without this look-up the
	// call's return type is whichever impl the analyzer saw last
	// regardless of the dispatch receiver. Nil entries for impls whose
	// owning FA wasn't typed at index-build time are tolerated by the
	// reader.
	ImplFuncTypes map[*ast.FuncDef]Type

	// TypeMethods is the project-wide type-method table for impl-block
	// functions: receiver-type base name → method name → Symbol. Union of
	// every reachable FA's per-file TypeMethods. The checker's type-qualified
	// dispatch (`Type.foo(...)`) consults this so a `pub fn` inside an inherent
	// block in one module resolves from a call site in another. A non-pub
	// impl-block fn is filtered out of this union (it's only reachable inside
	// its declaring module, via the per-file TypeMethods); only Public members
	// land here. Inherent wins over interface-impl on a same-name clash for one
	// type, matching the per-file precedence.
	TypeMethods map[string]map[string]*Symbol

	// TypeMethodModule maps receiver-type → method → the module path that
	// provides that public type-promoted method (the filesByKey key, e.g.
	// "std/lists" or a sibling name; never "" for the entry). Parallel to
	// TypeMethods, populated in the same union loop. It names the provider of
	// an inherent method like `List.concat` even when the entry never
	// imported std/lists; the analyzer always knows the method, because the
	// stdlib is always analyzed.
	TypeMethodModule map[string]map[string]string

	// ImplBlockReceiver maps each block-form impl-method FuncDef to its
	// receiver type's base name (the block header's receiver). Union of every
	// FA's per-file ImplBlockReceiver. The canonical receiver of a block item
	// — whose first parameter is the concrete receiver type, so the first-param heuristic can't
	// recover it. ReceiverOf consults this (falling back to the first param
	// for decorator-form impls) so consumers that match an impl FuncDef to a
	// receiver type — the orphan/collision checks — handle both impl forms
	// uniformly.
	ImplBlockReceiver     map[*ast.FuncDef]string
	ImplBlockInterfaceKey map[*ast.FuncDef]string

	// QualifiedReceiver is ImplBlockReceiver with the receiver's
	// DECLARING module's key attached (qualifyReceiver: `std/bool` and
	// `Bool`) — the impl's nominal identity rather than its base name.
	//
	// Deliberately separate from ImplBlockReceiver rather than replacing
	// it. Base names are what the checker, the orphan rule, the
	// typed-literal handler lookup, and the type-method tables all key
	// on, and those consumers frequently hold nothing but a bare name.
	// The collision check needs identity (two same-named types are two
	// types), so it reads this map. See nominal_identity.go.
	QualifiedReceiver map[*ast.FuncDef]string

	// ImplIfaceOrigin is QualifiedReceiver's counterpart on the INTERFACE
	// side: each impl-method FuncDef → the build key of the file that
	// declared the interface the impl named. IfaceOriginByPair is the same
	// answer keyed the way `Impls` is keyed, receiver → interface → origin,
	// for the unifier's bound check.
	//
	// Populated by PopulateImplIfaceOrigins, after BuildTypes. See
	// interface_identity.go for why an absent entry accepts.
	ImplIfaceOrigin   map[*ast.FuncDef]string
	IfaceOriginByPair map[string]map[string][]string

	// ImplsByIdentity is `Impls` re-keyed by the receiver's nominal identity —
	// (Origin, Type, Iface) instead of (Type, Iface) — so the three stdlib
	// types named `Error` cannot share one conformance. `DetectMissingImpls`
	// consults it and falls back to `Impls` when the receiver's origin is
	// unknown.
	//
	// implNamesWithIdentity is the set of (Type, Iface) pairs this index
	// COVERS. It is not derivable from ImplsByIdentity's key set without
	// iterating it, and the fallback rule needs the question "is this pair
	// covered at all" answered in one lookup: a pair nobody could resolve an
	// origin for must keep the bare-name answer rather than be rejected for
	// having no matching key.
	//
	// Populated by PopulateImplsByIdentity, after BuildTypes. See
	// missing_impl_identity.go for why an absent entry accepts.
	ImplsByIdentity       map[ImplIdentityKey]bool
	implNamesWithIdentity map[string]map[string]bool

	// TypeMethodByIdentity is TypeMethods re-keyed by the receiver's nominal
	// identity — (Origin, Type, Method) instead of (Type, Method) — so the
	// three stdlib types named `Error` cannot collapse into one slot. The
	// checker's `Type.method` resolution consults it first and falls back to
	// TypeMethods when the receiver's origin is unknown.
	//
	// TypeMethodIdentityConflicts records every fully-qualified key two
	// different symbols contended for: with the module in the key, that is a
	// genuine duplicate rather than the name collision the bare table suffered.
	// Expected empty; a guard reads it.
	//
	// Populated by PopulateTypeMethodIdentities, after BuildTypes. See
	// type_method_identity.go.
	//
	// TypeMethodModuleByIdentity is TypeMethodModule's identity-keyed twin: the
	// build key of the file whose impl block PROVIDES the method (which is not
	// the key in TypeMethodKey.Origin — that one is where the receiver TYPE was
	// declared, and `impl Iter for List` splits the two). The runtime bridge
	// force-loads it, so it has to follow the symbol the identity path picked.
	//
	// TypeMethodOrigin is the reverse: each symbol TypeMethodByIdentity holds,
	// mapped to its receiver's declaring origin. resolveTypeMethodSymbol reads
	// it to refuse a bare-name answer that belongs to another module's
	// same-named type.
	TypeMethodByIdentity        map[TypeMethodKey]*Symbol
	TypeMethodModuleByIdentity  map[TypeMethodKey]string
	TypeMethodIdentityConflicts []TypeMethodKey
	TypeMethodOrigin            map[*Symbol]string

	// IfaceMethodImplExterns / ImplExternFiles / ImplBlockReceiverExtern are the
	// `host fn` parallel of IfaceMethodImpls / ImplFiles / ImplBlockReceiver,
	// unioned from the per-file fields of the same name. resolveConcreteImpl
	// consults them so an interface-/module-qualified call to an EXTERN impl
	// method (e.g. `Display.to_string` on `Float`, whose impl is `host fn
	// to_string`) resolves to its concrete source for go-to-def / hover. The
	// FuncDef maps stay extern-free — the type-checker and the other readers
	// want FuncDefs — so externs are carried only on this navigation path.
	IfaceMethodImplExterns      map[string]map[string][]*ast.ExternFunc
	ImplExternFiles             map[*ast.ExternFunc]string
	ImplBlockReceiverExtern     map[*ast.ExternFunc]string
	ImplBlockInterfaceKeyExtern map[*ast.ExternFunc]string

	// EnumDecls holds every enum declaration of the reachable program,
	// block-local ones included, by bare name, in build-key order. The
	// checker reads it to find the declaration a `.Variant` resolved
	// against when the enum's name is not in the file's scope
	// (enumDeclSymbol). Populated by PopulateEnumDecls.
	EnumDecls map[string][]*Symbol
}

// PopulateEnumDecls fills idx.EnumDecls from every FA's Definitions.
func PopulateEnumDecls(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis) {
	if idx == nil {
		return
	}
	keys := make([]string, 0, len(filesByKey))
	for key := range filesByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	idx.EnumDecls = make(map[string][]*Symbol)
	seen := make(map[*Symbol]bool)
	for _, key := range keys {
		fa := filesByKey[key]
		noteStdlibWork(key)
		if fa == nil {
			continue
		}
		var syms []*Symbol
		for _, sym := range fa.Definitions {
			if sym != nil && sym.Kind == SymbolEnum && !seen[sym] {
				seen[sym] = true
				syms = append(syms, sym)
			}
		}
		sort.Slice(syms, func(i, j int) bool { return earlierDeclaration(syms[i], syms[j]) })
		for _, sym := range syms {
			idx.EnumDecls[sym.Name] = append(idx.EnumDecls[sym.Name], sym)
		}
	}
}

// ReceiverOf returns the receiver type's base name for an impl-method FuncDef,
// whichever front-end declared it: the recorded block-header receiver for a
// block item (whose first parameter is `self`), or the first-parameter type
// for a decorator-form impl. Returns "" when neither is resolvable.
func (idx *ProjectImplIndex) ReceiverOf(fn *ast.FuncDef) string {
	if idx != nil && idx.ImplBlockReceiver != nil {
		if r, ok := idx.ImplBlockReceiver[fn]; ok && r != "" {
			return r
		}
	}
	return funcDefReceiverBaseName(fn)
}

// buildProjectImplIndex unions every FA's per-file impl recordings
// into a single project-level ProjectImplIndex. Single-pass over
// filesByKey; deterministic; no I/O. Runs once at
// BuildProjectWithCache post-Sweep-B, at the same point
// detectImplCollisions runs (after filesByKey is built).
//
// The impl-method tables come from each file's Program (implTablesOf), and
// one build's files share one, so each Program's tables are folded once.
// Duplicate FuncDef pointers that remain (a stdlib file reached under two
// keys) are deduplicated by pointer identity in the IfaceMethodImpls union — otherwise downstream
// consumers like detectImplCollisions would see len(fns) == N and
// emit spurious diagnostics for every (iface, method, receiver-type)
// triple. The same hazard does not exist for Impls / ImplManifest
// (booleans, idempotent on write) or ImplFiles (FuncDef→string,
// duplicate writes carry the same value).
//
// ImplTypeArgs union semantic: last-write-wins on key collision. Unlike
// the bool sets (Impls / ImplManifest, idempotent) and the slice
// append (IfaceMethodImpls, dedup'd by pointer triple), ImplTypeArgs
// holds a single template per (type, iface) — two FAs claiming the same
// pair with different templates would be a real coherence violation, but
// catching that is the orphan rule's concern, not this aggregation's.
// Iteration over filesByKey is deterministic in practice because callers
// (BuildProjectWithCache, AttachStdlibProjectImpls) pass closed module
// sets, and the same FA pointer carries the same ImplTypeArgs across
// every key it appears under.
func buildProjectImplIndex(filesByKey map[string]*FileAnalysis, own *implTables, owned map[*FileAnalysis]bool) *ProjectImplIndex {
	idx := &ProjectImplIndex{
		Impls:                 make(map[string]map[string]bool),
		ImplManifest:          make(map[string]map[string][]Recording),
		IfaceMethodImpls:      make(map[string]map[string][]*ast.FuncDef),
		ImplFiles:             make(map[*ast.FuncDef]string),
		ImplTypeArgs:          make(map[string]map[string]*ImplTypeArgs),
		ImplTypeArgSets:       make(map[string]map[string][]*ImplTypeArgs),
		ImplBlocks:            make(map[string]map[string][]*ImplTypeArgs),
		ImplFuncTypes:         make(map[*ast.FuncDef]Type),
		TypeMethods:           make(map[string]map[string]*Symbol),
		TypeMethodModule:      make(map[string]map[string]string),
		ImplBlockReceiver:     make(map[*ast.FuncDef]string),
		QualifiedReceiver:     make(map[*ast.FuncDef]string),
		ImplBlockInterfaceKey: make(map[*ast.FuncDef]string),
		ImplIfaceOrigin:       make(map[*ast.FuncDef]string),
		IfaceOriginByPair:     make(map[string]map[string][]string),
		TypeMethodByIdentity:  make(map[TypeMethodKey]*Symbol),

		IfaceMethodImplExterns:      make(map[string]map[string][]*ast.ExternFunc),
		ImplExternFiles:             make(map[*ast.ExternFunc]string),
		ImplBlockReceiverExtern:     make(map[*ast.ExternFunc]string),
		ImplBlockInterfaceKeyExtern: make(map[*ast.ExternFunc]string),
	}
	// Dedupe key for IfaceMethodImpls: (iface, method, fn). The dedupe must
	// be slot-local, not global-by-fn: a global `seenFns[fn]` would catch a
	// FuncDef at the first slot it hits and silently suppress any other slot
	// that legitimately points at the same node. Within one (iface, method)
	// slot, the same FuncDef can still arrive from two FAs (when a stdlib file
	// flows through both `all` and the eager fold) — the triple key still
	// dedupes those.
	type seenKey struct {
		iface  string
		method string
		fn     *ast.FuncDef
	}
	seenImpls := make(map[seenKey]bool)
	type seenExternKey struct {
		iface  string
		method string
		ext    *ast.ExternFunc
	}
	seenExterns := make(map[seenExternKey]bool)
	// Dedupe key for ImplManifest: a recording is folded once per (iface,
	// type) list, the first time a file in key order holds it.
	type seenRecordingKey struct {
		iface, typeName string
		rec             Recording
	}
	seenRecordings := make(map[seenRecordingKey]bool)
	// The files of one build share one set of impl-method tables, so the
	// stdlib's fifty-odd analyses hold one set between them, and a program's
	// files another. Folding a set a second time adds nothing, so each is
	// folded once.
	foldedTables := make(map[*implTables]bool)
	foldedFiles := make(map[*FileAnalysis]bool)
	// The stdlib's files first, then the program's, each in key order: where
	// two files contend for one first-write-wins slot (TypeMethods), the
	// answer is the same on every build, and the same one a build over the
	// stdlib's base (StdBase) gives.
	keys := stdlibFirstKeys(filesByKey)
	for _, key := range keys {
		fa := filesByKey[key]
		noteStdlibWork(key)
		if foldedFiles[fa] {
			continue
		}
		foldedFiles[fa] = true
		tables := implTablesOf(fa, own, owned)
		if foldedTables[tables] {
			continue
		}
		foldedTables[tables] = true
		// A set another build listed may hold the impls of a file this
		// index has another analysis of: the shared stdlib's set holds
		// those of a std module that stands here checked from edited
		// source (AttachEditedStdlibImpls). That file's impls are its own
		// tables' alone.
		replaced := func(path string) bool {
			return tables != own && owned[filesByKey[path]]
		}
		for fn, recv := range tables.ImplBlockReceiver {
			if fn != nil && recv != "" {
				idx.ImplBlockReceiver[fn] = recv
			}
		}
		for fn, key := range tables.ImplBlockInterfaceKey {
			if fn != nil && key != "" {
				idx.ImplBlockInterfaceKey[fn] = key
			}
		}
		for ifaceName, byMethod := range tables.IfaceMethodImpls {
			if idx.IfaceMethodImpls[ifaceName] == nil {
				idx.IfaceMethodImpls[ifaceName] = make(map[string][]*ast.FuncDef)
			}
			for methodName, fns := range byMethod {
				for _, fn := range fns {
					k := seenKey{iface: ifaceName, method: methodName, fn: fn}
					if seenImpls[k] || replaced(tables.IfaceMethodImplFiles[fn]) {
						continue
					}
					seenImpls[k] = true
					idx.IfaceMethodImpls[ifaceName][methodName] = append(idx.IfaceMethodImpls[ifaceName][methodName], fn)
				}
			}
		}
		// ImplFuncTypes is populated separately in a post-BuildTypes pass
		// (see PopulateProjectImplFuncTypes), since sym.Type isn't set on
		// impl FuncDef Symbols until BuildTypes runs — buildProjectImplIndex
		// executes earlier in the build pipeline (post Sweep B-ann, pre
		// Sweep C-types).
		for fn, path := range tables.IfaceMethodImplFiles {
			if !replaced(path) {
				idx.ImplFiles[fn] = path
			}
		}
		// Extern-impl parallel of the unions above. IfaceMethodImplExterns
		// dedups by the (iface, method, ext) triple; ImplExternFiles
		// overwrites idempotently (a given extern maps to one path), so a
		// plain copy is correct.
		for ifaceName, byMethod := range tables.IfaceMethodImplExterns {
			if idx.IfaceMethodImplExterns[ifaceName] == nil {
				idx.IfaceMethodImplExterns[ifaceName] = make(map[string][]*ast.ExternFunc)
			}
			for methodName, exts := range byMethod {
				for _, ext := range exts {
					k := seenExternKey{iface: ifaceName, method: methodName, ext: ext}
					if seenExterns[k] || replaced(tables.IfaceMethodImplExternFiles[ext]) {
						continue
					}
					seenExterns[k] = true
					idx.IfaceMethodImplExterns[ifaceName][methodName] = append(idx.IfaceMethodImplExterns[ifaceName][methodName], ext)
				}
			}
		}
		for ext, path := range tables.IfaceMethodImplExternFiles {
			if !replaced(path) {
				idx.ImplExternFiles[ext] = path
			}
		}
		for ext, key := range tables.ImplBlockInterfaceKeyExtern {
			if ext != nil && key != "" {
				idx.ImplBlockInterfaceKeyExtern[ext] = key
			}
		}
	}
	for _, key := range keys {
		fa := filesByKey[key]
		for typeName, ifaces := range fa.Impls {
			if idx.Impls[typeName] == nil {
				idx.Impls[typeName] = make(map[string]bool)
			}
			for iface := range ifaces {
				idx.Impls[typeName][iface] = true
			}
		}
		for ifaceName, types := range fa.ImplManifest {
			if idx.ImplManifest[ifaceName] == nil {
				idx.ImplManifest[ifaceName] = make(map[string][]Recording)
			}
			for typeName, recs := range types {
				existing := idx.ImplManifest[ifaceName][typeName]
				for _, rec := range recs {
					k := seenRecordingKey{iface: ifaceName, typeName: typeName, rec: rec}
					if !seenRecordings[k] {
						seenRecordings[k] = true
						existing = append(existing, rec)
					}
				}
				noteRecordingCompares(len(recs))
				idx.ImplManifest[ifaceName][typeName] = existing
			}
		}
		for ext, recv := range fa.ImplBlockReceiverExtern {
			if ext != nil && recv != "" {
				idx.ImplBlockReceiverExtern[ext] = recv
			}
		}
		for typeName, byIface := range fa.ImplTypeArgs {
			if idx.ImplTypeArgs[typeName] == nil {
				idx.ImplTypeArgs[typeName] = make(map[string]*ImplTypeArgs)
			}
			for ifaceName, ita := range byIface {
				idx.ImplTypeArgs[typeName][ifaceName] = ita
			}
		}
		for typeName, byIface := range fa.ImplTypeArgSets {
			if idx.ImplTypeArgSets[typeName] == nil {
				idx.ImplTypeArgSets[typeName] = make(map[string][]*ImplTypeArgs)
			}
			for ifaceName, set := range byIface {
				idx.ImplTypeArgSets[typeName][ifaceName] = append(idx.ImplTypeArgSets[typeName][ifaceName], set...)
			}
		}
		addImplBlocks(idx, fa)
		// Type-promoted functions: every impl-block member another file may
		// see, a public or an internal one. A lookup through the index
		// answers only one its file sees (FileAnalysis.Sees), so an
		// internal one stays in its module. A private member stays
		// reachable solely via its owning FA's per-file TypeMethods.
		for typeName, byMethod := range fa.TypeMethods {
			for methodName, sym := range byMethod {
				// Interface impl methods do not spell `pub`: their exported
				// surface is governed by the public interface contract. Derived
				// impl methods follow the same rule, so include impl methods in
				// the project type-method table whatever their own Visibility.
				if sym == nil || (sym.Visibility == ast.Private && !sym.IsImplMethod) {
					continue
				}
				if idx.TypeMethods[typeName] == nil {
					idx.TypeMethods[typeName] = make(map[string]*Symbol)
				}
				// First write wins for stability; a genuine duplicate is a
				// collision the coherence pass reports separately.
				if _, present := idx.TypeMethods[typeName][methodName]; !present {
					idx.TypeMethods[typeName][methodName] = sym
					// Record the provider module path (the filesByKey key).
					// Skip the entry ("" key) — it needs no provider.
					if key != "" {
						if idx.TypeMethodModule[typeName] == nil {
							idx.TypeMethodModule[typeName] = make(map[string]string)
						}
						idx.TypeMethodModule[typeName][methodName] = key
					}
				}
			}
		}
	}
	// Stable order so the missing-impl diagnostic and any other consumer
	// sees the same Recording ordering across runs; Go map iteration is
	// randomized. Sort by (Pos.File, Pos.Line, Pos.Col, Kind) — the
	// natural source-location ordering, with Kind as the final tiebreaker
	// for two recordings sharing the exact same position.
	for _, byType := range idx.ImplManifest {
		for typeName, recs := range byType {
			sort.SliceStable(recs, func(i, j int) bool {
				if recs[i].Pos.File != recs[j].Pos.File {
					return recs[i].Pos.File < recs[j].Pos.File
				}
				if recs[i].Pos.Line != recs[j].Pos.Line {
					return recs[i].Pos.Line < recs[j].Pos.Line
				}
				if recs[i].Pos.Col != recs[j].Pos.Col {
					return recs[i].Pos.Col < recs[j].Pos.Col
				}
				return recs[i].Kind < recs[j].Kind
			})
			byType[typeName] = recs
		}
	}

	return idx
}

// PopulateQualifiedReceivers fills idx.QualifiedReceiver. Must run
// AFTER BuildTypes (Sweep C-types): the qualifier is read off the
// receiver type's Origin, and type symbols carry no Type pointer until
// then, so running it during buildProjectImplIndex yields bare names
// for everything.
//
// The origin is resolved through the impl's HOME file. The per-file
// impl maps are broadcast — every FileAnalysis ends up holding the same
// method index covering every file's impls — so iterating a particular FA
// is not sufficient to discover every receiver. The method index is
// authoritative for every FuncDef the bridge can register.
//
// `filesByKey` is also what resolves a PRIMITIVE receiver's origin. `Int`,
// `String`, `Float`, `Byte`, `Bytes` and `Unit` resolve to a shared
// *PrimitiveType singleton that carries no Origin, so a user file reaching
// one through the prelude could not qualify it and `impl Equatable for Int`
// keyed as bare `Int` while std/int's own keyed as `int.Int` — two groups,
// no duplicate diagnostic, and a user impl that silently took std's
// dispatch slot for `Equatable.equal` while `==` stayed structural.
// `Bool` escaped that because the prelude imports `bool.Bool.{self, ...}`
// and `Bool` is an *EnumType, which does carry an Origin; that asymmetry is
// what the probe measured.
func PopulateQualifiedReceivers(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis) {
	populateQualifiedReceivers(idx, filesByKey, primitiveOriginIndex(filesByKey))
}

// populateQualifiedReceivers is PopulateQualifiedReceivers with the
// primitive-origin index (primitiveOriginIndex over filesByKey) given.
func populateQualifiedReceivers(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis, primitives map[string]string) {
	if idx == nil {
		return
	}
	if idx.QualifiedReceiver == nil {
		idx.QualifiedReceiver = make(map[*ast.FuncDef]string, len(idx.ImplBlockReceiver))
	}
	entry := entryOriginOf(filesByKey)
	populate := func(fn *ast.FuncDef) {
		if fn == nil {
			return
		}
		// An inherent-block function has no home file here (ImplFiles holds
		// interface impls only). Resolved through filesByKey[""] it would be
		// read in the entry's scope, an answer that depends on which file is
		// the entry; nothing reads one, so it gets none.
		home, ok := idx.ImplFiles[fn]
		if !ok {
			return
		}
		noteStdlibWork(home)
		recv := idx.ReceiverOf(fn)
		if recv == "" {
			return
		}
		fa := filesByKey[home]
		idx.QualifiedReceiver[fn] = qualifyReceiverThrough(fa, implReceiverModule(fa, fn), recv, primitives, entry)
	}
	for fn := range idx.ImplBlockReceiver {
		populate(fn)
	}
	for _, byMethod := range idx.IfaceMethodImpls {
		for _, fns := range byMethod {
			for _, fn := range fns {
				populate(fn)
			}
		}
	}
}

// primitiveOriginIndex maps each primitive type name to the build key of the
// file that DECLARES it — `Int` → `std/int`, `String` → `std/strings`.
//
// Derived rather than tabulated: a file declares the name when its own module
// scope resolves it locally (`sym.Resolved == nil`) to a built-in
// *PrimitiveType; a host type carries its own Origin and needs no entry. That
// is the same test `declaringOriginOfType` already applies to decide `local`,
// so the two cannot disagree about what "declares" means, and adding a
// primitive to the language needs no edit here.
//
// Two files declaring one primitive name would be a genuine coherence
// violation rather than something to arbitrate, so the first key in sorted
// order wins and the result stays deterministic.
func primitiveOriginIndex(filesByKey map[string]*FileAnalysis) map[string]string {
	keys := make([]string, 0, len(filesByKey))
	for key := range filesByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := map[string]string{}
	for _, key := range keys {
		fa := filesByKey[key]
		noteStdlibWork(key)
		if fa == nil || fa.ModuleScope == nil || fa.Origin == "" {
			continue
		}
		for name, sym := range fa.ModuleScope.Symbols {
			if sym == nil || sym.Resolved != nil {
				continue
			}
			switch sym.Kind {
			case SymbolOnce, SymbolBinding, SymbolFunction:
				// A value whose type is a primitive declares no primitive.
				// A `once` is typed once its value is inferred, which a build
				// that reuses a file's earlier analysis has, and a fresh build
				// asks here before inference.
				continue
			}
			if pt, ok := sym.Type.(*PrimitiveType); !ok || pt.Origin != "" {
				continue
			}
			if _, seen := out[name]; !seen {
				out[name] = fa.Origin
			}
		}
	}
	return out
}

// qualifyReceiver renders an impl block's receiver as its nominal
// identity: the key of the module that DECLARED the receiver type, plus the
// name, joined by receiverOriginSep (receiverDisplay renders it).
//
// Only the per-impl receiver maps (ImplBlockReceiver, keyed by
// *ast.FuncDef) carry the qualified form. The type-keyed maps —
// Impls, ImplTypeArgs, TypeMethods, ImplManifest — stay on bare names,
// because they are queried by bare name from all over the checker and
// unifier, which frequently hold nothing else. Putting identity on the
// per-impl side gets it where it is actually needed (collision
// detection and dispatch registration both start from a specific impl
// block) without re-keying every lookup in the analyzer.
//
// The declaring module is not the module hosting the impl block —
// `impl Iter for List` lives in std/iter.nomi while `List` is declared
// in std/lists.nomi — so the origin comes from the receiver's own
// declaration, resolved through the file's scope. A name that resolves
// to no declaration (a type parameter, an unresolved import) stays
// bare; it has no identity to qualify with.
//
// `primitives` (primitiveOriginIndex) covers the one kind whose declaring
// module is not recoverable from the type: a built-in *PrimitiveType is a shared
// singleton with no Origin, so `Int` reached through the prelude had no
// identity and keyed bare. Nil is accepted — the callers that pass nil
// (single-file paths) get exactly the previous answer.
//
// The program entry's own types key bare.
// entryOrigin is the entry's Origin, its module key (entryOriginOf); the
// entry is a fact about the program, not about the file that declares the
// type.
func qualifyReceiver(fa *FileAnalysis, typeName string, primitives map[string]string, entryOrigin string) string {
	return qualifyReceiverThrough(fa, "", typeName, primitives, entryOrigin)
}

// qualifyReceiverThrough is qualifyReceiver for a receiver written through
// module, the name fa binds a module to (`users` in
// `impl Html for users.User`), or "" for one written bare.
func qualifyReceiverThrough(fa *FileAnalysis, module, typeName string, primitives map[string]string, entryOrigin string) string {
	if typeName == "" {
		return typeName
	}
	// A nested receiver (`FromJson.Options`) is still declared by some
	// module and needs the qualifier just as much — two modules each
	// declaring `FromJson.Options` would otherwise share one dispatch
	// slot. Its origin is the origin of its leading segment.
	head := typeName
	if i := strings.Index(typeName, "."); i >= 0 {
		head = typeName[:i]
	}
	origin := implReceiverOrigin(fa, module, head, primitives)
	if origin == "" {
		origin = primitives[head]
	}
	if origin == "" || origin == OriginEntry || origin == entryOrigin {
		return typeName
	}
	return origin + receiverOriginSep + typeName
}

// implReceiverModule is the module an impl item's block header names its
// receiver through, as fa, the file that writes the block, binds it; ""
// for a receiver written bare.
func implReceiverModule(fa *FileAnalysis, fn *ast.FuncDef) string {
	if fa == nil {
		return ""
	}
	return fa.ImplBlockReceiverModule[fn]
}

// implReceiverOrigin is the Origin of the receiver type an impl block in fa
// names: `name` in the scope of the module fa binds to module when the
// header qualifies it (`impl Html for users.User`), so a type of fa's own
// with the same name is not the receiver, else `name` in fa's own scope
// (declaringOriginOfType).
func implReceiverOrigin(fa *FileAnalysis, module, name string, primitives map[string]string) string {
	if module == "" {
		return declaringOriginOfType(fa, name, primitives)
	}
	if fa == nil || fa.ModuleScope == nil {
		return ""
	}
	mod := fa.ModuleScope.Lookup(module)
	for mod != nil && mod.Resolved != nil {
		mod = mod.Resolved
	}
	if mod == nil || mod.Kind != SymbolModule || mod.ModuleScope == nil {
		return ""
	}
	sym := mod.ModuleScope.Lookup(name)
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil {
		return ""
	}
	switch t := sym.Type.(type) {
	case *StructType:
		return t.Origin
	case *EnumType:
		return t.Origin
	case *DistinctType:
		return t.Origin
	case *PrimitiveType:
		if t.Origin != "" {
			return t.Origin
		}
		return primitives[sym.Name]
	}
	return ""
}

// receiverOriginSep joins a qualified receiver's declaring module key to its
// name. The key is the whole module key, never its last segment: a user
// module's `sql_literals/sql.nomi` and `std/sql.nomi` both end in `sql`, and
// each may declare its own `Sql`. A module key may hold any character a path
// may, so the separator is one no path holds.
const receiverOriginSep = "\x01"

// receiverDisplay renders a qualified receiver as a diagnostic names it: the
// declaring module's last segment and the name (`bool.Bool`, `sql.Sql`). A
// bare receiver renders as itself.
func receiverDisplay(qualified string) string {
	origin, name, ok := strings.Cut(qualified, receiverOriginSep)
	if !ok {
		return qualified
	}
	return lastSegment(origin) + "." + name
}

// declaringOriginOfType finds the Origin of the type `name` denotes in
// this file's scope, following the import chain to the declaration.
//
// A non-generic `host type` carries its Origin like a struct does. A built-in
// primitive (`Int`) is a shared singleton with no Origin (and could not have
// one: it is one object shared by every use). When the name is declared in
// THIS file, the file's own origin is the answer. When it is imported, the
// declaring file is the one primitiveOriginIndex names for it; `primitives`
// is that index, and nil leaves an imported built-in unresolved.
func declaringOriginOfType(fa *FileAnalysis, name string, primitives map[string]string) string {
	if fa == nil || fa.ModuleScope == nil {
		return ""
	}
	sym := fa.ModuleScope.Lookup(name)
	if sym == nil {
		return ""
	}
	local := sym.Resolved == nil
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	switch t := sym.Type.(type) {
	case *StructType:
		return t.Origin
	case *EnumType:
		return t.Origin
	case *DistinctType:
		return t.Origin
	case *PrimitiveType:
		if t.Origin != "" {
			return t.Origin
		}
		if !local {
			return primitives[sym.Name]
		}
	}
	if local {
		return fa.Origin
	}
	return ""
}

// AttachStdlibProjectImpls builds a stdlib-only ProjectImplIndex from
// `stdlibFAs` (typically std.StdLib.Files) and gives fa a Program holding it
// in place of its own. The single-file BuildFileWithStdlib path leaves
// Program().ImplIndex nil — consumers (checker's implsContext /
// typeImplementsInterface) then miss every stdlib
// (Type, Iface) pair, which
// surfaces as spurious "X does not implement Y" errors on routine
// stdlib calls (e.g. `io.inspect(42)` failing because the unifier
// can't see "Int implements Display"). Call sites that exercise the
// single-file path and want the same stdlib impl coverage the project
// path provides should call this after BuildFileWithStdlib.
//
// No-op when stdlibFAs is empty or fa is nil. Idempotent — replaces
// any prior index (callers that already went through
// BuildProject have their richer index; they shouldn't call this).
func AttachStdlibProjectImpls(fa *FileAnalysis, stdlibFAs map[string]*FileAnalysis) {
	attachStdlibImpls(fa, stdlibFAs, nil, nil)
}

// AttachEditedStdlibImpls is AttachStdlibProjectImpls for fa, the stdlib
// module `module` ("sql") checked from edited source, nodes, which the
// program holds under that name in place of stdlibFAs' (the shared
// analyses') copy. The shared copy stays among the files the program
// reaches (Program.reachedFiles): the stdlib's scopes name its
// declarations, as an attached test's `import std::sql` does. The index
// holds the impls nodes declare and none of the shared copy's. The shared
// analyses list every stdlib impl in one set of tables, the shared copy's
// among them; an impl function of that copy found there would be the
// checker's dispatch target (Symbol.DispatchImpl), which the IR builder
// does not lower, and would be typed by whatever the edited file defines at
// its line and column (PopulateProjectImplFuncTypes), another declaration
// once an edit moves the file's lines.
func AttachEditedStdlibImpls(fa *FileAnalysis, nodes []ast.Node, module string, stdlibFAs map[string]*FileAnalysis) {
	if fa == nil {
		return
	}
	own := &implTables{
		IfaceMethodImpls:            map[string]map[string][]*ast.FuncDef{},
		IfaceMethodImplFiles:        map[*ast.FuncDef]string{},
		ImplBlockReceiver:           map[*ast.FuncDef]string{},
		ImplBlockInterfaceKey:       map[*ast.FuncDef]string{},
		IfaceMethodImplExterns:      map[string]map[string][]*ast.ExternFunc{},
		IfaceMethodImplExternFiles:  map[*ast.ExternFunc]string{},
		ImplBlockInterfaceKeyExtern: map[*ast.ExternFunc]string{},
	}
	var inherents []InherentMethodRecord
	IndexImplBlockFuncDefs(nodes, "std/"+module, fa.Definitions, fa.ModuleScope, own.IfaceMethodImpls, own.IfaceMethodImplFiles,
		own.ImplBlockReceiver, own.ImplBlockInterfaceKey, own.IfaceMethodImplExterns, own.IfaceMethodImplExternFiles,
		own.ImplBlockInterfaceKeyExtern, &inherents)
	files := make(map[string]*FileAnalysis, len(stdlibFAs))
	for name, sfa := range stdlibFAs {
		files[name] = sfa
	}
	files[module] = fa
	attachStdlibImpls(fa, files, own, map[*FileAnalysis]bool{fa: true})
	if shared := stdlibFAs[module]; shared != nil && shared != fa {
		fa.program.editedStd = []*FileAnalysis{shared}
	}
}

// attachStdlibImpls is AttachStdlibProjectImpls, where own lists the impls
// of the analyses owned, each standing in stdlibFAs for the shared
// analysis's copy of its module.
func attachStdlibImpls(fa *FileAnalysis, stdlibFAs map[string]*FileAnalysis, own *implTables, owned map[*FileAnalysis]bool) {
	if fa == nil || len(stdlibFAs) == 0 {
		return
	}
	filesByKey := make(map[string]*FileAnalysis, len(stdlibFAs))
	for name, sfa := range stdlibFAs {
		filesByKey["std/"+name] = sfa
	}
	// The stdlib's base holds this index already, worked out once.
	base := stdBaseOf(stdlibFAs)
	var idx *ProjectImplIndex
	if base != nil {
		idx = base.index
	} else {
		idx = stdlibOnlyImplIndex(filesByKey, own, owned)
	}
	// A new Program in place of the old one, which other code may hold. It
	// reaches the stdlib files the index folds as well as its own.
	with := *fa.Program()
	with.ImplIndex = idx
	with.stdBase = base
	if own != nil {
		with.implTables = *own
	}
	files := make(map[string]*FileAnalysis, len(filesByKey)+len(with.filesByKey))
	for key, sfa := range filesByKey {
		files[key] = sfa
	}
	for key, own := range with.filesByKey {
		files[key] = own
	}
	if len(with.filesByKey) == 0 {
		files[""] = fa
	}
	// The index folds the stdlib alone, so it cannot judge a receiver
	// declared in fa or a user module fa loaded.
	unindexed := map[string]bool{}
	for origin := range with.unindexedOrigins {
		if !IsStdlibKey(origin) {
			unindexed[origin] = true
		}
	}
	if fa.Origin != OriginUnresolved && !IsStdlibKey(fa.Origin) {
		unindexed[fa.Origin] = true
	}
	with.unindexedOrigins = unindexed
	with.setFiles(files)
	fa.program = &with
}

// stdlibOnlyImplIndex answers the impl index of the stdlib analyses
// filesByKey, which are typed: their fold and every identity pass over it.
// own and owned are buildProjectImplIndex's: a std module's tables and
// analysis when it was checked from edited source.
func stdlibOnlyImplIndex(filesByKey map[string]*FileAnalysis, own *implTables, owned map[*FileAnalysis]bool) *ProjectImplIndex {
	idx := buildProjectImplIndex(filesByKey, own, owned)
	// PopulateProjectImplFuncTypes fills the post-BuildTypes
	// ImplFuncTypes index — the stdlib FAs have already been through
	// BuildTypes during std.Load (via BuildProjectWithCache), so their
	// impl Symbol.Type fields are resolved and this pass can lift them
	// into the freshly-built index. Without this, the checker's
	// dispatch-return-type override (via `ImplFuncTypes`) sees an empty
	// index and can't correct same-file multi-impl calls' return types.
	PopulateProjectImplFuncTypes(idx, filesByKey)
	// Same "stdlib FAs are already typed" reasoning: receiver identities
	// can be read off Origin immediately. Without this the single-file
	// path registers every stdlib `Error` under one bare key, and
	// random.Error / calendar.Error collide on the Debug slot.
	PopulateQualifiedReceivers(idx, filesByKey)
	// And interface identities, for the same reason on the other side of
	// the pair: without it a bound naming one `Renderer` is satisfied by an
	// impl written against another. See interface_identity.go.
	PopulateImplIfaceOrigins(idx, filesByKey)
	// And the type-method table's identity key, for the third time on the same
	// precondition: without it `Error.to_string` resolves to whichever of
	// std/calendar and std/random the union loop's map iteration happened
	// to visit first. See type_method_identity.go.
	PopulateTypeMethodIdentities(idx, filesByKey)
	PopulateEnumDecls(idx, filesByKey)
	// And the conformance table's identity key, for the fourth time on the
	// same precondition: without it `Equatable.equal` on a value whose type
	// is a user's own `Error` is judged satisfied by std/calendar's
	// `derive Equatable for Error`. See missing_impl_identity.go.
	PopulateImplsByIdentity(idx, filesByKey)
	return idx
}

// PopulateProjectImplFuncTypes fills idx.ImplFuncTypes from every reachable
// FA's typed impl Symbols. Must run AFTER BuildTypes — sym.Type on impl
// FuncDef Symbols is set during that pass. Parallels the ImplTypeArgs
// re-aggregation in project_build.go: buildProjectImplIndex runs before
// Sweep C-types so its initial pass sees nil Types; this pass catches up.
//
// Idempotent: subsequent calls overwrite with the same value (an impl's
// Type doesn't change post-BuildTypes). Order over filesByKey is
// irrelevant since each impl has exactly one owning FA.
func PopulateProjectImplFuncTypes(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis) {
	if idx == nil {
		return
	}
	for fn, path := range idx.ImplFiles {
		noteStdlibWork(path)
		ownerFA, ok := filesByKey[path]
		if !ok || ownerFA == nil {
			continue
		}
		sym, ok := ownerFA.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
		if !ok || sym == nil || sym.Type == nil {
			continue
		}
		idx.ImplFuncTypes[fn] = sym.Type
	}
}

// PopulateProjectTypeMethodSymbols refreshes idx.TypeMethods after BuildTypes
// has populated per-file method Symbol.Type fields. The initial project index is
// built before Sweep C, so it may contain the right public/synthesized method
// symbol with a nil Type. Hover and type-qualified dispatch need the post-type
// symbol.
func PopulateProjectTypeMethodSymbols(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis) {
	if idx == nil {
		return
	}
	for _, key := range stdlibFirstKeys(filesByKey) {
		fa := filesByKey[key]
		noteStdlibWork(key)
		if fa == nil {
			continue
		}
		for typeName, byMethod := range fa.TypeMethods {
			for methodName, sym := range byMethod {
				if sym == nil || (sym.Visibility == ast.Private && !sym.IsImplMethod) {
					continue
				}
				if idx.TypeMethods[typeName] == nil {
					idx.TypeMethods[typeName] = make(map[string]*Symbol)
				}
				existing := idx.TypeMethods[typeName][methodName]
				if existing == nil || (existing.Type == nil && sym.Type != nil) {
					idx.TypeMethods[typeName][methodName] = sym
					if key != "" {
						if idx.TypeMethodModule[typeName] == nil {
							idx.TypeMethodModule[typeName] = make(map[string]string)
						}
						idx.TypeMethodModule[typeName][methodName] = key
					}
				}
			}
		}
	}
}

// detectImplCollisions reports each impl block that implements an
// interface for a type another block of the program already implements it
// for (spec §12 *Implementations*): a type implements an interface once
// at each set of type arguments, whether a block is written, derived or
// the universal Debug the front end writes, and whatever functions it
// writes. Two empty `impl Marked for P` blocks are a duplicate.
//
// Blocks are grouped by implBlockGroupKey, the interface's identity and
// the receiver's declaration, and each group is split by the interface's
// type arguments compared as types (instantiationsOf), all read off the
// block's record (ImplTypeArgs). So `impl json.ToJson for U` and `impl
// ToJson for U` in a file whose `ToJson` is std's are one interface's
// impls, impls of two interfaces of one name are not, and `impl From<A>
// for T` beside `impl From<B> for T` are two instantiations, as are `impl
// From<a.Error> for T` and `impl From<b.Error> for T` for two files'
// `Error`. `impl Holds<T> for Wrap<T>` beside `impl Holds<Int> for
// Wrap<T>` is the overlap checkImplInstantiations reports, not a
// duplicate.
//
// The universal Debug of an opaque or host type yields to an explicit
// Debug for the type (ImplPrecedence), as a call reaches the explicit one:
// a REPL input's derive, or an `impl Debug` in
// another file than the type's.
//
// Each group of two or more is one error, at the block a reader can act
// on (collisionAnchor), with another block of the group as its related
// location. A derive beside a hand-written impl in the same module is the
// spec's `derive ... conflicts with a hand-written impl`, whichever
// spelling each names the interface by. A hand-written impl that collides
// with another module's derived one, as a program's `impl Equatable for
// Bool` does with std's, is a duplicate that names both modules.
//
// blocks is every interface impl block of the program (programImplBlocks),
// names says how the error names a receiver, and pathOf answers a module
// key's file, for the error's File.
func detectImplCollisions(blocks []implBlockAt, names receiverNames, pathOf func(key string) string) []TypeError {
	groups := map[string][]implBlockAt{}
	for _, b := range blocks {
		noteStdlibWork(b.key)
		if k, ok := implBlockGroupKey(b.info); ok {
			groups[k] = append(groups[k], b)
		}
	}
	keys := make([]string, 0, len(groups))
	for k, group := range groups {
		if len(group) > 1 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var errs []TypeError
	for _, k := range keys {
		insts := instantiationsOf(groups[k])
		for _, inst := range insts {
			group := withoutYieldingImpls(inst)
			if len(group) < 2 {
				continue
			}
			sort.SliceStable(group, func(i, j int) bool {
				if group[i].key != group[j].key {
					return group[i].key < group[j].key
				}
				li, ci := implBlockPos(group[i].info.Block)
				lj, cj := implBlockPos(group[j].info.Block)
				if li != lj {
					return li < lj
				}
				return ci < cj
			})
			errs = append(errs, implCollisionError(group, insts, names, pathOf))
		}
	}
	return errs
}

// instantiationsOf splits group, the blocks of one interface for one
// receiver, by the interface's type arguments each implements, compared as
// types (TypesIdentical), so a type argument is its declaration and not
// its name: `From<a.Error>` and `From<b.Error>` are two instantiations. The
// parts are in the order of their first blocks in group.
func instantiationsOf(group []implBlockAt) [][]implBlockAt {
	var out [][]implBlockAt
	for _, b := range group {
		at := slices.IndexFunc(out, func(inst []implBlockAt) bool {
			return sameTypeArgs(inst[0].info.Args, b.info.Args)
		})
		if at < 0 {
			out = append(out, nil)
			at = len(out) - 1
		}
		out[at] = append(out[at], b)
	}
	return out
}

// sameTypeArgs reports whether a and b, two impls' interface type
// arguments, are one list of types (TypesIdentical): one instantiation.
// Coherence, the candidates a call chooses among (implCandidates) and the
// impl a call's, a bound's or a conversion's type arguments name all ask
// this, so `From<Circle>` and `From<Shape>` for a Shape that embeds Circle
// are two impls everywhere.
func sameTypeArgs(a, b []Type) bool {
	return slices.EqualFunc(a, b, TypesIdentical)
}

// implBlockAt is an interface impl block's record and the module key of the
// program file that writes it.
type implBlockAt struct {
	key  string
	info *ImplTypeArgs
}

// programImplBlocks answers every interface impl block files' analyses
// record (FileAnalysis.ImplBlocks), each once: a std module reached under
// two keys, or analysed twice, holds its blocks at one place, so a block is
// identified by its file's origin and its source (implBlockKey). A block
// written for a declaration refused for its name (synthesizedForRefusedDecl)
// is no impl.
func programImplBlocks(files map[string]*FileAnalysis) []implBlockAt {
	var out []implBlockAt
	seen := map[string]bool{}
	for _, key := range stdlibFirstKeys(files) {
		fa := files[key]
		if fa == nil {
			continue
		}
		for _, byIface := range fa.ImplBlocks {
			for _, infos := range byIface {
				for _, info := range infos {
					if info == nil || info.Block == nil || synthesizedForRefusedDecl(fa, info.Block) {
						continue
					}
					id := fa.Origin + "\x00" + implBlockKey(info.Block)
					if seen[id] {
						continue
					}
					seen[id] = true
					out = append(out, implBlockAt{key: key, info: info})
				}
			}
		}
	}
	return out
}

// implBlockGroupKey is the key detectImplCollisions groups an impl block
// by, read off its record: the interface's name and declaring origin and
// the receiver's identity (implReceiverIdentity). The interface's type
// arguments, written or solved from the block's functions, are types, not
// a key: instantiationsOf compares them. ok is false for a block whose
// receiver did not resolve.
func implBlockGroupKey(info *ImplTypeArgs) (string, bool) {
	recv, _, ok := implReceiverIdentity(info.Receiver, receiverNames{})
	if !ok || info.Interface == "" {
		return "", false
	}
	return info.Interface + "\x00" + info.Origin + "\x00" + recv, true
}

// implReceiverIdentity is an impl block's receiver as coherence keys it,
// its declaration whatever its type arguments (a type implements an
// interface once, not once per instance), and as a diagnostic names it: a
// struct, enum or distinct type by its declaring origin and name, shown as
// the declaring module's last segment and the name (`bool.Bool`), or the
// bare name for one the program entry declares; a built-in primitive, one
// type wherever it is named, by its name alone, shown through the module
// that declares it (`int.Int`).
// receiverNames is what a coherence error names a receiver through: the
// program entry's origin (entryOriginOf), whose types are named bare, and
// each primitive's declaring module (primitiveOriginIndex).
type receiverNames struct {
	entry      string
	primitives map[string]string
}

func implReceiverIdentity(t Type, names receiverNames) (key, display string, ok bool) {
	t = resolveTypeVar(t)
	if t == nil {
		return "", "", false
	}
	name := concreteTypeName(t)
	if name == "" {
		return "", "", false
	}
	if _, prim := t.(*PrimitiveType); prim {
		if origin := names.primitives[name]; origin != "" {
			return name, lastSegment(origin) + "." + name, true
		}
		return name, name, true
	}
	origin, nominal := nominalOrigin(t)
	if !nominal || origin == "" || origin == OriginUnresolved || origin == OriginEntry || origin == names.entry {
		return origin + receiverOriginSep + name, name, true
	}
	return origin + receiverOriginSep + name, lastSegment(origin) + "." + name, true
}

// withoutYieldingImpls is group without its universal Debug blocks when an
// explicit block is among them (ImplPrecedence), or group itself.
func withoutYieldingImpls(group []implBlockAt) []implBlockAt {
	explicit := make([]implBlockAt, 0, len(group))
	for _, b := range group {
		if ImplPrecedence(b.info.Block) < implUniversal {
			explicit = append(explicit, b)
		}
	}
	if len(explicit) == 0 {
		return group
	}
	return explicit
}

// implBlockPos is where a diagnostic about impl block b points: its header;
// for a derive's block, the interface the derive names; for the universal
// Debug the front end writes, the declaration it is written for.
func implBlockPos(b *ast.ImplBlock) (int, int) {
	if !IsSynthesizedLine(b.Line) {
		return b.Line, b.Col
	}
	if line, col := typeExprPos(b.Interface, 0, 0); line > 0 && !IsSynthesizedLine(line) {
		return line, col
	}
	return b.SynthOriginLine, b.SynthOriginCol
}

// funcDefReceiverBaseName is the base name of an impl method's receiver —
// the first parameter's type. Returns "" when there is no first param or
// its type is unannotated/unresolvable.
func funcDefReceiverBaseName(fn *ast.FuncDef) string {
	if len(fn.Params) == 0 || fn.Params[0].TypeAnnotation == nil {
		return ""
	}
	return TypeExprBaseName(fn.Params[0].TypeAnnotation)
}

// receiverBaseName returns the receiver base name for a (possibly block-form)
// impl method. For block-form impls the header receiver is recorded in
// `receivers` (the item's first param is `self`, unresolvable by
// funcDefReceiverBaseName); the override wins when present. Otherwise
// fall back to the first-parameter type.
func receiverBaseName(fn *ast.FuncDef, receivers map[*ast.FuncDef]string) string {
	if receivers != nil {
		if r, ok := receivers[fn]; ok && r != "" {
			return r
		}
	}
	return funcDefReceiverBaseName(fn)
}

// implCollisionError builds the diagnostic for group, two or more impl
// blocks of one instantiation in source order, among insts, every
// instantiation of the interface for the receiver (instantiationsOf). It
// names every home module so the user can find and remove the duplicate,
// is anchored by collisionAnchor at a block the reader can open, and
// points at another of the program's blocks of the group.
func implCollisionError(group []implBlockAt, insts [][]implBlockAt, names receiverNames, pathOf func(key string) string) TypeError {
	first := group[0].info
	_, recv, _ := implReceiverIdentity(first.Receiver, names)
	label := first.Interface
	if first.Written && len(first.Args) > 0 {
		label = instantiationLabel(first, insts)
	}
	mods := make([]string, 0, len(group))
	seen := map[string]bool{}
	for _, b := range group {
		m := b.key
		if m == "" {
			m = "<project entry>"
		}
		if !seen[m] {
			seen[m] = true
			mods = append(mods, m)
		}
	}
	sort.Strings(mods)
	where := "by modules " + strings.Join(mods, ", ")
	if len(mods) == 1 {
		// Every block is in one module, which the error's file names; the
		// related location is the other block.
		where = "twice in this module"
	}
	msg := fmt.Sprintf(
		"duplicate impl: `%s` is implemented for `%s` %s; a type implements an interface once at each set of type arguments",
		label, recv, where)
	// A derive beside a hand-written impl in its own module (spec §12
	// *Derive*): the derive sits in the type's file, so the two are one
	// author's. A derived impl in another module, std's derived Equatable
	// for Bool among them, is the other half of a duplicate like any impl.
	derivedIn, writtenIn := map[string]bool{}, map[string]bool{}
	for _, b := range group {
		switch ImplPrecedence(b.info.Block) {
		case implWritten:
			writtenIn[b.key] = true
		case implDerived:
			if deriveSupported[first.Interface] {
				derivedIn[b.key] = true
			}
		}
	}
	beside := false
	for m := range derivedIn {
		beside = beside || writtenIn[m]
	}
	if beside {
		// The anchor is the hand-written one.
		msg = fmt.Sprintf(
			"`derive %s for %s` conflicts with a hand-written `impl %s for %s` block — a derived impl is synthesized whole, so it cannot coexist with a manual impl; drop one",
			first.Interface, recv, first.Interface, recv)
	}
	at := collisionAnchor(group)
	anchor := group[at]
	line, col := implBlockPos(anchor.info.Block)
	err := TypeError{
		Line:    line,
		Col:     col,
		Message: msg,
		File:    fileAt(pathOf, anchor.key),
	}
	// The first other block of the program's own, as the related location:
	// a std block is named by its module in the message alone.
	for i, other := range group {
		if i == at || strings.HasPrefix(other.key, "std/") {
			continue
		}
		otherLine, otherCol := implBlockPos(other.info.Block)
		related := RelatedInfo{Line: otherLine, Col: otherCol, Message: "the other impl is here"}
		if other.key != anchor.key {
			related.File = fileAt(pathOf, other.key)
		}
		err.Related = []RelatedInfo{related}
		break
	}
	return err
}

// instantiationLabel is the interface at info's type arguments as a
// collision names it, `From<Error>`, with a type qualified by its module
// (`From<a.Error>`) where another instantiation among insts prints the same
// with another declaration of its name (distinctTypeTexts).
func instantiationLabel(info *ImplTypeArgs, insts [][]implBlockAt) string {
	others := make([]Type, len(insts))
	for i, inst := range insts {
		others[i] = &InterfaceType{Name: info.Interface, TypeArgs: inst[0].info.Args}
	}
	return distinctTypeTexts("", []Type{&InterfaceType{Name: info.Interface, TypeArgs: info.Args}}, others)[0]
}

// fileAt is the path of the file with module key key, which a program-level
// diagnostic names as its file (TypeError.File): pathOf's answer, or "" when
// there is no pathOf.
func fileAt(pathOf func(key string) string, key string) string {
	if pathOf == nil {
		return ""
	}
	return pathOf(key)
}

// collisionAnchor is the index in group of the block where the error is
// reported: a block a reader can act on, a project block over a stdlib one
// and a written one over a synthesized one, and among those the last, so a
// second block in one file is reported at the second. That keeps a std
// block's position, in a file the user cannot edit, and a synthesized
// block's, in the derive band, out of the report.
func collisionAnchor(group []implBlockAt) int {
	best, bestScore := 0, -1
	for i, b := range group {
		score := 0
		if !strings.HasPrefix(b.key, "std/") {
			score += 2
		}
		if !IsSynthesizedLine(b.info.Block.Line) {
			score++
		}
		if score >= bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

// detectOrphanImpls reports a TypeError for every impl whose
// interface AND receiver-type are BOTH declared outside the impl's
// own package. The orphan rule requires at least one of (interface,
// receiver-type) to be local to the implementing package; without
// that anchor, two different libraries can ship the same
// (Iface, Type) pair and the analyzer's collision check has no way
// to catch the conflict at any single user's build.
//
// Inputs:
//
//   - index: the interface-method-impl index (Program.IfaceMethodImpls
//     unioned with stdlib).
//   - declPackage: base-name → declaring short-package-name, as built
//     by declaringPackageIndex. A missing entry on EITHER side means
//     the analyzer couldn't resolve that name; we silently skip
//     rather than double-fire as "orphan" on already-broken code or
//     emit a confusing `for X (declared in package "")` message.
//   - implPackageOf: returns the short-package-name owning a given
//     impl FuncDef. Caller derives this from its files-map + the
//     same first-segment / entry-package logic declaringPackageIndex
//     uses for project files. A "" return means unkeyable —
//     conservative skip (same posture as funcDefReceiverBaseName == "").
//   - fileOf: returns the path of the file declaring a given impl
//     FuncDef, which the error names (TypeError.File); nil names none.
//
// Complements detectImplCollisions: collisions reject duplicate
// (Iface, Type) pairs within one build; the orphan rule prevents
// such pairs from being constructible across unrelated builds in
// the first place. Together they make impl coherence a global
// property the package ecosystem can rely on.
func detectOrphanImpls(
	index map[string]map[string][]*ast.FuncDef,
	declPackage map[string][]string,
	implPackageOf func(*ast.FuncDef) string,
	receivers map[*ast.FuncDef]string,
	facts *orphanFacts,
	fileOf func(*ast.FuncDef) string,
) []TypeError {
	var errs []TypeError
	ifaceNames := make([]string, 0, len(index))
	for iface := range index {
		ifaceNames = append(ifaceNames, iface)
	}
	sort.Strings(ifaceNames)

	for _, iface := range ifaceNames {
		ifacePkg, ifaceKnown := declaringPackageOf(declPackage, iface)
		byMethod := index[iface]
		methodNames := make([]string, 0, len(byMethod))
		for m := range byMethod {
			methodNames = append(methodNames, m)
		}
		sort.Strings(methodNames)

		for _, method := range methodNames {
			for _, fn := range byMethod[method] {
				recv := receiverBaseName(fn, receivers)
				if recv == "" {
					continue // unkeyable; collision check skips these too
				}
				recvPkg, recvKnown := declaringPackageOf(declPackage, recv)
				// Plan called for `&&` (skip only when BOTH unresolved).
				// `||` is the shipped behavior: emitting an orphan error
				// with one side unresolved would print `(declared in
				// package "")`, doubling up on the unknown-name diagnostic
				// the analyzer's other passes already report at the use
				// site. Pinned by TestDetectOrphanImpls_SkipsWhenRecvUnresolved
				// and TestDetectOrphanImpls_SkipsWhenIfaceUnresolved.
				// Trade-off: a real orphan is masked when an unrelated
				// unresolved-name bug coexists in the same build.
				if ifaceKnown && !recvKnown && facts.bareReceiver(fn, iface, recv) {
					// `impl<T> Multiply<Rate, T> for T`: a receiver
					// that is the impl's own type parameter is declared
					// nowhere, and claims every type, so only a local
					// interface admits the impl.
					recvKnown, recvPkg = true, ""
				}
				if !ifaceKnown || !recvKnown {
					continue
				}
				implPkg := implPackageOf(fn)
				if implPkg == "" {
					continue // unkeyable; conservative skip
				}
				// Membership, not equality: a name declared in two
				// packages is local if EITHER declaration is in the
				// impl.s package. Comparing against one chosen package
				// made a user.s own type look foreign whenever it
				// shared a base name with a stdlib one.
				if declaredInPackage(declPackage, iface, implPkg) || declaredInPackage(declPackage, recv, implPkg) {
					continue // local on at least one side — rule satisfied
				}
				// Neither side is local: a type argument the package
				// declares may still admit the impl (orphan_type_args.go).
				admitted, reason := facts.verdict(fn, iface, recv, implPkg)
				if admitted {
					continue
				}
				e := orphanImplError(iface, method, recv, ifacePkg, recvPkg, implPkg, reason, fn)
				if fileOf != nil {
					e.File = fileOf(fn)
				}
				errs = append(errs, e)
			}
		}
	}
	return errs
}

// orphanImplError builds the diagnostic for one orphan impl. It is
// anchored at the FuncDef's position and names both foreign sides plus
// the offending impl's home file, so the user can see the full
// triangle that violates the rule.
//
// reason, when not empty, says why a type argument the package declares does
// not admit the impl either.
func orphanImplError(iface, method, recv, ifacePkg, recvPkg, implPkg, reason string, fn *ast.FuncDef) TypeError {
	why := "the interface, the implementing type, or a type heading a type argument the interface's functions take must be declared in the implementing package"
	if reason != "" {
		why = reason
	}
	forRecv := fmt.Sprintf("`%s` (declared in package %q)", recv, recvPkg)
	if recvPkg == "" {
		forRecv = fmt.Sprintf("the type parameter `%s`", recv)
	}
	return TypeError{
		Line: fn.Line,
		Col:  fn.Col,
		Message: fmt.Sprintf(
			"orphan impl: `%s` (declared in package %q) for %s cannot be implemented in package %q (function `%s`): %s",
			iface, ifacePkg, forRecv, implPkg, method, why),
	}
}

// detectInherentImplCollisions reports a duplicate-definition error for each
// type function name declared more than once on the SAME receiver type —
// same by nominal identity, `(declaring file, name)`, not merely by printed
// name. Two types that share a bare name are two types, and each keeps its own
// functions; grouping them together made `import std::calendar` un-declare
// `Date.new` in the importer's own file. See inherent_identity.go for the
// reproducer, and for what an unresolved receiver origin means here (it falls
// back to the bare grouping, so the check never becomes weaker than it was).
//
// Deterministic ordering for stable diagnostics: bare keys sorted, buckets in
// first-appearance order within a key, position-sorted anchor within a bucket.
func detectInherentImplCollisions(inherents []InherentMethodRecord, pathOf func(key string) string) []TypeError {
	byKey := map[string][]InherentMethodRecord{}
	for _, rec := range inherents {
		if rec.Receiver == "" || rec.Method == "" {
			continue
		}
		key := rec.Receiver + "\x00" + rec.Method
		byKey[key] = append(byKey[key], rec)
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var errs []TypeError
	for _, k := range keys {
		group := byKey[k]
		if len(group) < 2 {
			continue
		}
		// Anchor at the earliest source position for a stable message.
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Fn.Line != group[j].Fn.Line {
				return group[i].Fn.Line < group[j].Fn.Line
			}
			return group[i].Fn.Col < group[j].Fn.Col
		})
		for _, bucket := range inherentOriginBuckets(group) {
			if len(bucket) < 2 {
				continue
			}
			first := bucket[0]
			errs = append(errs, TypeError{
				Line: first.Fn.Line,
				Col:  first.Fn.Col,
				Message: fmt.Sprintf(
					"duplicate type-owned function: `%s.%s` is defined %d times — a type may define each function at most once",
					first.Receiver, first.Method, len(bucket)),
				File: fileAt(pathOf, first.Module),
			})
		}
	}
	return errs
}

// detectInherentImplNameClashes reports each type's own (inherent) function
// whose name is also a function the type's interface impls write. Nomi does
// not overload, and `Type.name(...)` names both: which one a call reached
// depended on the interface (`Box.multiply(b, 1.5)` beside an
// `impl Multiply<Int, Box> for Box` reached the impl's `multiply` and failed
// "argument 2: expected Int, got Float"). The error is at the
// inherent function's name whichever is declared first, once per inherent
// function, naming the first clashing impl function by interface name. Every
// type implements Debug, so a function named as one of Debug's clashes
// with it for a type with no Debug block too.
//
// A type is its nominal identity, `(declaring file, name)`: an inherent
// record's ReceiverOrigin against the origin of the impl's receiver as its
// home file resolves it. A record or an impl whose origin is unresolved (an
// impl over a type parameter) matches nothing. Both sides come from the same
// files (`ifaceImpls` and `inherents` are collected over one build's files),
// and a type's inherent functions and its impls are in one module's files
// (the orphan rule), so a std type's clashes are std's own, which a build of
// std reports.
func detectInherentImplNameClashes(
	inherents []InherentMethodRecord,
	ifaceImpls map[string]map[string][]*ast.FuncDef,
	implFiles map[*ast.FuncDef]string,
	receivers map[*ast.FuncDef]string,
	ifaceKeys map[*ast.FuncDef]string,
	filesByKey map[string]*FileAnalysis,
	primitives map[string]string,
	pathOf func(key string) string,
) []TypeError {
	if len(inherents) == 0 {
		return nil
	}
	ifaces := make([]string, 0, len(ifaceImpls))
	for iface := range ifaceImpls {
		ifaces = append(ifaces, iface)
	}
	sort.Strings(ifaces)
	recs := make([]InherentMethodRecord, len(inherents))
	copy(recs, inherents)
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].Module != recs[j].Module {
			return recs[i].Module < recs[j].Module
		}
		if recs[i].Fn.Line != recs[j].Fn.Line {
			return recs[i].Fn.Line < recs[j].Fn.Line
		}
		return recs[i].Fn.Col < recs[j].Fn.Col
	})
	type originKey struct{ module, qualifier, head string }
	origins := map[originKey]string{}
	originOf := func(fn *ast.FuncDef) string {
		fa := filesByKey[implFiles[fn]]
		k := originKey{module: implFiles[fn], qualifier: implReceiverModule(fa, fn), head: typeNameHead(receivers[fn])}
		origin, seen := origins[k]
		if !seen {
			origin = implReceiverOrigin(fa, k.qualifier, k.head, primitives)
			origins[k] = origin
		}
		return origin
	}
	var errs []TypeError
	for _, rec := range recs {
		if rec.Fn == nil || rec.Receiver == "" || rec.Method == "" || rec.ReceiverOrigin == OriginUnresolved {
			continue
		}
		var clash *ast.FuncDef
		var clashIface string
		for _, iface := range ifaces {
			for _, fn := range ifaceImpls[iface][rec.Method] {
				if receivers[fn] != rec.Receiver || originOf(fn) != rec.ReceiverOrigin {
					continue
				}
				clash, clashIface = fn, iface
				if key := ifaceKeys[fn]; key != "" {
					clashIface = key
				}
				break
			}
			if clash != nil {
				break
			}
		}
		if clash == nil && stdDebugDecl != nil && interfaceMethodFuncType(stdDebugDecl, rec.Method) != nil {
			// Every type implements Debug, with a block of it or by its
			// shape (universalDebugFor).
			clashIface = stdDebugDecl.Name
		}
		if clashIface == "" {
			continue
		}
		errs = append(errs, TypeError{
			Line: rec.Fn.Line,
			Col:  rec.Fn.Col,
			Message: fmt.Sprintf(
				"`%s.%s` is already `%s`'s `%s` for `%s`, and Nomi does not overload, so a call `%s.%s(...)` would name both; give this function another name",
				rec.Receiver, rec.Method, clashIface, rec.Method, rec.Receiver, rec.Receiver, rec.Method),
			File: fileAt(pathOf, rec.Module),
		})
	}
	return errs
}

// implFileBlocks is one file of the build and its top-level impl blocks, for
// the checks that need what an impl leaves unwritten.
type implFileBlocks struct {
	key    string
	fa     *FileAnalysis
	blocks []*ast.ImplBlock
}

// inheritedDefault is one default function an interface impl does not write,
// so the receiver gets the interface's: `least` for `impl Ord2 for Odd` when
// Ord2 declares `open fn least` and the block has no `fn least`.
type inheritedDefault struct {
	receiver string // the receiver's base name, as InherentMethodRecord.Receiver
	origin   string // the receiver type's declaring build key
	block    *ast.ImplBlock
	iface    string // the interface as the header writes it: `Ord2`, `Add<Int, Box>`
	open     bool
}

// detectInherentDefaultClashes reports each type's own (inherent) function
// whose name is a default function the type gets from an interface it
// implements without writing that function. `Type.name(...)` names both, as
// it does for a function the impl writes (detectInherentImplNameClashes), and
// a call reached neither: `Odd.least(a, b)` beside `impl Ord2 for Odd` was
// accepted and refused when it ran. The error is at the inherent function's
// name, once per function, naming the first interface by its header. An open
// default is overridden by writing it in the impl; a final one cannot be, so
// the only fix is another name. A derived impl is not written by hand, so
// there too the fix is another name.
func detectInherentDefaultClashes(
	inherents []InherentMethodRecord,
	files []implFileBlocks,
	primitives map[string]string,
	pathOf func(key string) string,
) []TypeError {
	if len(inherents) == 0 || len(files) == 0 {
		return nil
	}
	byMethod := map[string][]inheritedDefault{}
	for _, f := range files {
		for _, blk := range f.blocks {
			if blk.Interface == nil {
				continue
			}
			iface := implBlockInterfaceType(f.fa, blk.Interface)
			if iface == nil {
				continue
			}
			recv := TypeExprBaseName(blk.Receiver)
			if recv == "" {
				continue
			}
			var origin string
			resolved := false
			for _, m := range iface.Methods {
				if !m.HasDefault && !m.Extern {
					continue
				}
				if implBlockWrites(blk, m.Name) {
					continue
				}
				if !resolved {
					origin = implReceiverOrigin(f.fa, implBlockReceiverQualifier(blk), typeNameHead(recv), primitives)
					resolved = true
				}
				if origin == "" || origin == OriginUnresolved {
					break
				}
				byMethod[m.Name] = append(byMethod[m.Name], inheritedDefault{
					receiver: recv,
					origin:   origin,
					block:    blk,
					iface:    implInterfaceKey(f.fa.ModuleScope, blk.Interface),
					open:     m.Open,
				})
			}
		}
	}
	if len(byMethod) == 0 {
		return nil
	}
	recs := make([]InherentMethodRecord, len(inherents))
	copy(recs, inherents)
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].Module != recs[j].Module {
			return recs[i].Module < recs[j].Module
		}
		if recs[i].Fn.Line != recs[j].Fn.Line {
			return recs[i].Fn.Line < recs[j].Fn.Line
		}
		return recs[i].Fn.Col < recs[j].Fn.Col
	})
	var errs []TypeError
	for _, rec := range recs {
		if rec.Fn == nil || rec.Receiver == "" || rec.Method == "" || rec.ReceiverOrigin == OriginUnresolved {
			continue
		}
		cands := byMethod[rec.Method]
		var hit *inheritedDefault
		for i := range cands {
			if cands[i].receiver == rec.Receiver && cands[i].origin == rec.ReceiverOrigin {
				if hit == nil || cands[i].iface < hit.iface {
					hit = &cands[i]
				}
			}
		}
		if hit == nil {
			continue
		}
		errs = append(errs, TypeError{
			Line:    rec.Fn.Line,
			Col:     rec.Fn.Col,
			Message: inheritedDefaultClashMessage(rec.Receiver, rec.Method, *hit),
			File:    fileAt(pathOf, rec.Module),
		})
	}
	return errs
}

func inheritedDefaultClashMessage(recv, method string, d inheritedDefault) string {
	head := fmt.Sprintf(
		"`%s.%s` shadows the default `%s` that `%s` gives `%s`, and Nomi does not overload, so a call `%s.%s(...)` would name both; ",
		recv, method, method, d.iface, recv, recv, method)
	switch {
	case !d.open:
		return head + fmt.Sprintf("`%s`'s `%s` is final and cannot be overridden, so give this function another name", d.iface, method)
	case d.block.SynthOriginLine > 0:
		return head + fmt.Sprintf("the derived impl cannot override it, so give this function another name, or replace the derive with `impl %s for %s` and write `fn %s` there", d.iface, d.block.Receiver.TypeString(), method)
	default:
		return head + fmt.Sprintf("to override the default, write `fn %s` in `impl %s for %s` instead", method, d.iface, d.block.Receiver.TypeString())
	}
}

// implBlockWrites reports whether blk writes a function named name, as a
// `fn` or a `host fn`.
func implBlockWrites(blk *ast.ImplBlock, name string) bool {
	for _, item := range blk.Items {
		if n, _, _ := implItemNameAndPos(item); n == name {
			return true
		}
	}
	return false
}

// implBlockReceiverQualifier is the module an impl header qualifies its
// receiver with (`users` in `impl Html for users.User`), as the builder
// records it in ImplBlockReceiverModule; "" when it names the type bare.
func implBlockReceiverQualifier(blk *ast.ImplBlock) string {
	if q, ok := blk.Receiver.(*ast.QualifiedType); ok && !ast.IsPublic(q.Module) {
		return q.Module
	}
	return ""
}

// implBlockInterfaceType is the interface an impl header names, resolved in
// the impl's file: through the module a header qualifies it with
// (`impl json.ToJson for User`), else by name.
func implBlockInterfaceType(fa *FileAnalysis, te ast.TypeExpr) *InterfaceType {
	return interfaceThrough(fa, implBlockInterfaceQualifier(te), TypeExprBaseName(te))
}

// implBlockInterfaceQualifier is the module an impl header qualifies its
// interface with (`calendar` in `impl calendar.DateParts for Q`), as the
// builder records it in ImplBlockInterfaceModule; "" when it names the
// interface bare.
func implBlockInterfaceQualifier(te ast.TypeExpr) string {
	if q, ok := te.(*ast.QualifiedType); ok && !ast.IsPublic(q.Module) {
		return q.Module
	}
	return ""
}

// interfaceThrough is the interface name denotes in fa: in the scope of
// the module fa binds to module when an impl header qualifies the
// interface with it, so an interface of fa's own with the same name is
// not the one meant, else in fa's own scope.
func interfaceThrough(fa *FileAnalysis, module, name string) *InterfaceType {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	if module == "" {
		return lookupInterfaceType(fa, nil, name)
	}
	mod := fa.ModuleScope.Lookup(module)
	for mod != nil && mod.Resolved != nil {
		mod = mod.Resolved
	}
	if mod == nil || mod.Kind != SymbolModule || mod.ModuleScope == nil {
		return nil
	}
	sym := mod.ModuleScope.Lookup(name)
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil {
		return nil
	}
	it, _ := sym.Type.(*InterfaceType)
	return it
}

// implInterfaceName is the name of the interface declaration an impl
// header names, read in fa, the file that writes the block (implInterfaceNameIn
// over its scope). Every table that keys an impl by its interface keys it by
// this name, so an impl belongs to the interface its header resolves to, not
// to the header's spelling.
func implInterfaceName(fa *FileAnalysis, te ast.TypeExpr) string {
	if fa == nil {
		return TypeExprBaseName(te)
	}
	return implInterfaceNameIn(fa.ModuleScope, te)
}

// implInterfaceNameIn is the name of the interface declaration te names in
// scope: after `import std::resources.{Release as Freeable}`, `impl Freeable
// for Lock` names Release, and `impl a.Shown for P` names the interface a
// binds to Shown. A header the scope does not resolve to an interface keeps
// its spelling, which the checks that report it read.
func implInterfaceNameIn(scope *Scope, te ast.TypeExpr) string {
	name := TypeExprBaseName(te)
	if scope == nil || name == "" {
		return name
	}
	var sym *Symbol
	if module := implBlockInterfaceQualifier(te); module != "" {
		mod := scope.Lookup(module)
		for mod != nil && mod.Resolved != nil {
			mod = mod.Resolved
		}
		if mod != nil && mod.Kind == SymbolModule && mod.ModuleScope != nil {
			sym = mod.ModuleScope.Lookup(name)
		}
	} else if _, ownerQualified := te.(*ast.QualifiedType); !ownerQualified {
		sym = scope.Lookup(name)
	}
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil || sym.Kind != SymbolInterface || sym.Name == "" {
		return name
	}
	return sym.Name
}

// implInterfaceKey is te's text with its head spelled as the declaration it
// names (implInterfaceNameIn): `Release`, `From<DbError>`. The impl tables
// that key a block by its whole header (ImplBlockInterfaceKey) read it, so
// two headers that name one interface under two names are one key.
func implInterfaceKey(scope *Scope, te ast.TypeExpr) string {
	text := te.TypeString()
	written := TypeExprBaseName(te)
	if declared := implInterfaceNameIn(scope, te); declared != written {
		if _, qualified := te.(*ast.QualifiedType); !qualified && strings.HasPrefix(text, written) {
			return declared + strings.TrimPrefix(text, written)
		}
	}
	return text
}

func detectInherentImplBlockCollisions(blocks []InherentImplBlockRecord, pathOf func(key string) string) []TypeError {
	byKey := map[string][]InherentImplBlockRecord{}
	for _, rec := range blocks {
		if rec.Signature == "" {
			continue
		}
		key := rec.Module + "\x00" + rec.Signature
		byKey[key] = append(byKey[key], rec)
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var errs []TypeError
	for _, k := range keys {
		group := byKey[k]
		if len(group) < 2 {
			continue
		}
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Line != group[j].Line {
				return group[i].Line < group[j].Line
			}
			return group[i].Col < group[j].Col
		})
		first := group[0]
		errs = append(errs, TypeError{
			Line: first.Line,
			Col:  first.Col,
			Message: fmt.Sprintf(
				"duplicate type-owned impl: `impl %s` appears %d times in file %q — merge its functions into one block",
				first.Signature, len(group), displayModuleKey(first.Module)),
			File: fileAt(pathOf, first.Module),
		})
	}
	return errs
}

// detectInherentImplBlockOrphans reports each inherent impl block whose
// receiver type is not declared in the same file. Inherent impls define the
// type's own qualified API, so they must live with the type declaration; unlike
// interface impls, there is no separate interface-local anchor.
func detectInherentImplBlockOrphans(
	blocks []InherentImplBlockRecord,
	declaredLocally func(module, name string) bool,
	pathOf func(key string) string,
) []TypeError {
	// Deterministic ordering.
	sorted := append([]InherentImplBlockRecord(nil), blocks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Line != sorted[j].Line {
			return sorted[i].Line < sorted[j].Line
		}
		return sorted[i].Col < sorted[j].Col
	})

	var errs []TypeError
	for _, rec := range sorted {
		recv := rec.Receiver
		if recv == "" {
			continue
		}
		if rec.TypeVarReceiver {
			errs = append(errs, TypeError{
				Line: rec.Line,
				Col:  rec.Col,
				Message: fmt.Sprintf(
					"orphan impl: inherent `impl %s` is not allowed — a generic type variable is not a locality anchor; inherent impls require a concrete implementing type declared in this file",
					rec.Signature),
				File: fileAt(pathOf, rec.Module),
			})
			continue
		}
		if declaredLocally != nil && declaredLocally(rec.Module, recv) {
			continue
		}
		errs = append(errs, TypeError{
			Line: rec.Line,
			Col:  rec.Col,
			Message: fmt.Sprintf(
				"orphan impl: inherent `impl %s` in file %q cannot extend `%s` — inherent impls must be declared in the same file as their receiver type",
				rec.Signature, displayModuleKey(rec.Module), recv),
			File: fileAt(pathOf, rec.Module),
		})
	}
	return errs
}

func displayModuleKey(module string) string {
	if module == "" {
		return "<project entry>"
	}
	return module
}
