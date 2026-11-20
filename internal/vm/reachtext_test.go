package vm

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// A chain step that is an impl block's function is worded from its block
// (ChainStep.Impl), never from the text of its name: the method a dispatch
// calls and the type a rendering holds.
func TestReachText_AnImplStepIsWordedFromItsBlock(t *testing.T) {
	loud := ir.ImplFunc{Type: "Box<Int>", Interface: "Display", Func: "to_string"}
	root := ChainStep{Func: "main", Edge: EdgeCall}
	for _, tc := range []struct {
		edge EdgeKind
		want string
	}{
		{EdgeDispatch, "reached from `main`; `main` calls `to_string` through an interface, and any implementation of it counts, including `Display.to_string for Box<Int>`"},
		{EdgeDisplay, "reached from `main`; `main` displays a value that may be or hold a `Box<Int>`, so `impl Display for Box<Int>` counts"},
		{EdgeCall, "reached from `main`; `main` calls `Display.to_string for Box<Int>`"},
	} {
		step := ChainStep{Func: loud.Name(), Edge: tc.edge, Impl: loud}
		if got := ReachText([]ChainStep{root, step}, EdgeCall, ""); got != tc.want {
			t.Errorf("edge %s:\n got %s\nwant %s", tc.edge, got, tc.want)
		}
	}
}
