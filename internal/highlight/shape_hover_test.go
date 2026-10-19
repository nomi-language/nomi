package highlight_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
)

// The tour hovers every character of a block, punctuation included, and
// answers a shape literal's head and closing bracket, a variant's `.` and a
// pattern's head as the editor does.
func TestAnalyze_ShapeHovers(t *testing.T) {
	const src = `import std/io

enum Room {
  /// The cave.
  Cave
}

fn main() {
  s = %["a"]
  r: Room = .Cave
  pair = #(1, "a")
  case s {
    %["a", ..rest] -> io.inspect(rest)
    _ -> io.inspect(#(r, pair))
  }
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
		pos  analysis.Pos
		want string
	}{
		{"the % of a set literal", analysis.Pos{Line: 9, Col: 7}, "```nomi\nSet<String>\n```"},
		{"its closing bracket", analysis.Pos{Line: 9, Col: 12}, "```nomi\nSet<String>\n```"},
		{"the . of a variant", analysis.Pos{Line: 10, Col: 13}, "```nomi\nRoom.Cave\n```\n\n*`Room` from the expected type*\n\nThe cave."},
		{"the #( of a tuple", analysis.Pos{Line: 11, Col: 10}, "```nomi\ntuple #(Int, String)\n```"},
		{"the head of a set pattern", analysis.Pos{Line: 13, Col: 5}, "set pattern: matches a `Set<String>` containing `\"a\"`; `rest` is `Set<String>`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hoverdoc.At(fa, nodes, c.pos); got != c.want {
				t.Errorf("tour hover = %q, want %q", got, c.want)
			}
			if got, _, _, _ := hoverdoc.SyntaxAt(editor, editorNodes, c.pos); got != c.want {
				t.Errorf("editor hover = %q, want %q", got, c.want)
			}
		})
	}
}

// editorDoc is the analysis and syntax trees the editor's document holds
// for src.
func editorDoc(t *testing.T, lib *std.StdLib, src string) (*analysis.FileAnalysis, []ast.Node) {
	t.Helper()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	doc := dm.Open("file:///shape_hover.nomi", src)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("the LSP document manager produced no analysis")
	}
	return doc.Analysis, doc.Nodes
}
