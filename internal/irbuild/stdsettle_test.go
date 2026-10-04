package irbuild

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
)

// The settling tests for lowerStdlibModule's two fixed points.
//
// # Why these are written against a SYNTHETIC stdlib module
//
// The properties under test are properties of the fixed point, not of std: std
// contains exactly ONE settleable self-recursive declaration
// (`bytes.join_debug_parts`) and ZERO settleable mutual cycles, so three of the
// four rows the acceptance needs — a mutual pair, a genuinely unsettleable
// sibling, and a TRANSITIVE dependent of one — cannot be stated over it at all.
// A test that could only observe the one case std happens to carry would pass
// for a fixed point that special-cased self-calls, which is the relaxation this
// is supposed to rule out.
//
// So `stdSettleFixture` analyzes source as an ordinary module and hands it to
// lowerStdlibModule, which is the unit: the same function buildStdlibIndex
// calls, with an EMPTY `earlier` index so nothing resolves cross-module and the
// answer is about this module's own settling and nothing else.
//
// # The row this file CANNOT hold
//
// Both fixed points are keyed on the candidate POINTER, not on `c.sib`, the
// `<recv>.<name>` SPELLING. A spelling is shared: std/calendar declares eleven
// `impl Add<X, NaiveDateTime>` blocks and every one of them spells
// `NaiveDateTime.add`. Keyed on the spelling, one settled rung would mark the
// other ten settled, bindStdSiblings would hand out a name for a body that
// never lowered, and a sibling's lowered call would reference it: a dangling
// call, not a refusal. See stdCandidate and bindStdSiblings.
//
// A row for it cannot be written against a synthetic module; each spelling
// fails before reaching the property:
//
//   - Two same-named methods from two NON-generic interfaces on one receiver
//     collapse in `stdKey` as well (`bytes.Bytes.to_string` is std's one live
//     case), so `byKey` holds one of them and the fixture cannot name the
//     other.
//   - A GENERIC interface separates the keys, but the FRONT END will not
//     select between the instantiations at a qualified call site:
//     `Bump.bump(n, False)` against `impl Bump<Int>`/`impl Bump<Bool>` is
//     `argument 2: expected Int, got Bool`, and `n.bump(False)` is
//     `Int has no field 'bump'`. Only the OPERATOR path selects by argument
//     kind, which is why std/calendar reaches it and a hand-written call
//     cannot.
//   - The operator path then needs a receiver that is inside the std scalar
//     subset and is NOT lowered natively. A module-declared
//     `pub type Meters Int` is `stdlib function outside the scalar subset` in
//     every position, because stdTypeKind admits a distinct only through an
//     opaqueSpecs anchor and a synthetic module has none. `Int` is inside the
//     subset and its `+` is native, so it never reaches the sibling scope.
//
// So the only witness is the REAL stdlib, where the Nomi-bodied
// `Add<Unit, NaiveDateTime>` rungs share one spelling.

// stdSettleFixture lowers src as though it were one stdlib module, answering
// every declaration the walk saw.
func stdSettleFixture(t *testing.T, src string) map[string]*stdFunc {
	t.Helper()
	return stdSettleLower(t, src, false)
}

// stdSettleLower is stdSettleFixture with the option to hand lowerStdlibModule
// an UNMARKED tree, which is what std.Load() hands it. lowerStdlibModule runs
// MarkTailCalls itself, so the interlock holds on such a tree.
func stdSettleLower(t *testing.T, src string, unmark bool) map[string]*stdFunc {
	t.Helper()
	byKey, _ := stdSettleLowerUnlowered(t, src, unmark)
	return byKey
}

