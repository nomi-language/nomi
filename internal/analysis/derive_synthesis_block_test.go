package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// countSynthDebugImpls counts auto-synthesized `impl Debug for T { ... }` blocks
// (those in the synth line band) targeting the given receiver type name.
func countSynthDebugImpls(nodes []ast.Node, recv string) int {
	n := 0
	for _, node := range nodes {
		blk, ok := node.(*ast.ImplBlock)
		if !ok || blk.Line < synthLineBase || blk.Interface == nil {
			continue
		}
		if TypeExprBaseName(blk.Interface) == "Debug" && TypeExprBaseName(blk.Receiver) == recv {
			n++
		}
	}
	return n
}

// A hand-written BLOCK-form `impl Debug for T { ... }` must suppress
// universal-Debug auto-synthesis. Otherwise a second, structural Debug is
// synthesized; for an enum, that body references unbound payload vars
// (`v0`) and traps at runtime.
func TestUniversalDebug_BlockImplSuppressesSynth(t *testing.T) {
	src := `struct E { x: Int }

impl Debug for E {
  fn inspect(_e: E): String {
    "E"
  }
}`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	// Lower the `impl Debug for E` block into the top-level `impl Debug for E`
	// block that universal-Debug synthesis scans to suppress auto-synthesis.
	nodes, _ = LowerDerives(nodes)
	out := SynthesizeUniversalDebug(nodes)
	if got := countSynthDebugImpls(out, "E"); got != 0 {
		t.Fatalf("expected no synthesized Debug for E (explicit block impl present), got %d", got)
	}
}
