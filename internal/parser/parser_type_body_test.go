package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Type-shape syntax.
//
// These tests cover concrete type-shape bodies (struct fields and enum
// variants), external impl blocks, optional bodies on extern/distinct types,
// plus rejection of non-shape items inside concrete type bodies.
// ---------------------------------------------------------------------------

func parseSingleStructDef(t *testing.T, src string) *ast.StructDef {
	t.Helper()
	nodes := parse(t, src)
	if len(nodes) != 1 {
		t.Fatalf("Parse(%q): expected 1 statement, got %d", src, len(nodes))
	}
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("Parse(%q): expected *ast.StructDef, got %T", src, nodes[0])
	}
	return sd
}

func parseSingleEnumDef(t *testing.T, src string) *ast.EnumDef {
	t.Helper()
	nodes := parse(t, src)
	if len(nodes) != 1 {
		t.Fatalf("Parse(%q): expected 1 statement, got %d", src, len(nodes))
	}
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("Parse(%q): expected *ast.EnumDef, got %T", src, nodes[0])
	}
	return ed
}

func parseError(t *testing.T, src string) error {
	t.Helper()
	tokens := lexer.Lex(src)
	_, err := Parse(tokens)
	if err == nil {
		t.Fatalf("Parse(%q): expected parse error, got nil", src)
	}
	return err
}

// --- struct bodies -----------------------------------------------------

func TestParseStructBody_FieldKeywordItems(t *testing.T) {
	src := `struct Dog {
  name: String
  breed: String = "lab"
}`
	sd := parseSingleStructDef(t, src)
	if len(sd.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sd.Fields))
	}
	if sd.Fields[0].Name != "name" || sd.Fields[0].TypeAnnotation.TypeString() != "String" {
		t.Errorf("field 0 = %+v", sd.Fields[0])
	}
	if sd.Fields[1].Name != "breed" {
		t.Errorf("field 1 = %+v", sd.Fields[1])
	}
	if sd.Fields[1].Default == nil {
		t.Errorf("expected default on field 'breed', got nil")
	}
	if len(sd.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(sd.Items))
	}
}

func TestParseTypeBody_TestItemsRejected(t *testing.T) {
	src := `struct Label {
  text: String

  test "renders" {
    assert Label.render(Label{text: "ready"}) == "[ready]"
  }
}`
	if err := parseError(t, src); !strings.Contains(err.Error(), "tests are declarations outside type bodies") {
		t.Fatalf("expected type-body test rejection, got %q", err.Error())
	}
}

// Struct bodies contain only fields; interface implementations stay as
// separate top-level blocks.
func TestParseStructBody_WithSiblingImplBlocks(t *testing.T) {
	src := `struct Dog {
  name: String
}


impl HasName for Dog

impl Speech for Dog {
  fn speak(_d: self): String {
    "woof"
  }
}

`
	nodes := parse(t, src)
	if len(nodes) != 3 {
		t.Fatalf("expected struct + 2 interface impl blocks, got %d nodes", len(nodes))
	}
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("node[0]: expected *ast.StructDef, got %T", nodes[0])
	}
	if len(sd.Fields) != 1 || sd.Fields[0].Name != "name" {
		t.Fatalf("expected 1 field 'name', got %#v", sd.Fields)
	}
	if len(sd.Items) != 0 {
		t.Fatalf("expected no type-body items, got %d", len(sd.Items))
	}

	empty, ok := nodes[1].(*ast.ImplBlock)
	if !ok || empty.Interface == nil || empty.Interface.TypeString() != "HasName" || len(empty.Items) != 0 {
		t.Fatalf("node[1]: expected empty HasName impl block, got %T %+v", nodes[1], nodes[1])
	}
	block, ok := nodes[2].(*ast.ImplBlock)
	if block.Interface == nil || block.Interface.TypeString() != "Speech" || len(block.Items) != 1 {
		t.Fatalf("impl block: expected Interface Speech with 1 method, got %+v", block)
	}
	speak, ok := block.Items[0].(*ast.FuncDef)
	if !ok || speak.Name != "speak" {
		t.Fatalf("block item[0]: expected fn speak, got %T", block.Items[0])
	}
}

