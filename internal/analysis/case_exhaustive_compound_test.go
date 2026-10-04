package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// Exhaustiveness over tuple, struct, distinct, list and map scrutinees, and
// over enums nested inside them. A literal never covers its type, so a column
// it decides needs a catch-all; enums, tuples, structs, distinct types and
// lists are covered component by component.

const compoundDecls = `
struct Point {
  x: Int
  y: Int
}

struct Slot {
  name: String
  value: Maybe<Int>
}

enum Shape {
  Circle(Float)
  Rect{w: Float, h: Float}
  Empty
}

type Wrapped Maybe<Int>
`

// nonExhaustive returns the coverage errors among errs, as their messages.
func nonExhaustive(errs []analysis.TypeError) []string {
	var out []string
	for _, e := range errs {
		if strings.HasPrefix(e.Message, "non-exhaustive") {
			out = append(out, e.Message)
		}
	}
	return out
}

func TestCaseExhaustive_CompoundScrutineesRejected(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"tuple of literals": {
			"fn f(n: Int): String {\n  case (n, n) {\n    (0, 0) -> \"zero\"\n  }\n}",
			"non-exhaustive case: add a `_` arm",
		},
		"tuple of Maybes missing (None, None)": {
			"fn f(a: Maybe<Int>, b: Maybe<Int>): Int {\n  case (a, b) {\n    (Some(x), None) -> x\n    (None, Some(y)) -> y\n    (Some(x), Some(y)) -> x + y\n  }\n}",
			"non-exhaustive case on (Maybe<Int>, Maybe<Int>): missing (None, None)",
		},
		"tuple with a literal beside an enum": {
			"fn f(a: Maybe<Int>, n: Int): Int {\n  case (a, n) {\n    (Some(x), _) -> x\n    (None, 0) -> 0\n  }\n}",
			"non-exhaustive case on (Maybe<Int>, Int): missing (None, _)",
		},
		"tuple of enum and Bool": {
			"fn f(s: Shape, b: Bool): Int {\n  case (s, b) {\n    (.Circle(_), True) -> 1\n    (.Rect{w, h}, _) -> 2\n    (.Empty, _) -> 3\n  }\n}",
			"non-exhaustive case on (Shape, Bool): missing (Circle(_), False)",
		},
		"list missing longer lists": {
			"fn f(xs: List<Int>): Int {\n  case xs {\n    [] -> 0\n    [a] -> a\n  }\n}",
			"non-exhaustive case on List<Int>: missing [_, _, ..]",
		},
		"list missing the empty list": {
			"fn f(xs: List<Int>): Int {\n  case xs {\n    [h, ..t] -> h\n  }\n}",
			"non-exhaustive case on List<Int>: missing []",
		},
		"list with a nested enum": {
			"fn f(xs: List<Maybe<Int>>): Int {\n  case xs {\n    [] -> 0\n    [Some(a), ..rest] -> a\n  }\n}",
			"non-exhaustive case on List<Maybe<Int>>: missing [None, ..]",
		},
		"struct with a literal field": {
			"fn f(p: Point): Int {\n  case p {\n    {x: 0, y} -> y\n  }\n}",
			"non-exhaustive case: add a `_` arm",
		},
		"struct with an enum field": {
			"fn f(s: Slot): Int {\n  case s {\n    {name, value: Some(v)} -> v\n  }\n}",
			"non-exhaustive case on Slot: missing Slot{value: None, ..}",
		},
		"distinct over an enum": {
			"fn f(w: Wrapped): Int {\n  case w {\n    Wrapped(Some(v)) -> v\n  }\n}",
			"non-exhaustive case on Wrapped: missing Wrapped(None)",
		},
		"map naming a key": {
			"fn f(m: Map<String, Int>): Int {\n  case m {\n    {\"a\" => v} -> v\n  }\n}",
			"non-exhaustive case: add a `_` arm",
		},
		"guarded tuple arm": {
			"fn f(a: Maybe<Int>, b: Maybe<Int>): Int {\n  case (a, b) {\n    (x, y) when x == y -> 0\n  }\n}",
			"non-exhaustive case: add a `_` arm",
		},
		"else arms over a tuple": {
			"fn f(p: (Maybe<Int>, Maybe<Int>)): Int {\n  (Some(a), Some(b)) = p else {\n    (None, _) -> return 0\n  }\n  a + b\n}",
			"non-exhaustive `else` on (Maybe<Int>, Maybe<Int>): missing (Some(_), None)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(compoundDecls + tc.body)
			got := nonExhaustive(errs)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("got coverage errors %q, want exactly %q\nall errors: %v", got, tc.want, errs)
			}
		})
	}
}

