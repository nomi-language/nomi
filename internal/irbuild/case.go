package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Pattern helpers shared by the destructuring prologue and the blocker walk:
// reading a variant's payload, holding a value the prologue reads twice, a
// pattern's type head, and naming the pattern kinds outside the subset.

// payloadValue is the value a variant's payload is read from.
//
// Three rules meet here, and each of them is a wrong answer if it is missed:
//
//   - ZERO-SIZED MATERIALIZATION. A payload that occupies no storage gets no
//     slot at all (see assignSlots), so there is nothing to read: the value
//     has one inhabitant and no instruction computes it.
//   - `embeds` OF A WRAPPING DISTINCT unwraps. The slot holds the distinct, but
//     the pattern must see the INNER value: the checker's contract is that the
//     variant's payload type is the wrapped type (spec §7), so
//     `Identifier.UserId(name)` binds a String.
//   - A BOXED payload is behind a pointer, because its type can reach its own
//     container.
//
// The last two are `ir.ProjPayload` and then `ir.ProjInner`, chained, which is
// what the unwrap IS.
func (g *gen) payloadValue(at ast.Node, d *typeDef, v *variantDef, i int, subj expr) expr {
	p := &v.payloads[i]
	if p.slot < 0 {
		return expr{k: p.k}
	}
	out := g.irProjPayload(at, subj, d, v, i, p.k)
	if payloadUnwrapsDistinct(v) {
		out = g.irProjInner(at, out, v.embeds, v.embeds.inner)
	}
	return out
}

// payloadUnwrapsDistinct reports whether reading this variant's payload has to
// see through a wrapping distinct.
func payloadUnwrapsDistinct(v *variantDef) bool {
	// kindInvalid: marker — distinguishes a distinct WITH an inner from a marker; neither declines.
	return v.kind == "embedded" && v.embeds.isDistinct && v.embeds.inner != kindInvalid
}

// hold copies a value the prologue reads more than once into a temporary of
// its own, unless it is already held in one: a parameter, a binding or an
// earlier copy (irHeldValue). `destructureTuple` holds a NESTED tuple's
// source, which is a projection.
//
// A COPY AND NOT A BIND: the temporary names no Nomi declaration, and
// `ir.Bind`'s symbol is a declaration identity.
func (g *gen) hold(e expr) expr {
	// kindInvalid: mechanical — nothing to hold; the refusal is recorded where it happened.
	if e.k == kindInvalid || (e.t != ir.NoTemp && irHeldValue(g.irPro.fn, e.t, nil)) {
		return e
	}
	c := g.irCopyNode(g.irPro.at, e.k, g.irHold(e))
	return expr{t: c.Dst(), k: e.k}
}

// patternHead splits a pattern's type head into its qualifier and its member.
//
// Three spellings reach one answer: `Variant` (bare, resolved against the scrutinee's type),
// `Enum.Variant` (qualified) and `.Variant` (dotted, the qualifier inferred).
// A head this builder cannot read at all reports ok=false rather than guessing
// a member name out of a TypeString.
func patternHead(te ast.TypeExpr) (owner, member string, ok bool) {
	switch t := te.(type) {
	case *ast.SimpleType:
		return "", t.Name, true
	case *ast.QualifiedType:
		if t.Member == nil {
			return "", "", false
		}
		return t.Module, t.Member.TypeString(), true
	case *ast.DotVariantType:
		return "", t.Name, true
	}
	return "", "", false
}

// probePattern names every pattern kind outside the subset, without needing the
// scrutinee's kind.
//
// It is what keeps a blocker set complete when the `case` itself could not be
// lowered for an unrelated reason — a scrutinee whose type this builder cannot
// represent, or a `case` inside a refused lambda. Reporting nothing there would
// truncate the blocker set: an unsupported pattern inside it would go
// unreported.
func (g *gen) probePattern(p ast.Node) {
	if isNilNode(p) {
		return
	}
	switch pat := p.(type) {
	case *ast.WildcardPattern, *ast.IdentPattern, *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CodepointLit:

	case *ast.EnumPattern:
		g.probePattern(pat.Payload)

	case *ast.StructPattern:
		// Lowered in both spellings (structArm — nominal and anonymous), so no
		// refusal of its own, for the reason the TuplePattern arm below gives.
		g.probeStructFields(pat)

	case *ast.TuplePattern:
		// Lowered (collections.go), so no refusal of its own: reaching
		// probePattern means the SCRUTINEE could not be represented, and
		// naming the pattern here would put a supported construct in the
		// blocker set.
		for _, sub := range pat.Patterns {
			g.probePattern(sub)
		}

	case *ast.ListPattern:
		// Lowered in both spellings (collections.go's listArm — anonymous,
		// and type-prefixed for a variant or a list-distinct, through
		// attach.go), so no refusal of its own, for the reason the TuplePattern
		// arm above gives.
		g.probeListPattern(pat)

	case *ast.MapPattern:
		// Lowered in both spellings (maps.go's mapArm — anonymous, and
		// type-prefixed for a variant or a map-distinct), so no refusal of its
		// own, for the reason the TuplePattern arm above gives.
		//
		// A key is an EXPRESSION and a value is a PATTERN, so both halves are
		// walked and by the walk each half needs.
		for _, entry := range pat.Entries {
			g.probePattern(entry.Pattern)
		}

	case *ast.Binary:
		// `"prefix" + rest` — the string-prefix pattern. It is a Binary node in
		// pattern position.
		g.reject("string prefix pattern", pat.Op, pat)

	case *ast.DecimalLit:
		// Lowered (decimal.go's decimalArm), so no refusal of its own, for the
		// reason the TuplePattern arm above gives.

	default:
		g.reject("case pattern", p.NodeType(), p)
	}
}

// probeListPattern names the blockers inside a list pattern — every head and
// the spread tail. Split out because listArm needs it too: when the type
// prefix fails to resolve, the body has not been walked and the file's blocker
// set would be short by however much the body holds.
func (g *gen) probeListPattern(pat *ast.ListPattern) {
	for _, sub := range pat.Heads {
		g.probePattern(sub)
	}
	g.probePattern(pat.TailSpread)
}

func (g *gen) probeStructFields(pat *ast.StructPattern) {
	for _, f := range pat.Fields {
		g.probePattern(f.Pattern)
	}
}
