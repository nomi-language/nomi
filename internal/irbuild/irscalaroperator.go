package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func (bl *irScalarBuilder) scalarOperatorCall(t *ast.Call, args irQualArgs, owner, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	op, known := scalarArithOp(owner, method)
	if !known || !args.ok || len(args.kinds) != 2 || args.kinds[0] != args.kinds[1] {
		return no()
	}
	// Local or imported user interfaces retain their own dispatch path.
	if d, found := bl.g.ifaceNamed(owner); found && !d.stdShared {
		return no()
	}
	k := args.kinds[0]
	if k == kindString && op == "+" {
		n := ir.NewConcat(bl.g.irNodePos(t), bl.f.NewTemp(), args.temps...)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: k})
		return n.Dst(), k, false, true
	}
	shape, ok := bl.g.irArithKind(irArithOps[op], k, false)
	if !ok {
		return no()
	}
	n := ir.NewArith(bl.g.irNodePos(t), bl.f.NewTemp(), irArithOps[op], shape, args.temps[0], args.temps[1])
	bl.b.Append(n)
	return n.Dst(), k, false, true
}

// scalarEqualCall lowers `Int.equal?(a, b)` and its Float/String/Bool/Equatable
// spellings over two scalars of one kind to an `ir.Compare`, which the Go
// reader spells through `scalarEqualCode` (`==`, or `rt.EqFloat`). Without it
// the call would reach the stdlib index and become a call to the std
// `Equatable.equal?` body.
//
// `ir.Compare` does not admit a Unit shape, so a Unit pair declines the body.
func (bl *irScalarBuilder) scalarEqualCall(t *ast.Call, args irQualArgs, owner, method string) (ir.Temp, kind, bool, bool, bool) {
	if method != "equal?" || len(t.Args) != 2 {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	switch owner {
	case "Equatable", "Int", "Float", "String", "Bool":
	default:
		return ir.NoTemp, kindInvalid, false, false, false
	}
	// From here this row owns the call: operands it cannot compare decline
	// the body rather than falling through to a later arm.
	if !args.ok || args.kinds[0] != args.kinds[1] {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	var shape ir.ValShape
	switch args.kinds[0] {
	case kindInt:
		shape = ir.ValInt
	case kindFloat:
		shape = ir.ValFloat
	case kindString:
		shape = ir.ValString
	case kindBool:
		shape = ir.ValBool
	case kindUnit:
		return ir.NoTemp, kindInvalid, false, false, true
	default:
		return ir.NoTemp, kindInvalid, false, false, false
	}
	c := ir.NewCompare(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OpEq, shape, args.temps[0], args.temps[1])
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	return c.Dst(), kindBool, false, true, true
}
