package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The white-box unit tests for this defect live in
// analysis/operator_coherence_test.go, which calls detectImplCollisions
// directly. That is a probe of one function's grouping, and a probe of an
// internal function measures that function, not the program: the index it is
// handed is hand-built, so it proves nothing about whether the real pipeline
// populates ImplBlockInterfaceKey for these impls at all.
//
// These two drive the real BuildProject pipeline from source text, which is the
// entry a program enters: both programs must be rejected by analysis, because
// dispatch cannot hold both impls.
func TestBuildProject_OperatorImplsDifferingOnlyInOutputAreRejected(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
type Day Int
type Days Int

fn day_value(d: Day): Int {
  Day(n) = d
  n
}

fn days_value(d: Days): Int {
  Days(n) = d
  n
}

impl Add<Days, Day> for Day {
  fn add(lhs: Day, rhs: Days): Day {
    Day(day_value(lhs) + days_value(rhs))
  }
}

impl Add<Days, Days> for Day {
  fn add(lhs: Day, rhs: Days): Days {
    Days(day_value(lhs) + days_value(rhs))
  }
}

fn main() {
  Unit
}
`, nil)
	assertErrorContains(t, errs, "Add<Days, Day>")
	assertErrorContains(t, errs, "Add<Days, Days>")
	assertErrorContains(t, errs, "right-hand type")
}

// The second route to the same collision, on a NON-operator generic interface:
// dispatch keys any non-operator interface impl on the receiver ALONE, so both
// instantiations claim `Holds.hold` for `Box`.
func TestBuildProject_TwoInstantiationsOfANonOperatorInterfaceAreRejected(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
pub interface Holds<T> {
  fn hold(h: self): T
}

type Box Int

impl Holds<Int> for Box {
  fn hold(h: Box): Int {
    Box(n) = h
    n
  }
}

impl Holds<String> for Box {
  fn hold(_h: Box): String {
    "box"
  }
}

fn main() {
  Unit
}
`, nil)
	assertErrorContains(t, errs, "Holds<Int>")
	assertErrorContains(t, errs, "Holds<String>")
	assertErrorContains(t, errs, "Box")
}

// The population that must keep analyzing clean, driven through the same
// pipeline: three `Add` impls for one receiver at three different right-hand
// types — the shape `std/calendar` ships eleven of, per receiver, for four
// receivers.
func TestBuildProject_OperatorLadderAtDistinctRightHandTypesStillAnalyzes(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
type Meters Int
type Feet Int
type Yards Int

fn meters_value(m: Meters): Int {
  Meters(n) = m
  n
}

impl Add<Meters, Meters> for Meters {
  fn add(lhs: Meters, rhs: Meters): Meters {
    Meters(meters_value(lhs) + meters_value(rhs))
  }
}

impl Add<Feet, Meters> for Meters {
  fn add(lhs: Meters, rhs: Feet): Meters {
    Feet(f) = rhs
    Meters(meters_value(lhs) + f)
  }
}

impl Add<Yards, Meters> for Meters {
  fn add(lhs: Meters, rhs: Yards): Meters {
    Yards(y) = rhs
    Meters(meters_value(lhs) + y)
  }
}

fn main() {
  Unit
}
`, nil)
	for _, e := range errs {
		if e.Code == analysis.UnusedBindingCode {
			continue
		}
		t.Errorf("the three-rung `Add` ladder produced a diagnostic: %s", e.Message)
	}
}

// TestBuildProject_OutInBoundPositionStillSolves is the boundary of this change,
// and it is the reason `Out` stays a type PARAMETER.
//
// The spec states one rule for every generic interface
// (docs/spec.md, "Generic Interfaces"): the type parameter "is
// *determined* by each implementor (one impl per type), so it is never written on
// the interface name where the implementor is already known … Where it earns a
// name is exactly where the implementor is *unknown* — an interface-typed
// parameter … or a bound". `Out` is on both sides of that sentence:
//
//	impl Add<Bonus, Score> for Score   determined by the impl -> no discriminator
//	where L: Add<R, Out>               implementor unknown    -> earns its name
//
// So the fix removes `Out` from the coherence key for IMPLS and touches bound
// solving not at all. `tests/12-derives-and-standard-interfaces/
// add_test.nomi:90` writes the `plus` shape below, plus `minus`, `times` and
// `divided_by` on the three lines after it.
//
// # THE CALL SITE IS THE CORPUS'S SHAPE, AND FINDING THAT OUT COST TWO A/B RUNS
//
// Two earlier drafts of this fixture FAILED, and neither failure was this change.
// Both were run through binaries built with and without this change
// on the same file, and both report the same message on both sides, byte
// for byte:
//
//	score_value(plus(Score(2), Bonus(3)))   argument 1: expected Score, got Int
//	fn main(): Int { total: Score = plus(Day(20), Days(2)) … }
//	                                        type mismatch: expected Day, got Int
//
// The measured discriminator is not the annotation and not the operand types: the
// SAME declarations, the SAME call and the SAME annotation solve inside a `test`
// body and do not inside `fn main`. That is a pre-existing inference limit with
// nothing to do with impl coherence, recorded because a reader who writes the
// `fn main` form would otherwise read this test as promising it works, and
// because `add_test.nomi` — the corpus file that pins this behaviour — happens to
// use the `test` form for all six of its call sites, so the limit is invisible
// from there.
func TestBuildProject_OutInBoundPositionStillSolves(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
type Day Int
type Days Int

fn day_value(d: Day): Int {
  Day(n) = d
  n
}

fn days_value(d: Days): Int {
  Days(n) = d
  n
}

impl Add<Days, Day> for Day {
  fn add(lhs: Day, rhs: Days): Day {
    Day(day_value(lhs) + days_value(rhs))
  }
}

fn plus<L, R, Out>(lhs: L, rhs: R): Out where L: Add<R, Out> {
  lhs + rhs
}

test "bound position Out solves" {
  later: Day = plus(Day(20), Days(2))
  assert day_value(later) == 22
}
`, nil)
	for _, e := range errs {
		if e.Code == analysis.UnusedBindingCode {
			continue
		}
		t.Errorf("`Out` in BOUND position stopped solving; the coherence-key change was supposed "+
			"to touch impl identity only: %s", e.Message)
	}
}
