package analysis_test

import (
	"strings"
	"testing"
)

// `subtests` stands where a statement assertion does, and each misplacement
// is an error at the keyword that names it. A row is not a function, so an
// exit written directly in its body is an error at the exit. Each case fails
// if the source starts being admitted, and checks the message.
func TestSubtests_RejectedPlacementsAndExits(t *testing.T) {
	cases := []struct {
		name, src string
		line, col int
		msg, hint string
	}{
		{
			name: "fn without the Result shape",
			src: `fn f(xs: List<Int>): Int {
    subtests xs, x {
        assert x > 0
    }
    1
}`,
			line: 2, col: 5,
			msg: "`subtests` can return AssertionFailure, but the enclosing function returns Int",
		},
		{
			name: "main",
			src: `fn main() {
    subtests [1], x {
        assert x > 0
    }
}`,
			line: 2, col: 5,
			msg: "`subtests` can return AssertionFailure, but the enclosing function returns Unit",
		},
		{
			name: "once",
			src: `once n = {
    subtests [1], x {
        assert x > 0
    }
    1
}`,
			line: 2, col: 5,
			msg:  "`subtests` cannot be used in the initializer of `once n`: a `once` has no function to return from",
			hint: "move the `subtests` into a function that returns a Result",
		},
		{
			name: "lambda",
			src: `test "t" {
    f = || {
        subtests [1], x {
            assert x > 0
        }
    }
    _ = f
    assert True
}`,
			line: 3, col: 9,
			msg: "`subtests` inside a lambda becomes the lambda's return value instead of failing the test",
		},
		{
			name: "nested",
			src: `test "t" {
    subtests [1], x {
        subtests [2], y {
            assert x < y
        }
    }
}`,
			line: 3, col: 9,
			msg:  "`subtests` cannot be written inside another `subtests` body: each row of the outer statement is one case",
			hint: "combine the two row lists into one, as in `subtests pairs, (a, b) { ... }`, or write the inner rows as a test of their own",
		},
		{
			name: "setup",
			src: `tests "g" {
    setup {
        subtests [1], x {
            assert x > 0
        }
        1
    }

    test "t", n {
        assert n == 1
    }
}`,
			line: 3, col: 9,
			msg:  "`subtests` cannot be used in a `setup`: a setup builds the value each test binds",
			hint: "move the rows into a test of the group",
		},
		{
			name: "rows that are not iterable",
			src: `test "t" {
    subtests 5, x {
        assert x > 0
    }
}`,
			line: 2, col: 14,
			msg: "`subtests` runs its body once per element of what it is given, which must be something `Iter` accepts, such as a List, a Range or an Iter; got Int",
		},
		{
			name: "rows of no determined type",
			src: `test "t" {
    subtests [], x {
        assert x == 1
    }
}`,
			line: 2, col: 14,
			msg:  "the type of the rows of `subtests` is not determined: List<",
			hint: "give the rows a type, as in `rows: List<Int> = []`",
		},
		{
			name: "refutable pattern",
			src: `test "t" {
    subtests [Some(1)], Some(n) {
        assert n > 0
    }
}`,
			line: 2, col: 25,
			msg: "a `subtests` pattern must match every row; bind it with a name or an irrefutable pattern and match it in the body",
		},
		{
			name: "return in a row",
			src: `test "t" {
    subtests [1, 2], x {
        if x == 1 {
            return
        }
        assert x > 0
    }
}`,
			line: 4, col: 13,
			msg:  "`return` cannot be used in a `subtests` body: a row is not a function, and each row runs to its end",
			hint: "end the row with its last statement, or put what follows in an `if` or `case` arm",
		},
		{
			name: "break in a row",
			src: `test "t" {
    subtests [1, 2], x {
        if x == 1 {
            break
        }
        assert x > 0
    }
}`,
			line: 4, col: 13,
			msg:  "`break` cannot be used in a `subtests` body: every row runs, in order",
			hint: "choose the rows before the statement, as in `subtests rows |> Iter.filter(keep), row { ... }`",
		},
		{
			name: "continue in a row",
			src: `test "t" {
    subtests [1, 2], x {
        if x == 1 {
            continue
        }
        assert x > 0
    }
}`,
			line: 4, col: 13,
			msg: "`continue` cannot be used in a `subtests` body: every row runs, in order",
		},
		{
			name: "try in a row",
			src: `test "t" {
    subtests [Some(1)], m {
        n = try m
        assert n > 0
    }
}`,
			line: 3, col: 13,
			msg:  "`try` cannot be used in a `subtests` body: a row is not a function, so there is nothing to return the Err or None to",
			hint: "assert the shape instead: `assert Ok(value) = expr` binds value or fails the row",
		},
		{
			name: "a body that can never fail",
			src: `import std/io

test "t" {
    subtests [1, 2], n {
        io.print(n)
    }
}`,
			line: 4, col: 5,
			msg:  "`subtests` body must contain an assert or refute, or end each row with a Result<_, AssertionFailure>",
			hint: "assert something about each row, or end the body with a call that returns Result<_, AssertionFailure>, as a helper that asserts does",
		},
		{
			name: "a final Result whose error is not AssertionFailure",
			src: `fn parse(n: Int): Result<Int, String> {
    Ok(n)
}

test "t" {
    subtests [1, 2], n {
        parse(n)
    }
}`,
			line: 6, col: 5,
			msg: "`subtests` body must contain an assert or refute, or end each row with a Result<_, AssertionFailure>",
		},
		{
			name: "an assertion only in a lambda in the body",
			src: `test "t" {
    subtests [1, 2], n {
        f = |m| m > n
        _ = f
    }
}`,
			line: 2, col: 5,
			msg: "`subtests` body must contain an assert or refute, or end each row with a Result<_, AssertionFailure>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := analyzeConcurrentRaw(tc.src)
			found := -1
			for i := range errs {
				if strings.Contains(errs[i].Message, tc.msg) {
					found = i
					break
				}
			}
			if found < 0 {
				t.Fatalf("the checker admits this source, or rejects it for another reason; want %q, got:\n%s",
					tc.msg, errorList(errs))
			}
			got := errs[found]
			if got.Line != tc.line || got.Col != tc.col {
				t.Errorf("reported at %d:%d, want %d:%d", got.Line, got.Col, tc.line, tc.col)
			}
			if tc.hint != "" && (len(got.Hints) != 1 || got.Hints[0] != tc.hint) {
				t.Errorf("hints = %q, want [%q]", got.Hints, tc.hint)
			}
		})
	}
}

