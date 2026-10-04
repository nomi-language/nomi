package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TestResolveLocalGoImportDir_CoLocatedStdlibAdapter was here and is DELETED
// with the branch it covered. It resolved `nomi/std/regex` to `std/regex/` so
// go-to-definition on the facade's `gopkg` handle reached `regex.go`. There is
// no such handle: std/regex declares `host fn` and its Go is `nomi/stdregex`,
// a sibling package no Nomi source names.

func TestFindDefinition_ReferenceResolvesToSymbol(t *testing.T) {
	input := `fn double(x: Int): Int { x + x }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)

	// Find any reference to "x" and verify SymbolAt returns the param
	for pos, sym := range file.References {
		if sym.Name == "x" {
			result := file.SymbolAt(pos)
			if result == nil {
				t.Fatal("SymbolAt returned nil for known reference")
			}
			if result.Name != "x" {
				t.Errorf("expected x, got %s", result.Name)
			}
			if result.Kind != analysis.SymbolParam {
				t.Errorf("expected param, got %v", result.Kind)
			}
			return
		}
	}
	t.Error("no reference to x found in analysis")
}

func TestFindDefinition_AttachedAssertCodeUsesNormalReferences(t *testing.T) {
	input := `//! assert ready_label() == "[ready]"
fn ready_label(): String {
  "[ready]"
}
`
	uri := "file:///attached_assert_definition.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 0, Character: 13},
		},
	}
	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	if loc.Range.Start.Line != 1 || loc.Range.Start.Character != 3 {
		t.Fatalf("expected definition at line 4 col 4, got LSP line %d char %d", loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestFindDefinition_AttachedAssertTypeQualifiedImplFunctionResolves(t *testing.T) {
	input, pos := definitionMarkerPosition(t, `struct Box {
  n: Int
}

impl Display for Box {
  //! assert Box.▮to_string(Box{n: 3}) == "box:3"
  fn to_string(b: Box): String {
    "box:" + Int.to_string(b.n)
  }
}
`)
	uri := "file:///attached_impl_assert_definition.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     pos,
		},
	}
	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	if loc.Range.Start.Line != 6 || loc.Range.Start.Character != 5 {
		t.Fatalf("expected definition at LSP line 6 char 5, got LSP line %d char %d", loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestFindDefinition_AttachedAssertBareOperatorImplResolvesByArgs(t *testing.T) {
	input, pos := definitionMarkerPosition(t, `pub interface Divide<Rhs, Out> {
  fn divide(lhs: self, rhs: Rhs): Out
}

pub opaque type NonZeroInt Int

impl Divide<Int, Int> for Int {
  fn divide(lhs: Int, rhs: Int): Int {
    0
  }
}

impl Divide<NonZeroInt, Int> for Int {
  //! nz = NonZeroInt(3)
  //! assert ▮divide(10, nz) == 10
  //
  fn divide(lhs: Int, rhs: NonZeroInt): Int {
    10
  }
}
`)
	uri := "file:///attached_bare_operator_impl_definition.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	loc := mustDefinitionLocation(t, s, uri, pos)
	if loc.Range.Start.Line != 16 || loc.Range.Start.Character != 5 {
		t.Fatalf("expected definition at NonZeroInt impl line 16 char 5, got LSP line %d char %d", loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestFindDefinition_InlineGoAliasReferenceResolvesToImport(t *testing.T) {
	root := stageGoFFIDefinitionProject(t)
	mainPath := filepath.Join(root, "main.nomi")
	input, pos := definitionMarkerPosition(t, `import std/io

gopkg "example.com/binding/ffi" as ffi

fn echo_upper(s: String): String go ▮ffi.EchoUpper

fn main() {
  io.print(echo_upper("fixture"))
}
`)
	if err := os.WriteFile(mainPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}

	s := NewServer()
	uri := pathToURI(mainPath)
	s.docs.Open(uri, input)

	loc := mustDefinitionLocation(t, s, uri, pos)
	wantURI := protocol.DocumentUri(uri)
	if loc.URI != wantURI {
		t.Fatalf("expected URI %q, got %q", wantURI, loc.URI)
	}
	wantLine := uint32(2)
	wantChar := uint32(strings.LastIndex(strings.Split(input, "\n")[2], "ffi"))
	if loc.Range.Start.Line != wantLine || loc.Range.Start.Character != wantChar {
		t.Fatalf("expected extern alias at LSP %d:%d, got %d:%d", wantLine, wantChar, loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestFindDefinition_GoPackagePathResolvesToGoPackage(t *testing.T) {
	root := stageGoFFIDefinitionProject(t)
	mainPath := filepath.Join(root, "main.nomi")
	input, pos := definitionMarkerPosition(t, `import std/io

gopkg "example.com/▮binding/ffi" as ffi

fn echo_upper(s: String): String go ffi.EchoUpper

fn main() {
  io.print(echo_upper("fixture"))
}
`)
	if err := os.WriteFile(mainPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}

	s := NewServer()
	uri := pathToURI(mainPath)
	s.docs.Open(uri, input)

	loc := mustDefinitionLocation(t, s, uri, pos)
	wantURI := protocol.DocumentUri(pathToURI(filepath.Join(root, "binding", "binding.go")))
	if loc.URI != wantURI {
		t.Fatalf("expected URI %q, got %q", wantURI, loc.URI)
	}
	if loc.Range.Start.Line != 0 || loc.Range.Start.Character != 8 {
		t.Fatalf("expected Go package identifier at LSP 0:8, got %d:%d", loc.Range.Start.Line, loc.Range.Start.Character)
	}
	if loc.Range.End.Line != 0 || loc.Range.End.Character != 15 {
		t.Fatalf("expected Go package identifier end at LSP 0:15, got %d:%d", loc.Range.End.Line, loc.Range.End.Character)
	}
}

func TestFindDefinition_InlineGoNameResolvesToGoFunction(t *testing.T) {
	root := stageGoFFIDefinitionProject(t)
	mainPath := filepath.Join(root, "main.nomi")
	input, pos := definitionMarkerPosition(t, `import std/io

gopkg "example.com/binding/ffi" as ffi

fn echo_upper(s: String): String go ffi.▮EchoUpper

fn main() {
  io.print(echo_upper("fixture"))
}
`)
	if err := os.WriteFile(mainPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}

	s := NewServer()
	uri := pathToURI(mainPath)
	s.docs.Open(uri, input)

	loc := mustDefinitionLocation(t, s, uri, pos)
	wantURI := protocol.DocumentUri(pathToURI(filepath.Join(root, "binding", "binding.go")))
	if loc.URI != wantURI {
		t.Fatalf("expected URI %q, got %q", wantURI, loc.URI)
	}
	if loc.Range.Start.Line != 6 || loc.Range.Start.Character != 5 {
		t.Fatalf("expected Go function identifier at LSP 6:5, got %d:%d", loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestFindDefinition_InlineGoTypeResolvesToGoType(t *testing.T) {
	root := stageGoFFIDefinitionProject(t)
	mainPath := filepath.Join(root, "main.nomi")
	input, pos := definitionMarkerPosition(t, `import std/io

gopkg "example.com/binding/ffi" as ffi

opaque type RawBox go ffi.▮Box

fn echo_upper(s: String): String go ffi.EchoUpper

fn main() {
  io.print(echo_upper("fixture"))
}
`)
	if err := os.WriteFile(mainPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}

	s := NewServer()
	uri := pathToURI(mainPath)
	s.docs.Open(uri, input)

	loc := mustDefinitionLocation(t, s, uri, pos)
	wantURI := protocol.DocumentUri(pathToURI(filepath.Join(root, "binding", "binding.go")))
	if loc.URI != wantURI {
		t.Fatalf("expected URI %q, got %q", wantURI, loc.URI)
	}
	if loc.Range.Start.Line != 4 || loc.Range.Start.Character != 5 {
		t.Fatalf("expected Go type identifier at LSP 4:5, got %d:%d", loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestFindDefinition_ForeignBindingLocalCallResolvesToNomiBinding(t *testing.T) {
	root := stageGoFFIDefinitionProject(t)
	mainPath := filepath.Join(root, "main.nomi")
	input, pos := definitionMarkerPosition(t, `import std/io

gopkg "example.com/binding/ffi" as ffi

fn echo_upper(s: String): String go ffi.EchoUpper

fn main() {
  io.print(▮echo_upper("fixture"))
}
`)
	if err := os.WriteFile(mainPath, []byte(input), 0o644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}

	s := NewServer()
	uri := pathToURI(mainPath)
	s.docs.Open(uri, input)

	loc := mustDefinitionLocation(t, s, uri, pos)
	wantURI := protocol.DocumentUri(uri)
	if loc.URI != wantURI {
		t.Fatalf("expected URI %q, got %q", wantURI, loc.URI)
	}
	wantLine := uint32(4)
	wantChar := uint32(strings.Index(strings.Split(input, "\n")[4], "echo_upper"))
	if loc.Range.Start.Line != wantLine || loc.Range.Start.Character != wantChar {
		t.Fatalf("expected Nomi binding at LSP %d:%d, got %d:%d", wantLine, wantChar, loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func mustDefinitionLocation(t *testing.T, s *Server, uri string, pos protocol.Position) *protocol.Location {
	t.Helper()
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     pos,
		},
	}
	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	return loc
}

func stageGoFFIDefinitionProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bindingDir := filepath.Join(root, "binding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir binding: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bindingDir, "binding.go"), []byte(`package binding

import "strings"

type Box struct{}

func EchoUpper(s string) string {
	return strings.ToUpper(s)
}
`), 0o644); err != nil {
		t.Fatalf("write binding.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bindingDir, "go.mod"), []byte(`module example.com/binding/ffi

go 1.26.3
`), 0o644); err != nil {
		t.Fatalf("write binding go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(`module testproject

go 1.26.3

require example.com/binding/ffi v0.0.0

replace example.com/binding/ffi => ./binding
`), 0o644); err != nil {
		t.Fatalf("write project go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nomi.toml"), []byte(`[module]
name = "testproject"
`), 0o644); err != nil {
		t.Fatalf("write nomi.toml: %v", err)
	}
	return root
}

func definitionMarkerPosition(t *testing.T, input string) (string, protocol.Position) {
	t.Helper()
	const marker = "▮"
	idx := strings.Index(input, marker)
	if idx < 0 {
		t.Fatal("missing definition marker")
	}
	cleaned := strings.Replace(input, marker, "", 1)
	line, char := uint32(0), uint32(0)
	for _, b := range []byte(input[:idx]) {
		if b == '\n' {
			line++
			char = 0
		} else {
			char++
		}
	}
	return cleaned, protocol.Position{Line: line, Character: char}
}

func TestFindDefinition_DefinitionResolvesToItself(t *testing.T) {
	input := `fn add(x: Int, y: Int): Int { x + y }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)

	for pos, sym := range file.Definitions {
		if sym.Name == "add" {
			result := file.SymbolAt(pos)
			if result == nil {
				t.Fatal("SymbolAt returned nil")
			}
			if result.Name != "add" {
				t.Errorf("expected add, got %s", result.Name)
			}
			return
		}
	}
	t.Error("no definition of add found")
}

