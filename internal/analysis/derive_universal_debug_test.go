package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// debugImplForType scans a node slice for the synthesized `impl Debug for T {
// fn inspect(value: T) }` block whose receiver base type name is typeName,
// returning its inspect FuncDef.
// Returns nil when none is present. Unlike synthDebugForSource it operates on
// an already-synthesized slice (so callers can run SynthesizeUniversalDebug
// then assert over its output). Routes through the shared block-aware matcher.
func debugImplForType(nodes []ast.Node, typeName string) *ast.FuncDef {
	for _, n := range nodes {
		if fn := synthMethodFromNode(n, "inspect", "Debug", typeName); fn != nil {
			return fn
		}
	}
	return nil
}

// countDebugImpls returns how many synthesized Debug impl blocks for typeName
// appear in nodes — used to assert no duplicate is emitted.
func countDebugImpls(nodes []ast.Node, typeName string) int {
	n := 0
	for _, node := range nodes {
		if synthMethodFromNode(node, "inspect", "Debug", typeName) != nil {
			n++
		}
	}
	return n
}

func parseNodes(t *testing.T, src string) []ast.Node {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return nodes
}

// debugBodyIsNameOnly reports whether the synthesized Debug fn's body is a
// single static `"<opaque TypeName>"` StringLit (no interpolation). That's
// the opaque auto-default shape. Returns the StringLit value when matched.
func debugBodyIsNameOnly(fn *ast.FuncDef) (string, bool) {
	if fn == nil || fn.Body == nil || len(fn.Body.Stmts) != 1 {
		return "", false
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		return "", false
	}
	sl, ok := es.Expr.(*ast.StringLit)
	if !ok {
		return "", false
	}
	return sl.Value, true
}

// TestSynthesizeAutoDebug_NonOpaqueIsStructural pins that synthesizeAutoDebug
// delegates to the structural synthesizer for non-opaque types — its output
// is byte-identical to an explicit @derive Debug.
func TestSynthesizeAutoDebug_NonOpaqueIsStructural(t *testing.T) {
	nodes := parseNodes(t, `struct Point { x: Int; y: Int }`)
	var structDef ast.Node
	for _, n := range nodes {
		if _, ok := n.(*ast.StructDef); ok {
			structDef = n
		}
	}
	if structDef == nil {
		t.Fatal("no struct decl parsed")
	}
	out := synthesizeAutoDebug(structDef, synthSiteOf(0, structDef))
	fn := debugImplForType(out, "Point")
	if fn == nil {
		t.Fatal("synthesizeAutoDebug produced no Debug impl for Point")
	}
	// Structural: body is a StringInterp (not a bare StringLit).
	if fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %v", fn.Body)
	}
	es := fn.Body.Stmts[0].(*ast.ExprStmt)
	if _, ok := es.Expr.(*ast.StringInterp); !ok {
		t.Fatalf("expected structural StringInterp body, got %T", es.Expr)
	}
}

// TestSynthesizeAutoDebug_OpaqueIsNameOnly pins the opaque rule: the auto
// path emits a name-only `"<opaque TypeName>"` body, NOT structural.
func TestSynthesizeAutoDebug_OpaqueIsNameOnly(t *testing.T) {
	nodes := parseNodes(t, `opaque type Baz Int`)
	var td ast.Node
	for _, n := range nodes {
		if _, ok := n.(*ast.TypeDef); ok {
			td = n
		}
	}
	if td == nil {
		t.Fatal("no type decl parsed")
	}
	if !td.(*ast.TypeDef).Opaque {
		t.Fatal("parser did not mark Baz opaque")
	}
	out := synthesizeAutoDebug(td, synthSiteOf(0, td))
	fn := debugImplForType(out, "Baz")
	if fn == nil {
		t.Fatal("synthesizeAutoDebug produced no Debug impl for opaque Baz")
	}
	val, ok := debugBodyIsNameOnly(fn)
	if !ok {
		t.Fatalf("expected name-only StringLit body for opaque Baz, got %v", fn.Body)
	}
	if val != "<opaque Baz>" {
		t.Errorf("opaque body: got %q, want %q", val, "<opaque Baz>")
	}
}

