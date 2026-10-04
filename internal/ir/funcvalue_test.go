package ir

import "testing"

func TestFuncValue_CapturesAreOperandsAndBodyIsLinted(t *testing.T) {
	at := At("closure.nomi", 1, 1)
	body := NewFunc(at, "body")
	captured := typedParam(body, NewSymbol("capture"), IntType)
	b := body.NewBlock(at, "entry")
	b.SetTerm(NewReturn(at, captured))
	outer := NewFunc(at, "outer")
	entry := outer.NewBlock(at, "entry")
	c := NewInt(at, outer.NewTemp(), 42)
	entry.Append(c)
	operands := []Temp{c.Dst()}
	n := NewFuncValue(at, outer.NewTemp(), body, operands...)
	outer.SetType(n.Dst(), NewFuncType(nil, IntType))
	operands[0] = NoTemp
	entry.Append(n)
	entry.SetTerm(NewReturn(at, n.Dst()))
	if n.Arity() != 0 || n.NumCaptures() != 1 || n.AppendUses(nil)[0] != c.Dst() {
		t.Fatal("capture contract lost")
	}
	if err := Lint(outer); err != nil {
		t.Fatal(err)
	}
	// A nested body's invalid operand must fail lint of its owning function.
	bad := NewFunc(at, "bad")
	bad.NewBlock(at, "entry").SetTerm(NewReturn(at, Temp(99)))
	invalid := NewFunc(at, "invalid")
	ib := invalid.NewBlock(at, "entry")
	badValue := NewFuncValue(at, invalid.NewTemp(), bad)
	ib.Append(badValue)
	ib.SetTerm(NewReturn(at, badValue.Dst()))
	if Lint(invalid) == nil {
		t.Fatal("nested body escaped validation")
	}
}

func TestFuncValue_RejectsMissingCaptureOperands(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("accepted missing capture")
		}
	}()
	at := At("closure.nomi", 1, 1)
	body := NewFunc(at, "body")
	body.AddParam(NewSymbol("capture"), ValInt)
	NewFuncValue(at, Temp(1), body, NoTemp)
}
