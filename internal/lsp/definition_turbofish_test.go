package lsp

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"testing"
)

// A turbofish type argument is a type reference: the builder must register
// it in file.References so the LSP offers hover / go-to-def on it (e.g. the
// `Item` in `id<Item>(it)`). Here `Item` appears in a construction and in a
// turbofish; without the builder walking TypeArgs only the construction
// would be registered, so we require at least two references.
func TestFindDefinition_TurbofishTypeArgResolves(t *testing.T) {
	input := `struct Item { id: Int }
fn id<T>(x: T): T { x }
fn main(): Unit {
  it = Item{id: 1}
  y = id<Item>(it)
}`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)

	itemRefs := 0
	for _, sym := range file.References {
		if sym.Name == "Item" {
			itemRefs++
		}
	}
	if itemRefs < 2 {
		t.Fatalf("expected the turbofish `Item` to be registered as a reference "+
			"(construction + turbofish = 2+), got %d", itemRefs)
	}
}