// TestSynthesizeAutoDebug_ExternIsBareName pins the extern-type rule: a
// declared `host type` gets a name-only body rendering the BARE type name
// (no `<...>` marker) — the rendering a zero-sized extern singleton flowing
// out of an `embeds` pattern binding must produce (`True`, not
// `<extern True>`), matching how zero-sized distincts render.
func TestSynthesizeAutoDebug_ExternIsBareName(t *testing.T) {
	nodes := parseNodes(t, `pub host type True`)
	var et ast.Node
	for _, n := range nodes {
		if _, ok := n.(*ast.ExternType); ok {
			et = n
		}
	}
	if et == nil {
		t.Fatal("no host type decl parsed")
	}
	out := synthesizeAutoDebug(et, synthSiteOf(0, et))
	fn := debugImplForType(out, "True")
	if fn == nil {
		t.Fatal("synthesizeAutoDebug produced no Debug impl for host type True")
	}
	if !fn.AutoSynth {
		t.Error("extern auto Debug impl not tagged AutoSynth")
	}
	val, ok := debugBodyIsNameOnly(fn)
	if !ok {
		t.Fatalf("expected name-only StringLit body for extern True, got %v", fn.Body)
	}
	if val != "True" {
		t.Errorf("extern body: got %q, want bare %q", val, "True")
	}
}

// TestSynthesizeUniversalDebug_CoversExternTypes pins the pass over extern
// declarations: an host type lacking explicit Debug gets exactly one
// bare-name auto impl; one whose (already-lowered) `impl Debug` block is
// present is skipped; a generic host type carries its type params through.
func TestSynthesizeUniversalDebug_CoversExternTypes(t *testing.T) {
	src := `
pub host type True

pub host type Task<T>

pub host type Dynamic

impl Debug for Dynamic {
  fn inspect(_value: Dynamic): String { "custom" }
}
`
	nodes := parseNodes(t, src)
	nodes, _ = LowerDerives(nodes)
	nodes, _ = SynthesizeDerives(nodes)
	out := SynthesizeUniversalDebug(nodes)

	for _, ty := range []string{"True", "Task"} {
		if got := countDebugImpls(out, ty); got != 1 {
			t.Errorf("%s: expected exactly 1 Debug impl, got %d", ty, got)
		}
	}
	trueFn := debugImplForType(out, "True")
	if val, ok := debugBodyIsNameOnly(trueFn); !ok || val != "True" {
		t.Errorf("True: expected bare-name body %q, got ok=%v val=%q", "True", ok, val)
	}
	taskFn := debugImplForType(out, "Task")
	if val, ok := debugBodyIsNameOnly(taskFn); !ok || val != "Task" {
		t.Errorf("Task: expected bare-name body %q, got ok=%v val=%q", "Task", ok, val)
	}
	if len(taskFn.TypeParams) != 1 || taskFn.TypeParams[0].Name != "T" {
		t.Errorf("Task: expected type param T carried onto the impl, got %v", taskFn.TypeParams)
	}
	// Dynamic has an explicit Debug impl block — no auto impl added.
	if got := countDebugImpls(out, "Dynamic"); got != 1 {
		t.Errorf("Dynamic: explicit impl present — expected exactly 1, got %d", got)
	}
	dynFn := debugImplForType(out, "Dynamic")
	if val, ok := debugBodyIsNameOnly(dynFn); !ok || val != "custom" {
		t.Errorf("Dynamic: expected the explicit body, got ok=%v val=%q", ok, val)
	}
	// Idempotent over the extern impls too.
	twice := SynthesizeUniversalDebug(out)
	for _, ty := range []string{"True", "Task", "Dynamic"} {
		if got := countDebugImpls(twice, ty); got != 1 {
			t.Errorf("%s: second pass not idempotent — expected 1 impl, got %d", ty, got)
		}
	}
}

