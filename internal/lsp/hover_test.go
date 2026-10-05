package lsp

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func buildSymbol(src string, name string) *analysis.Symbol {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	if sym := file.ModuleScope.LookupLocal(name); sym != nil {
		return sym
	}
	return nil
}

func checkedFile(src string) *analysis.FileAnalysis {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)
	return file
}

func checkedLoweredFile(src string) *analysis.FileAnalysis {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = analysis.LowerDerives(nodes)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)
	return file
}

func TestHover_FuncWithParamsAndReturn(t *testing.T) {
	sym := buildSymbol("/// Doubles a number.\nfn double(n: Int): Int { n + n }", "double")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nfn double(n: Int): Int\n```\n\nDoubles a number."
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_FuncNoReturnNoDoc(t *testing.T) {
	sym := buildSymbol("fn greet(name: String) { io.print(name) }", "greet")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nfn greet(name: String)\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_FuncWithTypeParams(t *testing.T) {
	sym := buildSymbol("fn map<T, U>(list: List<T>, f: (T) -> U): List<U> { _ = f; list }", "map")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nfn map<T, U>(list: List<T>, f: (T) -> U): List<U>\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_FuncNoParams(t *testing.T) {
	sym := buildSymbol("fn main() { 42 }", "main")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nfn main()\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_AttachedAssertCodeUsesNormalReferences(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `//! assert ▮ready_label() == "[ready]"
fn ready_label(): String {
  "[ready]"
}
`)
	uri := "file:///attached_assert_hover.nomi"

	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected symbol at attached assert call site")
	}
	got := renderHover(sym)
	expected := "```nomi\nfn ready_label(): String\n```"
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_AttachedAssertTypeQualifiedImplFunction(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `struct Box {
  n: Int
}

impl Display for Box {
  //! assert Box.▮to_string(Box{n: 3}) == "box:3"
  fn to_string(b: Box): String {
    "box:" + Int.to_string(b.n)
  }
}
`)
	uri := "file:///attached_impl_assert_hover.nomi"

	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected symbol at attached impl assert call site")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "fn to_string(b: Box): String") {
		t.Errorf("expected hover to render concrete impl signature, got:\n%s", got)
	}
}

func TestHover_AttachedAssertBoundaryUsesDeclaration(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `host type AssertionFailure

//! ▮assert ready_label() == "[ready]"
fn ready_label(): String {
  "[ready]"
}
`)
	uri := "file:///attached_assert_boundary_hover.nomi"

	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected symbol at attached assert keyword")
	}
	got := renderHover(sym)
	expected := "```nomi\nassert: Bool -> Bool\n```\n\nRequires `True`. On `False`, returns `AssertionFailure` and unwinds to `attached test for ready_label`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_InlineGoHelperInGoBody(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `fn parse(raw: String): Result<Int, String> go {
  return ▮toNomiErrString[int64]("bad")
}
`)
	uri := "file:///inline_go_helper_hover.nomi"
	s := NewServer()
	s.docs.Open(uri, input)

	res, err := s.textDocumentHover(nil, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position: protocol.Position{
				Line:      uint32(pos.Line - 1),
				Character: uint32(pos.Col - 1),
			},
		},
	})
	if err != nil {
		t.Fatalf("hover error: %v", err)
	}
	if res == nil {
		t.Fatal("expected hover content for inline Go helper")
	}
	mc, ok := res.Contents.(protocol.MarkupContent)
	if !ok {
		t.Fatalf("expected MarkupContent, got %T", res.Contents)
	}
	if !strings.Contains(mc.Value, "func toNomiErrString[T any](message string) (T, error)") ||
		!strings.Contains(mc.Value, "Return an Err from a string.") {
		t.Fatalf("unexpected hover:\n%s", mc.Value)
	}
}

// When the function has no return annotation but BuildTypes ran (so the
// symbol carries an inferred FuncType), hover should append the inferred
// return type rather than show the bare `fn name(...)` shape.
func TestHover_FuncNoReturnAnnotationUsesInferredType(t *testing.T) {
	src := "fn shadow_demo() { 42 }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)
	sym := fa.ModuleScope.LookupLocal("shadow_demo")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nfn shadow_demo(): Unit\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

// A function with irrefutable destructuring params must render the source
// patterns (`Dur(x)`, `Point{x, y}`, `(a, b)`) in hover — not the internal
// `__destr_<line>_<col>` placeholder used as a binding slot.
func TestHover_FuncDestructureParams(t *testing.T) {
	src := "type Dur Int\n" +
		"struct Point {\n\tx: Int\n\ty: Int\n}\n" +
		"enum Wrapper {\n\tOnly Int\n}\n" +
		"fn subtract(Dur(x), Dur(y)): Dur { Dur(x - y) }\n" +
		"fn sum_point(Point{x, y}): Int { x + y }\n" +
		"fn scale_point({x, y}: Point): Int { x * y }\n" +
		"fn add_pair((a, b): (Int, Int)): Int { a + b }\n" +
		"fn unwrap(Wrapper.Only(n)): Int { n }"
	wants := map[string]string{
		"subtract":    "```nomi\nfn subtract(Dur(x), Dur(y)): Dur\n```",
		"sum_point":   "```nomi\nfn sum_point(Point{x, y}): Int\n```",
		"scale_point": "```nomi\nfn scale_point({x, y}: Point): Int\n```",
		"add_pair":    "```nomi\nfn add_pair((a, b): (Int, Int)): Int\n```",
		"unwrap":      "```nomi\nfn unwrap(Wrapper.Only(n)): Int\n```",
	}
	for name, want := range wants {
		sym := buildSymbol(src, name)
		if sym == nil {
			t.Fatalf("symbol %q not found", name)
		}
		got := renderHover(sym)
		if got != want {
			t.Errorf("%s: got:\n%s\n\nexpected:\n%s", name, got, want)
		}
	}
}

// The inner binding of a destructuring param must be typed. `Dur` wraps Int,
// so `Dur(ns)` binds `ns: Int`.
// Regression: buildImplBlockTypes derived the destructure slot type only
// from the (absent) annotation, ignoring the self-typing pattern head, so
// the slot stayed nil and checkPattern never typed the binding — hover
// showed a bare `ns`. buildFuncType (top-level fns) never had this gap.
func TestHover_DestructureParamBindingInFunction(t *testing.T) {
	cases := map[string]string{
		"opaque distinct": "pub opaque type Dur Int\n\npub fn as_nanos(Dur(ns)): Int {\n  ns\n}",
		"plain distinct":  "type Dur Int\n\npub fn as_nanos(Dur(ns)): Int {\n  ns\n}",
	}
	for label, src := range cases {
		tokens := lexer.Lex(src)
		nodes, _ := parser.ParseWithRecovery(tokens)
		nodes, _ = analysis.LowerDerives(nodes)
		file := buildFile(nodes)
		analysis.BuildTypes(file, nodes)
		analysis.CheckTypes(file, nodes)

		var sym *analysis.Symbol
		for _, s := range file.Definitions {
			if s.Name == "ns" && s.Kind == analysis.SymbolBinding {
				sym = s
				break
			}
		}
		if sym == nil {
			t.Fatalf("%s: no `ns` binding symbol found", label)
		}
		got := renderHover(sym)
		expected := "```nomi\nns: Int\n```"
		if got != expected {
			t.Errorf("%s: got:\n%s\n\nexpected:\n%s", label, got, expected)
		}
	}
}

func TestHover_Struct(t *testing.T) {
	sym := buildSymbol("/// A user record.\nstruct User {\n\tname: String\n\tage: Int\n}", "User")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nstruct User {\n    name: String\n    age: Int\n}\n```\n\nA user record."
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

// An opaque struct hides its construction surface, so its hover renders
// the declaration head only (no `field` body), keeping the opaque note.
func TestHover_StructOpaqueHidesFields(t *testing.T) {
	sym := buildSymbol("/// A widget.\npub opaque struct Widget {\n\tsecret: Int\n}", "Widget")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nstruct Widget\n```\n\n*opaque — construction surface is private to its defining module*\n\nA widget."
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

// The opaque head still carries type parameters.
func TestHover_StructOpaqueWithTypeParams(t *testing.T) {
	sym := buildSymbol("pub opaque struct Wrap<T> {\n\tinner: T\n}", "Wrap")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nstruct Wrap<T>\n```\n\n*opaque — construction surface is private to its defining module*"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_StructWithTypeParams(t *testing.T) {
	sym := buildSymbol("struct Pair<T, U> {\n\tfirst: T\n\tsecond: U\n}", "Pair")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nstruct Pair<T, U> {\n    first: T\n    second: U\n}\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_TypeShowsImplementedInterfaces(t *testing.T) {
	input := `interface Tagged {
  fn tag(value: self): String
}

struct User {
  name: String
}

impl Tagged for User {
  fn tag(_value: User): String {
    "user"
  }
}
`
	uri := "file:///test.nomi"
	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}
	sym := doc.Analysis.ModuleScope.LookupLocal("User")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	got := renderHoverWithAnalysis(sym, doc.Analysis)
	if !strings.Contains(got, "*impl* `Tagged`") {
		t.Errorf("expected User hover to list Tagged, got:\n%s", got)
	}
	if strings.Contains(got, "`Debug`") {
		t.Errorf("hover should not list compiler-synthesized Debug, got:\n%s", got)
	}
}

func TestHover_TypeShowsExplicitDebugImplementation(t *testing.T) {
	input := `struct User {
  name: String
}

impl Debug for User {
  fn inspect(_value: User): String {
    "User"
  }
}
`
	uri := "file:///test.nomi"
	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}
	sym := doc.Analysis.ModuleScope.LookupLocal("User")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	got := renderHoverWithAnalysis(sym, doc.Analysis)
	if !strings.Contains(got, "*impl* `Debug`") {
		t.Errorf("expected User hover to list explicit Debug, got:\n%s", got)
	}
}

