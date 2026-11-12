package analysis_test

import (
	"strings"
	"testing"
)

const discardValueMsg = "`_` is a discard name, not a value\nhelp: a capture's parameter is written `&1`, as in `Iter.map(xs, &(&1 * 2))`"

const placeholderDecls = `struct Point
    x: Int
    y: Int
end

enum E
    A
    B
end

fn add(a: Int, b: Int) -> Int
    a + b
end

fn connect(host: String, port: Int = 80) -> String
    "${host}:${port}"
end

`

// `_` is a discard name: read as a value anywhere it is the error that says
// a capture's parameter is written `&1`.
func TestDiscard_ReadAsAValueIsRejected(t *testing.T) {
	for name, body := range map[string]string{
		"case arm body":         "fn f(e: E) -> Int\n    case e\n        .A -> _\n        .B -> 1\n    end\nend\n",
		"function body":         "fn f() -> Int\n    _\nend\n",
		"binding value":         "fn f() -> Int\n    x = _\n    x\nend\n",
		"list element":          "fn f() -> List<Int>\n    [1, _]\nend\n",
		"lambda body":           "fn f() -> (Int) -> Int\n    fn _n: Int -> _\nend\n",
		"if branch":             "fn f(b: Bool) -> Int\n    if b then _ else 2\nend\n",
		"parenthesized operand": "fn f() -> (Int) -> Int\n    (_) + 1\nend\n",
		"string interpolation":  "fn f() -> (Int) -> String\n    \"${_}\"\nend\n",
		"set element":           "fn f() -> (Int) -> Set<Int>\n    Set[_]\nend\n",
		"vector element":        "fn f() -> (Int) -> Vector<Int>\n    Vector[_]\nend\n",
		"map value":             "fn f() -> (Int) -> Map<String, Int>\n    {\"k\" => _}\nend\n",
		"struct spread":         "fn f(p: Point) -> Point\n    {.._, x: 1}\nend\n",
		"pipe operand":          "fn f() -> (Int) -> Int\n    _ |> add(1)\nend\n",
		"operator operand":      "fn f() -> (Int) -> Bool\n    _ > 2\nend\n",
		"struct field":          "fn f() -> (Int) -> Point\n    Point{x: _, y: 0}\nend\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(placeholderDecls + body)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, diagText(e))
				// A typed hole's help may follow (holes.go).
				if strings.HasPrefix(diagText(e), discardValueMsg) {
					return
				}
			}
			t.Fatalf("errors = %q, want one to be %q", msgs, discardValueMsg)
		})
	}
}

// Each spelling of the `_` placeholder a call used to write is the error,
// once per call, and its help writes the call as a capture with its
// parameters numbered; a pipe stage's is a `then` stage, whose capture
// takes the piped value as each `&1`.
func TestDiscard_TheOldPlaceholderSpellingsSayToNumber(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"a partial application", "fn f() -> (Int) -> Int\n    add(1, _)\nend\n",
			"`_` is a discard name, not a value\nhelp: a capture's parameter is written `&1`: `&add(1, &1)`"},
		{"two placeholders", "fn f() -> (String, Int) -> String\n    connect(_, port: _)\nend\n",
			"`_` is a discard name, not a value\nhelp: number the capture's parameters `&1`, `&2`, ... in the order they are written: `&connect(&1, port: &2)`"},
		{"a variant constructor", "fn f() -> (Int) -> Maybe<Int>\n    Some(_)\nend\n",
			"`_` is a discard name, not a value\nhelp: a capture's parameter is written `&1`: `&Some(&1)`"},
		{"a pipe stage", "fn f() -> Int\n    10 |> add(100, _)\nend\n",
			"`_` is a discard name, not a value\nhelp: a pipe fills the first argument; write `|> then &add(100, &1)`"},
		{"a pipe stage with two", "fn f() -> Int\n    10 |> add(_, _)\nend\n",
			"`_` is a discard name, not a value\nhelp: a pipe fills the first argument; write `|> then &add(&1, &1)`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(placeholderDecls + tc.body)
			var msgs []string
			hits := 0
			for _, e := range errs {
				msgs = append(msgs, diagText(e))
				// A typed hole's help may follow (holes.go).
				if diagText(e) == tc.want || strings.HasPrefix(diagText(e), tc.want+"\nhelp: as a hole") {
					hits++
				}
			}
			if hits != 1 {
				t.Fatalf("errors = %q, want exactly one %q", msgs, tc.want)
			}
		})
	}
}

// Every legal `_` and `&N`: a partial capture (positional, named, of a
// variant constructor), a `then` stage's capture, and `_` as a discard in
// each pattern and parameter position.
func TestPlaceholder_LegalPositionsAreAccepted(t *testing.T) {
	for name, body := range map[string]string{
		"partial application":     "fn f() -> Int\n    inc = &add(1, &1)\n    inc(2)\nend\n",
		"named partial":           "fn f() -> Int\n    inc = &add(a: 1, b: &1)\n    inc(2)\nend\n",
		"variant ctor partial":    "fn f() -> Maybe<Int>\n    wrap: (Int) -> Maybe<Int> = &Some(&1)\n    wrap(1)\nend\n",
		"pipe slot":               "fn f() -> Int\n    10 |> then &add(100, &1)\nend\n",
		"pipe slot twice":         "fn f() -> Int\n    10 |> then &add(&1, &1)\nend\n",
		"discard binding":         "fn f() -> Int\n    _ = add(1, 2)\n    3\nend\n",
		"discard parameter":       "fn f(_: Int) -> Int\n    1\nend\n",
		"named discard parameter": "fn f(_n: Int) -> Int\n    1\nend\n",
		"lambda discard param":    "fn f() -> Int\n    g = fn _: Int -> 3\n    g(0)\nend\n",
		"lambda tuple param":      "fn f() -> List<Int>\n    [(1, 2)] |> Iter.map(fn (_, v) -> v) |> Iter.to_list()\nend\n",
		"tuple pattern":           "fn f() -> Int\n    (a, _) = (1, 2)\n    a\nend\n",
		"struct pattern":          "fn f(p: Point) -> Int\n    Point{x:, y: _} = p\n    x\nend\n",
		"list pattern":            "fn f(xs: List<Int>) -> Int\n    case xs\n        [_, b, ..] -> b\n        _ -> 0\n    end\nend\n",
		"case wildcard":           "fn f(e: E) -> Int\n    case e\n        .A -> 1\n        _ -> 2\n    end\nend\n",
		"variant pattern":         "fn f(m: Maybe<Int>) -> Int\n    case m\n        Some(_) -> 1\n        None -> 0\n    end\nend\n",
		"binding else":            "fn f(m: Maybe<Int>) -> Int\n    Some(_) = m else\n        return 0\n    end\n    1\nend\n",
		"subject-less catch-all":  "fn f(n: Int) -> Int\n    case\n        n > 3 -> 1\n        _ -> 2\n    end\nend\n",
		"numeric separator":       "fn f() -> Int\n    1_000\nend\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(placeholderDecls + body)
			for _, e := range errs {
				t.Errorf("unexpected error %q", diagText(e))
			}
		})
	}
}
