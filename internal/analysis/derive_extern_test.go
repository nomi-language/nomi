package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Tests for `@derive` on host type declarations (derive-as-assertion,
// spec §38.1 "Extern types"): the decorator asserts the type has trivial
// structure (a zero-sized singleton), and the synthesizers emit constant
// bodies — `True` / `0` / `Equal` / the bare type name — rather than
// recursing into structure the declaration doesn't carry.

const externDeriveSrc = `pub host type Tok
derive Equatable for Tok
derive Comparable for Tok
derive Hashable for Tok`

// TestDeriveExternTypeEquatableIsTrue pins the Equatable body: a single
// ExprStmt holding the bare `True` ident — the same constant body a
// zero-sized distinct derives.
func TestDeriveExternTypeEquatableIsTrue(t *testing.T) {
	fn := synthMethodForSource(t, externDeriveSrc, "equal?", "Equatable", "Tok")
	if fn == nil {
		t.Fatal("no synthesized Equatable impl (fn equals) for host type Tok")
	}
	if len(fn.Params) != 2 {
		t.Errorf("expected 2 params, got %d", len(fn.Params))
	}
	if fn.AutoSynth {
		t.Error("explicit @derives must not be marked AutoSynth")
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	ti, ok := es.Expr.(*ast.TypeIdent)
	if !ok || ti.Name != "True" {
		t.Errorf("expected body == TypeIdent(True), got %T %v", es.Expr, es.Expr)
	}
}

// TestDeriveExternTypeHashableIsZero pins the Hashable body: the constant
// `0` — all values of a trivial-structure type are equal, so they share one
// hash bucket and the equals/hash law holds.
func TestDeriveExternTypeHashableIsZero(t *testing.T) {
	fn := synthMethodForSource(t, externDeriveSrc, "hash", "Hashable", "Tok")
	if fn == nil {
		t.Fatal("no synthesized Hashable impl (fn hash) for host type Tok")
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	il, ok := es.Expr.(*ast.IntLit)
	if !ok || il.Value != 0 {
		t.Errorf("expected body == IntLit(0), got %T %v", es.Expr, es.Expr)
	}
}

// TestDeriveExternTypeComparableIsEqual pins the Comparable body: the bare
// `Equal` Ordering variant — with one inhabitant every comparison is a
// self-comparison.
func TestDeriveExternTypeComparableIsEqual(t *testing.T) {
	fn := synthMethodForSource(t, externDeriveSrc, "compare", "Comparable", "Tok")
	if fn == nil {
		t.Fatal("no synthesized Comparable impl (fn compare) for host type Tok")
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	assertOrderingValue(t, es.Expr, "Equal")
}

// TestDeriveExternTypeDebugAndDisplayAreNameOnly pins the stringify pair:
// both bodies are the static bare type name — the declaration carries no
// payload shape, matching the universal-Debug extern default's rendering.
func TestDeriveExternTypeDebugAndDisplayAreNameOnly(t *testing.T) {
	src := `pub host type Tok
derive Debug for Tok
derive Display for Tok`
	for method, iface := range map[string]string{"inspect": "Debug", "to_string": "Display"} {
		fn := synthMethodForSource(t, src, method, iface, "Tok")
		if fn == nil {
			t.Fatalf("no synthesized %s impl (fn %s) for host type Tok", iface, method)
		}
		if len(fn.Body.Stmts) != 1 {
			t.Fatalf("%s: expected 1-stmt body, got %d", iface, len(fn.Body.Stmts))
		}
		es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
		if !ok {
			t.Fatalf("%s: expected ExprStmt, got %T", iface, fn.Body.Stmts[0])
		}
		sl, ok := es.Expr.(*ast.StringLit)
		if !ok || sl.Value != "Tok" {
			t.Errorf("%s: expected body == StringLit(\"Tok\"), got %T %v", iface, es.Expr, es.Expr)
		}
	}
}

// TestDeriveDebugOnExternSuppressesUniversalDebug — an explicit
// `@derive Debug` on an host type must suppress the universal-Debug auto
// default for that type; otherwise the two synthesized blocks collide on
// the single (Debug, Tok) dispatch slot.
func TestDeriveDebugOnExternSuppressesUniversalDebug(t *testing.T) {
	src := `pub host type Tok
derive Debug for Tok`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	nodes, _ = LowerDerives(nodes)
	nodes, errs := SynthesizeDerives(nodes)
	if len(errs) > 0 {
		t.Fatalf("synth errors: %v", errs)
	}
	nodes = SynthesizeUniversalDebug(nodes)
	if got := countSynthDebugImpls(nodes, "Tok"); got != 1 {
		t.Errorf("expected exactly 1 synthesized Debug impl for Tok, got %d", got)
	}
}

// TestDeriveExternTypeIsIdempotent — the runtime-then-analyzer call shape
// runs SynthesizeDerives twice over overlapping slices; the second call
// must not re-emit the host type's impls.
func TestDeriveExternTypeIsIdempotent(t *testing.T) {
	nodes, err := parser.Parse(lexer.Lex(externDeriveSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	nodes, _ = LowerDerives(nodes)
	once, errs := SynthesizeDerives(nodes)
	if len(errs) > 0 {
		t.Fatalf("first synth: unexpected errors %v", errs)
	}
	twice, errs := SynthesizeDerives(once)
	if len(errs) > 0 {
		t.Fatalf("second synth: unexpected errors %v", errs)
	}
	if len(twice) != len(once) {
		t.Errorf("idempotency violated: first synth len=%d, second synth len=%d", len(once), len(twice))
	}
}

// TestDeriveExternTypeUnknownProtocolStillErrors — the per-arg validation
// path (unknown protocol, duplicates) runs for host types exactly as for
// the other type-decl kinds.
func TestDeriveExternTypeUnknownProtocolStillErrors(t *testing.T) {
	src := `pub host type Tok
derive Widget for Tok`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// `derive Widget` for an unknown derivable protocol is rejected by
	// LowerDerives (the home of the derive-validation checks) before any
	// decorator is stamped — so the diagnostic surfaces in its error return.
	_, errs := LowerDerives(nodes)
	if len(errs) == 0 {
		t.Fatal("expected 'unknown derivable protocol' error for derive Widget on host type")
	}
}