func TestHover_Enum(t *testing.T) {
	sym := buildSymbol("enum Color {\n\tRed\n\tGreen\n\tBlue\n}", "Color")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nenum Color {\n    Red\n    Green\n    Blue\n}\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

// An opaque enum hides its variants the same way an opaque struct hides
// its fields — head-only, with the opaque note.
func TestHover_EnumOpaqueHidesVariants(t *testing.T) {
	sym := buildSymbol("pub opaque enum Token {\n\tA\n\tB\n}", "Token")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nenum Token\n```\n\n*opaque — construction surface is private to its defining module*"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_EnumWithData(t *testing.T) {
	sym := buildSymbol("/// An optional value.\nenum Maybe<T> {\n\tSome T\n\tNone\n}", "Maybe")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nenum Maybe<T> {\n    Some T\n    None\n}\n```\n\nAn optional value."
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_InterfaceMethod(t *testing.T) {
	src := `interface Greeting {
	fn greet(value: self): String
}
fn main() {
	Greeting.greet("hello")
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)

	// "greet" in Greeting.greet call — find its position from references
	var methodSym *analysis.Symbol
	for _, sym := range file.References {
		if sym.Name == "greet" && sym.Kind == analysis.SymbolInterfaceMethod {
			methodSym = sym
			break
		}
	}
	if methodSym == nil {
		t.Fatal("expected reference to interface method 'greet'")
	}
	result := renderHover(methodSym)
	expected := "```nomi\nfn greet(value: self): String\n```\n\n*interface* `Greeting`"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_InterfaceMethodGenericWhereSignature(t *testing.T) {
	src := `interface Ranked<T> {
	fn prefer<K>(value: self, lhs: K, rhs: K): Bool where K: Comparable
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)

	var methodSym *analysis.Symbol
	for _, sym := range file.Definitions {
		if sym.Name == "prefer" && sym.Kind == analysis.SymbolInterfaceMethod {
			methodSym = sym
			break
		}
	}
	if methodSym == nil {
		t.Fatal("expected definition for interface method 'prefer'")
	}

	result := renderHover(methodSym)
	expected := "```nomi\nfn prefer<K>(value: self, lhs: K, rhs: K): Bool where K: Comparable\n```\n\n*interface* `Ranked`"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_InterfaceMethodSymbolAt(t *testing.T) {
	src := `interface Greeting {
	fn greet(value: self): String
}
fn main() {
	Greeting.greet("hello")
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)

	// Find the position of "greet" in "Greeting.greet" on line 5
	var greetPos analysis.Pos
	found := false
	for pos, sym := range file.References {
		if sym.Name == "greet" && sym.Kind == analysis.SymbolInterfaceMethod {
			greetPos = pos
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected reference to interface method 'greet'")
	}

	// SymbolAt should find it
	sym := file.SymbolAt(greetPos)
	if sym == nil {
		t.Fatal("SymbolAt returned nil for interface method reference")
	}
	if sym.Kind != analysis.SymbolInterfaceMethod {
		t.Errorf("expected SymbolInterfaceMethod, got %v", sym.Kind)
	}
	if sym.Name != "greet" {
		t.Errorf("expected 'greet', got '%s'", sym.Name)
	}
}

// Hover on the interface name in an `impl Iface for Type { ... }` block
// should return the interface's hover (its signature + doc).
func TestHover_ImplHeaderInterfaceSymbolAt(t *testing.T) {
	src := `/// Things that can speak.
interface Speech {
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
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)

	// Find the `Speech` reference at the `impl Speech for Dog` header —
	// 1-based line 10 (the `///` doc comment is line 1).
	var headerPos analysis.Pos
	found := false
	for pos, sym := range file.References {
		if sym.Name == "Speech" && sym.Kind == analysis.SymbolInterface && pos.Line == 10 {
			headerPos = pos
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected a Speech interface reference at the impl header (line 10)")
	}

	sym := file.SymbolAt(headerPos)
	if sym == nil {
		t.Fatal("SymbolAt returned nil at the impl block header's interface position")
	}
	result := renderHover(sym)
	if !strings.Contains(result, "interface Speech") {
		t.Errorf("expected hover to contain the interface signature, got:\n%s", result)
	}
	if !strings.Contains(result, "Things that can speak.") {
		t.Errorf("expected hover to contain the interface doc comment, got:\n%s", result)
	}
}

func TestHover_Interface(t *testing.T) {
	sym := buildSymbol("/// Can be displayed as text.\ninterface Display {\n\tfn display(value: self): String\n}", "Display")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\ninterface Display {\n    fn display(value: self): String\n}\n```\n\nCan be displayed as text."
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_InterfaceStructuralRequirements(t *testing.T) {
	sym := buildSymbol(`/// Event-shaped values.
interface EventLike {
  field source: String
  fn label(value: self): String
}`, "EventLike")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\ninterface EventLike {\n    field source: String\n    fn label(value: self): String\n}\n```\n\nEvent-shaped values."
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_TypeDef(t *testing.T) {
	sym := buildSymbol("/// User ID type.\ntype UserId Int", "UserId")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\ntype UserId Int\n```\n\nUser ID type."
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_TypeDefZeroSized(t *testing.T) {
	sym := buildSymbol("type Void", "Void")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\ntype Void\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_TypeDefOpaque(t *testing.T) {
	sym := buildSymbol(
		"/// Positive integer.\npub opaque type PositiveInt Int",
		"PositiveInt",
	)
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	if !strings.Contains(result, "type PositiveInt Int") {
		t.Errorf("expected sig in hover, got:\n%s", result)
	}
	if !strings.Contains(result, "opaque") {
		t.Errorf("expected hover to surface opacity, got:\n%s", result)
	}
	if !strings.Contains(result, "Positive integer.") {
		t.Errorf("expected docstring in hover, got:\n%s", result)
	}
}

func TestHover_TypeDefNonOpaqueHasNoOpaqueNote(t *testing.T) {
	// Sanity check: non-opaque distinct types should not surface
	// "opaque" in hover.
	sym := buildSymbol("type UserId Int", "UserId")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	if strings.Contains(result, "opaque") {
		t.Errorf("expected no opaque note for non-opaque type, got:\n%s", result)
	}
}

func TestHover_TypeAlias(t *testing.T) {
	sym := buildSymbol("typealias Name String", "Name")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\ntypealias Name String\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_Once(t *testing.T) {
	sym := buildSymbol("/// The answer.\nonce answer = 42", "answer")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nonce answer = 42\n```\n\nThe answer."
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_Param(t *testing.T) {
	src := "fn add(x: Int, y: Int): Int { x + y }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	var sym *analysis.Symbol
	for _, s := range file.Definitions {
		if s.Name == "x" && s.Kind == analysis.SymbolParam {
			sym = s
			break
		}
	}
	if sym == nil {
		t.Fatal("param not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nx: Int\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_ParamNoType(t *testing.T) {
	src := "fn identity(x) { x }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	var sym *analysis.Symbol
	for _, s := range file.Definitions {
		if s.Name == "x" && s.Kind == analysis.SymbolParam {
			sym = s
			break
		}
	}
	if sym == nil {
		t.Fatal("param not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nx\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_Binding(t *testing.T) {
	src := "fn main() { result = 42 }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	var sym *analysis.Symbol
	for _, s := range file.Definitions {
		if s.Name == "result" && s.Kind == analysis.SymbolBinding {
			sym = s
			break
		}
	}
	if sym == nil {
		t.Fatal("binding not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nresult\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_BindingWithStructType(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
fn main() { p = Point{x: 1, y: 2} }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)
	var sym *analysis.Symbol
	for _, s := range file.Definitions {
		if s.Name == "p" && s.Kind == analysis.SymbolBinding {
			sym = s
			break
		}
	}
	if sym == nil {
		t.Fatal("binding not found")
	}
	result := renderHover(sym)
	expected := "```nomi\np: Point\n\nstruct Point {\n    x: Int\n    y: Int\n}\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_BindingWithGenericStructTypeInstantiatesFields(t *testing.T) {
	src := `struct Box<T> {
  value: T
}

fn main() {
  box = Box{value: 42}
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)
	var sym *analysis.Symbol
	for _, s := range file.Definitions {
		if s.Name == "box" && s.Kind == analysis.SymbolBinding {
			sym = s
			break
		}
	}
	if sym == nil {
		t.Fatal("binding not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nbox: Box<Int>\n\nstruct Box<Int> {\n    value: Int\n}\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_TestGroupSetupBindingInNestedTest(t *testing.T) {
	src := `struct Config {
  env: String
}

tests "config" {
  setup {
    {cfg: Config{env: "test"}}
  }

  test "uses cfg", {cfg} {
    assert cfg.env == "test"
  }
}
`
	lib := std.Load()
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	typeErrs := analysis.BuildTypes(file, nodes)
	checkErrs := analysis.CheckTypes(file, nodes)
	if len(typeErrs)+len(checkErrs) > 0 {
		t.Fatalf("unexpected type errors: %v %v", typeErrs, checkErrs)
	}

	sym := file.SymbolAt(analysis.Pos{Line: 11, Col: 12})
	if sym == nil {
		t.Fatal("expected cfg symbol in nested test")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "cfg: Config") {
		t.Fatalf("expected cfg hover to include type, got:\n%s", got)
	}
}

// A call-site hover on a generic fn whose param destructures must also
// render the pattern, not the `__destr_*` slot (the renderFuncSigFromType
// path, distinct from the definition-hover RenderFuncSig path).
func TestHover_DestructureParamCallSiteInstantiated(t *testing.T) {
	src := "fn fst<T>((a, b): (T, T)): T { a }\n" +
		"fn main() { fst((1, 2)) }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)

	var callSym *analysis.Symbol
	for pos, sym := range file.References {
		if sym.Name == "fst" && pos.Line == 2 {
			callSym = sym
			break
		}
	}
	if callSym == nil {
		t.Fatal("expected reference to fst on line 2")
	}
	result := renderHover(callSym)
	expected := "```nomi\nfn fst((a, b): (Int, Int)): Int\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_GenericCallSiteShowsInstantiatedType(t *testing.T) {
	src := `fn identity<T>(x: T): T { x }
fn main() { identity(42) }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)

	// Find the reference symbol for "identity" on line 2
	var callSym *analysis.Symbol
	for pos, sym := range file.References {
		if sym.Name == "identity" && pos.Line == 2 {
			callSym = sym
			break
		}
	}
	if callSym == nil {
		t.Fatal("expected reference to identity on line 2")
	}

	result := renderHover(callSym)
	expected := "```nomi\nfn identity(x: Int): Int\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_GenericCallSiteInstantiatesWhereClause(t *testing.T) {
	src := `type Day Int
type Days Int

impl Add<Days, Day> for Day {
  fn add(lhs: Day, rhs: Days): Day {
    _ = rhs
    lhs
  }
}

fn plus<L, R, Out>(lhs: L, rhs: R): Out where L: Add<R, Out> {
  lhs + rhs
}

fn main(): Day {
  plus(Day(10), Days(4))
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	file := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	file.TypeErrors = append(file.TypeErrors, analysis.BuildTypes(file, nodes)...)
	file.TypeErrors = append(file.TypeErrors, analysis.CheckTypes(file, nodes)...)
	if len(file.TypeErrors) > 0 {
		t.Fatalf("unexpected type errors: %v", file.TypeErrors)
	}

	var callSym *analysis.Symbol
	for _, sym := range file.References {
		if sym.Name == "plus" && sym.CallType != nil {
			callSym = sym
			break
		}
	}
	if callSym == nil {
		t.Fatal("expected call-site reference to plus")
	}

	result := renderHover(callSym)
	expected := "```nomi\nfn plus(lhs: Day, rhs: Days): Day where Day: Add<Days, Day>\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_GenericCallSiteInstantiatesWhereClauseThroughList(t *testing.T) {
	src := `interface Mark {}
impl Mark for Int {}

fn keep<T>(xs: List<T>): List<T> where T: Mark {
  xs
}

fn main(): List<Int> {
  keep([1, 2])
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	file.TypeErrors = append(file.TypeErrors, analysis.BuildTypes(file, nodes)...)
	file.TypeErrors = append(file.TypeErrors, analysis.CheckTypes(file, nodes)...)
	if len(file.TypeErrors) > 0 {
		t.Fatalf("unexpected type errors: %v", file.TypeErrors)
	}

	var callSym *analysis.Symbol
	for _, sym := range file.References {
		if sym.Name == "keep" && sym.CallType != nil {
			callSym = sym
			break
		}
	}
	if callSym == nil {
		t.Fatal("expected call-site reference to keep")
	}

	result := renderHover(callSym)
	expected := "```nomi\nfn keep(xs: List<Int>): List<Int> where Int: Mark\n```"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

// Hovering on `Ok` in a case pattern like `Some(Ok(n)) -> n` should show
// the instantiated variant type — the substituted T/E from the scrutinee
// — not the raw `type Result<T, E>` definition. The variant pattern
// reference should carry CallType the same way a call-site reference does.
// Hovering on a generic function declaration with an interface bound should show
// the bound in the signature's `where` clause.
func TestHover_FuncWithInterfaceBound(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
fn identity<T>(x: T): T where T: Showable { x }`
	sym := buildSymbol(src, "identity")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "fn identity<T>(x: T): T where T: Showable") {
		t.Errorf("expected hover to show `where T: Showable`, got:\n%s", got)
	}
}

// Hovering on the bound interface `Showable` in `where T: Showable` should
// resolve to the interface symbol (not be a hover-blank).
func TestHover_BoundInterfaceReference(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
fn identity<T>(x: T): T where T: Showable { x }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	// `Showable` in the bound clause is on line 2. Find by name.
	var boundRef *analysis.Symbol
	for _, sym := range fa.References {
		if sym != nil && sym.Name == "Showable" && sym.Kind == analysis.SymbolInterface {
			boundRef = sym
			break
		}
	}
	if boundRef == nil {
		t.Fatal("expected a Showable interface reference (probably from the bound clause)")
	}
	got := renderHover(boundRef)
	if !strings.Contains(got, "interface Showable") {
		t.Errorf("expected hover on bound `Showable` to render the interface, got:\n%s", got)
	}
}

// Hovering on the type-param identifier `T` itself (e.g. the `T` in
// body or `where` clause) should show its bound, not just the
// bare name.
func TestHover_TypeParamWithBound(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
fn identity<T>(x: T): T where T: Showable { x }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)
	// The `T` in the param annotation `x: T` should resolve to the
	// type-param symbol. Search references for a SimpleType named T.
	var tSym *analysis.Symbol
	for _, sym := range fa.References {
		if sym != nil && sym.Name == "T" {
			tSym = sym
			break
		}
	}
	if tSym == nil {
		t.Fatal("could not find type-param T reference")
	}
	got := renderHoverWithAnalysis(tSym, fa)
	if !strings.Contains(got, "Showable") {
		t.Errorf("expected hover for T to mention `Showable`, got:\n%s", got)
	}
}

// When an interface-qualified call (`Iface.method(...)`) appears inside
// generic code, hovering on the method name should render the interface
// method's contract — not an arbitrary impl. The arg's type is the
// generic param `T`, no specific impl is statically picked at this call
// site, so reaching for one is misleading.
func TestHover_InterfaceMethodInGenericCode(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
struct User { name: String }
impl Showable for User {
  fn show(value: User): String { value.name }
}
fn render<T>(value: T): String where T: Showable {
  Showable.show(value)
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)
	analysis.CheckTypes(fa, nodes)

	// Find the reference for `show` on the call line (line 7).
	var methodRef *analysis.Symbol
	for pos, sym := range fa.References {
		if pos.Line == 7 && sym.Name == "show" {
			methodRef = sym
			break
		}
	}
	if methodRef == nil {
		t.Fatal("expected reference to `show` on line 7")
	}
	got := renderHover(methodRef)
	if strings.Contains(got, "value: User") {
		t.Errorf("hover leaked the User impl into a generic context — got:\n%s", got)
	}
	if !strings.Contains(got, "value: self") && !strings.Contains(got, "value: T") {
		t.Errorf("expected hover to mention `self` or `T` (the abstract receiver), got:\n%s", got)
	}
}

// When multiple impls exist for an interface method, an interface-
// qualified call (`Iface.method(...)`) should resolve to the interface
// method itself rather than whichever impl happens to be registered
// last — last-write-wins on a map keyed only by interface+method is
// arbitrary and misleading.
func TestHover_InterfaceMethodMultipleImpls(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
struct A {}
struct B {}
impl Showable for A {
  fn show(_value: A): String { "a" }
}
impl Showable for B {
  fn show(_value: B): String { "b" }
}
fn dump<T>(value: T): String where T: Showable {
  Showable.show(value)
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)
	analysis.CheckTypes(fa, nodes)

	var methodRef *analysis.Symbol
	for pos, sym := range fa.References {
		if pos.Line == 11 && sym.Name == "show" {
			methodRef = sym
			break
		}
	}
	if methodRef == nil {
		t.Fatal("expected reference to `show` on line 15")
	}
	got := renderHover(methodRef)
	if strings.Contains(got, "value: A") || strings.Contains(got, "value: B") {
		t.Errorf("hover picked an arbitrary impl (A or B) — got:\n%s", got)
	}
}

// Hover on the method name of an interface-qualified call with a
// statically concrete receiver (`Display.to_string(42)`) should render
// the concrete impl's signature (std/int.nomi's `to_string(n: Int)`,
// param `n`), matching where go-to-def lands — not the abstract
// `to_string(value: self)` interface contract (param `value`). Block-form
// impls spell concrete receiver types, and hover follows that concrete source.
// Keeps hover and go-to-def consistent for concrete dispatch. Uses the server
// harness (not BuildFile) because the dispatch target is only recorded when
// ProjectImpls is populated.
func TestHover_InterfaceQualifiedCallRendersConcreteImpl(t *testing.T) {
	input := "import std/io\n" +
		"\n" +
		"fn main() {\n" +
		"  io.print(Display.to_string(42))\n" +
		"}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}

	// `to_string` in `Display.to_string(42)` — line 4, col 20 (1-based);
	// col 22 lands inside the token.
	sym := doc.Analysis.SymbolAt(analysis.Pos{Line: 4, Col: 22})
	if sym == nil {
		t.Fatal("no symbol at the to_string call-site position")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "to_string(n: Int)") {
		t.Errorf("expected hover to render the concrete Int impl (param `n`), got:\n%s", got)
	}
	if strings.Contains(got, "value: self") {
		t.Errorf("hover rendered the abstract interface contract (`value: self`) instead of the concrete impl, got:\n%s", got)
	}
	if strings.Contains(got, "n: self") {
		t.Errorf("hover rendered the old concrete `self` spelling instead of the concrete receiver type, got:\n%s", got)
	}
}

// Same as the Int case above, but the concrete impl is an `host fn`
// inside `impl Display for Float` (std/float.nomi). Hover must render that
// extern impl's signature (`to_string(x: Float)`, param `x`), not the
// abstract interface contract (`to_string(value: self)`, param `value`) —
// block-form EXTERN interface impls render their concrete source too.
func TestHover_InterfaceQualifiedCall_ExternImpl_RendersConcreteImpl(t *testing.T) {
	input := "import std/io\n" +
		"\n" +
		"fn main() {\n" +
		"  io.print(Display.to_string(3.14))\n" +
		"}\n"
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}

	// `to_string` in `Display.to_string(3.14)` — line 4, col 20 (1-based);
	// col 22 lands inside the token.
	sym := doc.Analysis.SymbolAt(analysis.Pos{Line: 4, Col: 22})
	if sym == nil {
		t.Fatal("no symbol at the to_string call-site position")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "to_string(x: Float)") {
		t.Errorf("expected hover to render the concrete Float extern impl (param `x`), got:\n%s", got)
	}
	if strings.Contains(got, "value: self") {
		t.Errorf("hover rendered the abstract interface contract (`value: self`) instead of the concrete extern impl, got:\n%s", got)
	}
	if strings.Contains(got, "x: self") {
		t.Errorf("hover rendered the old concrete `self` spelling instead of the concrete receiver type, got:\n%s", got)
	}
}

func TestHover_FileFunctionRendersConcreteSignature(t *testing.T) {
	inputWithMarker := `import std/int.{Int, PositiveInt}

fn main() {
  p = try Int.to_positive(5)
  PositiveInt.▮to_non_zero(p)
}
`
	input, pos := hoverMarkerPosition(t, inputWithMarker)
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("no symbol at the to_non_zero call-site position")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "fn to_non_zero(p: PositiveInt): NonZeroInt") {
		t.Errorf("expected hover to render concrete PositiveInt parameter type, got:\n%s", got)
	}
	if strings.Contains(got, "Maybe<self>") {
		t.Errorf("hover rendered the old concrete `self` spelling, got:\n%s", got)
	}
}

func TestHover_DerivedTypeQualifiedImplMethods(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantParts []string
	}{
		{
			name: "maybe equal",
			input: `test "hover" {
  assert Maybe.▮equal?(Some(4), Some(4))
}
`,
			wantParts: []string{"fn equal?", "Maybe<Int>", "Bool"},
		},
		{
			name: "result equal",
			input: `test "hover" {
  assert Result.▮equal?(Ok(4), Ok(4))
}
`,
			wantParts: []string{"fn equal?", "Result<Int, _>", "Bool"},
		},
		{
			name: "maybe hash",
			input: `test "hover" {
  assert Maybe.▮hash(Some(4)) == Maybe.hash(Some(4))
}
`,
			wantParts: []string{"fn hash", "Maybe<Int>", "Int"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, pos := hoverMarkerPosition(t, tc.input)
			uri := "file:///" + strings.ReplaceAll(tc.name, " ", "_") + ".nomi"

			s := NewServer()
			s.docs.Open(uri, input)
			doc := s.docs.Get(uri)
			if doc == nil {
				t.Fatal("doc not found after Open")
			}

			sym := doc.Analysis.SymbolAt(pos)
			if sym == nil {
				t.Fatal("no symbol at derived method call-site position")
			}
			got := renderHover(sym)
			for _, want := range tc.wantParts {
				if !strings.Contains(got, want) {
					t.Errorf("expected hover to contain %q, got:\n%s", want, got)
				}
			}
			if strings.Contains(got, "self") {
				t.Errorf("hover rendered the old concrete `self` spelling, got:\n%s", got)
			}
		})
	}
}

func TestHover_ImplMethodParamUsageRendersConcreteType(t *testing.T) {
	inputWithMarker := `interface Equal {
  fn equal?(a: self, b: self): Bool
}

enum Maybe<T> {
  Some T
  None
}

impl Equal for Maybe<T> {
  fn equal?(a: Maybe<T>, b: Maybe<T>): Bool {
    a == ▮b
  }
}
`
	input, pos := hoverMarkerPosition(t, inputWithMarker)
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("no symbol at impl method param usage")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "b: Maybe<T>") {
		t.Errorf("expected hover to render concrete impl receiver type, got:\n%s", got)
	}
	if strings.Contains(got, "b: self") {
		t.Errorf("hover rendered the old concrete `self` spelling, got:\n%s", got)
	}
}

func TestHover_BareImportedFunctionBeatsSynthesizedDebugMethod(t *testing.T) {
	inputWithMarker := `import std/io.inspect

interface Greeting {
  fn greet(value: self): String
}

struct Guest {
  name: String
}

impl Greeting for Guest {
  fn greet(guest: Guest): String {
    "Welcome, " + guest.name + "!"
  }
}

fn main() {
  result = 42
  ▮inspect(result)

  Guest{name: "Bob"}
  |> Greeting.greet()
  |> inspect()
}
`
	input, pos := hoverMarkerPosition(t, inputWithMarker)
	uri := "file:///test.nomi"

	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}
	if len(doc.Analysis.TypeErrors) != 0 {
		t.Fatalf("expected no type errors, got: %v", doc.Analysis.TypeErrors)
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("no symbol at the inspect call-site position")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "fn inspect(value: Int): Unit") {
		t.Errorf("expected hover to render imported io.inspect, got:\n%s", got)
	}
	if strings.Contains(got, "value: Guest") {
		t.Errorf("hover rendered synthesized Guest Debug.inspect instead of imported io.inspect, got:\n%s", got)
	}
}

func hoverMarkerPosition(t *testing.T, input string) (string, analysis.Pos) {
	t.Helper()
	const marker = "▮"
	idx := strings.Index(input, marker)
	if idx < 0 {
		t.Fatal("missing hover marker")
	}
	cleaned := strings.Replace(input, marker, "", 1)
	line, col := 1, 1
	for _, b := range []byte(input[:idx]) {
		if b == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return cleaned, analysis.Pos{Line: line, Col: col}
}

func TestHover_ImplBlockFunctionShowsImplementationRole(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		wantParts    []string
		blockedParts []string
	}{
		{
			name: "interface implementation function",
			input: `interface Display {
  fn to_string(value: self): String
}

struct Money {
  cents: Int
}

impl Display for Money {
  fn ▮to_string(m: Money): String {
    "money"
  }
}
`,
			wantParts: []string{
				"fn to_string(m: Money): String",
				"*impl* `Display.to_string`",
			},
		},
		{
			name: "first duplicate method name keeps its block interface",
			input: `interface greeter {
  fn greet(value: self): String
}

interface Farewell {
  fn greet(value: self): String
}

struct Person {
  name: String
}

impl greeter for Person {
  fn ▮greet(p: Person): String {
    "Hello, " + p.name
  }
}

impl Farewell for Person {
  fn greet(p: Person): String {
    "Goodbye, " + p.name
  }
}
`,
			wantParts: []string{
				"fn greet(p: Person): String",
				"*impl* `greeter.greet`",
			},
		},
		{
			name: "second duplicate method name keeps its block interface",
			input: `interface greeter {
  fn greet(value: self): String
}

interface Farewell {
  fn greet(value: self): String
}

struct Person {
  name: String
}

impl greeter for Person {
  fn greet(p: Person): String {
    "Hello, " + p.name
  }
}

impl Farewell for Person {
  fn ▮greet(p: Person): String {
    "Goodbye, " + p.name
  }
}
`,
			wantParts: []string{
				"fn greet(p: Person): String",
				"*impl* `Farewell.greet`",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, pos := hoverMarkerPosition(t, tc.input)
			uri := "file:///" + strings.ReplaceAll(tc.name, " ", "_") + ".nomi"
			s := NewServer()
			s.docs.Open(uri, input)
			doc := s.docs.Get(uri)
			if doc == nil {
				t.Fatal("doc not found after Open")
			}
			sym := doc.Analysis.SymbolAt(pos)
			if sym == nil {
				t.Fatalf("no symbol at marker position %+v", pos)
			}
			got := renderHoverWithAnalysis(sym, doc.Analysis)
			for _, want := range tc.wantParts {
				if !strings.Contains(got, want) {
					t.Errorf("expected hover to contain %q, got:\n%s", want, got)
				}
			}
			for _, blocked := range tc.blockedParts {
				if strings.Contains(got, blocked) {
					t.Errorf("expected hover not to contain %q, got:\n%s", blocked, got)
				}
			}
		})
	}
}

func TestHover_DiscardBindersRenderTypeAtBindingSite(t *testing.T) {
	input := `fn ignore(_param: Int): Int {
  _count = 5
  (_left, _right) = (1, "a")
  0
}
`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)
	analysis.CheckTypes(fa, nodes)

	cases := []struct {
		name string
		pos  analysis.Pos
		want string
	}{
		{"discard param", analysis.Pos{Line: 1, Col: 11}, "_param: Int"},
		{"discard binding", analysis.Pos{Line: 2, Col: 3}, "_count: Int"},
		{"discard tuple left", analysis.Pos{Line: 3, Col: 4}, "_left: Int"},
		{"discard tuple right", analysis.Pos{Line: 3, Col: 11}, "_right: String"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sym := fa.SymbolAt(tc.pos)
			if sym == nil {
				t.Fatalf("no symbol at %+v", tc.pos)
			}
			got := normalizeHover(renderHover(sym))
			if got != tc.want {
				t.Fatalf("hover: got %q, want %q", got, tc.want)
			}
		})
	}
}

// An interface-bound alias (`typealias Foo A and B`) should hover with the
// full declaration showing the conjunction RHS.
func TestHover_BoundAliasDeclaration(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
interface Tagged { fn tag(value: self): String }
typealias ShowAndTag Showable and Tagged`
	sym := buildSymbol(src, "ShowAndTag")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "typealias ShowAndTag Showable and Tagged") {
		t.Errorf("expected hover to render the alias declaration, got:\n%s", got)
	}
}

// Inside a bound-alias RHS, each interface name must register a
// Reference so hover and go-to-def work. Without this, a user hovering
// on `Showable` or `Tagged` in `typealias Foo Showable and Tagged`
// gets nothing.
func TestHover_BoundAlias_RHS_InterfaceReferences(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
interface Tagged { fn tag(value: self): String }
typealias ShowAndTag Showable and Tagged`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	// Find references on line 3 (the typealias RHS) for both names.
	foundShowable := false
	foundTagged := false
	for pos, sym := range fa.References {
		if pos.Line != 3 {
			continue
		}
		if sym != nil && sym.Name == "Showable" && sym.Kind == analysis.SymbolInterface {
			foundShowable = true
		}
		if sym != nil && sym.Name == "Tagged" && sym.Kind == analysis.SymbolInterface {
			foundTagged = true
		}
	}
	if !foundShowable {
		t.Error("expected a Showable interface reference on the typealias RHS line")
	}
	if !foundTagged {
		t.Error("expected a Tagged interface reference on the typealias RHS line")
	}
}

// Type-param hover when the bound is an interface-bound alias should show
// the alias name (`ShowAndTag`), not the expanded conjunction. The
// alias is the abstraction the user chose; rendering its expansion at
// the type-param site defeats the abstraction (and is inconsistent
// with the function-signature and bound-clause hovers, which already
// show the alias name).
func TestHover_TypeParamWithBoundAlias_RendersAliasName(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
interface Tagged { fn tag(value: self): String }
typealias ShowAndTag Showable and Tagged
fn describe<T>(_value: T): String where T: ShowAndTag { "" }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	// Find the type-param `T` reference (in the param annotation `value: T`).
	var tSym *analysis.Symbol
	for _, sym := range fa.References {
		if sym != nil && sym.Name == "T" {
			tSym = sym
			break
		}
	}
	if tSym == nil {
		t.Fatal("could not find type-param T reference")
	}
	got := renderHoverWithAnalysis(tSym, fa)
	if !strings.Contains(got, "ShowAndTag") {
		t.Errorf("expected hover to show alias name `ShowAndTag`, got:\n%s", got)
	}
	if strings.Contains(got, "Showable and Tagged") {
		t.Errorf("expected hover NOT to show the expansion `Showable and Tagged`, got:\n%s", got)
	}
}

// At a use site (`where T: ShowAndTag`), hover on the alias name should
// resolve to the alias declaration the same way.
func TestHover_BoundAliasReference(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
interface Tagged { fn tag(value: self): String }
typealias ShowAndTag Showable and Tagged
fn announce<T>(_value: T): String where T: ShowAndTag { "" }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	// Find the reference to ShowAndTag in the bound clause (line 4).
	var ref *analysis.Symbol
	for pos, sym := range fa.References {
		if pos.Line == 4 && sym.Name == "ShowAndTag" {
			ref = sym
			break
		}
	}
	if ref == nil {
		t.Fatal("expected reference to `ShowAndTag` on the bound clause")
	}
	got := renderHover(ref)
	if !strings.Contains(got, "typealias ShowAndTag Showable and Tagged") {
		t.Errorf("expected hover at use site to mirror declaration, got:\n%s", got)
	}
}

// Multi-bound `where T: A and B` hover should render both bounds joined by
// `and` on the function signature.
func TestHover_FuncWithMultipleInterfaceBounds(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
interface Tagged { fn tag(value: self): String }
fn identity<T>(x: T): T where T: Showable and Tagged { x }`
	sym := buildSymbol(src, "identity")
	if sym == nil {
		t.Fatal("symbol not found")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "where T: Showable and Tagged") {
		t.Errorf("expected hover to show `where T: Showable and Tagged`, got:\n%s", got)
	}
}

// Hovering on the type-param itself with multiple bounds should list
// every bound interface, joined by `and`.
func TestHover_TypeParamWithMultipleBounds(t *testing.T) {
	src := `interface Showable { fn show(value: self): String }
interface Tagged { fn tag(value: self): String }
fn identity<T>(x: T): T where T: Showable and Tagged { x }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)
	var tSym *analysis.Symbol
	for _, sym := range fa.References {
		if sym != nil && sym.Name == "T" {
			tSym = sym
			break
		}
	}
	if tSym == nil {
		t.Fatal("could not find type-param T reference")
	}
	got := renderHoverWithAnalysis(tSym, fa)
	for _, want := range []string{"`Showable`", "`Tagged`"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected hover for T to mention %s, got:\n%s", want, got)
		}
	}
}

func TestHover_TypeParamExplainsBounds(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface Comparable {}
	interface Steppable<S> {}
fn step_by<▮T, S>(value: T, step: S): T where T: Comparable and Steppable<S> { value }`)
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	sym := fa.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected type-param symbol at marker")
	}
	got := renderHoverWithAnalysis(sym, fa)
	for _, want := range []string{
		"```nomi\n<T> type parameter\n```",
		"`T` is a generic type chosen by the caller.",
		"Every `T` in this signature means that same chosen type.",
		"`Comparable`",
		"`Steppable<S>`",
		"`Steppable<S>` means the chosen `T` must implement `Steppable` with `S` as the type argument.",
		"`T = Int`, `S = Int`",
		"`T = Date`, `S = Duration`",
		"`S` does not have to be the same type as `T`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected hover to contain %q, got:\n%s", want, got)
		}
	}
}

func TestHover_TypeParamExplainsUseInPeerBound(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface Comparable {}
interface Steppable<S> {}
fn step_by<T, ▮S>(value: T, step: S): T where T: Comparable and Steppable<S> { value }`)
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	sym := fa.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected type-param symbol at marker")
	}
	got := renderHoverWithAnalysis(sym, fa)
	for _, want := range []string{
		"```nomi\n<S> type parameter\n```",
		"`S` is a generic type chosen by the caller.",
		"`S` is used as the type argument in `T: Steppable<S>`",
		"`T = Int`, `S = Int`",
		"`T = Date`, `S = Duration`",
		"`S` does not have to be the same type as `T`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected hover to contain %q, got:\n%s", want, got)
		}
	}
}

