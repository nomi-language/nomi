package vmhost_test

import "testing"

// `Range.step_by` runs for every element type std makes steppable (Int,
// Decimal and Codepoint), over inclusive and exclusive ranges, stepping by
// the element's own step type. A Float range has no Steppable impl, and a
// step of another numeric type is a checker error
// (TestBoundTypeArgs_StepByRejectsAStepOfAnotherNumericType).
func TestRangeStepBy_RunsForEverySteppableElement(t *testing.T) {
	got := runSourceOutput(t, `import std/io

fn main() {
    1..=10 |> Range.step_by(3) |> Iter.to_list() |> Debug.inspect() |> io.print()
    1..10 |> Range.step_by(3) |> Iter.to_list() |> Debug.inspect() |> io.print()
    1.0d..=1.3d |> Range.step_by(0.1d) |> Iter.to_list() |> Debug.inspect() |> io.print()
    1.0d..1.3d |> Range.step_by(0.1d) |> Iter.to_list() |> Debug.inspect() |> io.print()
    'a'..='e' |> Range.step_by(2) |> Iter.to_list() |> Debug.inspect() |> io.print()
    'a'..'e' |> Range.step_by(2) |> Iter.to_list() |> Debug.inspect() |> io.print()
}
`)
	want := `[1, 4, 7, 10]
[1, 4, 7]
[1.0d, 1.1d, 1.2d, 1.3d]
[1.0d, 1.1d, 1.2d]
[Codepoint(97), Codepoint(99), Codepoint(101)]
[Codepoint(97), Codepoint(99)]
`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
