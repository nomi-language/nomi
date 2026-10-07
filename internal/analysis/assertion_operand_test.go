package analysis_test

import "testing"

// An `assert` or `refute` is not an operand: `!assert x` reads as a negated
// check but passes when x holds and then evaluates to False.
func TestAssertionOperand_IsRejected(t *testing.T) {
	for _, tc := range []struct{ stmt, want, hint string }{
		{"!assert 1 == 1", "`assert` cannot be an operand of `!`",
			"to check that a condition is false, write `refute condition` or `assert !condition`"},
		{"x = !(refute 1 == 2)\n  assert x", "`refute` cannot be an operand of `!`", ""},
		{"x = (assert 1 == 1) and True\n  assert x", "`assert` cannot be an operand of `and`",
			"write the assertion as its own statement, or bind its value first: `ok = assert condition`"},
		{"x = True == (assert True)\n  assert x", "`assert` cannot be an operand of `==`", ""},
	} {
		src := "test \"x\" {\n  " + tc.stmt + "\n}\n"
		_, errs := checkSourceWithStdlib(src)
		expectStdlibError(t, errs, tc.want)
		if tc.hint != "" {
			expectStdlibError(t, errs, tc.hint)
		}
	}
}

// The mirror: an assertion as a statement or a binding's value, and a
// negated subject, are accepted.
func TestAssertionOperand_StatementForms(t *testing.T) {
	for _, stmt := range []string{
		"assert !(1 == 2)",
		"refute 1 == 2",
		"b = assert 1 == 1\n  assert b",
	} {
		src := "test \"x\" {\n  " + stmt + "\n}\n"
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
}
