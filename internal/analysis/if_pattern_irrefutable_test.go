package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// `if Pattern = value` tests whether the value matches. A pattern that
// matches every value of the value's type tests nothing, and is an error at
// the pattern. A bare name shadowing a value of the same type is usually
// `if x = 0` written for `if x == 0`, and the hint says so.

const ifIrrefutableDecls = `type Meters Int

struct Pair
    a: Int
    b: Int
end

enum Only
    V(Int)
end
`

func ifIrrefutableMsg(ty string) string {
	return fmt.Sprintf("this pattern always matches a value of type %s, so the `if` has nothing to test; bind it on its own line with `pattern = value`", ty)
}

func TestIfPattern_IrrefutableIsRejected(t *testing.T) {
	compareHint := "`if x = ...` binds a new `x`; to compare the existing `x` with the value, write `if x == ...`"
	for name, tc := range map[string]struct {
		body, pos, ty, hint string
	}{
		"a name over a parameter of the value's type": {`fn f(x: Int) -> Int
    if x = 0 then return -x
    x
end`, "13:8", "Int", compareHint},
		"a name over a binding of the value's type": {`fn f() -> Int
    x = 3
    if x = 0 then return 1
    x
end`, "14:8", "Int", compareHint},
		"a fresh name": {`fn f(n: Int) -> Int
    if y = n then return y
    0
end`, "13:8", "Int", ""},
		"a name over a value of another type": {`fn f(x: String) -> Int
    if x = 0 then return x
    0
end`, "13:8", "Int", ""},
		"a wildcard": {`fn f(n: Int) -> Int
    if _ = n then return 1
    0
end`, "13:8", "Int", ""},
		"a tuple of names": {`fn f(p: (Int, Int)) -> Int
    if (a, b) = p then return a + b
    0
end`, "13:8", "(Int, Int)", ""},
		"a distinct type": {`fn f(w: Meters) -> Int
    if Meters(m) = w then return m
    0
end`, "13:8", "Meters", ""},
		"a struct": {`fn f(p: Pair) -> Int
    if Pair{a:, b:} = p then return a + b
    0
end`, "13:8", "Pair", ""},
		"a single-variant enum": {`fn f(o: Only) -> Int
    if Only.V(n) = o then return n
    0
end`, "13:8", "Only", ""},
		"with an else, used for its value": {`fn f(n: Int) -> Int
    if a = n then a + 1 else 0
end`, "13:8", "Int", ""},
	} {
		_, errs := checkSourceWithStdlib(ifIrrefutableDecls + "\n" + tc.body + "\n")
		want := tc.pos + ": " + ifIrrefutableMsg(tc.ty)
		var got []string
		found := false
		for _, e := range errs {
			line := fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message)
			got = append(got, line)
			if line != want {
				continue
			}
			found = true
			hint := strings.Join(e.Hints, "\n")
			if hint != tc.hint {
				t.Errorf("%s: hint %q, want %q", name, hint, tc.hint)
			}
		}
		if !found {
			t.Errorf("%s: the front end accepts this; want %q, got:\n%s", name, want, strings.Join(got, "\n"))
		}
	}
}

// A pattern that can fail still makes a test, including a tuple with one
// refutable part.
func TestIfPattern_RefutableIsAccepted(t *testing.T) {
	for name, body := range map[string]string{
		"a variant": `fn f(m: Maybe<Int>) -> Int
    if Some(v) = m then return v
    0
end`,
		"a tuple with a variant": `fn f(p: (Int, Maybe<Int>)) -> Int
    if (a, Some(b)) = p then return a + b
    0
end`,
		"a literal": `fn f(n: Int) -> Int
    if 0 = n then return 1
    n
end`,
	} {
		_, errs := checkSourceWithStdlib(ifIrrefutableDecls + "\n" + body + "\n")
		if len(errs) != 0 {
			var got []string
			for _, e := range errs {
				got = append(got, fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message))
			}
			t.Errorf("%s: unexpected errors:\n%s", name, strings.Join(got, "\n"))
		}
	}
}
