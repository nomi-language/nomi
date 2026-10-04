package irbuild

import (
	"strings"
	"testing"
)

// `std/ranges.Range<T>` as an anchored generic stdlib struct.
//
// The bound polarity, the reversed range, the saturated last element, the zero
// step and the count protocol are all VALUES, and a mistake in any of them
// changes the printed bytes. For a range the tempting wrong implementations are
// all off-by-one shaped. TestRanges_PinnedText states each rule against the
// mistake it excludes.

// TestRanges_PinnedText pins the reference bytes absolutely.
//
// Grouped the way the fixture is, and every group names the wrong implementation
// it excludes — because a comparison against a recorded output cannot tell
// "right" from "wrong the way the record was", and for a Range every plausible
// mistake is an off-by-one.
//
//	1-2-3-4-. / 1-2-3-4-5-.   THE BOUND POLARITY at identical endpoints. An
//	                          implementation that confused `..` with `..=` gets
//	                          exactly these four rows wrong and nothing else.
//	. / 3-.                   THE DEGENERATE RANGE. `3..3` is empty and `3..=3`
//	                          is one element — the only operands where the two
//	                          spellings differ in LENGTH rather than in the last
//	                          element, so a polarity bug off by one in the other
//	                          direction still shows.
//	. / .                     REVERSED IS EMPTY, both polarities. Neither may
//	                          walk downward, and neither may walk upward from 5
//	                          forever.
//	2-1-0-. / 2-1-.           SATURATION. `Discrete.next(Int.max_value)` is None
//	                          and std yields `start` ONE MORE TIME rather than
//	                          dropping it, so the inclusive walk is 3 elements
//	                          and the exclusive 2. Rendered as offsets from
//	                          max_value. Dropping the last element is the single
//	                          most tempting simplification in rt.RangeEachWhile
//	                          and this is the only row that catches it.
//	Some(4) … None None       known_count IS THE PROTOCOL. `Iter.count` consults
//	                          it before folding, so the two `None` rows are what
//	                          keep `Iter.count(Range.from(1))` from being a
//	                          non-terminating program rather than a value. Some(0)
//	                          for `5..1` comes from the start>end short-circuit
//	                          and NOT from steps_between.
//	200000 / 200010000        the O(1) count, then a 20_000-element WALK through
//	                          map and reduce. A per-element Go frame accumulates
//	                          here where std's tail recursion does not.
//	1-2-3-4-5-. / 0-1-2-. / . `Range.from`, `Range.naturals`, and `take(0)` — the
//	                          unbounded sources, each bounded BEFORE a consuming
//	                          terminal. `take(0)` must not touch the source at
//	                          all, which is what makes it terminate.
//	True True False False     bounded? — `case r.end` with no element method.
//	False True True False …   contains? AT BOTH BOUNDS, and they are asymmetric:
//	                          the LOWER bound is always inclusive, the UPPER
//	                          depends on the operator. So 1 is in `1..5` and 5 is
//	                          not, while 5 IS in `1..=5`.
//	True True False False …   the STRING and FLOAT rows: Comparable and NOT
//	                          Discrete, so an interval value that can be queried
//	                          and not walked. Both bounds again.
//	1-4-7-. / 1-4-7-. / .     step_by, and THE ZERO STEP THAT EMITS NOTHING.
//	                          `Range.step_by(1..=9, 0)` is `[]` — not `[1]` and
//	                          not a hang. The one exit in this type that is not
//	                          about the bound.
//	None                      a stepped range is a `Seq`, so known_count DECLINES
//	                          even though the underlying range could answer. std's
//	                          own disagreement: `impl Iter for StepByRange`
//	                          declares only `each_while`.
//	97-98-99-100-. …          Codepoint: the element's OWN predicate. 55295 and
//	                          57344 are the neighbours across the UTF-16
//	                          surrogate block, so a naive `n + 1` successor
//	                          produces a value `Codepoint.from_int` refuses and
//	                          the list comes out SHORT rather than wrong.
//	1..5 … "a".."m"           RENDERING, and the endpoints go through Debug and
//	                          NOT Display — std's choice at the impl. So a String
//	                          range renders WITH quotes under both renderings,
//	                          where `${["a"]}` is `[a]`. Passing the Display
//	                          renderer would change exactly the two quoted rows.
//	55 / 0-2-4-6-8-. …        the ordinary Iter surface over a Range source.
func TestRanges_PinnedText(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		// 1. bound polarity
		"1-2-3-4-.", "1-2-3-4-5-.", "0-1-2-3-4-.", "0-1-2-3-4-5-.",
		// 2. degenerate
		".", "3-.",
		// 3. reversed
		".", ".",
		// 4. saturation, inclusive then exclusive
		"2-1-0-.", "2-1-.",
		// 5. known_count
		"Some(4)", "Some(5)", "Some(0)", "Some(1)", "Some(0)", "None", "None",
		"200000",
		// 6. the long walk
		"200010000",
		// 7. unbounded, bounded first
		"1-2-3-4-5-.", "0-1-2-.", ".",
		// 8. bounded?
		"True", "True", "False", "False",
		// 9. contains? at both bounds
		"False", "True", "True", "False", "False", "True", "False",
		"False", "True", "True",
		// 10. non-discrete elements: "a" in, "h" in, "m" OUT (exclusive),
		// "z" out, then "m" IN under `..=`; Float exclusive/inclusive at 1.0
		// and one below the lower bound.
		"True", "True", "False", "False", "True", "False", "True", "False",
		// 11. step_by and the zero step
		"1-4-7-.", "1-4-7-.", ".", "1-2-3-4-5-6-7-8-9-.", ".",
		"None",
		// 12. Codepoint
		"97-98-99-100-.", "55295-57344-.", "97-99-.", "55295-57344-.", ".",
		// 13. rendering
		"1..5", "1..=5", "1..", "0..", `"a".."m"`, "0.0..=1.0", "1..=5", `"a".."m"`,
		// 14. pipelines
		"55", "0-2-4-6-8-.", "1-4-9-16-25-.", "True", "True",
	}, "\n") + "\n"
	got := vmReference("testdata/ranges.nomi")
	vmSkipIfKnownBlocked(t, got)
	if got.stdout != want || got.exit != 0 {
		t.Fatalf("reference output is not what this fixture pins: %s\nwant stdout=%q", got, want)
	}
}
