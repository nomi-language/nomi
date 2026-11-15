package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// `continue` in an `Iter.iterate` step is a compile error at the
// `continue`: the step's answer is the next state, so a step that skips has
// no state to go on to. The rule is the step's alone, a lambda's own scope
// or a named function passed as the step.
const iterateContinueMessage = "`continue` in an `Iter.iterate` step has no next state to go on to: the step's answer is the next state"

func iterateContinueErrors(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Message == iterateContinueMessage {
			out = append(out, e)
		}
	}
	return out
}

func TestIterateContinue_IsRejectedAtTheContinue(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		line, col int
	}{
		{"lambda", "fn main()\n    xs = Iter.iterate(1, fn n ->\n        if n > 3 then continue\n        n * 2\n    end) |> Iter.take(3) |> Iter.to_list()\n    dbg xs\nend\n", 3, 23},
		{"piped seed", "fn main()\n    xs = 1 |> Iter.iterate(fn n ->\n        if n > 3 then continue\n        n * 2\n    end) |> Iter.take(3) |> Iter.to_list()\n    dbg xs\nend\n", 3, 23},
		{"named step", "fn step(n: Int) -> Int\n    if n > 3 then continue\n    n * 2\nend\n\nfn main()\n    xs = Iter.iterate(1, step) |> Iter.take(3) |> Iter.to_list()\n    dbg xs\nend\n", 7, 26},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			got := iterateContinueErrors(errs)
			if len(got) != 1 {
				t.Fatalf("the front end admits a `continue` in an Iter.iterate step, or reports it %d times; errors: %v\n%s", len(got), errs, tc.src)
			}
			if got[0].Line != tc.line || got[0].Col != tc.col {
				t.Errorf("reported at %d:%d, want %d:%d", got[0].Line, got[0].Col, tc.line, tc.col)
			}
			if len(got[0].Hints) != 1 || got[0].Hints[0] != "skip states after the step instead: `Iter.iterate(seed, step) |> Iter.filter(keep)`" {
				t.Errorf("hints = %q", got[0].Hints)
			}
		})
	}
}

// `break` in a step still ends the iteration, a `continue` in another
// iteration's callback inside the step is that callback's, and `continue`
// in another iteration's callback is unaffected.
func TestIterateContinue_OtherSignalsAreAccepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"break", "fn main()\n    xs = Iter.iterate(1, fn n ->\n        if n > 3 then break\n        n * 2\n    end) |> Iter.to_list()\n    dbg xs\nend\n"},
		{"nested callback", "fn main()\n    xs = Iter.iterate([1], fn ns ->\n        Iter.map(ns, fn n ->\n            if n > 3 then continue\n            n * 2\n        end) |> Iter.to_list()\n    end) |> Iter.take(2) |> Iter.to_list()\n    dbg xs\nend\n"},
		{"map", "fn main()\n    xs = Iter.map([1, 5], fn n ->\n        if n > 3 then continue\n        n * 2\n    end) |> Iter.to_list()\n    dbg xs\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			if errs = withoutUnusedBindingErrors(errs); len(errs) != 0 {
				t.Errorf("errors %v\n%s", errs, tc.src)
			}
		})
	}
}
