package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Spec §8 *Type Narrowing*: only an embedded struct or distinct type narrows.
// Bool embeds the host singletons True and False, and narrowing there made
// `x` in a `True -> assert x` arm a `True`, which `assert` rejected
// ("expects Bool, Result, Maybe, or Assertable, got True").
func TestTypeNarrowing_ABoolArmLeavesTheNameABool(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "bool_arm_test.nomi"
	p, err := vmhost.LoadSource(name, `test "t" {
    x = 1 == 1
    case x {
        True -> assert x
        False -> assert True
    }
}

test "f" {
    x = 1 == 2
    case x {
        True -> assert False
        False -> refute x
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
	if strings.Contains(out, "BLOCKED") || !strings.Contains(out, "2 passed, 0 failed") {
		t.Fatalf("want two passing cases:\n%s", out)
	}
}

// A name matched against a plain, payload or struct-shaped variant, or a
// prelude Bool, Maybe or Result variant, keeps its enum type in the arm, so
// it flows where that enum is expected.
func TestTypeNarrowing_APlainVariantArmKeepsTheEnum(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Circle {
    radius: Float
}

enum Shape {
    Dot
    Round Float
    Rect { w: Int }
    Wrapped Circle
}

fn bool_text(b: Bool): String {
    if b { "yes" } else { "no" }
}

fn shape_text(s: Shape): String {
    case s {
        .Dot -> "dot"
        .Round(r) -> "round ${r}"
        .Rect{w} -> "rect ${w}"
        .Wrapped(c) -> "wrapped ${c.radius}"
    }
}

fn result_text(r: Result<Int, String>): String {
    case r {
        Ok(n) -> "ok ${n}"
        Err(e) -> "err ${e}"
    }
}

fn bools(x: Bool): String {
    case x {
        True -> bool_text(x)
        False -> bool_text(x)
    }
}

fn maybes(m: Maybe<Int>): String {
    case m {
        Some(_) -> Maybe.with_default(m, 0) |> Int.to_string()
        None -> Maybe.with_default(m, -1) |> Int.to_string()
    }
}

fn results(r: Result<Int, String>): String {
    case r {
        Ok(_) -> result_text(r)
        Err(_) -> result_text(r)
    }
}

fn shapes(s: Shape): String {
    case s {
        .Dot -> shape_text(s)
        .Round(_) -> shape_text(s)
        .Rect{w: _} -> shape_text(s)
        .Wrapped(_) -> shape_text(s)
    }
}

fn main() {
    io.print("${bools(True)} ${bools(False)}")
    io.print("${maybes(Some(4))} ${maybes(None)}")
    io.print("${results(Ok(1))} ${results(Err("e"))}")
    io.print(shapes(Shape.Dot))
    io.print(shapes(Shape.Round(1.5)))
    io.print(shapes(Shape.Rect{w: 3}))
    io.print(shapes(Shape.Wrapped(Circle{radius: 2.0})))
}
`)
	want := `yes no
4 -1
ok 1 err e
dot
round 1.5
rect 3
wrapped 2.0
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
