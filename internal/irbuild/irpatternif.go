package irbuild

import (
	"maps"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// patternIfRegion lowers `if pattern = subject { ... } else { ... }`. The
// subject is evaluated once, whether or not the pattern matches. A wildcard or
// identifier pattern always matches, so its then-arm runs and its else-arm is
// dead. With no else the `if` is Unit, and a miss answers Unit, as ifRegion's
// Boolean form does.
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
	switch p := t.CondPattern.(type) {
	case *ast.WildcardPattern:
		return bl.irrefutablePatternIf(t, sig, nil, subj, sk)
	case *ast.IdentPattern:
		return bl.irrefutablePatternIf(t, sig, p, subj, sk)
	}
	// `if P as name = v`: P is tested, and name binds v in the then-arm.
	pattern := ast.WithoutAs(t.CondPattern)
	// `if .Circle{radius} = v` is a braced variant test, which enumCaseTest
	// lowers as it does a `case` arm's, or declines.
	sp, braced := pattern.(*ast.StructPattern)
	braced = braced && sp.TypeName != nil
	// `if False = b` over a Bool: Bool has no *typeDef, and enumCaseTest
	// tests its variants as `case b { False -> … }` does.
	if _, _, ok = bl.g.retainedEnumPattern(pattern, sk); !ok && !braced && sk != kindBool {
		irDeclineNote("a pattern `if` whose pattern is not a retained variant test over " + sk.nomi())
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
	then := bl.f.NewBlock(bl.g.irNodePos(t.Then), "pattern then")
	elsAt := t.Else
	if elsAt == nil {
		elsAt = t
	}
	els := bl.f.NewBlock(bl.g.irNodePos(elsAt), "pattern else")
	exit := bl.f.NewBlock(bl.g.irNodePos(t), "exit")
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	if !bl.enumCaseTest(pattern, subj, sk, then, els) {
		irDeclineNote("a pattern `if` variant test outside retained payload patterns")
		return kindInvalid, false
	}
	bl.bindAsNames(t.CondPattern, subj, sk)
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

// irrefutablePatternIf lowers a pattern `if` whose pattern always matches: the
// then-arm, with an identifier pattern binding the subject. The else-arm never
// runs, so it is not built.
func (bl *irScalarBuilder) irrefutablePatternIf(t *ast.If, sig irFuncSig, id *ast.IdentPattern, subj ir.Temp, sk kind) (kind, bool) {
	then := bl.f.NewBlock(bl.g.irNodePos(t.Then), "pattern then")
	exit := bl.f.NewBlock(bl.g.irNodePos(t), "exit")
	bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(t.CondPattern), then.ID()))
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	if id != nil {
		prev := bl.b
		bl.b = then
		bl.patternBinding(id, id.Name, subj, sk)
		bl.b = prev
	}
	tk, ok := bl.armInto(then, exit, t.Then, sig)
	if !ok {
		return kindInvalid, false
	}
	if tk == kindDiverged {
		// The only arm that runs leaves the activation.
		bl.b = exit
		return irJoinArms(tk, tk, sig)
	}
	if t.Else == nil && !sig.testArms && tk != kindUnit {
		irDeclineNote("a pattern `if` without `else` whose then-arm is not Unit: " + tk.nomi())
		return kindInvalid, false
	}
	bl.b = exit
	return tk, true
}