// Go-to-def on an embeds-variant name (`embeds True`) must land on
// the embedded type's declaration, not on the variant symbol defined at the
// same position. SymbolAt is references-first, and walkTypeExpr records a
// type Reference at the embeds name — pinned here because semtokens'
// one-token-per-position tie-break relies on the same references-first
// preference (color and go-to-def must agree on what the name is).
func TestFindDefinition_EmbedsVariantNameResolvesToEmbeddedType(t *testing.T) {
	input := `pub host type True

pub enum Bool {
  embeds True
}`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)

	// `True` in `embeds True` starts at line 4, col 10.
	sym := file.SymbolAt(analysis.Pos{Line: 4, Col: 10})
	if sym == nil {
		t.Fatal("SymbolAt returned nil at the embeds name")
	}
	if sym.Kind != analysis.SymbolType {
		t.Errorf("expected SymbolType (the embedded host type), got %v", sym.Kind)
	}
	if sym.Pos.Line != 1 {
		t.Errorf("expected definition at line 1 (pub host type True), got line %d", sym.Pos.Line)
	}
}

// Go-to-def on a stdlib selective-import name (e.g. `Result` in
// `import std/results.Result`) should jump to the real definition
// site inside the stdlib module file — not stay parked at the click
// position. The local binding's `Resolved` points at the real stdlib
// symbol; the trailing block of textDocumentDefinition must use that
// resolved Pos and Name (not the local sym's) when computing both
// the target URI and the in-file location.
func TestFindDefinition_StdlibSelectiveImportResolvesToRealDefinition(t *testing.T) {
	input := "import std/results.Result\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// `Result` inside the braces sits at column 21 (1-based) in the import line.
	// Place the cursor at line 0 char 20 (zero-based) — anywhere inside `Result`.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 0, Character: 20},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `Result`, got nil")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("results"))
	if loc.URI != wantURI {
		t.Errorf("expected URI %q, got %q", wantURI, loc.URI)
	}
	// The real `type Result<T, E>` is on line 10 of results.nomi
	// (1-based). Zero-based: line 9. Anything other than line 9 means
	// we either landed at the click position (line 0) or at the top of
	// the file (line 0 with col 0) — both are bugs.
	if loc.Range.Start.Line == 0 && loc.Range.Start.Character == 0 {
		t.Errorf("Location is the top of the file (0:0) — definition wasn't resolved to the real Pos. URI=%s, Range=%v", loc.URI, loc.Range)
	}
}

