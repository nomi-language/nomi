package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// `with` is a token of two constructs: the options clause of a `derive`
// declaration, and the application-field override expression
// (parser_with_test.go). The two never meet: `derive` reads the token after
// its receiver type, and an expression starts with it.

// TestWithToken_OptionsClauseParses pins the derive use. `parseDeriveDecl`
// reads `token.WITH` after the receiver type, so removing the token breaks
// every `derive ToJson for T with ToJson.Options{...}` in the repository.
func TestWithToken_OptionsClauseParses(t *testing.T) {
	nodes := parse(t, `derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel}`)
	d, ok := nodes[0].(*ast.ImplConformance)
	if !ok {
		t.Fatalf("expected *ast.ImplConformance, got %T", nodes[0])
	}
	if d.Options == nil {
		t.Fatal("`derive ... with <options>` parsed but carried no options")
	}
}
