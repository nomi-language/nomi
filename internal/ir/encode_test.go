package ir_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// imageFixture builds two modules that exercise what the format must keep:
// a symbol shared across modules, a self-reaching struct type, a recursive
// function value, a lazy cell, a test case in a group, dispatch and Display
// entries, a table type with a conformance, a deferred
// call, the shared scalar types, and nil beside empty slices.
func imageFixture() (ir.Image, map[string]any) {
	const file = "fixture.nomi"
	at := func(line, col int) ir.Pos { return ir.At(file, line, col) }

	shared := ir.NewSymbol("shared.helper")
	nodeSym := ir.NewSymbol("shapes.Node")
	node := ir.NewStructType(nodeSym)
	// A struct whose field reaches its own type through a List.
	node.SetFields([]ir.Field{{Name: "value", Type: ir.IntType},
		{Name: "children", Type: ir.NewListType(node)}})

	tab := ir.NewTable()
	point, _ := tab.Concrete("Point", "Point")
	point.SetVal(node)
	show, _ := tab.Existential("Show", "Show")
	tab.Conforms(point, show)

	lib := ir.NewModule("lib.nomi")
	helper := ir.NewFuncFor(at(1, 1), shared)
	x := helper.AddParam(ir.NewSymbol("x"), ir.ValInt)
	helper.SetType(x, ir.IntType)
	hb := helper.NewBlock(at(1, 1), "entry")
	sum := helper.NewTemp()
	hb.Append(ir.NewArith(at(2, 3), sum, ir.OpAdd, ir.IntArith(ir.OverflowFaults), x, x))
	helper.SetType(sum, ir.IntType)
	pt := helper.NewTemp()
	hb.Append(ir.NewSlot(at(2, 5), pt, point))
	named := helper.NewTemp()
	hb.Append(ir.NewCopy(at(2, 7), named, pt))
	helper.SetType(named, node)
	hb.SetTerm(ir.NewReturn(at(3, 1), sum))
	lib.AddFunc(helper)
	lib.ImplementDisplay(nodeSym, shared)

	app := ir.NewModule("app.nomi")
	main := ir.NewFuncFor(ir.Spanning(file, 10, 1, 14, 2), ir.NewSymbol("main"))
	mb := main.NewBlock(at(10, 1), "entry")
	one := main.NewTemp()
	mb.Append(ir.NewInt(at(11, 3), one, 1))
	got := main.NewTemp()
	mb.Append(ir.NewCall(at(11, 5), got, ir.OrdinaryCall, shared, one))
	mb.DeferLastCall(at(11, 5), 1)
	lambda := ir.NewFunc(at(12, 3), "lambda")
	lambda.AddParam(ir.NewSymbol("self"), ir.ValFunc)
	lb := lambda.NewBlock(at(12, 3), "entry")
	lb.SetTerm(ir.NewReturnUnit(at(12, 9)))
	fv := main.NewTemp()
	mb.Append(ir.NewRecursiveFuncValue(at(12, 3), fv, lambda))
	lb2 := lambda.NewBlock(at(12, 4), "again")
	self := lambda.NewTemp()
	lb2.Append(ir.NewFuncValue(at(12, 4), self, lambda))
	lb2.SetTerm(ir.NewReturnUnit(at(12, 9)))
	mb.Append(ir.NewMakeRecord(at(13, 1), main.NewTemp(), []string{}, nil))
	mb.Append(ir.NewRenderDebugWith(at(13, 2), main.NewTemp(), one, []ir.DebugImpl{}))
	mb.Append(ir.NewRunDefer(at(13, 3), 1))
	mb.SetFault(at(13, 4), 0)
	mb.SetTerm(ir.NewReturnUnit(at(14, 1)))
	app.AddFunc(main)

	init := ir.NewFunc(ir.AtSynthesized(file, 20, 1), "cell.init")
	ib := init.NewBlock(at(20, 1), "entry")
	f := init.NewTemp()
	ib.Append(ir.NewFloat(at(20, 3), f, -0.5))
	ib.SetTerm(ir.NewReturn(at(20, 4), f))
	app.DeclareLazyCell(ir.NewSymbol("cell"), point, init)
	boot := ir.NewSymbol("boot")
	app.SetBoot(boot)
	app.AddTestBoot(boot)
	body := ir.NewFunc(at(30, 1), "test body")
	body.NewBlock(at(30, 1), "entry").SetTerm(ir.NewReturnUnit(at(31, 1)))
	app.DeclareTestIn("a case", body, ir.TestGroup{Boot: boot, VirtualClock: true})
	app.Implement(ir.NewSymbol("Show.show"), nodeSym, shared)

	im := ir.Image{Modules: []*ir.Module{app, lib}, Entry: 0, HostKeys: []string{"b.key", "a.key"},
		HasMain: true, Root: "/project"}
	return im, map[string]any{"shared": shared, "lambda": lambda}
}

