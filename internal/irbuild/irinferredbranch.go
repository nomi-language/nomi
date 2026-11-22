package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// inferredRegionValue lowers an `if` or `case` used for its value with the
// same arm lowering used by tail expressions. The region's kind is the
// checker's type for it (regionSig): an arm of a narrower type enters it, as
// a `List<T>` enters an `Iter<T>` and a concrete value an interface. The
// typed declaration is inserted only after that answer exists; the slot and
// its type are immutable once constructed.
func (bl *irScalarBuilder) inferredRegionValue(value ast.Node, statement bool) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	// Inside an assertion subject a row recorded in an arm is an `ir.Record`
	// the machine appends when that arm runs.
	entry, index := bl.b, len(bl.b.Instrs())
	result := bl.f.NewTemp()
	outer := bl.sh.result
	bl.sh.result = result
	defer func() { bl.sh.result = outer }()
	sig, ok := bl.regionSig(value)
	if !ok {
		bl.g.dc.note("an `if` or `case` used for its value with no type in the checker's records")
		return no()
	}
	var k kind
	switch v := value.(type) {
	case *ast.If:
		k, ok = bl.ifRegion(v, sig)
	case *ast.Case:
		k, ok = bl.caseRegion(v, sig)
	}
	diverged := ok && (k == kindDiverged || !ir.Reachable(bl.f)[bl.b.ID()])
	switch {
	case !ok:
		// The arm that declined named its reason.
	case diverged:
		// A region whose every arm returned has no value to bind, and the
		// statements after it cannot run; the checker rejects them.
		bl.g.dc.note("an `if` or `case` whose every arm leaves the body, used for its value")
	}
	if !ok || diverged {
		bl.abandonRegion(value)
		return no()
	}
	// The region's kind is the record's, which every arm met.
	irHeldOperand(k, "an `if` or `case` used for its value")
	// A held kind has an IR type: irTypeOf answers nil only for kindInvalid.
	ty := bl.g.irTypeOf(k)
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

// regionSig is the signature value, an `if` or `case` used for its value,
// lowers its arms with: the kind the checker's records give it
// (valueKind). A region of Infallible holds no value, each arm a trap or a
// leave: it takes the type its position settled, as a `todo` does, or
// stays Infallible. ok is false for a region with no record that names a
// kind, which declines.
func (bl *irScalarBuilder) regionSig(value ast.Node) (irFuncSig, bool) {
	k, ok := bl.valueKind("region", value)
	return irFuncSig{result: k}, ok
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
