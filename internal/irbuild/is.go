package irbuild

import (
	"maps"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// isTest lowers `value is Pattern` as a `case` with one arm: caseArmTest
// tests the pattern as it tests an arm's, so every pattern a `case` arm
// lowers lowers here the same way, and the arm writes True into the test's
// Bool where the no-match block writes False. The value is evaluated once.
// The pattern binds nothing (the checker refuses a name), so no binding
// outlives the test.
func (bl *irScalarBuilder) isTest(t *ast.Is) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	subj, sk, _, ok := bl.lower(t.Value)
	if !ok {
		return no()
	}
	if !irHeldValue(bl.f, subj, bl.sides) {
		c := ir.NewCopy(bl.g.irNodePos(t.Value), bl.f.NewTemp(), subj)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: sk, copy: irCopyHold})
		subj = c.Dst()
	}
	pos := bl.g.irNodePos(t)
	result := bl.f.NewTemp()
	bl.b.Append(ir.NewSlot(pos, result, bl.g.irTypeOf(kindBool)))
	missed := bl.f.NewBlock(pos, "is miss")
	exit := bl.f.NewBlock(pos, "exit")
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	matched, next, ok := bl.caseArmTest(t, &ast.CaseBranch{Pattern: t.Pattern, Body: t}, false, subj, sk, true, missed)
	if !ok {
		return no()
	}
	if next == nil {
		// The checker rejects a pattern that matches every value of the
		// value's type, so the no-match block always has a way in.
		irDeclineNote("an `is` test whose pattern always matches " + sk.nomi())
		return no()
	}
	for _, arm := range []struct {
		b *ir.Block
		v bool
	}{{matched, true}, {missed, false}} {
		c := ir.NewBool(pos, bl.f.NewTemp(), arm.v)
		arm.b.Append(c)
		arm.b.Append(ir.NewCopy(pos, result, c.Dst()))
		arm.b.SetTerm(ir.NewJump(pos, exit.ID()))
	}
	bl.b = exit
	bl.side(result, irScalarSide{k: kindBool})
	// MOBILE: the result is storage two Copies write and a reader reads.
	return result, kindBool, true, true
}
