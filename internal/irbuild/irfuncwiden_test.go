package irbuild

import "testing"

// A function whose parameter is wider than the function type its position
// expects runs when passed as a value: an `Iter<T>` parameter where a
// `List<T>` (or Range, Set) is expected, a `Display` parameter where an Int
// is expected, and a `List<Int>` result where an `Iter<Int>` is expected.
// User functions and std functions, generic or not, in owner calls, pipes,
// bare generic calls, and after a trip through a binding, a list or a
// record field. A list of functions enters an `Iter` of functions too, which
// `Iter.count` given a `(List<(Int) -> Int>) -> Int` position needs.
func TestIRFuncWiderThanExpected_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Box {
    n: Int
}

impl Box {
    fn over<T>(xs: List<List<T>>, f: (List<T>) -> Int): List<Int> {
        Iter.map(xs, f) |> Iter.to_list()
    }
}

fn cnt<T>(xs: Iter<T>): Int {
    Iter.count(xs)
}

fn cnt_int(xs: Iter<Int>): Int {
    Iter.count(xs)
}

fn cnt_list<T>(xs: List<T>): Int {
    Iter.count(xs)
}

fn show(x: Display): String {
    "<" + Display.to_string(x) + ">"
}

fn apply<T>(xs: List<T>, f: (List<T>) -> Int): Int {
    f(xs)
}

fn call_all(xs: Iter<(Int) -> Int>): List<Int> {
    Iter.map(xs, |f| f(2)) |> Iter.to_list()
}

fn pair(n: Int): List<Int> {
    [n, n * 2]
}