// stdSettleLowerUnlowered is stdSettleLower that also answers the bodies the
// builder did not lower, keyed by declaration key with the builder's reason.
func stdSettleLowerUnlowered(t *testing.T, src string, unmark bool) (map[string]*stdFunc, map[string]string) {
	t.Helper()
	proj, err := frontend.New(frontend.Config{}).CheckSource("stdsettle", src, frontend.Mode{})
	if err != nil {
		t.Fatalf("the fixture must type-check before it can say anything about lowering: %v", err)
	}
	nodes, fa := proj.Nodes, proj.FA
	if unmark {
		marks := 0
		for _, n := range nodes {
			marks += clearTailMarks(n)
		}
		if marks == 0 {
			t.Fatal("the fixture carried no tail marks to begin with, so unmarking it proves nothing")
		}
	}
	empty := &stdlibIndex{
		byType:    map[string][]*stdFunc{},
		byFile:    map[string]*stdFunc{},
		byIface:   map[string]map[kind][]*stdFunc{},
		modulePkg: map[string]string{},
		byKey:     map[string]*stdFunc{},
		byOnce:    map[string]*stdOnce{},
	}
	// false — no `//!` prompt cases; this test is about body settling. See
	// stdtests.go.
	//
	// The context is built through stdModuleContext, which is what
	// buildStdlibIndex does too — lowerStdlibModule stopped building its own
	// when the index began retaining it for the per-program instance driver.
	// MarkTailCalls is still stdModuleContext's first line, which is what
	// TestStdSettling_TailMarksAreNotAssumed is about, so the `unmark` arm
	// above still exercises the same thing.
	v := stdModuleContext("stdsettle", "stdsettle.nomi", "nomistdsettle", nodes, fa, empty)
	funcs, _, _ := lowerStdlibModule(v, empty)
	byKey := make(map[string]*stdFunc, len(funcs))
	for _, f := range funcs {
		byKey[f.key] = f
	}
	unlowered := map[string]string{}
	for _, why := range v.unlowered {
		key, reason, _ := strings.Cut(why, "|")
		unlowered[key] = reason
	}
	return byKey, unlowered
}

// clearTailMarks erases every tail-position mark under n, answering how many it
// erased.
func clearTailMarks(n ast.Node) int {
	if isNilNode(n) {
		return 0
	}
	cleared := 0
	if call, ok := n.(*ast.Call); ok && call.IsTailCall {
		call.IsTailCall = false
		cleared++
	}
	for _, child := range childNodes(n) {
		cleared += clearTailMarks(child)
	}
	return cleared
}

