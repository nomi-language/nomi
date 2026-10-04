package format

import (
	"strings"
	"testing"
)

// A statement that spans more than one line has a blank line before it,
// unless it opens its block, and after it, unless it closes its block. A
// block's final expression is a statement for this rule. One-line statements
// sit together, keeping a single blank line the source wrote between them.
func TestFormat_StatementSpacingSetsMultiLineStatementsApart(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"a binding then a multi-line if": {
			src: `fn pad(generation: Int): String {
    text = "${generation}"
    width = String.length(text)
    if width >= 3 {
        text
    } else {
        String.repeat(" ", 3 - width) + text
    }
}
`,
			want: `fn pad(generation: Int): String {
    text = "${generation}"
    width = String.length(text)

    if width >= 3 {
        text
    } else {
        String.repeat(" ", 3 - width) + text
    }
}
`,
		},
		"a multi-line statement in the middle": {
			src: `fn f(xs: List<Int>): Int {
    first = 1
    total = case xs {
        [] -> 0
        _ -> 1
    }
    total + first
}
`,
			want: `fn f(xs: List<Int>): Int {
    first = 1

    total = case xs {
        [] -> 0
        _ -> 1
    }

    total + first
}
`,
		},
		"multi-line first and last statements get no blank line at the block's edges": {
			src: `fn f(x: Int): Int {

    y = case x {
        0 -> 1
        _ -> x
    }
    z = y + 1
    case z {
        1 -> 0
        _ -> z
    }

}
`,
			want: `fn f(x: Int): Int {
    y = case x {
        0 -> 1
        _ -> x
    }

    z = y + 1

    case z {
        1 -> 0
        _ -> z
    }
}
`,
		},
		"one-line statements stay together": {
			src: `fn f(): Int {
    a = 1
    b = 2
    c = a + b
    c
}
`,
			want: `fn f(): Int {
    a = 1
    b = 2
    c = a + b
    c
}
`,
		},
		"a blank line written between one-line statements is kept": {
			src: `fn f(): Int {
    a = 1


    b = 2
    a + b
}
`,
			want: `fn f(): Int {
    a = 1

    b = 2
    a + b
}
`,
		},
		"two multi-line statements share one blank line": {
			src: `fn f(x: Int): Int {
    a = case x {
        0 -> 1
        _ -> 2
    }
    b = case x {
        1 -> 3
        _ -> 4
    }
    a + b
}
`,
			want: `fn f(x: Int): Int {
    a = case x {
        0 -> 1
        _ -> 2
    }

    b = case x {
        1 -> 3
        _ -> 4
    }

    a + b
}
`,
		},
		"the blank line goes above a comment on the multi-line statement": {
			src: `fn f(x: Int): Int {
    a = 1
    // pick one
    b = case x {
        0 -> a
        _ -> 2
    }
    b
}
`,
			want: `fn f(x: Int): Int {
    a = 1

    // pick one
    b = case x {
        0 -> a
        _ -> 2
    }

    b
}
`,
		},
		"nested blocks follow the rule at their own indent": {
			src: `fn f(x: Int): Int {
    if x > 0 {
        y = x + 1
        z = case y {
            1 -> 0
            _ -> y
        }
        z
    } else {
        0
    }
}
`,
			want: `fn f(x: Int): Int {
    if x > 0 {
        y = x + 1

        z = case y {
            1 -> 0
            _ -> y
        }

        z
    } else {
        0
    }
}
`,
		},
		"a lambda block body follows the rule": {
			src: `fn f(xs: List<Int>): Int {
    Iter.reduce(xs, |acc = 0, x| {
        y = x * 2
        z = case y {
            4 -> y
            _ -> 0
        }
        acc + z
    })
}
`,
			want: `fn f(xs: List<Int>): Int {
    Iter.reduce(xs, |acc = 0, x| {
        y = x * 2

        z = case y {
            4 -> y
            _ -> 0
        }

        acc + z
    })
}
`,
		},
		"a test body follows the rule": {
			src: `test "spacing" {
    a = 1
    assert a == 1
    b = case a {
        1 -> 2
        _ -> 3
    }
    assert b == 2
}
`,
			want: `test "spacing" {
    a = 1
    assert a == 1

    b = case a {
        1 -> 2
        _ -> 3
    }

    assert b == 2
}
`,
		},
		"a test group's tests are set apart": {
			src: `tests "group" {
    clock Clock.Virtual

    test "one" {
        assert True
    }
    test "two" {
        assert True
    }
}
`,
			want: `tests "group" {
    clock Clock.Virtual

    test "one" {
        assert True
    }

    test "two" {
        assert True
    }
}
`,
		},
		"an attached test follows the rule": {
			src: `//! label = case 1 {
//!     1 -> "one"
//!     _ -> "many"
//! }
//! assert label == "one"
fn f(): Int {
    1
}
`,
			want: `//! label = case 1 {
//!     1 -> "one"
//!     _ -> "many"
//! }
//!
//! assert label == "one"
fn f(): Int {
    1
}
`,
		},
		"a short if that stays on one line is a one-line statement": {
			src: `fn f(n: Int): Int {
    if n > 1 { return 0 }
    n
}
`,
			want: `fn f(n: Int): Int {
    if n > 1 { return 0 }
    n
}
`,
		},
		"an over-long line with nowhere to break is a one-line statement": {
			src: `fn f(): String {
    a = "a string literal long enough that this binding runs past the line width of one hundred"
    a
}
`,
			want: `fn f(): String {
    a = "a string literal long enough that this binding runs past the line width of one hundred"
    a
}
`,
		},
		"a statement written across lines that fits is laid out on one line": {
			src: `fn f(): Int {
    a = 1
    b = Int.max(
        a,
        2,
    )
    b
}
`,
			want: `fn f(): Int {
    a = 1
    b = Int.max(a, 2)
    b
}
`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			formatsTo(t, c.src, c.want)
		})
	}
}

