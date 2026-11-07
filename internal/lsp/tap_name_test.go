package lsp

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

const tapNameSrc = `fn log_it(n: Int) {
  log_it_too(n)
}

fn log_it_too(n: Int) {}

fn main() {
  n = 3 |> tap log_it
  log_it(n)
}
`

// The function a `tap` stage names resolves to its declaration and hovers
// with its signature, as a call's callee does.
func TestTapName_HoverAndDefinition(t *testing.T) {
	lib := loadStd()
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(tapNameSrc))
	file := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	typeErrs := analysis.BuildTypes(file, nodes)
	checkErrs := analysis.CheckTypes(file, nodes)
	if len(typeErrs)+len(checkErrs) > 0 {
		t.Fatalf("unexpected type errors: %v %v", typeErrs, checkErrs)
	}
	use := posOf(t, tapNameSrc, "|> tap log_it", "log_it", 0)
	def := posOf(t, tapNameSrc, "fn log_it(n", "log_it", 0)
	sym := file.SymbolAt(use)
	if sym == nil {
		t.Fatalf("no symbol at %v", use)
	}
	if sym.Pos != def {
		t.Errorf("resolves to %v, want the definition at %v", sym.Pos, def)
	}
	if got, want := renderHover(sym), "```nomi\nfn log_it(n: Int) -> Unit\n```"; got != want {
		t.Errorf("hover %q, want %q", got, want)
	}
}
