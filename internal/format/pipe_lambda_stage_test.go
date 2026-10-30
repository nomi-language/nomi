package format

import "testing"

// A pipe stage that ends in a lambda's bare body is parenthesized when
// another stage follows it. The parser builds `x |> dbg fn -> y` as the
// stages `fn -> y` and `dbg`; the formatter writes a decorating `dbg` as a
// stage of its own, and `x |> fn -> y |> dbg` would read as one lambda whose
// body is `y |> dbg`.
func TestFormat_LambdaStageBeforeAnotherStageKeepsItsEnd(t *testing.T) {
	cases := []struct{ src, want string }{
		{"{(0)|>dbg fn -> 0}\n", "{ 0 |> (fn -> 0) |> dbg }\n"},
		{"x |> dbg fn -> y\n", "x |> (fn -> y) |> dbg\n"},
		{"x |> dbg fn -> { y }\n", "x |> (fn -> y) |> dbg\n"},
		{"x |> dbg fn v -> v\n", "x |> (fn v -> v) |> dbg\n"},
		{"x |> dbg fn v -> v |> f\n", "x |> (fn v -> v |> f) |> dbg\n"},
		{"x |> dbg fn -> y == 1\n", "x |> (fn -> y == 1) |> dbg\n"},
		{"x |> dbg -fn -> y\n", "x |> (-fn -> y) |> dbg\n"},
		{"x |> assert fn -> y\n", "x |> (fn -> y) |> assert\n"},
		{"x |> refute fn -> y\n", "x |> (fn -> y) |> refute\n"},
		{"x |> dbg (fn -> y)?\n", "x |> (fn -> y)? |> dbg\n"},
		{
			"x\n    // note\n    |> dbg fn -> y\n",
			"x\n// note\n|> (fn -> y)\n|> dbg\n",
		},
		// The lambda's body takes in the stage on the next line.
		{
			"x\n    // note\n    |> dbg fn -> y\n    |> f()\n",
			"x\n// note\n|> (fn -> {\n    y\n    |> f()\n})\n|> dbg\n",
		},
		// The last stage needs no parentheses; one a `?` follows does.
		{"x |> (fn -> y)?\n", "x |> (fn -> y)?\n"},
		{"x |> f() |> fn -> y\n", "x |> f() |> fn -> y\n"},
		{"x |> dbg f() |> g()\n", "x |> f() |> dbg |> g()\n"},
		{"x |> dbg == 1\n", "x |> dbg == 1\n"},
		{"x |> dbg and b\n", "x |> dbg and b\n"},
		{"x |> dbg a or fn -> y\n", "x |> a |> dbg or fn -> y\n"},
	}
	for _, c := range cases {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}

// Parentheses around a stage that ends in a lambda's bare body do not
// change what the pipe means; around a call they still do.
func TestSameMeaning_LambdaStageParentheses(t *testing.T) {
	if err := SameMeaning("x |> dbg fn -> y\n", "x |> (fn -> y) |> dbg\n"); err != nil {
		t.Errorf("parenthesizing a lambda stage: %v", err)
	}
	if err := SameMeaning("x |> f() |> dbg\n", "x |> (f()) |> dbg\n"); err == nil {
		t.Error("parenthesizing a call stage kept the meaning, want a change")
	}
}
