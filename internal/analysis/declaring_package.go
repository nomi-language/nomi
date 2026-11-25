package analysis

import (
	"maps"
	"slices"
	"strings"
)

// declaringPackageIndex returns base-name → short-package-name for every
// interface, struct, enum, distinct type, and type alias declared in a
// project build (entry file + siblings + stdlib). The orphan check
// (`impl Iface for T` block requires Iface or T to be declared in the
// current package) consumes this lookup to answer "which package owns
// this name?" in O(1).
//
// Inputs:
//
//   - files: per-file FileAnalysis keyed by module key. "" is the
//     entry file; non-empty keys are siblings or cross-package deps.
//   - entryPackageName: the entry package's short name, read from
//     nomi.toml's [package].name.
//   - crossPackageSegments: the set of first-segment short names that
//     identify cross-package dependencies (versus intra-package
//     siblings). Built by the caller from the project's package index.
//
// Package-name-from-key rule:
//
//	""                → entryPackageName
//	"std/strings"      → "std"   (first segment "std" — matches the
//	                             bucket stdlib-declared names land in)
//	"foo"             → "foo"   (if "foo" ∈ crossPackageSegments)
//	"sub/log"         → entryPackageName   (intra-package sibling; "sub"
//	                                       is not in crossPackageSegments)
//	"stringkit/pad"   → "stringkit"   ("stringkit" ∈ crossPackageSegments)
//
// Precedence on collision: project wins. If a name appears in both
// stdlib and a project file, the project file's package wins the
// mapping. Rationale: a user's local `pub struct Date` (allowed for
// any stdlib name NOT in the auto-prepended prelude — stdlib exports
// many non-prelude names like Date, Duration, App, calendar.Error,
// io.Error, the Iter-adapter structs, etc.) shadows the stdlib
// name in normal lookup. Mapping it to "std" would make
// `impl SomeIface for Date` in the user's package falsely look
// orphan (Iface foreign, type "from std"), when in fact the receiver
// is the user's own type. Project-wins keeps the orphan check aligned
// with the resolver's user-shadows-stdlib reality. (Prelude-exported
// names like Int / Display can't be redeclared at all — see
// analysis/builder.go's checkReservedTypeName — so for those the
// precedence is unobservable.) Note the interface-impl orphan path
// (detectOrphanImpls) tolerates this base-name shadowing only because
// every force-loaded stdlib interface today is prelude-reserved; a user
// shadowing both a non-prelude stdlib interface and type (e.g. `Literal` +
// `Date` while importing std/calendar) would hit the analogous false
// positive — known limitation, the fix is a multi-valued index if that
// combination ever becomes reachable.
//
// Symbol filtering: only declarations land in the index — imports
// (sym.Resolved != nil) and non-type kinds (functions, bindings,
// params, once, enum variants, etc.) are skipped. Opaque is a Symbol
// flag, not a kind, so opaque types reach the index via their
// underlying SymbolStruct/SymbolEnum/SymbolType kind.
//
// Pure: no mutation of inputs, no I/O. Safe to call from BuildProject.
func declaringPackageIndex(
	files map[string]*FileAnalysis,
	entryPackageName string,
	crossPackageSegments map[string]bool,
) map[string][]string {
	idx := make(map[string][]string)

	// "Project wins on collision" must be DETERMINISTIC. Stdlib FAs and
	// project FAs both arrive through `files`, so a single map-order pass
	// would let either side win at
	// random when a name is declared in both (e.g. a user `struct Task`
	// vs `std/tasks.Task`). Process stdlib keys FIRST, then project
	// keys, so a project declaration always overwrites a stdlib one.
	writeDecls := func(stdlib bool) {
		seen := map[*Scope]bool{}
		// In key order, so the package named last for a name two packages
		// declare (declaringPackageOf, the orphan-impl error) is the same
		// on every run.
		for _, key := range slices.Sorted(maps.Keys(files)) {
			fa := files[key]
			if fa == nil || fa.ModuleScope == nil {
				continue
			}
			if IsStdlibKey(key) != stdlib {
				continue
			}
			noteStdlibWork(key)
			pkg := packageNameFromKey(key, entryPackageName, crossPackageSegments)
			writeDeclsFromScope(fa.ModuleScope, pkg, idx, seen)
		}
	}
	writeDecls(true)  // stdlib first
	writeDecls(false) // project overwrites

	return idx
}