func TestHover_TypeParamInsidePeerBound(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface Comparable {}
interface Steppable<S> {}
fn step_by<T, S>(value: T, step: S): T where T: Comparable and Steppable<▮S> { value }`)
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	sym := fa.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected type-param symbol at marker")
	}
	got := renderHoverWithAnalysis(sym, fa)
	for _, want := range []string{
		"```nomi\n<S> type parameter\n```",
		"`S` is a generic type chosen by the caller.",
		"`S` is used as the type argument in `T: Steppable<S>`",
		"`S` does not have to be the same type as `T`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected hover to contain %q, got:\n%s", want, got)
		}
	}
}

func TestHover_TypeParamWhereBound(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface Steppable<S> {}
fn step_by<T, S>(value: T, step: S): T where ▮T: Steppable<S> { value }`)
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	sym := fa.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected type-param symbol at marker")
	}
	got := renderHoverWithAnalysis(sym, fa)
	for _, want := range []string{
		"```nomi\n<T> type parameter\n```",
		"The `where` clause means `T` can be any concrete type that implements `Steppable<S>`.",
		"`Steppable<S>` means the chosen `T` must implement `Steppable` with `S` as the type argument.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected hover to contain %q, got:\n%s", want, got)
		}
	}
}

func TestHover_TypeParamUsedInsideWhereBound(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface Steppable<S> {}
fn step_by<T, ▮S>(value: T, step: S): T where T: Steppable<S> { value }`)
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	sym := fa.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected type-param symbol at marker")
	}
	got := renderHoverWithAnalysis(sym, fa)
	for _, want := range []string{
		"```nomi\n<S> type parameter\n```",
		"`S` is used as the type argument in `T: Steppable<S>`",
		"`S` does not have to be the same type as `T`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected hover to contain %q, got:\n%s", want, got)
		}
	}
}

func TestHover_TypeParamInsideWhereBound(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface Steppable<S> {}
fn step_by<T, S>(value: T, step: S): T where T: Steppable<▮S> { value }`)
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	sym := fa.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected type-param symbol at marker")
	}
	got := renderHoverWithAnalysis(sym, fa)
	for _, want := range []string{
		"```nomi\n<S> type parameter\n```",
		"`S` is used as the type argument in `T: Steppable<S>`",
		"`S` does not have to be the same type as `T`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected hover to contain %q, got:\n%s", want, got)
		}
	}
}

