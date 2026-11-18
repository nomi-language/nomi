package token

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"testing"
)

// Every TokenType constant's String is its name: tokentype_string.go is
// current with token.go.
func TestTokenNamesAreCurrent(t *testing.T) {
	file, err := parser.ParseFile(gotoken.NewFileSet(), "token.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != gotoken.CONST || len(gen.Specs) == 0 {
			continue
		}
		first := gen.Specs[0].(*ast.ValueSpec)
		if id, ok := first.Type.(*ast.Ident); !ok || id.Name != "TokenType" {
			continue
		}
		for _, spec := range gen.Specs {
			for _, name := range spec.(*ast.ValueSpec).Names {
				names = append(names, name.Name)
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("found no TokenType constants in token.go")
	}
	for i, name := range names {
		if got := TokenType(i).String(); got != name {
			t.Errorf("TokenType(%d).String() = %q, want %q; run `go generate ./internal/token`", i, got, name)
		}
	}
}
