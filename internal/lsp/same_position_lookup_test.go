package lsp

import (
	"maps"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Two files each declare an interface with a method `go` at the same line
// and column. The method's owner is the interface whose Members hold that
// method, not the first interface the scope's map yields with a method at
// the same position, so go-to-implementation (and the references that
// list impls) answers Bar's impl on every run.
func TestImplementation_MethodOwnerIsExactAcrossSamePositionedInterfaces(t *testing.T) {
	main := "import a.Foo\n" +
		"import b.Bar\n" +
		"\n" +
		"impl Foo for Int {\n" +
		"  fn go(value: self) -> Int { value }\n" +
		"}\n" +
		"\n" +
		"impl Bar for String {\n" +
		"  fn go(value: self) -> Int { String.length(value) }\n" +
		"}\n" +
		"\n" +
		"fn main() {\n" +
		"  _ = Foo.go(1)\n" +
		"  _ = Bar.go(\"x\")\n" +
		"}\n"
	s, uri := openProject(t, map[string]string{
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"a.nomi":    "pub interface Foo {\n  fn go(value: self) -> Int\n}\n",
		"b.nomi":    "pub interface Bar {\n  fn go(value: self) -> Int\n}\n",
		"main.nomi": main,
	}, "main.nomi")
	at := offsetPosition(main, strings.Index(main, "Bar.go")+len("Bar."))
	barImplLine := uint32(8)
	for i := 0; i < 50; i++ {
		result, err := s.textDocumentImplementation(nil, &protocol.ImplementationParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: uri},
				Position:     at,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		locs, _ := result.([]protocol.Location)
		if len(locs) != 1 || locs[0].Range.Start.Line != barImplLine {
			t.Fatalf("run %d: want Bar's one impl at line %d, got %+v", i, barImplLine, locs)
		}
	}
}

// withFakeStd gives s a copy of its std whose Modules and Files also hold
// a module "fake" with the given scope. The name sorts before "json", so a
// lookup that takes the first module in name order would take it. The
// process's shared std is left alone.
func withFakeStd(s *Server, scope *analysis.Scope) {
	lib := *s.std
	lib.Modules = maps.Clone(s.std.Modules)
	lib.Files = maps.Clone(s.std.Files)
	lib.Modules["fake"] = scope
	lib.Files["fake"] = &analysis.FileAnalysis{ModuleScope: scope}
	s.std = &lib
}

func definitionAt(t *testing.T, s *Server, uri string, at protocol.Position) protocol.Location {
	t.Helper()
	result, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     at,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	loc, ok := result.(*protocol.Location)
	if !ok || loc == nil {
		t.Fatalf("want one location, got %T %+v", result, result)
	}
	return *loc
}

// Go-to-definition on a std declaration lands in the std file that declares
// that symbol. Another std file with a symbol of the same name at the same
// line and column (here a "fake" module beside json) is a different
// declaration, and used to win whenever the Files map yielded it first.
func TestDefinition_StdSymbolLandsInItsOwnFileBesideASamePositionedTwin(t *testing.T) {
	main := "import std/json.{Json, ToJson}\n" +
		"\n" +
		"struct Point { x: Int }\n" +
		"\n" +
		"impl ToJson for Point {\n" +
		"  fn to_json(_value: self) -> Json { Json.Null }\n" +
		"}\n"
	s, uri := openProject(t, map[string]string{"main.nomi": main}, "main.nomi")
	real := s.std.Modules["json"].LookupLocal("ToJson")
	if real == nil {
		t.Fatal("std/json declares no ToJson")
	}
	twin := analysis.NewScope(nil)
	twin.Define(&analysis.Symbol{Name: "ToJson", Kind: analysis.SymbolInterface, Pos: real.Pos, Members: map[string]*analysis.Symbol{}})
	withFakeStd(s, twin)
	at := offsetPosition(main, strings.Index(main, "impl ToJson")+len("impl "))
	for i := 0; i < 50; i++ {
		loc := definitionAt(t, s, uri, at)
		if !strings.HasSuffix(string(loc.URI), "/json.nomi") || loc.Range.Start.Line != uint32(real.Pos.Line-1) {
			t.Fatalf("run %d: want std/json.nomi:%d, got %+v", i, real.Pos.Line, loc)
		}
	}
}

// Go-to-definition on an impl's function goes to the method of the
// interface its header names (`json.ToJson`), never to a same-named
// interface another std module declares (the "fake" module here), which
// the first module the Modules map yielded used to decide.
func TestDefinition_ImplMethodGoesToTheInterfaceItsHeaderNames(t *testing.T) {
	main := "import std/json\n" +
		"\n" +
		"struct Point { x: Int }\n" +
		"\n" +
		"impl json.ToJson for Point {\n" +
		"  fn to_json(_value: self) -> json.Json { json.Json.Null }\n" +
		"}\n"
	s, uri := openProject(t, map[string]string{"main.nomi": main}, "main.nomi")
	want := s.std.Modules["json"].LookupLocal("ToJson").Members["to_json"]
	twin := analysis.NewScope(nil)
	twin.Define(&analysis.Symbol{Name: "ToJson", Kind: analysis.SymbolInterface, Pos: analysis.Pos{Line: 1, Col: 15}, Members: map[string]*analysis.Symbol{
		"to_json": {Name: "to_json", Kind: analysis.SymbolInterfaceMethod, Pos: analysis.Pos{Line: 2, Col: 8}},
	}})
	withFakeStd(s, twin)
	at := offsetPosition(main, strings.Index(main, "fn to_json")+len("fn "))
	for i := 0; i < 50; i++ {
		loc := definitionAt(t, s, uri, at)
		if !strings.HasSuffix(string(loc.URI), "/json.nomi") || loc.Range.Start.Line != uint32(want.Pos.Line-1) {
			t.Fatalf("run %d: want std/json.nomi:%d, got %+v", i, want.Pos.Line, loc)
		}
	}
}