func TestParseStructBody_EnumVariantShapeRejected(t *testing.T) {
	err := parseError(t, "struct Dog {\n  variant Pending\n}\n")
	if !strings.Contains(err.Error(), "expected ':' after field name") {
		t.Errorf("expected variant-in-struct rejection, got %q", err.Error())
	}
}

// Struct fields are newline- or semicolon-separated; commas are reserved for
// brace-bounded record-like shapes.
func TestParseStructBody_CommaSeparatedFieldsRejected(t *testing.T) {
	for _, src := range []string{
		"struct Point {x: Int, y: Int}\n",
		"struct Point {\n  x: Int,\n  y: Int,\n}\n",
	} {
		if err := parseError(t, src); !strings.Contains(err.Error(), "expected") {
			t.Errorf("Parse(%q): expected a parse error mentioning the expected item, got %q", src, err.Error())
		}
	}
}

func TestParseStructBody_FieldItemNamedField(t *testing.T) {
	sd := parseSingleStructDef(t, "struct Box {\n  field: Int\n}\n")
	if len(sd.Fields) != 1 || sd.Fields[0].Name != "field" {
		t.Fatalf("expected field named 'field', got %#v", sd.Fields)
	}
}

func TestParseStructBody_DocCommentsOnItems(t *testing.T) {
	src := `struct Dog {
  /// The dog's name.
  name: String
}


/// Speech behavior.
impl Speech for Dog
`
	nodes := parse(t, src)
	if len(nodes) != 2 {
		t.Fatalf("expected struct + interface impl block, got %d nodes", len(nodes))
	}
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("node[0]: expected *ast.StructDef, got %T", nodes[0])
	}
	if len(sd.Fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(sd.Fields))
	}
	if !strings.Contains(sd.Fields[0].Doc, "The dog's name") {
		t.Errorf("expected doc on field, got %q", sd.Fields[0].Doc)
	}
	if len(sd.Items) != 0 {
		t.Fatalf("expected no type-body items, got %d", len(sd.Items))
	}
	block, ok := nodes[1].(*ast.ImplBlock)
	if !ok || !strings.Contains(block.Doc, "Speech behavior") {
		t.Errorf("expected doc on impl block, got %T %q", nodes[1], block.Doc)
	}
}

func TestParseStructBody_InlineImplementsEntry(t *testing.T) {
	err := parseError(t, "struct Dog {\n  impl Speech\n}\n")
	if !strings.Contains(err.Error(), "interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body") {
		t.Fatalf("expected type-body impl rejection, got %q", err.Error())
	}
}

func TestParseStructBody_InlineImplementsDeriveEntry(t *testing.T) {
	err := parseError(t, "struct Dog {\n  derive Equatable\n}\n")
	if !strings.Contains(err.Error(), "outside the type body") {
		t.Fatalf("expected type-body derive rejection, got %q", err.Error())
	}
}

// Unsupported conformance spellings are parse errors pointing at the current
// `impl Iface` / `derive Iface for Type` syntax.
func TestParseStructBody_UnsupportedConformanceSpellingsRejected(t *testing.T) {
	err := parseError(t, "struct Dog {\n  derive impl Equatable\n}\n")
	if !strings.Contains(err.Error(), "a derived interface conformance is `derive Iface for Type`") ||
		!strings.Contains(err.Error(), "`derive impl` is not a conformance keyword") {
		t.Errorf("expected `derive impl` conformance rejection pointing at `derive Iface`, got %q", err.Error())
	}
	err = parseError(t, "struct Dog {\n  derives Equatable\n}\n")
	if !strings.Contains(err.Error(), "a derived interface conformance is `derive Iface for Type`") ||
		!strings.Contains(err.Error(), "`derives` is not a conformance keyword") {
		t.Errorf("expected bare `derives` rejection pointing at `derive Iface`, got %q", err.Error())
	}
}

