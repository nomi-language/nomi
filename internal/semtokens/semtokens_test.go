package semtokens_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/semtokens"
	"github.com/nomi-language/nomi/std"
)

func TestCollect_ForeignAliasDefinitionsAndImplReferences(t *testing.T) {
	src := `gopkg "example.com/binding/ffi" as ffi

pub opaque type Box go ffi.Box

impl Box {
  fn echo(value: String): String go ffi.Echo
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)
	tokens := semtokens.Collect(fa)
	var aliasDefinition, aliasReference bool
	for _, tok := range tokens {
		if tok.Type != "namespace" {
			continue
		}
		if tok.Line == 1 {
			aliasDefinition = true
		}
		if tok.Line == 6 {
			aliasReference = true
		}
	}
	if !aliasDefinition || !aliasReference {
		t.Fatalf("foreign alias tokens = %v, definition=%v reference=%v", tokens, aliasDefinition, aliasReference)
	}
}

// TestCollect_NoTokenAtImplKeyword pins the user-visible token boundary:
// Collect should color the interface and receiver names in an impl header, not
// repaint the `impl` keyword itself as either type.
func TestCollect_NoTokenAtImplKeyword(t *testing.T) {
	src := `interface Speech {
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
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)
	toks := semtokens.Collect(fa)
	sawSpeech := false
	for _, tok := range toks {
		if tok.Line != 9 {
			continue
		}
		// Line 9 is `impl Speech for Dog` — `impl` spans cols 1-4,
		// `Speech` starts at col 6. Any token starting before col 6 covers
		// keyword text.
		if tok.Col < 6 {
			t.Errorf("unexpected semantic token over the `impl` keyword: col %d len %d type %s", tok.Col, tok.Length, tok.Type)
			continue
		}
		if tok.Col == 6 && tok.Type == "interface" {
			sawSpeech = true
		}
	}
	if !sawSpeech {
		t.Errorf("expected an interface token for Speech at line 9 col 6; tokens: %v", toks)
	}
}

// TestCollect_InterfaceTokenAtImplHeader pins that the interface named in an
// `impl Iface for Type { ... }` block resolves as a type reference and gets an
// "interface" semantic token.
func TestCollect_InterfaceTokenAtImplHeader(t *testing.T) {
	src := `interface Speech {
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
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)
	toks := semtokens.Collect(fa)
	// Line 9 is `impl Speech for Dog` — `Speech` starts at col 6.
	sawHeader := false
	for _, tok := range toks {
		if tok.Type != "interface" {
			continue
		}
		if tok.Line == 9 && tok.Col == 6 {
			sawHeader = true
		}
	}
	if !sawHeader {
		t.Errorf("expected an interface token for Speech in `impl Speech for Dog` at line 9 col 6; tokens: %v", toks)
	}
}

func TestCollect_DiscardBindersDoNotEmitSemanticTokens(t *testing.T) {
	src := `fn ignore(_param: Int): Int {
  _count = 5
  0
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)
	analysis.CheckTypes(fa, nodes)

	for pos, sym := range fa.Definitions {
		if ast.IsDiscardName(sym.Name) &&
			(sym.Kind == analysis.SymbolBinding || sym.Kind == analysis.SymbolParam) &&
			sym.Type == nil {
			t.Fatalf("discard definition %s at %+v has no type for hover", sym.Name, pos)
		}
	}

	for _, tok := range semtokens.Collect(fa) {
		if (tok.Line == 1 && tok.Col == 11) || (tok.Line == 2 && tok.Col == 3) {
			t.Fatalf("discard binder received semantic token %+v; tree-sitter dimming should own its color", tok)
		}
	}
}

