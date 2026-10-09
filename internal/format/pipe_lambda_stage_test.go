package format

import "testing"

// A pipe stage that ends in a lambda's bare body is parenthesized when
// another stage follows it. The parser builds `x |> dbg || y` as the stages
// `|| y` and `dbg`; the formatter writes a decorating `dbg` as a stage of its
// own, and `x |> || y |> dbg` would read as one lambda whose body is
// `y |> dbg`. Logical or is `or`, so `||` here is a lambda with no
// parameters.
func TestFormat_LambdaStageBeforeAnotherStageKeepsItsEnd(t *testing.T) {
	cases := []struct{ src, want string }{
		{"{(0)|>dbg||0}\n", "{ 0 |> (|| 0) |> dbg }\n"},
		{"x |> dbg || y\n", "x |> (|| y) |> dbg\n"},
		{"x |> dbg || { y }\n", "x |> (|| y) |> dbg\n"},
		{"x |> dbg |v| v\n", "x |> (|v| v) |> dbg\n"},
		{"x |> dbg |v| v |> f\n", "x |> (|v| v |> f) |> dbg\n"},
		{"x |> dbg || y == 1\n", "x |> (|| y == 1) |> dbg\n"},
		{"x |> dbg -|| y\n", "x |> (-|| y) |> dbg\n"},
		{"x |> assert || y\n", "x |> (|| y) |> assert\n"},
		{"x |> refute || y\n", "x |> (|| y) |> refute\n"},
		{"x |> dbg try || y\n", "x |> try (|| y) |> dbg\n"},
		{"x |> try dbg || y\n", "x |> (|| y) |> dbg |> try\n"},
		{
			"x\n    // note\n    |> dbg || y\n",
			"x\n// note\n|> (|| y)\n|> dbg\n",
		},
		// The lambda's body takes in the stage on the next line.
		{
			"x\n    // note\n    |> dbg || y\n    |> f()\n",
			"x\n// note\n|> (||\n    y\n    |> f())\n|> dbg\n",
		},
		// The last stage needs no parentheses, and a decorating `try`
		// stays the stage's prefix.
		{"x |> try || y\n", "x |> try || y\n"},
		{"x |> f() |> || y\n", "x |> f() |> || y\n"},
		{"x |> dbg f() |> g()\n", "x |> f() |> dbg |> g()\n"},
		{"x |> dbg == 1\n", "x |> dbg == 1\n"},
		{"x |> dbg and b\n", "x |> dbg and b\n"},
		{"x |> dbg a or || y\n", "x |> a |> dbg or || y\n"},
	}
	for _, c := range cases {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}

// Parentheses around a stage that ends in a lambda's bare body do not
// change what the pipe means; around a call they still do.
func TestSameMeaning_LambdaStageParentheses(t *testing.T) {
	if err := SameMeaning("x |> dbg || y\n", "x |> (|| y) |> dbg\n"); err != nil {
		t.Errorf("parenthesizing a lambda stage: %v", err)
	}
	if err := SameMeaning("x |> f() |> dbg\n", "x |> (f()) |> dbg\n"); err == nil {
		t.Error("parenthesizing a call stage kept the meaning, want a change")
	}
}
