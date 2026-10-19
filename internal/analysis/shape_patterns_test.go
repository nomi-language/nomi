package analysis_test

import (
	"strings"
	"testing"
)

// shapePatternErrors checks body inside a function of a set and a vector and
// returns the error messages.
func shapePatternErrors(t *testing.T, body string) []string {
	t.Helper()
	src := "type Path Vector<Int>\n\nfn run(s: Set<String>, v: Vector<Int>, p: Path) -> Int {\n    _ = #(s, v, p)\n" + body + "\n}\n"
	_, errs := checkSourceWithStdlib(src)
	var out []string
	for _, e := range errs {
		out = append(out, e.Message+strings.Join(e.Hints, "; "))
	}
	return out
}

func wantShapePatternError(t *testing.T, body, want string) {
	t.Helper()
	errs := shapePatternErrors(t, body)
	for _, e := range errs {
		if strings.Contains(e, want) {
			return
		}
	}
	t.Errorf("checking\n%s\nreported %q, want an error containing %q", body, errs, want)
}

func wantNoShapePatternError(t *testing.T, body string) {
	t.Helper()
	if errs := shapePatternErrors(t, body); len(errs) > 0 {
		t.Errorf("checking\n%s\nreported %q, want no error", body, errs)
	}
}

func TestVectorPatternsCheck(t *testing.T) {
	wantNoShapePatternError(t, `    case v {
        #[] -> 0
        #[x, ..rest] -> x + Vector.length(rest)
    }`)
	wantNoShapePatternError(t, `    case v {
        #[] -> 0
        #[x] -> x
        #[first, .., last] -> first + last
    }`)
	wantNoShapePatternError(t, `    #[..all] = v
    Vector.length(all)`)
	wantNoShapePatternError(t, `    case p {
        #Path[a, ..] -> a
        _ -> 0
    }`)
	wantShapePatternError(t, `    case v {
        #[] -> 0
        #[x] -> x
    }`, "non-exhaustive case on Vector<Int>: missing #[_, _, ..]")
	wantShapePatternError(t, `    #[x, ..] = v
    x`, "this pattern can fail to match a value of type Vector<Int>")
	wantShapePatternError(t, `    case s {
        #[x, ..] -> 1
        _ -> 0
    }`, "vector pattern requires a vector, got Set<String>")
	wantShapePatternError(t, `    case v {
        #["a", ..] -> 1
        _ -> 0
    }`, `pattern "a" is a String, but the value is an Int`)
	wantShapePatternError(t, `    f = fn #[x, ..]: Vector<Int> -> x
    f(v)`, "refutable pattern in parameter")
}

func TestSetPatternsCheck(t *testing.T) {
	wantNoShapePatternError(t, `    case s {
        %["admin", ..rest] -> Set.size(rest)
        %["user", ..] -> 1
        %[] -> 0
        _ -> -1
    }`)
	wantNoShapePatternError(t, `    who = "user"
    case s {
        %[who, ..] -> 1
        _ -> 0
    }`)
	wantShapePatternError(t, `    case s {
        %["admin", ..] -> 1
        %[] -> 0
    }`, "non-exhaustive case")
	wantShapePatternError(t, `    %["admin", ..rest] = s
    Set.size(rest)`, "a set pattern does not match a set that lacks one of its elements")
	wantShapePatternError(t, `    case s {
        %[_, ..] -> 1
        _ -> 0
    }`, "an element of a set pattern is a value to look up, not a place to bind: `_` names no value; `..` matches the other elements")
	wantShapePatternError(t, `    case s {
        %[role, ..] -> 1
        _ -> 0
    }`, "an element of a set pattern is a value to look up, not a place to bind: `role` is not in scope, and `..role` binds the other elements")
	wantShapePatternError(t, `    case s {
        %[1, ..] -> 1
        _ -> 0
    }`, "set pattern element type mismatch: expected String, got Int")
	wantShapePatternError(t, `    case s {
        %["a", "a", ..] -> 1
        _ -> 0
    }`, "element `\"a\"` is given twice in this set pattern")
	wantShapePatternError(t, `    case v {
        %[1, ..] -> 1
        _ -> 0
    }`, "set pattern requires a set, got Vector<Int>")
}

// The spellings the shape symbols replace that still parse are checker
// errors, never another meaning.
func TestOldShapeSpellingsAreCheckerErrors(t *testing.T) {
	const decls = "type Pair #(Int, String)\n\n"
	check := func(body, want string) {
		t.Helper()
		_, errs := checkSourceWithStdlib(decls + "fn run(p: Pair) -> Int {\n    _ = p\n" + body + "\n    0\n}\n")
		for _, e := range errs {
			if strings.Contains(e.Message, want) {
				return
			}
		}
		t.Errorf("checking\n%s\nreported %v, want an error containing %q", body, errs, want)
	}
	check(`    _ = Vector[1, 2]`, "Vector is not a list-distinct type")
	check(`    _ = Set[1, 2]`, "Set is not a list-distinct type")
	check(`    _ = Pair(1, "a")`, "Pair takes one argument, its tuple #(Int, String); got 2")
	check(`    _ = case p {
        Pair(n, s) -> n
    }`, "Pair takes one value, its tuple #(Int, String); a pattern takes it apart with `#Pair(...)`")
}
