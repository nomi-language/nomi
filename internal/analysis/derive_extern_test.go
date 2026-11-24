package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Tests for `@derive` on host type declarations (derive-as-assertion,
// spec §12 *Host types*): the decorator asserts the type has trivial
// structure (a zero-sized singleton), and the synthesizers emit constant
// bodies (`Equal`, the bare type name) rather than recursing into
// structure the declaration doesn't carry. Equatable, Hashable, Debug and
// Display write no function.

const externDeriveSrc = `pub host type Tok
derive Equatable for Tok
derive Comparable for Tok
derive Hashable for Tok`

// TestDeriveExternTypeEquatableWritesNoFunction: a derived Equatable on a
// host type writes no function, as on any other type: two values compare
// as the machine compares the host's handles (rt.Equal), which for a type
// of one inhabitant is always equal.
func TestDeriveExternTypeEquatableWritesNoFunction(t *testing.T) {
	if fn := synthMethodForSource(t, externDeriveSrc, "equal", "Equatable", "Tok"); fn != nil {
		t.Errorf("derive Equatable for host type Tok writes `fn equal`, want no function")
	}
}

// TestDeriveExternTypeHashableWritesNoFunction: a derived Hashable on a
// host type writes no function, as on any other type: a value hashes as
// the machine hashes the host's handle (rt.Hash).
func TestDeriveExternTypeHashableWritesNoFunction(t *testing.T) {
	if fn := synthMethodForSource(t, externDeriveSrc, "hash", "Hashable", "Tok"); fn != nil {
		t.Errorf("derive Hashable for host type Tok writes `fn hash`, want no function")
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

// TestDeriveExternTypeDebugAndDisplayAreNameOnly pins a host type's
// derived Debug and Display: each writes no function, and rt renders a
// host value with no body by its bare type name.
func TestDeriveExternTypeDebugAndDisplayAreNameOnly(t *testing.T) {
	src := `pub host type Tok
derive Debug for Tok
derive Display for Tok`
	for method, iface := range map[string]string{"inspect": "Debug", "to_string": "Display"} {
		if fn := synthMethodForSource(t, src, method, iface, "Tok"); fn != nil {
			t.Errorf("derive %s for host type Tok writes `fn %s`, want no function", iface, method)
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
	nodes = SynthesizeUniversalDebug(nodes)
	if got := countSynthDebugImpls(nodes, "Tok"); got != 1 {
		t.Errorf("expected exactly 1 synthesized Debug impl for Tok, got %d", got)
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
		t.Fatal("expected an error for derive Widget on host type")
	}
}
