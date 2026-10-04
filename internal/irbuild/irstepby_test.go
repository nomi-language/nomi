package irbuild

import "testing"

func TestIRStepBy_TourDecimalRange(t *testing.T) {
	verifyLambdaProgram(t, `fn main(): List<Decimal> {
  1.0d..=1.3d
  |> Range.step_by(0.1d)
  |> Iter.to_list()
  |> dbg
}
`, "dbg line 5:\n  1.0d..=1.3d\n  |> Range.step_by(0.1d)\n  |> Iter.to_list()\n  = [1.0d, 1.1d, 1.2d, 1.3d]\n")
}

func TestIRStepBy_BoundsZeroStepAndLaziness(t *testing.T) {
	verifyLambdaProgram(t, `fn steps(r: Range<Decimal>, by: Decimal): List<Decimal> {
  Range.step_by(r, by) |> Iter.to_list()
}
fn main(): List<Decimal> {
  dbg steps(0.0d..1.0d, 0.25d)
  dbg steps(0.0d..=1.0d, 0.25d)
  dbg steps(1.0d..=2.0d, 0.0d)
  dbg steps(2.0d..=1.0d, 0.5d)
  dbg steps(1.50d..=2.5d, 0.5d)
  dbg Range.step_by(0.0d..=100.0d, 0.5d) |> Iter.take(3) |> Iter.to_list()
}
`, "dbg line 5: steps(0.0d..1.0d, 0.25d) = [0.0d, 0.25d, 0.50d, 0.75d]\ndbg line 6: steps(0.0d..=1.0d, 0.25d) = [0.0d, 0.25d, 0.50d, 0.75d, 1.00d]\ndbg line 7: steps(1.0d..=2.0d, 0.0d) = []\ndbg line 8: steps(2.0d..=1.0d, 0.5d) = []\ndbg line 9: steps(1.50d..=2.5d, 0.5d) = [1.50d, 2.00d, 2.50d]\ndbg line 10: Range.step_by(0.0d..=100.0d, 0.5d) |> Iter.take(3) |> Iter.to_list() = [0.0d, 0.5d, 1.0d]\n")
}
