package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// patternTest tests pat against value, of kind k, from the block bl.b is
// at, and binds pat's names. Every position a pattern takes reaches it: a
// `case` arm, a binding with or without `else`, a pattern `if`, `is`,
// `assert P = v`, and each sub-pattern of another pattern.
//
// A failed test branches to next, which is nil where the checker records
// the pattern as matching every value (bindPattern). On success the builder is left on the
// block the last test passed into, still open, with the names bound
// there, so the position goes on from it with no jump: an arm's guard and
// body, the next field, the statements after a binding. A pattern with
// nothing to test (a name, `_`, a tuple of names, a pattern the checker
// records as always matching) binds in the block it starts in.
//
// The node type chooses the test. The checker's records decide what the
// node cannot: the variant a head names (PatternVariants), whether a head
// names a subtype (PatternTypes), whether the pattern can fail at all
// (IrrefutablePatterns) and what a bracket pattern takes apart
// (ListPattern.Kind). The value's kind only shapes the IR the chosen test
// is built from: a field read off a struct or a record, a distinct's inner
// value read before its pattern is matched.
//
// A pattern it cannot test is the decline's position, unless a pattern
// inside it already is.
func (bl *irScalarBuilder) patternTest(pat ast.Node, value ir.Temp, k kind, next *ir.Block) bool {
	if !bl.patternTestNode(pat, value, k, next) {
		bl.g.dc.patternFailed(pat)
		return false
	}
	return true
}

func (bl *irScalarBuilder) patternTestNode(pat ast.Node, value ir.Temp, k kind, next *ir.Block) bool {
	switch p := pat.(type) {
	case *ast.WildcardPattern:
		return true
	case *ast.IdentPattern:
		bl.patternBinding(p, p.Name, value, k)
		return true
	case *ast.AsPattern:
		// `P as name`: P's test, then name bound on its success edge.
		if !bl.patternTest(p.Pattern, value, k, next) {
			return false
		}
		bl.patternBinding(p, p.Name, value, k)
		return true
	case *ast.OrPattern:
		return bl.orPatternTest(p, value, k, next)
	case *ast.CodepointLit:
		return bl.codepointCaseTest(p, value, k, next)
	case *ast.IntLit, *ast.StringLit, *ast.FloatLit, *ast.DecimalLit:
		return bl.literalTest(p, value, k, next)
	case *ast.Binary:
		return bl.strPrefixTest(p, value, k, next)
	case *ast.TuplePattern:
		return bl.tupleCaseTest(p, value, k, next)
	case *ast.MapPattern:
		if irDistinctOverKind(k, tagMap) {
			return bl.nominalCaseTest(p, value, k, next)
		}
		if p.TypeName != nil {
			return bl.enumCaseTest(p, value, k, next)
		}
		return bl.mapCaseTest(p, value, k, next)
	case *ast.EnumPattern, *ast.StructPattern:
		if bl.g.patternAlwaysMatches(p) && len(patternBinders(p)) == 0 {
			// `Unit`, a marker's name, `Point{x: _}`: it matches every value
			// and binds nothing, so it is `_`.
			return true
		}
		if ep, isEnum := p.(*ast.EnumPattern); isEnum && bl.g.patternRefines(ep) {
			// `Port(n)`: a subtype's value is its base's (spec §4
			// *Subtypes*), so the payload matches the value itself.
			if ep.Payload != nil {
				return bl.patternTest(ep.Payload, value, k, next)
			}
			if ep.Binding != "" {
				bl.patternBinding(ep, ep.Binding, value, k)
			}
			return true
		}
		if irNominalCaseKind(k) {
			return bl.nominalCaseTest(p, value, k, next)
		}
		return bl.enumCaseTest(p, value, k, next)
	case *ast.ListPattern:
		switch p.Kind {
		case ast.SeqVector:
			if irDistinctOverVector(k) {
				return bl.nominalCaseTest(p, value, k, next)
			}
			if p.TypeName != nil {
				return bl.enumCaseTest(p, value, k, next)
			}
			return bl.vectorCaseTest(p, value, k, next)
		case ast.SeqSet:
			if irDistinctOverSet(k) {
				return bl.nominalCaseTest(p, value, k, next)
			}
			if p.TypeName != nil {
				return bl.enumCaseTest(p, value, k, next)
			}
			return bl.setCaseTest(p, value, k, next)
		default:
			if irDistinctOverKind(k, tagList) {
				return bl.nominalCaseTest(p, value, k, next)
			}
			if p.TypeName != nil {
				return bl.enumCaseTest(p, value, k, next)
			}
			return bl.listCaseTest(p, value, k, next)
		}
	}
	return false
}

// passOrFail ends the current block in a branch on cond: true goes on in a
// fresh block, which the builder is left at, and false to next. A nil next
// is an edge no value takes (bindPattern): it goes to the function's one
// NoMatch block, made the first time a test needs it.
func (bl *irScalarBuilder) passOrFail(at ast.Node, cond ir.Temp, next *ir.Block) {
	pos := bl.g.irNodePos(at)
	if next == nil {
		if bl.sh.unmatched == nil {
			bl.sh.unmatched = bl.f.NewBlock(pos, "pattern mismatch")
			bl.sh.unmatched.Append(ir.NewNoMatch(pos))
			bl.sh.unmatched.SetTerm(ir.NewJump(pos, bl.sh.unmatched.ID()))
		}
		next = bl.sh.unmatched
	}
	passed := bl.f.NewBlock(pos, "pattern passed")
	bl.b.SetTerm(ir.NewBranch(pos, cond, passed.ID(), next.ID()))
	bl.b = passed
}

// literalTest tests an Int, String, Float or Decimal literal pattern: a
// MatchLit, which the VM answers with rt.Equal, so EqFloat and EqDecimal
// decide at every depth.
func (bl *irScalarBuilder) literalTest(p ast.Node, value ir.Temp, k kind, next *ir.Block) bool {
	lit, lk, _, ok := bl.litFor(p, k)
	if !ok || lk != k {
		if ok {
			bl.g.dc.note("a literal pattern's kind: " + lk.nomi())
		}
		return false
	}
	m := ir.NewMatchLitInto(bl.g.irNodePos(p), bl.f.NewTemp(), value, lit)
	bl.b.Append(m)
	bl.side(m.Dst(), irScalarSide{k: kindBool})
	bl.passOrFail(p, m.Dst(), next)
	return true
}

// bindPattern binds the names of pat, a pattern the checker records as
// matching every value of its subject (a parameter's, a binding's with no
// `else`, an Iter.loop state's), from value. A test it still builds (a
// field's literal under an `or` whose alternatives together match every
// value) fails to the function's NoMatch, which no value reaches.
func (bl *irScalarBuilder) bindPattern(pat ast.Node, value ir.Temp, k kind) bool {
	return bl.patternTest(pat, value, k, nil)
}
