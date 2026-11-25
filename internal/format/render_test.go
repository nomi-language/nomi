package format

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// importNodesFrom parses src and returns its top-level import nodes.
func importNodesFrom(t *testing.T, src string) []ast.Node {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, _, err := parser.ParseFile(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var imports []ast.Node
	for _, n := range nodes {
		switch n.(type) {
		case *ast.ImportStmt, *ast.ImportBlock:
			imports = append(imports, n)
		}
	}
	return imports
}

func TestRenderImports_Canonical(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "single module",
			src:  "import std::io\n",
			want: "import std::io",
		},
		{
			name: "selective list alphabetized",
			src:  "import std::calendar.{Days, Error}\n",
			want: "import std::calendar.{Days, Error}",
		},
		{
			name: "self first",
			src:  "import std::io\n",
			want: "import std::io",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderImports(importNodesFrom(t, tc.src))
			if err != nil {
				t.Fatalf("RenderImports: %v", err)
			}
			if got != tc.want {
				t.Errorf("RenderImports = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderImports_Idempotent(t *testing.T) {
	src := "import std::results.{Result}\nimport std::io\nimport std::lists: List\n"
	first, err := RenderImports(importNodesFrom(t, src))
	if err != nil {
		t.Fatalf("RenderImports: %v", err)
	}
	second, err := RenderImports(importNodesFrom(t, first+"\n"))
	if err != nil {
		t.Fatalf("RenderImports (reparse): %v", err)
	}
	if first != second {
		t.Errorf("RenderImports not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestRenderImports_NonImportNode(t *testing.T) {
	tokens := lexer.Lex("fn main()\n  42\nend\n")
	nodes, _, err := parser.ParseFile(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := RenderImports(nodes); err == nil {
		t.Fatal("expected error for non-import node, got nil")
	}
}

// A subtype's rule that holds a syntax error, as a recovered tree gives
// it mid-edit (`.Obj(_) -> True _ -> False` on the `case` line), renders
// with a marker where the parser could not read, not a panic.
func TestRenderNode_ErrorNodeRendersAMarker(t *testing.T) {
	src := "subtype JsonObject Json when fn json -> case json .Obj(_) -> True _ -> False end\n"
	nodes, errs, _ := parser.ParseResilient(lexer.Lex(src))
	if len(errs) == 0 {
		t.Fatal("the parser accepts a case's arms on its header's line; the fixture needs a source it recovers from")
	}
	var rule ast.Node
	for _, n := range nodes {
		if td, ok := n.(*ast.TypeDef); ok {
			rule = td.Rule
		}
	}
	if rule == nil {
		t.Fatalf("no recovered rule in %v", nodes)
	}
	found := false
	ast.Inspect(rule, func(n ast.Node) bool {
		if _, ok := n.(*ast.ErrorNode); ok {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("the recovered rule holds no ErrorNode")
	}
	if got := RenderNode(rule); !strings.Contains(got, ErrorNodeText) {
		t.Fatalf("RenderNode(rule) = %q, want it to hold %q", got, ErrorNodeText)
	}
}
