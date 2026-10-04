package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Decimal is already shared with rt. This adapter only validates operands and
// converts runtime traps into the VM's error result at the operation boundary.
func (m *Machine) decimalArith(fr *frame, n *ir.Arith, lhs, rhs any) (err error) {
	a, ok := lhs.(rt.Decimal)
	if !ok {
		return fmt.Errorf("vm: %s: a Decimal operation's left operand is %T", fr.fn.Name(), lhs)
	}
	var b rt.Decimal
	if n.Op() != ir.OpNeg {
		if b, ok = rhs.(rt.Decimal); !ok {
			return fmt.Errorf("vm: %s: a Decimal operation's right operand is %T", fr.fn.Name(), rhs)
		}
	}
	defer func() {
		if p := recover(); p != nil {
			if fault, ok := p.(*rt.Error); ok {
				err = &Fault{err: fault}
			} else {
				panic(p)
			}
		}
	}()
	var result rt.Decimal
	switch n.Op() {
	case ir.OpAdd:
		result = rt.AddDecimal(a, b)
	case ir.OpSub:
		result = rt.SubDecimal(a, b)
	case ir.OpMul:
		result = rt.MulDecimal(a, b)
	case ir.OpDiv:
		result = rt.DivDecimal(a, b, n.Pos().Line())
	case ir.OpNeg:
		result = rt.NegDecimal(a)
	case ir.OpRem:
		result = rt.ModDecimal(a, b, n.Pos().Line())
	default:
		return fmt.Errorf("vm: no such Decimal operator %s", n.Op())
	}
	fr.write(n.Dst(), result)
	return nil
}
