package irbuild

import (
	"maps"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// patternIfRegion lowers `if pattern = subject { ... } else { ... }` as a
// `case` with one arm whose no-match is the else-arm: caseArmTest tests the
// pattern and binds its names, so every pattern a `case` arm lowers (variant,
// Bool, literal, tuple, list, struct, map, `as`, and their nestings) lowers
// here the same way. The subject is evaluated once, whether or not the
// pattern matches. With no else the `if` is Unit, and a miss answers Unit, as
// ifRegion's Boolean form does.
func (bl *irScalarBuilder) patternIfRegion(t *ast.If, sig irFuncSig) (kind, bool) {
	unitElse := t.Else == nil && !sig.testArms && (sig.inferResult || sig.result == kindUnit)
	switch {
	case t.Else == nil && !sig.testArms && !unitElse:
		irDeclineNote("a pattern `if` without `else` whose value is not Unit")
		return kindInvalid, false
	case isNilNode(t.Cond):
		irDeclineNote("a pattern `if` without a subject")
		return kindInvalid, false
	}
	subj, sk, _, ok := bl.lower(t.Cond)
	if !ok {
		return kindInvalid, false
	}
	if !irHeldValue(bl.f, subj, bl.sides) {
		c := ir.NewCopy(bl.g.irNodePos(t.Cond), bl.f.NewTemp(), subj)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: sk, copy: irCopyHold})
		subj = c.Dst()
	}
	if bl.casePrefix == nil {
		bl.casePrefix = map[*ir.Block]int{}
	}
	bl.casePrefix[bl.b] = len(bl.b.Instrs())
	elsAt := t.Else
	if elsAt == nil {
		elsAt = t
	}
	els := bl.f.NewBlock(bl.g.irNodePos(elsAt), "pattern else")
	exit := bl.f.NewBlock(bl.g.irNodePos(t), "exit")
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	then, next, ok := bl.caseArmTest(t, &ast.CaseBranch{Pattern: t.CondPattern, Body: t.Then}, false, subj, sk, true, els)
	if !ok {
		return kindInvalid, false
	}
	if next == nil {
		// The checker rejects a pattern `if` whose pattern matches every
		// value (`if x = v`), so the else-arm always has a way in.
		irDeclineNote("a pattern `if` whose pattern always matches " + sk.nomi())
		return kindInvalid, false
	}
	tk, ok := bl.armInto(then, exit, t.Then, sig)
	if !ok {
		return kindInvalid, false
	}
	bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms
	ek, ok := bl.testElse(els, exit, t, tk, bl.laterArmSig(sig, tk))
	if !ok {
		return kindInvalid, false
	}
	k, agree := irJoinArms(tk, ek, sig)
	if !agree {
		irDeclineNote("a pattern `if` whose arms disagree: " + tk.nomi() + " vs " + ek.nomi())
		return kindInvalid, false
	}
	bl.b = exit
	return k, true
}
