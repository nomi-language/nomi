package highlight_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
)

// The tour answers both characters of a pipe's `|>` as the editor does: the
// incoming type, the stage and the type that comes out.
func TestAnalyze_PipeHovers(t *testing.T) {
	const src = `import std/io

fn parse(s: String) -> Result<Int, String> {
  n = s |> String.to_int() |> Maybe.to_result("bad")?
  Ok(n)
}

fn main() {
  total = ["1", "x"]
  |> Iter.map(fn s -> String.to_int(s))
  |> Maybe.values()
  |> Iter.sum()
  |> tap fn v -> io.inspect(v)
  |> then fn v -> v + 1
  |> dbg
  xs = [1, 2] |> Iter.map(fn n -> n |> Int.to_string()) |> Iter.to_list()
  io.inspect(#(total, xs, parse("1"), "x" |> nope()))
}

test "parses" {
  n = "4" |> String.to_int() |> Maybe.to_result("no")?
  assert n == 4
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
		// line holds the pipe, the occurrence of `|>` on it is the
		// nth (from 1).
		line string
		nth  int
		want string
	}{
		{"a call stage", "  |> Maybe.values()", 1, "`Iter<Maybe<Int>>` → `Maybe.values(maybes)` → `List<Int>`\n\n```nomi\nfn values(maybes: Iter<Maybe<Int>>) -> List<Int>\n```\n\nThe piped value fills `maybes`."},
		{"tap", "  |> tap fn v", 1, "`Int` → `tap fn v -> …` → `Int`\n\n`v` is the `Int`; the lambda runs for its effect and the value passes on unchanged."},
		{"then", "  |> then fn v", 1, "`Int` → `then fn v -> …` → `Int`\n\n`v` is the `Int`; the stage answers the lambda's result."},
		{"dbg", "  |> dbg", 1, "`Int` → `dbg` → `Int`\n\nPrints the value with its source line and passes it on unchanged."},
		{"a ? in a function", `  n = s |> String.to_int()`, 2, "`Maybe<Int>` → `Maybe.to_result(maybe, error)` → `Result<Int, String>` → `?` → `Int`\n\n```nomi\nfn to_result(maybe: Maybe<Int>, error: String) -> Result<Int, String>\n```\n\nThe piped value fills `maybe`.\n\nAnswers the success value, or returns the `Err` from `fn parse`."},
		{"a ? in a test", `  n = "4" |>`, 2, "`Maybe<Int>` → `Maybe.to_result(maybe, error)` → `Result<Int, String>` → `?` → `Int`\n\n```nomi\nfn to_result(maybe: Maybe<Int>, error: String) -> Result<Int, String>\n```\n\nThe piped value fills `maybe`.\n\nAnswers the success value, or returns the `Err` from `test \"parses\"`."},
		{"a pipe inside a lambda", "  xs = [1, 2]", 2, "`Int` → `Int.to_string(n)` → `String`\n\n```nomi\nfn to_string(n: Int) -> String\n```\n\nThe piped value fills `n`."},
		{"a stage that fails to type", "  io.inspect(#(total", 1, "`String` → `nope()`"},
	}
	lines := strings.Split(src, "\n")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := -1
			for i, l := range lines {
				if strings.HasPrefix(l, c.line) {
					line = i
				}
			}
			if line < 0 {
				t.Fatalf("no line starts with %q", c.line)
			}
			col := -1
			for i, rest := 0, lines[line]; i < c.nth; i++ {
				at := strings.Index(rest[col+1:], "|>")
				if at < 0 {
					t.Fatalf("line %q has no `|>` number %d", lines[line], c.nth)
				}
				col += at + 1
			}
			for offset := 0; offset < 2; offset++ {
				pos := analysis.Pos{Line: line + 1, Col: col + 1 + offset}
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
