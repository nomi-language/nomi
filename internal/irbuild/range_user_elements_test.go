package irbuild

import "testing"

// Range literals over user element types, built, rendered, tested with
// `Range.contains?` and walked with `Range.step_by`. The VM's range hosts take
// the element's comparator and step function as function values, and for a
// user type both are the program's own impl bodies: elemComparator and
// elemStepper choose them, this file's or the declaring file's. A user
// `impl Steppable<Int> for T` lowers through the operator-impl route
// (operimpl.go), so `Steppable.step_by(m, 4)` and `Meters.step_by(m, 4)` run
// as well. Every line was BLOCKED, the literal as `*ast.RangeLit`.
func TestRangeUserElements_BuildContainAndStep(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"Meters{n: 1}..=Meters{n: 7}\n" +
		"True\n" +
		"False\n" +
		"False\n" +
		"[Meters{n: 1}, Meters{n: 3}, Meters{n: 5}, Meters{n: 7}]\n" +
		// 8 has no further step (8 + 3 > 9), so it is the last element.
		"[Meters{n: 5}, Meters{n: 8}]\n" +
		"Some(Meters{n: 5})\n" +
		"None\n" +
		"[Low, Mid, High]\n" +
		"False\n" +
		"[Grams(1), Grams(2), Grams(3)]\n" +
		"True\n" +
		"False\n"
	got := vmReference(fixture("range_user_elements/main.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("ranges over user element types:\nwant stdout=%q\ngot  %s", want, got)
	}
}
