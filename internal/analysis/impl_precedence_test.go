package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// The blocks a call at a type reaches are those of the best rank: a written
// impl over a derived one over the universal Debug every type's file holds.
// A REPL input that derives Debug for an earlier input's type leaves the
// type with a derived block and its file's universal one, and a call at the
// type, written there or at an instance of a generic body, names the derived
// one (TestReplVM_ALaterDeriveReplacesTheTypesImpl in cmd/nomi runs it).
func TestImplPrecedence_WrittenOverDerivedOverUniversal(t *testing.T) {
	block := func(line int, auto bool) *ImplTypeArgs {
		return &ImplTypeArgs{Block: &ast.ImplBlock{Line: line, Items: []ast.Node{&ast.FuncDef{Name: "inspect", Line: line, AutoSynth: auto}}}}
	}
	written := block(3, false)
	derived := block(synthLineBase+40, false)
	universal := block(synthLineBase+80, true)
	for _, c := range []struct {
		name   string
		blocks []*ImplTypeArgs
		want   *ImplTypeArgs
	}{
		{"derived over universal", []*ImplTypeArgs{universal, derived}, derived},
		{"written over universal", []*ImplTypeArgs{universal, written}, written},
		{"written over derived", []*ImplTypeArgs{derived, written}, written},
		{"written over both", []*ImplTypeArgs{universal, derived, written}, written},
	} {
		got := BestImplPrecedence(c.blocks, implTypeArgsBlock)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: %d blocks remain, want the one at line %d", c.name, len(got), c.want.Block.Line)
		}
	}
	if got := BestImplPrecedence([]*ImplTypeArgs{universal}, implTypeArgsBlock); len(got) != 1 || got[0] != universal {
		t.Errorf("a type with only the universal Debug keeps it, got %d blocks", len(got))
	}
}
