package ir

import "testing"

func TestCompareList_ShapeAndOrdering(t *testing.T) {
	for _, valid := range []bool{true, false} {
		f := NewFunc(shapePos(), "equal")
		b := f.NewBlock(shapePos(), "entry")
		a, c := f.NewTemp(), f.NewTemp()
		b.Append(NewEmptyList(shapePos(), a, NewSymbol("Int")))
		if valid {
			b.Append(NewEmptyList(shapePos(), c, NewSymbol("Int")))
		} else {
			b.Append(NewInt(shapePos(), c, 1))
		}
		n := NewCompare(shapePos(), f.NewTemp(), OpEq, ValContainer, a, c)
		b.Append(n)
		b.SetTerm(NewReturn(shapePos(), n.Dst()))
		violations := shapeViolations(t, f)
		if valid && len(violations) != 0 || !valid && len(violations) != 1 {
			t.Fatalf("valid=%v: %v", valid, violations)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("list ordering was admitted")
		}
	}()
	NewCompare(shapePos(), Temp(3), OpLt, ValContainer, Temp(1), Temp(2))
}
