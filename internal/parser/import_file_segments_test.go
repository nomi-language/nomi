package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// An import's file path is its `/`-joined segments and every lowercase
// segment written after a `.`; only a trailing run of PascalCase segments
// are owners, since an owner is a type. So `std.a.B` and `std/a.B` parse to
// the same import, and `nomi fmt`, which writes the file path `/`-joined,
// keeps its meaning. Found by FuzzFormatKeepsMeaning on
// `{{import std.a000000.A…}}`.
func TestImportFileSegments(t *testing.T) {
	cases := []struct {
		src  string
		path int
		file int
	}{
		{"import std.a000000.A0000000000000000000000", 2, 2},
		{"import std/a000000.A0000000000000000000000", 2, 2},
		{"import std.io.print", 2, 2},
		{"import std.io.{self, print}", 2, 2},
		{"import api.http.Header.{self, Name}", 3, 2},
		{"import shape.Shape.{Circle}", 2, 1},
		{"import shape.Shape.Inner.Circle", 3, 1},
		{"import foo.Bar.baz.Q", 3, 3},
		{"import a/B.c", 2, 2},
		{"import calc: add, Shape.{Circle}", 1, 1},
		{"fn main() {\n    {\n        import std.a.B\n    }\n}", 2, 2},
	}
	for _, tc := range cases {
		nodes, err := Parse(lexer.Lex(tc.src))
		if err != nil {
			t.Errorf("%q: %v", tc.src, err)
			continue
		}
		var imp *ast.ImportStmt
		for _, node := range nodes {
			ast.Inspect(node, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.ImportStmt:
					if imp == nil {
						imp = v
					}
				case *ast.ImportBlock:
					if imp == nil && len(v.Entries) > 0 {
						imp = v.Entries[0]
					}
				}
				return imp == nil
			})
		}
		if imp == nil {
			t.Errorf("%q: no import", tc.src)
			continue
		}
		if len(imp.ModulePath) != tc.path || imp.FileSegments != tc.file {
			t.Errorf("%q: %d path segments with %d naming the file, want %d with %d", tc.src, len(imp.ModulePath), imp.FileSegments, tc.path, tc.file)
		}
	}
}
