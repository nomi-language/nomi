package irbuild

import (
	"maps"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irReturningArms recognizes explicit terminal returns without unwrapping their
// operands into implicit results. Both branches exit this activation.
func irReturningArms(t *ast.If) (*ast.Return, *ast.Return) {
	els, ok := t.Else.(*ast.Block)
	if !ok || els == nil || t.Then == nil || len(t.Then.Stmts) == 0 || len(els.Stmts) == 0 {
		return nil, nil
	}
	thenRet, _ := t.Then.Stmts[len(t.Then.Stmts)-1].(*ast.Return)
	elseRet, _ := els.Stmts[len(els.Stmts)-1].(*ast.Return)
	return thenRet, elseRet
}

func (bl *irScalarBuilder) returnBranches(t *ast.If, thenRet, elseRet *ast.Return) (kind, bool) {
	if bl.inTest || t.CondPattern != nil || isNilNode(t.Cond) {
		return kindInvalid, false
	}
	cond, ck, _, ok := bl.lower(t.Cond)
	if !ok || ck != kindBool {
		return kindInvalid, false
	}
	then := bl.f.NewBlock(bl.g.irNodePos(t.Then), "return then")
	els := bl.f.NewBlock(bl.g.irNodePos(t.Else), "return else")
	br := ir.NewBranch(bl.g.irNodePos(t.Cond), cond, then.ID(), els.ID())
	bl.b.SetTerm(br)
	if _, ok := bl.returnBranch(then, t.Then, thenRet); !ok {
		return kindInvalid, false
	}
	return bl.returnBranch(els, t.Else.(*ast.Block), elseRet)
}

func (bl *irScalarBuilder) returnBranch(arm *ir.Block, body *ast.Block, ret *ast.Return) (kind, bool) {
	defer bl.g.enterBlockTypes(body)()
	lead, tail := bl.g.irScalarBlock(body, "")
	if tail != ret {
		return kindInvalid, false
	}
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	bl.b = arm
	// The arm ends in `return`, which leaves the activation and so restores
	// what a `with` in it replaced.
	bl.openWithScope(true)
	if !bl.leading(lead) || bl.b != arm {
		return kindInvalid, false
	}
	return bl.explicitReturn(ret)
}
