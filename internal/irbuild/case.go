package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// The blocker walk over a pattern: naming the pattern kinds outside the
// subset, without the scrutinee's kind.

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

	case *ast.AsPattern:
		// Lowered wherever its pattern is (the name binds the value the
		// pattern matched), so no refusal of its own.
		g.probePattern(pat.Pattern)

	case *ast.OrPattern:
		// Lowered wherever its alternatives are (irorpattern.go), so no
		// refusal of its own.
		for _, alt := range pat.Alternatives {
			g.probePattern(alt)
		}

	case *ast.StructPattern:
		// Lowered in both spellings (irstructpattern.go — nominal and anonymous),
		// so no refusal of its own, for the reason the TuplePattern arm below
		// gives.
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
		switch pat.Kind {
		case ast.SeqVector:
			// Lowered (irvectorpattern.go), so no refusal of its own, for the
			// reason the TuplePattern arm above gives.
			for _, sub := range pat.Heads {
				g.probePattern(sub)
			}
			for _, sub := range pat.Tails {
				g.probePattern(sub)
			}

		case ast.SeqSet:
			// Lowered (irvectorpattern.go); its elements are expressions and
			// its rest a name, so there is nothing below it to probe.

		default:
			// Lowered in both spellings (anonymous in irlistpattern.go, and
			// type-prefixed for a variant or a list-distinct), so no refusal of
			// its own, for the reason the TuplePattern arm above gives.
			g.probeListPattern(pat)

		}
	case *ast.MapPattern:
		// Lowered in both spellings (anonymous in irmappattern.go, and
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
		// Lowered, so no refusal of its own, for the reason the TuplePattern arm
		// above gives.

	default:
		g.reject("case pattern", p.NodeType(), p)
	}
}

// probeListPattern names the blockers inside a list pattern — every head,
// the spread and every tail.
func (g *gen) probeListPattern(pat *ast.ListPattern) {
	for _, sub := range pat.Heads {
		g.probePattern(sub)
	}
	g.probePattern(pat.TailSpread)
	for _, sub := range pat.Tails {
		g.probePattern(sub)
	}
}

func (g *gen) probeStructFields(pat *ast.StructPattern) {
	for _, f := range pat.Fields {
		g.probePattern(f.Pattern)
	}
}