func TestHover_UnboundedTypeParamExplainsAbsenceOfBounds(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `fn identity<▮T>(value: T): T { value }`)
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)

	sym := fa.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected type-param symbol at marker")
	}
	got := renderHover(sym)
	for _, want := range []string{
		"```nomi\n<T> type parameter\n```",
		"`T` is a generic type chosen by the caller.",
		"no interface bounds",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected hover to contain %q, got:\n%s", want, got)
		}
	}
}

func TestHover_VariantPatternShowsInstantiatedType(t *testing.T) {
	// Locally-declared Maybe / Result so the test doesn't depend on stdlib
	// being loaded. The substitution path through TypeParamDefs is the same
	// as for the real stdlib types.
	src := `enum Maybe<T> { Some T; None }
enum Result<T, E> { Ok T; Err E }
fn unwrap_or(m: Maybe<Result<Int, String>>): Int {
  case m {
    Some(Ok(n)) -> n
    Some(Err(_)) -> -1
    None -> 0
  }
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)
	analysis.CheckTypes(fa, nodes)

	// Find the `Ok` pattern reference. `Some(Ok(n))` lives on line 5,
	// column 10 (`    Some(Ok(...))` — 4 spaces + "Some(" puts Ok at 10).
	var okSym *analysis.Symbol
	for pos, sym := range fa.References {
		if pos.Line == 5 && pos.Col == 10 {
			okSym = sym
			break
		}
	}
	if okSym == nil {
		var lines []string
		for pos, sym := range fa.References {
			if sym != nil {
				lines = append(lines, fmt.Sprintf("  L%dC%d -> %s (kind=%v callType=%v)", pos.Line, pos.Col, sym.Name, sym.Kind, sym.CallType))
			}
		}
		t.Fatalf("could not find Ok reference at line 5 col 10. References:\n%s", strings.Join(lines, "\n"))
	}
	got := renderHover(okSym)
	if !strings.Contains(got, "variant Ok(Int): Result<Int, String>") {
		t.Errorf("expected hover to show `variant Ok(Int): Result<Int, String>`, got:\n%s", got)
	}
}

// Hovering on `Some` in `Some(Err("boom"))` shouldn't claim there's a `fn Some`
// somewhere — there isn't. It's a variant constructor. The hover should make
// the variant nature explicit so a reader doesn't go hunting for a phantom
// `fn` declaration.
func TestHover_VariantConstructorAtCallSite(t *testing.T) {
	resultStrInt := &analysis.EnumType{
		Name:     "Result",
		TypeArgs: []analysis.Type{analysis.TypeInt, analysis.TypeString},
	}
	maybeResult := &analysis.EnumType{
		Name:     "Maybe",
		TypeArgs: []analysis.Type{resultStrInt},
	}
	ft := &analysis.FuncType{
		Params: []analysis.Type{resultStrInt},
		Return: maybeResult,
	}
	sym := &analysis.Symbol{
		Name:     "Some",
		Kind:     analysis.SymbolEnumVariant,
		CallType: ft,
	}
	got := renderHover(sym)
	want := "```nomi\nvariant Some(Result<Int, String>): Maybe<Result<Int, String>>\n```"
	if got != want {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, want)
	}
}

func TestHover_AppFieldReference(t *testing.T) {
	src := `

interface Logger {
  fn log(value: self, msg: String): Unit
}

type Stdout

impl Logger for Stdout {
  fn log(_value: Stdout, msg: String): Unit { _ = msg }
}

struct AppEnv {
  context: Context

  logger: Logger
}

fn boot(): AppEnv {
  AppEnv{context: Context.root(), logger: Stdout}
}

fn greet(name: String) {
  Logger.log(AppEnv.logger, "hi " + name)
}

fn main() {
  greet("ada")
}

`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	file := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, "", nil)
	_ = analysis.CheckTypes(file, nodes)

	// The `AppEnv.logger` read, not the struct literal's `logger:` label,
	// which is also a field reference.
	lines := strings.Split(src, "\n")
	var sym *analysis.Symbol
	for i, line := range lines {
		if at := strings.Index(line, "AppEnv.logger"); at >= 0 {
			sym = file.References[analysis.Pos{Line: i + 1, Col: at + len("AppEnv.") + 1}]
		}
	}
	if sym == nil {
		t.Fatal("app field reference not found")
	}
	result := renderHover(sym)
	expected := "```nomi\nlogger: Logger\n```\n\n*app field of* `AppEnv`"
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

func TestHover_ImportedFileStructFieldReference(t *testing.T) {
	apiSrc := `import std/results.Result

pub struct RunResult {
  output: String
}

pub fn run(): Result<RunResult, String> {
  Ok(RunResult{output: "hi"})
}
`
	input, pos := hoverMarkerPosition(t, `import {
  api
}

fn main() {
  case api.run() {
    Ok(result) -> {
      _ = dbg result.▮output
    }
    Err(_) -> Unit
  }
}
`)
	s := NewServer()
	s.docs.Open("file:///api.nomi", apiSrc)
	s.docs.Open("file:///main.nomi", input)

	doc := s.docs.Get("file:///main.nomi")
	if doc == nil {
		t.Fatal("doc not in manager after Open")
	}
	if len(doc.Analysis.TypeErrors) != 0 {
		t.Fatalf("expected no type errors, got: %v", doc.Analysis.TypeErrors)
	}
	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected field symbol at imported module struct field access")
	}
	got := renderHover(sym)
	want := "```nomi\noutput: String\n```"
	if got != want {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, want)
	}
}

// Hover on the `self` marker in a drill-through selective import
// (`import std/foo.Bar.{self, ...}`) should render the enum definition
// the same way hovering on the `Bar` path segment does. The self
// symbol's Resolved pointer feeds the renderer the real enum.
func TestHover_SelfMarkerDrillThrough(t *testing.T) {
	siblingSrc := "pub enum Color { Red; Green; Blue }\n"
	uri := "file:///main.nomi"
	siblingURI := "file:///palette.nomi"

	s := NewServer()
	s.docs.Open(siblingURI, siblingSrc)
	s.docs.Open(uri, "import palette.Color.{self, Red}\n")

	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not in manager after Open")
	}
	// Find the `self` definition whose Resolved enum is Color — the
	// auto-prepended std prelude chain also emits
	// drill-through self markers for Bool/Maybe/Result, so a plain
	// "first self" lookup is ambiguous; filter by the enum's name.
	var sym *analysis.Symbol
	for _, def := range doc.Analysis.Definitions {
		if def.Name != "self" || def.Resolved == nil {
			continue
		}
		if def.Resolved.Name == "Color" {
			sym = def
			break
		}
	}
	if sym == nil {
		t.Fatal("expected a `self` definition entry for drill-through import resolving to Color")
	}
	if sym.Resolved == nil {
		t.Fatal("expected drill-through self to carry a Resolved pointer to the enum")
	}
	got := renderHover(sym)
	if !strings.Contains(got, "enum Color") {
		t.Errorf("expected hover to render the resolved enum definition, got:\n%s", got)
	}
}

// Hovering over the `try` in `try r` (where r: Result<Int, String>) should show
// the unwrapped success type, the Err payload propagated on failure, and
// the enclosing fn the `try` unwinds to. The checker registers a synthetic
// symbol at the `try` token's position; renderHover dispatches on it.
func TestHover_TryOp_Result(t *testing.T) {
	src := "enum Result<T, E> { Ok T; Err E }\n" +
		"fn calc(r: Result<Int, String>): Int {\n" +
		"  v = try r\n" +
		"  v + 1\n" +
		"}"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)

	// `  v = try r` — `try` starts at column 7 on line 3 (1-based).
	pos := analysis.Pos{Line: 3, Col: 7}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `try` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\ntry: Result<Int, String> -> Int\n```\n\nOn `Err: String`, unwinds to `fn calc`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

// Same as above but for Maybe<T>: prose says `On None` (no payload type)
// instead of `On Err: <type>`.
func TestHover_TryOp_Maybe(t *testing.T) {
	src := "enum Maybe<T> { Some T; None }\n" +
		"fn lookup(m: Maybe<Int>): Int {\n" +
		"  v = try m\n" +
		"  v + 1\n" +
		"}"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)

	// `  v = try m` — `try` starts at column 7 on line 3 (1-based).
	pos := analysis.Pos{Line: 3, Col: 7}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `try` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\ntry: Maybe<Int> -> Int\n```\n\nOn `None`, unwinds to `fn lookup`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

// `x |> f() |> try` — pipe-stage try unwraps the previous stage. Hover on
// the `try` in that form must still resolve.
func TestHover_TryOp_InPipe(t *testing.T) {
	src := "enum Result<T, E> { Ok T; Err E }\n" +
		"fn parse(_s: String): Result<Int, String> { Result.Ok(0) }\n" +
		"fn run(s: String): Result<Int, String> {\n" +
		"  n = s |> parse() |> try\n" +
		"  Result.Ok(n + 1)\n" +
		"}"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)

	// `  n = s |> parse() |> try` — `try` starts at column 24 on line 4
	// (1-based): 2 spaces + "n = s |> parse() |> " is 23 columns.
	pos := analysis.Pos{Line: 4, Col: 24}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the piped `try` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\ntry: Result<Int, String> -> Int\n```\n\nOn `Err: String`, unwinds to `fn run`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

// `try` inside a lambda unwinds to the lambda boundary, not the enclosing fn
// (Nomi has no non-local returns). Hover should reflect that.
func TestHover_TryOp_InLambda(t *testing.T) {
	src := "enum Result<T, E> { Ok T; Err E }\n" +
		"fn outer(): (Result<Int, String>) -> Int {\n" +
		"  |r| {\n" +
		"    v = try r\n" +
		"    v + 1\n" +
		"  }\n" +
		"}"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)

	// `    v = try r` on line 4 — `try` starts at column 9 (4 spaces + "v = " = 8).
	pos := analysis.Pos{Line: 4, Col: 9}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `try` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\ntry: Result<Int, String> -> Int\n```\n\nOn `Err: String`, unwinds to `lambda`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_ImplKeyword_LocalBlock(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface Speakable {
  fn speak(value: self): String
}

struct Dog {
}

▮impl Speakable for Dog {
  fn speak(dog: Dog): String {
    "woof"
  }
}`)
	file := checkedLoweredFile(input)
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `impl` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nimpl Speakable for Dog\n```\n\nImplements `Speakable` for `Dog`. Manual interface implementations use this block form."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_ImplKeyword_BlockWithFunction(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface Speakable {
  fn speak(value: self): String
}

struct Dog {
}

▮impl Speakable for Dog {
  fn speak(dog: Dog): String {
    "woof"
  }
}`)
	file := checkedLoweredFile(input)
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `impl` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nimpl Speakable for Dog\n```\n\nImplements `Speakable` for `Dog`. Manual interface implementations use this block form."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_ImplKeyword_DuplicateMethodNameBlock(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface greeter {
  fn greet(value: self): String
}

interface Farewell {
  fn greet(value: self): String
}

struct Person {
}

▮impl greeter for Person {
  fn greet(person: Person): String {
    "hello"
  }
}

impl Farewell for Person {
  fn greet(person: Person): String {
    "goodbye"
  }
}`)
	file := checkedLoweredFile(input)
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `impl` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nimpl greeter for Person\n```\n\nImplements `greeter` for `Person`. Manual interface implementations use this block form."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_ImplKeyword_ExternFunctionBlock(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface Inspectable {
  fn inspect(value: self): String
}

host type Handle

▮impl Inspectable for Handle {
  host fn inspect(handle: Handle): String
}`)
	file := checkedLoweredFile(input)
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `impl` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nimpl Inspectable for Handle\n```\n\nImplements `Inspectable` for `Handle`. Manual interface implementations use this block form."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_ImplKeyword_TopLevelBlock(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `host type Foreign