// Go-to-def on a drill-through selective-import name (e.g. `Some` in
// `import std/maybe.Maybe.{Some, None}`) should land at the real
// variant definition inside maybe.nomi. Same bug as the flat selective
// case — manifests on every drill-through stdlib variant in
// prelude.nomi.
func TestFindDefinition_StdlibDrillThroughVariantResolvesToRealDefinition(t *testing.T) {
	input := "import std/maybe.Maybe.{Some, None}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// `Some` is at column 25 (1-based) in the import line.
	// Place the cursor at line 0 char 24 (zero-based) — inside `Some`.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 0, Character: 24},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `Some`, got nil")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("maybe"))
	if loc.URI != wantURI {
		t.Errorf("expected URI %q, got %q", wantURI, loc.URI)
	}
	if loc.Range.Start.Line == 0 && loc.Range.Start.Character == 0 {
		t.Errorf("Location is the top of the file (0:0) — definition wasn't resolved. URI=%s, Range=%v", loc.URI, loc.Range)
	}
}

// Go-to-def on a path segment of a stdlib import (`std`/`list` in
// `import std/lists`) should jump to the materialised stdlib file URI,
// not return nil. Returning nil makes Zed silently fall back to a
// references list, which is the wrong UX. Cross-file project imports
// keep working through the existing project-relative path resolution.
func TestFindDefinition_StdlibImportSegmentResolvesToStdlibFile(t *testing.T) {
	input := "import std/lists.List\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not in manager after Open")
	}

	// The "list" path segment is at line 1 col 12 (after `import std.`).
	// Place the cursor at line 0 char 11 (zero-based) — that's somewhere
	// inside "list" in `import std/lists.List`.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 0, Character: 11},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `list` in `import std/lists.List`, got nil — editor will fall back to references")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("lists"))
	if loc.URI != wantURI {
		t.Errorf("expected URI %q, got %q", wantURI, loc.URI)
	}
}

