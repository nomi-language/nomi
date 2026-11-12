package analysis_test

import (
	"strings"
	"testing"
)

// shapePatternErrors checks body inside a function of a set and a vector and
// returns the error messages.
func shapePatternErrors(t *testing.T, body string) []string {
	t.Helper()
	src := "type Path Vector<Int>\n\nfn run(s: Set<String>, v: Vector<Int>, p: Path) -> Int\n    _ = (s, v, p)\n" + body + "\nend\n"
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
	wantNoShapePatternError(t, `    case v
        [] -> 0
        [x, ..rest] -> x + Vector.length(rest)
    end`)
	wantNoShapePatternError(t, `    case v
        [] -> 0
        [x] -> x
        [first, .., last] -> first + last
    end`)
	wantNoShapePatternError(t, `    [..all] = v
    Vector.length(all)`)
	wantNoShapePatternError(t, `    case p
        Path[a, ..] -> a
        _ -> 0
    end`)
	wantShapePatternError(t, `    case v
        [] -> 0
        [x] -> x
    end`, "non-exhaustive case on Vector<Int>: missing [_, _, ..]")
	wantShapePatternError(t, `    [x, ..] = v
    x`, "this pattern can fail to match a value of type Vector<Int>")
	wantShapePatternError(t, `    case v
        [..Some(x)] -> x
        _ -> 0
    end`, "the rest of a vector pattern is a name or nothing: `..rest` or `..`")
	wantShapePatternError(t, `    case v
        ["a", ..] -> 1
        _ -> 0
    end`, `pattern "a" is a String, but the value is an Int`)
	wantShapePatternError(t, `    f = fn [x, ..]: Vector<Int> -> x
    f(v)`, "refutable pattern in parameter")
}

func TestSetPatternsCheck(t *testing.T) {
	wantNoShapePatternError(t, `    case s
        ["admin", ..rest] -> Set.size(rest)
        ["user", ..] -> 1
        [] -> 0
        _ -> -1
    end`)
	wantShapePatternError(t, `    who = "user"
    case s
        [who, ..] -> 1
        _ -> 0
    end`, "an element of a set pattern is a constant the set contains, not a place to bind: `who` would bind a name; `..who` binds the other elements, and a guard tests a value in scope (`when Set.contains(s, who)`)")
	wantShapePatternError(t, `    case s
        ["admin", ..] -> 1
        [] -> 0
    end`, "non-exhaustive case")
	wantShapePatternError(t, `    ["admin", ..rest] = s
    Set.size(rest)`, "a set pattern does not match a set that lacks one of its elements")
	wantShapePatternError(t, `    case s
        [_, ..] -> 1
        _ -> 0
    end`, "an element of a set pattern is a constant the set contains, not a place to bind: `_` names no value; `..` matches the other elements")
	wantShapePatternError(t, `    case s
        [Some(r), ..] -> 1
        _ -> 0
    end`, "an element of a set pattern is a constant the set contains, not a place to bind: write a number, string or codepoint")
	wantShapePatternError(t, `    case s
        [..rest, "a"] -> Set.size(rest)
        _ -> 0
    end`, "set pattern `..` must be the last element")
	wantShapePatternError(t, `    case s
        [1, ..] -> 1
        _ -> 0
    end`, "set pattern element type mismatch: expected String, got Int")
	wantShapePatternError(t, `    case s
        ["a", "a", ..] -> 1
        _ -> 0
    end`, "element `\"a\"` is given twice in this set pattern")
}

// A literal naming the std collection is that collection, and a named
// tuple's call checks its arity.
func TestOldShapeSpellingsAreCheckerErrors(t *testing.T) {
	const decls = "type Pair (Int, String)\n\nstruct Point\n    x: Int\nend\n\n"
	check := func(body, want string) {
		t.Helper()
		_, errs := checkSourceWithStdlib(decls + "fn run(p: Pair) -> Int\n    _ = p\n" + body + "\n    0\nend\n")
		for _, e := range errs {
			if strings.Contains(e.Message, want) {
				return
			}
		}
		t.Errorf("checking\n%s\nreported %v, want an error containing %q", body, errs, want)
	}
	check(`    v: List<Int> = Vector[1, 2]`, "type mismatch: expected List<Int>, got Vector<Int>")
	check(`    _ = Point[1, 2]`, "Point is not a type over a list, vector or set")
	check(`    _ = Pair(1, "a", 2)`, "Pair wraps (Int, String), a tuple of 2 elements; got 3")
}
