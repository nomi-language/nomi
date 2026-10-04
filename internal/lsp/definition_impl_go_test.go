package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"testing"
)

func TestForeignGoDefinition_TraversesImplBlock(t *testing.T) {
	src := `gopkg "example.com/binding/ffi" as ffi

struct Widget {
  value: String
}

impl Widget {
  fn echo(value: String): String go ffi.Echo
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var impl *ast.ImplBlock
	for _, node := range nodes {
		if candidate, ok := node.(*ast.ImplBlock); ok {
			impl = candidate
			break
		}
	}
	if impl == nil || len(impl.Items) != 1 {
		t.Fatalf("impl block = %#v", impl)
	}
	ext, ok := impl.Items[0].(*ast.ExternFunc)
	if !ok {
		t.Fatalf("impl item = %T, want ExternFunc", impl.Items[0])
	}
	importPath, goName, ok := foreignBindingAt(nodes, analysis.Pos{Line: ext.ForeignNameLine, Col: ext.ForeignNameCol}, nil)
	if !ok || importPath != "example.com/binding/ffi" || goName != "Echo" {
		t.Fatalf("binding = (%q, %q, %v), want (example.com/binding/ffi, Echo, true)", importPath, goName, ok)
	}
}
