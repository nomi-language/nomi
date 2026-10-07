package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"maps"
)

// blockValue lowers a block used as an operand (`pick({ 5 })`): its leading
// statements in their own lexical scope, then its tail's value, then its
// deferred calls. The tail's temporary is the block's value; the block's
// names do not escape.
func (bl *irScalarBuilder) blockValue(block *ast.Block) (ir.Temp, kind, bool, bool) {
	if bl.recording > 0 {
		return ir.NoTemp, kindInvalid, false, false
	}
	defer bl.g.enterBlockTypes(block)()
	lead, tail := bl.g.irScalarBlock(block, "")
	if tail == nil {
		irDeclineNote(irDeclineBodyWhy)
		return ir.NoTemp, kindInvalid, false, false
	}
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	scope := bl.openDeferScope(block)
	ws := bl.openWithScope(false)
	if !bl.leading(lead) {
		return ir.NoTemp, kindInvalid, false, false
	}
	val, k, _, ok := bl.lower(tail)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	bl.closeDefers(scope, tail)
	bl.closeWithScope(ws, tail)
	return val, k, false, true
}

// blockBinding lowers a lexical block into the binding's typed result slot,
// bl.sh.result. Its local identities do not escape; the result register
// does. A tail `if` or `case` writes the slot from each arm, as a function
// body's tail region does, and a tail block is lowered the same way, so the
// block's value may branch at any depth.
func (bl *irScalarBuilder) blockBinding(block *ast.Block, want kind) (kind, bool) {
	defer bl.g.enterBlockTypes(block)()
	lead, tail := bl.g.irScalarBlock(block, "")
	if tail == nil {
		irDeclineNote(irDeclineBodyWhy)
		return kindInvalid, false
	}
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	scope := bl.openDeferScope(block)
	ws := bl.openWithScope(false)
	if !bl.leading(lead) {
		return kindInvalid, false
	}
	var k kind
	var ok bool
	switch t := tail.(type) {
	case *ast.If:
		k, ok = bl.ifRegion(t, irFuncSig{result: want})
	case *ast.Case:
		k, ok = bl.caseRegion(t, irFuncSig{result: want})
	case *ast.Block:
		k, ok = bl.blockBinding(t, want)
	default:
		var val ir.Temp
		val, k, _, ok = bl.lowerWant(tail, want)
		if ok && k != want {
			irDeclineNote("a block's value is not the binding's kind: " + k.nomi() + " vs " + want.nomi())
			ok = false
		}
		if ok {
			bl.resultCopy(tail, val)
		}
	}
	if !ok {
		irDeclineAtNode(tail)
		return kindInvalid, false
	}
	bl.closeDefers(scope, tail)
	bl.closeWithScope(ws, tail)
	return k, true
}
