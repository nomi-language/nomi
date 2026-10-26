package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Each capture (`&(&1 > 2)`, `&(0..&1)`, `&(-&1)`, `&#Point{x: &1, y: 0}`,
// `&#(&1, 0)`) runs as the lambda it stands for, in each place a function
// value stands: a callback, a pipe stage's argument, an annotated binding,
// a struct field and a generic call.
func TestCaptureFunc_Runs(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Point {
    x: Int
    y: Int
}

struct Box<T> {
    v: T
}

struct Rule {
    name: String
    keep: (Int) -> Bool
}

enum Shape {
    Circle {radius: Float}
    Dot
}

fn above<T>(xs: List<T>, min: T) -> List<T> where T: Comparable {
    xs |> Iter.filter(&(&1 > min)) |> Iter.to_list()
}

fn apply(xs: List<Int>, label: String = "applied", f: (Int) -> Int) -> List<Int> {
    io.print(label)
    xs |> Iter.map(f) |> Iter.to_list()
}

fn main() {
    io.inspect([1, 2, 3] |> Iter.filter(&(&1 > 2)) |> Iter.to_list())
    io.inspect([-1, 2] |> Iter.filter(&(0 < &1)) |> Iter.to_list())
    io.inspect([1, 2, 3] |> Iter.map(&(&1 * 2)) |> Iter.to_list())
    add: (Int, Int) -> Int = &(&1 + &2)
    io.inspect(add(2, 3))
    less: (Int, Int) -> Int = &(&1 - &2)
    io.inspect(less(10, 3))
    io.inspect([10, 21] |> Iter.map(&(&1 / 5)) |> Iter.to_list())
    io.inspect([10, 21] |> Iter.map(&(&1 % 5)) |> Iter.to_list())
    io.inspect(["a", "b"] |> Iter.filter(&(&1 != "a")) |> Iter.to_list())
    io.inspect([1, 2, 3] |> Iter.filter(&(2 >= &1)) |> Iter.to_list())
    io.inspect([1, 2, 3] |> Iter.filter(&(&1 <= 1)) |> Iter.count())
    io.inspect([1, 2] |> Iter.filter(&(&1 == 2)) |> Iter.count())
    io.inspect(["a", "b"] |> Iter.map(&(&1 + "!")) |> String.join(","))
    io.inspect([[1], [2]] |> Iter.map(&(&1 + [0])) |> Iter.to_list())
    io.inspect([%[1]] |> Iter.map(&(&1 + %[9])) |> Iter.to_list())
    upto: (Int) -> Range<Int> = &(0..&1)
    io.inspect(upto(3) |> Iter.to_list())
    io.inspect([2, 3] |> Iter.map(&(1..=&1)) |> Iter.map(Iter.count) |> Iter.to_list())
    io.inspect([1, -2] |> Iter.map(&(-&1)) |> Iter.to_list())
    io.inspect([True, False] |> Iter.filter(&(!&1)) |> Iter.count())
    mk: (Int) -> Point = &#Point{x: &1, y: 0}
    io.inspect(mk(5))
    io.inspect(mk(x: 6))
    swap: (Int, Int) -> Point = &#Point{y: &1, x: &2}
    io.inspect(swap(1, 2))
    anon: (Int) -> Point = &#{x: &1, y: 7}
    io.inspect(anon(3))
    box: (String) -> Box<String> = &#Box{v: &1}
    io.inspect(box("b"))
    io.inspect([1.5] |> Iter.map(&#Shape.Circle{radius: &1}) |> Iter.to_list())
    circle: (Float) -> Shape = &#.Circle{radius: &1}
    io.inspect(circle(2.5))
    io.inspect([1, 2] |> Iter.map(&#(&1, "n")) |> Iter.to_list())
    pair: (Int, String) -> #(Int, String) = &#(&1, &2)
    io.inspect(pair(1, "a"))
    io.inspect([1, 5] |> Iter.filter(&(&1 > 2)) |> Iter.to_list())
    rule = #Rule{name: "big", keep: &(&1 > 10)}
    io.inspect(rule.keep(11))
    io.inspect(above([1, 5, 9], 4))
    io.inspect(apply([1, 2], &(&1 * 10)))
    wrapped: Maybe<(Int) -> Int> = Some(&(&1 + 1))
    io.inspect(Maybe.map(wrapped, fn f -> f(1)))
}
`)
	want := `[3]
[2]
[2, 4, 6]
5
7
[2, 4]
[0, 1]
["b"]
[1, 2]
1
1
"a!,b!"
[[1, 0], [2, 0]]
[%[1, 9]]
[0, 1, 2]
[2, 3]
[-1, 2]
1
#Point{x: 5, y: 0}
#Point{x: 6, y: 0}
#Point{x: 2, y: 1}
#Point{x: 3, y: 7}
#Box{v: "b"}
[#Circle{radius: 1.5}]
#Circle{radius: 2.5}
[#(1, "n"), #(2, "n")]
#(1, "a")
[5]
True
[5, 9]
applied
[10, 20]
Some(2)
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A partial capture's written arguments run in the order its call would run
// them, positionals and then named arguments, each in written order, as the
// equivalent lambda's do, whatever slots they fill.
func TestCaptureFunc_APartialRunsItsArgumentsInCallOrder(t *testing.T) {
	got := runSourceOutput(t, `import std/io

fn g(s: String) -> String {
    io.print("eval ${s}")
    s
}

fn f(a: String, b: String, c: String) -> String {
    a + b + c
}

fn main() {
    p = &f(b: g("b"), a: g("a"), c: &1)
    io.print(p("c"))
    q = &f(g("a"), c: g("c"), b: &1)
    io.print(q("b"))
}
`)
	want := "eval b\neval a\nabc\neval a\neval c\nabc\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A capture's body is evaluated on each call, as the equivalent lambda's
// is: nothing runs when the capture is made.
func TestCaptureFunc_EvaluatesItsBodyOnEachCall(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Point {
    x: Int
    y: Int
}

fn noisy(n: Int) -> Int {
    io.print("noisy ${n}")
    n
}

fn main() {
    big: (Int) -> Bool = &(&1 > noisy(2))
    mk: (Int) -> Point = &#Point{y: noisy(4), x: &1}
    io.print("made")
    io.inspect(big(1))
    io.inspect(big(3))
    io.inspect(mk(1))
    io.inspect(mk(2))
}
`)
	want := "made\nnoisy 2\nFalse\nnoisy 2\nTrue\nnoisy 4\n#Point{x: 1, y: 4}\nnoisy 4\n#Point{x: 2, y: 4}\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// The function a capture makes is its body, which `&` marks: a call, a
// parenthesized expression or a literal. `Some(&(&1 + 1))` holds a
// function, where `&Some(&1 + 1)` is one; a capture called directly is
// called where it is written; a `then` stage applies one to the piped
// value.
func TestCaptureFunc_TheBodyIsWhatTheAmpersandMarks(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Point {
    x: Int
    y: Int
}

struct Order {
    total: Int
}

fn f(a: Int, b: Int) -> Int {
    a * 10 + b
}

fn divide(x: Int, y: Int) -> Int {
    x / y
}

fn g(h: (Int) -> Int) -> Int {
    h(-5)
}

fn main() {
    xs = [-1, 2, 3]
    io.inspect(Iter.map(xs, &(&1 * 2 + 1)) |> Iter.to_list())
    io.inspect(Iter.filter(xs, &(&1 % 2 == 0)) |> Iter.to_list())
    io.inspect(Iter.map(xs, &Int.max(&1, 0)) |> Iter.to_list())
    io.inspect(Iter.map(xs, &(Int.max(&1, 0) + 1)) |> Iter.to_list())
    io.inspect(g(&Int.max(&1, 0)))
    t = #(&Int.max(&1, 0), 1)
    io.inspect(t.0(-3))
    io.inspect(Iter.map(xs, &#Point{x: &1, y: 0}) |> Iter.to_list())
    mul: (Int, Int) -> Int = &(&1 * &2)
    io.inspect(mul(3, 4))
    io.inspect((&f(2, &1))(4))
    io.inspect(10 |> then &divide(100, &1))
    big = [#Order{total: 50}, #Order{total: 150}] |> Iter.filter(&(&1.total > 100)) |> Iter.count()
    io.inspect(big)
    io.inspect(xs |> Iter.map(&(-&1 + 1)) |> Iter.to_list())
    sq = xs |> Iter.map(&(f(&1, 1) - 1)) |> Iter.to_list()
    io.inspect(sq)
    held: Maybe<(Int) -> Int> = Some(&(&1 + 1))
    io.inspect(Maybe.map(held, fn h -> h(1)))
    wrap: (Int) -> Maybe<Int> = &Some(&1 + 1)
    io.inspect(wrap(1))
}
`)
	want := `[-1, 5, 7]
[2]
[0, 2, 3]
[1, 3, 4]
0
0
[#Point{x: -1, y: 0}, #Point{x: 2, y: 0}, #Point{x: 3, y: 0}]
12
24
10
1
[2, -1, -2]
[-10, 20, 30]
Some(2)
Some(2)
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// Numbered parameters: one used twice, parameters in another order, one no
// `&N` names (`&(&2 * 10)`, and a partial ignoring the parameter past its
// own), and a partial capture's parameter names and defaults.
func TestCaptureFunc_Numbering(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Point {
    x: Int
    y: Int
}

struct Order {
    total: Int
}

fn f(a: Int, b: Int) -> Int {
    a * 10 + b
}

fn divide(x: Int, y: Int) -> Int {
    x / y
}

fn g(h: (Int) -> Int) -> Int {
    h(-5)
}

fn connect(host: String, port: Int = 8080, timeout: Int = 30) -> String {
    "${host}:${port}/${timeout}"
}

fn main() {
    xs = [-1, 2, 3]
    io.inspect(Iter.map(xs, &(&1 * 2 + 1)) |> Iter.to_list())
    io.inspect(Iter.filter(xs, &(&1 % 2 == 0)) |> Iter.to_list())
    io.inspect(Iter.map(xs, &Int.max(&1, 0)) |> Iter.to_list())
    io.inspect(Iter.map(xs, &(Int.max(&1, 0) + 1)) |> Iter.to_list())
    io.inspect(g(&Int.max(&1, 0)))
    t = #(&Int.max(&1, 0), 1)
    io.inspect(t.0(-3))
    io.inspect(Iter.map(xs, &#Point{x: &1, y: 0}) |> Iter.to_list())
    mul: (Int, Int) -> Int = &(&1 * &2)
    io.inspect(mul(3, 4))
    sq: (Int) -> Int = &(&1 * &1)
    io.inspect(sq(5))
    rev: (Int, Int) -> Int = &f(&2, &1)
    io.inspect(rev(1, 2))
    second: (Int, Int) -> Int = &(&2 * 10)
    io.inspect(second(1, 2))
    only_second: (Int, Int) -> Int = &(&2)
    io.inspect(only_second(1, 2))
    first_in_f: (Int, Int) -> Int = &f(&1, 9)
    io.inspect(first_in_f(4, 5))
    io.inspect((&f(2, &1))(4))
    io.inspect(10 |> then &divide(100, &1))
    big = [#Order{total: 50}, #Order{total: 150}] |> Iter.filter(&(&1.total > 100)) |> Iter.count()
    io.inspect(big)
    inc = &f(1, &1)
    io.inspect(inc(2))
    c = &connect(&1, port: &2)
    io.print(c("h", 1))
    io.print(c("h"))
    io.print(c(port: 80, host: "x"))
    mk: (Int) -> Point = &#Point{x: &1, y: 0}
    io.inspect(mk(x: 7))
}
`)
	want := `[-1, 5, 7]
[2]
[0, 2, 3]
[1, 3, 4]
0
0
[#Point{x: -1, y: 0}, #Point{x: 2, y: 0}, #Point{x: 3, y: 0}]
12
25
21
20
2
49
24
10
1
12
h:1/30
h:8080/30
x:80/30
#Point{x: 7, y: 0}
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A partial capture of an operator interface's function (`&Int.add(7, &1)`)
// is a function of its open operands, typed by the impl its written operand
// and the expected parameter choose, so a generic std callee such as
// `Maybe.map` solves its result type from it. Before, the checker gave the
// capture no type, and `Maybe.map`'s `U` stayed open: "this call to
// `Maybe.map` is not supported yet". Each case is its own program, since a
// program's first decline hides the rest.
func TestCaptureFunc_AnOperatorFunctionsPartialIsAFunctionOfItsOpenOperands(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"Maybe.map(Some(2), &Int.add(7, &1))", "Some(9)"},
		{"Maybe.map(Some(2), &Int.add(&1, 7))", "Some(9)"},
		{"Maybe.map(Some(2), &Int.subtract(7, &1))", "Some(5)"},
		{"Maybe.map(Some(2), &Int.subtract(&1, 7))", "Some(-5)"},
		{"Maybe.map(Some(2), &Int.multiply(7, &1))", "Some(14)"},
		{"Maybe.map(Some(2), &Int.divide(10, &1))", "Some(5)"},
		{"Maybe.map(Some(2.5), &Float.add(1.0, &1))", "Some(3.5)"},
		{"Maybe.map(Some(2.5), &Float.divide(&1, 2.0))", "Some(1.25)"},
		{"Result.map(ok(), &Int.add(7, &1))", "Ok(9)"},
		{"Result.map(ok(), &Int.multiply(&1, 3))", "Ok(6)"},
		{"Result.map_err(err(), &Int.add(7, &1))", "Err(11)"},
		{"Result.map_err(err(), &Int.subtract(&1, 1))", "Err(3)"},
		{"Iter.reduce([1, 2, 3], &Int.subtract(&2, &1), from: 10)", "-8"},
		{"(&Int.add(7, &1))(1)", "8"},
		// Int has two Divide impls; the open operand's expected type picks
		// `Divide<NonZeroInt, Int>`.
		{"Int.to_non_zero(4) |> Maybe.map(&Int.divide(12, &1))", "Some(3)"},
		{"Maybe.map(Int.to_non_zero(4), &Int.divide(12, &1))", "Some(3)"},
		{"Int.to_non_zero(4) |> Maybe.to_result(\"zero\") |> Result.map(&Int.divide(12, &1))", "Ok(3)"},
		{"Int.to_non_zero(0) |> Maybe.to_result(\"zero\") |> Result.map(&Int.divide(12, &1))", "Err(\"zero\")"},
		{"Int.to_non_zero(4) |> Maybe.flat_map(fn d -> Maybe.map(Some(20), &Int.divide(&1, d)))", "Some(5)"},
		// With nothing expected, the left operand of `Int.subtract` is Int.
		{"(&Int.subtract(&1, 1))(9)", "8"},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			src := `import std/io

fn ok() -> Result<Int, String> {
    Ok(2)
}

fn err() -> Result<Int, Int> {
    Err(4)
}

fn main() {
    _ = #(ok(), err())
    io.inspect(` + c.expr + `)
}
`
			p, err := vmhost.LoadSource("main.nomi", src)
			if err != nil {
				t.Fatalf("the front end rejects the program: %v", err)
			}
			if u := p.Unsupported(); u != nil {
				t.Fatalf("the program does not lower: %v", u)
			}
			var out bytes.Buffer
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := strings.TrimSpace(out.String()); got != c.want {
				t.Fatalf("output %q, want %q", got, c.want)
			}
		})
	}
}
