package ir

// `Store` and `Module`, checked at the level internal/ir owns: what the
// constructors refuse, and what the nodes answer. The routing is
// internal/irbuild's; this is the model.

import (
	"strings"
	"testing"
)

func storeFile() string { return "store.nomi" }

// TestStore_WritesADeclarationAndNotATemporary is the property that makes
// `Store` a different node from `Copy` rather than a spelling of it.
//
// `internal/irbuild/irbind.go`'s table calls `ir.Copy` the rebind — `n = v`
// writes a name that exists — and that is true about the Go spelling and false
// about the representation: the builder reaches it through
// `irName` -> `irHold(atom(goName, k))`, so the Copy's
// DESTINATION is a temporary holding the destination's Go NAME. A Store names
// the declaration.
func TestStore_WritesADeclarationAndNotATemporary(t *testing.T) {
	tbl := NewTable()
	field := tbl.Symbol("ctxField", "EffectsApp.context")
	src := Temp(3)
	s := NewStoreAppField(At(storeFile(), 12, 3), field, src)

	if s.Dst() != NoTemp {
		t.Errorf("a Store writes temporary %s; its effect is on a declaration and a Dst "+
			"carrying the stored value would make one field mean two things", s.Dst())
	}
	if s.Sym() != field {
		t.Error("the Store does not name the declaration it was given")
	}
	if s.Src() != src {
		t.Errorf("Src is %s, not %s", s.Src(), src)
	}
	if got := s.AppendUses(nil); len(got) != 1 || got[0] != src {
		t.Errorf("AppendUses answered %v; a Store reads exactly its source", got)
	}
	if s.Kind() != StoreAppField {
		t.Errorf("kind is %v", s.Kind())
	}
	if got := s.String(); got != "store app field EffectsApp.context = t3" {
		t.Errorf("String is %q", got)
	}

	// A SECOND READ OF ONE DECLARATION IS ONE SYMBOL, so a write and a read of
	// the same app field compare equal in the representation. That is the
	// whole reason the target is interned rather than minted, and it is what a
	// consumer asking "does this scope restore what it overrode" needs.
	read := NewRefAppField(At(storeFile(), 12, 3), Temp(4), tbl.Symbol("ctxField", "whatever"))
	if read.Sym() != s.Sym() {
		t.Error("the read and the write of one app field do not name one declaration")
	}

}

func TestStore_TheConstructorRefusesWhatOneCallCanBeWrongAbout(t *testing.T) {
	tbl := NewTable()
	sym := tbl.Symbol("ctxField", "EffectsApp.context")
	for _, tc := range []struct {
		name string
		want string
		call func()
	}{
		{"no position", "needs a position", func() {
			NewStoreAppField(Pos{}, sym, Temp(1))
		}},
		{"no file", "needs the file", func() {
			NewStoreAppField(At("", 1, 1), sym, Temp(1))
		}},
		{"no target", "needs the declaration's identity", func() {
			NewStoreAppField(At(storeFile(), 1, 1), nil, Temp(1))
		}},
		{"an unnamed target", "empty target name", func() {
			NewStoreAppField(At(storeFile(), 1, 1), NewSymbol(""), Temp(1))
		}},
		{"no source", "no value to store", func() {
			NewStoreAppField(At(storeFile(), 1, 1), sym, NoTemp)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("accepted a store with %s", tc.name)
				}
				if msg, _ := r.(string); !strings.Contains(msg, tc.want) {
					t.Errorf("the panic is about something else: %v", r)
				}
			}()
			tc.call()
		})
	}

	// THE CONTROL. Without it every row above would read the same on a
	// constructor that rejected everything.
	if s := NewStoreAppField(At(storeFile(), 1, 1), sym, Temp(1)); s == nil {
		t.Fatal("a well-formed store was refused")
	}
}

