package highlight_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
)

// The tour answers a lambda's `fn` and `->`, and a declaration's `fn`, as
// the editor does.
func TestAnalyze_FnHovers(t *testing.T) {
	const src = `import std::io

/// Doubles n.
fn double(n: Int) -> Int
  n * 2
end

fn main()
  m = Maybe.map(Some(1), fn n -> [n])
  total = Iter.reduce([1, 2], 0, fn total, x -> total + x)
  k = 3
  |> then fn v -> v + 1
  f = fn -> 42
  io.inspect((m, total, k, f(), double(2)))
end
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
		{"a declaration's fn", analysis.Pos{Line: 4, Col: 1}, "```nomi\nfn double(n: Int) -> Int\n```\n\nDoubles n.\n\n---\n\nDeclares a function. Every parameter has a type, the result type is written unless it is `Unit`, and the body's last expression is the result.\n\nMore in the [tour](https://nomi-lang.org/functions-and-lambdas/#defining-a-function)"},
		{"a lambda argument's fn", analysis.Pos{Line: 9, Col: 26}, mapped + lambdaLink},
		{"its ->", analysis.Pos{Line: 9, Col: 31}, mapped},
		{"a fold's callback", analysis.Pos{Line: 10, Col: 34}, "```nomi\nfn total: Int, x: Int -> Int\n```\n\nFills `Iter.reduce`'s `f: (U, T) -> U`, which gives its parameter types." + lambdaLink},
		{"a then stage's lambda", analysis.Pos{Line: 12, Col: 11}, "```nomi\nfn v: Int -> Int\n```\n\nThe `then` stage's lambda; its parameter is the piped value." + lambdaLink},
		{"zero parameters", analysis.Pos{Line: 13, Col: 7}, "```nomi\nfn -> Int\n```" + lambdaLink},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for offset := 0; offset < 2; offset++ {
				pos := analysis.Pos{Line: c.pos.Line, Col: c.pos.Col + offset}
				if got := hoverdoc.At(fa, nodes, src, pos); got != c.want {
					t.Errorf("tour hover on character %d = %q, want %q", offset+1, got, c.want)
				}
				if got := hoverdoc.At(editor, editorNodes, src, pos); got != c.want {
					t.Errorf("editor hover on character %d = %q, want %q", offset+1, got, c.want)
				}
			}
		})
	}
}

// lambdaLink ends the hover of a lambda's `fn`, which the keyword adds
// below the lambda's type.
const lambdaLink = "\n\nMore in the [tour](https://nomi-lang.org/functions-and-lambdas/#lambdas)"

// The tour's editors answer a keyword as the editor does
// (hoverdoc.KeywordAt): its construct and link in the place it stands, and
// a word operator's typed hover with its link.
func TestAnalyze_KeywordHovers(t *testing.T) {
	const src = `import std::io

fn main()
  n = 2
  label = case n
    1 -> "one"
    _ -> "many"
  end
  io.print(label)
  _ = n > 1 and n < 3
end
`
	lib := std.Load()
	fa, nodes := highlight.AnalyzeNodes(src, lib)
	if fa == nil {
		t.Fatal("highlight.AnalyzeNodes returned nil")
	}
	editor, editorNodes := editorDoc(t, lib, src)
	cases := []struct {
		name string
		pos  analysis.Pos
		want []string
	}{
		{"import", analysis.Pos{Line: 1, Col: 1}, []string{"```nomi\nimport path::file\n", "/modules-and-imports/#import-syntax"}},
		{"case", analysis.Pos{Line: 5, Col: 11}, []string{"/pattern-matching/#binding-patterns-and-guards"}},
		{"and", analysis.Pos{Line: 10, Col: 13}, []string{"/spec/expressions/#logical-operators"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hoverdoc.At(fa, nodes, src, c.pos)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("tour hover = %q, want it to hold %q", got, w)
				}
			}
			if ed := hoverdoc.At(editor, editorNodes, src, c.pos); got != ed {
				t.Errorf("tour hover = %q, editor's = %q", got, ed)
			}
		})
	}
}
