package ir_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// Infallible has no values: every type accepts it, it accepts nothing but
// itself and Any, and a temporary of it takes no register.
func TestNeverType_IsAcceptedEverywhereAndStoresNothing(t *testing.T) {
	for _, want := range []*ir.ValType{ir.IntType, ir.StringType, ir.UnitType,
		ir.NewListType(ir.IntType), ir.NewTupleType(ir.IntType, ir.BoolType)} {
		if !want.Accepts(ir.NeverType) {
			t.Errorf("%s does not accept Infallible", want)
		}
		if ir.NeverType.Accepts(want) {
			t.Errorf("Infallible accepts %s", want)
		}
	}
	if !ir.NewListType(ir.IntType).Accepts(ir.NewListType(ir.NeverType)) {
		t.Error("List<Int> does not accept List<Infallible>")
	}
	if ir.NeverType.Class() != ir.ClassNone {
		t.Errorf("Infallible's class is %s", ir.NeverType.Class())
	}
	if ir.NeverType.String() != "Infallible" {
		t.Errorf("Infallible prints as %q", ir.NeverType.String())
	}
}

// A `todo` of Infallible retyped to Int for the Int the function returns:
// the graph lints, and an image keeps NeverType as the shared singleton.
func TestNeverType_ATodoRetypedToItsPositionLintsAndRoundTrips(t *testing.T) {
	const file = "never.nomi"
	f := ir.NewFuncFor(ir.At(file, 1, 1), ir.NewSymbol("f"))
	b := f.NewBlock(ir.At(file, 1, 1), "entry")
	never := f.NewTemp()
	b.Append(ir.NewTodo(ir.At(file, 2, 5), never, ""))
	f.SetType(never, ir.NeverType)
	n := f.NewTemp()
	b.Append(ir.NewCopy(ir.At(file, 2, 5), n, never))
	f.SetType(n, ir.IntType)
	b.SetTerm(ir.NewReturn(ir.At(file, 3, 1), n))
	if err := ir.Lint(f); err != nil {
		t.Fatalf("lint: %v", err)
	}

	m := ir.NewModule("never.nomi")
	m.AddFunc(f)
	data, err := ir.EncodeImage(ir.Image{Modules: []*ir.Module{m}, Entry: 0})
	if err != nil {
		t.Fatal(err)
	}
	back, err := ir.DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	g := back.Modules[0].Funcs()[0]
	if g.TempType(never) != ir.NeverType {
		t.Fatalf("the todo's type decoded as %v, not the shared NeverType", g.TempType(never))
	}
}
