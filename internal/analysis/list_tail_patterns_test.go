package analysis_test

import (
	"strings"
	"testing"
)

// listPatternErrors checks body inside a function of a list of numbers, a
// list of lines and a distinct type over a list, and returns the error
// messages.
func listPatternErrors(t *testing.T, body string) []string {
	t.Helper()
	src := "type Items List<Int>\n\nfn run(xs: List<Int>, lines: List<String>, items: Items) -> Int {\n    _ = #(xs, lines, items)\n" + body + "\n}\n"
	_, errs := checkSourceWithStdlib(src)
	var out []string
	for _, e := range errs {
		out = append(out, e.Message+strings.Join(e.Hints, "; "))
	}
	return out
}

func wantListPatternError(t *testing.T, body, want string) {
	t.Helper()
	errs := listPatternErrors(t, body)
	for _, e := range errs {
		if strings.Contains(e, want) {
			return
		}
	}
	t.Errorf("checking\n%s\nreported %q, want an error containing %q", body, errs, want)
}

func wantNoListPatternError(t *testing.T, body string) {
	t.Helper()
	if errs := listPatternErrors(t, body); len(errs) > 0 {
		t.Errorf("checking\n%s\nreported %q, want no error", body, errs)
	}
}

// A list pattern with elements after its `..` tests them against the list's
// last elements, so it can fail: it needs an else as a binding, takes one,
// and does not cover a case on its own.
func TestListPatternsWithTailsCanFail(t *testing.T) {
	wantNoListPatternError(t, `    [1, .., 3] = xs else {
        return 0
    }
    1`)
	wantNoListPatternError(t, `    [first, ..mid, last] = xs else {
        return 0
    }
    first + Iter.count(mid) + last`)
	wantNoListPatternError(t, `    ["graph {", ..body, "}"] = lines else {
        return 0
    }
    Iter.count(body)`)
	wantListPatternError(t, `    [first, .., last] = xs
    first + last`, "this pattern can fail to match a value of type List<Int>")
	wantListPatternError(t, `    case xs {
        [1, .., 3] -> 1
    }`, "non-exhaustive case on List<Int>: missing []")
	wantListPatternError(t, `    case xs {
        [1, .., 3] -> 1
        [] -> 0
        [_] -> 0
    }`, "non-exhaustive case on List<Int>: missing [_, .., _]")
	wantListPatternError(t, `    case xs {
        [] -> 0
        [_] -> 0
        [_, _, _] -> 0
        [_, .., 3] -> 1
    }`, "non-exhaustive case on List<Int>: missing [_, _]")
}

// Patterns with tails still cover what they match: every length from their
// element count on, with the tails at the end.
func TestListPatternsWithTailsCover(t *testing.T) {
	wantNoListPatternError(t, `    case xs {
        [] -> 0
        [_] -> 1
        [_, .., _] -> 2
    }`)
	wantNoListPatternError(t, `    case xs {
        [] -> 0
        [.., last] -> last
    }`)
	wantNoListPatternError(t, `    case xs {
        [1, .., 3] -> 0
        [..] -> 1
    }`)
	wantNoListPatternError(t, `    case xs {
        [a, _, _] -> a
        [x, .., _] -> x
        [] -> 0
        [_] -> 1
    }`)
	wantNoListPatternError(t, `    [..all, last] = xs else {
        return 0
    }
    Iter.count(all) + last`)
	wantNoListPatternError(t, `    case items {
        Items[] -> 0
        Items[.., last] -> last
    }`)
}

// A tail's type is the element type, and the middle rest is a List.
func TestListPatternTailsAreTyped(t *testing.T) {
	wantListPatternError(t, `    [.., "x"] = xs else {
        return 0
    }
    1`, `pattern "x" is a String, but the value is an Int`)
	wantListPatternError(t, `    [_, ..mid, _] = xs else {
        return 0
    }
    mid`, "List<Int>")
}