// Go-to-def on the interface name in an `derive Iface` conformance
// line should jump to the interface declaration. Until the builder walks the
// conformance's interface name as a reference, SymbolAt returns nil at that
// position and Zed's editor silently falls back to the references
// list — broken UX. The fix walks each `derive Iface` interface name
// through the existing walkTypeExpr resolver, exactly like an `impl Iface for T` block.
// Go-to-def on the method name of a type-qualified call (`Dog.speak(rex)`)
// should jump to the impl-block method definition. The checker records a
// reference at the method-name position via lookupTypeMethodSymbol; without
// it the editor can't navigate (the call still type-checks permissively).
func TestFindDefinition_OnTypeQualifiedMethodResolvesToImpl(t *testing.T) {
	input := `import std/io

pub interface Speech {
  fn speak(animal: self): String
}

pub struct Dog {
  name: String
}

impl Speech for Dog {
  fn speak(d: Dog): String {
    d.name + " says woof"
  }
}

fn main() {
  rex = Dog{name: "Rex"}
  io.inspect(Dog.speak(rex))
}
`
	uri := "file:///test.nomi"
	s := NewServer()
	s.docs.Open(uri, input)

	// `speak` in `Dog.speak(rex)` on line 19 (1-based): two leading spaces,
	// `io.inspect(Dog.` puts `speak` at col 18 (1-based) → char 17 (0-based);
	// line 19 → LSP line 18. Click mid-token at char 18.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 18, Character: 18},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `speak` in `Dog.speak(rex)`, got nil — go-to-def on a type-qualified method should jump to the impl method")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	// The impl method `fn speak(d: self)` is on line 12 (1-based) → LSP
	// line 11. Anything pointing into the impl block (not line 0, same file)
	// confirms the reference resolved to the method definition.
	if loc.Range.Start.Line == 0 {
		t.Errorf("go-to-def landed at line 0 — reference not resolved to the impl method; got Range=%v", loc.Range)
	}
}

