package irbuild

import (
	"testing"
)

// TestPreludeHof_ControlSignalsAreAFrontEndWall pins the reason preludehof.go
// needs no `rt.*Ctl` twin.
//
// ctrladapt.go gives a four-state `rt.Ctl` callback signature to every `Iter`
// family whose callback may carry `break`/`continue`, and names
// analysis/iter_sensitive.go's `iterCallbackSlots` as the authoritative list of
// positions where the two are legal. A prelude higher-order callback is not in
// that list, and the spelling does not even PARSE — which is a stronger wall than
// a checker rule, because it cannot be reached by any program.
//
// Pinned rather than reasoned about, because the failure it forecloses is
// asymmetric. If the parser ever admitted the spelling, preludeHofCall would
// receive a callback whose kind carries an extra `rt.Ctl` result and would
// lower a call at the wrong arity. This test fails first and says which
// file needs the twin.
func TestPreludeHof_ControlSignalsAreAFrontEndWall(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"break in a Maybe.map callback",
			"fn f(a: Maybe<Int>): Maybe<Int> {\n  Maybe.map(a, |x| break x)\n}\n"},
		{"continue in a Result.map callback",
			"fn f(a: Result<Int, String>): Result<Int, String> {\n  Result.map(a, |x| continue)\n}\n"},
		{"break in a Maybe.flat_map callback",
			"fn f(a: Maybe<Int>): Maybe<Int> {\n  Maybe.flat_map(a, |x| break Some(x))\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := AnalyzeSource("main", c.src); err == nil {
				t.Fatalf("the front end now accepts a control signal in a prelude higher-order callback, so preludehof.go needs an rt.Ctl twin per ctrladapt.go's table — it has none")
			}
		})
	}
	// The complement, and it is the half that says WHY the parser rule is not a
	// coincidence: the same signal in an `Iter.map` callback IS accepted,
	// because `Iter.map` is in iterCallbackSlots and this is not. Without this
	// row the test above would pass just as well if `break` had been removed
	// from the language.
	const inIter = "fn f(_xs: List<Int>): Int {\n  Iter.loop(|| {\n    break 1\n  })\n}\n"
	if _, err := AnalyzeSource("main", inIter); err != nil {
		t.Fatalf("`break` is no longer accepted in an Iter.loop callback either, so the row above proves nothing about prelude callbacks specifically: %v", err)
	}
}