interface Html {
  fn html(value: self): String
}

▮impl Html for Foreign {
  fn html(value: Foreign): String {
    "html"
  }
}`)
	file := checkedLoweredFile(input)
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `impl` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nimpl Html for Foreign\n```\n\nImplements `Html` for `Foreign`. Manual interface implementations use this block form."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_ImplKeyword_ConstrainedBlock(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `interface Comparable {
  fn compare(a: self, b: self): Int
}

interface Discrete {
  fn step(value: self): self
}

interface Iter {
  fn count(value: self): Int
}

struct Range<T> where T: Comparable {}

▮impl Iter for Range<T> where T: Discrete {
  fn count(r: Range<T>): Int {
    0
  }
}`)
	file := checkedLoweredFile(input)
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `impl` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nimpl Iter for Range<T> where T: Discrete\n```\n\nImplements `Iter` for `Range<T>`. Manual interface implementations use this block form."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_Assertion_Bool(t *testing.T) {
	src := "enum Result<T, E> { Ok T; Err E }\n" +
		"host type AssertionFailure\n" +
		"fn validate(): Result<Bool, AssertionFailure> {\n" +
		"  v = assert 1 == 1\n" +
		"  Result.Ok(v)\n" +
		"}"
	file := checkedFile(src)

	pos := analysis.Pos{Line: 4, Col: 7}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `assert` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nassert: Bool -> Bool\n```\n\nRequires `True`. On `False`, returns `AssertionFailure` and unwinds to `fn validate`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_Refute_Result(t *testing.T) {
	src := "enum Result<T, E> { Ok T; Err E }\n" +
		"host type AssertionFailure\n" +
		"fn validate(r: Result<Int, String>): Result<Result<Int, String>, AssertionFailure> {\n" +
		"  e = refute r\n" +
		"  Result.Ok(e)\n" +
		"}"
	file := checkedFile(src)

	pos := analysis.Pos{Line: 4, Col: 7}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `refute` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nrefute: Result<Int, String> -> Result<Int, String>\n```\n\nRequires `Err(_)`. On `Ok: Int`, returns `AssertionFailure` and unwinds to `fn validate`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_Check_Maybe(t *testing.T) {
	src := "enum Result<T, E> { Ok T; Err E }\n" +
		"enum Maybe<T> { Some T; None }\n" +
		"host type AssertionFailure\n" +
		"host fn check<T>(subject: T): Result<T, AssertionFailure>\n" +
		"fn validate(m: Maybe<Int>) {\n" +
		"  checked = check(m)\n" +
		"}"
	file := checkedFile(src)

	pos := analysis.Pos{Line: 6, Col: 15}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `check` position")
	}
	got := renderHover(sym)
	expected := "```nomi\nfn check(subject: Maybe<Int>): Result<Maybe<Int>, AssertionFailure>\n```"
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_Assertion_TestBoundary(t *testing.T) {
	src := "enum Result<T, E> { Ok T; Err E }\n" +
		"host type AssertionFailure\n" +
		"test \"math works\" {\n" +
		"  assert 1 == 1\n" +
		"}"
	file := checkedFile(src)

	pos := analysis.Pos{Line: 4, Col: 3}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `assert` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nassert: Bool -> Bool\n```\n\nRequires `True`. On `False`, returns `AssertionFailure` and unwinds to `test \"math works\"`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_Assertion_PipelineBool(t *testing.T) {
	inputWithMarker := `test "maybe equality works as a pipeline predicate" {
  3.7
  |> Float.round()
  |> Float.to_int()
  |> Maybe.equal?(Some(4))
  |> ▮assert
}
`
	input, pos := hoverMarkerPosition(t, inputWithMarker)
	uri := "file:///pipeline_assert.nomi"

	s := NewServer()
	s.docs.Open(uri, input)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the piped `assert` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nassert: Bool -> Bool\n```\n\nRequires `True`. On `False`, returns `AssertionFailure` and unwinds to `test \"maybe equality works as a pipeline predicate\"`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_Assertion_PatternDestructure(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `host type AssertionFailure

struct Point {
  x: Int
  y: Int
}

test "pattern assert" {
  ▮assert Point{x, y} = Point{x: 3, y: 4}
}
`)
	file := checkedFile(input)

	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the pattern `assert` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nassert: Point -> Unit\n```\n\nMatches the value against the left-hand pattern. On mismatch, returns `AssertionFailure` and unwinds to `test \"pattern assert\"`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_IfKeyword(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `fn label(flag: Bool): String {
  ▮if flag { "yes" } else { "no" }
}
`)
	file := checkedFile(input)

	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `if` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nif: Bool -> String\n```\n\nBranches on a `Bool`. With `else`, both branches must produce compatible values; without `else`, the expression returns `Unit`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_IfPatternKeyword(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `enum Box<T> {
  Some T
  None
}

fn label(maybe_name: Box<String>): String {
  ▮if Box.Some(name) = maybe_name { name } else { "unknown" }
}
`)
	file := checkedFile(input)

	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the pattern `if` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nif: Box<String> -> String\n```\n\nMatches the input value against the condition pattern. On mismatch, runs `else`; without `else`, the expression returns `Unit`."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_CaseKeyword(t *testing.T) {
	input, pos := hoverMarkerPosition(t, `enum Maybe<T> {
  Some T
  None
}

fn value(m: Maybe<Int>): Int {
  ▮case m {
    Some(n) -> n
    None -> 0
  }
}
`)
	file := checkedFile(input)

	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `case` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\ncase: Maybe<Int> -> Int\n```\n\nMatches the input value against patterns. Branch bodies must produce compatible values."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_TestSetup_RootContext(t *testing.T) {
	src := "tests \"numbers\" {\n" +
		"  setup {\n" +
		"    42\n" +
		"  }\n" +
		"\n" +
		"  test \"uses setup\", value { assert value == 42 }\n" +
		"}"
	file := checkedFile(src)

	pos := analysis.Pos{Line: 2, Col: 3}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `setup` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nsetup: Unit -> Int\n```\n\nRuns before each test in `tests \"numbers\"`, after the group's boot. A test binds the returned value with a pattern after its name."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_Tests_GroupContext(t *testing.T) {
	src := "tests \"numbers\" {\n" +
		"  setup {\n" +
		"    42\n" +
		"  }\n" +
		"\n" +
		"  test \"uses setup\", value { assert value == 42 }\n" +
		"}"
	file := checkedFile(src)

	pos := analysis.Pos{Line: 1, Col: 1}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `tests` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\ntests: Unit -> Int\n```\n\nGroups tests under `tests \"numbers\"`. Its setup runs before each test and returns the value a test's pattern binds."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_Test_ContextPattern(t *testing.T) {
	src := "tests \"numbers\" {\n" +
		"  setup {\n" +
		"    42\n" +
		"  }\n" +
		"\n" +
		"  test \"uses setup\", value { assert value == 42 }\n" +
		"}"
	file := checkedFile(src)

	pos := analysis.Pos{Line: 6, Col: 3}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `test` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\ntest: Int -> Unit\n```\n\nRuns only under `nomi test` as `test \"uses setup\"`. Binds its group's setup value through its pattern. Assertions unwind to this test."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_TestSetup_RecordContext(t *testing.T) {
	src := "tests \"child\" {\n" +
		"  setup {\n" +
		"    {inherited: \"parent\", value: 2, child_only: \"ready\"}\n" +
		"  }\n" +
		"\n" +
		"  test \"uses setup\", {inherited, value, child_only} { assert child_only == \"ready\" }\n" +
		"}"
	file := checkedFile(src)

	pos := analysis.Pos{Line: 2, Col: 3}
	sym := file.SymbolAt(pos)
	if sym == nil {
		t.Fatal("expected a symbol at the `setup` keyword position")
	}
	got := renderHover(sym)
	expected := "```nomi\nsetup: Unit -> {inherited: String, value: Int, child_only: String}\n```\n\nRuns before each test in `tests \"child\"`, after the group's boot. A test binds the returned value with a pattern after its name."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

func TestHover_MultilineTestContextDoesNotLeakIntoTestTitle(t *testing.T) {
	src := "tests \"child\" {\n" +
		"  setup {\n" +
		"    {inherited: \"parent\", value: 2, child_only: \"ready\"}\n" +
		"  }\n" +
		"\n" +
		"  test \"uses child setup\", {\n" +
		"    inherited,\n" +
		"    value,\n" +
		"    child_only,\n" +
		"  } { assert child_only == \"ready\" }\n" +
		"}"
	file := checkedFile(src)

	titlePos := analysis.Pos{Line: 6, Col: 10}
	if sym := file.SymbolAt(titlePos); sym != nil {
		t.Fatalf("unexpected symbol over test title at %+v: %s", titlePos, renderHover(sym))
	}

	childOnlyPos := analysis.Pos{Line: 9, Col: 5}
	sym := file.SymbolAt(childOnlyPos)
	if sym == nil {
		t.Fatal("expected a symbol at the multiline context binding")
	}
	got := renderHover(sym)
	expected := "```nomi\nchild_only: String\n```"
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}

// Case-pattern payload bindings inside a NESTED impl (lowered out of the
// type body by LowerDerives) must keep their checker-recorded types.
// Regression: callers that ran BuildTypes/CheckTypes over the pre-lowering
// node slice never type-checked the lowered blocks' bodies (the decl's
// Items are consumed by lowering), so `r` hovered as a bare `r`. The
// pipeline below mirrors production (document.go / runtime.LoadSource):
// lower FIRST, then hand the SAME slice to build + check.
func TestHover_NestedImplCasePayloadBindings(t *testing.T) {
	src := `pub interface Measure {
  fn area(s: self): Float
}

pub enum Shape {
  Circle Float
  Rectangle {width: Float, height: Float}
}

impl Measure for Shape {
  fn area(s: Shape): Float {
    case s {
      .Circle(r) -> 3.14 * r * r
      .Rectangle{width, height} -> width * height
    }
  }
}

`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = analysis.LowerDerives(nodes)
	file := buildFile(nodes)
	analysis.BuildTypes(file, nodes)
	analysis.CheckTypes(file, nodes)

	wants := map[string]struct {
		line int
		frag string // the binding's text on that line
		want string
	}{
		"r":     {13, "r) ->", "```nomi\nr: Float\n```"},
		"width": {14, "width,", "```nomi\nwidth: Float\n```"},
	}
	lines := strings.Split(src, "\n")
	for name, w := range wants {
		col := strings.Index(lines[w.line-1], w.frag) + 1
		sym := file.SymbolAt(analysis.Pos{Line: w.line, Col: col})
		if sym == nil {
			t.Fatalf("no symbol for %q at L%d:'C%d", name, w.line, col)
		}
		got := renderHover(sym)
		if got != w.want {
			t.Errorf("%s: got:\n%s\n\nexpected:\n%s", name, got, w.want)
		}
	}
}

// interfaceSelfSymbolAt lowers, builds, and returns the `self` symbol at the
// first interface-signature `: self` occurrence in src.
func interfaceSelfSymbolAt(t *testing.T, src string) *analysis.Symbol {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = analysis.LowerDerives(nodes)
	file := buildFile(nodes)
	for li, ln := range strings.Split(src, "\n") {
		idx := strings.Index(ln, ": self")
		if idx < 0 {
			continue
		}
		sym := file.SymbolAt(analysis.Pos{Line: li + 1, Col: idx + 3}) // 's' in self
		if sym != nil {
			return sym
		}
	}
	t.Fatal("no symbol at the `self` position")
	return nil
}

func TestHover_InterfaceSelfPlaceholder(t *testing.T) {
	src := "/// Values that can speak.\ninterface Speaker {\n  fn speak(value: self): String\n}"
	sym := interfaceSelfSymbolAt(t, src)
	result := renderHover(sym)
	expected := "```nomi\ninterface Speaker {\n    fn speak(value: self): String\n}\n```\n\nValues that can speak."
	if result != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", result, expected)
	}
}

// A STRUCT LITERAL'S FIELD LABEL HOVERS THE INSTANTIATED FIELD TYPE, AND THE
// JUMP STILL LANDS ON THE FIELD DECLARATION. Both halves are asserted in one
// test because either one alone is satisfied by the wrong thing: the type
// alone passes for an implementation that synthesizes a fresh symbol at the
// label's own position (which moves go-to-definition into the literal), and
// the jump alone passes for one that reports the uninstantiated type.
//
// `registerStructLitFieldRefs` (builder.go) points each label at the struct's
// own field declaration symbol, whose Type for a generic struct is the unbound
// type parameter. Hovering that symbol unsubstituted would show `value: _`,
// `left: _`, `right: _`, and `inner: Box<_>` for a field declared
// `inner: Box<T>`.
//
// THE NON-GENERIC CASES ARE THE DISCRIMINATOR and they are in the same table.
// `Point{x: 1}` and the `tag: String` field sitting beside a `value: T` need
// no substitution, so a table that only held generic fields could not tell a
// substitution bug from a wholesale failure to type struct-literal labels at
// all.
func TestHover_StructLitFieldLabelCarriesTheInstantiatedType(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantHover string
		wantDecl  string
	}{
		{
			name: "one type parameter, inferred from the value",
			src: `struct Box<T> {
  value: T
}

fn main() {
  b = Box{▮value: 1}
  b
}`,
			wantHover: "```nomi\nvalue: Int\n```",
			wantDecl:  "value: T",
		},
		{
			name: "two parameters, the FIRST label — solved by the loop that has not reached it yet",
			src: `struct Pair<A, B> {
  left: A
  right: B
}

fn main() {
  p = Pair{▮left: 1, right: "x"}
  p
}`,
			wantHover: "```nomi\nleft: Int\n```",
			wantDecl:  "left: A",
		},
		{
			name: "two parameters, the SECOND label",
			src: `struct Pair<A, B> {
  left: A
  right: B
}

fn main() {
  p = Pair{left: 1, ▮right: "x"}
  p
}`,
			wantHover: "```nomi\nright: String\n```",
			wantDecl:  "right: B",
		},
		{
			name: "the parameter is NESTED in the field type — a one-level unwrap renders Box<_>",
			src: `struct Box<T> {
  value: T
}

struct Outer<T> {
  inner: Box<T>
}

fn main() {
  o = Outer{▮inner: Box{value: 1}}
  o
}`,
			wantHover: "```nomi\ninner: Box<Int>\n```",
			wantDecl:  "inner: Box<T>",
		},
		{
			name: "a concrete field beside a generic one keeps the shared declaration symbol",
			src: `struct Wrap<T> {
  value: T
  tag: String
}

fn main() {
  w = Wrap{value: "s", ▮tag: "t"}
  w
}`,
			wantHover: "```nomi\ntag: String\n```",
			wantDecl:  "tag: String",
		},
		{
			name: "non-generic struct, the control",
			src: `struct Point {
  x: Int
}

fn main() {
  p = Point{▮x: 1}
  p
}`,
			wantHover: "```nomi\nx: Int\n```",
			wantDecl:  "x: Int",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := definitionMarkerPosition(t, tc.src)
			uri := "file:///struct_lit_label.nomi"
			s := NewServer()
			s.docs.Open(uri, src)

			if got := appFieldHover(t, s, uri, pos); got != tc.wantHover {
				t.Errorf("hover on the field label:\ngot:  %q\nwant: %q", got, tc.wantHover)
			}

			loc := mustDefinitionLocation(t, s, uri, pos)
			if loc.URI != protocol.DocumentUri(uri) {
				t.Fatalf("definition landed in %q, want the declaring document %q", loc.URI, uri)
			}
			lines := strings.Split(src, "\n")
			at := int(loc.Range.Start.Line)
			if at < 0 || at >= len(lines) {
				t.Fatalf("definition points at line %d, outside the document's %d lines", at, len(lines))
			}
			if got := strings.TrimSpace(lines[at]); got != tc.wantDecl {
				t.Fatalf("definition points at line %d %q, want the field declaration %q", at+1, got, tc.wantDecl)
			}
		})
	}
}

