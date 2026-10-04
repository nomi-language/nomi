package irbuild

import (
	"strings"
	"testing"
)

// TestPreludeInstanceSlotsAreNeverBoxed pins the invariant tryGuard depends on
// and cannot check at its own call site.
//
// A lambda's guard is BUILT after the lambda's whole body has been lowered, so
// anything that writes a statement while building it would land its statement
// at the end of the body. variantValue writes one for a BOXED payload — and it
// cannot reach that branch for a prelude instance, because preludeInstance
// assigns slots directly instead of going through assignSlots, which is the
// only place an enum slot is ever marked boxed. This asserts that rather than
// leaving it as a reading of the code.
func TestPreludeInstanceSlotsAreNeverBoxed(t *testing.T) {
	// A struct reached through a prelude enum, and reached from that struct
	// again, which is the shape that boxes anything at all.
	const src = "struct Node {\n  next: Maybe<Node>\n}\n\n" +
		"fn head(): Maybe<Node> {\n  None\n}\n\nfn main() {\n  _ = head()\n}\n"
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	g := newGen(p.Entry(), "nomimod0", nil, nil, -1, nil)
	g.declareTypes()
	g.declareFuncs()
	if len(g.preludeOrder) == 0 {
		t.Fatal("no prelude instance was created, so this asserts nothing")
	}
	for _, d := range g.preludeOrder {
		for i, s := range d.slots {
			if s.boxed {
				t.Errorf("prelude instance %s slot %d is boxed; tryGuard builds a lambda's "+
					"guard after the body is lowered and variantValue would write a statement "+
					"for a boxed payload, which would land at the wrong position",
					d.nomi, i)
			}
		}
	}
}

// TestTry_TheBoundaryMismatchesAreFrontEndERRORSNow asserts that each `try`
// whose propagated value its boundary cannot hold is rejected by the FRONT
// END, naming both types. The checker implements spec §9's boundary rule
// (analysis.checker.checkTryBoundary and checkInferredBoundaryFlavour), so
// `AnalyzeSource` refuses these before the builder is reached.
//
// The builder keeps its `try boundary mismatch` refusal as a FENCE, as try.go's
// header does for `try without an operand` and `try boundary disagreement`:
// refusals that nothing reachable can trigger, kept because a front end that
// changes under this file must not silently lower an unsound program. This test
// is the assertion that fails if the checker regresses. The seven sources are
// kept verbatim rather than collapsed, because they are seven distinct shapes,
// including a bare pipe stage, a lambda, and shapes inside a test FILE.
//
// Not restated as a row: `try inside a concurrent block` is a DIFFERENT
// boundary from a test body. A `concurrent` block establishes its own
// try-boundary, so the block's value becomes the Err and the enclosing test
// keeps running, while `g.inTest` is true for both. Reading `g.inTest` as "the
// boundary is the test" would END those cases. That is why the classification
// is the checker's record; see try.go.
func TestTry_TheBoundaryMismatchesAreFrontEndERRORSNow(t *testing.T) {
	cases := []struct {
		name string
		want string
		src  string
	}{
		// `Maybe` propagated into a `Result` boundary. Admitted, it returned
		// a `None` from a function declared `Result`, and the caller's `case`
		// then faulted with "no matching case branch".
		{"maybe into a result fn",
			"`try` on a Maybe cannot propagate out of a function returning Result<Int, String>",
			"fn find(): Maybe<Int> {\n  None\n}\n\n" +
				"fn f(): Result<Int, String> {\n  v = try find()\n\n  Ok(v)\n}\n"},
		// Same prelude enum, different error type: `Result<Int, Int>`
		// propagated into `Result<Int, String>`.
		{"error type laundered",
			"try error type mismatch: expected String, got Int",
			"fn r(): Result<Int, Int> {\n  Err(7)\n}\n\n" +
				"fn f(): Result<Int, String> {\n  v = try r()\n\n  Ok(v)\n}\n"},
		// A boundary that is not a prelude enum at all.
		{"boundary is not a result or maybe",
			"`try` cannot propagate a Result out of a function returning Int",
			"fn r(): Result<Int, Int> {\n  Err(7)\n}\n\n" +
				"fn f(): Int {\n  v = try r()\n\n  v\n}\n"},
		// The PIPE spelling. The parser desugars every keyword-prefixed stage,
		// so this is the prefix
		// shape with the piped value as its operand.
		{"pipe spelling",
			"`try` on a Maybe cannot propagate out of a function returning Result<Int, String>",
			"fn f(): Result<Int, String> {\n  n = Some(1) |> try\n\n  Ok(n)\n}\n"},
		// A LAMBDA whose inferred result cannot hold the propagated value. This
		// one is the INFERRING boundary and it has its own checker path: a
		// lambda declares no result, so the rule is applied to the result the
		// body produced. That is why the checker has two entry points onto one
		// rule.
		{"lambda result cannot hold it",
			"`try` cannot propagate a Result out of a lambda whose result is Int",
			"fn parse(n: Int): Result<Int, String> {\n  Ok(n)\n}\n\n" +
				"fn main() {\n  _ = |x: Int| {\n    v = try parse(x)\n\n    v\n  }\n}\n"},
		// Two shapes inside a test FILE. Neither is a test-body `try`: a test
		// body declares no result, so it has no boundary to mismatch.
		{"maybe into a result fn, in a test file",
			"`try` on a Maybe cannot propagate out of a function returning Result<Int, String>",
			"fn find(): Maybe<Int> {\n  None\n}\n\n" +
				"fn f(): Result<Int, String> {\n  v = try find()\n\n  Ok(v)\n}\n\n" +
				"test \"calls it\" {\n  assert f() == Ok(1)\n}\n"},
		{"lambda result cannot hold it, in a test body",
			"`try` cannot propagate a Result out of a lambda whose result is Int",
			"fn parse(n: Int): Result<Int, String> {\n  Ok(n)\n}\n\n" +
				"test \"lambda result cannot hold it\" {\n  f = |x: Int| {\n    v = try parse(x)\n\n    v\n  }\n\n  assert f(1) == 1\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AnalyzeSource("main", tc.src)
			if err == nil {
				t.Fatalf("the front end ACCEPTED an unsound `try`; the builder's %q "+
					"fence is all that stands "+
					"between it and a lowered program:\n%s", "try boundary mismatch", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("front end rejected it for the wrong reason\n  want substring: %s\n  got: %v",
					tc.want, err)
			}
		})
	}
}
