package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
)

// Where a FAILING assertion goes, which is a property of the BOUNDARY and not of
// the assertion.
//
// # One channel, two shapes at the other end
//
// A failed `assert` has exactly one exit: an early return — the same channel
// `return` and `try` ride — carrying a `Result.Err` whose payload is
// std/assertions' `AssertionFailure` as a NOMI VALUE. The innermost ACTIVATION
// catches it, and what happens next depends on what that activation is:
//
//   - A TEST body. The case runner reads the failure and prints the report,
//     so the value never reaches user code. The emitted test function returns
//     rt.TestFailure and a failing assertion returns the REPORT pointer
//     (*rt.AssertionFailure) straight out — nothing is converted, because nothing
//     is observed as a value.
//
//   - An ordinary `fn`. The `Result.Err` IS the function's return value, read by
//     the caller's `case` or pattern assertion and its fields selected by name.
//     So the report has to become the Nomi value, which is rt's
//     AssertionFailure.NomiFailure, the SAME conversion `testing.check` already goes through, pinned by
//     TestPinned_TestingCheck. There is no second renderer here and no second
//     rule; the only new thing is which variant the value is wrapped in.
//
// A LAMBDA is the third activation and it is a front-end error: the checker
// refuses `assert` in a lambda body outright ("becomes the lambda's return value
// instead of failing the test"), because the caller would discard the failure and
// the assertion would pass whatever it said.
//
// # The classification is the CHECKER's, read back rather than re-derived
//
// The checker maintains `tryBoundary` for its own diagnostics and stamps the
// answer on the assertion keyword's own hover symbol —
// analysis.AssertionInfo.Boundary, one of `"fn <name>"`, `"lambda"`,
// `"concurrent"`, `"test \"<name>\""`. try.go reads the same field off a `try`
// and for the same two reasons, both of which apply verbatim here:
//
//   - It is RIGHT where the builder's own flags are not. `g.inTest` is true for
//     an assertion inside a `concurrent` block inside a test body, and that
//     assertion unwinds the BLOCK — the checker pushes a boundary for one and the
//     test keeps running. Reading `g.inTest` there would end the case at an
//     assertion the program runs straight through.
//   - It makes a boundary DISAGREEMENT a refusal instead of a wrong answer.
//     `g.inferResult != nil` and `g.inTest` are two independent statements of two
//     of the checker's bits, so when either diverges the honest answer is
//     neither.
//
// Both spellings register: `checkAssertionSubject` for `assert expr` at the
// keyword's own position, `checkPatternDestructure` for `assert pat = expr` at
// AssertLine/AssertCol. So one reader serves tests.go and patternassert.go, which
// is what makes them agree about the boundary by construction.

// assertionBoundary is the boundary the checker stamped on one assertion
// keyword, at the position that keyword occupies.
func (g *gen) assertionBoundary(line, col int) (string, bool) {
	if g.fa == nil {
		return "", false
	}
	sym, found := g.fa.References[analysis.Pos{Line: line, Col: col}]
	if !found || sym == nil || sym.Kind != analysis.SymbolAssertion || sym.Assertion == nil {
		return "", false
	}
	return sym.Assertion.Boundary, true
}

// isTestBoundary is "this activation is a test case", over the checker's own
// three spellings for one thing.
//
// A `test "name"` declaration is `test %q`, a `//!` prompt case is
// `attached test for <owner>` (testBoundaryOverride), and a `tests` group's
// setup body is `setup of tests %q`: the setup runs inside every case it
// serves, so a `try` or assertion there ends that case. All three abort a case
// the same way, so anything reading the field has to admit all three. Matching only the
// first is a live hazard rather than a hypothetical: it reported an `assert` in
// every prompt case as a boundary disagreement, caught by TestPinned_AttachedTests
// the moment this reader replaced the raw `g.inTest` flag.
func isTestBoundary(boundary string) bool {
	return strings.HasPrefix(boundary, "test \"") ||
		strings.HasPrefix(boundary, "attached test for ") ||
		strings.HasPrefix(boundary, "setup of tests \"")
}

// assertionFailureKind is std/assertions' `AssertionFailure` as a Nomi VALUE:
// `rt.NomiAssertionFailure`, which is a different type from the reporter's
// `*rt.AssertionFailure`. See rt/nomiassertion.go for why there are two.
//
// Reports false when std/assertions' declaration does not anchor, which is the
// only way the def can be absent.
func assertionFailureKind() (kind, bool) {
	defs := stdStructDefs()
	if stdSpecAssertionFailure >= len(defs) || !stdStructValidated()[stdSpecAssertionFailure] {
		return kindInvalid, false
	}
	return named(defs[stdSpecAssertionFailure]), true
}
