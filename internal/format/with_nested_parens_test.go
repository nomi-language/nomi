package format

import "testing"

// A `with` statement's struct literal value loses all its parentheses, not
// only the outer pair: `((FakeClock{}))` used to become `(FakeClock{})`,
// which the next format turned into `FakeClock{}`.
func TestFormat_WithStructLiteralLosesNestedParens(t *testing.T) {
	src := "fn f() {\n    with App.clock = ((FakeClock{at: 1}))\n    run()\n}\n"
	want := "fn f() {\n    with App.clock = FakeClock{at: 1}\n    run()\n}\n"
	formatsKeepingMeaning(t, src, want)
}
