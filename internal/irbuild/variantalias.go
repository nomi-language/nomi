package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
)

// A selectively imported enum variant has TWO names, and every resolver in this
// package that reads only one of them is wrong for the alias spelling.
//
//	import std/json.Json.{String as JStr}
//	JStr("Ada")
//
// # What the front end records
//
// `fa.References` records the reference at `JStr`'s own position as
//
//	Symbol{Name: "JStr", Node: *ast.ImportStmt, Resolved: Symbol{Name: "String", Node: *ast.EnumDef}}
//
// so the LOCAL spelling is on the outer symbol and the DECLARATION'S OWN name
// is on its resolution. The two other shapes fall out of the same rule rather
// than needing arms of their own, which is why this is one function:
//
//	Some(7)      unaliased selective import  Name "Some", Resolved.Name "Some"
//	Json.Obj(m)  qualified, no import symbol Name "Obj",  Resolved nil
//
// # Why this exists rather than three re-derivations
//
// It had three: `preludeCall`, `preludeBare` and `stdEnumVariantAt` each
// decided for themselves what variant a reference named, and TWO of them were
// wrong in a way an alias exposes. `stdEnumVariantAt` followed `Resolved` FIRST
// and then compared the canonical name against the caller's local spelling —
// vacuously right when there is no alias and wrong when there is, which is a
// guard with a green witness for a rule it did not implement. The prelude pair
// matched on the local spelling and then looked the local spelling up in the
// spec's variant list, which cannot contain it.
//
// One implementation of an observable question, reached by every path, is the
// same rule dotvariant.go states for construction. Two answers to "what variant
// is this" is a divergence no test keeps in step, and the transposed alias in
// testdata/variant_alias.nomi is what it costs: `import Json.{Int as Arr}`
// makes the local spelling name a DIFFERENT declared variant, so resolving by
// spelling builds the wrong tag over the wrong slot. That compiles.
//
// # What this deliberately does NOT do
//
// It is keyed on a POSITION, so it inherits `fa.References`' coverage exactly —
// including analysis/builder.go:1665's deliberate exclusion of derive-synthesis
// band positions, which exists because synthesized type references are not
// navigable source and admitting them would make rename and find-references
// emit bogus results. A caller needing an answer for a synthesized position
// needs a non-positional route; this is the wrong door and must not be widened
// into one.
//
// It also does not decide whether the enum is REPRESENTABLE. That is the
// caller's own anchor check — a prelude spec for prelude.go, a std enum spec
// for stdenum.go — and keeping it there is what stops this from becoming a
// name table that answers about a user's own same-named enum.

// variantRefAt is the analyzer's reference at a position that names an enum
// variant spelled `local`, together with the declaration's own name for it.
//
// The match is on the OUTER symbol, because `local` is how the programmer
// spelled it HERE; the canonical name then comes from the resolution when there
// is one. A caller that wants the declaration reads `ref.Resolved`, because the
// CALL-SITE symbol is where the analyzer records an instantiated signature and
// the resolved one is the shared declaration.
func variantRefAt(fa *analysis.FileAnalysis, local string, line, col int) (ref *analysis.Symbol, canonical string, ok bool) {
	if fa == nil {
		return nil, "", false
	}
	ref, found := fa.References[analysis.Pos{Line: line, Col: col}]
	if !found || ref == nil || ref.Name != local {
		return nil, "", false
	}
	if ref.Resolved != nil {
		return ref, ref.Resolved.Name, true
	}
	return ref, ref.Name, true
}
