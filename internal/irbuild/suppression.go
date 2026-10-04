package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// Counting what the tally DECLINED to say, so a blocker set can report its own
// incompleteness.
//
// # The bias this file measures
//
// The blocker-set probe is COMPLETE over SYNTAX and INCOMPLETE over TYPES, and
// that asymmetry has one mechanism. `probe` descends into a refused subtree, so
// a file's set names every unsupported CONSTRUCT in it. But a refusal returns
// bad(), whose kind is kindInvalid, and a site whose operand kind is invalid
// declines to report rather than tallying one gap a second time (cascade.go,
// deliberately). So a refusal that fails to produce a KIND silences every
// downstream blocker whose NAME IS A TYPE.
//
// For example, if `diagnostics = compiler.check(...)` is refused, that binding
// has no type, so `test_helpers.has_diagnostic?(diagnostics, s)` cannot report
// a type refusal for a `List<Diagnostic>` nobody computed.
//
// # Direction is bounded; magnitude is not
//
// Un-masking can only ADD members to a set, never remove one, so a reported set
// is always a SUBSET of the true one and every percentage derived from it is a
// CEILING. But an unresolved kind produces another unresolved kind, so one
// masked refusal can hide a chain of arbitrary length and there is no a-priori
// bound on the magnitude.
//
// # What the counter counts, exactly
//
// One POSITION per (file, Nomi line) at which the builder consumed an operand
// whose kind was kindInvalid and took a path that records no blocker. Deduped
// by position for the same reason newUnsupportedError dedupes a construct at a
// position: `probe` and the real lowering both reach many sites, and a chain of
// propagation on one line is one masked line, not five.
//
// The position is the NODE's, taken through nodePos exactly as a refusal's is,
// and every suppression names the node the declined check would have been
// reported at. That is a correctness requirement rather than a nicety:
// gen.nomiLine is a LOWERING CURSOR and not a position. The cursor has already
// moved by the time a guard fires — walking the operand advances it to the
// operand's innermost descendant, and caseInto's arm probes every branch
// before declining, which advances it to the last line of the last branch.
// Because dedupe is BY position, a drifted line that lands on an
// already-counted one would vanish and silently undercount. See
// TestSuppression_PositionsAreTheNodesNotTheCursor.
//
// It is NOT a count of missing tally keys, and this file will not claim it is.
// A masked site might have lowered cleanly once its operand resolved. What the
// number does answer exactly is: is this file's blocker set complete? Zero
// means yes. Non-zero means the set is a strict lower bound.
//
// # Why bad() is not instrumented
//
// Most of bad()'s call sites follow a real `reject` — they are
// the refusal's own return value, not a suppression of somebody else's. Only a
// guard that tests an operand's produced kind and then declines to judge it is
// a suppression, so the counter lives at those guards.
//
// # Completeness is enforced as a rule
//
// Every `== kindInvalid` / `!= kindInvalid` comparison in this package is
// either ROUTED through the three calls below or carries a `kindInvalid:<class>`
// marker naming why it is not a suppression.
// TestSuppression_EveryKindInvalidComparisonIsRoutedOrExplained enforces that
// as a rule rather than pinning a count, so the guarantee is "no comparison is
// unexamined" instead of "the numbers have not moved".
//
// An unrouted comparison either reports its own refusal, propagates a kind
// whose judging site reports, or does not consume a refused operand at all.
// The marker classes are the closed vocabulary in suppression_guard_test.go.

// Suppression is one position at which the builder declined to name a blocker
// because the operand it was about to judge had no kind.
//
// Position rather than construct, because a suppression has no construct name —
// that is precisely what was lost.
type Suppression struct {
	File string
	Line int
}

// suppress records that this position declined to report. It is the primitive;
// the two wrappers below exist so a guard reads as a single expression.
//
// `at` is the node the declined check would have been REPORTED at — the same
// node the neighbouring `g.reject` names. That makes a suppression's position
// directly comparable to the refusal it stands in for, which is what the
// completeness report needs it to be.
func (g *gen) suppress(at ast.Node) {
	g.masked = append(g.masked, Suppression{File: g.nomiPath, Line: g.suppressionLine(at)})
}

// suppressionLine is the node's own line, falling back to the lowering cursor
// only for a node that never got a usable position — derive synthesis allocates
// from a band around 2^30 (see emittableLine), and a suppression at line
// 1077657600 would be a worse answer than a stale one.
func (g *gen) suppressionLine(at ast.Node) int {
	if isNilNode(at) {
		return g.nomiLine
	}
	if line, _ := nodePos(at); emittableLine(line) {
		return line
	}
	return g.nomiLine
}
