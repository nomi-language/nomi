package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// `A or B` runs in every position a pattern takes: the first alternative
// that matches binds the names, a guard applies to the arm whichever
// alternative matched, and a failed guard goes on to the next arm.
func TestOrPattern_RunsInEveryPosition(t *testing.T) {
	got := runSourceOutput(t, `import std/io

enum Tri {
    Equilateral
    Isosceles
    Scalene
}

enum Ev {
    A Int
    B Int
    C String
}

enum Two {
    L Int
    R Int
}

enum Shape {
    Circle {r: Float}
    Square {r: Float}
    Rect {w: Float, h: Float}
    Point
}

struct P {
    kind: Tri
    n: Int
}

fn tri(m: Maybe<Tri>): String {
    case m {
        Some(.Equilateral or .Isosceles) -> "two equal"
        Some(.Scalene) -> "none equal"
        None -> "no triangle"
    }
}

fn ev(e: Ev): String {
    case e {
        .A(n) or .B(n) when n > 0 -> "positive ${n}"
        .A(n) -> "A ${n}"
        .B(n) -> "B ${n}"
        .C(s) -> s
    }
}

fn shape(s: Shape): String {
    case s {
        .Circle{r} or .Square{r} -> "round-ish ${r}"
        .Rect({w: 0.0, h: x} or {w: x, h: 0.0}) -> "flat ${x}"
        .Rect(_) or .Point -> "other"
    }
}

fn num(n: Int): String {
    case n {
        1 or 2 or 3 -> "small"
        -1 or -2 -> "negative"
        _ -> "big"
    }
}

fn word(s: String): String {
    case s {
        "yes" or "y" -> "affirmative"
        "no" or "n" -> "negative"
        _ -> "?"
    }
}

fn closer(cs: List<Codepoint>): String {
    case cs {
        [')' or ']' or '}' as c, ..rest] -> "closer ${c} then ${Iter.count(rest)}"
        [] or [_] -> "short"
        _ -> "other"
    }
}

fn pair(t: (Int, Int)): Int {
    case t {
        (0, x) or (x, 0) -> x
        (a, b) -> a * b
    }
}

fn field(p: P): String {
    case p {
        P{kind: .Equilateral or .Isosceles, n} -> "eq ${n}"
        P{n: 1 or 2} -> "small"
        _ -> "other"
    }
}

fn entry(m: Map<String, Int>): String {
    case m {
        {"k" => 1 or 2 as v} -> "low ${v}"
        _ -> "other"
    }
}

fn nested(m: Maybe<Ev>): String {
    case m {
        Some(.A(n) or (.B(n) or .A(n))) or Some(.B(n)) -> "${n}"
        Some(.C(_)) or None -> "none"
    }
}

fn whole(e: Ev): String {
    case e {
        .A(_) or .B(_) as v -> "numeric " + ev(v)
        .C(_) -> "string"
    }
}

fn first_num(e: Ev): Int {
    .A(n) or .B(n) = e else { return -1 }
    n
}

fn iff(e: Ev): String {
    if .A(n) or .B(n) = e {
        "got ${n}"
    } else {
        "nothing"
    }
}

fn plain(t: Two): Int {
    .L(n) or .R(n) = t
    n
}

fn param(.L(n) or .R(n): Two): Int {
    n + 1
}

fn tup((x, 0) or (_, x): (Int, Int)): Int {
    x
}

fn fallback(r: Result<Two, String>): Int {
    Ok(.L(n) or .R(n)) = r else { Two.L(0) }
    n
}

fn fallback_name(r: Result<Int, String>): Int {
    Ok(n or n) = r else { 5 }
    n
}

fn main() {
    io.print(tri(Some(Tri.Isosceles)))
    io.print(tri(Some(Tri.Scalene)))
    io.print(tri(None))
    io.print(ev(Ev.A(3)))
    io.print(ev(Ev.A(-3)))
    io.print(ev(Ev.B(-4)))
    io.print(ev(Ev.C("c")))
    io.print(shape(Shape.Square{r: 1.5}))
    io.print(shape(Shape.Rect{w: 0.0, h: 2.0}))
    io.print(shape(Shape.Rect{w: 3.0, h: 0.0}))
    io.print(shape(Shape.Rect{w: 3.0, h: 1.0}))
    io.print(num(2))
    io.print(num(-2))
    io.print(num(9))
    io.print(word("y"))
    io.print(word("n"))
    io.print(word("x"))
    io.print(closer([']', 'a']))
    io.print(closer(['a']))
    io.print(closer(['a', 'b']))
    io.print(pair((0, 7)))
    io.print(pair((8, 0)))
    io.print(pair((2, 3)))
    io.print(field(P{kind: Tri.Isosceles, n: 5}))
    io.print(field(P{kind: Tri.Scalene, n: 2}))
    io.print(field(P{kind: Tri.Scalene, n: 9}))
    io.print(entry({"k" => 2}))
    io.print(entry({"k" => 3}))
    io.print(nested(Some(Ev.B(6))))
    io.print(nested(None))
    io.print(whole(Ev.B(1)))
    io.print(first_num(Ev.B(11)))
    io.print(first_num(Ev.C("x")))
    io.print(iff(Ev.A(5)))
    io.print(iff(Ev.C("q")))
    io.print(plain(Two.R(4)))
    io.print(param(Two.L(1)))
    io.print(tup((5, 0)))
    io.print(tup((5, 6)))
    f = |.L(n) or .R(n): Two| n * 2
    io.print(f(Two.R(21)))
    io.print("${[Two.L(1), Two.R(2)] |> Iter.map(|.L(n) or .R(n)| n) |> Iter.to_list()}")
    io.print(fallback(Ok(Two.R(3))))
    io.print(fallback(Err("x")))
    io.print(fallback_name(Ok(1)))
    io.print(fallback_name(Err("x")))
}
`)
	want := `two equal
none equal
no triangle
positive 3
A -3
B -4
c
round-ish 1.5
flat 2.0
flat 3.0
other
small
negative
big
affirmative
negative
?
closer ] then 1
short
other
7
8
6
eq 5
small
other
low 2
other
6
none
numeric positive 1
11
-1
got 5
nothing
4
2
5
6
42
[1, 2]
3
0
1
5
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// In a test body: `assert` with an `or` pattern, a setup pattern and a
// `subtests` pattern whose alternatives together match every value.
func TestOrPattern_RunsInTests(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "or_test.nomi"
	p, err := vmhost.LoadSource(name, `enum Two {
    L Int
    R Int
}

test "assert binds from either alternative" {
    assert Some(n) or Some(n) = Some(3)
    assert n == 3
    assert .L(k) or .R(k) = Two.R(4)
    assert k == 4
}

test "assert fails when no alternative matches" {
    assert 1 or 2 = 3
}

group "setup" {
    setup {
        Two.R(7)
    }

    test "binds from the alternative that matched", .L(n) or .R(n) {
        assert n == 7
    }
}

test "subtests" {
    subtests [Two.L(1), Two.R(1)], .L(n) or .R(n) {
        assert n == 1
    }
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	out := buf.String()
	if strings.Contains(out, "BLOCKED") || !strings.Contains(out, "3 passed, 1 failed") ||
		!strings.Contains(out, "pattern did not match\n    assert 1 or 2 = 3") {
		t.Fatalf("want three passing cases and one pattern mismatch:\n%s", out)
	}
}
