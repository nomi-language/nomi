package ir_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// linkFixture is a library module defining helper and an app module calling
// it through callee: helper's symbol when they share one, else another
// symbol with callee's name and linkage name.
func linkFixture(helper, callee *ir.Symbol, value int64) (lib, app *ir.Module) {
	at := func(line int) ir.Pos { return ir.At("link.nomi", line, 1) }
	lib = ir.NewModule("lib.nomi")
	h := ir.NewFuncFor(at(1), helper)
	hb := h.NewBlock(at(1), "entry")
	v := h.NewTemp()
	hb.Append(ir.NewInt(at(2), v, value))
	h.SetType(v, ir.IntType)
	hb.SetTerm(ir.NewReturn(at(3), v))
	lib.AddFunc(h)

	app = ir.NewModule("app.nomi")
	m := ir.NewFuncFor(at(10), ir.NewLinkedSymbol("main", "app.main"))
	mb := m.NewBlock(at(10), "entry")
	got := m.NewTemp()
	mb.Append(ir.NewCall(at(11), got, ir.OrdinaryCall, callee))
	m.SetType(got, ir.IntType)
	mb.SetTerm(ir.NewReturnUnit(at(12)))
	app.AddFunc(m)
	return lib, app
}

func calleeOf(t *testing.T, m *ir.Module) *ir.Symbol {
	t.Helper()
	for _, b := range m.Funcs()[0].Blocks() {
		for _, in := range b.Instrs() {
			if c, ok := in.(*ir.Call); ok {
				return c.Callee()
			}
		}
	}
	t.Fatal("no call")
	return nil
}

func TestLink_ObjectsEncodedApartShareTheirDeclarations(t *testing.T) {
	helper := ir.NewLinkedSymbol("helper", "lib.helper")
	lib, app := linkFixture(helper, helper, 7)
	if err := ir.CheckLinkage([]*ir.Module{lib, app}, nil); err != nil {
		t.Fatal(err)
	}
	l := ir.NewLinker()
	var mods []*ir.Module
	for _, m := range []*ir.Module{app, lib} {
		data, err := ir.EncodeObject(m)
		if err != nil {
			t.Fatal(err)
		}
		im, err := l.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		mods = append(mods, im.Modules...)
	}
	call := calleeOf(t, mods[0])
	def := mods[1].Funcs()[0].Sym()
	if call != def {
		t.Fatalf("the app's call names %p and the library defines %p: linking did not make them one", call, def)
	}
	if call == helper || call.Link() != "lib.helper" {
		t.Fatalf("decoded symbol %p %q; want a fresh symbol linked as lib.helper", call, call.Link())
	}
	// Without a linker each object decodes its own copy.
	a, _ := ir.EncodeObject(app)
	b, _ := ir.EncodeObject(lib)
	ma, _ := ir.DecodeModules(a)
	mb, _ := ir.DecodeModules(b)
	if calleeOf(t, ma[0]) == mb[0].Funcs()[0].Sym() {
		t.Fatal("two objects decoded without a linker share a symbol")
	}
}

func TestLink_ASymbolTwoModulesNameNeedsALinkageName(t *testing.T) {
	helper := ir.NewSymbol("helper")
	lib, app := linkFixture(helper, helper, 7)
	err := ir.CheckLinkage([]*ir.Module{lib, app}, nil)
	if err == nil || !strings.Contains(err.Error(), "helper is named by lib.nomi and app.nomi and has no linkage name") {
		t.Fatalf("CheckLinkage = %v; want the unlinked helper reported", err)
	}
}

func TestLink_TwoDeclarationsMayNotShareALinkageName(t *testing.T) {
	lib, _ := linkFixture(ir.NewLinkedSymbol("helper", "lib.helper"), ir.NewSymbol("x"), 7)
	other, _ := linkFixture(ir.NewLinkedSymbol("helper", "lib.helper"), ir.NewSymbol("y"), 8)
	err := ir.CheckLinkage([]*ir.Module{lib, other}, nil)
	if err == nil || !strings.Contains(err.Error(), `two symbols have the linkage name "lib.helper"`) {
		t.Fatalf("CheckLinkage = %v; want the shared name reported", err)
	}
}

func TestLink_TheSameCodeDefinedTwiceIsLinkedOnce(t *testing.T) {
	// Two lowerings' instances of one template: two symbols, one name, the
	// same body.
	lib, _ := linkFixture(ir.NewLinkedSymbol("helper", "lib.helper"), ir.NewSymbol("x"), 7)
	again, _ := linkFixture(ir.NewLinkedSymbol("helper", "lib.helper"), ir.NewSymbol("y"), 7)
	if err := ir.CheckLinkage([]*ir.Module{lib, again}, nil); err != nil {
		t.Fatalf("CheckLinkage = %v; two copies of one body link once", err)
	}
	l := ir.NewLinker()
	var kept int
	for _, m := range []*ir.Module{lib, again} {
		data, err := ir.EncodeObject(m)
		if err != nil {
			t.Fatal(err)
		}
		im, err := l.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		kept += len(im.Modules[0].Funcs())
	}
	if kept != 1 {
		t.Fatalf("%d definitions of lib.helper kept; the linker keeps the first", kept)
	}
}