// Sibling `derive` declarations parse to *ast.ImplConformance nodes.
func TestParseStructBody_ConformanceAndDeriveItems(t *testing.T) {
	src := `struct Dog {
  name: String
}
derive Equatable for Dog
derive Hashable for Dog

impl Named for Dog

impl Speech for Dog {
  fn speak(_d: self): String {
    "woof"
  }
}
`
	nodes := parse(t, src)
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("node[0]: expected *ast.StructDef, got %T", nodes[0])
	}
	if len(sd.Fields) != 1 || len(sd.Items) != 0 {
		t.Fatalf("expected 1 field + no body items, got %d fields, %d items", len(sd.Fields), len(sd.Items))
	}
	wantConfs := []struct {
		iface  string
		derive bool
	}{{"Equatable", true}, {"Hashable", true}}
	for i, want := range wantConfs {
		conf, ok := nodes[i+1].(*ast.ImplConformance)
		if !ok || conf.Derive != want.derive || conf.Interface.TypeString() != want.iface || conf.Receiver.TypeString() != "Dog" {
			t.Errorf("node[%d]: expected `%s` derive=%v, got %T %+v", i+1, want.iface, want.derive, nodes[i+1], nodes[i+1])
		}
	}
	if block, ok := nodes[3].(*ast.ImplBlock); !ok || block.Interface.TypeString() != "Named" {
		t.Errorf("node[3]: expected `impl Named for Dog`, got %T %+v", nodes[3], nodes[3])
	}
	if block, ok := nodes[4].(*ast.ImplBlock); !ok || block.Interface.TypeString() != "Speech" || len(block.Items) != 1 {
		t.Errorf("node[4]: expected `impl Speech for Dog { ... }`, got %T %+v", nodes[4], nodes[4])
	}
}

func TestParseStructBody_MultipleDerivedConformances(t *testing.T) {
	src := `struct Dog {
  name: String
}
derive Equatable for Dog
derive Hashable for Dog

impl Speech for Dog {
  fn speak(_d: self): String {
    "woof"
  }
}

`
	nodes := parse(t, src)
	sd, ok := nodes[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("node[0]: expected *ast.StructDef, got %T", nodes[0])
	}
	if len(sd.Items) != 0 {
		t.Fatalf("expected no type-body items, got %d", len(sd.Items))
	}
	want := []struct {
		iface  string
		derive bool
	}{{"Equatable", true}, {"Hashable", true}}
	for i, w := range want {
		conf, ok := nodes[i+1].(*ast.ImplConformance)
		if !ok || conf.Interface.TypeString() != w.iface || conf.Derive != w.derive {
			t.Errorf("node[%d]: expected `%s` derive=%v, got %T %+v", i+1, w.iface, w.derive, nodes[i+1], nodes[i+1])
		}
	}
	block, ok := nodes[3].(*ast.ImplBlock)
	if !ok || block.Interface.TypeString() != "Speech" || len(block.Items) != 1 {
		t.Errorf("node[3]: expected Speech impl block, got %T %+v", nodes[3], nodes[3])
	}
}

func TestParseConformanceBlock_Rejected(t *testing.T) {
	err := parseError(t, "struct Dog {\n  impl { }\n}\n")
	if !strings.Contains(err.Error(), "interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body") {
		t.Errorf("expected grouped-block rejection, got %q", err.Error())
	}
}

func TestParseTypeBody_ImplBlockPubFnRejected(t *testing.T) {
	cases := []string{
		`impl Speech for Dog {
  pub fn speak(d: self): String { "woof" }
}`,
		`impl Speech for Dog {
  pub host fn speak(d: self): String
}`,
	}
	for _, src := range cases {
		err := parseError(t, src)
		if !strings.Contains(err.Error(), "do not take `pub`") {
			t.Errorf("expected impl-block pub rejection, got %q", err.Error())
		}
	}
}

func TestParseTypeBody_NestedImplBlock(t *testing.T) {
	err := parseError(t, "struct Dog {\n  impl Speech {\n    fn speak(d: self): String { \"woof\" }\n  }\n}\n")
	if !strings.Contains(err.Error(), "interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body") {
		t.Fatalf("expected nested impl rejection, got %q", err.Error())
	}
}

