package ir

import "testing"

func TestCompareVariant_ShapeAndOrdering(t *testing.T) {
	for _, valid := range []bool{true, false} {
		f := NewFunc(shapePos(), "equal")
		b := f.NewBlock(shapePos(), "entry")
		a, c := f.NewTemp(), f.NewTemp()
		sym := NewSymbol("maybe.Maybe")
		b.Append(NewMakeVariant(shapePos(), a, sym, "None", nil))
		if valid {
			b.Append(NewMakeVariant(shapePos(), c, sym, "None", nil))
		} else {
			b.Append(NewInt(shapePos(), c, 1))
		}
		n := NewCompare(shapePos(), f.NewTemp(), OpEq, ValVariant, a, c)
		b.Append(n)
		b.SetTerm(NewReturn(shapePos(), n.Dst()))
		violations := shapeViolations(t, f)
		if valid && len(violations) != 0 || !valid && len(violations) != 1 {
			t.Fatalf("valid=%v: %v", valid, violations)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("variant ordering was admitted")
		}
	}()
	NewCompare(shapePos(), Temp(3), OpLt, ValVariant, Temp(1), Temp(2))
}