func TestCaseExhaustive_CompoundScrutineesAccepted(t *testing.T) {
	for name, body := range map[string]string{
		"tuple of Maybes, every pair":  "fn f(a: Maybe<Int>, b: Maybe<Int>): Int {\n  case (a, b) {\n    (Some(x), Some(y)) -> x + y\n    (Some(x), None) -> x\n    (None, Some(y)) -> y\n    (None, None) -> 0\n  }\n}",
		"tuple with a wildcard column": "fn f(a: Maybe<Int>, b: Maybe<Int>): Int {\n  case (a, b) {\n    (Some(x), _) -> x\n    (None, Some(y)) -> y\n    (None, None) -> 0\n  }\n}",
		"tuple of literals and _":      "fn f(n: Int): String {\n  case (n, n) {\n    (0, 0) -> \"zero\"\n    _ -> \"other\"\n  }\n}",
		"tuple literal with binding":   "fn f(n: Int, b: Bool): Int {\n  case (n, b) {\n    (0, _) -> 0\n    (m, True) -> m\n    (_, False) -> -1\n  }\n}",
		"nested Result in a tuple":     "fn f(a: Result<Int, String>, b: Bool): Int {\n  case (a, b) {\n    (Ok(n), _) -> n\n    (Err(_), True) -> 1\n    (Err(_), False) -> 2\n  }\n}",
		"list by length":               "fn f(xs: List<Int>): Int {\n  case xs {\n    [] -> 0\n    [a] -> a\n    [a, b, ..rest] -> a + b\n  }\n}",
		"list head and tail":           "fn f(xs: List<Int>): Int {\n  case xs {\n    [] -> 0\n    [h, ..t] -> h\n  }\n}",
		"list of Maybes":               "fn f(xs: List<Maybe<Int>>): Int {\n  case xs {\n    [] -> 0\n    [Some(a), ..rest] -> a\n    [None, ..rest] -> 0\n  }\n}",
		"struct binding fields":        "fn f(p: Point): Int {\n  case p {\n    {x, y} -> x + y\n  }\n}",
		"struct with enum field":       "fn f(s: Slot): Int {\n  case s {\n    {name, value: Some(v)} -> v\n    {name, value: None} -> 0\n  }\n}",
		"struct literal field and _":   "fn f(p: Point): Int {\n  case p {\n    {x: 0, y} -> y\n    _ -> 0\n  }\n}",
		"distinct over an enum":        "fn f(w: Wrapped): Int {\n  case w {\n    Wrapped(Some(v)) -> v\n    Wrapped(None) -> 0\n  }\n}",
		"map naming a key and _":       "fn f(m: Map<String, Int>): Int {\n  case m {\n    {\"a\" => v} -> v\n    _ -> 0\n  }\n}",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(compoundDecls + body)
			if got := nonExhaustive(errs); len(got) != 0 {
				t.Fatalf("got coverage errors %q, want none", got)
			}
			expectNoStdlibErrors(t, errs)
		})
	}
}

// A binding pattern is irrefutable when the coverage rules say it is, so a
// tuple holding a single-variant enum needs no `else`.
func TestPatternBinding_TupleOfIrrefutablePartsNeedsNoElse(t *testing.T) {
	src := compoundDecls + "enum Only {\n  One(Int)\n}\nfn f(p: (Only, Int)): Int {\n  (.One(a), b) = p\n  a + b\n}"
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}
