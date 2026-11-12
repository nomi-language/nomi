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
			src: `fn pad(generation: Int) -> String
    text = "${generation}"
    width = String.length(text)
    if width >= 3 then
        text
    else
        String.repeat(" ", 3 - width) + text
    end
end
`,
			want: `fn pad(generation: Int) -> String
    text = "${generation}"
    width = String.length(text)

    if width >= 3 then
        text
    else
        String.repeat(" ", 3 - width) + text
    end
end
`,
		},
		"a multi-line statement in the middle": {
			src: `fn f(xs: List<Int>) -> Int
    first = 1
    total = case xs
        [] -> 0
        _ -> 1
    end
    total + first
end
`,
			want: `fn f(xs: List<Int>) -> Int
    first = 1

    total = case xs
        [] -> 0
        _ -> 1
    end

    total + first
end
`,
		},
		"multi-line first and last statements get no blank line at the block's edges": {
			src: `fn f(x: Int) -> Int

    y = case x
        0 -> 1
        _ -> x
    end
    z = y + 1
    case z
        1 -> 0
        _ -> z
    end

end
`,
			want: `fn f(x: Int) -> Int
    y = case x
        0 -> 1
        _ -> x
    end

    z = y + 1

    case z
        1 -> 0
        _ -> z
    end
end
`,
		},
		"one-line statements stay together": {
			src: `fn f() -> Int
    a = 1
    b = 2
    c = a + b
    c
end
`,
			want: `fn f() -> Int
    a = 1
    b = 2
    c = a + b
    c
end
`,
		},
		"a blank line written between one-line statements is kept": {
			src: `fn f() -> Int
    a = 1


    b = 2
    a + b
end
`,
			want: `fn f() -> Int
    a = 1

    b = 2
    a + b
end
`,
		},
		"two multi-line statements share one blank line": {
			src: `fn f(x: Int) -> Int
    a = case x
        0 -> 1
        _ -> 2
    end
    b = case x
        1 -> 3
        _ -> 4
    end
    a + b
end
`,
			want: `fn f(x: Int) -> Int
    a = case x
        0 -> 1
        _ -> 2
    end

    b = case x
        1 -> 3
        _ -> 4
    end

    a + b
end
`,
		},
		"the blank line goes above a comment on the multi-line statement": {
			src: `fn f(x: Int) -> Int
    a = 1
    // pick one
    b = case x
        0 -> a
        _ -> 2
    end
    b
end
`,
			want: `fn f(x: Int) -> Int
    a = 1

    // pick one
    b = case x
        0 -> a
        _ -> 2
    end

    b
end
`,
		},
		"nested blocks follow the rule at their own indent": {
			src: `fn f(x: Int) -> Int
    if x > 0 then
        y = x + 1
        z = case y
            1 -> 0
            _ -> y
        end
        z
    else
        0
    end
end
`,
			want: `fn f(x: Int) -> Int
    if x > 0 then
        y = x + 1

        z = case y
            1 -> 0
            _ -> y
        end

        z
    else
        0
    end
end
`,
		},
		"a lambda block body follows the rule": {
			src: `fn f(xs: List<Int>) -> Int
    Iter.reduce(xs, 0, fn acc, x ->
        y = x * 2
        z = case y
            4 -> y
            _ -> 0
        end
        acc + z
    end)
end
`,
			want: `fn f(xs: List<Int>) -> Int
    Iter.reduce(xs, 0, fn acc, x ->
        y = x * 2

        z = case y
            4 -> y
            _ -> 0
        end

        acc + z
    end)
end
`,
		},
		"a test body follows the rule": {
			src: `test "spacing"
    a = 1
    assert a == 1
    b = case a
        1 -> 2
        _ -> 3
    end
    assert b == 2
end
`,
			want: `test "spacing"
    a = 1
    assert a == 1

    b = case a
        1 -> 2
        _ -> 3
    end

    assert b == 2
end
`,
		},
		"a test group's tests are set apart": {
			src: `group "group"
    clock Clock.Virtual

    test "one"
        assert True
    end
    test "two"
        assert True
    end
end
`,
			want: `group "group"
    clock Clock.Virtual

    test "one"
        assert True
    end

    test "two"
        assert True
    end
end
`,
		},
		"an attached test follows the rule": {
			src: `//! label = case 1
//!     1 -> "one"
//!     _ -> "many"
//! end
//! assert label == "one"
fn f() -> Int
    1
end
`,
			want: `//! label = case 1
//!     1 -> "one"
//!     _ -> "many"
//! end
//!
//! assert label == "one"
fn f() -> Int
    1
end
`,
		},
		"a short if that stays on one line is a one-line statement": {
			src: `fn f(n: Int) -> Int
    if n > 1 then return 0
    n
end
`,
			want: `fn f(n: Int) -> Int
    if n > 1 then return 0
    n
end
`,
		},
		"an over-long line with nowhere to break is a one-line statement": {
			src: `fn f() -> String
    a = "a string literal long enough that this binding runs past the line width of one hundred"
    a
end
`,
			want: `fn f() -> String
    a = "a string literal long enough that this binding runs past the line width of one hundred"
    a
end
`,
		},
		"a statement written across lines that fits is laid out on one line": {
			src: `fn f() -> Int
    a = 1
    b = Int.max(
        a,
        2,
    )
    b
end
`,
			want: `fn f() -> Int
    a = 1
    b = Int.max(a, 2)
    b
end
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
	shallow := "fn f() -> Int\n" +
		"    a = 1\n" +
		"    " + call + "\n" +
		"    total\n" +
		"end\n"
	formatsTo(t, shallow, shallow)

	deep := "fn f(x: Int) -> Int\n" +
		"    if x > 0 then\n" +
		"        if x > 1 then\n" +
		"            a = 1\n" +
		"            " + call + "\n" +
		"            total\n" +
		"        else\n" +
		"            0\n" +
		"        end\n" +
		"    else\n" +
		"        0\n" +
		"    end\n" +
		"end\n"
	broken := strings.Join([]string{
		"            total = Int.max(",
		"                first_value_with_a_long_name,",
		"                second_value_with_a_long_name,",
		"                third_value_n,",
		"            )",
	}, "\n")
	want := "fn f(x: Int) -> Int\n" +
		"    if x > 0 then\n" +
		"        if x > 1 then\n" +
		"            a = 1\n" +
		"\n" +
		broken + "\n" +
		"\n" +
		"            total\n" +
		"        else\n" +
		"            0\n" +
		"        end\n" +
		"    else\n" +
		"        0\n" +
		"    end\n" +
		"end\n"
	formatsTo(t, deep, want)
}

// The import rule still holds: a blank line follows a block's imports. A
// pipe statement follows the multi-line rule like any other statement.
func TestFormat_StatementSpacingKeepsTheImportRule(t *testing.T) {
	src := `fn f(xs: List<Int>) -> Int
    import std::io
    n = xs |> Iter.count()
    m = n + 1
    io.print(m)
    m
end
`
	want := `fn f(xs: List<Int>) -> Int
    import std::io

    n = xs |> Iter.count()
    m = n + 1
    io.print(m)
    m
end
`
	formatsTo(t, src, want)
}

// A pipe statement is set apart only when it spans lines: stacked as written,
// or broken because it does not fit. One that fits on its line sits with its
// neighbours, as any one-line statement does, and keeps a blank line the
// source wrote.
func TestFormat_StatementSpacingTreatsAPipeAsAnyStatement(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"an inline pipe sits with its neighbours": {
			src: `fn f(c: Int) -> String
    a = c |> Int.to_string() |> String.pad_start(5)
    b = String.length(a)
    c |> dbg
    assert b |> Int.to_string() == "5"
    a
end
`,
			want: `fn f(c: Int) -> String
    a = c |> Int.to_string() |> String.pad_start(5)
    b = String.length(a)
    c |> dbg
    assert b |> Int.to_string() == "5"
    a
end
`,
		},
		"an inline pipe keeps the blank line the source wrote": {
			src: `fn f(c: Int) -> String
    a = c |> Int.to_string() |> String.pad_start(5)

    b = String.length(a)
    a
end
`,
			want: `fn f(c: Int) -> String
    a = c |> Int.to_string() |> String.pad_start(5)

    b = String.length(a)
    a
end
`,
		},
		"an inline pipe first and last in its block": {
			src: `fn f(c: Int) -> String
    a = c |> Int.to_string()
    b = a
    b |> String.pad_start(5)
end
`,
			want: `fn f(c: Int) -> String
    a = c |> Int.to_string()
    b = a
    b |> String.pad_start(5)
end
`,
		},
		"a stacked pipe is set apart": {
			src: `fn f(xs: List<Int>) -> Int
    first = 1
    total =
        xs
        |> Iter.map(fn x -> x * 2)
        |> Iter.count()
    total + first
end
`,
			want: `fn f(xs: List<Int>) -> Int
    first = 1

    total =
        xs
        |> Iter.map(fn x -> x * 2)
        |> Iter.count()

    total + first
end
`,
		},
		"a pipe that breaks to fit the width is set apart": {
			src: `fn f(xs: List<Int>) -> Int
    first = 1
    total = xs |> Iter.map(fn x -> x * 2 + first_value_with_a_long_name) |> Iter.filter(fn x -> x > second_value_long) |> Iter.count()
    total + first
end
`,
			want: `fn f(xs: List<Int>) -> Int
    first = 1

    total =
        xs
        |> Iter.map(fn x -> x * 2 + first_value_with_a_long_name)
        |> Iter.filter(fn x -> x > second_value_long)
        |> Iter.count()

    total + first
end
`,
		},
		"an inline pipe in a lambda body and a case arm": {
			src: `fn f(n: Int) -> Int
    g = fn x ->
        y = x |> Int.abs()
        y + 1
    end
    case n
        0 ->
            z = n |> g()
            z + 1

        _ ->
            n
    end
end
`,
			want: `fn f(n: Int) -> Int
    g = fn x ->
        y = x |> Int.abs()
        y + 1
    end

    case n
        0 ->
            z = n |> g()
            z + 1

        _ ->
            n
    end
end
`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			formatsTo(t, c.src, c.want)
			if err := SameMeaning(c.src, c.want); err != nil {
				t.Fatalf("the formatted source does not keep the meaning: %v", err)
			}
		})
	}
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
