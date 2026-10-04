package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// lowerSource parses src and runs LowerDerives, failing the test on any
// parse or lowering error.
func lowerSource(t *testing.T, src string) []ast.Node {
	t.Helper()
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	out, errs := LowerDerives(nodes)
	if len(errs) != 0 {
		t.Fatalf("LowerDerives(%q): unexpected errors: %v", src, errs)
	}
	return out
}

// implBlocksOf filters the top-level *ast.ImplBlock nodes out of a slice.
func implBlocksOf(nodes []ast.Node) []*ast.ImplBlock {
	var out []*ast.ImplBlock
	for _, n := range nodes {
		if ib, ok := n.(*ast.ImplBlock); ok {
			out = append(out, ib)
		}
	}
	return out
}

func TestLowerDerives_ImplBlockPassesThrough(t *testing.T) {
	src := `struct Dog {
  name: String
}

/// Speech behavior.
impl Speech for Dog {
  fn speak(_d: self): String {
    "woof"
  }
}

`
	out := lowerSource(t, src)
	if len(out) != 2 {
		t.Fatalf("expected 2 top-level nodes (decl + lowered impl), got %d", len(out))
	}
	sd, ok := out[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("node 0: expected *ast.StructDef, got %T", out[0])
	}
	if len(sd.Items) != 0 {
		t.Errorf("expected no decl Items, got %d", len(sd.Items))
	}
	if len(sd.Fields) != 1 {
		t.Errorf("expected Fields untouched, got %d", len(sd.Fields))
	}
	ib, ok := out[1].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("node 1: expected *ast.ImplBlock, got %T", out[1])
	}
	if ib.Interface == nil || TypeExprBaseName(ib.Interface) != "Speech" {
		t.Errorf("expected Interface Speech, got %v", ib.Interface)
	}
	recv, ok := ib.Receiver.(*ast.SimpleType)
	if !ok || recv.Name != "Dog" {
		t.Fatalf("expected SimpleType receiver Dog, got %#v", ib.Receiver)
	}
	if len(ib.Generics) != 0 {
		t.Errorf("expected no generics on the block, got %d", len(ib.Generics))
	}
	if len(ib.Items) != 1 {
		t.Fatalf("expected 1 method item, got %d", len(ib.Items))
	}
	if fn, ok := ib.Items[0].(*ast.FuncDef); !ok || fn.Name != "speak" {
		t.Errorf("expected fn speak carried over, got %T", ib.Items[0])
	}
	if ib.Doc == "" {
		t.Errorf("expected doc comment carried from the conformance line")
	}
	if ib.Line != 6 {
		t.Errorf("expected source impl block at line 6, got %d", ib.Line)
	}
	if ib.EndLine == 0 {
		t.Errorf("expected non-zero EndLine carried from the nested block")
	}
}

func TestLowerDerives_GenericImplBlockPassesThrough(t *testing.T) {
	src := `struct Box<T> {
  value: T
}

impl Container for Box<T>

`
	out := lowerSource(t, src)
	blocks := implBlocksOf(out)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 impl block, got %d", len(blocks))
	}
	ib := blocks[0]
	recv, ok := ib.Receiver.(*ast.GenericType)
	if !ok || recv.Name != "Box" {
		t.Fatalf("expected GenericType receiver Box<T>, got %#v", ib.Receiver)
	}
	if len(recv.Params) != 1 {
		t.Fatalf("expected 1 receiver type arg, got %d", len(recv.Params))
	}
	if arg, ok := recv.Params[0].(*ast.SimpleType); !ok || arg.Name != "T" {
		t.Errorf("expected receiver type arg T, got %#v", recv.Params[0])
	}
	if len(ib.Generics) != 0 {
		t.Fatalf("expected no opener generics, got %#v", ib.Generics)
	}
}

func TestLowerDerives_ConstrainedImplBlockKeepsBounds(t *testing.T) {
	src := `struct Range<T> where T: Comparable {}

impl Iter for Range<T> where T: Discrete {

  fn next(_r: Range<T>): Maybe<(T, Range<T>)> {
    none
  }
}

`
	out := lowerSource(t, src)
	blocks := implBlocksOf(out)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 impl block, got %d", len(blocks))
	}
	ib := blocks[0]
	if len(ib.Generics) != 0 {
		t.Fatalf("expected no opener generics, got %#v", ib.Generics)
	}
	var bounds []string
	for _, b := range ib.WhereClauses[0].Bounds {
		bounds = append(bounds, b.TypeString())
	}
	want := []string{"Discrete"}
	if len(bounds) != len(want) {
		t.Fatalf("bounds = %#v, want %#v", bounds, want)
	}
	for i := range want {
		if bounds[i] != want[i] {
			t.Fatalf("bounds = %#v, want %#v", bounds, want)
		}
	}
}

