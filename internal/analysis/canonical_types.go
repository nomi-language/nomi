package analysis

import "sync"

// installBoolOnce, installOrderingOnce, installAssertionFailureOnce,
// installCodepointOnce and installRangeOnce gate the process-wide writes to
// TypeBool, TypeOrdering, TypeAssertionFailure, TypeCodepoint and TypeRange. The
// first build that declares the std file supplies the answer for the process;
// the declarations never change between builds.
var (
	installBoolOnce             sync.Once
	installOrderingOnce         sync.Once
	installAssertionFailureOnce sync.Once
	installCodepointOnce        sync.Once
	installRangeOnce            sync.Once
)

// installCanonicalStdTypes points TypeBool, TypeOrdering, TypeAssertionFailure,
// TypeCodepoint and TypeRange at std's own declarations, using the shells
// BuildTypeShells just attached. buildProjectWithCache calls it after the
// shell sweep and BEFORE the BuildTypes sweep.
//
// The position is the fix for an order dependence. NewTypeRegistry registers
// `Bool` as TypeBool, and compilerKnownSynthType resolves a derive's
// `Ordering` to TypeOrdering, so both are read while BuildTypes resolves every
// std signature. These were installed by std.Load after BuildProjectWithCache
// returned, so the FIRST load in a process resolved every std file's `Bool`
// to the hand-written fallback in types.go, an EnumType with no Origin and no
// `embeds` payloads. Later loads saw std's declaration. internal/irbuild caches
// one stdlib lowering per process, so a process that lowered std before
// analyzing any program kept first-load types permanently: preludeArgKind
// could not project `Result<Bool>` in a std/json instance body, the derived
// `FromJson` impl for a struct with a Bool field failed to lower, and
// `Flag.from_json(...)` was refused. internal/irbuild's
// TestStdOrder_LoweringStdFirstMatchesAnalyzingFirst is the regression test.
//
// Shells are mutated in place by BuildTypes, so installing the pointer before
// its variants are filled in is sound: every reader shares the one pointer.
func installCanonicalStdTypes(cache map[string]*FileAnalysis) {
	lookup := func(key, name string) Type {
		fa := cache[key]
		if fa == nil || fa.ModuleScope == nil {
			return nil
		}
		sym := fa.ModuleScope.Lookup(name)
		if sym == nil {
			return nil
		}
		return sym.Type
	}
	if et, ok := lookup("std/bool", "Bool").(*EnumType); ok {
		installBoolOnce.Do(func() { TypeBool = et })
	}
	if et, ok := lookup("std/comparable", "Ordering").(*EnumType); ok {
		installOrderingOnce.Do(func() { TypeOrdering = et })
	}
	if st, ok := lookup("std/assertions", "AssertionFailure").(*StructType); ok {
		installAssertionFailureOnce.Do(func() { TypeAssertionFailure = st })
	}
	if dt, ok := lookup("std/codepoints", "Codepoint").(*DistinctType); ok {
		installCodepointOnce.Do(func() { TypeCodepoint = dt })
	}
	if st, ok := lookup("std/ranges", "Range").(*StructType); ok {
		installRangeOnce.Do(func() { TypeRange = st })
	}
}