// A type-body `derive Iface { ... }` entry is rejected — derives are sibling
// declarations.
func TestParseConformanceLine_DeriveBodyRejected(t *testing.T) {
	err := parseError(t, "struct Dog {\n  derive Equatable {\n    fn eq(a: Dog, b: Dog): Bool { True }\n  }\n}\n")
	if !strings.Contains(err.Error(), "outside the type body") {
		t.Errorf("expected derive-body rejection, got %q", err.Error())
	}
}

// `impl` inside a type body is rejected before any `for`-clause or
// type-argument parsing.
func TestParseConformanceLine_ImplementsForAndTypeArgRejected(t *testing.T) {
	err := parseError(t, "struct Dog {\n  impl Speech for Dog\n}\n")
	if !strings.Contains(err.Error(), "interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body") {
		t.Errorf("expected impl `for` rejection, got %q", err.Error())
	}
	err = parseError(t, "struct Box {\n  impl iter<T>\n}\n")
	if !strings.Contains(err.Error(), "interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body") {
		t.Errorf("expected impl type-arg rejection, got %q", err.Error())
	}
}

// --- enum bodies --------------------------------------------------------

func TestParseEnumBody_VariantKeywordItems(t *testing.T) {
	src := `enum Status {
  Active
  Pending Int
  Card {rank: Int}
  embeds Click
}

impl Display for Status {
  fn to_string(_s: self): String {
    "status"
  }
}

`
	nodes := parse(t, src)
	if len(nodes) != 2 {
		t.Fatalf("expected enum + impl block, got %d nodes", len(nodes))
	}
	ed, ok := nodes[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("node[0]: expected *ast.EnumDef, got %T", nodes[0])
	}
	if len(ed.Variants) != 4 {
		t.Fatalf("expected 4 variants, got %d", len(ed.Variants))
	}
	if ed.Variants[0].Name != "Active" || ed.Variants[0].Kind != "bare" {
		t.Errorf("variant 0 = %+v", ed.Variants[0])
	}
	if ed.Variants[1].Name != "Pending" || ed.Variants[1].Kind != "positional" {
		t.Errorf("variant 1 = %+v", ed.Variants[1])
	}
	if ed.Variants[1].DataTypeExpr == nil || ed.Variants[1].DataTypeExpr.TypeString() != "Int" {
		t.Errorf("variant 1 payload = %v", ed.Variants[1].DataTypeExpr)
	}
	if ed.Variants[2].Name != "Card" || ed.Variants[2].Kind != "struct" || len(ed.Variants[2].Fields) != 1 {
		t.Errorf("variant 2 = %+v", ed.Variants[2])
	}
	if ed.Variants[3].Name != "Click" || ed.Variants[3].Kind != "embedded" {
		t.Errorf("variant 3 = %+v", ed.Variants[3])
	}

	if len(ed.Items) != 0 {
		t.Fatalf("expected no enum body items, got %d", len(ed.Items))
	}
	block, ok := nodes[1].(*ast.ImplBlock)
	if !ok || block.Interface.TypeString() != "Display" || len(block.Items) != 1 {
		t.Fatalf("node[1]: expected Display impl block, got %T %+v", nodes[1], nodes[1])
	}
	toString, ok := block.Items[0].(*ast.FuncDef)
	if !ok || toString.Name != "to_string" {
		t.Errorf("block item[0]: expected fn to_string, got %T %+v", block.Items[0], block.Items[0])
	}
}

func TestParseEnumBody_FieldItemRejected(t *testing.T) {
	err := parseError(t, "enum Status {\n  Active\n  field x: Int\n}\n")
	if !strings.Contains(err.Error(), "expected variant name or 'embeds'") {
		t.Errorf("expected field-in-enum rejection, got %q", err.Error())
	}
}

func TestParseEnumBody_DocCommentOnVariant(t *testing.T) {
	src := `enum Status {
  /// Still waiting.
  Pending

  Active
}`
	ed := parseSingleEnumDef(t, src)
	if len(ed.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(ed.Variants))
	}
	if !strings.Contains(ed.Variants[0].Doc, "Still waiting") {
		t.Errorf("expected doc on variant, got %q", ed.Variants[0].Doc)
	}
}

