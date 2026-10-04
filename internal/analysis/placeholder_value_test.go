package analysis_test

import "testing"

const placeholderValueMsg = "`_` has no value here\nhelp: `_` stands for a missing argument only in a call, as in add(1, _)"

const placeholderDecls = `struct Point {
    x: Int
    y: Int
}

enum E {
    A
    B
}

fn add(a: Int, b: Int): Int { a + b }

`

// `_` read as a value anywhere but a call's argument list has no value.
func TestPlaceholder_OutsideACallIsRejected(t *testing.T) {
	for name, body := range map[string]string{
		"struct field value":                  "fn f(): Point { Point{x: _, y: 1} }\n",
		"case arm body":                       "fn f(e: E): Int {\n    case e {\n        .A -> _\n        .B -> 1\n    }\n}\n",
		"function body":                       "fn f(): Int { _ }\n",
		"binding value":                       "fn f(): Int {\n    x = _\n    x\n}\n",
		"list element":                        "fn f(): List<Int> { [1, _] }\n",
		"tuple element":                       "fn f(): (Int, Int) { (1, _) }\n",
		"operator operand":                    "fn f(): Int { 1 + _ }\n",
		"lambda body":                         "fn f(): (Int) -> Int { |_n: Int| _ }\n",
		"if branch":                           "fn f(b: Bool): Int { if b { _ } else { 2 } }\n",
		"inside a call's argument expression": "fn f(): Int { add(1 + _, 2) }\n",
	} {
		_, errs := checkSourceWithStdlib(placeholderDecls + body)
		var msgs []string
		hits := 0
		for _, e := range errs {
			msgs = append(msgs, diagText(e))
			if diagText(e) == placeholderValueMsg {
				hits++
			}
		}
		if hits == 0 {
			t.Errorf("%s: got %d %q errors, want at least 1; all errors: %q", name, hits, placeholderValueMsg, msgs)
		}
	}
}

// Every legal `_`: partial application (positional, named, of a variant
// constructor), the slot a pipe fills, discard bindings and parameters,
// patterns, and the subject-less case's catch-all arm.
func TestPlaceholder_LegalPositionsAreAccepted(t *testing.T) {
	for name, body := range map[string]string{
		"partial application":    "fn f(): Int {\n    inc = add(1, _)\n    inc(2)\n}\n",
		"named partial":          "fn f(): Int {\n    inc = add(a: 1, b: _)\n    inc(2)\n}\n",
		"variant ctor partial":   "fn f(): Maybe<Int> {\n    wrap: (Int) -> Maybe<Int> = Some(_)\n    wrap(1)\n}\n",
		"pipe slot":              "fn f(): Int { 10 |> add(100, _) }\n",
		"discard binding":        "fn f(): Int {\n    _ = add(1, 2)\n    3\n}\n",
		"discard parameter":      "fn f(_: Int): Int { 1 }\n",
		"lambda discard param":   "fn f(): Int {\n    g = |_: Int| 3\n    g(0)\n}\n",
		"tuple pattern":          "fn f(): Int {\n    (a, _) = (1, 2)\n    a\n}\n",
		"case wildcard":          "fn f(e: E): Int {\n    case e {\n        .A -> 1\n        _ -> 2\n    }\n}\n",
		"variant pattern":        "fn f(m: Maybe<Int>): Int {\n    case m {\n        Some(_) -> 1\n        None -> 0\n    }\n}\n",
		"subject-less catch-all": "fn f(n: Int): Int {\n    case {\n        n > 3 -> 1\n        _ -> 2\n    }\n}\n",
		"numeric separator":      "fn f(): Int { 1_000 }\n",
	} {
		_, errs := checkSourceWithStdlib(placeholderDecls + body)
		for _, e := range errs {
			t.Errorf("%s: unexpected error %q", name, e.Message)
		}
	}
}