// Go-to-def on a bare same-owner type-body call should jump to the sibling
// function's definition. The checker tracks the block's receiver type so the
// bare call resolves like `Point.get(p)` without exposing a module-level
// binding.
func TestFindDefinition_OnBareSameOwnerMethodResolvesToSibling(t *testing.T) {
	input := `import std/io

pub struct Point {
  x: Int

  pub fn get(p: Point): Int {
    p.x
  }

  pub fn get_twice(p: Point): Int {
    get(p) + get(p)
  }
}

fn main() {
  io.inspect(Point.get_twice(Point{x: 5}))
}
`
	uri := "file:///test.nomi"
	s := NewServer()
	s.docs.Open(uri, input)

	// `get` in the first `get(p)` on line 11 (1-based): four leading
	// spaces, `get` starts at col 5 (1-based) → char 4 (0-based).
	// line 11 → LSP line 10. Click mid-token at char 5.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 10, Character: 5},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for bare `get(p)`, got nil — go-to-def should jump to the sibling function")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	// `fn get` is on line 6 (1-based) → LSP line 5. Not line 0 confirms it
	// resolved to the method definition rather than parking at the click.
	if loc.Range.Start.Line == 0 {
		t.Errorf("go-to-def landed at line 0 — bare same-owner call not resolved; got Range=%v", loc.Range)
	}
}

func TestFindDefinition_OnDeriveDecoratorArgResolvesToInterface(t *testing.T) {
	input := `import std/equatable.{Equatable}

pub struct Point {
  x: Int
  y: Int
}

derive Equatable for Point
`
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// `Equatable` on the `derive Equatable for Point` line lives at
	// 1-based line 8. LSP zero-based: line 7, char 10 is inside `Equatable`.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 7, Character: 10},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `Equatable` on the derive line, got nil — clicking the conformance arg should jump to the interface decl")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("equatable"))
	if loc.URI != wantURI {
		t.Errorf("expected URI %q (stdlib equatable.nomi), got %q", wantURI, loc.URI)
	}
	// The real `pub interface Equatable` is on line 20 of equatable.nomi
	// (1-based) — anything other than the top of the file (0:0) means it
	// resolved through to a real Pos.
	if loc.Range.Start.Line == 0 && loc.Range.Start.Character == 0 {
		t.Errorf("Location is the top of the file (0:0) — definition wasn't resolved to the real Pos. URI=%s, Range=%v", loc.URI, loc.Range)
	}
}