// The retired `V1 | V2` alternation form is a parse error.
func TestParseEnumBody_PipeFormRejected(t *testing.T) {
	for _, src := range []string{
		"enum Color { Red | Green | Blue }\n",
		"enum Color {\n  Red\n  | Green\n  | Blue\n}\n",
	} {
		if err := parseError(t, src); !strings.Contains(err.Error(), "expected") {
			t.Errorf("Parse(%q): expected alternation-form parse error, got %q", src, err.Error())
		}
	}
}

func TestParseStructBody_OnceItemRejected(t *testing.T) {
	err := parseError(t, `struct Cache {
  seed: Int

  pub once Default: Int = 1
}
`)
	if !strings.Contains(err.Error(), "once bindings are declarations outside type bodies") {
		t.Fatalf("expected once rejection, got %q", err.Error())
	}
}

func TestParseEnumBody_FnItemRejected(t *testing.T) {
	src := `enum Status {
  Active

  pub fn label(s: self): String { "x" }
}
`
	err := parseError(t, src)
	if !strings.Contains(err.Error(), "functions are declarations outside type bodies") {
		t.Fatalf("expected enum fn rejection, got %q", err.Error())
	}
}

// --- conformance-line rejections -----------------------------------------

func TestParseConformanceLine_ForClauseRejected(t *testing.T) {
	err := parseError(t, "struct Dog {\n  impl Speech for Dog\n}\n")
	if !strings.Contains(err.Error(), "interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body") {
		t.Errorf("expected type-body impl rejection, got %q", err.Error())
	}
}

func TestParseImplBlock_GenericBounds(t *testing.T) {
	nodes := parse(t, `struct Box<T> {}

impl iter for Box<T> where T: Display

`)
	if len(nodes) != 2 {
		t.Fatalf("expected struct + impl block, got %d nodes", len(nodes))
	}
	blk, ok := nodes[1].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected ImplBlock, got %T", nodes[1])
	}
	if len(blk.Generics) != 0 {
		t.Fatalf("expected no opener generics, got %#v", blk.Generics)
	}
	if len(blk.WhereClauses) != 1 || blk.WhereClauses[0].Name != "T" {
		t.Fatalf("expected where T, got %#v", blk.WhereClauses)
	}
	if len(blk.WhereClauses[0].Bounds) != 1 || blk.WhereClauses[0].Bounds[0].TypeString() != "Display" {
		t.Fatalf("expected T: Display bound, got %#v", blk.WhereClauses[0].Bounds)
	}
}

// --- host type bodies ---------------------------------------------------

func TestParseExternType_WithBodyRejected(t *testing.T) {
	src := `pub host type List<T> {
  fn first(xs: self): T {
    panic("x")
  }
}

impl iter for List<T> {
  host fn next(list: self): Maybe<(T, self)>
}

`
	err := parseError(t, src)
	if !strings.Contains(err.Error(), "host type declarations do not take bodies") {
		t.Fatalf("expected host type body rejection, got %q", err.Error())
	}
}

func TestParseExternType_BodilessUnchanged(t *testing.T) {
	nodes := parse(t, "pub host type Handle\n")
	et, ok := nodes[0].(*ast.ExternType)
	if !ok {
		t.Fatalf("expected *ast.ExternType, got %T", nodes[0])
	}
	if et.HasBody || len(et.Items) != 0 {
		t.Errorf("expected bodiless host type, got HasBody=%v items=%d", et.HasBody, len(et.Items))
	}
}

func TestParseExternType_FieldItemRejected(t *testing.T) {
	err := parseError(t, "pub host type List<T> {\n  field len: Int\n}\n")
	if !strings.Contains(err.Error(), "host type declarations do not take bodies") {
		t.Errorf("expected field-in-extern-type rejection, got %q", err.Error())
	}
}

// --- distinct / zero-sized type bodies -------------------------------------