// TestStore_GoesIntoABlockAndDefinesNoTemporary checks the def table is not
// disturbed by an instruction that writes nothing, because `Func.noteDef`
// skipping NoTemp is what lets `Store` be an ordinary `Instr`.
func TestStore_GoesIntoABlockAndDefinesNoTemporary(t *testing.T) {
	tbl := NewTable()
	f := NewFunc(At(storeFile(), 1, 1), "install")
	b := f.NewBlock(At(storeFile(), 1, 1), "entry")
	v := NewInt(At(storeFile(), 2, 1), f.NewTemp(), 7)
	b.Append(v)
	b.Append(NewStoreAppField(At(storeFile(), 2, 1),
		tbl.Symbol("portField", "Cfg.port"), v.Dst()))
	b.SetTerm(NewReturnUnit(At(storeFile(), 2, 1)))

	if err := Lint(f); err != nil {
		t.Fatalf("a function whose body is one store is well formed and Lint rejected it:\n%v", err)
	}
	if f.Def(v.Dst()) != v {
		t.Error("the store displaced the definition of the temporary it reads")
	}

	// AND THE STORE'S SOURCE IS SUBJECT TO RULE 2, which is the reason a
	// Store's AppendUses is its source rather than nothing: a store of an
	// undefined temporary is a producer bug the def-use rule already knows how
	// to report, and this is the plant for it reaching a Store.
	f2 := NewFunc(At(storeFile(), 1, 1), "install")
	b2 := f2.NewBlock(At(storeFile(), 1, 1), "entry")
	b2.Append(NewStoreAppField(At(storeFile(), 2, 1),
		tbl.Symbol("portField", "Cfg.port"), Temp(9)))
	b2.SetTerm(NewReturnUnit(At(storeFile(), 2, 1)))
	got := violationsFor(t, f2, RuleTempDefinedBeforeUse)
	if len(got) != 1 {
		t.Fatalf("a store of an undefined temporary drew %d violations: %v", len(got), got)
	}
}

// TestModule_HoldsDeclarationsAndCarriesNoPosition is the container's shape,
// including the two absences it argues for.
func TestModule_HoldsDeclarationsAndCarriesNoPosition(t *testing.T) {
	m := wellFormedModule()
	if m.Name() != lintFile() {
		t.Errorf("Name is %q", m.Name())
	}
	if got := m.String(); !strings.HasPrefix(got, "module ") {
		t.Errorf("String is %q", got)
	}

	// A CELL IS REACHABLE BY ITS DECLARATION IDENTITY, which is what a
	// declaration write's target has to be checkable against.
	first := m.Cells()[0]
	if m.Cell(first.Sym()) != first {
		t.Error("the module cannot find the cell it declared")
	}
	if m.Cell(NewSymbol("appCell_Clock")) != nil {
		t.Error("a freshly minted symbol with the same NAME found a cell, so the container " +
			"keys on the printed name")
	}
	if first.Module() != m || first.Type() == nil {
		t.Error("a cell does not answer for its module or its type")
	}

	// NO POSITION, AND IT IS NOT A NODE. The compile-time half of that is the
	// absence of a `Pos()` method, which cannot be asserted at run time; what
	// can be is that `Module` does not satisfy `Node`.
	var n any = m
	if _, isNode := n.(Node); isNode {
		t.Error("Module satisfies Node, so something gave it a position — a module is a SET " +
			"OF FILES and the only position available is `At(file, 1, 1)` standing in for a " +
			"compilation unit, and a fabricated position is never recorded")
	}
}

func TestModule_TheConstructorRefusesWhatOneCallCanBeWrongAbout(t *testing.T) {
	tbl := NewTable()
	ty, _ := tbl.Concrete("clockDecl", "Clock")
	for _, tc := range []struct {
		name string
		want string
		call func()
	}{
		{"an unnamed module", "no name", func() { NewModule("") }},
		{"a nil function", "AddFunc(nil)", func() { NewModule(lintFile()).AddFunc(nil) }},
		{"storage with no identity", "needs a declaration identity", func() {
			NewModule(lintFile()).DeclareCell(nil, ty)
		}},
		{"storage with an empty name", "empty name", func() {
			NewModule(lintFile()).DeclareCell(NewSymbol(""), ty)
		}},
		{"storage with no type", "cannot size", func() {
			NewModule(lintFile()).DeclareCell(tbl.Symbol("clockDecl", "appCell_Clock"), nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("accepted %s", tc.name)
				}
				if msg, _ := r.(string); !strings.Contains(msg, tc.want) {
					t.Errorf("the panic is about something else: %v", r)
				}
			}()
			tc.call()
		})
	}

	// THE CONTROL, and it is also the line this type draws: a DUPLICATE is
	// accepted here and reported by Lint, because it is a fact about the set
	// rather than about one call. See Module.AddFunc.
	m := NewModule(lintFile())
	sym := tbl.Symbol("clockDecl", "appCell_Clock")
	m.DeclareCell(sym, ty)
	m.DeclareCell(sym, ty)
	if len(m.Cells()) != 2 {
		t.Fatalf("the duplicate was silently collapsed to %d cell(s), which is a wrong "+
			"answer where the slice gives a reported one", len(m.Cells()))
	}
	if LintModule(m) == nil {
		t.Fatal("the duplicate was accepted and nothing reported it")
	}
}