// Go-to-def on the interface name in an `impl Iface for Type { ... }` block
// should jump to the interface declaration.
func TestFindDefinition_OnImplHeaderInterfaceResolvesToInterface(t *testing.T) {
	input := `interface Speech {
  fn speak(s: self): String
}

struct Dog {
  name: String
}

impl Speech for Dog {
  fn speak(d: Dog): String {
    d.name
  }
}

`
	uri := "file:///test.nomi"
	s := NewServer()
	s.docs.Open(uri, input)

	// `Speech` in `impl Speech for Dog` is on 1-based line 9, col 6.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 8, Character: 7},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `Speech` in `impl Speech for Dog`, got nil")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	// `interface Speech` is on 1-based line 1 → LSP line 0. The declaration
	// is in the same file, so the URI matches and the range points at the
	// interface name, not the click position.
	if loc.Range.Start.Line != 0 {
		t.Errorf("expected go-to-def to land on the interface decl (LSP line 0), got Range=%v", loc.Range)
	}
}

// Go-to-def on the interface name in an `impl Iface for T` block whose
// interface comes from the stdlib (e.g. `impl Display for AppEnv`) should
// jump to the interface declaration in the stdlib file, the same way
// `derive Iface` does. Until the builder walks the impl block's interface
// name as a reference, SymbolAt returns nil at that position and Zed's
// editor silently falls back to the references list.
func TestFindDefinition_OnImplHeaderImportedInterfaceResolvesToInterface(t *testing.T) {
	input := `import {
  std/display.Display
}

pub struct AppEnv {
  context: Context
}

impl Display for AppEnv {
  fn to_string(_env: AppEnv): String {
    "env"
  }
}

`
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// `Display` on the impl header lives at 1-based line 10, col 6.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 9, Character: 6},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `Display` on the impl-block line, got nil — clicking the interface name should jump to the interface decl")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("display"))
	if loc.URI != wantURI {
		t.Errorf("expected URI %q (stdlib display.nomi), got %q", wantURI, loc.URI)
	}
	if loc.Range.Start.Line == 0 && loc.Range.Start.Character == 0 {
		t.Errorf("Location is the top of the file (0:0) — definition wasn't resolved to the real Pos. URI=%s, Range=%v", loc.URI, loc.Range)
	}
}

// Go-to-def on the `self` marker in a drill-through selective import
// (`import std/maybe.Maybe.{self, Some, None}`) should land on the
// enum's real definition inside maybe.nomi, the same place a click on
// `Maybe` (the trailing path segment) goes.
func TestFindDefinition_SelfMarkerDrillThroughStdlib(t *testing.T) {
	input := "import std/maybe.Maybe.{self, Some, None}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// `self` sits at 1-based cols 25–28; LSP 0-based char anywhere
	// in [24, 27] hits it.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 0, Character: 25},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `self`, got nil")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("maybe"))
	if loc.URI != wantURI {
		t.Errorf("expected URI %q, got %q", wantURI, loc.URI)
	}
	if loc.Range.Start.Line == 0 && loc.Range.Start.Character == 0 {
		t.Errorf("Location is the top of the file (0:0) — drill-through self should land on the enum definition. URI=%s, Range=%v", loc.URI, loc.Range)
	}
}

// Go-to-def on the method name of an interface-qualified call whose
// receiver type is statically concrete (`Display.to_string(42)` — the
// arg is a concrete Int) should land on the *concrete impl* of that
// method for that type (std/int.nomi's `to_string`), not the abstract
// `Display.to_string` interface-method declaration. The interface decl
// answers "what is the contract"; go-to-def should answer "where does
// this call actually land."
func TestFindDefinition_InterfaceQualifiedCallResolvesToConcreteImpl(t *testing.T) {
	input := "import std/io\n" +
		"\n" +
		"fn main() {\n" +
		"  io.print(Display.to_string(42))\n" +
		"}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// `to_string` in `Display.to_string(42)` sits on line 4 (1-based),
	// cols 20–28; LSP 0-based line 3, char anywhere in [19, 27]. Use 22.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 3, Character: 22},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `to_string`, got nil")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("int"))
	if loc.URI != wantURI {
		t.Errorf("expected go-to-def to land in std/int.nomi (%q), got %q — it likely resolved to the Display interface decl instead", wantURI, loc.URI)
	}
}

