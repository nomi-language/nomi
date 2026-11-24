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

// An impl block's function keeps its block through an object and through
// linking, and is named by it, whatever name a later intern offers; a token
// interned plain and then as an impl function panics.
func TestLink_AnImplFunctionKeepsItsBlock(t *testing.T) {
	tbl := ir.NewTable()
	tbl.SetLinkage(func(any, string) string { return "lib.impl Display for Loud.to_string" }, nil)
	token := new(int)
	loud := ir.ImplFunc{Type: "Loud", Interface: "Display", Func: "to_string"}
	helper := tbl.ImplSymbol(token, loud)
	if helper.Name() != "Display.to_string for Loud" || tbl.ImplSymbol(token, loud) != helper || tbl.Symbol(token, "Loud.to_string") != helper {
		t.Fatalf("the impl function is %q, interned again %p and %p; want one symbol named by its block",
			helper.Name(), tbl.ImplSymbol(token, loud), tbl.Symbol(token, "Loud.to_string"))
	}
	lib, app := linkFixture(helper, helper, 7)
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
	for _, s := range []*ir.Symbol{calleeOf(t, mods[0]), mods[1].Funcs()[0].Sym()} {
		if got, ok := s.Impl(); !ok || got != loud || s.Name() != loud.Name() {
			t.Errorf("decoded %q carries %+v, %v; want %+v", s.Name(), got, ok, loud)
		}
	}
	plain := new(int)
	tbl.Symbol(plain, "to_string")
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "to_string was interned as a plain symbol and is now Display.to_string for Loud") {
			t.Errorf("interning a plain symbol's token as an impl function: recovered %v", r)
		}
	}()
	tbl.ImplSymbol(plain, loud)
}

// A dispatch symbol no object defines (a std interface's method) is one
// symbol across the objects that name it: the one whose call dispatches on
// it and the ones that record an entry for it. Each object's entries stay its
// module's. In one lowering it is one symbol already, as CheckLinkage
// requires of a name no module defines; decoded apart, each object holds its
// own copy until the linker makes them one.
func TestLink_ASymbolNoObjectDefinesIsOneAcrossObjects(t *testing.T) {
	at := func(line int) ir.Pos { return ir.At("link.nomi", line, 1) }
	hash := ir.NewLinkedSymbol("Hashable.hash", "std/hashable.Hashable.hash")
	method := func() *ir.Symbol { return hash }
	caller := ir.NewModule("caller.nomi")
	f := ir.NewFuncFor(at(1), ir.NewLinkedSymbol("hash_of", "caller.hash_of"))
	recv := f.AddParam(ir.NewSymbol("value"), ir.ValUnknown)
	fb := f.NewBlock(at(1), "entry")
	got := f.NewTemp()
	fb.Append(ir.NewDispatchCall(at(2), got, ir.OrdinaryCall, method(), 0, recv))
	f.SetType(got, ir.IntType)
	fb.SetTerm(ir.NewReturn(at(3), got))
	caller.AddFunc(f)
	impl := func(name, typ string, value int64) *ir.Module {
		m := ir.NewModule(name)
		body := ir.NewFuncFor(at(10), ir.NewSymbol(typ+".hash"))
		body.AddParam(ir.NewSymbol("value"), ir.ValUnknown)
		b := body.NewBlock(at(10), "entry")
		v := body.NewTemp()
		b.Append(ir.NewInt(at(11), v, value))
		body.SetType(v, ir.IntType)
		b.SetTerm(ir.NewReturn(at(12), v))
		m.AddFunc(body)
		m.Implement(method(), ir.NewSymbol(typ), body.Sym())
		return m
	}
	mods := []*ir.Module{caller, impl("a.nomi", "A", 1), impl("b.nomi", "B", 2)}
	if err := ir.CheckLinkage(mods, nil); err != nil {
		t.Fatal(err)
	}
	l := ir.NewLinker()
	var linked []*ir.Module
	for _, m := range mods {
		data, err := ir.EncodeObject(m)
		if err != nil {
			t.Fatal(err)
		}
		im, err := l.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		linked = append(linked, im.Modules...)
	}
	call := calleeOf(t, linked[0])
	for _, m := range linked[1:] {
		impls := m.Impls()
		if len(impls) != 1 || impls[0].Method != call {
			t.Fatalf("%s records %+v; want one entry on the caller's dispatch symbol %p", m.Name(), impls, call)
		}
	}
	if call == hash || call.Link() != "std/hashable.Hashable.hash" || l.Symbol(ir.NewLinkedSymbol("Hashable.hash", "std/hashable.Hashable.hash")) != call {
		t.Fatalf("the dispatch symbol is %p %q; want a decoded symbol the linker answers for its name", call, call.Link())
	}
	// A second symbol of the name in one lowering is two symbols CheckLinkage
	// refuses: neither is a definition the linker could keep one of.
	other := impl("c.nomi", "C", 3)
	other.Impls()[0].Method = ir.NewLinkedSymbol("Hashable.hash", "std/hashable.Hashable.hash")
	if err := ir.CheckLinkage([]*ir.Module{caller, other}, nil); err == nil || !strings.Contains(err.Error(), `two symbols have the linkage name "std/hashable.Hashable.hash"`) {
		t.Fatalf("CheckLinkage = %v; want the second dispatch symbol reported", err)
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

func TestLink_ALinkerOverABaseLinksAgainstItAndLeavesItAsItWas(t *testing.T) {
	lib, app := linkFixture(ir.NewLinkedSymbol("helper", "lib.helper"), ir.NewLinkedSymbol("helper", "lib.helper"), 7)
	encode := func(m *ir.Module) []byte {
		data, err := ir.EncodeObject(m)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	base := ir.NewLinker()
	libIm, err := base.Decode(encode(lib))
	if err != nil {
		t.Fatal(err)
	}
	baseHelper := libIm.Modules[0].Funcs()[0].Sym()
	// Two linkers over one base each link the app to the base's helper, and
	// drop a copy of helper as the base's.
	for i := range 2 {
		l := ir.NewLinkerOver(base)
		appIm, err := l.Decode(encode(app))
		if err != nil {
			t.Fatal(err)
		}
		if got := calleeOf(t, appIm.Modules[0]); got != baseHelper {
			t.Fatalf("linker %d: the app calls %p, not the base's helper %p", i, got, baseHelper)
		}
		again, err := l.Decode(encode(lib))
		if err != nil {
			t.Fatal(err)
		}
		if n := len(again.Modules[0].Funcs()); n != 0 {
			t.Fatalf("linker %d: kept %d copies of helper, which the base defines", i, n)
		}
	}
	// A name a linker over the base links is that linker's alone.
	other := ir.NewLinkedSymbol("other", "app.other")
	_, app2 := linkFixture(ir.NewLinkedSymbol("unused", "lib.unused"), other, 7)
	if _, err := ir.NewLinkerOver(base).Decode(encode(app2)); err != nil {
		t.Fatal(err)
	}
	if s := ir.NewLinkerOver(base).Symbol(other); s != other {
		t.Fatal("the base learned a name a linker over it linked")
	}
}