func TestParseTypeDef_OpaqueDistinctWithBody(t *testing.T) {
	src := `pub opaque type NonZeroInt Int

impl Display for NonZeroInt {
  fn to_string(_n: self): String {
    "n"
  }
}

`
	nodes := parse(t, src)
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected *ast.TypeDef, got %T", nodes[0])
	}
	if td.Name != "NonZeroInt" || !td.Public || !td.Opaque {
		t.Errorf("type def header = %+v", td)
	}
	if td.InnerTypeExpr == nil || td.InnerTypeExpr.TypeString() != "Int" {
		t.Errorf("expected inner type Int, got %v", td.InnerTypeExpr)
	}
	if td.HasBody || len(td.Items) != 0 {
		t.Fatalf("expected bodiless type def, got HasBody=%v items=%d", td.HasBody, len(td.Items))
	}
	block, ok := nodes[1].(*ast.ImplBlock)
	if !ok || block.Interface.TypeString() != "Display" || len(block.Items) != 1 {
		t.Errorf("node[1]: expected Display impl block, got %T %+v", nodes[1], nodes[1])
	}
}

func TestParseTypeDef_ZeroSizedWithLiteralImpl(t *testing.T) {
	src := `type Sql

impl Literal for Sql {
  fn from_fragments(_fragments: List<Fragment<String>>): String {
    "q"
  }
}

`
	nodes := parse(t, src)
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected *ast.TypeDef, got %T", nodes[0])
	}
	if td.Name != "Sql" || td.InnerTypeExpr != nil {
		t.Errorf("expected zero-sized Sql, got %+v", td)
	}
	if td.HasBody || len(td.Items) != 0 {
		t.Fatalf("expected bodiless type def, got HasBody=%v items=%d", td.HasBody, len(td.Items))
	}
	block, ok := nodes[1].(*ast.ImplBlock)
	if !ok || block.Interface.TypeString() != "Literal" || len(block.Items) != 1 {
		t.Errorf("node[1]: expected Literal impl block, got %T %+v", nodes[1], nodes[1])
	}
}

func TestParseTypeDef_BodilessUnchanged(t *testing.T) {
	nodes := parse(t, "type Expired\n")
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("expected *ast.TypeDef, got %T", nodes[0])
	}
	if td.HasBody || len(td.Items) != 0 {
		t.Errorf("expected bodiless type def, got HasBody=%v items=%d", td.HasBody, len(td.Items))
	}
}

// A distinct `type` with an inner type takes no body, so a stray item inside
// braces is rejected at the declaration rather than parsed as a member.
func TestParseTypeDef_StrayBodyItemRejected(t *testing.T) {
	err := parseError(t, "type Id Int {\n  Foo\n}\n")
	if !strings.Contains(err.Error(), "distinct `type` declarations do not take bodies") {
		t.Errorf("expected body-on-a-distinct-type rejection, got %q", err.Error())
	}
}

// The anon-struct-RHS carve-out keeps its diagnostic: `type X {x: Int}` is
// still rejected because `type` declarations do not carry bodies; use
// `struct` for records.
func TestParseTypeDef_BareFieldBodyStillRejected(t *testing.T) {
	err := parseError(t, "type MyStruct {x: Int}\n")
	if !strings.Contains(err.Error(), "distinct `type` declarations do not take bodies") {
		t.Errorf("expected brace-body rejection, got %q", err.Error())
	}
}

// Doc comments inside an interface body land on the right member: contract
// members (required fn, open default, host-backed default, field requirement)
// carry them on the InterfaceMethod.Doc / InterfaceField.Doc slots. Before the
// slots existed the parser consumed these docs without attaching them — `nomi
// fmt -w` then deleted them.
func TestParseInterfaceDef_DocCommentsOnMembers(t *testing.T) {
	src := `interface Speech {
  /// The speaker's name.
  field name: String

  /// The required noise.
  fn speak(s: self): String

  /// Overridable politeness.
  open fn greet(_s: self): String { "hi" }

  /// Host-backed shout.
  host fn shout(s: self): String
}`
	nodes := parse(t, src)
	id, ok := nodes[0].(*ast.InterfaceDef)
	if !ok {
		t.Fatalf("expected *ast.InterfaceDef, got %T", nodes[0])
	}
	if len(id.Fields) != 1 || !strings.Contains(id.Fields[0].Doc, "The speaker's name") {
		t.Errorf("expected doc on field requirement, got %#v", id.Fields)
	}
	if len(id.Methods) != 3 {
		t.Fatalf("expected 3 contract methods, got %d", len(id.Methods))
	}
	if id.Methods[0].Name != "speak" || !strings.Contains(id.Methods[0].Doc, "The required noise") {
		t.Errorf("expected doc on required method, got %q", id.Methods[0].Doc)
	}
	if id.Methods[1].Name != "greet" || !strings.Contains(id.Methods[1].Doc, "Overridable politeness") {
		t.Errorf("expected doc on open default method, got %q", id.Methods[1].Doc)
	}
	if id.Methods[2].Name != "shout" || !id.Methods[2].Extern || !strings.Contains(id.Methods[2].Doc, "Host-backed shout") {
		t.Errorf("expected doc on host-backed default, got %q", id.Methods[2].Doc)
	}
}