fn main() {
    io.print(Map.map_values({"a" => 1, "b" => 22}, Int.to_string))
    io.print(Map.map_values({"a" => "x", "b" => "yz"}, String.length))
    m = {"a" => [1, 2], "b" => [3]}
    io.print(Map.map_values(m, cnt_list))
    io.print(Map.map_values(m, cnt))
    io.print(Map.map_values(m, cnt_int))
    io.print(Map.map_values(m, Iter.count))
    io.print(Map.map_values(m, Iter.first))
    io.print([[1, 2], [3]] |> Iter.map(Iter.count) |> Iter.to_list())
    io.print([[1, 2], [3]] |> Iter.map(cnt) |> Iter.to_list())
    io.print([1..4, 1..2] |> Iter.map(Iter.count) |> Iter.to_list())
    io.print([Iter.to_set([1, 2, 2])] |> Iter.map(cnt) |> Iter.to_list())
    io.print([1, 2] |> Iter.map(show) |> Iter.to_list())
    io.print(apply([1, 2, 3], cnt))
    io.print([1, 2] |> apply(Iter.count))
    io.print(Box.over([[1], [2, 3]], cnt))
    io.print(Box.over([[1], [2, 3]], Iter.count))
    c = cnt_int
    io.print(Map.map_values(m, c))
    fs = [cnt_int]
    Iter.each(fs, |f| io.print(Map.map_values(m, f)))
    held = {count: cnt_int}
    io.print(Map.map_values(m, held.count))
    f: (List<Int>) -> Int = cnt_int
    io.print(f([1]))
    g: (List<Int>) -> Int = Iter.count
    io.print(g([1, 2]))
    p: (Int) -> Iter<Int> = pair
    io.print(p(3) |> Iter.to_list())
    adders = [|n: Int| n + 1, |n: Int| n * 10]
    io.print(call_all(adders))
    io.print(apply(adders, Iter.count))
}
`
	const want = "{a => 1, b => 22}\n" +
		"{a => 1, b => 2}\n" +
		"{a => 2, b => 1}\n" +
		"{a => 2, b => 1}\n" +
		"{a => 2, b => 1}\n" +
		"{a => 2, b => 1}\n" +
		"{a => Some(1), b => Some(3)}\n" +
		"[2, 1]\n" +
		"[2, 1]\n" +
		"[3, 1]\n" +
		"[2]\n" +
		"[<1>, <2>]\n" +
		"3\n" +
		"2\n" +
		"[1, 2]\n" +
		"[1, 2]\n" +
		"{a => 2, b => 1}\n" +
		"{a => 2, b => 1}\n" +
		"{a => 2, b => 1}\n" +
		"1\n" +
		"2\n" +
		"[3, 6]\n" +
		"[3, 20]\n" +
		"2\n"
	irRunSource(t, src, want)
}

// A function whose type is assignable to the position's (wider parameters,
// a narrower result) runs at every position the checker admits it: a plain
// and a named argument, an interface parameter, an `embeds` enum parameter,
// a List result where an Iter is expected, a higher-order argument, a
// generic argument direct and piped, an annotated binding, an annotated
// list's elements, a struct field, a generic struct at the binding's own
// type, a result and an early `return`, and a generic join of two lambdas.
func TestIRFuncWiderThanExpected_EveryPosition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Holder {
    f: (List<Int>) -> Int
}

struct Box<T> {
    v: T
}

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

fn cnt(xs: Iter<Int>): Int {
    Iter.count(xs)
}

fn shown(x: Display): String {
    "<" + Display.to_string(x) + ">"
}

fn area(s: Shape): Int {
    case s {
        .Circle{r} -> r * r
        .Dot -> 0
    }
}

fn pair(n: Int): List<Int> {
    [n, n * 2]
}

fn use(f: (List<Int>) -> Int): Int {
    f([1, 2, 3])
}

fn use_named(xs: List<Int>, f: (List<Int>) -> Int): Int {
    f(xs)
}

fn use_show(f: (Int) -> String): String {
    f(7)
}

fn use_circle(f: (Circle) -> Int): Int {
    f(Circle{r: 3})
}

fn use_iter(f: (Int) -> Iter<Int>): List<Int> {
    f(4) |> Iter.to_list()
}

fn higher(g: ((Iter<Int>) -> Int) -> Int): Int {
    g(cnt)
}

fn give_list(f: (List<Int>) -> Int): Int {
    f([5, 6])
}

fn apply<T>(x: T, f: (T) -> String): String {
    f(x)
}

fn pick<T>(c: Bool, a: T, b: T): T {
    if c { a } else { b }
}

fn returns_fn(): (List<Int>) -> Int {
    cnt
}

fn returns_early(b: Bool): (List<Int>) -> Int {
    if b {
        return cnt
    }
    |xs| Iter.count(xs) + 100
}

fn main() {
    io.print(use(cnt))
    io.print(use_named(f: cnt, xs: [1]))
    io.print(use_show(shown))
    io.print(use_circle(area))
    io.print(use_iter(pair))
    io.print(higher(give_list))
    io.print(apply(3, shown))
    io.print(5 |> apply(shown))
    f: (List<Int>) -> Int = cnt
    io.print(f([1, 2]))
    fs: List<(List<Int>) -> Int> = [cnt, |xs| Iter.count(xs) + 100]
    io.print(Iter.map(fs, |g| g([1, 2, 3])) |> Iter.to_list())
    h = Holder{f: cnt}
    io.print(h.f([1]))
    b: Box<(List<Int>) -> Int> = Box{v: f}
    io.print(b.v([9]))
    io.print(returns_fn()([1, 2, 3, 4]))
    io.print(returns_early(True)([1]))
    io.print(returns_early(False)([1, 2]))
    c = pick(True, |x: Float| Float.to_string(x), |x| shown(x))
    io.print(c(1.5))
    d: (Circle) -> Int = area
    io.print(d(Circle{r: 4}))
}
`
	const want = "3\n" +
		"1\n" +
		"<7>\n" +
		"9\n" +
		"[4, 8]\n" +
		"2\n" +
		"<3>\n" +
		"<5>\n" +
		"2\n" +
		"[3, 103]\n" +
		"1\n" +
		"1\n" +
		"4\n" +
		"1\n" +
		"102\n" +
		"1.5\n" +
		"16\n"
	irRunSource(t, src, want)
}
