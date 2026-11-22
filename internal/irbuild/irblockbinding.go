package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// blockValue lowers a block used as an operand (`pick({ 5 })`): its leading
// statements in their own lexical scope, then its tail's value, then its
// deferred calls. The tail's temporary is the block's value; the block's
// names do not escape.
//
// want, when it is not kindInvalid, types the tail as it would type the
// block's value written there: the position's expected type, or the
// block's own record (blockKind). `Hold{v: { io.print("x")  [] }}` builds
// its set at the field's `Set<Int>`, and `[1, "a"]` as a tail where a
// `List<Display>` is expected holds each element as the existential.
func (bl *irScalarBuilder) blockValue(block *ast.Block, want kind) (ir.Temp, kind, bool, bool) {
	defer bl.keepOpenDefers()()
	defer bl.g.enterBlockTypes(block)()
	lead, tail := bl.g.irScalarBlock(block)
	scope := bl.openDeferScope(block)
	ws := bl.openWithScope(false)
	if !bl.leading(lead) {
		return ir.NoTemp, kindInvalid, false, false
	}
	bl.markStmt(tail, ir.StmtStatement)
	var val ir.Temp
	var k kind
	var ok bool
	// kindInvalid: sentinel — no expected type was passed.
	if want == kindInvalid {
		val, k, _, ok = bl.lower(tail)
	} else {
		val, k, _, ok = bl.lowerTypedOperand(tail, want)
	}
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	bl.closeDefers(scope, tail)
	bl.closeWithScope(ws, tail)
	return val, k, false, true
}

// blockKind is the kind a block used as an operand with no expected type
// is lowered at: the kind the checker's records give it (valueKind), as a
// region's (regionSig). A block with no record that names a kind answers
// kindInvalid, and its tail's own kind is the block's.
func (bl *irScalarBuilder) blockKind(block *ast.Block) kind {
	if k, ok := bl.valueKind("block", block); ok {
		return k
	}
	return kindInvalid
}

// blockBinding lowers a lexical block into the binding's typed result slot,
// bl.sh.result, of kind want, which its tail is lowered at. Its local
// identities do not escape; the result register does. A tail `if` or `case`
// writes the slot from each arm, as a function body's tail region does, and
// a tail block is lowered the same way, so the block's value may branch at
// any depth.
func (bl *irScalarBuilder) blockBinding(block *ast.Block, want kind) bool {
	defer bl.keepOpenDefers()()
	defer bl.g.enterBlockTypes(block)()
	lead, tail := bl.g.irScalarBlock(block)
	scope := bl.openDeferScope(block)
	ws := bl.openWithScope(false)
	if !bl.leading(lead) {
		return false
	}
	var ok bool
	bl.markStmt(tail, ir.StmtStatement)
	switch t := tail.(type) {
	case *ast.If:
		_, ok = bl.ifRegion(t, irFuncSig{result: want})
	case *ast.Case:
		_, ok = bl.caseRegion(t, irFuncSig{result: want})
	case *ast.Block:
		ok = bl.blockBinding(t, want)
	default:
		var val ir.Temp
		val, _, _, ok = bl.lowerWant(tail, want)
		if ok {
			bl.resultCopy(tail, val)
		}
	}
	if !ok {
		bl.g.dc.atNode(tail)
		return false
	}
	bl.closeDefers(scope, tail)
	bl.closeWithScope(ws, tail)
	return true
}
