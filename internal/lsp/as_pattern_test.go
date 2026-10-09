package lsp

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// asPatternSource binds `t` with `as` in a case arm: line 12, column 27 is
// the name, and line 12, column 33 its first use.
const asPatternSource = `struct Tx {
  start: Int
}

enum Outcome {
  Done Tx
  Failed
}

fn start_of(r: Outcome): Int {
  case r {
    .Done(Tx{start: _} as t) -> t.start
    .Failed -> 0
  }
}
`

func TestAsPattern_HoverOnTheNameShowsTheWholeValuesType(t *testing.T) {
	file := checkedFile(asPatternSource)
	for _, pos := range []analysis.Pos{{Line: 12, Col: 27}, {Line: 12, Col: 33}} {
		sym := file.SymbolAt(pos)
		if sym == nil {
			t.Fatalf("no symbol at %v", pos)
		}
		if got, want := renderHover(sym), "```nomi\nt: Tx\n\nstruct Tx {\n    start: Int\n}\n```"; got != want {
			t.Errorf("hover at %v:\ngot  %q\nwant %q", pos, got, want)
		}
	}
}

func TestAsPattern_DefinitionOfAUseIsTheName(t *testing.T) {
	file := checkedFile(asPatternSource)
	sym := file.SymbolAt(analysis.Pos{Line: 12, Col: 33})
	if sym == nil {
		t.Fatal("no symbol at the use of t")
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym.Pos != (analysis.Pos{Line: 12, Col: 27}) || sym.Kind != analysis.SymbolBinding {
		t.Errorf("definition: got %s at %v (kind %v), want the binding t at 12:27", sym.Name, sym.Pos, sym.Kind)
	}
}

func TestAsPattern_RenameEditsTheNameAndItsUse(t *testing.T) {
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(asPatternSource))
	fa := buildFile(nodes)
	sym := fa.SymbolAt(analysis.Pos{Line: 12, Col: 33})
	if sym == nil {
		t.Fatal("no symbol at the use of t")
	}
	target := sym
	if target.Resolved != nil {
		target = target.Resolved
	}
	edits := buildRenameEdits(fa, target, symbolIdentity(sym), "tx")
	if len(edits) != 2 {
		t.Fatalf("got %d edits, want 2 (the name and its use): %+v", len(edits), edits)
	}
	for _, e := range edits {
		if e.Range.Start.Line != 11 || e.Range.End.Character-e.Range.Start.Character != 1 {
			t.Errorf("edit %+v: want a one-character range on line 12", e.Range)
		}
	}
}

func TestAsPattern_SemanticTokensMarkTheNameAVariable(t *testing.T) {
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(asPatternSource))
	fa := buildFile(nodes)
	data := encodeSemanticTokens(fa, nodes)
	line, col := 1, 1
	for i := 0; i+4 < len(data); i += 5 {
		line += int(data[i])
		if data[i] > 0 {
			col = int(data[i+1]) + 1
		} else {
			col += int(data[i+1])
		}
		if line == 12 && col == 27 {
			if data[i+2] != 1 || tokenTypes[data[i+3]] != "variable" {
				t.Errorf("token at 12:27: length %d, type %s; want length 1, variable", data[i+2], tokenTypes[data[i+3]])
			}
			return
		}
	}
	t.Error("no semantic token at the `as` name")
}
