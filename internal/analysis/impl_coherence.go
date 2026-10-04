package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// ProjectImplIndex aggregates per-FA impl recordings into a project-
// level view that consumers (checker, derive synthesis, coherence checks,
// the LSP) read uniformly. Single source of truth for "which Ts implement
// which Ifaces" across the whole reachable program post-stdlib-
// globals-retirement; cache write-through (commit f34c61c) plus the
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
	// DECLARING module attached (`bool.Bool`, `shapes.Point`) — the
	// impl's runtime identity rather than its base name.
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
	IfaceOriginByPair map[string]map[string]string

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
	TypeMethodByIdentity        map[TypeMethodKey]*Symbol
	TypeMethodModuleByIdentity  map[TypeMethodKey]string
	TypeMethodIdentityConflicts []TypeMethodKey

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
// Duplicate FuncDef pointers (e.g. from shared per-FA map pointers
// post-broadcast in project_build.go) are deduplicated by pointer
// identity in the IfaceMethodImpls union — otherwise downstream
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
func buildProjectImplIndex(filesByKey map[string]*FileAnalysis) *ProjectImplIndex {
	idx := &ProjectImplIndex{
		Impls:                 make(map[string]map[string]bool),
		ImplManifest:          make(map[string]map[string][]Recording),
		IfaceMethodImpls:      make(map[string]map[string][]*ast.FuncDef),
		ImplFiles:             make(map[*ast.FuncDef]string),
		ImplTypeArgs:          make(map[string]map[string]*ImplTypeArgs),
		ImplTypeArgSets:       make(map[string]map[string][]*ImplTypeArgs),
		ImplFuncTypes:         make(map[*ast.FuncDef]Type),
		TypeMethods:           make(map[string]map[string]*Symbol),
		TypeMethodModule:      make(map[string]map[string]string),
		ImplBlockReceiver:     make(map[*ast.FuncDef]string),
		QualifiedReceiver:     make(map[*ast.FuncDef]string),
		ImplBlockInterfaceKey: make(map[*ast.FuncDef]string),
		ImplIfaceOrigin:       make(map[*ast.FuncDef]string),
		IfaceOriginByPair:     make(map[string]map[string]string),
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
	for key, fa := range filesByKey {
		for fn, recv := range fa.ImplBlockReceiver {
			if fn != nil && recv != "" {
				idx.ImplBlockReceiver[fn] = recv
			}
		}
		for fn, key := range fa.ImplBlockInterfaceKey {
			if fn != nil && key != "" {
				idx.ImplBlockInterfaceKey[fn] = key
			}
		}
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
				idx.ImplManifest[ifaceName][typeName] = existing
			}
		}
		for ifaceName, byMethod := range fa.IfaceMethodImpls {
			if idx.IfaceMethodImpls[ifaceName] == nil {
				idx.IfaceMethodImpls[ifaceName] = make(map[string][]*ast.FuncDef)
			}
			for methodName, fns := range byMethod {
				for _, fn := range fns {
					k := seenKey{iface: ifaceName, method: methodName, fn: fn}
					if seenImpls[k] {
						continue
					}
					seenImpls[k] = true
					idx.IfaceMethodImpls[ifaceName][methodName] = append(idx.IfaceMethodImpls[ifaceName][methodName], fn)
				}
			}
		}
		for fn, path := range fa.IfaceMethodImplFiles {
			idx.ImplFiles[fn] = path
			// ImplFuncTypes is populated separately in a
			// post-BuildTypes pass (see PopulateProjectImplFuncTypes),
			// since sym.Type isn't set on impl FuncDef Symbols until
			// BuildTypes runs — buildProjectImplIndex executes earlier
			// in the build pipeline (post Sweep B-impls, pre Sweep
			// C-types).
		}
		// Extern-impl parallel of the three unions above. IfaceMethodImplExterns
		// is shared across project FAs (same fix-up as IfaceMethodImpls), so it
		// dedups by the (iface, method, ext) triple; ImplExternFiles and
		// ImplBlockReceiverExtern overwrite idempotently (a given extern maps to
		// one path / one receiver), so a plain copy is correct.
		for ifaceName, byMethod := range fa.IfaceMethodImplExterns {
			if idx.IfaceMethodImplExterns[ifaceName] == nil {
				idx.IfaceMethodImplExterns[ifaceName] = make(map[string][]*ast.ExternFunc)
			}
			for methodName, exts := range byMethod {
				for _, ext := range exts {
					k := seenExternKey{iface: ifaceName, method: methodName, ext: ext}
					if seenExterns[k] {
						continue
					}
					seenExterns[k] = true
					idx.IfaceMethodImplExterns[ifaceName][methodName] = append(idx.IfaceMethodImplExterns[ifaceName][methodName], ext)
				}
			}
		}
		for ext, path := range fa.IfaceMethodImplExternFiles {
			idx.ImplExternFiles[ext] = path
		}
		for ext, recv := range fa.ImplBlockReceiverExtern {
			if ext != nil && recv != "" {
				idx.ImplBlockReceiverExtern[ext] = recv
			}
		}
		for ext, key := range fa.ImplBlockInterfaceKeyExtern {
			if ext != nil && key != "" {
				idx.ImplBlockInterfaceKeyExtern[ext] = key
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
		// Type-promoted exports: only Public impl-block members cross module
		// boundaries. A non-pub member stays reachable solely via its owning
		// FA's per-file TypeMethods (in-module-only, §4 block-as-module).
		for typeName, byMethod := range fa.TypeMethods {
			for methodName, sym := range byMethod {
				// Interface impl methods do not spell `pub`: their exported
				// surface is governed by the public interface contract. Derived
				// impl methods follow the same rule, so include impl methods in
				// the project type-method table even though their Symbol.Public
				// flag is false.
				if sym == nil || (!sym.Public && !sym.IsImplMethod) {
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
// dispatch slot for `Equatable.equal?` while `==` stayed structural.
// `Bool` escaped that because the prelude imports `bool.Bool.{self, ...}`
// and `Bool` is an *EnumType, which does carry an Origin; that asymmetry is
// what the probe measured.
func PopulateQualifiedReceivers(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis) {
	if idx == nil {
		return
	}
	if idx.QualifiedReceiver == nil {
		idx.QualifiedReceiver = make(map[*ast.FuncDef]string, len(idx.ImplBlockReceiver))
	}
	primitives := primitiveOriginIndex(filesByKey)
	populate := func(fn *ast.FuncDef) {
		if fn == nil {
			return
		}
		recv := idx.ReceiverOf(fn)
		if recv == "" {
			return
		}
		idx.QualifiedReceiver[fn] = qualifyReceiver(filesByKey[idx.ImplFiles[fn]], recv, primitives)
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
		if fa == nil || fa.ModuleScope == nil || fa.Origin == "" {
			continue
		}
		for name, sym := range fa.ModuleScope.Symbols {
			if sym == nil || sym.Resolved != nil {
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

// qualifyReceiver renders an impl block's receiver as its runtime
// identity: the module that DECLARED the receiver type, plus the name.
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
// This is the same qualifier a value's runtime type name carries
// (`bool.Bool`, `shapes.Point`). The two must agree, or registration and
// lookup miss each other.
//
// `primitives` (primitiveOriginIndex) covers the one kind whose declaring
// module is not recoverable from the type: a built-in *PrimitiveType is a shared
// singleton with no Origin, so `Int` reached through the prelude had no
// identity and keyed bare. Nil is accepted — the callers that pass nil
// (single-file paths) get exactly the previous answer.
func qualifyReceiver(fa *FileAnalysis, typeName string, primitives map[string]string) string {
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
	origin := declaringOriginOfType(fa, head, primitives)
	if origin == "" {
		origin = primitives[head]
	}
	if origin == "" || origin == OriginEntry {
		return typeName
	}
	return lastSegment(origin) + "." + typeName
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
// `stdlibFAs` (typically std.StdLib.Files) and assigns it to
// `fa.ProjectImpls`. The single-file BuildFileWithStdlib path leaves
// ProjectImpls nil — consumers (checker's implsContext /
// typeImplementsInterface, derive synthesis's CheckDeriveBounds /
// hasInterfaceImpl) then miss every stdlib (Type, Iface) pair, which
// surfaces as spurious "X does not implement Y" errors on routine
// stdlib calls (e.g. `io.inspect(42)` failing because the unifier
// can't see "Int implements Display"). Call sites that exercise the
// single-file path and want the same stdlib impl coverage the project
// path provides should call this after BuildFileWithStdlib.
//
// No-op when stdlibFAs is empty or fa is nil. Idempotent — overwrites
// any prior ProjectImpls pointer (callers that already went through
// BuildProject have their richer index; they shouldn't call this).
func AttachStdlibProjectImpls(fa *FileAnalysis, stdlibFAs map[string]*FileAnalysis) {
	if fa == nil || len(stdlibFAs) == 0 {
		return
	}
	filesByKey := make(map[string]*FileAnalysis, len(stdlibFAs))
	for name, sfa := range stdlibFAs {
		filesByKey["std/"+name] = sfa
	}
	fa.ProjectImpls = buildProjectImplIndex(filesByKey)
	// PopulateProjectImplFuncTypes fills the post-BuildTypes
	// ImplFuncTypes index — the stdlib FAs have already been through
	// BuildTypes during std.Load (via BuildProjectWithCache), so their
	// impl Symbol.Type fields are resolved and this pass can lift them
	// into the freshly-built index. Without this, the checker's
	// dispatch-return-type override (via `ImplFuncTypes`) sees an empty
	// index and can't correct same-file multi-impl calls' return types.
	PopulateProjectImplFuncTypes(fa.ProjectImpls, filesByKey)
	// Same "stdlib FAs are already typed" reasoning: receiver identities
	// can be read off Origin immediately. Without this the single-file
	// path registers every stdlib `Error` under one bare key, and
	// random.Error / calendar.Error collide on the Debug slot.
	PopulateQualifiedReceivers(fa.ProjectImpls, filesByKey)
	// And interface identities, for the same reason on the other side of
	// the pair: without it a bound naming one `Renderer` is satisfied by an
	// impl written against another. See interface_identity.go.
	PopulateImplIfaceOrigins(fa.ProjectImpls, filesByKey)
	// And the type-method table's identity key, for the third time on the same
	// precondition: without it `Error.to_string` resolves to whichever of
	// std/calendar and std/random the union loop's map iteration happened
	// to visit first. See type_method_identity.go.
	PopulateTypeMethodIdentities(fa.ProjectImpls, filesByKey)
	// And the conformance table's identity key, for the fourth time on the
	// same precondition: without it `Equatable.equal?` on a value whose type
	// is a user's own `Error` is judged satisfied by std/calendar's
	// `derive Equatable for Error`. See missing_impl_identity.go.
	PopulateImplsByIdentity(fa.ProjectImpls, filesByKey)
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
	for key, fa := range filesByKey {
		if fa == nil {
			continue
		}
		for typeName, byMethod := range fa.TypeMethods {
			for methodName, sym := range byMethod {
				if sym == nil || (!sym.Public && !sym.IsImplMethod) {
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

// detectImplCollisions reports a TypeError for every (interface declaration,
// method, receiver-type) triple implemented by two or more distinct impl
// FuncDefs. That triple is exactly the runtime dispatch table's key
// (root.dispatch["<origin>\x01Iface.method"]["TypeName"]), so two FuncDefs
// sharing it would silently last-wins-overwrite each other at registration
// time.
//
// # THE INTERFACE SIDE CARRIES IDENTITY, NOT A SPELLING
//
// Two files may each declare `pub interface Renderer`, and those are two
// interfaces. The check keys on `ifaceOrigins` (ProjectImplIndex.
// ImplIfaceOrigin) so an impl of one is not a duplicate of an impl of the
// other — ifaceOriginBuckets does the partition, with an unresolved origin
// joining every bucket so a build that cannot establish identity is no weaker
// than before. Accepting it is correct because dispatch keys each impl under
// its interface's declaration, not under the interface's bare name.
//
// `index` is interface name -> method name -> impl FuncDefs (the shape of
// FileAnalysis.IfaceMethodImpls); `files` maps each FuncDef to its
// import-form home module key (FileAnalysis.IfaceMethodImplFiles). The
// caller passes the project's `ifaceImpls` / `ifaceImplFiles` built by
// the per-file IndexImplBlockFuncDefs walk in BuildProjectWithCache —
// stdlib impls reach this index via the same per-file walk over
// stdlib FAs folded into filesByKey.
//
// # THE COHERENCE KEY IS THE RUNTIME DISPATCH KEY, DERIVED IN ONE PLACE
//
// Coherence keys on the receiver base name plus DispatchImplKey — the part of
// the interface header the runtime dispatch table can actually tell apart. That
// keeps ordinary impls unique (`Display for User`) and keeps distinct operator
// instantiations such as `Add<Days, Date> for Date` and `Add<Months, Date> for
// Date` distinct, while REJECTING two impls the table would have to store in one
// slot.
//
// Keying on the receiver plus the FULL interface header would be wrong in both
// directions the header can differ from the dispatch key:
//
//   - `impl Add<Days, Day> for Day` beside `impl Add<Days, Days> for Day` -
//     same receiver, same right-hand type, different OUTPUT. Both claim the
//     dispatch slot `Add.add` for `Day` with right-hand `Days`.
//   - `impl Holds<Int> for Box` beside `impl Holds<String> for Box` - a
//     NON-operator generic interface, whose runtime key is the receiver alone,
//     so every type argument is dropped rather than just the output. Both
//     claim `Holds.hold` for `Box`.
//
// Both are rejected with a diagnostic naming both headers. Regression:
// analysis.TestDetectImplCollisions_OperatorImplsDifferingOnlyInOutputCollide,
// ...NonOperatorGenericInstantiationsCollide, and the BuildProject-level pair
// in operator_coherence_project_test.go.
//
// The rejection is not conservatism: dispatch cannot hold two impls in one
// slot. Lifting the rejection needs the RUNTIME key to carry the output type (and, for a
// non-operator interface, the type arguments), which is a separate change and a
// language decision: nothing in Nomi's syntax selects an operator impl by its
// return type, and checkOperatorBinary takes the result type FROM the impl it
// resolved, so `(Add, Self, Rhs) -> Out` is a functional dependency. Rust spells
// the same dependency by making `Output` an associated type rather than a
// parameter; Nomi spells `Out` as a parameter, which makes the ill-formed
// program writable, and this check is what restores the dependency.
func detectImplCollisions(
	index map[string]map[string][]*ast.FuncDef,
	files map[*ast.FuncDef]string,
	receivers map[*ast.FuncDef]string,
	interfaceKeys map[*ast.FuncDef]string,
	ifaceOrigins map[*ast.FuncDef]string,
) []TypeError {
	var errs []TypeError
	// Deterministic iteration for stable error ordering.
	ifaceNames := make([]string, 0, len(index))
	for iface := range index {
		ifaceNames = append(ifaceNames, iface)
	}
	sort.Strings(ifaceNames)

	for _, iface := range ifaceNames {
		byMethod := index[iface]
		methodNames := make([]string, 0, len(byMethod))
		for m := range byMethod {
			methodNames = append(methodNames, m)
		}
		sort.Strings(methodNames)

		for _, method := range methodNames {
			fns := byMethod[method]
			// Group this (iface, method)'s FuncDefs by receiver base name and
			// DISPATCH key. Only the part of the interface header the runtime
			// table can tell apart is part of impl identity here: the right-hand
			// type for an operator interface, nothing for any other. Two impls
			// landing in one group are two impls the table would store in one
			// slot, which is the collision.
			byReceiver := map[string][]*ast.FuncDef{}
			for _, fn := range fns {
				recv := receiverBaseName(fn, receivers)
				if recv == "" {
					continue // unkeyable — skip, don't mis-group
				}
				ifaceKey := iface
				if interfaceKeys != nil && interfaceKeys[fn] != "" {
					ifaceKey = interfaceKeys[fn]
				}
				key := recv + "\x00" + DispatchImplKey(iface, ifaceKey)
				byReceiver[key] = append(byReceiver[key], fn)
			}
			receivers := make([]string, 0, len(byReceiver))
			for r := range byReceiver {
				receivers = append(receivers, r)
			}
			sort.Strings(receivers)

			for _, recv := range receivers {
				group := byReceiver[recv]
				displayRecv := implCollisionKeyReceiver(recv)
				// Universal default Debug gives every type a Debug impl, and
				// dispatch is keyed by unqualified base name — so a project type
				// can share a base name with an unrelated stdlib type (e.g. a
				// user `enum Direction` vs std/comparable's `Direction`). The
				// PROJECT type shadows the stdlib one. Mirror the runtime bridge — for
				// Debug, if any project (non-stdlib) impl exists for this base
				// name, the stdlib impl(s) are shadowed and don't count toward a
				// collision; only same-origin duplicates (2+ project) remain
				// genuine. Other interfaces are unaffected (no auto impls to
				// shadow).
				if iface == "Debug" {
					group = debugEffectiveCollisionGroup(group, files)
				}
				// Two impls that share a dispatch key but name two DIFFERENT
				// interface declarations are not a duplicate: dispatch keys
				// each under its interface's declaration. So partition by the interface's
				// declaring origin and report only within a bucket.
				for _, bucket := range ifaceOriginBuckets(group, ifaceOrigins) {
					if len(bucket) < 2 {
						continue
					}
					errs = append(errs, implCollisionError(iface, method, displayRecv, bucket, files, interfaceKeys))
				}
			}
		}
	}
	return errs
}

// CollisionImplIndex unions the reachable-set impl index with the project
// index, deduplicated BY DECLARATION rather than by pointer, and returns the
// pair detectImplCollisions consumes.
//
// # WHY THE UNION IS NEEDED
//
// `ifaceImpls` covers the files this build reached. A stdlib module the entry
// never reaches is absent from it, so its impls cannot collide with anything.
// `std/int` is the case that proves it: `Int`'s methods resolve through the
// prelude, so nothing makes `std/int` reachable, and at 41a60d35 a program
// with no imports could declare `impl Equatable for Int` and get no
// diagnostic at all — while the SAME program with `import { std/io }` was
// rejected, because std/io's own import chain pulled std/int in. A rule whose
// answer depends on an unrelated import is not a rule.
//
// # WHY DEDUPE BY DECLARATION AND NOT BY POINTER
//
// Measured: swapping the check straight onto `implIndex.IfaceMethodImpls`
// made `nomi run` report every std impl as a duplicate of itself — 100+
// diagnostics led by `Add<Bytes, Bytes> ... by modules std/bytes`, with ONE
// module named because both members were homed there. One std declaration
// reaches the project index as TWO FuncDef pointers: the FA `std.Load()`
// pre-analyzed, and the one this build parsed. They arrive under different
// filesByKey entries (the import-form key `bytes` from the demand-resolved
// cache, and `std/bytes` from the stdlibFAs fold), so the index's
// pointer-triple dedupe cannot see that they are one source item.
//
// (home, line, col, name) can. Two FuncDefs at one position in one module
// with one name ARE one declaration, and nothing in the language can produce
// two — the parser gives each item a distinct position. A member already in
// the reachable-set map wins the tie, because its receiver and interface-key
// entries are the ones the caller's other maps are keyed on.
func CollisionImplIndex(
	local map[string]map[string][]*ast.FuncDef,
	localFiles map[*ast.FuncDef]string,
	idx *ProjectImplIndex,
) (map[string]map[string][]*ast.FuncDef, map[*ast.FuncDef]string) {
	files := make(map[*ast.FuncDef]string, len(localFiles))
	if idx != nil {
		for fn, home := range idx.ImplFiles {
			files[fn] = home
		}
	}
	for fn, home := range localFiles {
		files[fn] = home
	}
	type declKey struct {
		home, name string
		line, col  int
	}
	out := make(map[string]map[string][]*ast.FuncDef, len(local))
	// Slot-local dedupe, for the same reason buildProjectImplIndex's is: one
	// FuncDef legitimately occupies more than one (iface, method) slot, and a
	// global seen-set would suppress every slot after the first.
	add := func(iface, method string, fns []*ast.FuncDef, seen map[declKey]bool) {
		for _, fn := range fns {
			if fn == nil {
				continue
			}
			k := declKey{home: files[fn], name: fn.Name, line: fn.Line, col: fn.Col}
			if seen[k] {
				continue
			}
			seen[k] = true
			out[iface][method] = append(out[iface][method], fn)
		}
	}
	ifaces := map[string]bool{}
	for iface := range local {
		ifaces[iface] = true
	}
	if idx != nil {
		for iface := range idx.IfaceMethodImpls {
			ifaces[iface] = true
		}
	}
	names := make([]string, 0, len(ifaces))
	for iface := range ifaces {
		names = append(names, iface)
	}
	sort.Strings(names)
	for _, iface := range names {
		out[iface] = map[string][]*ast.FuncDef{}
		methods := map[string]bool{}
		for method := range local[iface] {
			methods[method] = true
		}
		if idx != nil {
			for method := range idx.IfaceMethodImpls[iface] {
				methods[method] = true
			}
		}
		methodNames := make([]string, 0, len(methods))
		for method := range methods {
			methodNames = append(methodNames, method)
		}
		sort.Strings(methodNames)
		for _, method := range methodNames {
			seen := map[declKey]bool{}
			add(iface, method, local[iface][method], seen)
			if idx != nil {
				add(iface, method, idx.IfaceMethodImpls[iface][method], seen)
			}
		}
	}
	return out, files
}

// ifaceOriginBuckets partitions one dispatch-key group of impls — every member
// shares a (receiver, dispatch key) pair — into the sets that would genuinely
// be two impls of ONE interface declaration.
//
// An impl with a resolved interface origin belongs to that origin's bucket. An
// impl with `OriginUnresolved` belongs to ALL of them, because the lattice
// says an unresolved origin may be any of them and the check must not become
// weaker than it was when identity is unavailable; when nothing in the group
// resolved, the whole group is one bucket, which is exactly the pre-identity
// behaviour. Same rule and same reason as inherentOriginBuckets, applied on
// the interface side instead of the receiver side.
//
// Buckets come back in first-appearance order so the diagnostic a user sees
// does not depend on Go map iteration.
func ifaceOriginBuckets(group []*ast.FuncDef, ifaceOrigins map[*ast.FuncDef]string) [][]*ast.FuncDef {
	// A group is the impls sharing one dispatch key, so it is a handful at
	// most — a linear scan for distinctness beats allocating a set.
	var origins []string
	for _, fn := range group {
		origin := ifaceOrigins[fn]
		if origin == OriginUnresolved {
			continue
		}
		known := false
		for _, o := range origins {
			if o == origin {
				known = true
				break
			}
		}
		if !known {
			origins = append(origins, origin)
		}
	}
	// One origin (or none) means one bucket, and it is the whole group: an
	// unresolved member belongs to every bucket, so with a single resolved
	// origin there is nothing to partition. This is the shape of nearly every
	// group that reaches here, and it allocates nothing.
	if len(origins) <= 1 {
		return [][]*ast.FuncDef{group}
	}
	buckets := make([][]*ast.FuncDef, 0, len(origins))
	for _, origin := range origins {
		bucket := make([]*ast.FuncDef, 0, len(group))
		for _, fn := range group {
			if o := ifaceOrigins[fn]; o == origin || o == OriginUnresolved {
				bucket = append(bucket, fn)
			}
		}
		buckets = append(buckets, bucket)
	}
	return buckets
}

// implCollisionKeyReceiver recovers the receiver from a grouping key. The
// interface side is deliberately NOT recovered here: the key carries the
// DISPATCH key (`Add<Days>`), which is a fragment of no header the user wrote,
// so the diagnostic renders the interface from the group's own recorded headers
// instead.
func implCollisionKeyReceiver(key string) string {
	recv, _, _ := strings.Cut(key, "\x00")
	return recv
}

// debugEffectiveCollisionGroup applies the universal-Debug shadowing rule
// (option #1): a project type shadows a stdlib type of the same base name.
// If the group contains any project (non-stdlib) impl, the stdlib impls are
// dropped — they're shadowed at runtime by the impl bridge — so only the
// project impls compete for the single base-name dispatch slot. With no
// project impl, the group is returned unchanged (a genuine stdlib-internal
// duplicate would still be flagged). Stdlib home paths are spelled "std/...";
// the project entry is "" and sibling modules carry their own prefixes.
func debugEffectiveCollisionGroup(group []*ast.FuncDef, files map[*ast.FuncDef]string) []*ast.FuncDef {
	// Precedence step 1 — explicit beats auto. An auto-synth Debug impl
	// (AutoSynth) yields to any hand-written / @derive impl for the same
	// (Debug, T). This is what lets a type's explicit Debug live in a
	// different file than its declaration: the per-file synthesis pass still
	// emits an auto impl for the type (it can't see the sibling's explicit
	// one), but that auto impl drops out here.
	var explicit []*ast.FuncDef
	for _, fn := range group {
		if !fn.AutoSynth {
			explicit = append(explicit, fn)
		}
	}
	if len(explicit) > 0 {
		group = explicit
	}
	// Precedence step 2 — project beats stdlib (option #1). Among the
	// surviving impls, a project (entry "" or sibling) impl shadows a stdlib
	// one of the same base name.
	var project []*ast.FuncDef
	for _, fn := range group {
		path := files[fn]
		if path == "" || !strings.HasPrefix(path, "std/") {
			project = append(project, fn)
		}
	}
	if len(project) > 0 {
		return project
	}
	return group
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

// implCollisionError builds the diagnostic for one colliding group. It names
// every home module so the user can find and remove the duplicate, and is
// anchored by collisionAnchor at a position the reader can open.
//
// Group order does NOT answer this, and believing it did was a mistake this
// slice made and then measured. CollisionImplIndex adds the reachable-set
// members first, which puts the user's impl at index 0 for a program with no
// imports — but the reachable set ALSO holds std's impl once anything makes
// that module reachable, and there it comes first. So `impl Equatable for
// Int` in a file with `import { std/io }` anchored at `line 180, col 6`,
// std/int.nomi's line, in a file the user cannot open. The import-free
// version of the same program anchored correctly, which is why a test
// without an import passed with the anchor removed.
//
// The other bad position was `line 1433485312, col 1` for
// `impl Equatable for Bool`: the derive-synthesis reserved line band leaking
// into user-facing output.
//
// The reason it gives is EVALUATED, not asserted, and there are TWO shapes
// with two reasons. "A type may implement an interface at most once" is false
// for one of them, and a user reading that sentence would go looking for a
// duplicate that does not exist.
//
//   - The group's members name ONE interface at two different INSTANTIATIONS —
//     `Add<Days, Day>` beside `Add<Days, Days>`, or `Holds<Int>` beside
//     `Holds<String>`. Each pair is implemented once; what collides is the
//     dispatch key, which carries less than the header. `interfaceKeys` is what
//     distinguishes this shape, and the message names both headers because the
//     module list cannot: both impls are usually in the same file.
//   - Anything else is a genuine duplicate of one (interface, receiver) pair.
//
// A THIRD shape does not reach here: two DIFFERENT interfaces sharing a
// spelling, each implemented once for one receiver. It is accepted, because
// dispatch keys each impl under its interface's declaration —
// `detectImplCollisions` partitions by that origin
// before calling this (ifaceOriginBuckets), so every group arriving here names
// one declaration or has no identity to go on.
//
// Both remaining shapes stay REJECTED, and that is not conservatism: each puts
// two impls in one dispatch slot. Lifting either needs the RUNTIME key to carry what the
// header carries: the output type, or the type arguments.
func implCollisionError(
	iface, method, recv string,
	group []*ast.FuncDef,
	files map[*ast.FuncDef]string,
	interfaceKeys map[*ast.FuncDef]string,
) TypeError {
	mods := make([]string, 0, len(group))
	seen := map[string]bool{}
	for _, fn := range group {
		m := files[fn]
		if m == "" {
			m = "<project entry>"
		}
		if !seen[m] {
			seen[m] = true
			mods = append(mods, m)
		}
	}
	sort.Strings(mods)
	headers := distinctInterfaceHeaders(group, interfaceKeys)
	label := iface
	if len(headers) == 1 {
		label = headers[0]
	}
	msg := fmt.Sprintf(
		"duplicate impl: `%s` is implemented for `%s` by modules %s (function `%s`) — a type may implement an interface at most once",
		label, recv, strings.Join(mods, ", "), method)
	if len(headers) > 1 {
		msg = fmt.Sprintf("`%s` implements `%s` more than once at one dispatch key: %s. %s",
			recv, iface, quotedList(headers),
			dispatchKeyReason(iface, method, recv, group, interfaceKeys))
	}
	line, col := collisionAnchor(group, files)
	return TypeError{
		Line:    line,
		Col:     col,
		Message: msg,
	}
}

// collisionAnchor picks the position a reader can act on: a PROJECT impl over
// a stdlib one, and a real source line over a synthesized one.
//
// Both preferences come from a measured case — see implCollisionError's
// header for the two positions this replaces. IsSynthesizedLine is the
// established test for the derive-synthesis line band.
//
// Falls back to group[0] when every member is synthesized or homed in std,
// which is correct there: a stdlib-internal duplicate is a stdlib bug, and
// `std/...` is a position its maintainer can open.
func collisionAnchor(group []*ast.FuncDef, files map[*ast.FuncDef]string) (int, int) {
	best := group[0]
	bestScore := -1
	for _, fn := range group {
		score := 0
		if !strings.HasPrefix(files[fn], "std/") {
			score += 2
		}
		if !IsSynthesizedLine(fn.Line) {
			score++
		}
		if score > bestScore {
			best, bestScore = fn, score
		}
	}
	return best.Line, best.Col
}

// distinctInterfaceHeaders is the sorted set of interface headers the group's
// impls declared, deduplicated. Empty when no member recorded one, one entry
// when they all agree (the ordinary duplicate), more than one when the headers
// differ in a part the dispatch key does not carry.
func distinctInterfaceHeaders(group []*ast.FuncDef, interfaceKeys map[*ast.FuncDef]string) []string {
	if interfaceKeys == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(group))
	for _, fn := range group {
		key := interfaceKeys[fn]
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// dispatchKeyReason says WHY two impls share one slot. The two arms are NOT the
// same kind of statement, and the wording is deliberately different, because
// telling a user "your program is wrong" and "the compiler cannot do this yet"
// with one sentence is how an implementation limit gets remembered as a language
// rule.
//
// # THE OPERATOR ARM IS A SETTLED RULE, AND IT CITES THE SPEC
//
// docs/spec.md, "Generic Interfaces": a generic interface's type
// parameter "is *determined* by each implementor (one impl per type), so it is
// never written on the interface name where the implementor is already known …
// Where it earns a name is exactly where the implementor is *unknown* — an
// interface-typed parameter … or a bound". `Out` sits on both sides:
//
//	impl Add<Days, Day> for Day     determined by the impl -> not a discriminator
//	where L: Add<R, Out>            implementor unknown     -> earns its name
//
// So this is not a new special case for operators. It is the existing
// duplicate-impl rule reaching a case it was not reaching, because the key it
// grouped on carried a type argument the implementor determines. Bound solving is
// untouched — `Out` remains a type parameter, and it has to: `tests/
// 12-derives-and-standard-interfaces/add_test.nomi:90` names it in bound position
// four times. Every other component already agreed: the runtime dispatch key, the
// IR builder's `g.operImpls` (keyed `(interface, receiver, RHS)`), and all of the
// stdlib, in which no (receiver, right-hand type) pair carries two outputs
// (TestStdlibOperatorOutputIsDeterminedByReceiverAndRhs — 66 impls, 66 groups).
//
// # THE NON-OPERATOR ARM IS A LIMITATION, AND SAYS SO
//
// `impl Holds<Int> for Box` beside `impl Holds<String> for Box` is NOT an exotic
// shape — it is Rust's `impl From<i32> for Foo` / `impl From<String> for Foo`, one
// of that language's most-used patterns, dispatched on the argument type at the
// call site. Nomi cannot express it, and nobody decided that: the runtime key for
// a non-operator interface is the receiver ALONE (DispatchImplKey), so both
// impls land in one slot if they are not rejected here.
//
// Lifting it is a COSTED option rather than a wall, and the shape already exists:
// the runtime key would have to carry the interface's type arguments, exactly as
// operator dispatch already carries the right-hand type, and the IR builder's
// `g.operImpls` is the precedent for doing it in a SECOND index rather than by
// widening the shared one (widening `implsByIface` breaks `noEquatableImpl`, which
// reads it to establish ABSENCE — measured). Until someone pays for that, the
// diagnostic names the two things a user can write instead.
func dispatchKeyReason(iface, method, recv string, group []*ast.FuncDef, interfaceKeys map[*ast.FuncDef]string) string {
	op, isOperator := operatorInterfaceByName(iface)
	if !isOperator {
		return fmt.Sprintf(
			"Interface dispatch is keyed on the receiver ALONE today, so `%s.%s` has one slot for `%s` "+
				"and the type argument cannot choose between them. This is a LIMITATION of the current "+
				"dispatch key, not a rule about your program: lifting it means the runtime key carrying "+
				"the interface's type arguments, the way operator dispatch already carries its "+
				"right-hand type. Until then, write one named function per case, or take a single impl "+
				"over an enum of the argument types.",
			iface, method, recv)
	}
	rhs := ""
	for _, fn := range group {
		if r := OperatorInterfaceRhs(interfaceKeys[fn]); r != "" {
			rhs = r
			break
		}
	}
	return fmt.Sprintf(
		"These differ only in the OUTPUT type, which an impl DETERMINES rather than chooses: for "+
			"receiver `%s` and right-hand type `%s` there is one answer, so `%s.%s` has one slot and "+
			"`lhs %s rhs` would otherwise have two types. `Out` is a real parameter only where the "+
			"implementor is unknown (`where L: %s<R, Out>`). Give these different right-hand types, or "+
			"remove one.",
		recv, rhs, iface, method, op.Op, iface)
}

// quotedList renders `a`, `b` and `c` for a diagnostic.
func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = "`" + s + "`"
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
	}
}

// detectOrphanImpls reports a TypeError for every impl whose
// interface AND receiver-type are BOTH declared outside the impl's
// own module. The orphan rule requires at least one of (interface,
// receiver-type) to be local to the implementing module; without
// that anchor, two different libraries can ship the same
// (Iface, Type) pair and the analyzer's collision check has no way
// to catch the conflict at any single user's build.
//
// Inputs:
//
//   - index: the same interface-method-impl index detectImplCollisions
//     consumes (FileAnalysis.IfaceMethodImpls unioned with stdlib).
//   - declModule: base-name → declaring short-module-name, as built
//     by declaringModuleIndex. A missing entry on EITHER side means
//     the analyzer couldn't resolve that name; we silently skip
//     rather than double-fire as "orphan" on already-broken code or
//     emit a confusing `for X (declared in module "")` message.
//   - implModuleOf: returns the short-module-name owning a given
//     impl FuncDef. Caller derives this from its files-map + the
//     same first-segment / entry-module logic declaringModuleIndex
//     uses for project files. A "" return means unkeyable —
//     conservative skip (same posture as funcDefReceiverBaseName == "").
//
// Complements detectImplCollisions: collisions reject duplicate
// (Iface, Type) pairs within one build; the orphan rule prevents
// such pairs from being constructible across unrelated builds in
// the first place. Together they make impl coherence a global
// property the package ecosystem can rely on.
func detectOrphanImpls(
	index map[string]map[string][]*ast.FuncDef,
	declModule map[string][]string,
	implModuleOf func(*ast.FuncDef) string,
	receivers map[*ast.FuncDef]string,
) []TypeError {
	var errs []TypeError
	ifaceNames := make([]string, 0, len(index))
	for iface := range index {
		ifaceNames = append(ifaceNames, iface)
	}
	sort.Strings(ifaceNames)

	for _, iface := range ifaceNames {
		ifaceMod, ifaceKnown := declaringModuleOf(declModule, iface)
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
				recvMod, recvKnown := declaringModuleOf(declModule, recv)
				// Plan called for `&&` (skip only when BOTH unresolved).
				// `||` is the shipped behavior: emitting an orphan error
				// with one side unresolved would print `(declared in
				// module "")`, doubling up on the unknown-name diagnostic
				// the analyzer's other passes already report at the use
				// site. Pinned by TestDetectOrphanImpls_SkipsWhenRecvUnresolved
				// and TestDetectOrphanImpls_SkipsWhenIfaceUnresolved.
				// Trade-off: a real orphan is masked when an unrelated
				// unresolved-name bug coexists in the same build.
				if !ifaceKnown || !recvKnown {
					continue
				}
				implMod := implModuleOf(fn)
				if implMod == "" {
					continue // unkeyable; conservative skip
				}
				// Membership, not equality: a name declared in two
				// modules is local if EITHER declaration is in the
				// impl.s module. Comparing against one chosen module
				// made a user.s own type look foreign whenever it
				// shared a base name with a stdlib one.
				if declaredInModule(declModule, iface, implMod) || declaredInModule(declModule, recv, implMod) {
					continue // local on at least one side — rule satisfied
				}
				errs = append(errs, orphanImplError(iface, method, recv, ifaceMod, recvMod, implMod, fn))
			}
		}
	}
	return errs
}

// orphanImplError builds the diagnostic for one orphan impl. It is
// anchored at the FuncDef's position and names both foreign sides plus
// the offending impl's home file, so the user can see the full
// triangle that violates the rule.
func orphanImplError(iface, method, recv, ifaceMod, recvMod, implMod string, fn *ast.FuncDef) TypeError {
	return TypeError{
		Line: fn.Line,
		Col:  fn.Col,
		Message: fmt.Sprintf(
			"orphan impl: `%s` (declared in file %q) for `%s` (declared in file %q) cannot be implemented in file %q (function `%s`) — at least one of the interface or the implementing type must be declared in the implementing file",
			iface, ifaceMod, recv, recvMod, implMod, method),
	}
}

// detectInherentImplCollisions reports a duplicate-definition error for each
// type function name declared more than once on the SAME receiver type —
// same by nominal identity, `(declaring file, name)`, not merely by printed
// name. Two types that share a bare name are two types, and each keeps its own
// functions; grouping them together made `import std/calendar` un-declare
// `Date.new` in the importer's own file. See inherent_identity.go for the
// reproducer, and for what an unresolved receiver origin means here (it falls
// back to the bare grouping, so the check never becomes weaker than it was).
//
// Deterministic ordering for stable diagnostics: bare keys sorted, buckets in
// first-appearance order within a key, position-sorted anchor within a bucket.
func detectInherentImplCollisions(inherents []InherentMethodRecord) []TypeError {
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
			})
		}
	}
	return errs
}

func detectInherentImplBlockCollisions(blocks []InherentImplBlockRecord) []TypeError {
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
				"duplicate type-owned impl: `impl %s { ... }` appears %d times in file %q — merge its functions into one block",
				first.Signature, len(group), displayModuleKey(first.Module)),
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
					"orphan impl: inherent `impl %s { ... }` is not allowed — a generic type variable is not a locality anchor; inherent impls require a concrete implementing type declared in this file",
					rec.Signature),
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
				"orphan impl: inherent `impl %s { ... }` in file %q cannot extend `%s` — inherent impls must be declared in the same file as their receiver type",
				rec.Signature, displayModuleKey(rec.Module), recv),
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