// The admitted forms: a test body, a fn returning `Result<_, AssertionFailure>`,
// rows from each kind of source, exits inside a lambda or a nested fn in a
// row (boundaries of their own), and `subtests` as an ordinary name. Each
// fails if the checker starts rejecting it.
func TestSubtests_AdmittedForms(t *testing.T) {
	cases := []struct{ name, src string }{
		{"test body over a list of tuples", `test "t" {
    subtests [("a", 1), ("bb", 2)], (s, n) {
        assert String.length(s) == n
    }
}`},
		{"result fn", `import std/assertions.AssertionFailure

fn f(xs: List<Int>): Result<Unit, AssertionFailure> {
    subtests xs, x {
        assert x > 0
    }
    Ok(Unit)
}`},
		{"range, set, vector, map and iter rows", `test "t" {
    subtests 1..3, n {
        assert n > 0
    }
    subtests Set{1, 2}, n {
        assert n > 0
    }
    subtests Vector[1, 2], n {
        assert n > 0
    }
    subtests {"a" => 1}, (k, v) {
        assert k == "a" and v == 1
    }
    subtests [1, 2] |> Iter.map(|n| n * 2), n {
        assert n % 2 == 0
    }
}`},
		{"exits inside a lambda in a row", `test "t" {
    subtests [[1, 2]], xs {
        first = Iter.loop(|| {
            break
        })
        assert first == Unit
        assert Iter.all?(xs, |x| {
            if x > 5 {
                return False
            }
            True
        })
    }
}`},
		{"a row that ends with a helper that asserts", `import std/assertions.AssertionFailure

fn check_row(line: String, want: Bool): Result<Unit, AssertionFailure> {
    assert (line == String.reverse(line)) == want
    Ok(Unit)
}

test "t" {
    subtests [("abba", True), ("abc", False)], (line, want) {
        check_row(line, want)
    }
}`},
		{"a row whose final if or case arms end with a helper", `import std/assertions.AssertionFailure

fn positive(n: Int): Result<Unit, AssertionFailure> {
    assert n > 0
    Ok(Unit)
}

test "t" {
    subtests [1, 2], n {
        if n > 1 {
            positive(n)
        } else {
            positive(n + 1)
        }
    }
    subtests [1, 2], n {
        case n {
            1 -> positive(n)
            _ -> positive(n - 1)
        }
    }
}`},
		{"assertions nested in an arm, and a test in a group whose only assertion is a subtests", `tests "g" {
    test "t" {
        subtests [1, 2], n {
            if n > 1 {
                assert n == 2
            } else {
                refute n == 2
            }
        }
    }
}`},
		{"subtests as a name", `fn subtests(n: Int): Int {
    n
}

test "t" {
    subtests = [subtests(1)]
    assert subtests == [1]
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
