package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An early exit written directly in an expression no function encloses (a
// `once` initializer, a parameter default, a field default) has nothing to
// exit, and is an error at the exit.
func TestExitless_EarlyExitWithNoFunctionIsRejected(t *testing.T) {
	cases := []struct {
		name, src string
		line, col int
		msg, hint string
	}{
		{
			name: "try in a once",
			src: `once n = try Ok(3)

fn f(): Result<Int, String> {
    Ok(n)
}`,
			line: 1, col: 10,
			msg:  "`try` cannot be used in the initializer of `once n`: a `once` has no function to return from",
			hint: "bind the Result or Maybe and handle it where `n` is read, or compute the value inside a function",
		},
		{
			// Read from a function whose own return type would admit the
			// `try`: the once is checked from its read, and the reader's
			// boundary must not count.
			name: "try stage in a once read above it",
			src: `fn f(): Maybe<Int> {
    Some(n)
}

once n = Some(3) |> try`,
			line: 5, col: 21,
			msg: "`try` cannot be used in the initializer of `once n`",
		},
		{
			name: "try in a once block",
			src: `once n = {
    x = if True { try Some(3) } else { 4 }
    x + 1
}`,
			line: 2, col: 19,
			msg: "`try` cannot be used in the initializer of `once n`",
		},
		{
			name: "return in a once",
			src: `once n = {
    if True {
        return 3
    }
    4
}`,
			line: 3, col: 9,
			msg:  "`return` cannot be used in the initializer of `once n`: a `once` has no function to return from",
			hint: "give each branch of an `if` or `case` its own value instead of exiting",
		},
		{
			name: "break in a once",
			src: `once n = {
    if True {
        break
    }
    4
}`,
			line: 3, col: 9,
			msg: "`break` cannot be used in the initializer of `once n`",
		},
		{
			name: "continue in a once",
			src: `once n = {
    if True {
        continue
    }
    4
}`,
			line: 3, col: 9,
			msg: "`continue` cannot be used in the initializer of `once n`",
		},
		{
			name: "assert in a once",
			src: `once n = {
    assert 1 == 1
    4
}`,
			line: 2, col: 5,
			msg:  "`assert` cannot be used in the initializer of `once n`",
			hint: "move the `assert` into a function that returns a Result",
		},
		{
			name: "try in an owner-level once",
			src: `struct P {
    x: Int
}

impl P {
    pub once zero: P = P{x: try Some(0)}
}`,
			line: 6, col: 29,
			msg: "`try` cannot be used in the initializer of `once zero`",
		},
		{
			name: "try in a fn parameter default",
			src: `fn f(x: Int = try Some(3)): Maybe<Int> {
    Some(x)
}`,
			line: 1, col: 15,
			msg:  "`try` cannot be used in the default of parameter 'x': a default has no function to return from",
			hint: "handle the Result or Maybe inside the default with `case`, or compute the value inside the function",
		},
		{
			name: "return in a fn parameter default",
			src: `fn f(x: Int = {
    if True {
        return 3
    }
    4
}): Int {
    x
}`,
			line: 3, col: 9,
			msg: "`return` cannot be used in the default of parameter 'x'",
		},
		{
			name: "try in a lambda parameter default",
			src: `fn f(): Maybe<Int> {
    g = |x: Int = try Some(3)| x + 1
    Some(g())
}`,
			line: 2, col: 19,
			msg: "`try` cannot be used in the default of parameter 'x'",
		},
		{
			name: "try in an unannotated lambda parameter default",
			src: `fn f(): Maybe<Int> {
    g = |x = try Some(3)| x + 1
    Some(g())
}`,
			line: 2, col: 14,
			msg: "`try` cannot be used in the default of parameter 'x'",
		},
		{
			name: "try in a struct field default",
			src: `struct P {
    x: Int = try Some(3)
}`,
			line: 2, col: 14,
			msg:  "`try` cannot be used in the default of field 'x' of P: a default has no function to return from",
			hint: "handle the Result or Maybe inside the default with `case`, or compute the value in a function and pass the field",
		},
		{
			name: "return in a variant field default",
			src: `enum Shape {
    Circle {radius: Int = {
        if True {
            return 1
        }
        2
    }}
}`,
			line: 4, col: 13,
			msg: "`return` cannot be used in the default of field 'radius' of Shape.Circle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := analyzeConcurrentRaw(tc.src)
			var got *analysis.TypeError
			for i := range errs {
				if strings.Contains(errs[i].Message, tc.msg) {
					got = &errs[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("the checker admits this source, or rejects it for another reason; want %q, got:\n%s",
					tc.msg, errorList(errs))
			}
			if got.Line != tc.line || got.Col != tc.col {
				t.Errorf("reported at %d:%d, want the exit at %d:%d", got.Line, got.Col, tc.line, tc.col)
			}
			if tc.hint != "" && (len(got.Hints) != 1 || got.Hints[0] != tc.hint) {
				t.Errorf("hints = %q, want [%q]", got.Hints, tc.hint)
			}
			if len(errs) != 1 {
				t.Errorf("want only the exit reported, got:\n%s", errorList(errs))
			}
		})
	}
}

// A lambda, a nested `fn` and a `concurrent` block inside such an expression
// are boundaries of their own: an exit there leaves that boundary.
func TestExitless_ExitInsideANestedBoundaryIsAccepted(t *testing.T) {
	cases := []struct{ name, src string }{
		{"try in a lambda in a once", `once f = |x: Result<Int, String>| {
    v = try x
    Ok(v + 1)
}`},
		{"return in a lambda in a once", `once k = |x: Int| {
    if x > 0 {
        return 1
    }
    0
}`},
		{"try in a nested fn in a once", `once g: Maybe<Int> = {
    fn h(x: Maybe<Int>): Maybe<Int> {
        Some(try x)
    }
    h(Some(4))
}`},
		{"try in a concurrent block in a once", `once c: Result<Int, String> = concurrent {
    v = try Ok(3)
    Ok(v)
}`},
		{"continue in an iteration callback in a once", `once total = [1, 2, 3] |> Iter.map(|x| {
    if x == 2 {
        continue
    }
    x
}) |> Iter.to_list()`},
		{"try in a lambda in a parameter default", `fn f(g: (Maybe<Int>) -> Maybe<Int> = |m| Some(try m + 1)): Maybe<Int> {
    g(Some(1))
}`},
		{"try in a lambda in a field default", `struct P {
    g: (Maybe<Int>) -> Maybe<Int> = |m| Some(try m + 1)
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if errs := analyzeConcurrentRaw(tc.src); len(errs) != 0 {
				t.Fatalf("the checker rejects this source:\n%s\n%s", tc.src, errorList(errs))
			}
		})
	}
}

func errorList(errs []analysis.TypeError) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString("  " + e.Error() + "\n")
	}
	return b.String()
}
