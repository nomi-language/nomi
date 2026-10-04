package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// lowerNewform parses src and runs LowerDerives, returning the lowered
// node slice and any lowering-time TypeErrors (the home of the interface-
// header consistency + derive checks). Parse errors fail the test.
func lowerNewform(t *testing.T, src string) ([]ast.Node, []analysis.TypeError) {
	t.Helper()
	nodes, perr := parser.Parse(lexer.Lex(src))
	if perr != nil {
		t.Fatalf("Parse(%q): %v", src, perr)
	}
	return analysis.LowerDerives(nodes)
}

func implBlocks(nodes []ast.Node) []*ast.ImplBlock {
	var out []*ast.ImplBlock
	for _, n := range nodes {
		if ib, ok := n.(*ast.ImplBlock); ok {
			out = append(out, ib)
		}
	}
	return out
}

func findImplBlock(nodes []ast.Node, iface, recv string) *ast.ImplBlock {
	for _, ib := range implBlocks(nodes) {
		if ib.Interface == nil {
			continue
		}
		if analysis.TypeExprBaseName(ib.Interface) == iface && analysis.TypeExprBaseName(ib.Receiver) == recv {
			return ib
		}
	}
	return nil
}

func expectLowerClean(t *testing.T, errs []analysis.TypeError) {
	t.Helper()
	if len(errs) != 0 {
		for _, e := range errs {
			t.Logf("  %s", e.Error())
		}
		t.Fatalf("expected no lowering errors, got %d", len(errs))
	}
}

func expectLowerErr(t *testing.T, errs []analysis.TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return
		}
	}
	for _, e := range errs {
		t.Logf("  %s", e.Error())
	}
	t.Fatalf("expected a lowering error containing %q, got %d", substr, len(errs))
}

// --- Lowering: source impl blocks and derive declarations ---

func TestHeaderLower_ConformanceAndTagsRouteToBlocks(t *testing.T) {
	src := `struct Point {
  x: Int
  y: Int
}


impl Display for Point {
  fn to_string(_p: Point): String {
    "p"
  }
}

`
	out, errs := lowerNewform(t, src)
	expectLowerClean(t, errs)

	disp := findImplBlock(out, "Display", "Point")
	if disp == nil {
		t.Fatalf("expected a lowered `impl Display for Point` block")
	}
	if len(disp.Items) != 1 {
		t.Fatalf("expected the Display method routed into the block, got %d items", len(disp.Items))
	}
	if fn, ok := disp.Items[0].(*ast.FuncDef); !ok || fn.Name != "to_string" {
		t.Errorf("expected to_string routed into the Display block, got %T", disp.Items[0])
	}
	// Source impl blocks pass through lowering with their real source position.
	if disp.Line != 7 {
		t.Errorf("expected Display block at source line 7, got %d", disp.Line)
	}
}

func TestHeaderLower_EmptyConformanceIsFieldOnly(t *testing.T) {
	// A field-only/default-only interface implementation can be a bodyless
	// source impl declaration.
	src := `struct Widget {
  n: Int
}

impl Drawable for Widget
`
	out, errs := lowerNewform(t, src)
	expectLowerClean(t, errs)
	blk := findImplBlock(out, "Drawable", "Widget")
	if blk == nil {
		t.Fatalf("expected a bodyless `impl Drawable for Widget` declaration")
	}
	if len(blk.Items) != 0 {
		t.Errorf("expected an empty block, got %d items", len(blk.Items))
	}
}

// --- Lowering: derives ---

func TestHeaderLower_DeriveImplSynthesizes(t *testing.T) {
	src := `struct Point {
  x: Int
  y: Int

}
derive Equatable for Point`
	out, errs := lowerNewform(t, src)
	expectLowerClean(t, errs)
	blk := findImplBlock(out, "Equatable", "Point")
	if blk == nil {
		t.Fatalf("expected a synthesized `impl Equatable for Point` block")
	}
	if len(blk.Items) == 0 {
		t.Errorf("expected the synthesized equal? method, got an empty block")
	}
	if !analysis.IsSynthesizedLine(blk.Line) {
		t.Errorf("expected the derived block at a synth-band position, got line %d", blk.Line)
	}
}

func TestHeaderLower_DeriveNonDerivableErrors(t *testing.T) {
	src := `struct Point {
  x: Int

}
derive Speech for Point`
	_, errs := lowerNewform(t, src)
	expectLowerErr(t, errs, "unknown derivable protocol")
}

func TestHeaderLower_DeriveAndManualImplConflict(t *testing.T) {
	src := `struct Point {
  x: Int

}
derive Equatable for Point

impl Equatable for Point {
  fn equal?(_a: self, _b: self): Bool {
    True
  }
}
`
	out, errs := lowerNewform(t, src)
	expectLowerErr(t, errs, "conflicts with a hand-written `impl Equatable for Point")
	if len(implBlocks(out)) != 1 {
		t.Fatalf("expected only the manual impl block after conflict, got %d", len(implBlocks(out)))
	}
}

// --- Lowering: foreign-type `impl Iface for Type` blocks ---

