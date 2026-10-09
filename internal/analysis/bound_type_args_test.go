package analysis_test

import (
	"testing"
)

// A `where` bound with type arguments holds only when the subject has an
// impl with those arguments. `Range.step_by` is `where T: Steppable<S>`, and
// Decimal implements only Steppable<Decimal>, so a Float step for a Decimal
// range is an error at the call. Before, the checker asked only whether
// Decimal implements Steppable at all, accepted the call, and the IR builder
// then declined it.
func TestBoundTypeArgs_StepByRejectsAStepOfAnotherNumericType(t *testing.T) {
	for _, tc := range []struct {
		name, line, message, help string
		col                       int
	}{
		{
			"Float step, Decimal range, piped",
			"    _ = 1.0d..=1.3d |> Range.step_by(0.1) |> Iter.to_list()",
			"Decimal does not implement Steppable<Float> (required by `where T: Steppable<S>`)",
			"`Decimal` implements `Steppable<Decimal>`; Int, Float and Decimal never convert implicitly",
			24,
		},
		{
			"Float step, Decimal range, direct",
			"    _ = Range.step_by(1.0d..1.3d, 0.1) |> Iter.to_list()",
			"Decimal does not implement Steppable<Float> (required by `where T: Steppable<S>`)",
			"`Decimal` implements `Steppable<Decimal>`; Int, Float and Decimal never convert implicitly",
			9,
		},
		{
			"Decimal step, Int range",
			"    _ = 1..=10 |> Range.step_by(1d) |> Iter.to_list()",
			"Int does not implement Steppable<Decimal> (required by `where T: Steppable<S>`)",
			"`Int` implements `Steppable<Int>`; Int, Float and Decimal never convert implicitly",
			19,
		},
		{
			"Float step, Int range",
			"    _ = 1..10 |> Range.step_by(0.5) |> Iter.to_list()",
			"Int does not implement Steppable<Float> (required by `where T: Steppable<S>`)",
			"`Int` implements `Steppable<Int>`; Int, Float and Decimal never convert implicitly",
			18,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn main() {\n" + tc.line + "\n}\n"
			errs := checkWithStdlib(src)
			if len(errs) == 0 {
				t.Fatalf("the front end accepts a step of another type")
			}
			for _, e := range errs {
				if e.Message != tc.message {
					continue
				}
				if e.Line != 2 || e.Col != tc.col {
					t.Errorf("at %d:%d, want 2:%d", e.Line, e.Col, tc.col)
				}
				if len(e.Hints) != 1 || e.Hints[0] != tc.help {
					t.Fatalf("hints %q, want [%q]", e.Hints, tc.help)
				}
				return
			}
			t.Fatalf("no %q error; got %v", tc.message, errs)
		})
	}
}

// A step of the range's own element type still checks, for every element
// type std makes steppable.
func TestBoundTypeArgs_StepByAcceptsTheElementsOwnStep(t *testing.T) {
	for _, line := range []string{
		"    _ = 1.0d..=1.3d |> Range.step_by(0.1d) |> Iter.to_list()",
		"    _ = 1..10 |> Range.step_by(3) |> Iter.to_list()",
		"    _ = 'a'..='e' |> Range.step_by(2) |> Iter.to_list()",
	} {
		if errs := checkWithStdlib("fn main() {\n" + line + "\n}\n"); len(errs) > 0 {
			t.Errorf("%s: %v", line, errs)
		}
	}
}
