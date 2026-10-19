package analysis_test

import "testing"

const discardValueMsg = "`_` is a discard name, not a value\nhelp: a capture's parameter is written `&1`, as in `Iter.map(xs, &(&1 * 2))`"

const placeholderDecls = `struct Point {
    x: Int
    y: Int
}

enum E {
    A
    B
}

fn add(a: Int, b: Int) -> Int { a + b }

fn connect(host: String, port: Int = 80) -> String { "${host}:${port}" }

`

// `_` is a discard name: read as a value anywhere it is the error that says
// a capture's parameter is written `&1`.
func TestDiscard_ReadAsAValueIsRejected(t *testing.T) {
	for name, body := range map[string]string{
		"case arm body":         "fn f(e: E) -> Int {\n    case e {\n        .A -> _\n        .B -> 1\n    }\n}\n",
		"function body":         "fn f() -> Int { _ }\n",
		"binding value":         "fn f() -> Int {\n    x = _\n    x\n}\n",
		"list element":          "fn f() -> List<Int> { [1, _] }\n",
		"lambda body":           "fn f() -> (Int) -> Int { fn _n: Int -> _ }\n",
		"if branch":             "fn f(b: Bool) -> Int { if b { _ } else { 2 } }\n",
		"parenthesized operand": "fn f() -> (Int) -> Int { (_) + 1 }\n",
		"string interpolation":  "fn f() -> (Int) -> String { \"${_}\" }\n",
		"set element":           "fn f() -> (Int) -> Set<Int> { %[_] }\n",
		"vector element":        "fn f() -> (Int) -> Vector<Int> { #[_] }\n",
		"map value":             "fn f() -> (Int) -> Map<String, Int> { %{\"k\" => _} }\n",
		"struct spread":         "fn f(p: Point) -> Point { #{.._, x: 1} }\n",
		"pipe operand":          "fn f() -> (Int) -> Int { _ |> add(1) }\n",
		"operator operand":      "fn f() -> (Int) -> Bool { _ > 2 }\n",
		"struct field":          "fn f() -> (Int) -> Point { #Point{x: _, y: 0} }\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(placeholderDecls + body)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, diagText(e))
				if diagText(e) == discardValueMsg {
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
		{"a partial application", "fn f() -> (Int) -> Int { add(1, _) }\n",
			"`_` is a discard name, not a value\nhelp: a capture's parameter is written `&1`: `&add(1, &1)`"},
		{"two placeholders", "fn f() -> (String, Int) -> String { connect(_, port: _) }\n",
			"`_` is a discard name, not a value\nhelp: number the capture's parameters `&1`, `&2`, ... in the order they are written: `&connect(&1, port: &2)`"},
		{"a variant constructor", "fn f() -> (Int) -> Maybe<Int> { Some(_) }\n",
			"`_` is a discard name, not a value\nhelp: a capture's parameter is written `&1`: `&Some(&1)`"},
		{"a pipe stage", "fn f() -> Int { 10 |> add(100, _) }\n",
			"`_` is a discard name, not a value\nhelp: a pipe fills the first argument; write `|> then &add(100, &1)`"},
		{"a pipe stage with two", "fn f() -> Int { 10 |> add(_, _) }\n",
			"`_` is a discard name, not a value\nhelp: a pipe fills the first argument; write `|> then &add(&1, &1)`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(placeholderDecls + tc.body)
			var msgs []string
			hits := 0
			for _, e := range errs {
				msgs = append(msgs, diagText(e))
				if diagText(e) == tc.want {
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
		"partial application":     "fn f() -> Int {\n    inc = &add(1, &1)\n    inc(2)\n}\n",
		"named partial":           "fn f() -> Int {\n    inc = &add(a: 1, b: &1)\n    inc(2)\n}\n",
		"variant ctor partial":    "fn f() -> Maybe<Int> {\n    wrap: (Int) -> Maybe<Int> = &Some(&1)\n    wrap(1)\n}\n",
		"pipe slot":               "fn f() -> Int { 10 |> then &add(100, &1) }\n",
		"pipe slot twice":         "fn f() -> Int { 10 |> then &add(&1, &1) }\n",
		"discard binding":         "fn f() -> Int {\n    _ = add(1, 2)\n    3\n}\n",
		"discard parameter":       "fn f(_: Int) -> Int { 1 }\n",
		"named discard parameter": "fn f(_n: Int) -> Int { 1 }\n",
		"lambda discard param":    "fn f() -> Int {\n    g = fn _: Int -> 3\n    g(0)\n}\n",
		"lambda tuple param":      "fn f() -> List<Int> { [#(1, 2)] |> Iter.map(fn #(_, v) -> v) |> Iter.to_list() }\n",
		"tuple pattern":           "fn f() -> Int {\n    #(a, _) = #(1, 2)\n    a\n}\n",
		"struct pattern":          "fn f(p: Point) -> Int {\n    #Point{x, y: _} = p\n    x\n}\n",
		"list pattern":            "fn f(xs: List<Int>) -> Int {\n    case xs {\n        [_, b, .._] -> b\n        _ -> 0\n    }\n}\n",
		"case wildcard":           "fn f(e: E) -> Int {\n    case e {\n        .A -> 1\n        _ -> 2\n    }\n}\n",
		"variant pattern":         "fn f(m: Maybe<Int>) -> Int {\n    case m {\n        Some(_) -> 1\n        None -> 0\n    }\n}\n",
		"binding else":            "fn f(m: Maybe<Int>) -> Int {\n    Some(_) = m else { return 0 }\n    1\n}\n",
		"subject-less catch-all":  "fn f(n: Int) -> Int {\n    case {\n        n > 3 -> 1\n        _ -> 2\n    }\n}\n",
		"numeric separator":       "fn f() -> Int { 1_000 }\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(placeholderDecls + body)
			for _, e := range errs {
				t.Errorf("unexpected error %q", diagText(e))
			}
		})
	}
}
