package analysis_test

import (
	"fmt"
	"testing"
)

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

// An assertion written where its value feeds another expression is rejected
// at its keyword, with nothing else reported: the subject of another
// assertion, a call's argument, a collection element, a parenthesized
// expression, a condition, a returned value, the head of a pipe.
func TestAssertionAsValue_IsRejected(t *testing.T) {
	const valueMsg = "`%s` cannot be used as a value here: an assertion stands as its own statement or as a binding's value"
	const bindHint = "write the assertion on its own line, or bind its value first: `ok = %s condition`"
	for _, tc := range []struct {
		name, stmt, kw string
		col            int
		hint           string
	}{
		{"assertion subject", "assert assert x", "assert", 10,
			"remove the outer `assert`: it would check the value this `assert` passes on, not a condition"},
		{"refute subject", "assert refute !x", "refute", 10,
			"remove the outer `assert`: it would check the value this `refute` passes on, not a condition"},
		{"pattern assertion value", "assert True = assert x", "assert", 17, ""},
		{"call argument", "assert id(assert x)", "assert", 13, ""},
		{"call statement", "id(assert x)", "assert", 6, ""},
		{"list element", "xs = [assert x]\n  assert xs == [True]", "assert", 9, ""},
		{"tuple element", "t = (assert x, 1)\n  assert t == (True, 1)", "assert", 8, ""},
		{"variant payload", "m = Some(refute !x)\n  assert m", "refute", 12, ""},
		{"parenthesized binding value", "ok = (assert x)\n  assert ok", "assert", 9, ""},
		{"if condition", "assert if assert x { True } else { False }", "assert", 13, ""},
		{"case scrutinee", "assert case assert x {\n    True -> True\n    False -> False\n  }", "assert", 15, ""},
		{"returned value", "return assert x", "assert", 10, ""},
		{"pipe head", "ok = (assert x) |> id()\n  assert ok", "assert", 9, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn id(b: Bool): Bool {\n  b\n}\n\ntest \"x\" {\n  x = True\n  " + tc.stmt + "\n}\n"
			_, errs := checkSourceWithStdlib(src)
			errs = withoutUnusedBindingErrors(errs)
			if len(errs) == 0 {
				t.Fatalf("the front end accepts %q; want it rejected", tc.stmt)
			}
			if len(errs) != 1 {
				t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
			}
			e := errs[0]
			if want := fmt.Sprintf(valueMsg, tc.kw); e.Message != want {
				t.Errorf("message = %q, want %q", e.Message, want)
			}
			if e.Col != tc.col {
				t.Errorf("column = %d, want %d", e.Col, tc.col)
			}
			hint := tc.hint
			if hint == "" {
				hint = fmt.Sprintf(bindHint, tc.kw)
			}
			if len(e.Hints) != 1 || e.Hints[0] != hint {
				t.Errorf("hints = %q, want [%q]", e.Hints, hint)
			}
		})
	}
}

// The mirror: the places an assertion may stand are still accepted.
func TestAssertionAsValue_StatementPositions(t *testing.T) {
	for _, stmt := range []string{
		"assert x",
		"refute !x",
		"assert x |> id()",
		"ok = assert x |> id()\n  assert ok",
		"ok = refute !x\n  refute ok",
		"assert True = x",
		"case id(x) {\n    True -> assert x\n    False -> refute x\n  }",
	} {
		src := "fn id(b: Bool): Bool {\n  b\n}\n\ntest \"x\" {\n  x = True\n  " + stmt + "\n}\n"
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
}