// THE LABEL'S TYPE IS PER SITE, NOT PER DECLARATION. Two literals of one
// generic struct at two different instantiations, in one file, both re-record
// the same `Box.value` declaration symbol.
//
// This is the case that makes the COPY load-bearing instead of merely
// defensive. Writing `declSym.Type = instantiated` in place instead of copying
// leaves `./lsp` and `./analysis` green — the root-key slice measured that and
// recorded the mutant as uncaught — but measured here it makes the SECOND
// literal's instantiation win at both sites: `Box{value: 1}` hovers
// `value: String`. At the base twin both hover `value: _`.
//
// The jumps are asserted too, and they are expected to COINCIDE: two sites,
// two different types, one declaration to navigate to.
func TestHover_StructLitFieldLabelIsPerSiteNotPerDeclaration(t *testing.T) {
	src := `struct Box<T> {
  value: T
}

fn main() {
  a = Box{value: 1}
  b = Box{value: "s"}
  a
  b
}`
	uri := "file:///struct_lit_two_sites.nomi"
	s := NewServer()
	s.docs.Open(uri, src)

	// Occurrence 1 of `value:` is the declaration itself, so the literals are
	// occurrences 2 and 3.
	want := []string{"```nomi\nvalue: Int\n```", "```nomi\nvalue: String\n```"}
	for i, wantHover := range want {
		pos := appStructLitLabelCursor(t, src, "value", i+2)
		if got := appFieldHover(t, s, uri, pos); got != wantHover {
			t.Errorf("hover on literal %d's `value` label:\ngot:  %q\nwant: %q", i+1, got, wantHover)
		}
		loc := mustDefinitionLocation(t, s, uri, pos)
		if loc.URI != protocol.DocumentUri(uri) {
			t.Fatalf("definition from literal %d landed in %q, want %q", i+1, loc.URI, uri)
		}
		lines := strings.Split(src, "\n")
		at := int(loc.Range.Start.Line)
		if at < 0 || at >= len(lines) {
			t.Fatalf("definition from literal %d points at line %d, outside the document's %d lines", i+1, at, len(lines))
		}
		if got := strings.TrimSpace(lines[at]); got != "value: T" {
			t.Fatalf("definition from literal %d points at line %d %q, want the one declaration %q", i+1, at+1, got, "value: T")
		}
	}
}