func TestCollect_FieldsDoNotEmitSemanticTokens(t *testing.T) {
	src := `struct Point {
  x: Int
}

fn read(p: Point): Int {
  p.x
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)
	if sym := fa.Definitions[analysis.Pos{Line: 2, Col: 3}]; sym == nil || sym.Kind != analysis.SymbolField {
		t.Fatalf("expected field definition symbol at line 2 col 3, got %#v", sym)
	}
	for _, tok := range semtokens.Collect(fa) {
		if tok.Type == "property" {
			t.Fatalf("field semantic token should not be emitted: %+v", tok)
		}
		if tok.Line == 2 && tok.Col == 3 {
			t.Fatalf("semantic token should not cover field position: %+v", tok)
		}
	}
}

func TestCollect_QualifiedTypeSegmentsUseSourceSpan(t *testing.T) {
	src := `import std/json: FromJson, Json, ToJson

struct User {
  first_name: String
  age: Int
}

derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel}
derive FromJson for User with FromJson.Options{rename_all: Json.Case.Camel}
`
	fa := highlight.Analyze(src, std.Load())
	if fa == nil {
		t.Fatal("Analyze returned nil")
	}

	want := map[analysis.Pos]int{
		{Line: 8, Col: 29}: len("ToJson"),
		{Line: 8, Col: 36}: len("Options"),
		{Line: 8, Col: 56}: len("Json"),
		{Line: 8, Col: 61}: len("Case"),
		{Line: 9, Col: 31}: len("FromJson"),
		{Line: 9, Col: 40}: len("Options"),
		{Line: 9, Col: 60}: len("Json"),
		{Line: 9, Col: 65}: len("Case"),
	}
	got := make(map[analysis.Pos]semtokens.Token)
	for _, tok := range semtokens.Collect(fa) {
		got[analysis.Pos{Line: tok.Line, Col: tok.Col}] = tok
		if tok.Line == 8 && tok.Col <= 53 && tok.Col+tok.Length > 43 {
			t.Fatalf("semantic token bleeds into rename_all at line 8: %+v", tok)
		}
		if tok.Line == 9 && tok.Col <= 57 && tok.Col+tok.Length > 47 {
			t.Fatalf("semantic token bleeds into rename_all at line 9: %+v", tok)
		}
	}
	for pos, length := range want {
		tok, ok := got[pos]
		if !ok {
			t.Fatalf("expected semantic token at %+v; tokens: %+v", pos, semtokens.Collect(fa))
		}
		if tok.Length != length {
			t.Fatalf("token at %+v has length %d, want %d: %+v", pos, tok.Length, length, tok)
		}
	}
}

func TestCollect_ParamsUseParameterTokenType(t *testing.T) {
	src := `fn add_base(n: Int): Int {
  n + 1
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)
	toks := semtokens.Collect(fa)
	for _, tok := range toks {
		if tok.Type == "variable.parameter" {
			t.Fatalf("tree-sitter capture name should not be emitted as an LSP semantic token type: %+v", tok)
		}
	}
	sawDefinition := false
	sawReference := false
	for _, tok := range toks {
		if tok.Type != "parameter" {
			continue
		}
		if tok.Line == 1 && tok.Col == 13 && tok.Length == 1 {
			sawDefinition = true
		}
		if tok.Line == 2 && tok.Col == 3 && tok.Length == 1 {
			sawReference = true
		}
	}
	if !sawDefinition || !sawReference {
		t.Fatalf("expected parameter tokens for param definition and reference; def=%v ref=%v tokens=%+v", sawDefinition, sawReference, toks)
	}
}

func TestCollect_NestedFunctionParamsUseParameterTokenType(t *testing.T) {
	src := `test "nested params" {
  base = 100
  fn add_base(n: Int): Int {
    n + base
  }

  assert add_base(5) == 105
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)
	toks := semtokens.Collect(fa)
	sawDefinition := false
	sawReference := false
	for _, tok := range toks {
		if tok.Type != "parameter" {
			continue
		}
		if tok.Line == 3 && tok.Col == 15 && tok.Length == 1 {
			sawDefinition = true
		}
		if tok.Line == 4 && tok.Col == 5 && tok.Length == 1 {
			sawReference = true
		}
	}
	if !sawDefinition || !sawReference {
		t.Fatalf("expected parameter tokens for nested fn param definition and reference; def=%v ref=%v tokens=%+v", sawDefinition, sawReference, toks)
	}
}

func TestCollect_ImportPathSegmentsDoNotEmitSemanticTokens(t *testing.T) {
	src := `import {
  std/defer: Defer
  std/structs: Struct
  std/type: Type
  std/lists: List
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)

	blocked := map[analysis.Pos]string{
		{Line: 2, Col: 3}: "std",
		{Line: 2, Col: 7}: "defer",
		{Line: 3, Col: 3}: "std",
		{Line: 3, Col: 7}: "struct",
		{Line: 4, Col: 3}: "std",
		{Line: 4, Col: 7}: "type",
		{Line: 5, Col: 3}: "std",
		{Line: 5, Col: 7}: "list",
	}
	for _, tok := range semtokens.Collect(fa) {
		pos := analysis.Pos{Line: tok.Line, Col: tok.Col}
		if name, ok := blocked[pos]; ok {
			t.Fatalf("import path segment %q received semantic token %+v", name, tok)
		}
	}
}

