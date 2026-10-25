package highlight_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
)

// The tour answers a discard `_` as the editor does: the type it discards,
// and what it matches where a match may fail.
func TestAnalyze_DiscardHovers(t *testing.T) {
	const src = `import std/io

enum Coin {
  Penny
  Dime
}

fn count() -> Int {
  3
}

fn second(_: String, b: Int) -> Int {
  b
}

fn main() {
  #(_, coins) = #(1, [Coin.Penny])
  _ = count()
  a = case Some("a") {
    Some(_) -> 1
    None -> 0
  }
  c = case Coin.Dime {
    .Penny -> 1
    _ -> 2
  }
  n = Iter.reduce(["a"], fn total = 0, _ -> total + 1)
  r = Iter.reduce([1, 2], fn #(_, acc) = #(0, []), x -> #(x, [x, ..acc]))
  d = case {
    c > 1 -> 1
    _ -> 2
  }
  io.inspect(#(coins, a, n, r, d, second("a", 2)))
}
`
	lib := std.Load()
	fa, nodes := highlight.AnalyzeNodes(src, lib)
	if fa == nil {
		t.Fatal("highlight.AnalyzeNodes returned nil")
	}
	editor, editorNodes := editorDoc(t, lib, src)
	cases := []struct {
		name string
		// line starts the line that holds the `_`, and nth is its
		// occurrence on the line (from 1).
		line string
		nth  int
		want string
	}{
		{"a function parameter", "fn second(", 1, "`String`, discarded"},
		{"a tuple element", "  #(_, coins)", 1, "`Int`, discarded"},
		{"a binding", "  _ = count()", 1, "`Int`, discarded"},
		{"a variant payload in a case arm", "    Some(_)", 1, "`String`, discarded: matches any `String`"},
		{"a case arm's whole pattern", "    _ -> 2", 1, "`Coin`, discarded: matches any `Coin`"},
		{"a lambda parameter", "  n = Iter.reduce", 1, "`String`, discarded"},
		{"a reduce seed pattern", "  r = Iter.reduce", 1, "`Int`, discarded"},
	}
	lines := strings.Split(src, "\n")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := -1
			for i, l := range lines {
				if strings.HasPrefix(l, c.line) {
					line = i
					break
				}
			}
			if line < 0 {
				t.Fatalf("no line starts with %q", c.line)
			}
			col := -1
			for i := 0; i < c.nth; i++ {
				at := strings.Index(lines[line][col+1:], "_")
				if at < 0 {
					t.Fatalf("line %q has no `_` number %d", lines[line], c.nth)
				}
				col += at + 1
			}
			pos := analysis.Pos{Line: line + 1, Col: col + 1}
			if got := hoverdoc.At(fa, nodes, pos); got != c.want {
				t.Errorf("tour hover = %q, want %q", got, c.want)
			}
			if got, _, _, _ := hoverdoc.SyntaxAt(editor, editorNodes, pos); got != c.want {
				t.Errorf("editor hover = %q, want %q", got, c.want)
			}
		})
	}
	// The last `_ ->` is the arm of a `case` with no subject.
	last := strings.LastIndex(src, "    _ -> 2")
	pos := analysis.Pos{Line: strings.Count(src[:last], "\n") + 1, Col: 5}
	const otherwise = "Otherwise: runs when no condition above holds."
	if got := hoverdoc.At(fa, nodes, pos); got != otherwise {
		t.Errorf("tour hover on the subjectless case's `_` = %q, want %q", got, otherwise)
	}
	if got, _, _, _ := hoverdoc.SyntaxAt(editor, editorNodes, pos); got != otherwise {
		t.Errorf("editor hover on the subjectless case's `_` = %q, want %q", got, otherwise)
	}
}
