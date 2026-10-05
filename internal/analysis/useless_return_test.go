package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A `return` that leaves a body at its end changes nothing. Each source here
// is otherwise valid, so its only error is the one the case names; the test
// fails if the source is admitted or if anything else is reported.
func TestUselessReturn_Rejected(t *testing.T) {
	for _, tc := range []struct {
		name, src     string
		line, col     int
		endCol        int
		message, hint string
	}{
		{
			"bare return alone in a function body",
			"fn reset() {\n    return\n}\n",
			2, 5, 11,
			"this `return` does nothing: it is the last statement of `reset`",
			"remove it; if the function isn't written yet, write `todo` instead",
		},
		{
			"bare return with a comment after a Unit statement",
			"import std/io\n\nfn greet() {\n    io.print(\"hi\")\n    return // done\n}\n",
			5, 5, 11,
			"this `return` does nothing: it is the last statement of `greet`",
			"remove it",
		},
		{
			"bare return after a binding",
			"fn f() {\n    _ = 1\n    return\n}\n",
			3, 5, 11,
			"this `return` does nothing: it is the last statement of `f`",
			"remove it",
		},
		{
			"bare return ending a lambda",
			"import std/io\n\nfn f(xs: List<Int>) {\n    xs\n    |> Iter.each(|x| {\n        io.print(\"${x}\")\n        // done\n        return\n    })\n}\n",
			8, 9, 15,
			"this `return` does nothing: it is the last statement of this lambda",
			"remove it",
		},
		{
			"bare return alone in a tail branch",
			"import std/io\n\nfn f(x: Int) {\n    if x > 0 { return } else { io.print(\"x\") }\n}\n",
			4, 16, 22,
			"this `return` does nothing: it is the last statement of `f`",
			"remove it",
		},
		{
			"bare return as a tail case arm",
			"import std/io\n\nfn f(x: Int) {\n    case x {\n        0 -> return\n        _ -> io.print(\"x\")\n    }\n}\n",
			5, 14, 20,
			"this `return` does nothing: it is the last statement of `f`",
			"remove it",
		},
		{
			"bare return ending an impl function",
			"struct P {\n    x: Int\n}\n\nimpl P {\n    fn touch(p: P) {\n        _ = p\n        return\n    }\n}\n",
			8, 9, 15,
			"this `return` does nothing: it is the last statement of `touch`",
			"remove it",
		},
		{
			"bare return ending a test body",
			"test \"t\" {\n    assert 1 == 1\n    // done\n    return\n}\n",
			4, 5, 11,
			"this `return` does nothing: it is the last statement of test \"t\"",
			"remove it",
		},
		{
			"tail if whose only branch returns",
			"import std/io\n\nfn f(done: Bool) {\n    io.print(\"x\")\n    if done { return }\n}\n",
			5, 5, 7,
			"this `if` does nothing: every branch returns and nothing follows it",
			"remove the `if`; keep what it tests only if evaluating that has an effect you need",
		},
		{
			"tail if chain of returns and empty branches",
			"fn f(x: Int) {\n    if x > 0 {\n        return\n    } else if x < 0 {\n    } else {\n        return\n    }\n}\n",
			2, 5, 7,
			"this `if` does nothing: every branch returns and nothing follows it",
			"remove the `if`; keep what it tests only if evaluating that has an effect you need",
		},
		{
			"tail case whose arms return",
			"fn f(x: Int) {\n    case x {\n        0 -> return\n        _ -> {}\n    }\n}\n",
			2, 5, 9,
			"this `case` does nothing: every branch returns and nothing follows it",
			"remove the `case`; keep what it tests only if evaluating that has an effect you need",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := buildErrsWithStdlib(t, tc.src)
			if len(errs) != 1 {
				t.Fatalf("want exactly one error, got %d:\n  %s", len(errs), errorLines(errs))
			}
			e := errs[0]
			if e.Message != tc.message {
				t.Errorf("message:\n got %s\nwant %s", e.Message, tc.message)
			}
			if e.Line != tc.line || e.Col != tc.col || e.EndLine != tc.line || e.EndCol != tc.endCol {
				t.Errorf("span: got %d:%d-%d:%d, want %d:%d-%d:%d",
					e.Line, e.Col, e.EndLine, e.EndCol, tc.line, tc.col, tc.line, tc.endCol)
			}
			if len(e.Hints) != 1 || e.Hints[0] != tc.hint {
				t.Errorf("hints: got %q, want [%q]", e.Hints, tc.hint)
			}
		})
	}
}

// Returns that leave early, return a value, or make a non-Unit statement's
// body Unit do something, and an empty body has no return at all.
func TestUselessReturn_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"empty body", "fn reset() {}\n"},
		{"early return", "import std/io\n\nfn f(x: Int) {\n    if x > 0 { return }\n    io.print(\"x\")\n}\n"},
		{"early return in a lambda", "import std/io\n\nfn f(xs: List<Int>) {\n    xs\n    |> Iter.each(|x| {\n        if x > 0 { return }\n        io.print(\"x\")\n    })\n}\n"},
		{"tail value return", "fn f(x: Int): Int {\n    y = x + 1\n    return y\n}\n"},
		{"tail if returning values", "fn f(x: Int): Int {\n    if x > 0 { return 1 } else { return 2 }\n}\n"},
		{"tail if with work in a branch", "import std/io\n\nfn f(x: Int) {\n    if x > 0 {\n        io.print(\"x\")\n    }\n}\n"},
		{"bare return after dbg", "fn f() {\n    dbg 1\n    return\n}\n"},
		{"bare return after a dbg stage", "fn f() {\n    1 |> dbg\n\n    return\n}\n"},
		{"return in a binding else", "fn f(x: Maybe<Int>): Int {\n    n = x else { return 0 }\n    n\n}\n"},
		{"early return in a test body", "test \"t\" {\n    if 1 > 2 { return }\n    assert 1 == 1\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := buildErrsWithStdlib(t, tc.src); len(errs) != 0 {
				t.Fatalf("the front end rejects this:\n  %s", errorLines(errs))
			}
		})
	}
}

func errorLines(errs []analysis.TypeError) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n  ")
}