func TestImage_RoundTripKeepsEveryFieldAndIdentity(t *testing.T) {
	im, _ := imageFixture()
	data, err := ir.EncodeImage(im)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ir.DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	// Every field the encoder writes is written again from the decoded
	// graph, so equal bytes mean an equal graph, sharing included.
	again, err := ir.EncodeImage(back)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again) {
		t.Fatalf("re-encoding the decoded image changed %d bytes to %d", len(data), len(again))
	}
	if back.Entry != 0 || back.EntryModule().Name() != "app.nomi" {
		t.Fatalf("entry %d %v", back.Entry, back.EntryModule())
	}
	if strings.Join(back.HostKeys, ",") != "a.key,b.key" {
		t.Fatalf("host keys %v", back.HostKeys)
	}
	if !back.HasMain || back.Root != "/project" {
		t.Fatalf("has main %v, root %q", back.HasMain, back.Root)
	}

	app, lib := back.Modules[0], back.Modules[1]
	helper := lib.Funcs()[0]
	main := app.Funcs()[0]

	// ONE SYMBOL, TWO MODULES: the call in app names the function lib
	// declares, which is how the VM links them.
	var call *ir.Call
	var fv *ir.FuncValue
	for _, in := range main.Blocks()[0].Instrs() {
		switch n := in.(type) {
		case *ir.Defer:
			call = n.Call()
		case *ir.FuncValue:
			fv = n
		}
	}
	if call == nil || call.Callee() != helper.Sym() {
		t.Fatalf("the deferred call does not name lib's helper: %v", call)
	}
	if lib.DisplayImpls()[0].Func != helper.Sym() || app.Impls()[0].Func != helper.Sym() {
		t.Fatal("dispatch entries do not name the decoded helper")
	}

	// A recursive function value: the body names itself.
	inner := fv.Body().Blocks()[1].Instrs()[0].(*ir.FuncValue)
	if inner.Body() != fv.Body() {
		t.Fatal("the recursive function value decoded as two functions")
	}

	// The shared scalars stay shared, and the self-reaching struct stays a
	// cycle.
	if helper.TempType(helper.Params()[0].Temp) != ir.IntType {
		t.Fatal("IntType decoded as a copy")
	}
	node := helper.TempType(3)
	if node == nil || node.Kind() != ir.KindStruct {
		t.Fatalf("temp 3's type %v", node)
	}
	children := node.Layout().Fields[1].Type
	if children.Elem(0) != node {
		t.Fatal("the struct's List<Node> field no longer reaches the struct")
	}

	// The table type and its value type.
	slot := helper.Blocks()[0].Instrs()[1].(*ir.Slot)
	if slot.Type().Val() != node {
		t.Fatal("the slot's table type lost its value type")
	}

	// The cell's initializer, the test and its group, the boot.
	cell := app.Cells()[0]
	if cell.Module() != app || cell.Initializer() == nil || !cell.Initializer().Pos().Synthesized() {
		t.Fatalf("cell %v", cell)
	}
	tc := app.Tests()[0]
	if tc.Name() != "a case" || !tc.Group().VirtualClock || tc.Group().Boot != app.Boot() ||
		app.TestBoots()[0] != app.Boot() {
		t.Fatalf("test case %v", tc)
	}

	// Positions: a span and a fault edge.
	if !main.Pos().Spans() || main.Pos().EndLine() != 14 {
		t.Fatalf("main's position %v", main.Pos())
	}
	if target, ok := main.Blocks()[0].Fault(); !ok || target != 0 || main.Blocks()[0].FaultPos().Line() != 13 {
		t.Fatal("the fault edge was lost")
	}

	// Nil and empty stay apart.
	origInstrs := im.Modules[0].Funcs()[0].Blocks()[0].Instrs()
	for i, in := range main.Blocks()[0].Instrs() {
		if r, ok := in.(*ir.Render); ok {
			if (r.DebugImpls() == nil) != (origInstrs[i].(*ir.Render).DebugImpls() == nil) {
				t.Fatal("a Debug impl list changed between nil and empty")
			}
		}
	}

	// The definition table is the one the builder left: the deferred call's
	// destination has no definition, the constant's does.
	if main.Def(2) != nil || main.Def(1) == nil {
		t.Fatalf("definitions: t1=%v t2=%v", main.Def(1), main.Def(2))
	}
	// And the decoded functions print as the originals do.
	origApp := im.Modules[0].Funcs()[0]
	if dump(origApp) != dump(main) {
		t.Fatalf("main prints differently:\n%s\n---\n%s", dump(origApp), dump(main))
	}
}

func dump(f *ir.Func) string {
	var b strings.Builder
	for _, bl := range f.Blocks() {
		b.WriteString(bl.ID().String() + " " + bl.Pos().String() + "\n")
		for _, in := range bl.Instrs() {
			b.WriteString("  " + in.String() + " @" + in.Pos().String() + "\n")
		}
		if bl.Term() != nil {
			b.WriteString("  " + bl.Term().String() + "\n")
		}
	}
	return b.String()
}

func TestImage_EncodingIsDeterministic(t *testing.T) {
	a, _ := imageFixture()
	first, err := ir.EncodeImage(a)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := ir.EncodeImage(a)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Fatal("encoding one image twice gave different bytes")
		}
	}
	// A second, independently built copy of the same graph gives the same
	// bytes: identity is written as numbering, never as an address.
	b, _ := imageFixture()
	other, err := ir.EncodeImage(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, other) {
		t.Fatal("two builds of one graph encode differently")
	}
}

func TestImage_DecodeRejectsDamage(t *testing.T) {
	im, _ := imageFixture()
	data, err := ir.EncodeImage(im)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"empty":     nil,
		"magic":     append([]byte("NOMIXX\x00"), data[7:]...),
		"truncated": data[:len(data)-3],
		"flipped":   flip(data, len(data)-10),
		"version":   append(append([]byte(nil), data[:7]...), append([]byte{ir.FormatVersion + 1}, data[8:]...)...),
	}
	for name, bad := range cases {
		if _, err := ir.DecodeImage(bad); err == nil {
			t.Errorf("%s: decoded without an error", name)
		}
	}
}

func flip(data []byte, i int) []byte {
	out := append([]byte(nil), data...)
	out[i] ^= 0xff
	return out
}
