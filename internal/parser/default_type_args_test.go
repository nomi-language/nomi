package parser_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A type parameter may name a default after `=`, and the parameters with
// one come after every one without.
func TestTypeParamDefault(t *testing.T) {
	nodes, errs := parser.ParseWithRecovery(lexer.Lex("pub opaque type Route<P = Unit> String\n"))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok || len(td.TypeParams) != 1 || td.TypeParams[0].Default == nil || td.TypeParams[0].Default.TypeString() != "Unit" {
		t.Fatalf("want one parameter defaulting to Unit, got %#v", nodes[0])
	}
}

func TestTypeParamDefaultBeforeRequiredIsRefused(t *testing.T) {
	_, errs := parser.ParseWithRecovery(lexer.Lex("struct Pair<A = Int, B> {\n    a: A\n    b: B\n}\n"))
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "type parameter `B` has no default, so it can't follow `A`, which has one") {
		t.Fatalf("want the ordering error, got %v", errs)
	}
}

// A struct field may leave out its type when it has a default.
func TestStructFieldWithoutType(t *testing.T) {
	nodes, errs := parser.ParseWithRecovery(lexer.Lex("struct Game {\n    word: String\n    misses = 0\n}\n"))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	sd := nodes[0].(*ast.StructDef)
	if f := sd.Fields[1]; f.Name != "misses" || f.TypeAnnotation != nil || f.Default == nil {
		t.Fatalf("want misses with a default and no type, got %#v", f)
	}
}
