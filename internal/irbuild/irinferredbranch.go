package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// inferredRegionValue learns the result from the same arm lowering used by
// tail expressions. The typed declaration is inserted only after that answer
// exists; the slot and its type are immutable once constructed.
func (bl *irScalarBuilder) inferredRegionValue(value ast.Node, statement bool) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	// Inside an assertion subject only in a body built for the VM: a row
	// recorded in an arm is an `ir.Record` the machine appends when that arm
	// runs.
	if bl.recording > 0 && !bl.inTest {
		return no()
	}
	entry, index := bl.b, len(bl.b.Instrs())
	result := bl.f.NewTemp()
	outer := bl.sh.result
	bl.sh.result = result
	defer func() { bl.sh.result = outer }()
	var k kind
	var ok bool
	switch v := value.(type) {
	case *ast.If:
		k, ok = bl.ifRegion(v, irFuncSig{inferResult: true})
	case *ast.Case:
		k, ok = bl.caseRegion(v, irFuncSig{inferResult: true})
	}
	switch {
	case !ok:
		// The arm that declined named its reason.
	case k == kindDiverged:
		// A region whose every arm returned has no value to bind, and the
		// statements after it cannot run; the checker rejects them.
		irDeclineNote("an `if` or `case` whose every arm leaves the body, used for its value")
	case !irCallableValueKind(k) && k != kindUnit:
		irDeclineNote("an `if` or `case` whose value's kind is outside the domain: " + k.nomi())
	}
	if !ok || k == kindDiverged || (!irCallableValueKind(k) && k != kindUnit) {
		bl.abandonRegion(value)
		return no()
	}
	ty := bl.g.irTypeOf(k)
	if ty == nil {
		irDeclineNote("an `if` or `case` whose value has no IR type: " + k.nomi())
		bl.abandonRegion(value)
		return no()
	}
	if statement {
		// Case statements hold their subject before allocating the
		// discarded result; case operands allocate their result first.
		if prefix, ok := bl.casePrefix[entry]; ok {
			index = prefix
		}
	}
	entry.InsertSlot(index, ir.NewSlot(bl.g.irNodePos(value), result, ty))
	if prefix, hasCase := bl.casePrefix[entry]; hasCase {
		bl.casePrefix[entry] = prefix + 1
	}
	bl.side(result, irScalarSide{k: k})
	return result, k, true, true
}

// abandonRegion leaves the builder on an open block after a region declined
// partway. The region may have terminated the block it started in, or left
// the builder on its NoMatch block, and a caller that lowers its next operand
// after a failed one would otherwise append past a terminator. The body is
// declined either way; this keeps the decline a decline.
func (bl *irScalarBuilder) abandonRegion(at ast.Node) {
	if bl.b.Term() != nil {
		bl.b = bl.f.NewBlock(bl.g.irNodePos(at), "declined region")
	}
}
