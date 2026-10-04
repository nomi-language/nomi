package analysis_test

import (
	"strings"
	"testing"
)

// TestBoolLiteral_LowercaseIsUnbound pins that `true` and `false` are ordinary
// unbound names: the Bool literals are the prelude variants `True` and `False`.
// The checker used to type a bare `true` as Bool, so `nomi check` passed a
// program the IR builder could not lower. The error sits on the name and names
// the literal.
func TestBoolLiteral_LowercaseIsUnbound(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"true", "undefined variable 'true'\nhelp: the Bool literal is 'True'"},
		{"false", "undefined variable 'false'\nhelp: the Bool literal is 'False'"},
	} {
		_, errs := checkSourceWithStdlib("fn f(): Bool {\n  x: Bool = " + tc.name + "\n  x\n}\n")
		found := false
		for _, e := range errs {
			if strings.Contains(diagText(e), tc.want) {
				found = true
				if e.Line != 2 || e.Col != 13 {
					t.Errorf("%s: the error is at %d:%d, want 2:13 (the name)", tc.name, e.Line, e.Col)
				}
			}
		}
		if !found {
			expectStdlibError(t, errs, tc.want)
		}
	}
}

// TestBoolLiteral_CapitalizedIsAccepted is the acceptance mirror.
func TestBoolLiteral_CapitalizedIsAccepted(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn f(): Bool {\n  x: Bool = True\n  y: Bool = False\n  x && !y\n}\n")
	expectNoStdlibErrors(t, errs)
}
