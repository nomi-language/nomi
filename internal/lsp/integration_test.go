package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestFullPipeline(t *testing.T) {
	input := `struct User {
    name: String
    age: Int
}

fn greet(_user: User): String {
    greeting = "Hello, "
    greeting
}

enum Color {
    Red
    Green
    Blue
}`

	// 1. Parse with recovery
	tokens := lexer.Lex(input)
	nodes, errs := parser.ParseWithRecovery(tokens)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}

	// 2. Build analysis
	file := buildFile(nodes)

	// 3. Verify module scope has all top-level symbols
	for _, name := range []string{"User", "greet", "Color", "Red", "Green", "Blue"} {
		if file.ModuleScope.Lookup(name) == nil {
			t.Errorf("expected %s in module scope", name)
		}
	}

	// 4. Verify document symbols
	symbols := nodesToDocumentSymbols(nodes, file)
	if len(symbols) != 3 {
		t.Errorf("expected 3 top-level symbols, got %d", len(symbols))
	}

	// 5. Verify diagnostics conversion
	diags := parseErrorsToDiagnostics(nil)
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for valid input")
	}

	// 6. Verify completion
	visible := file.ModuleScope.AllVisible()
	if len(visible) < 3 {
		t.Errorf("expected at least 3 visible symbols, got %d", len(visible))
	}

	// 7. Verify document manager round-trip
	dm := analysis.NewDocumentManager()
	doc := dm.Open("file:///test.nomi", input)
	if doc.Analysis.ModuleScope.Lookup("User") == nil {
		t.Error("document manager should analyze on open")
	}
}

func TestFullPipeline_WithErrors(t *testing.T) {
	input := `fn broken( {

fn valid(x: Int): Int { x + 1 }`

	tokens := lexer.Lex(input)
	nodes, errs := parser.ParseWithRecovery(tokens)

	// Should have errors
	if len(errs) == 0 {
		t.Fatal("expected parse errors")
	}

	// Should still have recovered the Valid function
	file := buildFile(nodes)
	if file.ModuleScope.Lookup("valid") == nil {
		t.Error("expected valid to be recovered despite earlier error")
	}

	// Diagnostics should be non-empty
	diags := parseErrorsToDiagnostics(errs)
	if len(diags) == 0 {
		t.Error("expected diagnostics")
	}
	if *diags[0].Severity != protocol.DiagnosticSeverityError {
		t.Error("expected error severity")
	}
}

func TestFullPipeline_AllDefinitionTypes(t *testing.T) {
	input := `fn add(x: Int, y: Int): Int { x + y }

struct Point {
    x: Float
    y: Float
}

enum Direction {
    North
    South
    East
    West
}

type UserId Int

interface Display {
    fn to_string(value: self): String
}

impl Display for Point {
    fn to_string(_p: Point): String { "point" }
}`

	tokens := lexer.Lex(input)
	nodes, errs := parser.ParseWithRecovery(tokens)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	// Lower the `impl Display for Point { ... }` block into a top-level
	// ast.ImplBlock, exactly as the real document pipeline does
	// (DocumentManager.analyze runs LowerDerives before document symbols).
	nodes, _ = analysis.LowerDerives(nodes)

	file := buildFile(nodes)

	// All definition types present
	checks := map[string]analysis.SymbolKind{
		"add":       analysis.SymbolFunction,
		"Point":     analysis.SymbolStruct,
		"Direction": analysis.SymbolEnum,
		"North":     analysis.SymbolEnumVariant,
		"South":     analysis.SymbolEnumVariant,
		"UserId":    analysis.SymbolType,
		"Display":   analysis.SymbolInterface,
	}
	for name, expectedKind := range checks {
		sym := file.ModuleScope.Lookup(name)
		if sym == nil {
			t.Errorf("expected %s in scope", name)
			continue
		}
		if sym.Kind != expectedKind {
			t.Errorf("%s: expected kind %v, got %v", name, expectedKind, sym.Kind)
		}
	}

	// Document symbols should have all top-level named defs plus the
	// `impl Display for Point { ... }` block (surfaced as an Object symbol with
	// its methods as children). Six: fn add, struct Point, enum Direction, type
	// UserId, interface Display, and the impl block.
	symbols := nodesToDocumentSymbols(nodes, file)
	if len(symbols) < 6 {
		t.Errorf("expected at least 6 document symbols, got %d", len(symbols))
	}
	// The impl block surfaces as an Object whose method(s) are children.
	var implSym *protocol.DocumentSymbol
	for i := range symbols {
		if symbols[i].Kind == protocol.SymbolKindObject {
			implSym = &symbols[i]
			break
		}
	}
	if implSym == nil {
		t.Fatalf("expected an impl-block document symbol (SymbolKindObject), got none")
	}
	if len(implSym.Children) == 0 {
		t.Errorf("expected the impl block's methods as children, got none")
	}
}

func TestGoToDefinition_StdlibFunction(t *testing.T) {
	source := `import std/io
io.inspect(42)`

	tokens := lexer.Lex(source)
	nodes, _ := parser.ParseWithRecovery(tokens)

	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)

	// io.inspect on line 2 — "inspect" starts at col 4 (1-based: i=1, o=2, .=3, i=4)
	sym := fa.SymbolAt(analysis.Pos{Line: 2, Col: 4})
	if sym == nil {
		t.Fatal("expected SymbolAt to find inspect")
	}
	if sym.Name != "inspect" {
		t.Errorf("expected inspect, got %s", sym.Name)
	}
}

func TestCompletion_IncludesPrimitivesSymbols(t *testing.T) {
	source := `x = So`

	tokens := lexer.Lex(source)
	nodes, _ := parser.ParseWithRecovery(tokens)

	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)

	visible := fa.ModuleScope.AllVisible()
	found := false
	for _, sym := range visible {
		if sym.Name == "Some" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Some to be visible from primitives scope")
	}
}