// `@derive` in source is no longer a decorator — it is rejected at the
// decorator-name stage with a targeted message pointing at `derive Iface`.
func TestParseSourceDeriveDecoratorRejected(t *testing.T) {
	for _, src := range []string{
		"@derive Equatable\nstruct Point {\n  x: Int\n}\n",
		"@derive Debug\nenum Color {\n  Red\n}\n",
		"@derive Hashable\ntype Id Int\n",
		"@derive Equatable, Hashable\npub host type Tok\n",
	} {
		err := parseError(t, src)
		if !strings.Contains(err.Error(), "`@derive` is not supported") {
			t.Errorf("Parse(%q): expected `@derive` rejection, got %q", src, err.Error())
		}
	}
}

// Dual-window compatibility: a plain (non-open, non-pub) fn WITH body inside
// an interface keeps today's meaning — a final default method (an
// InterfaceMethod with Body and Open=false) — because the analyzer and its
// tests depend on that form. The design's reclassification of plain
// fn-with-body as an inherent item lands with the corpus migration.
func TestParseInterfaceDef_PlainFnWithBodyStaysFinalDefault(t *testing.T) {
	src := `interface Greeter {
  fn greet(_g: self): String { "hi" }
}`
	nodes := parse(t, src)
	id, ok := nodes[0].(*ast.InterfaceDef)
	if !ok {
		t.Fatalf("expected *ast.InterfaceDef, got %T", nodes[0])
	}
	if len(id.Methods) != 1 || id.Methods[0].Body == nil || id.Methods[0].Open {
		t.Fatalf("expected 1 final-default method, got %#v", id.Methods)
	}
}

// --- `field` / `variant` stay ordinary identifiers in expressions ------------

func TestFieldAndVariantAsOrdinaryIdentifiers(t *testing.T) {
	src := `fn f(field: Int, variant: Int): Int {
  field = field + 1
  variant = variant * 2
  field + variant
}`
	nodes := parse(t, src)
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected *ast.FuncDef, got %T", nodes[0])
	}
	if fd.Params[0].Name != "field" || fd.Params[1].Name != "variant" {
		t.Errorf("params = %#v", fd.Params)
	}
}

// Only a struct-shaped variant's named fields take defaults. A default written
// after a positional payload or a bare variant is rejected with a message that
// names the struct-shaped form, not with the generic separator error.
func TestParseEnumBody_VariantDefaultsAreRejectedByName(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"enum Shape {\n  Circle Float = 3.14\n}\n",
			"line 2, col 16: a positional variant payload can't have a default; name the field to give it one: `Circle {<name>: Float = <value>}`"},
		{"enum Direction {\n  North = 1\n  South\n}\n",
			"line 2, col 9: enum variant `North` carries no value, so it can't be assigned one"},
	} {
		err := parseError(t, tc.src)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%q: got %v, want %q", tc.src, err, tc.want)
		}
	}
	shape := parseSingleEnumDef(t, "enum Shape {\n  Rectangle {width: Float = 2.1, height: Float}\n}\n")
	if len(shape.Variants) != 1 || len(shape.Variants[0].Fields) != 2 || shape.Variants[0].Fields[0].Default == nil {
		t.Fatal("a struct-shaped variant's field default is no longer parsed")
	}
}