// TestSynthesizeUniversalDebug_CoversDeclaredTypes pins the pass: it appends
// an auto Debug impl for each declared type lacking an explicit one (struct,
// enum, opaque distinct), with the opaque one rendered name-only. It must NOT
// add one for a type that already has an explicit Debug impl block, nor
// duplicate one for a type @derive Debug already covered.
func TestSynthesizeUniversalDebug_CoversDeclaredTypes(t *testing.T) {
	src := `
struct Foo { a: Int }

enum Bar { Red; Green }

opaque type Baz Int

struct Qux { q: Int }

impl Debug for Qux {
  fn inspect(_value: Qux): String { "custom" }
}
`
	nodes := parseNodes(t, src)
	// Mirror the production pipeline: LowerDerives (turns the
	// `impl Debug for Qux` reopening block into a top-level impl Debug
	// block), SynthesizeDerives
	// (no-op here, no derive present), then SynthesizeUniversalDebug.
	nodes, _ = LowerDerives(nodes)
	nodes, _ = SynthesizeDerives(nodes)
	out := SynthesizeUniversalDebug(nodes)

	// Foo, Bar, Baz each get exactly one auto Debug impl.
	for _, ty := range []string{"Foo", "Bar", "Baz"} {
		if got := countDebugImpls(out, ty); got != 1 {
			t.Errorf("%s: expected exactly 1 Debug impl, got %d", ty, got)
		}
	}
	// Baz is opaque → name-only.
	bazFn := debugImplForType(out, "Baz")
	if val, ok := debugBodyIsNameOnly(bazFn); !ok || val != "<opaque Baz>" {
		t.Errorf("Baz: expected name-only `<opaque Baz>`, got ok=%v val=%q", ok, val)
	}
	// Foo (non-opaque struct) → structural StringInterp.
	fooFn := debugImplForType(out, "Foo")
	if fooFn == nil {
		t.Fatal("no Debug impl for Foo")
	}
	es := fooFn.Body.Stmts[0].(*ast.ExprStmt)
	if _, ok := es.Expr.(*ast.StringInterp); !ok {
		t.Errorf("Foo: expected structural StringInterp body, got %T", es.Expr)
	}
	// Qux has an explicit Debug impl block → the pass must NOT add an auto one.
	// Only the explicit (non-synth-band) impl should be present, so the
	// auto-synth count of synth-band impls is zero. Total inspect-for-Qux
	// stays at exactly 1 (the user's).
	if got := countDebugImpls(out, "Qux"); got != 1 {
		t.Errorf("Qux: explicit impl block present — expected exactly 1 (the user's), got %d", got)
	}
	// And it must remain the user's custom body (a bare StringLit "custom"),
	// not replaced by a synthesized structural one.
	quxFn := debugImplForType(out, "Qux")
	if quxFn == nil {
		t.Fatal("no Debug impl for Qux")
	}
	if val, ok := debugBodyIsNameOnly(quxFn); !ok || val != "custom" {
		t.Errorf("Qux: expected the user's custom body, got ok=%v val=%q", ok, val)
	}
}

// TestSynthesizeUniversalDebug_ExplicitDeriveSuppressesAuto pins precedence:
// a type with an explicit `@derive Debug` keeps its STRUCTURAL derived body
// — the universal pass must skip it (must not add a second, name-only one for
// opaque types). This guards `NonZeroInt(5)` not regressing to
// `<opaque NonZeroInt>`.
func TestSynthesizeUniversalDebug_ExplicitDeriveSuppressesAuto(t *testing.T) {
	src := `
opaque type NonZeroInt Int
derive Debug for NonZeroInt
`
	nodes := parseNodes(t, src)
	nodes, _ = LowerDerives(nodes)      // stamps the synthetic @derive Debug decorator
	nodes, _ = SynthesizeDerives(nodes) // produces the structural derived impl
	out := SynthesizeUniversalDebug(nodes)

	if got := countDebugImpls(out, "NonZeroInt"); got != 1 {
		t.Fatalf("NonZeroInt: explicit @derive Debug — expected exactly 1 impl, got %d", got)
	}
	fn := debugImplForType(out, "NonZeroInt")
	// The derived structural body for a distinct type unwraps + interpolates,
	// so it is NOT a bare name-only StringLit "<opaque NonZeroInt>".
	if val, ok := debugBodyIsNameOnly(fn); ok && val == "<opaque NonZeroInt>" {
		t.Error("NonZeroInt: explicit @derive Debug regressed to name-only `<opaque NonZeroInt>`")
	}
}