func TestLowerDerives_TypeBodyImplIsParseError(t *testing.T) {
	_, err := parser.Parse(lexer.Lex(`struct Box<T> {
  impl Iter for Box<T> where T: Display
}
`))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if got := err.Error(); got != "line 2, col 3: interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body" {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestLowerDerives_StructFnItemsAreParseErrors(t *testing.T) {
	_, err := parser.Parse(lexer.Lex(`struct Dog {
  name: String

  fn rename(d: Dog, n: String): Dog { Dog{name: n} }
}
`))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if got := err.Error(); got != "line 4, col 3: functions are declarations outside type bodies" {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestLowerDerives_EnumFnItemsAreParseErrors(t *testing.T) {
	_, err := parser.Parse(lexer.Lex(`enum Status {
  Active
  Pending Int

  pub fn label(s: Status): String {
    "x"
  }
}
`))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if got := err.Error(); got != "line 5, col 3: functions are declarations outside type bodies" {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestLowerDerives_ImplBlocksKeepStableOrder(t *testing.T) {
	src := `enum Status {
  Active
  Pending Int
}


impl Display for Status {
  fn to_string(_s: Status): String {
    "status"
  }
}

impl Speech for Status {
  fn speak(_s: Status): String {
    "hello"
  }
}

`
	out := lowerSource(t, src)
	if len(out) != 3 {
		t.Fatalf("expected decl + 2 impl blocks, got %d nodes", len(out))
	}
	ed, ok := out[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("node 0: expected *ast.EnumDef, got %T", out[0])
	}
	if len(ed.Variants) != 2 {
		t.Errorf("expected Variants untouched, got %d", len(ed.Variants))
	}
	if len(ed.Items) != 0 {
		t.Errorf("expected decl Items consumed, got %d", len(ed.Items))
	}
	// Stable source order: Display, then Speech.
	disp, ok := out[1].(*ast.ImplBlock)
	if !ok || disp.Interface == nil || TypeExprBaseName(disp.Interface) != "Display" {
		t.Fatalf("node 1: expected impl Display, got %T", out[1])
	}
	sp, ok := out[2].(*ast.ImplBlock)
	if !ok || sp.Interface == nil || TypeExprBaseName(sp.Interface) != "Speech" {
		t.Fatalf("node 2: expected impl Speech, got %T", out[2])
	}
	if recv := TypeExprBaseName(disp.Receiver); recv != "Status" {
		t.Errorf("expected receiver Status on Display block, got %q", recv)
	}
}

func TestLowerDerives_LoweredBlocksFollowTheirDecl(t *testing.T) {
	src := `struct Dog {
  name: String
}

impl Speech for Dog {
  fn speak(_d: self): String {
    "woof"
  }
}

struct Cat {
  name: String
}

impl Speech for Cat {
  fn speak(_c: Cat): String {
    "meow"
  }
}

`
	out := lowerSource(t, src)
	if len(out) != 4 {
		t.Fatalf("expected 4 nodes (2 decls + 2 blocks interleaved), got %d", len(out))
	}
	if _, ok := out[0].(*ast.StructDef); !ok {
		t.Errorf("node 0: expected Dog decl, got %T", out[0])
	}
	if ib, ok := out[1].(*ast.ImplBlock); !ok || TypeExprBaseName(ib.Receiver) != "Dog" {
		t.Errorf("node 1: expected Dog's lowered impl immediately after its decl, got %T", out[1])
	}
	if _, ok := out[2].(*ast.StructDef); !ok {
		t.Errorf("node 2: expected Cat decl, got %T", out[2])
	}
	if ib, ok := out[3].(*ast.ImplBlock); !ok || TypeExprBaseName(ib.Receiver) != "Cat" {
		t.Errorf("node 3: expected Cat's lowered impl after its decl, got %T", out[3])
	}
}

func TestLowerDerives_ExternTypeBodyIsParseError(t *testing.T) {
	_, err := parser.Parse(lexer.Lex(`pub host type List<T> {
  pub fn first(xs: List<T>): T {
    panic("x")
  }
}
`))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if got := err.Error(); got != "line 1, col 23: host type declarations do not take bodies; put host functions in impl blocks or beside the type" {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestLowerDerives_DistinctAndZeroSizedTypeBodies(t *testing.T) {
	src := `pub opaque type NonZeroInt Int

impl Display for NonZeroInt {
  fn to_string(_n: self): String {
    "n"
  }
}

type Sql

impl Literal for Sql {
  fn from_fragments(_fragments: List<Fragment<String>>): String {
    "q"
  }
}

`
	out := lowerSource(t, src)
	if len(out) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(out))
	}
	td, ok := out[0].(*ast.TypeDef)
	if !ok || len(td.Items) != 0 {
		t.Fatalf("node 0: expected consumed TypeDef, got %T", out[0])
	}
	ib1, ok := out[1].(*ast.ImplBlock)
	if !ok || TypeExprBaseName(ib1.Interface) != "Display" || TypeExprBaseName(ib1.Receiver) != "NonZeroInt" {
		t.Fatalf("node 1: expected impl Display for NonZeroInt, got %T", out[1])
	}
	ib2, ok := out[3].(*ast.ImplBlock)
	if !ok || TypeExprBaseName(ib2.Interface) != "Literal" || TypeExprBaseName(ib2.Receiver) != "Sql" {
		t.Fatalf("node 3: expected impl Literal for Sql, got %T", out[3])
	}
}

func TestLowerDerives_ImplReceiverPositionsAreReal(t *testing.T) {
	t.Run("interface impl", func(t *testing.T) {
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
		out := lowerSource(t, src)
		ib := implBlocksOf(out)[0]
		if IsSynthesizedLine(ib.Receiver.LineNum()) {
			t.Errorf("expected real receiver position, got synth-band line %d", ib.Receiver.LineNum())
		}
		if ib.Line != 9 {
			t.Errorf("expected impl block source line 9, got %d", ib.Line)
		}
		fa := BuildFile(out)
		for pos, sym := range fa.References {
			if pos.Line != 9 {
				continue
			}
			if (pos.Col == 6 && sym.Name == "Speech") || (pos.Col == 17 && sym.Name == "Dog") {
				continue
			}
			t.Errorf("unexpected Reference on the impl line at col %d: %s (kind %d)", pos.Col, sym.Name, sym.Kind)
		}
	})

	t.Run("generic impl", func(t *testing.T) {
		src := `interface Container {
  fn unwrap(c: self): Int
}

struct Box<T> {
  value: T
}

impl Container for Box<T> {
  fn unwrap(_b: Box<T>): Int {
    1
  }
}

`
		out := lowerSource(t, src)
		ib := implBlocksOf(out)[0]
		recv, ok := ib.Receiver.(*ast.GenericType)
		if !ok {
			t.Fatalf("expected GenericType receiver, got %#v", ib.Receiver)
		}
		if IsSynthesizedLine(recv.Line) {
			t.Errorf("expected real receiver head, got synth-band line %d", recv.Line)
		}
		for i, p := range recv.Params {
			if IsSynthesizedLine(p.LineNum()) {
				t.Errorf("receiver type arg %d: expected real position, got synth-band line %d", i, p.LineNum())
			}
		}
		fa := BuildFile(out)
		for pos, sym := range fa.References {
			if pos.Line != 9 {
				continue
			}
			if (pos.Col == 6 && sym.Name == "Container") || (pos.Col == 20 && sym.Name == "Box") || (pos.Col == 24 && sym.Name == "T") {
				continue
			}
			t.Errorf("unexpected Reference on the impl line at col %d: %s (kind %d)", pos.Col, sym.Name, sym.Kind)
		}
	})

}

func TestLowerDerives_NoBodiesPassThrough(t *testing.T) {
	src := `struct Point { x: Int; y: Int }

enum Color { Red; Green; Blue }

pub host type Handle

fn main() { 1 }`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, errs := LowerDerives(nodes)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(out) != len(nodes) {
		t.Fatalf("expected pass-through (no synthesized nodes), got %d -> %d", len(nodes), len(out))
	}
	for i := range out {
		if out[i] != nodes[i] {
			t.Errorf("node %d: expected identical node, got %T", i, out[i])
		}
	}
}

func TestLowerDerives_NestedImplPreservesGenericBounds(t *testing.T) {
	src := `struct StepByRange<T, S> where T: Comparable and Steppable<S> {
  value: T
  by: S
}

impl Iter for StepByRange<T, S> {
  fn next(_s: StepByRange<T, S>): Maybe<(T, StepByRange<T, S>)> {
    None
  }
}

`
	out := lowerSource(t, src)
	blocks := implBlocksOf(out)
	if len(blocks) != 1 {
		t.Fatalf("expected one impl block, got %d", len(blocks))
	}
	if len(blocks[0].Generics) != 0 {
		t.Fatalf("expected no opener generics, got %#v", blocks[0].Generics)
	}
	if len(blocks[0].WhereClauses) != 0 {
		t.Fatalf("expected impl block to inherit declaration bounds implicitly, got explicit where %#v", blocks[0].WhereClauses)
	}
}

func TestLowerDerives_DeriveWhereClausePropagatesToSynthImpl(t *testing.T) {
	src := `struct Box<T> {
  value: T
}

derive Display for Box<T> where T: Display
`
	out := lowerSource(t, src)
	blocks := implBlocksOf(out)
	if len(blocks) != 1 {
		t.Fatalf("expected one synthesized impl block, got %d", len(blocks))
	}
	if len(blocks[0].WhereClauses) != 1 || blocks[0].WhereClauses[0].Name != "T" {
		t.Fatalf("expected derive where clause on synthesized impl, got %#v", blocks[0].WhereClauses)
	}
}

func TestLowerDerives_Idempotent(t *testing.T) {
	src := `struct Dog {
  name: String
}


impl Speech for Dog {
  fn speak(_d: Dog): String {
    "woof"
  }
}

`
	once := lowerSource(t, src)
	twice, errs := LowerDerives(once)
	if len(errs) != 0 {
		t.Fatalf("second pass: unexpected errors: %v", errs)
	}
	if len(twice) != len(once) {
		t.Fatalf("expected second pass to be a no-op, got %d -> %d nodes", len(once), len(twice))
	}
	for i := range twice {
		if twice[i] != once[i] {
			t.Errorf("node %d: expected identical node after second pass, got %T", i, twice[i])
		}
	}
}
