package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `try` — Nomi's error propagation over the two prelude enums.
//
// # What `try` does, which is not "return early on Err"
//
// `try` evaluates the operand, unwraps `Ok(v)` / `Some(v)` to `v`, and for
// `Err(e)` / `None` returns the WHOLE variant value from the innermost
// ACTIVATION — the same exit `return` and a failing `assert` take. A lambda is
// an activation, so `try` unwinds to the enclosing LAMBDA and not to the
// enclosing `fn`. Nomi has no non-local return, so there is no third
// possibility.
//
// The boundary is decided by where the lowered return lands, which is decided
// by lexical nesting, the same argument returnStmt makes for `return`:
//
//	f = |x: Int| { v = try parse(x); Ok(v + 1) }
//	inner = f(n)          // Err(_) here, not an unwind of the caller
//	io.print("after lambda call")   // prints
//
// testdata/try_boundary.nomi is that program, and it is the fixture whose text
// distinguishes a lambda boundary from a function one: get it wrong and
// "after the lambda" stops printing.
//
// # The type of the propagated value
//
// `try` propagates the operand's own `Err(e)` value, so the propagated value
// must have the boundary's return type:
//
//	fn a(): Result<Int, String> { v = try m(); Ok(v) }   // m(): Maybe<Int>
//	fn b(): Int                 { v = try r(); v }       // r(): Result<Int, Int>
//	_ = |x: Int| { v = try parse(x); v }                 // lambda result Int
//
// `analysis.checkTryBoundary` rejects all three at the `try`, naming both
// types, and spec §9 states the rule as three clauses. This arm is a FENCE:
// if any of the three ever reaches here, refusing by name is still better than
// lowering a graph that is right by coincidence. The synthetic rows for it
// assert the FRONT END rejects each, as for `try without an operand` and
// `try boundary disagreement`.
//
// The boundary a `fn` propagates into is its declared result, known before the
// body is lowered. A LAMBDA declares nothing: its result is discovered from the
// first `return` or the body's tail (resultInference), and a `try` is usually
// the first statement in the body. So the guard is written as a placeholder
// and filled in once the lambda has settled — patchPoint/patch, which the
// lambda's own signature line also uses. The placeholder is deliberately not
// valid (see unpatchedTryGuard): an unresolved guard must be an error, never a
// value that type-checks and answers wrongly.
//
// # Four boundaries, and the classification is the CHECKER's
//
// A `try` can sit in exactly four boundaries, and the checker already names
// which: it maintains `tryBoundary` for its own diagnostics and stamps the
// answer on the `try` keyword's hover symbol — `"fn <name>"`, `"lambda"`,
// `"concurrent"`, `"test \"<name>\""`. This file reads that back rather than
// re-deriving it (inferred.go's doctrine, applied to a fact the front end
// already settled), because the builder's own flags get one of the four wrong:
// `g.inTest` is true for a `try` inside a `concurrent` block inside a test
// body, and that `try` unwinds the BLOCK, not the test. The test-body arm
// returns out of the lowered test function, so reading `g.inTest` as "the
// boundary is the test" would end the case at a `try` that only unwinds the
// block. See tryBoundaryRefusal.
//
// The two views are cross-checked, and on both bits rather than one: the
// `lambda` bit against `g.inferResult != nil`, and the `test` bit against
// `g.inTest`. A disagreement is refused at the `try`'s own position, so a drift
// between them can never be a wrong answer.
//
// Three of the four lower; one is refused:
//
//   - A `fn`'s declared result and a LAMBDA's inferred one both take the
//     ordinary guard, which propagates a value — see tryGuard.
//
//   - A TEST body takes a DIFFERENT guard, because what it propagates is not a
//     value at all. The case FAILS, and `nomi test` reports
//
//     FAIL <file> :: try propagates in a test body
//     line 23: test returned early
//     try parse(-1)
//     returned:
//     Err("negative -1")
//
//     — an rt.EarlyReturnFailure, carrying the `try`'s own line, the
//     expression as the FORMATTER renders it, and the propagated variant as
//     Debug renders it. No boundary type is involved: a test body
//     declares no result (gen.testDecl sets g.result to kindInvalid), so
//     `try boundary mismatch` cannot arise here and tryGuard is not reached.
//     See tryTestGuard, and rt.TestFailure for the failure shape.
//
//   - A `concurrent` block. It is refused wholesale elsewhere, so this arm
//     only ever fires while probing a subtree that is already lost; it exists
//     so the report says WHY instead of blaming the enclosing test.
//
// # The pipe spelling needs no separate lowering, but it does need its own TEXT
//
// Both pipe spellings arrive as one shape: the PARSER desugars a
// keyword-prefixed stage, so `x |> try f()` is `(x |> f()) |> try` (parser.go's
// parsePipeValueKeywordStage), and `x |> try` is already that shape. Every
// `try` in pipe position is therefore a `TryOp` with a nil `Expr` whose operand
// is the pipe's LEFT side, and the only thing pipe.go has to do is say so.
//
// The spellings differ in their rendered text. The pipe spelling renders the
// whole `*ast.Binary` into its EarlyReturnFailure (`-1 |> try parse()`) where
// the prefix spelling renders the `*ast.TryOp` (`try parse(-1)`), and a test
// body's report reads that text. So tryOp takes the node to RENDER separately
// from the node that carries the position: pipe.go passes the binary,
// native.go passes the TryOp. The formatter re-sugars, so a desugared
// `(-1 |> parse()) |> try` renders back as `-1 |> try parse()`;
// TestPinned_TryInTest is the pin, and it is a pin on literal text rather than
// on a comparison, because a comparison of two silences passes.

// recordedTryBoundary is the boundary the checker stamped on this `try`.
//
// fa.References keyed by the `try` keyword's own position, which is where both
// spellings register: checkTryOp for `try E`, and checkBinary's pipe branch for
// `L |> try`.
func (g *gen) recordedTryBoundary(t *ast.TryOp) (string, bool) {
	if g.fa == nil {
		return "", false
	}
	sym, found := g.fa.References[analysis.Pos{Line: t.Line, Col: t.Col}]
	if !found || sym == nil || sym.Kind != analysis.SymbolTryOp || sym.TryOp == nil {
		return "", false
	}
	return sym.TryOp.Boundary, true
}

// tryBoundaryFits is shared by native guard delivery and retained admission.
// Anchors belong to individual files; specs identify the prelude declaration.
func tryBoundaryFits(boundary kind, operand *typeDef, carried []kind) bool {
	bd := boundary.def
	if bd == nil || bd.preludeOf == nil || operand == nil || operand.preludeOf == nil ||
		bd.preludeOf.spec != operand.preludeOf.spec || len(bd.variants) == 0 || len(bd.variants) != len(operand.variants) {
		return false
	}
	u := &bd.variants[len(bd.variants)-1]
	if len(u.payloads) != len(carried) {
		return false
	}
	for i := range carried {
		if u.payloads[i].k != carried[i] {
			return false
		}
	}
	return true
}
