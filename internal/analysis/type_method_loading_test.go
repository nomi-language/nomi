package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Resolving a cross-module type-promoted method
// (`Type.method` found via the project-level TypeMethods table, not the
// per-file one) must record the provider module path into
// fa.TypeMethodModules.
func TestTypeMethodModule_RecordedAtCrossModuleCall(t *testing.T) {
	src := `pub struct Foo { x: Int }

fn use_foo(f: Foo): Int {
    Foo.bar(f)
}`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	fa := BuildFile(nodes)
	BuildTypes(fa, nodes)

	// Inject a cross-module type method: Foo.bar, provided by "std/widget".
	// (Mirrors how the real project index carries stdlib type methods.)
	barSym := &Symbol{
		Name:   "bar",
		Kind:   SymbolFunction,
		Public: true,
		Type:   &FuncType{Params: []Type{TypeInt}, Return: TypeInt},
	}
	fa.ProjectImpls = &ProjectImplIndex{
		TypeMethods:      map[string]map[string]*Symbol{"Foo": {"bar": barSym}},
		TypeMethodModule: map[string]map[string]string{"Foo": {"bar": "std/widget"}},
	}

	CheckTypes(fa, nodes)

	if !fa.TypeMethodModules["std/widget"] {
		t.Fatalf("expected provider module 'std/widget' recorded in TypeMethodModules, got %v", fa.TypeMethodModules)
	}
}
