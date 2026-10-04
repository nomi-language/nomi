package ir

import "testing"

func TestLazyCellInitializerParticipatesInModuleLint(t *testing.T) {
	for _, valid := range []bool{false, true} {
		at := At("lazy.nomi", 1, 1)
		mod := NewModule("lazy.nomi")
		fn := NewFunc(at, "initializer")
		b := fn.NewBlock(at, "entry")
		n := NewInt(at, fn.NewTemp(), 42)
		if valid {
			b.Append(n)
		}
		b.SetTerm(NewReturn(at, n.Dst()))
		ty, _ := NewTable().Concrete("Int", "Int")
		valued(ty, IntType)
		cell := mod.DeclareLazyCell(NewSymbol("answer"), ty, fn)
		if cell.Initializer() != fn || len(mod.Funcs()) != 0 {
			t.Fatal("initializer ownership changed")
		}
		if err := LintModule(mod); (err == nil) != valid {
			t.Fatalf("valid=%v: %v", valid, err)
		}
	}
}