func TestHeaderLower_ImplBlockRoutesMethods(t *testing.T) {
	src := `impl Display for Money {
  fn to_string(_m: self): String { "money" }
}`
	out, errs := lowerNewform(t, src)
	expectLowerClean(t, errs)
	// A foreign `impl Iface for Type` block parses straight into an
	// ast.ImplBlock and passes through lowering unchanged — one block remains.
	if len(out) != 1 {
		t.Fatalf("expected one block, got %d nodes", len(out))
	}
	blk := findImplBlock(out, "Display", "Money")
	if blk == nil {
		t.Fatalf("expected `impl Display for Money`")
	}
	if analysis.TypeExprBaseName(blk.Receiver) != "Money" {
		t.Errorf("expected receiver Money, got %q", analysis.TypeExprBaseName(blk.Receiver))
	}
	if len(blk.Items) != 1 {
		t.Errorf("expected to_string in the block, got %d items", len(blk.Items))
	}
}

func TestHeaderLower_ImplBlockMultiInterface(t *testing.T) {
	// A foreign type implementing two interfaces is two separate
	// `impl Iface for Type` blocks, each carrying the receiver's generics.
	src := `impl iter for Box<T> {
  fn next(_b: self): Maybe<(T, self)> { None }
}

impl Display for Box<T> {
  fn to_string(_b: Box<T>): String { "box" }
}`
	out, errs := lowerNewform(t, src)
	expectLowerClean(t, errs)
	iter := findImplBlock(out, "iter", "Box")
	disp := findImplBlock(out, "Display", "Box")
	if iter == nil || disp == nil {
		t.Fatalf("expected one block per interface (iter + Display), got %d blocks", len(implBlocks(out)))
	}
	if len(iter.Generics) != 0 {
		t.Errorf("expected receiver-inferred generics, got opener generics %#v", iter.Generics)
	}
}

// --- Lowering: duplicate conformance consistency ---

func TestHeaderLower_NestedImplWithoutBodylessConformanceIsValid(t *testing.T) {
	src := `struct Point {
  x: Int
}

impl Display for Point {
  fn to_string(_p: self): String {
    "p"
  }
}

`
	out, errs := lowerNewform(t, src)
	expectLowerClean(t, errs)
	if findImplBlock(out, "Display", "Point") == nil {
		t.Fatalf("expected nested Display impl block to lower")
	}
}

func TestHeaderLower_DuplicateConformanceErrors(t *testing.T) {
	src := `struct Point {
  x: Int

}
derive Equatable for Point
derive Equatable for Point`
	_, errs := lowerNewform(t, src)
	expectLowerErr(t, errs, "duplicate `derive Equatable for Point` declaration")
}

func TestHeaderLower_TypeBodyImplIsParseError(t *testing.T) {
	src := `struct Point {
  x: Int

  impl Equatable
}
derive Equatable for Point`
	_, perr := parser.Parse(lexer.Lex(src))
	if perr == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(perr.Error(), "interface implementations are written as `impl Iface for Type { ... }` blocks outside the type body") {
		t.Fatalf("unexpected parse error: %v", perr)
	}
}

// --- Full project pipeline: dispatch / coherence over the lowered form ---

func TestHeaderProject_TypeBodiesAndImplBlocks(t *testing.T) {
	// Display (conformance + tag) + derive Equatable + a foreign-type
	// `impl Iface for Type` block on a second type — all the surface forms
	// in one file, analyzed clean.
	src := `
pub struct Point {
  x: Int
  y: Int
}

pub fn manhattan(p: Point): Int {
  p.x + p.y
}

derive Equatable for Point

impl Display for Point {
  fn to_string(_p: Point): String {
    "(point)"
  }
}

pub struct Money {
  cents: Int
}

impl Display for Money {
  fn to_string(_m: Money): String {
    "money"
  }
}

`
	expectNoErrs(t, checkUniversalDebugProject(t, src))
}

func TestHeaderProject_DupNameTwoInterfacesClean(t *testing.T) {
	// Two interfaces declaring the same method name, satisfied by one type via
	// distinct impl blocks — each block scopes its own implementation, so
	// the redeclaration and collision checks see no duplicate.
	src := `interface Greeter {
  fn hello(g: self): String
}

interface Farewell {
  fn hello(f: self): String
}

struct Person {
  name: String
}

impl Greeter for Person {
  fn hello(p: Person): String {
    p.name
  }
}

impl Farewell for Person {
  fn hello(p: Person): String {
    p.name
  }
}

`
	expectNoErrs(t, checkUniversalDebugProject(t, src))
}

func TestHeaderProject_DupNameModuleFunctionAndInterfaceClean(t *testing.T) {
	// A module `fn foo` coexists with an impl method of the same name; the impl
	// method belongs to interface dispatch rather than the module scope.
	src := `interface A {
  fn foo(x: self): Int
}

pub struct T {
  n: Int
}

pub fn foo(x: T): Int {
  x.n
}

impl A for T {
  fn foo(x: T): Int {
    x.n
  }
}

`
	expectNoErrs(t, checkUniversalDebugProject(t, src))
}

func TestHeaderProject_SameInterfaceSameMethodCollides(t *testing.T) {
	// Two functions for the same interface implementation collide on the
	// (Iface, method, receiver) dispatch slot.
	src := `interface A {
  fn foo(x: self): Int
}

struct T {
  n: Int
}

impl A for T {
  fn foo(_x: T): Int {
    1
  }

  fn foo(_x: T): Int {
    2
  }
}

`
	expectErrContaining(t, checkUniversalDebugProject(t, src), "duplicate impl")
}

func TestHeaderProject_TwoModuleFunctionsCollide(t *testing.T) {
	src := `struct T {
  n: Int
}

pub fn foo(_x: T): Int { 1 }

pub fn foo(_x: T): Int { 2 }
`
	expectErrContaining(t, checkUniversalDebugProject(t, src), "'foo' is already defined")
}
