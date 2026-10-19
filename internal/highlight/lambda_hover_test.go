package highlight_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
)

// The tour answers a lambda's `fn` and `->`, and a declaration's `fn`, as
// the editor does.
func TestAnalyze_FnHovers(t *testing.T) {
	const src = `import std/io

/// Doubles n.
fn double(n: Int) -> Int {
  n * 2
}

fn main() {
  m = Maybe.map(Some(1), fn n -> [n])
  total = Iter.reduce([1, 2], fn total = 0, x -> total + x)
  k = 3
  |> then fn v -> v + 1
  f = fn -> 42
  io.inspect(#(m, total, k, f(), double(2)))
}
`
	lib := std.Load()
	fa, nodes := highlight.AnalyzeNodes(src, lib)
	if fa == nil {
		t.Fatal("highlight.AnalyzeNodes returned nil")
	}
	editor, editorNodes := editorDoc(t, lib, src)
	mapped := "```nomi\nfn n: Int -> List<Int>\n```\n\nFills `Maybe.map`'s `f: (T) -> U`, which gives its parameter types."
	cases := []struct {
		name string
		pos  analysis.Pos
		want string
	}{
		{"a declaration's fn", analysis.Pos{Line: 4, Col: 1}, "```nomi\nfn double(n: Int) -> Int\n```\n\nDoubles n."},
		{"a lambda argument's fn", analysis.Pos{Line: 9, Col: 26}, mapped},
		{"its ->", analysis.Pos{Line: 9, Col: 31}, mapped},
		{"a seeded parameter", analysis.Pos{Line: 10, Col: 31}, "```nomi\nfn total: Int = 0, x: Int -> Int\n```\n\nFills `Iter.reduce`'s `f: (U, T) -> U`, which gives its parameter types."},
		{"a then stage's lambda", analysis.Pos{Line: 12, Col: 11}, "```nomi\nfn v: Int -> Int\n```\n\nThe `then` stage's lambda; its parameter is the piped value."},
		{"zero parameters", analysis.Pos{Line: 13, Col: 7}, "```nomi\nfn -> Int\n```"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for offset := 0; offset < 2; offset++ {
				pos := analysis.Pos{Line: c.pos.Line, Col: c.pos.Col + offset}
				if got := hoverdoc.At(fa, nodes, pos); got != c.want {
					t.Errorf("tour hover on character %d = %q, want %q", offset+1, got, c.want)
				}
				if got, _, _, _ := hoverdoc.SyntaxAt(editor, editorNodes, pos); got != c.want {
					t.Errorf("editor hover on character %d = %q, want %q", offset+1, got, c.want)
				}
			}
		})
	}
}
