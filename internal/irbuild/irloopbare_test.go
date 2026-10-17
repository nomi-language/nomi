package irbuild

import "testing"

// A loop with no `break` called by its selectively imported bare name, or by
// an alias, is std's `Iter.loop` by the checker's reference at the name, so it
// lowers as the qualified loop does: a statement ending main, an Int
// function's tail, and a binding with code after it that never runs.
func TestIRLoopEndless_BareAndAliasedLoop(t *testing.T) {
	verifyEndlessTrap(t, `import {
    std/io
    std/iter.Iter.{loop, loop as forever}
}

fn spin() -> Int {
    forever(fn n = 0 -> {
        io.print("spin ${n}")
        n + 1
    })
}

fn main() {
    x = loop(fn n = 0 -> {
        if n == 2 { return 10 }
        if n == 11 { io.print(1 / (n - 11)) }
        io.print("bound ${n}")
        n + 1
    })
    io.inspect(x)
    io.inspect(spin())
}
`, "line 16: division by zero", "bound 0\nbound 1\nbound 10\n")
	verifyEndlessTrap(t, `import {
    std/io
    std/iter.Iter.{loop as forever}
}

fn main() {
    io.print("start")
    forever(fn -> {
        io.print("again")
        io.print(1 / 0)
    })
}
`, "line 10: division by zero", "start\nagain\n")
}

// A local function named `loop` is an ordinary call: the statement after it
// runs.
func TestIRLoop_LocalFunctionNamedLoopIsACall(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn loop(f: () -> Unit) {
    f()
    f()
}

fn main() {
    loop(fn -> {
        io.print("called")
    })
    io.print("after")
}
`, "called\ncalled\nafter\n")
}