func TestCollect_GoPackageHandleReferencesEmitNamespaceTokens(t *testing.T) {
	src := `gopkg "urltools"

pub fn escape_query(value: String): String go urltools.EscapeQuery
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)

	decl := analysis.Pos{Line: 1, Col: 8}
	ref := analysis.Pos{Line: 3, Col: 47}
	sawRef := false
	for _, tok := range semtokens.Collect(fa) {
		pos := analysis.Pos{Line: tok.Line, Col: tok.Col}
		if pos == decl {
			t.Fatalf("gopkg declaration string received semantic token %+v; syntax highlighting should own it", tok)
		}
		if pos == ref {
			if tok.Type != "namespace" || tok.Length != len("urltools") {
				t.Fatalf("gopkg handle reference token = %+v, want namespace length %d", tok, len("urltools"))
			}
			sawRef = true
		}
	}
	if !sawRef {
		t.Fatalf("missing gopkg handle namespace token at %+v; tokens: %+v", ref, semtokens.Collect(fa))
	}
}

func TestCollect_SelectiveStdEnumImportEmitsEnumToken(t *testing.T) {
	src := `import {
  std/channels.{self, Channel}
  std/context.{self, Context as Ctx}
  std/iter.Iter
  std/supervisors.{self, Restart}
  std/testing.Clock
}

fn main() {
  _ = Restart.Permanent
  Unit
}
`
	fa := highlight.Analyze(src, std.Load())
	if fa == nil {
		t.Fatal("Analyze returned nil")
	}

	for _, tok := range semtokens.Collect(fa) {
		if tok.Line == 5 && tok.Col == 26 {
			if tok.Type != "enum" || tok.Length != len("Restart") {
				pos := analysis.Pos{Line: 5, Col: 26}
				t.Fatalf("Restart import token should be enum length %d, got %+v; def=%#v ref=%#v",
					len("Restart"), tok, fa.Definitions[pos], fa.References[pos])
			}
			return
		}
	}
	pos := analysis.Pos{Line: 5, Col: 26}
	t.Fatalf("missing Restart import semantic token; def=%#v ref=%#v tokens: %+v",
		fa.Definitions[pos], fa.References[pos], semtokens.Collect(fa))
}

func TestCollect_GoBlocksDoNotEmitNomiSemanticTokens(t *testing.T) {
	src := `go {
  import sqlite_go "sqlite"
}

opaque type RawConn go {
  sqlite_go.Conn
}

fn open_raw(path: String): Result<RawConn, String> go {
  return sqlite_go.OpenRaw(path)
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)

	for _, tok := range semtokens.Collect(fa) {
		if tok.Line >= 1 && tok.Line <= 3 {
			t.Fatalf("raw Go block should be highlighted by injection, not Nomi semantic tokens; got %+v", tok)
		}
	}
}

func TestCollect_HoverOnlyKeywordMarkersDoNotEmitSemanticTokens(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
host type AssertionFailure

fn parse(): Result<Int, String> {
  Result.Ok(42)
}

tests "numbers" {
  setup {
    42
  }

  test "uses setup", value {
    parsed = try parse()
    assert parsed == value
  }
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)
	analysis.CheckTypes(fa, nodes)

	blocked := map[analysis.Pos]string{
		{Line: 8, Col: 1}:   "tests",
		{Line: 9, Col: 3}:   "setup",
		{Line: 13, Col: 3}:  "test",
		{Line: 14, Col: 14}: "try",
		{Line: 15, Col: 5}:  "assert",
	}
	for _, tok := range semtokens.Collect(fa) {
		pos := analysis.Pos{Line: tok.Line, Col: tok.Col}
		if name, ok := blocked[pos]; ok {
			t.Fatalf("hover-only keyword %q received semantic token %+v", name, tok)
		}
	}
}

