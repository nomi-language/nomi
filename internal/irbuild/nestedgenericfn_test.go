package irbuild

import (
	"strings"
	"testing"
)

// A generic fn declared in a body runs at each type argument tuple it is
// called at: called directly, through a pipe, from a lambda and from a
// non-generic nested fn's `try`; named as a value; capturing the value a
// name held where the fn was declared; with a `where` bound; called from
// another generic nested fn's body, two deep; inside a generic function's
// instance; at two instances of one generic struct, which render alike;
// calling itself; and in a test body.
func TestIRGenericNestedFn_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Box<T> {
    v: T
}

fn outer<T>(x: T): List<T> {
    fn twice<U>(u: U): List<U> {
        [u, u]
    }
    twice(x)
}

fn main() {
    k = 10
    fn add_k<T>(x: T, f: (T) -> Int): Int {
        f(x) + k
    }
    fn pair<A, B>(a: A, b: B): (B, A) {
        (b, a)
    }
    fn ok<T>(x: T): Result<T, String> {
        Ok(x)
    }
    fn show<T>(x: T): String where T: Display {
        Display.to_string(x)
    }
    fn label<T>(x: T): String where T: Display {
        "<" + show(x) + ">"
    }
    fn both<A, B>(a: A, b: B): String where A: Display, B: Display {
        label(a) + label(b)
    }
    k = 20
    io.inspect(add_k("abc", String.length))
    io.inspect(add_k(5, |n| n * 2))
    io.inspect(pair(1, "x"))
    io.inspect(3 |> pair("y"))
    io.inspect(Iter.map([1, 2], |n| pair(n, k)) |> Iter.to_list())
    io.inspect(Iter.map([True], ok) |> Iter.to_list())
    fn attempt(): Result<Box<Int>, String> {
        b = try ok(Box{v: 1})
        Ok(b)
    }
    io.inspect(attempt())
    io.inspect(ok(Box{v: 2.5}))
    io.inspect(both(1, "z"))
    io.inspect(outer("o"))
    io.inspect(outer(Some(1)))
    fn count<T>(xs: List<T>, n: Int): Int {
        case List.next_item(xs) {
            Some((_, rest)) -> count(rest, n + 1)
            None -> n
        }
    }
    io.inspect(count(["a", "b"], 0))
    io.inspect(count([1.5], 0))
}
`
	const want = "13\n" +
		"20\n" +
		"(\"x\", 1)\n" +
		"(\"y\", 3)\n" +
		"[(20, 1), (20, 2)]\n" +
		"[Ok(True)]\n" +
		"Ok(Box{v: 1})\n" +
		"Ok(Box{v: 2.5})\n" +
		"\"<1><z>\"\n" +
		"[\"o\", \"o\"]\n" +
		"[Some(1), Some(1)]\n" +
		"2\n" +
		"1\n"
	irRunSource(t, src, want)
	const tests = `test "a generic nested fn in a test" {
    fn ident<T>(x: T): T {
        x
    }
    assert ident(4) == 4
    assert ident("s") == "s"
}
`
	report, exit := irShapesRun(t, tests, []string{"a generic nested fn in a test"})
	if exit != 0 || !strings.Contains(report, "1 passed") {
		t.Fatalf("the case does not pass (exit %d):\n%s", exit, report)
	}
}
