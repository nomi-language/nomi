package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Clicking on an interface name returns the locations of every impl-block
// method that impl any of its methods. The fixture declares one
// interface with two methods plus three method impls (two for `speak`, one
// for `name`), so the response should be three Locations.
func TestImplementation_InterfaceName(t *testing.T) {
	input := "interface Speech {\n" +
		"  fn speak(value: self): String\n" +
		"  fn name(value: self): String\n" +
		"}\n" +
		"\n" +
		"struct Dog { sound: String }\n" +
		"struct Cat { sound: String }\n" +
		"\n" +
		"impl Speech for Dog {\n" +
		"  fn speak(value: self): String { value.sound }\n" +
		"  fn name(_value: self): String { \"Rex\" }\n" +
		"}\n" +
		"\n" +
		"impl Speech for Cat {\n" +
		"  fn speak(value: self): String { value.sound }\n" +
		"}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// `interface Speech {` — Speech starts at line 0 (zero-based), col 10.
	params := &protocol.ImplementationParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 0, Character: 11},
		},
	}
	result, err := s.textDocumentImplementation(nil, params)
	if err != nil {
		t.Fatalf("implementation request returned error: %v", err)
	}
	locs, ok := result.([]protocol.Location)
	if !ok {
		t.Fatalf("expected []protocol.Location, got %T", result)
	}
	if len(locs) != 3 {
		t.Fatalf("expected 3 impl locations, got %d: %+v", len(locs), locs)
	}
}

// Clicking on a specific interface-declared method (e.g. `speak` in the
// interface body) returns locations only for impls of that method, not
// every impl in the interface. The fixture has two `speak` impls and one
// `name` impl; the response should be two Locations (the speaks).
func TestImplementation_InterfaceMethod(t *testing.T) {
	input := "interface Speech {\n" +
		"  fn speak(value: self): String\n" +
		"  fn name(value: self): String\n" +
		"}\n" +
		"\n" +
		"struct Dog { sound: String }\n" +
		"struct Cat { sound: String }\n" +
		"\n" +
		"impl Speech for Dog {\n" +
		"  fn speak(value: self): String { value.sound }\n" +
		"  fn name(_value: self): String { \"Rex\" }\n" +
		"}\n" +
		"\n" +
		"impl Speech for Cat {\n" +
		"  fn speak(value: self): String { value.sound }\n" +
		"}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// Cursor on `speak` inside the interface body. The line reads
	// `  fn speak(value: self): String` — `speak` starts at line 1
	// (zero-based), col 5.
	params := &protocol.ImplementationParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 1, Character: 5},
		},
	}
	result, err := s.textDocumentImplementation(nil, params)
	if err != nil {
		t.Fatalf("implementation request returned error: %v", err)
	}
	locs, ok := result.([]protocol.Location)
	if !ok {
		t.Fatalf("expected []protocol.Location, got %T", result)
	}
	if len(locs) != 2 {
		t.Fatalf("expected 2 speak impl locations, got %d: %+v", len(locs), locs)
	}
}

// A DERIVED conformance (`derive Hashable for Point`) has no hand-written
// method block — its impl is synthesized at a fake synth-band position
// (line ~2^30). Go-to-implementation must NOT emit that un-navigable ghost;
// it remaps the target to the real `derive Hashable for Point` declaration
// entry so the editor actually jumps there.
func TestImplementation_DerivedConformanceRemapsToConformanceEntry(t *testing.T) {
	input := "struct Point {\n" + // 0
		"  x: Int\n" + // 1
		"}\n" + // 2
		"\n" + // 3
		"derive Hashable for Point\n" // 4  Hashable at char 7
	uri := "file:///test.nomi"
	s := NewServer()
	s.docs.Open(uri, input)

	params := &protocol.ImplementationParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 4, Character: 10}, // on Hashable
		},
	}
	result, err := s.textDocumentImplementation(nil, params)
	if err != nil {
		t.Fatalf("implementation request returned error: %v", err)
	}
	locs, ok := result.([]protocol.Location)
	if !ok || len(locs) != 1 {
		t.Fatalf("expected 1 location, got %T %+v", result, result)
	}
	// Must be the real conformance line (4), not a synth-band ghost (~2^30).
	if locs[0].Range.Start.Line != 4 {
		t.Fatalf("expected target on the conformance line 4, got line %d (col %d)",
			locs[0].Range.Start.Line, locs[0].Range.Start.Character)
	}
}

// Same, but with multiple derived conformances; the target must be the
// specific `derive Hashable for Point` line.
func TestImplementation_DerivedConformanceAmongMultipleDerivesRemapsToEntry(t *testing.T) {
	input := "struct Point {\n" + // 0
		"  x: Int\n" + // 1
		"}\n" + // 2
		"\n" + // 3
		"derive Equatable for Point\n" + // 4
		"derive Hashable for Point\n" // 5  Hashable at char 7
	uri := "file:///test.nomi"
	s := NewServer()
	s.docs.Open(uri, input)

	params := &protocol.ImplementationParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 5, Character: 10}, // on Hashable
		},
	}
	result, err := s.textDocumentImplementation(nil, params)
	if err != nil {
		t.Fatalf("implementation request returned error: %v", err)
	}
	locs, ok := result.([]protocol.Location)
	if !ok || len(locs) != 1 {
		t.Fatalf("expected 1 location, got %T %+v", result, result)
	}
	if locs[0].Range.Start.Line != 5 {
		t.Fatalf("expected target on the `derive Hashable` declaration line 5, got line %d",
			locs[0].Range.Start.Line)
	}
}

// Clicking on a position with no resolvable symbol (e.g. whitespace, a
// comment, or a binding) returns no implementations rather than erroring.
func TestImplementation_UnrelatedSymbolReturnsNil(t *testing.T) {
	input := "fn main() { x = 5 }\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	params := &protocol.ImplementationParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 0, Character: 12}, // on `x`
		},
	}
	result, err := s.textDocumentImplementation(nil, params)
	if err != nil {
		t.Fatalf("implementation request returned error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for a non-interface symbol, got %T: %+v", result, result)
	}
}