// TestSynthesizeUniversalDebug_ExplicitImplSuppressesAuto pins precedence for
// the explicit impl-block form (the highest-priority override).
func TestSynthesizeUniversalDebug_ExplicitImplSuppressesAuto(t *testing.T) {
	src := `
struct Custom { a: Int }

impl Debug for Custom {
  fn inspect(_value: Custom): String { "hand-written" }
}
`
	nodes := parseNodes(t, src)
	nodes, _ = LowerDerives(nodes)
	nodes, _ = SynthesizeDerives(nodes)
	out := SynthesizeUniversalDebug(nodes)

	if got := countDebugImpls(out, "Custom"); got != 1 {
		t.Fatalf("Custom: explicit impl Debug block — expected exactly 1 impl, got %d", got)
	}
	fn := debugImplForType(out, "Custom")
	if val, ok := debugBodyIsNameOnly(fn); !ok || val != "hand-written" {
		t.Errorf("Custom: expected the hand-written body, got ok=%v val=%q", ok, val)
	}
}

// TestUniversalDebug_ImportIndependent_BareBuild pins the invariant that
// universal-Debug synthesis is import-independent. A file with NO imports at
// all, built through the bare BuildFile path (no stdlib, no prelude — the
// name `Debug` resolves nowhere in scope), still gets a fully-resolving,
// dispatch-recorded auto Debug impl for every declared type: synthesis runs
// unconditionally, and the synthesized `impl Debug` header resolves via the
// compiler-known route in defineImplBlockAnnotations rather than file scope.
// Debug is compiler-known machinery (universal conformance, missing-impl
// exemption, eager registration), so no `impl block: undefined interface
// 'Debug'` — or any other — diagnostic may appear, and each (T, Debug) pair
// must land in fa.Impls exactly as it does in an import-rich file. This is
// also what lets stdlib modules (which get no prelude) declare types without
// carrying a `std/debug.{Debug}` import.
func TestUniversalDebug_ImportIndependent_BareBuild(t *testing.T) {
	src := `struct Point { x: Int; y: Int }

enum Color { Red; Green }

pub host type Handle

opaque type Token Int`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	for _, ty := range []string{"Point", "Color", "Handle", "Token"} {
		if !fa.Impls[ty]["Debug"] {
			t.Errorf("%s: auto Debug impl not recorded in fa.Impls (got %v)", ty, fa.Impls[ty])
		}
	}
	// A HAND-WRITTEN non-Debug interface header still requires the name in
	// scope — the compiler-known header route covers Debug (any block) and
	// the derivable protocols on synthesized blocks only (see
	// derive_scope_test.go).
	_, errs = checkSource(`struct P { x: Int }

impl Display for P {
  fn to_string(_value: P): String { "p" }
}`)
	expectError(t, errs, "undefined interface 'Display'")
}

// TestSynthesizeUniversalDebug_Idempotent pins idempotency directly: a second
// pass over already-synthesized nodes adds nothing (the auto Debug impl it
// emitted is seen as explicit-enough by the pass's own scan and skipped). The
// production pipeline relies on this — BuildProjectWithCache and
// internal/frontend's Checker.Prepare both run the pass over overlapping
// slices.
func TestSynthesizeUniversalDebug_Idempotent(t *testing.T) {
	src := `
struct Point { x: Int; y: Int }

enum Shape { Circle Float; Nothing }
`
	nodes := parseNodes(t, src)
	nodes, _ = LowerDerives(nodes)
	nodes, _ = SynthesizeDerives(nodes)
	once := SynthesizeUniversalDebug(nodes)
	twice := SynthesizeUniversalDebug(once)

	for _, ty := range []string{"Point", "Shape"} {
		if got := countDebugImpls(once, ty); got != 1 {
			t.Fatalf("%s: after one pass expected 1 impl, got %d", ty, got)
		}
		if got := countDebugImpls(twice, ty); got != 1 {
			t.Errorf("%s: second pass not idempotent — expected 1 impl, got %d", ty, got)
		}
	}
	if len(twice) != len(once) {
		t.Errorf("second pass changed node count: once=%d twice=%d", len(once), len(twice))
	}
}
