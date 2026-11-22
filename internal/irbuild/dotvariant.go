package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `.Exponential{max_restarts: 2}`, `.Off`, `.Circle(1.0)` — the leading-dot
// variant shorthand in EXPRESSION position.
//
// # The dot means "the qualified spelling", so that is what is lowered
//
// The analyzer resolves the dot against the EXPECTED TYPE at the position and
// records the answer on the node: `ResolvedEnum` (internal/ast/ast.go's
// DotVariant and DotVariantType). qualifiedDot reads that field, synthesizes
// the qualified node the programmer could have written, and its caller hands
// it to the existing resolver. So `.Off` in a `Backoff` position becomes
// exactly `Backoff.Off`, and `variantBare` and `variantCall` stay the one
// implementation of variant construction, with their payload checks and their
// arity checks.
//
// Rewriting rather than reimplementing is not merely tidier. A second
// construction path is where the zero-sized-payload rule and the
// `embeds`-unwrap rule would drift, and both of those are wrong-answer bugs.
//
// # IDENTITY: the enum the checker typed the shorthand as
//
// The written node's checked type names the enum by its declaration, and
// noteVariantOwner files it as the owner of the node qualifiedDot writes
// (`dotOwners`). A monomorphic enum's def is the one `project` resolves
// that type to whatever the file imports (dotEnumDef), so the shorthand
// works for an imported enum — `policy.{Backoff}` in
// tests/07-structs-and-enums — and for one the file never names. A generic
// enum's template is the one the type names (genericVariant,
// genericTemplateOf).
//
// An empty `ResolvedEnum` means analysis did not fire or the resolution FAILED,
// in which case the analyzer already reported a diagnostic and the program does
// not run. It refuses under `dot variant`.
//
// # THE SYNTHESIZED POSITION CARRIES NO REFERENCE, so a POSITIONAL resolver
// declines for the shorthand while answering for the spelling it abbreviates
//
// The rewritten node's Line/Col are the DOT's, and a resolver keyed on
// `fa.References` at that position finds nothing: the analyzer records a
// DotVariant's resolution on the NODE (`ResolvedEnum`) and not in the
// reference map. Resolved that way, `c: Outcome<Unit> = Outcome.Cancelled`
// would lower while `c: Outcome<Unit> = .Cancelled` would not, though the two
// are one value.
//
// So `preludeBareValue` reads the shorthand's checked type before the
// rewrite: the enum instance the checker typed the dot at, by its
// declaration's identity, whose spec names the variant. A module declaring
// its own `Outcome` types the dot at its own enum, which is no prelude
// instance. The same holds for every prelude spec, not only Outcome:
// `m: Maybe<Int> = .None` takes this route too.
//
// WHAT THIS ROUTE CANNOT DO, and it is UNREACHABLE rather than a hole:
// canonicalize an ALIAS. `t.Name` is the LOCAL spelling and the
// declaration-name mapping lives only in the reference map, so
// `import std::maybe.Maybe.{None as Nothing}` followed by `.Nothing`
// would find no variant on the spec and decline. The analyzer refuses that
// program first — "no variant 'Nothing' on enum Maybe" — because the dot
// shorthand resolves against the DECLARATION's variant names and an import
// alias does not rename a variant for it. The unqualified form `Nothing` (no
// dot) is a different node and lowers. Both halves are asserted, so an
// analyzer that admitted `.Nothing` fails here rather than silently guessing.
//
// # What is NOT claimed
//
// `.Arr[1, 2, 3]` and `.Obj{"k" => v}` — the LIST and MAP literal-attach forms
// — are not routed here. Their TypeName slot carries a DotVariantType too, but
// the value under it is a list or a map literal, and those refuse for their own
// reasons in their own files. Routing them through here would replace one
// honest refusal with another that names the wrong thing.

// qualifiedDot is the `Enum.Variant` field access the shorthand abbreviates,
// carrying the DOT's own position so every refusal lands where the programmer
// wrote it rather than at a node nobody typed.
func (g *gen) qualifiedDot(t *ast.DotVariant) *ast.FieldAccess {
	fa := &ast.FieldAccess{
		Object: &ast.TypeIdent{Name: t.ResolvedEnum, Line: t.Line, Col: t.Col},
		Field:  &ast.Ident{Name: t.Name, Line: t.Line, Col: t.Col},
		Line:   t.Line,
		Col:    t.Col,
	}
	g.noteVariantOwner(fa, t)
	return fa
}

// noteVariantOwner files the enum the checker typed at, a `.Variant`
// shorthand or a literal-attach form, as the owner of fa, the
// `Enum.Variant` the builder writes for it (dotOwners).
func (g *gen) noteVariantOwner(fa *ast.FieldAccess, at ast.Node) {
	et := g.dotEnumType(at)
	if et == nil {
		return
	}
	if g.dotOwners == nil {
		g.dotOwners = map[*ast.FieldAccess]*analysis.EnumType{}
	}
	g.dotOwners[fa] = et
}

// dotEnumType is the enum the checker resolved a `.Variant` (or
// `.Variant{...}`, whose node `at` is the struct literal) to: the checked
// type at `at`, or for a payload variant, whose checked type is the
// constructor's function type, its result. Nil when it is no enum.
func (g *gen) dotEnumType(at ast.Node) *analysis.EnumType {
	ty := g.checkedExprType(at)
	if ft, isFunc := irResolvedType(ty).(*analysis.FuncType); isFunc {
		ty = ft.Return
	}
	et, isEnum := resolvedEnumType(ty)
	if !isEnum {
		return nil
	}
	return et
}

// dotEnumDef is the def of et, the monomorphic enum the checker resolved a
// `.Variant` to (dotEnumType), when it declares variant, or nil.
//
// # THE NAME IS NOT IN THIS FILE'S SCOPE WHEN THE EXPECTED TYPE COMES FROM A CALLEE
//
// `Iter.sort(xs, .Descending)` resolves against std/comparable's `Direction`,
// which the calling file never imports and may not import (an import nothing
// names is an error). `ResolvedEnum` is the bare `Direction`, so a lookup in
// this file's tables finds nothing, or finds the file's OWN `Direction` if it
// declares one: a wrong answer if that enum also has a `Descending`. The checked
// type carries the declaration's (Origin, Name), which `project` resolves to
// the right def whatever the file imports, as it does for any inferred
// position.
//
// A generic enum's instance comes from the call's checked signature or the
// expected kind, and its template from et (genericVariant).
func (g *gen) dotEnumDef(et *analysis.EnumType, variant string) *typeDef {
	if et == nil || len(et.TypeParamDefs) > 0 {
		return nil
	}
	k := g.project(et)
	if k.tag != tagNamed || k.def == nil || k.def.variant(variant) == nil {
		return nil
	}
	return k.def
}
