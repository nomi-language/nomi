package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// caseArmTest builds one `case` arm's selector, in the order it is tried:
// the pattern's test (binding its names), then the guard with those names in
// scope. It answers the arm's block, which the caller lowers the body into,
// and the block the next arm's test starts in. On a failed pattern or guard
// control reaches that block; after the last arm it is the case's NoMatch.
//
// next is nil for an unguarded pattern the checker records as matching
// every value (patternAlwaysMatches: `_`, a name, `Point{x:, y:}`, `Unit`,
// `Port(n)`): no later arm can run. Such a pattern's test, which binds its
// names, has a failure edge the record says no value takes. An ad-hoc
// arm's condition is an expression, and only its `_` matches every value.
//
// bl.b is left at next.
func (bl *irScalarBuilder) caseArmTest(t ast.Node, br *ast.CaseBranch, adHoc bool, subj ir.Temp, sk kind, last bool, nomatch *ir.Block) (arm, next *ir.Block, ok bool) {
	_, wildcard := br.Pattern.(*ast.WildcardPattern)
	irrefutable := wildcard
	if !adHoc {
		irrefutable = bl.g.patternAlwaysMatches(ast.WithoutAs(br.Pattern))
	}
	if !irrefutable || br.Guard != nil {
		next = nomatch
		if !last {
			next = bl.f.NewBlock(bl.g.irNodePos(t), "next")
		}
	}
	failed := next
	if failed == nil {
		failed = nomatch
	}
	switch {
	case !adHoc:
		if !bl.patternTest(br.Pattern, subj, sk, failed) {
			return nil, nil, false
		}
	case !wildcard:
		cond, ck, _, ok := bl.lower(br.Pattern)
		if !ok || ck != kindBool {
			// The arm's condition is where the decline is reported, unless
			// an expression inside it already placed it.
			bl.g.dc.note("an ad-hoc case condition outside a straight Bool expression")
			bl.g.dc.atNode(br.Pattern)
			return nil, nil, false
		}
		bl.passOrFail(br.Pattern, cond, failed)
	}
	arm = bl.b
	if br.Guard != nil {
		cond, ck, _, ok := bl.lower(br.Guard)
		if !ok || ck != kindBool {
			if ok {
				bl.g.dc.note("a case guard that is not Bool: " + ck.nomi())
			}
			return nil, nil, false
		}
		arm = bl.f.NewBlock(bl.g.irNodePos(br.Body), "arm")
		bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(br.Guard), cond, arm.ID(), next.ID()))
	}
	if next != nil {
		bl.b = next
	}
	return arm, next, true
}
