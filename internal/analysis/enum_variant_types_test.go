package analysis

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"testing"
)

func TestEnumVariantTypes(t *testing.T) {
	tokens := lexer.Lex(`
enum Result<T, E> {
    Ok T
    Err E
}
enum Maybe<T> {
    Some T
    None
}
enum Bool {
    True
    False
}
`)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := BuildFile(nodes)
	BuildTypes(fa, nodes)

	// Data-carrying variants should have function types
	okSym := fa.ModuleScope.Lookup("Ok")
	if okSym == nil || okSym.Type == nil {
		t.Fatal("Ok should have a type")
	}
	ft, ok := okSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("Ok should be FuncType, got %T", okSym.Type)
	}
	if len(ft.Params) != 1 {
		t.Fatalf("Ok should have 1 param, got %d", len(ft.Params))
	}

	// Bare variants should have enum type
	trueSym := fa.ModuleScope.Lookup("True")
	if trueSym == nil || trueSym.Type == nil {
		t.Fatal("True should have a type")
	}
	et, ok := trueSym.Type.(*EnumType)
	if !ok {
		t.Fatalf("True should be EnumType, got %T", trueSym.Type)
	}
	if et.Name != "Bool" {
		t.Fatalf("True type should be Bool, got %s", et.Name)
	}

	// None is bare — should be Maybe<T>
	noneSym := fa.ModuleScope.Lookup("None")
	if noneSym == nil || noneSym.Type == nil {
		t.Fatal("None should have a type")
	}
	noneEt, ok := noneSym.Type.(*EnumType)
	if !ok {
		t.Fatalf("None should be EnumType, got %T", noneSym.Type)
	}
	if noneEt.Name != "Maybe" {
		t.Fatalf("None type should be Maybe, got %s", noneEt.Name)
	}
}
