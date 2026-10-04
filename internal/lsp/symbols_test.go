package lsp

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestNodesToDocumentSymbols_TypeAliases(t *testing.T) {
	input := `interface Showable {
  fn show(value: self): String
}

interface Tagged {
  fn tag(value: self): String
}

typealias ShowAndTag Showable and Tagged

typealias Names List<String>
`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(input))
	file := buildFile(nodes)

	want := map[string]string{
		"ShowAndTag": "Showable and Tagged",
		"Names":      "List<String>",
	}
	for _, sym := range nodesToDocumentSymbols(nodes, file) {
		w, ok := want[sym.Name]
		if !ok {
			continue
		}
		if sym.Detail == nil || *sym.Detail != w {
			t.Errorf("%s: detail %v, want %q", sym.Name, sym.Detail, w)
		}
		delete(want, sym.Name)
	}
	if len(want) != 0 {
		t.Errorf("missing alias symbols: %v", want)
	}
}

func TestNodesToDocumentSymbols(t *testing.T) {
	input := `fn add(x: Int, y: Int): Int { x + y }

struct User {
    name: String
    age: Int
}

enum Shape {
    Circle Float
    Point
}`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)

	symbols := nodesToDocumentSymbols(nodes, file)

	if len(symbols) != 3 {
		t.Fatalf("expected 3 symbols, got %d", len(symbols))
	}

	if symbols[0].Name != "add" || symbols[0].Kind != protocol.SymbolKindFunction {
		t.Errorf("expected add/Function, got %s/%v", symbols[0].Name, symbols[0].Kind)
	}
	if symbols[1].Name != "User" || symbols[1].Kind != protocol.SymbolKindStruct {
		t.Errorf("expected User/Struct, got %s/%v", symbols[1].Name, symbols[1].Kind)
	}
	if len(symbols[1].Children) != 2 {
		t.Errorf("expected 2 struct fields, got %d", len(symbols[1].Children))
	}
	if symbols[2].Name != "Shape" || symbols[2].Kind != protocol.SymbolKindEnum {
		t.Errorf("expected Shape/Enum, got %s/%v", symbols[2].Name, symbols[2].Kind)
	}
	if len(symbols[2].Children) != 2 {
		t.Errorf("expected 2 enum variants, got %d", len(symbols[2].Children))
	}
}

func TestNodesToDocumentSymbols_Interface(t *testing.T) {
	input := `interface Display { fn to_string(value: self): String }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)

	symbols := nodesToDocumentSymbols(nodes, file)
	if len(symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(symbols))
	}
	if symbols[0].Kind != protocol.SymbolKindInterface {
		t.Errorf("expected Interface kind, got %v", symbols[0].Kind)
	}
	if len(symbols[0].Children) != 1 {
		t.Errorf("expected 1 method, got %d", len(symbols[0].Children))
	}
}
