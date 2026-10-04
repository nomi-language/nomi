package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// `.Exponential{max_restarts: 2}`, `.Off`, `.Circle(1.0)` — the leading-dot
// variant shorthand in EXPRESSION position.
//
// # The dot means "the qualified spelling", so that is what is emitted
//
// The analyzer resolves the dot against the EXPECTED TYPE at the position and
// records the answer on the node: `ResolvedEnum` (ast.go's DotVariant and
// DotVariantType). Every arm below reads that field, synthesizes the qualified
// node the programmer could have written, and hands it to the existing
// resolver. So `.Off` in a `Backoff` position becomes exactly `Backoff.Off`,
// and `bareVariant`, `variantLit` and `variantCall` stay the one implementation
// of variant construction — with their payload checks, their arity checks,
// their `embeds`-of-a-wrapping-distinct arm and their refusal keys.
//
// Rewriting rather than reimplementing is not merely tidier. A second
// construction path is where the zero-sized-payload rule and the
// `embeds`-unwrap rule would drift, and both of those are wrong-answer bugs
// rather than compile errors.
//
// # IDENTITY: this resolves a NAME
//
// `ResolvedEnum` is the checker's `EnumType.Name` — a bare declared name, never
// a qualified path (analysis/checker.go, six assignment sites, all
// `= et.Name`) — resolved in the scope of the site, so a name lookup is the
// specification here rather than a shortcut around one. `g.namedType` does
// that lookup and reaches a sibling file's declaration through a
// mirror, which is what makes the shorthand work for an imported enum —
// `policy.{Backoff}` in tests/07-structs-and-enums.
//
// An empty `ResolvedEnum` means analysis did not fire or the resolution FAILED,
// in which case the analyzer already reported a diagnostic and the program does
// not run. It refuses under `dot variant`.
//
// # THE SYNTHESIZED POSITION CARRIES NO REFERENCE, so a POSITIONAL resolver
// declines for the shorthand while answering for the spelling it abbreviates
//
// The rewrite above hands `bareVariant` a node whose Line/Col are the DOT's, and
// `bareVariant`'s first rung — `preludeBare` — is keyed on `fa.References` at
// that position. The analyzer records a DotVariant's resolution on the NODE
// (`ResolvedEnum`) and not in the reference map, so that rung declines and the
// chain falls through to `lookupVariant`, which reports
// `type-qualified reference`: `c: Outcome<Unit> = Outcome.Cancelled` lowers
// while `c: Outcome<Unit> = .Cancelled` would not, though `rt.Outcome[T]`
// represents both.
//
// So dotVariantBare asks by NAME before rewriting. variantalias.go's header
// says a positional resolver "inherits fa.References' coverage exactly" and
// that "a caller needing an answer for a synthesized position needs a
// non-positional route; this is the wrong door and must not be widened into
// one" — this is that route, and `preludeByName` is the same oracle
// synthderive.go, iter.go, lists.go and maps.go already use for exactly the
// no-position case. It is shadow-safe by construction: preludeAnchorsOf takes
// the declaration `ModuleScope` resolves the name to and compares it against
// the spec, so a module declaring its own `Outcome` gets no anchor.
//
// The same holds for every prelude spec, not only Outcome: `m: Maybe<Int> =
// .None` takes this route too.
//
// WHAT THE NAME ROUTE CANNOT DO, and it is UNREACHABLE rather than a hole:
// canonicalize an ALIAS. `t.Name` is the LOCAL spelling and the
// declaration-name mapping lives only in the reference map this route exists to
// avoid, so `import std/maybe.Maybe.{None as Nothing}` followed by `.Nothing`
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
	return &ast.FieldAccess{
		Object: &ast.TypeIdent{Name: g.dotEnumName(t.ResolvedEnum), Line: t.Line, Col: t.Col},
		Field:  &ast.Ident{Name: t.Name, Line: t.Line, Col: t.Col},
		Line:   t.Line,
		Col:    t.Col,
	}
}

// dotEnumName is the spelling `namedType` can resolve the shorthand's enum by.
//
// `ResolvedEnum` is a BARE declared name and the file's own tables bind only
// what the file names. A SELECTIVE import (`import shapes.{Shape}`) puts
// `Shape` there and the bare name resolves; a WHOLE-FILE import (`import
// shapes`) binds only the qualifier, so for `b: shapes.Shape = .Ring{r: 7}`
// the bare `Shape` does not resolve and the module-qualified spelling does.
//
// Returns the input unchanged when nothing better is found, so a refusal keeps
// its operand and its position.
func (g *gen) dotEnumName(resolved string) string {
	if resolved == "" {
		return resolved
	}
	if _, known := g.namedType(resolved); known {
		return resolved
	}
	if spelling, qualified := g.moduleQualifiedBare(resolved); qualified {
		return spelling
	}
	return resolved
}
