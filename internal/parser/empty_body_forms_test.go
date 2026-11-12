package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// An empty named literal is a struct literal whatever line breaks, comments
// and `;` it holds, so formatting `A{//` `;}` into `A{ // }` keeps it one
// (FuzzFormatKeepsMeaning's ac54c6ad992aec42).
func TestEmptyNamedBraceIsAStructLiteralWhateverItHolds(t *testing.T) {
	for _, src := range []string{
		"A{}\n",
		"A{\n}\n",
		"A{ // note\n}\n",
		"A{//\n;}\n",
		"A{\n    // note\n}\n",
	} {
		nodes, err := ParseAll(lexer.Lex(src))
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		stmt, ok := nodes[0].(*ast.ExprStmt)
		if !ok {
			t.Fatalf("%q: got %T, want an expression statement", src, nodes[0])
		}
		if _, ok := stmt.Expr.(*ast.StructLit); !ok {
			t.Errorf("%q: got %T, want *ast.StructLit", src, stmt.Expr)
		}
	}
}

// `import end` is the empty import block, as `import` above `end` is, so the
// formatter's one-line empty body keeps its meaning (FuzzFormatKeepsMeaning's
// 32f52de0a0dadac3).
func TestEmptyImportBlockOnOneLineIsABlock(t *testing.T) {
	for _, src := range []string{"import\nend\n", "import end\n"} {
		nodes, err := ParseAll(lexer.Lex(src))
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if _, ok := nodes[0].(*ast.ImportBlock); !ok {
			t.Errorf("%q: got %T, want *ast.ImportBlock", src, nodes[0])
		}
	}
}