func TestCollect_MultilineTestContextBindingsUseTheirOwnLines(t *testing.T) {
	src := `tests "setup semantics" {
  setup {
    {inherited: "parent", value: "child", child_only: "ready"}
  }

  test "setup binds its record", {
    inherited,
    value,
    child_only,
  } {
    assert inherited == "parent"
    assert value == "child"
    assert child_only == "ready"
  }
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)
	analysis.BuildTypes(fa, nodes)
	analysis.CheckTypes(fa, nodes)

	for _, pos := range []analysis.Pos{
		{Line: 7, Col: 5},
		{Line: 8, Col: 5},
		{Line: 9, Col: 5},
	} {
		sym := fa.Definitions[pos]
		if sym == nil {
			t.Fatalf("expected test context binding definition at %+v", pos)
		}
	}

	for _, tok := range semtokens.Collect(fa) {
		if tok.Line == 6 && tok.Col >= 8 && tok.Col <= 31 {
			t.Fatalf("semantic token leaked into test title: %+v", tok)
		}
	}
}

// TestCollect_EmbedsVariantNameSingleTypeToken pins the embeds-variant
// double-token regression: `embeds True` records BOTH a type
// Reference (walkTypeExpr on the EmbeddedTypeExpr) and a variant-symbol
// Definition at the same position, and Collect used to emit both —
// unstable sort order then picked the visible winner per position, so
// `embeds True` and `embeds False` in std/bool.nomi could render
// different colors. Collect now emits exactly one token per position,
// Reference-first (matching SymbolAt), so both embeds names get a
// single, deterministic "type" token.
func TestCollect_EmbedsVariantNameSingleTypeToken(t *testing.T) {
	src := `pub host type True

pub host type False

pub enum Bool {
  embeds True
  embeds False
}`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fa := buildFile(nodes)
	toks := semtokens.Collect(fa)
	// `True` / `False` both start at col 10 on lines 6 / 7.
	for _, want := range []struct{ line, col int }{{6, 10}, {7, 10}} {
		var got []semtokens.Token
		for _, tok := range toks {
			if tok.Line == want.line && tok.Col == want.col {
				got = append(got, tok)
			}
		}
		if len(got) != 1 {
			t.Errorf("line %d col %d: want exactly 1 token, got %d: %v", want.line, want.col, len(got), got)
			continue
		}
		if got[0].Type != "type" {
			t.Errorf("line %d col %d: want token type %q, got %q", want.line, want.col, "type", got[0].Type)
		}
	}
}

// TestAttachedTestLines_CoversEveryPromptLineAndNothingElse pins the line set
// the LSP's attachedTest modifier comes from: every line of every `//!`
// group, on top-level declarations and on impl-block items, continuation
// lines of a multi-line test included, and not the `//` line between two
// groups, the declarations, or their bodies.
func TestAttachedTestLines_CoversEveryPromptLineAndNothingElse(t *testing.T) {
	src := `struct Label {
  text: String
}

//! assert ready() == 1
//
//! xs = [1, 2]
//!   |> Iter.map(|x| x + ready())
//!   |> Iter.to_list()
//! assert xs == [2, 3]
fn ready(): Int {
  1
}

impl Label {
  //! assert Label.wrap("x").text == "[x]"
  fn wrap(text: String): Label {
    Label{text: "[${text}]"}
  }
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := semtokens.AttachedTestLines(nodes)
	want := map[int]bool{5: true, 7: true, 8: true, 9: true, 10: true, 16: true}
	for line := 1; line <= 21; line++ {
		if got[line] != want[line] {
			t.Errorf("line %d: in attached test = %v, want %v", line, got[line], want[line])
		}
	}
}

// TestCollect_DotVariantShorthandIsAnEnumMember: a `.Variant` gets the
// token its written `Enum.Variant` gets, whether its enum is the file's
// own, block-local, or a std enum the file never names.
func TestCollect_DotVariantShorthandIsAnEnumMember(t *testing.T) {
	src := `enum Color {
    Red
    Green
}

fn paint(c: Color): Int {
    case c {
        .Red -> 1
        .Green -> 2
    }
}

fn main() {
    _a = paint(.Red)
    _b = Iter.sort([3, 1, 2], .Descending)
    enum Local {
        One
        Two
    }
    pick = |l: Local| l
    _c = pick(.Two)
    _d = paint(Color.Red)
}
`
	fa := highlight.Analyze(src, std.Load())
	if fa == nil {
		t.Fatal("Analyze returned nil")
	}
	toks := map[analysis.Pos]semtokens.Token{}
	for _, tok := range semtokens.Collect(fa) {
		toks[analysis.Pos{Line: tok.Line, Col: tok.Col}] = tok
	}
	for _, want := range []struct {
		line, col int
		name      string
	}{
		{8, 10, "Red"},
		{14, 17, "Red"},
		{15, 32, "Descending"},
		{21, 16, "Two"},
		{22, 22, "Red"},
	} {
		tok, ok := toks[analysis.Pos{Line: want.line, Col: want.col}]
		if !ok || tok.Type != "enumMember" || tok.Length != len(want.name) {
			t.Errorf("%s at %d:%d: token %+v (present %v), want enumMember of length %d", want.name, want.line, want.col, tok, ok, len(want.name))
		}
	}
}
