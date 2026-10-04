package ir

import "testing"

func TestNot_OperandShape(t *testing.T) {
	for _, valid := range []bool{true, false} {
		f := NewFunc(shapePos(), "negate")
		b := f.NewBlock(shapePos(), "entry")
		operand := f.NewTemp()
		if valid {
			b.Append(NewBool(shapePos(), operand, true))
		} else {
			b.Append(NewInt(shapePos(), operand, 1))
		}
		n := NewNot(shapePos(), f.NewTemp(), operand)
		b.Append(n)
		b.SetTerm(NewReturn(shapePos(), n.Dst()))
		violations := shapeViolations(t, f)
		if valid && len(violations) != 0 {
			t.Fatal(violations)
		}
		if !valid && len(violations) != 1 {
			t.Fatalf("wrong operand violations: %v", violations)
		}
		if got := shapeWritten(f, n, 0); got != ValBool {
			t.Fatalf("result shape %s", got)
		}
	}
}