// Same as the Int case above, but the concrete impl is an `host fn`
// inside `impl Display for Float` (std/float.nomi) rather than a Nomi
// `fn`. Go-to-def must land on the extern impl method in std/float.nomi,
// not the Display interface decl — block-form EXTERN interface impls are
// navigable dispatch targets too.
func TestFindDefinition_InterfaceQualifiedCall_ExternImpl_ResolvesToConcreteImpl(t *testing.T) {
	input := "import std/io\n" +
		"\n" +
		"fn main() {\n" +
		"  io.print(Display.to_string(3.14))\n" +
		"}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// `to_string` in `Display.to_string(3.14)` — line 4 (1-based), cols
	// 20–28; LSP 0-based line 3, char 22.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 3, Character: 22},
		},
	}
	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `to_string`, got nil")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("float"))
	if loc.URI != wantURI {
		t.Errorf("expected go-to-def to land in std/float.nomi (%q), got %q — the extern impl likely didn't resolve, falling back to the Display interface decl", wantURI, loc.URI)
	}
}

// A DERIVED impl has no user-navigable source: Ordering's Display comes from
// `derive Display` (synthesized AST, no real location in comparable.nomi),
// so go-to-def on `Display.to_string(Ordering.Less)` falls back to the
// interface method declaration in std/display.nomi — the sane target when
// the concrete impl isn't written anywhere. (Bool's Display is written out
// in bool.nomi, so it is no longer the witness.)
func TestFindDefinition_InterfaceQualifiedCall_DerivedImpl_FallsBackToInterface(t *testing.T) {
	input := "import std/io\n" +
		"\n" +
		"fn main() {\n" +
		"  io.print(Display.to_string(Ordering.Less))\n" +
		"}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 3, Character: 22},
		},
	}
	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("display"))
	if loc.URI != wantURI {
		t.Errorf("expected go-to-def to fall back to the interface decl in std/display.nomi (%q), got %q", wantURI, loc.URI)
	}
}

// The concrete-receiver dispatch resolution also covers user types whose
// impl lives in the entry file (home module key ""): go-to-def on
// `Display.to_string(greeter)` lands on the entry file's `to_string`
// impl, not the interface decl. Exercises the "" → open-doc-URI branch
// of moduleKeyToURI.
func TestFindDefinition_InterfaceQualifiedCallResolvesToUserImpl(t *testing.T) {
	input := "import std/io\n" +
		"\n" +
		"pub struct Greeter { name: String }\n" +
		"\n" +
		"impl Display for Greeter {\n" +
		"  fn to_string(g: self): String { \"hi ${g.name}\" }\n" +
		"}\n" +
		"\n" +
		"fn main() {\n" +
		"  greeter = Greeter{name: \"Alice\"}\n" +
		"  io.print(Display.to_string(greeter))\n" +
		"}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// `to_string` in `Display.to_string(greeter)` — line 11 (1-based),
	// LSP 0-based line 10, char 22 (within the `to_string` token).
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 10, Character: 22},
		},
	}
	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	if loc.URI != protocol.DocumentUri(uri) {
		t.Errorf("expected go-to-def to stay in the entry file (%q), got %q", uri, loc.URI)
	}
	// The `fn to_string(g: self)` impl method is on line 6 (1-based) →
	// 0-based line 5; indented 2 spaces, so `to_string` starts at 0-based
	// char 5 (`  fn ` = chars 0–4, name begins at char 5).
	if loc.Range.Start.Line != 5 || loc.Range.Start.Character != 5 {
		t.Errorf("expected the user impl at line 5 char 5 (0-based), got line %d char %d", loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

// When the receiver of an interface-qualified call is generic (no single
// statically-known concrete type), go-to-def must fall back to the
// interface method declaration rather than guessing an impl. Locks the
// decision that we only redirect to an impl for concrete receivers.
func TestFindDefinition_InterfaceQualifiedCallGenericFallsBackToInterface(t *testing.T) {
	input := "interface Showable { fn show(value: self): String }\n" +
		"struct User { name: String }\n" +
		"impl Showable for User {\n" +
		"  fn show(value: self): String { value.name }\n" +
		"}\n" +
		"fn render<T>(value: T): String where T: Showable {\n" +
		"  Showable.show(value)\n" +
		"}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)

	// `show` in `Showable.show(value)` — line 7 (1-based) → LSP 0-based
	// line 6, char 13 (within the `show` token).
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 6, Character: 13},
		},
	}
	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	if loc.URI != protocol.DocumentUri(uri) {
		t.Errorf("expected fallback to stay in the same file (%q), got %q", uri, loc.URI)
	}
	// The interface method `show` is on line 1 (0-based line 0). The User
	// impl method is now on line 4 (0-based line 3) — landing there would
	// mean we wrongly resolved a concrete impl for a generic receiver.
	if loc.Range.Start.Line != 0 {
		t.Errorf("expected fallback to the interface method (0-based line 0), got line %d (line 5 = the User impl)", loc.Range.Start.Line)
	}
}

