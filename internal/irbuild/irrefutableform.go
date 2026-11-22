package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Whether a pattern can fail is the checker's (analysis package doc,
// "Patterns' types"): it recorded, for every pattern it checked against a
// known type, whether the pattern matches every value of that type
// (FileAnalysis.IrrefutablePatterns), and for every binding with an `else`
// what the `else` may fall back to (BindingFallbacks). The builder reads
// those records where it would leave a test out: an arm whose pattern
// always matches ends the arms after it (caseArmTest), a pattern that
// always matches and binds nothing lowers as `_` (patternTest), a variant
// pattern that always matches, a one-variant enum's, tests no tag
// (variantTagTest), `assert P = v` adds no assertion and an `or` tests no
// alternative after one that always matches. It answers none of it from a
// pattern's node or its subject's kind.
//
// A pattern the builder makes in place of a checked one carries that
// pattern's record (carryPattern), as carryCall carries a call's: a
// literal-attach pattern read as its variant pattern
// (irAttachedVariantPattern), a typed list or map pattern without its
// head, a binding's struct-variant fields read as the record they match.

// irrefutableClass is where the answer to whether a pattern always
// matches came from.
type irrefutableClass string

const (
	// irrefutableFromRecord: the checker's record of the pattern.
	irrefutableFromRecord irrefutableClass = "from the record"
	// irrefutableCarried: the record of the checked pattern a pattern the
	// builder made stands for (carryPattern).
	irrefutableCarried irrefutableClass = "carried from the checked pattern"
	// irrefutableUnrecorded: any other pattern with no record, which no
	// checked program reaches. Only `_` and a name always match there.
	irrefutableUnrecorded irrefutableClass = "no record: a pattern the builder made"
)

// irrefutableClasses is every irrefutableClass, with whether it is held
// to a maximum (maxIrrefutableSites): each class that is not a record's.
// A class it does not list fails the lowering.
var irrefutableClasses = map[irrefutableClass]bool{
	irrefutableFromRecord: false,
	irrefutableCarried:    false,
	irrefutableUnrecorded: true,
}

// patternAlwaysMatches reports whether p matches every value of its
// subject: the checker's record of p, or of the checked pattern p stands
// for. A pattern with neither always matches only when it is `_` or a
// name, which match every value at any type; each such answer is counted
// (noteIrrefutable).
func (g *gen) patternAlwaysMatches(p ast.Node) bool {
	if always, recorded := g.srcFA().PatternIrrefutable(p); recorded {
		g.noteIrrefutable(p, irrefutableFromRecord)
		return always
	}
	if always, carried := g.carriedPatterns[p]; carried {
		g.noteIrrefutable(p, irrefutableCarried)
		return always
	}
	g.noteIrrefutable(p, irrefutableUnrecorded)
	switch p.(type) {
	case *ast.WildcardPattern, *ast.IdentPattern:
		return true
	}
	return false
}

// carryPattern gives made, a pattern the builder made in place of a checked
// one, the answer to whether it always matches: the record of the checked
// pattern it stands for, from (patternAlwaysMatches). It answers made.
func (g *gen) carryPattern(made, from ast.Node) ast.Node {
	return g.carryPatternAnswer(made, g.patternAlwaysMatches(from))
}

// carryPatternAnswer gives made the answer always, which a record
// other than a pattern's says: a binding's fallback record says its
// payload's pattern always matches.
func (g *gen) carryPatternAnswer(made ast.Node, always bool) ast.Node {
	if g.carriedPatterns == nil {
		g.carriedPatterns = map[ast.Node]bool{}
	}
	g.carriedPatterns[made] = always
	return made
}

// noteIrrefutable counts, under substVerify, the site of p in the
// irrefutable pattern census by where the answer for it came from. A
// class irrefutableClasses does not list fails the lowering.
func (g *gen) noteIrrefutable(p ast.Node, class irrefutableClass) {
	if !substVerify || isNilNode(p) || g.srcFA() == nil {
		return
	}
	line, col := nodePos(p)
	if _, listed := irrefutableClasses[class]; !listed {
		g.calleeMismatch(line, col, fmt.Sprintf("whether a pattern always matches is answered %q, which no class lists", class))
		return
	}
	irrefutableCensus.note(g, string(class), line, col)
}
