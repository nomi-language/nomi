package analysis

import "strings"

// declaringModuleIndex returns base-name → short-module-name for every
// interface, struct, enum, distinct type, and type alias declared in a
// project build (entry file + siblings + stdlib). The orphan check
// (`impl Iface for T` block requires Iface or T to be declared in the
// current module) consumes this lookup to answer "which module owns
// this name?" in O(1).
//
// Inputs:
//
//   - files: per-file FileAnalysis keyed by module key. "" is the
//     entry file; non-empty keys are siblings or cross-module deps.
//   - entryModuleName: the entry module's short name, read from
//     nomi.toml's [module].name.
//   - stdlibInterfaces, stdlibTypes: production passes nil but the seeding path remains
//     for unit-test exercise (the impl_orphan_test.go hand-rolled
//     callers seed entries here rather than building a project's
//     worth of files). Now that the stdlib-globals retirement is
//     complete (production stdlib FAs flow through filesByKey
//     normally), these params are pure test-shim. Both may be nil —
//     no panic, no synthesized entries.
//   - crossModuleSegments: the set of first-segment short names that
//     identify cross-module dependencies (versus intra-module
//     siblings). Built by the caller from the project's module index.
//
// Module-name-from-key rule:
//
//	""                → entryModuleName
//	"std/strings"      → "std"   (first segment "std" — matches the
//	                             bucket stdlib-declared names land in)
//	"foo"             → "foo"   (if "foo" ∈ crossModuleSegments)
//	"sub/log"         → entryModuleName   (intra-module sibling; "sub"
//	                                       is not in crossModuleSegments)
//	"stringkit/pad"   → "stringkit"   ("stringkit" ∈ crossModuleSegments)
//
// Precedence on collision: project wins. If a name appears in both
// stdlib and a project file, the project file's module wins the
// mapping. Rationale: a user's local `pub struct Date` (allowed for
// any stdlib name NOT in the auto-prepended prelude — stdlib exports
// many non-prelude names like Date, Duration, App, calendar.Error,
// io.Error, the Iter-adapter structs, etc.) shadows the stdlib
// name in normal lookup. Mapping it to "std" would make
// `impl SomeIface for Date` in the user's module falsely look
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
func declaringModuleIndex(
	files map[string]*FileAnalysis,
	entryModuleName string,
	stdlibInterfaces, stdlibTypes map[string]bool,
	crossModuleSegments map[string]bool,
) map[string][]string {
	idx := make(map[string][]string)

	// Stdlib first; project files overwrite below to enforce
	// "project wins on collision".
	for name := range stdlibInterfaces {
		idx[name] = appendDeclaringModule(idx[name], "std")
	}
	for name := range stdlibTypes {
		idx[name] = appendDeclaringModule(idx[name], "std")
	}

	// "Project wins on collision" must be DETERMINISTIC. Stdlib FAs and
	// project FAs both arrive through `files` (post-stdlib-as-package
	// cutover), so a single map-order pass would let either side win at
	// random when a name is declared in both (e.g. a user `struct Task`
	// vs `std/tasks.Task`). Process stdlib keys FIRST, then project
	// keys, so a project declaration always overwrites a stdlib one.
	writeDecls := func(stdlib bool) {
		seen := map[*Scope]bool{}
		for key, fa := range files {
			if fa == nil || fa.ModuleScope == nil {
				continue
			}
			if IsStdlibKey(key) != stdlib {
				continue
			}
			mod := moduleNameFromKey(key, entryModuleName, crossModuleSegments)
			writeDeclsFromScope(fa.ModuleScope, mod, idx, seen)
		}
	}
	writeDecls(true)  // stdlib first
	writeDecls(false) // project overwrites

	return idx
}

func writeDeclsFromScope(scope *Scope, mod string, idx map[string][]string, seen map[*Scope]bool) {
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
			idx[name] = appendDeclaringModule(idx[name], mod)
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

// moduleNameFromKey maps a FileAnalysis module key to the short
// module name that owns the declarations in that file. See the
// declaringModuleIndex doc-comment for the full rule table.
//
// Stdlib keys (first segment "std", e.g. "std/strings") always map to
// the std package. Some analysis paths have a populated module index and
// some run from sparse fixtures/test-program harnesses; the stdlib's
// ownership cannot depend on crossModuleSegments being present.
func moduleNameFromKey(key, entryModuleName string, crossModuleSegments map[string]bool) string {
	if key == "" {
		return entryModuleName
	}
	if IsStdlibKey(key) {
		return "std"
	}
	first := key
	if i := strings.IndexByte(key, '/'); i >= 0 {
		first = key[:i]
	}
	if crossModuleSegments[first] {
		return first
	}
	return entryModuleName
}

// appendDeclaringModule records mod as declaring a name, keeping the
// list free of duplicates and preserving insertion order.
//
// The index is multi-valued because one base name can genuinely be
// declared by two modules — a user's `Date` alongside std/calendar's.
// A single-valued index had to pick one, and the loser's impls then
// looked orphaned: `impl SomeIface for Date` in the user's own module
// read as "receiver belongs to std". Callers ask "is this name declared
// in module M?" rather than "which module declares it?".
//
// Order still matters for diagnostics: stdlib is written first and
// project modules append after, so declaringModuleOf reports the
// project module on a collision — the same name the previous
// project-wins overwrite produced.
func appendDeclaringModule(mods []string, mod string) []string {
	for _, existing := range mods {
		if existing == mod {
			return mods
		}
	}
	return append(mods, mod)
}

// declaredInModule reports whether `name` is declared by `mod`.
func declaredInModule(idx map[string][]string, name, mod string) bool {
	for _, candidate := range idx[name] {
		if candidate == mod {
			return true
		}
	}
	return false
}

// declaringModuleOf names a module that declares `name`, preferring the
// last recorded (a project module over stdlib). Reports false when the
// name is not declared anywhere reachable.
func declaringModuleOf(idx map[string][]string, name string) (string, bool) {
	mods := idx[name]
	if len(mods) == 0 {
		return "", false
	}
	return mods[len(mods)-1], true
}
