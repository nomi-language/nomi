package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
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

// importedVariantAt resolves a variant named bare through a drill-through
// import (`import shapes.Holder.{Of}`, `import std/json.Json.{String as
// JStr}`) at the node `at` that builds or names it, to the def and the
// variant. A std enum resolves through stdEnumVariantAt. Any other enum
// resolves through the type the checker gave `at`, so a generic enum's
// variant gets the instance the checker solved (`Of{value: 1}` is a
// `Holder<Int>`), as the qualified spelling `shapes.Holder.Of{value: 1}`
// does. That fallback is for user enums only: a std enum the std paths
// decline declines here too. A same-file variant is not imported (its reference has no
// resolution) and declines, as the checker rejects it bare.
func (g *gen) importedVariantAt(at ast.Node, local string, line, col int) (*typeDef, *variantDef, bool) {
	if d, v, ok := g.stdEnumVariantAt(local, line, col); ok {
		return d, v, true
	}
	ref, canonical, ok := variantRefAt(g.srcFA(), local, line, col)
	if !ok || ref.Resolved == nil {
		return nil, nil, false
	}
	decl := ref.Resolved
	for decl.Resolved != nil {
		decl = decl.Resolved
	}
	if decl.Kind != analysis.SymbolEnumVariant {
		return nil, nil, false
	}
	ty := g.checkedExprType(at)
	if ft, isFunc := irResolvedType(ty).(*analysis.FuncType); isFunc {
		ty = ft.Return
	}
	et, isEnum := resolvedEnumType(ty)
	if !isEnum {
		return nil, nil, false
	}
	// The checked type must be the enum the import names, not merely one
	// with a variant of that name.
	declared := analysis.Type(decl.Type)
	if ft, isFunc := declared.(*analysis.FuncType); isFunc {
		declared = ft.Return
	}
	if de, ok := declared.(*analysis.EnumType); !ok || de.Origin != et.Origin || de.Name != et.Name {
		return nil, nil, false
	}
	// A std enum's variant (the prelude's `Some` among them) is answered by
	// stdEnumVariantAt or by the prelude's own paths, from the signature the
	// checker recorded at the call. When those decline, its metadata is
	// missing, and the checked type of the node must not stand in for it.
	if strings.HasPrefix(et.Origin, "std/") {
		return nil, nil, false
	}
	// A type argument nothing determines (`Empty` of a `Holder<T>`) is
	// filled by its position, as a generic literal's is.
	k := g.project(irFillHolesByPosition(et, false))
	if k.tag != tagNamed || k.def == nil {
		return nil, nil, false
	}
	v := k.def.variant(canonical)
	if v == nil {
		return nil, nil, false
	}
	return k.def, v, true
}
