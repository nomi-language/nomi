package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `?` — Nomi's error propagation over the two prelude enums.
//
// # What `?` does, which is not "return early on Err"
//
// `?` evaluates the operand, unwraps `Ok(v)` / `Some(v)` to `v`, and for
// `Err(e)` / `None` returns the WHOLE variant value from the innermost
// ACTIVATION — the same exit `return` and a failing `assert` take. A lambda is
// an activation, so `?` unwinds to the enclosing LAMBDA and not to the
// enclosing `fn`. Nomi has no non-local return, so there is no third
// possibility.
//
// The boundary is decided by where the lowered return lands, which is decided
// by lexical nesting, the same argument returnStmt makes for `return`:
//
//	f = fn x: Int -> { v = parse(x)?; Ok(v + 1) }
//	inner = f(n)          // Err(_) here, not an unwind of the caller
//	io.print("after lambda call")   // prints
//
// testdata/try_boundary.nomi is that program, and it is the fixture whose text
// distinguishes a lambda boundary from a function one: get it wrong and
// "after the lambda" stops printing.
//
// # The type of the propagated value
//
// `?` propagates the operand's own `Err(e)` value, so the propagated value
// must have the boundary's return type:
//
//	fn a() -> Result<Int, String> { v = m()?; Ok(v) }   // m(): Maybe<Int>
//	fn b() -> Int                 { v = r()?; v }       // r(): Result<Int, Int>
//	_ = fn x: Int -> { v = parse(x)?; v }                 // lambda result Int
//
// `analysis.checkTryBoundary` rejects all three at the `?`, naming both
// types, and spec §9 states the rule as three clauses. This arm is a FENCE:
// if any of the three ever reaches here, refusing by name is still better than
// lowering a graph that is right by coincidence. The synthetic rows for it
// assert the FRONT END rejects each, as for `try boundary disagreement`.
//
// The boundary a `fn` propagates into is its declared result, known before the
// body is lowered. A LAMBDA declares nothing: its result is discovered from the
// first `return` or the body's tail (resultInference), and a `?` is usually
// the first statement in the body. So the guard is written as a placeholder
// and filled in once the lambda has settled — patchPoint/patch, which the
// lambda's own signature line also uses. The placeholder is deliberately not
// valid (see unpatchedTryGuard): an unresolved guard must be an error, never a
// value that type-checks and answers wrongly.
//
// # Four boundaries, and the classification is the CHECKER's
//
// A `?` can sit in exactly four boundaries, and the checker already names
// which: it maintains `tryBoundary` for its own diagnostics and stamps the
// answer on the `?`'s hover symbol — `"fn <name>"`, `"lambda"`,
// `"concurrent"`, `"test \"<name>\""`. This file reads that back rather than
// re-deriving it (inferred.go's doctrine, applied to a fact the front end
// already settled), because the builder's own flags get one of the four wrong:
// `g.inTest` is true for a `?` inside a `concurrent` block inside a test
// body, and that `?` unwinds the BLOCK, not the test. The test-body arm
// returns out of the lowered test function, so reading `g.inTest` as "the
// boundary is the test" would end the case at a `?` that only unwinds the
// block. See tryBoundaryRefusal.
//
// The two views are cross-checked, and on both bits rather than one: the
// `lambda` bit against `g.inferResult != nil`, and the `test` bit against
// `g.inTest`. A disagreement is refused at the `?`'s own position, so a drift
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
//     FAIL <file> :: a ? propagates in a test body
//     line 23: test returned early
//     parse(-1)?
//     returned:
//     Err("negative -1")
//
//     — an rt.EarlyReturnFailure, carrying the `?`'s own line, the
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
// # A `?` stage needs no separate lowering, but it does need its own TEXT
//
// The PARSER builds `x |> f()?` as the pipe `x |> f()` and then a `?` stage
// (parser.go's peelQuestions): a `TryOp` with a nil `Expr` whose operand is
// the pipe's LEFT side, and the only thing pipe.go has to do is say so.
//
// The two forms differ in their rendered text. A `?` stage renders the whole
// `*ast.Binary` into its EarlyReturnFailure (`-1 |> parse()?`) where `E?`
// renders the `*ast.TryOp` (`parse(-1)?`), and a test body's report reads
// that text. So tryOp takes the node to RENDER separately from the node that
// carries the position: pipe.go passes the binary, native.go passes the
// TryOp. The formatter writes the `?` stage against its stage, so the pipe
// renders back as `-1 |> parse()?`; TestPinned_TryInTest is the pin, and it is a pin on literal text rather than
// on a comparison, because a comparison of two silences passes.

// recordedTryBoundary is the boundary the checker stamped on this `?`.
//
// fa.References keyed by the `?`'s own position, which is where both forms
// register: checkTryOp for `E?`, and checkBinary's pipe branch for a `?`
// stage.
func (g *gen) recordedTryBoundary(t *ast.TryOp) (string, bool) {
	if g.fa == nil {
		return "", false
	}
	sym, found := g.srcFA().References[analysis.Pos{Line: t.Line, Col: t.Col}]
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
