package irbuild

import "testing"

// A deferred call's operands are evaluated where the `defer` stands, whatever
// computes them: another call, an `if` or a `case`, an embedded struct
// widened to its enum, a default, a `try` that leaves before the call is
// registered, and a named argument in a lambda's defer. A later rebinding of
// the local does not reach the deferred call.
func TestDeferOperands_AreEvaluatedAtTheDefer(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std/io

struct Spot {
    n: Int
}

enum Shape {
    embeds Spot
    Dot
}

fn show(s: Shape): Unit {
    io.inspect(s)
}

fn label(n: Int): String {
    io.print("label ${n}")
    "n=${n}"
}

fn get(n: Int): Result<Int, String> {
    if n > 0 { Ok(n) } else { Err("neg ${n}") }
}

fn note(s: String, suffix: String = "!"): Unit {
    io.print(s + suffix)
}

fn run(n: Int): Result<Int, String> {
    defer note("first")
    defer note(Int.to_string(try get(n)))
    Ok(n * 10)
}

fn main() {
    x = 1
    defer io.print(Debug.inspect(x))
    defer io.print(if x > 0 { "pos" } else { "neg" })
    defer io.print(case x {
        1 -> "one"
        _ -> "other"
    })
    sp = Spot{n: 3}
    defer show(sp)
    defer show(Spot{n: 4})
    defer io.print(label(x + 1))
    x = 5
    io.print("body ${x}")
    io.inspect(run(2))
    io.inspect(run(-1))
    f = |n: Int| {
        defer note(Int.to_string(n + 1), suffix: "?")
        n
    }
    io.inspect(f(4))
}
`, "label 2\nbody 5\n2!\nfirst!\nOk(20)\nfirst!\nErr(\"neg -1\")\n5?\n4\nn=2\nSpot{n: 4}\nSpot{n: 3}\none\npos\n1\n")
}