func writeDeclsFromScope(scope *Scope, pkg string, idx map[string][]string, seen map[*Scope]bool) {
	if scope == nil {
		return
	}
	if seen[scope] {
		return
	}
	seen[scope] = true
	for name, sym := range scope.Symbols {
		if sym == nil || sym.Resolved != nil {
			continue // imported, not declared here
		}
		if isDeclaringKind(sym.Kind) {
			idx[name] = appendDeclaringPackage(idx[name], pkg)
		}
	}
}

// isDeclaringKind reports whether a SymbolKind represents an
// interface or type declaration whose name can be the receiver type
// of an impl. Enum variants (lifted as siblings into ModuleScope)
// are excluded — they aren't impl receiver types.
func isDeclaringKind(k SymbolKind) bool {
	switch k {
	case SymbolInterface, SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias:
		return true
	}
	return false
}

// orphanRulePackage is the name the orphan rule gives the program's own
// package. A project's is its manifest's `[package].name`. A program with no
// `nomi.toml` is its project root's tree, as a script alone in a directory
// is its own project (spec §15 *Entry point*), so it is named by that directory: an
// absolute path, which no package short name and not "std" can equal. A
// program with no root on disk (a source handed in memory) is named
// fileModePackage. The package is never "", which the rule skips as
// unkeyable, so a file outside any project is held to the rule too.
func orphanRulePackage(manifestName string, proj *Project) string {
	if manifestName != "" {
		return manifestName
	}
	if proj != nil && proj.ProjectRoot != "" {
		return proj.ProjectRoot
	}
	return fileModePackage
}

// fileModePackage names the package of a program with neither a manifest nor
// a project root.
const fileModePackage = "<program>"

// packageNameFromKey maps a FileAnalysis module key to the short
// package name that owns the declarations in that file. See the
// declaringPackageIndex doc-comment for the full rule table.
//
// Stdlib keys (first segment "std", e.g. "std/strings") always map to
// the std package. Some analysis paths have a populated package index and
// some run from sparse fixtures/test-program harnesses; the stdlib's
// ownership cannot depend on crossPackageSegments being present.
func packageNameFromKey(key, entryPackageName string, crossPackageSegments map[string]bool) string {
	if key == "" {
		return entryPackageName
	}
	if IsStdlibKey(key) {
		return "std"
	}
	first := key
	if i := strings.IndexByte(key, '/'); i >= 0 {
		first = key[:i]
	}
	if crossPackageSegments[first] {
		return first
	}
	return entryPackageName
}

// appendDeclaringPackage records pkg as declaring a name, keeping the
// list free of duplicates and preserving insertion order.
//
// The index is multi-valued because one base name can genuinely be
// declared by two packages — a user's `Date` alongside std/calendar's.
// A single-valued index had to pick one, and the loser's impls then
// looked orphaned: `impl SomeIface for Date` in the user's own package
// read as "receiver belongs to std". Callers ask "is this name declared
// in package M?" rather than "which package declares it?".
//
// Order still matters for diagnostics: stdlib is written first and
// project packages append after, so declaringPackageOf reports the
// project package on a collision — the same name the previous
// project-wins overwrite produced.
func appendDeclaringPackage(pkgs []string, pkg string) []string {
	for _, existing := range pkgs {
		if existing == pkg {
			return pkgs
		}
	}
	return append(pkgs, pkg)
}

// declaredInPackage reports whether `name` is declared by `pkg`.
func declaredInPackage(idx map[string][]string, name, pkg string) bool {
	for _, candidate := range idx[name] {
		if candidate == pkg {
			return true
		}
	}
	return false
}

// declaringPackageOf names a package that declares `name`, preferring the
// last recorded (a project package over stdlib). Reports false when the
// name is not declared anywhere reachable.
func declaringPackageOf(idx map[string][]string, name string) (string, bool) {
	pkgs := idx[name]
	if len(pkgs) == 0 {
		return "", false
	}
	return pkgs[len(pkgs)-1], true
}