// stdSettleSource is the fixture every row below reads, written as one module so
// the rows share one fixed point rather than one per test — which is the point:
// a settling answer is a property of the WHOLE module, and a per-row module
// could not express the transitive row at all.
const stdSettleSource = `
/// The CONTROL: no recursion anywhere. It settles in phase 1, and it is here so
/// a failure of the rows below is distinguishable from the whole harness being
/// broken. Without it a fixed point that settled nothing at all would pass every
/// negative row.
fn doubled(n: Int): Int {
  n * 2
}

/// Phase 1 with a sibling, the shape that already worked: settles in round 2,
/// against a sibling that settled in round 1.
fn quadrupled(n: Int): Int {
  doubled(doubled(n))
}

/// SELF-RECURSIVE, and not in tail position — the recursive call is an operand
/// of ` + "`+`" + `. This is what the least fixed point could never settle, because it
/// only offers a function once ` + "`settled[c.sib]`" + ` is true and that is false for the
/// whole of the round in which this one is tried.
fn depth(n: Int): Int {
  case n {
    0 -> 0
    _ -> 1 + depth(n - 1)
  }
}

/// A MUTUAL cycle, both members non-tail. A different fixed-point problem from
/// self-recursion — neither member can be settled before the other — so it is
/// stated separately rather than assumed to follow.
fn ping(n: Int): Int {
  case n {
    0 -> 0
    _ -> 1 + pong(n - 1)
  }
}

fn pong(n: Int): Int {
  case n {
    0 -> 0
    _ -> 1 + ping(n - 1)
  }
}

/// A caller of a phase-2 member. It cannot settle in phase 1 — ` + "`depth`" + ` is not
/// offered there — so it becomes a phase-2 candidate itself and settles with no
/// cycle of its own. Here because the emission pass binds two sibling scopes and
/// this is the row that fails if the second one is not bound.
fn depth_plus(n: Int): Int {
  depth(n) + 1
}

/// TAIL-recursive, and therefore REFUSED rather than lowered. Nomi guarantees
/// constant stack for this call (spec §12.7) and a stdlib gen has no trampoline,
/// so lowering it as plain Go recursion would be a stack overflow on a large
/// input rather than a Nomi answer.
fn spin(n: Int, acc: Int): Int {
  case n {
    0 -> acc
    _ -> spin(n - 1, acc + n)
  }
}

/// A struct this module declares, which is PERMANENTLY outside the scalar
/// boundary: its kind identity is a *typeDef belonging to this generated
/// package, which is the whole reason the boundary exists (see stdTypeKind).
/// Chosen for the unsettleable rows precisely because it cannot stop being
/// unsettleable when the builder learns to lower some other construct; a fixture
/// whose negative row can silently become vacuous is worse than no fixture.
struct Box {
  n: Int
}

/// UNSETTLEABLE, by signature. Never a candidate at all.
fn boxed(b: Box): Int {
  b.n
}

/// Self-recursive AND dependent on the unsettleable one. THE discriminating
/// row: a fixed point that assumed its members settle would emit this, and the
/// call it emitted would name a function nothing generates.
fn needs_boxed(n: Int): Int {
  case n {
    0 -> boxed(Box { n: n })
    _ -> 1 + needs_boxed(n - 1)
  }
}

/// TRANSITIVELY dependent, and self-recursive so only phase 2 can reach it.
/// Survives phase 2's FIRST round — ` + "`needs_boxed`" + ` is offered in it — and must be
/// removed by the second, which is the propagation that makes the closure a
/// fixed point rather than one optimistic pass.
///
/// The ` + "`+ 1`" + ` is LOAD-BEARING and must not be tidied away: a bare
/// ` + "`needs_boxed(n)`" + ` in a case arm is in TAIL position, so the interlock would
/// claim this declaration in round 1 and the row would stop testing propagation
/// while still passing under a different name.
fn needs_needs_boxed(n: Int): Int {
  case n {
    0 -> needs_boxed(n) + 1
    _ -> 1 + needs_needs_boxed(n - 1)
  }
}
`

// TestStdSettling_TerminatesAndIsOrderIndependent is the convergence claim
// stated as a claim rather than as a runtime that happened to finish.
//
// Two properties, and they are separate. TERMINATION rests only on F(S) being a
// subset of S over a finite set, so the loop is checked to finish at all (a
// non-terminating one would hang this test rather than fail it, which is why the
// second property is the useful one). DETERMINISM rests on F being monotone, and
// it is checked by permuting the declaration order the walk produces — a fixed
// point that leaked its visit order would answer differently.
func TestStdSettling_TerminatesAndIsOrderIndependent(t *testing.T) {
	first := stdSettleFixture(t, stdSettleSource)
	settled := func(byKey map[string]*stdFunc) []string {
		var out []string
		for key, f := range byKey {
			if f.body {
				out = append(out, key)
			}
		}
		sort.Strings(out)
		return out
	}
	want := settled(first)
	if len(want) == 0 {
		t.Fatal("nothing settled at all, so this test is vacuous")
	}

	// Same declarations, reversed source order. The fixed point must answer the
	// same set: within a round every decision is taken against the round's
	// snapshot, and removals are applied only after the round ends.
	reversed := reverseDecls(stdSettleSource)
	second := stdSettleFixture(t, reversed)
	if got := settled(second); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("declaration order changed the answer:\n  forward: %v\n  reverse: %v", want, got)
	}
}

// reverseDecls reverses the order of the fixture's top-level declarations,
// splitting on blank-line-separated blocks. Crude on purpose: it has to move
// declarations around, not understand them.
func reverseDecls(src string) string {
	blocks := strings.Split(strings.TrimSpace(src), "\n\n")
	for i, j := 0, len(blocks)-1; i < j; i, j = i+1, j-1 {
		blocks[i], blocks[j] = blocks[j], blocks[i]
	}
	return "\n" + strings.Join(blocks, "\n\n") + "\n"
}
