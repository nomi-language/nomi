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

// blockBinding lowers a lexical block into the binding's typed result slot.
// Its local identities do not escape; the result register does. Scope bounds
// only manage the builder's Go-spelled name environment and produce no statements.
func (bl *irScalarBuilder) blockBinding(block *ast.Block, slot *ir.Slot, want kind) (kind, bool) {
	lead, tail := bl.g.irScalarBlock(block, "")
	if tail == nil {
		return kindInvalid, false
	}
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	entry := bl.b
	scope := bl.openDeferScope(block)
	ws := bl.openWithScope(false)
	if !bl.leading(lead) {
		return kindInvalid, false
	}
	val, k, _, ok := bl.lower(tail)
	if !ok || k != want || bl.b != entry {
		irDeclineNote("a block-valued binding outside a straight result of the declared kind")
		return kindInvalid, false
	}
	bl.resultCopy(tail, val)
	bl.closeDefers(scope, tail)
	bl.closeWithScope(ws, tail)
	return k, true
}