// Whether a statement spans lines is decided where its block is laid out:
// the same call stays on one line in a shallow block and breaks in a deep
// one, and only the deep one is set apart.
func TestFormat_StatementSpacingMeasuresFromTheBlockIndent(t *testing.T) {
	// 91 columns: it fits at indent 4 and 8, and breaks at indent 12.
	call := "total = Int.max(first_value_with_a_long_name, second_value_with_a_long_name, third_value_n)"
	if len(call) != 91 {
		t.Fatalf("the call is %d columns, want 91", len(call))
	}
	shallow := "fn f(): Int {\n    a = 1\n    " + call + "\n    total\n}\n"
	formatsTo(t, shallow, shallow)

	deep := "fn f(x: Int): Int {\n    if x > 0 {\n        if x > 1 {\n            a = 1\n            " + call + "\n            total\n        } else {\n            0\n        }\n    } else {\n        0\n    }\n}\n"
	broken := strings.Join([]string{
		"            total = Int.max(",
		"                first_value_with_a_long_name,",
		"                second_value_with_a_long_name,",
		"                third_value_n,",
		"            )",
	}, "\n")
	want := "fn f(x: Int): Int {\n    if x > 0 {\n        if x > 1 {\n            a = 1\n\n" + broken + "\n\n            total\n        } else {\n            0\n        }\n    } else {\n        0\n    }\n}\n"
	formatsTo(t, deep, want)
}

// The pipe, assertion and import rules still hold: a blank line follows every
// pipe statement, inline or stacked, and a block's imports.
func TestFormat_StatementSpacingKeepsThePipeAndImportRules(t *testing.T) {
	src := `fn f(xs: List<Int>): Int {
    import std/io
    n = xs |> Iter.count()
    m = n + 1
    io.print(m)
    m
}
`
	want := `fn f(xs: List<Int>): Int {
    import std/io

    n = xs |> Iter.count()

    m = n + 1
    io.print(m)
    m
}
`
	formatsTo(t, src, want)
}

func formatsTo(t *testing.T, src, want string) {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	again, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatalf("not idempotent:\n%s\nthen:\n%s", got, again)
	}
}