// A GENERIC ENUM'S STRUCT-VARIANT LITERAL HOVERS THE INSTANTIATED ENUM, and
// its field label hovers the solved field type. Both are asserted here because
// they come from one substitution and neither alone identifies it: the binding
// alone passes for an implementation that stamps arguments without checking
// the fields, and the label alone passes for one that types the label off the
// value rather than off the declaration.
//
// Without the substitution the enum row would read:
//
//	s = Shape.Wrap{inner: 3}   ->  s: Shape       and  inner: _
//
// The struct row is in the table because it goes through the same path for a
// plain struct, so a table of enum rows alone could not tell a substitution
// bug from struct-variant literals being untyped altogether. The NON-GENERIC
// enum row is the second discriminator: `inner: Int` needs no type argument
// there, which localises a fault to the type-argument solution and not to the
// label reference. The label's type is one the checker derives for the
// literal, not one the hover invents.
func TestHover_EnumStructVariantLitIsInstantiated(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantBind  string
		wantLabel string
		label     string
	}{
		{
			name: "one type parameter, inferred from the field value",
			src: `enum Shape<T> {
  Wrap {inner: T}
}

fn main() {
  s = Shape.Wrap{inner: 3}
  s
}`,
			wantBind:  "```nomi\ns: Shape<Int>\n```",
			wantLabel: "```nomi\ninner: Int\n```",
			label:     "inner",
		},
		{
			name: "the argument is NESTED — Shape<List<Int>>",
			src: `enum Shape<T> {
  Wrap {inner: T}
}

fn main() {
  s = Shape.Wrap{inner: [1, 2]}
  s
}`,
			wantBind:  "```nomi\ns: Shape<List<Int>>\n```",
			wantLabel: "```nomi\ninner: List<Int>\n```",
			label:     "inner",
		},
		{
			name: "TWO parameters — the second label, solved after the first",
			src: `enum Two<A, B> {
  Both {left: A, right: B}
}

fn main() {
  t = Two.Both{left: 1, right: "s"}
  t
}`,
			wantBind:  "```nomi\nt: Two<Int, String>\n```",
			wantLabel: "```nomi\nright: String\n```",
			label:     "right",
		},
		{
			name: "the parameter is inside the field type — items: List<T>",
			src: `enum Holder<T> {
  Of {items: List<T>}
}

fn main() {
  h = Holder.Of{items: ["a"]}
  h
}`,
			wantBind:  "```nomi\nh: Holder<String>\n```",
			wantLabel: "```nomi\nitems: List<String>\n```",
			label:     "items",
		},
		{
			name: "NON-GENERIC enum — correct at the twin, pinned as the control",
			src: `enum Shape {
  Wrap {inner: Int}
}

fn main() {
  s = Shape.Wrap{inner: 3}
  s
}`,
			wantBind:  "```nomi\ns: Shape\n```",
			wantLabel: "```nomi\ninner: Int\n```",
			label:     "inner",
		},
		{
			name: "CONTROL — the struct literal",
			src: `struct Box<T> {
  value: T
}

fn main() {
  b = Box{value: 1}
  b
}`,
			// A STRUCT's hover appends the struct body; an enum's does not.
			// That asymmetry is pre-existing and is not what this test is
			// about, so the control records what the server actually sends
			// rather than being trimmed to match the enum rows.
			wantBind:  "```nomi\nb: Box<Int>\n\nstruct Box<Int> {\n    value: Int\n}\n```",
			wantLabel: "```nomi\nvalue: Int\n```",
			label:     "value",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri := "file:///variant_lit_hover.nomi"
			s := NewServer()
			s.docs.Open(uri, tc.src)

			// Occurrence 1 of `<label>:` is the declaration, so the literal
			// is occurrence 2.
			labelPos := appStructLitLabelCursor(t, tc.src, tc.label, 2)
			if got := appFieldHover(t, s, uri, labelPos); got != tc.wantLabel {
				t.Errorf("hover on the field label:\ngot:  %q\nwant: %q", got, tc.wantLabel)
			}

			bindPos := variantLitBindingCursor(t, tc.src)
			if got := appFieldHover(t, s, uri, bindPos); got != tc.wantBind {
				t.Errorf("hover on the binding:\ngot:  %q\nwant: %q", got, tc.wantBind)
			}
		})
	}
}

// variantLitBindingCursor points at the name on the line that binds a literal
// — the single ` x = ` inside `fn main()` in each source above.
func variantLitBindingCursor(t *testing.T, src string) protocol.Position {
	t.Helper()
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		eq := strings.Index(trimmed, " = ")
		if eq <= 0 {
			continue
		}
		col := strings.Index(line, trimmed)
		return protocol.Position{Line: uint32(i), Character: uint32(col)}
	}
	t.Fatalf("no binding line found in:\n%s", src)
	return protocol.Position{}
}

func TestHover_ThenKeyword(t *testing.T) {
	got := hoverText(t, "then", "fn label(n: Int): String {\n  n\n  |> ▮then |v| Int.to_string(v + 1)\n}\n")
	expected := "```nomi\nthen: Int -> String\n```\n\nApplies the lambda to the piped value. Its body ends at the next `|>`; braces keep a pipe inside it."
	if got != expected {
		t.Errorf("got:\n%s\n\nexpected:\n%s", got, expected)
	}
}