// Go-to-def on a stdlib module function must jump to the stdlib module file.
// Regression guard: stdlib symbols carry no SourceFile, so if the stdlib URI
// match in textDocumentDefinition misses one, targetURI silently stays the
// USER's file while the range carries the stdlib position, bumping the cursor to
// a nonsense line (clamped to EOF in the editor).
func TestFindDefinition_OnStdlibModuleFunctionResolvesToStdlibFile(t *testing.T) {
	input := `import std/io
import std/lists

fn main() {
  first = List.head([3, 1, 2])
  io.inspect(first)
}
`
	uri := "file:///test.nomi"
	s := NewServer()
	s.docs.Open(uri, input)

	// `head` in `List.head(...)` on line 5 (1-based): `  first = lists.` puts
	// `head` at chars 16-20 (0-based). Click mid-token.
	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 4, Character: 17},
		},
	}

	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a Location for `head` in `List.head(...)`, got nil")
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("lists"))
	if loc.URI != wantURI {
		t.Errorf("go-to-def on List.head routed to %q (range %v), want stdlib %q — a user-file URI here is the cursor-bumps-to-EOF bug", loc.URI, loc.Range, wantURI)
	}
}

func TestFindDefinition_OnImplMethodResolvesToInterfaceMethod(t *testing.T) {
	input := `interface Speech {
  fn speak(value: self): String
}

struct Dog {
  name: String
}

impl Speech for Dog {
  fn speak(d: Dog): String {
    d.name
  }
}

`
	uri := "file:///test.nomi"
	s := NewServer()
	s.docs.Open(uri, input)

	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			// `speak` in the implementation method.
			Position: protocol.Position{Line: 9, Character: 7},
		},
	}
	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	if loc.URI != protocol.DocumentUri(uri) {
		t.Fatalf("expected same-file interface method target, got %q", loc.URI)
	}
	if loc.Range.Start.Line != 1 {
		t.Fatalf("expected target on interface method line 1, got %v", loc.Range)
	}
}

func TestFindDefinition_OnStdlibImplMethodResolvesToInterfaceMethod(t *testing.T) {
	rangeFile := filepath.Join("..", "..", "std", "ranges.nomi")
	srcBytes, err := os.ReadFile(rangeFile)
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	idx := strings.Index(src, "fn to_string")
	if idx < 0 {
		t.Fatal("expected std/ranges.nomi to define to_string")
	}
	line := uint32(strings.Count(src[:idx], "\n"))
	lineStart := strings.LastIndex(src[:idx], "\n") + 1
	char := uint32(idx-lineStart) + 3 // inside `to_string`

	absRange, err := filepath.Abs(rangeFile)
	if err != nil {
		t.Fatal(err)
	}
	uri := "file://" + absRange
	s := NewServer()
	s.docs.Open(uri, src)

	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: line, Character: char},
		},
	}
	result, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition request returned error: %v", err)
	}
	loc, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", result)
	}
	wantURI := protocol.DocumentUri(s.std.FileURI("display"))
	if loc.URI != wantURI {
		t.Fatalf("expected Display.to_string in std/display.nomi, got %q range %v", loc.URI, loc.Range)
	}
}
