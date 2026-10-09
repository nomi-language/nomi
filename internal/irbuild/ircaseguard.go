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
// next is nil for an unguarded irrefutable pattern (`_`, a name, a tuple of
// names): it matches every value, so no later arm can run.
//
// bl.b is left at next.
func (bl *irScalarBuilder) caseArmTest(t ast.Node, br *ast.CaseBranch, adHoc bool, subj ir.Temp, sk kind, last bool, nomatch *ir.Block) (arm, next *ir.Block, ok bool) {
	// `P as name ->`: the arm tests P, and name binds the subject where P's
	// names are bound.
	asPattern := br.Pattern
	if inner := ast.WithoutAs(br.Pattern); inner != br.Pattern {
		peeled := *br
		peeled.Pattern = inner
		br = &peeled
	}
	guarded := br.Guard != nil
	_, wildcard := br.Pattern.(*ast.WildcardPattern)
	ident, _ := br.Pattern.(*ast.IdentPattern)
	if adHoc || !irRetainedValueKind(sk) {
		ident = nil
	}
	tupleFallback := !adHoc && tupleCaseIrrefutable(br.Pattern, sk)
	irrefutable := wildcard || ident != nil || tupleFallback
	arm = bl.f.NewBlock(bl.g.irNodePos(br.Body), "arm")
	target := arm
	if guarded {
		target = bl.f.NewBlock(bl.g.irNodePos(br.Guard), "guard")
	}
	if !irrefutable || guarded {
		next = nomatch
		if !last {
			next = bl.f.NewBlock(bl.g.irNodePos(t), "next")
		}
	}
	if irrefutable {
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(br.Pattern), target.ID()))
		bl.b = target
		if tupleFallback {
			bl.tupleCaseBindings(br.Pattern, subj, sk)
		} else if ident != nil {
			bl.patternBinding(ident, ident.Name, subj, sk)
		}
	} else if !bl.casePatternTest(br, adHoc, subj, sk, target, next) {
		return nil, nil, false
	}
	bl.bindAsNames(asPattern, subj, sk)
	if guarded {
		bl.b = target
		cond, ck, _, ok := bl.lower(br.Guard)
		if !ok || ck != kindBool {
			if ok {
				irDeclineNote("a case guard that is not Bool: " + ck.nomi())
			}
			return nil, nil, false
		}
		bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(br.Guard), cond, arm.ID(), next.ID()))
	}
	if next != nil {
		bl.b = next
	}
	return arm, next, true
}

// casePatternTest tests one refutable pattern against the subject, branching
// to target on success (with its names bound on that path) and to next on
// failure.
func (bl *irScalarBuilder) casePatternTest(br *ast.CaseBranch, adHoc bool, subj ir.Temp, sk kind, target, next *ir.Block) bool {
	if adHoc {
		cond, ck, _, ok := bl.lower(br.Pattern)
		if !ok || ck != kindBool {
			irDeclineNote("an ad-hoc case condition outside a straight Bool expression")
			return false
		}
		bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(br.Pattern), cond, target.ID(), next.ID()))
		return true
	}
	if cp, isCodepoint := br.Pattern.(*ast.CodepointLit); isCodepoint {
		if !bl.codepointCaseTest(cp, subj, sk, target, next) {
			irDeclineNote("a codepoint literal pattern over a subject that is not a Codepoint")
			return false
		}
		return true
	}
	switch {
	case irRetainedListKind(sk) || irListTransportKind(sk):
		if !bl.listCaseTest(br.Pattern, subj, sk, target, next) {
			irDeclineNote("a list case pattern outside flat bindings and wildcards")
			return false
		}
		return true
	case irNominalCaseKind(sk):
		if !bl.nominalCaseTest(br.Pattern, subj, sk, target, next) {
			irDeclineNote("a struct or distinct case pattern outside retained field and payload patterns")
			return false
		}
		return true
	case sk.tag == tagMap && irRetainedMapKind(sk):
		if !bl.mapCaseTest(br.Pattern, subj, sk, target, next) {
			irDeclineNote("a map case pattern outside retained keys and value patterns")
			return false
		}
		return true
	case sk == kindBool || (sk.tag == tagNamed && irRetainedEnumKind(sk.def)):
		if !bl.enumCaseTest(br.Pattern, subj, sk, target, next) {
			irDeclineNote("a `case` enum pattern outside retained bare/positional payload tests")
			return false
		}
		return true
	case irRetainedTupleKind(sk):
		return bl.tupleCaseTest(br.Pattern, subj, sk, target, next)
	}
	lit, lk, _, lok := bl.litFor(br.Pattern, sk)
	if !lok || lk != sk {
		if lok {
			irDeclineNote("a `case` literal pattern's kind: " + lk.nomi())
		} else {
			irDeclineNote("a `case` pattern outside the retained tests")
		}
		return false
	}
	m := ir.NewMatchLitInto(bl.g.irNodePos(br.Pattern), bl.f.NewTemp(), subj, lit)
	bl.b.Append(m)
	bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(br.Pattern), m.Dst(), target.ID(), next.ID()))
	bl.b = target
	return true
}
