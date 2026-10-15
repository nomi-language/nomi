package irbuild

import "testing"

// A partial application whose open slot has a computed default takes the
// callee's default on each call that omits the slot, evaluated in the
// callee's scope (a local `base` does not reach it) and from the arguments
// before the slot: over a function of this file, a generic one, an owner
// function, a lambda binding (whose default captured `n` when it was made)
// and another file's function, by name and qualified.
func TestPartialComputedDefault_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import {
    std/io
    lib
}

fn base(): Int {
    io.print("base")
    10
}

fn conn(host: String, port: Int = base() + 1): String {
    "${host}:${port}"
}

fn span(a: Int, b: Int = a * 2, c: Int = a + b): (Int, Int, Int) {
    (a, b, c)
}

fn pair<A>(x: A, y: A = x): (A, A) {
    (x, y)
}

struct Box {
    v: Int
}

impl Box {
    fn make(a: Int, b: Int = a * 2): Box {
        Box{v: a + b}
    }
}

fn main() {
    c = conn("h", _)
    io.print(c(3))
    io.print(c())
    base = 99
    io.print("${base}")
    d = conn(_, _)
    io.print(d("x"))
    io.print(d("y", port: 5))
    s = span(4, _, _)
    io.inspect(s())
    io.inspect(s(1))
    t = span(_, c: 7, _)
    io.inspect(t(3))
    p = pair(1, _)
    io.inspect(p())
    m = Box.make(1, _)
    io.inspect(m())
    n = 5
    cf = |x: Int, y: Int = x + n| x * y
    n = 100
    q = cf(2, _)
    io.inspect(q())
    io.inspect(q(3))
    g = || q()
    io.inspect(g())
    l = lib.dial("l", _)
    io.print(l())
    io.print(l(1))
    io.inspect(n)
}
`, "h:3\nbase\nh:11\n99\nbase\nx:11\ny:5\n(4, 8, 12)\n(4, 1, 5)\n(3, 6, 7)\n(1, 1)\nBox{v: 3}\n14\n6\n14\n"+
		"lib base\nl:42\nl:1\n100\n", map[string]string{"lib.nomi": `import std/io

fn base(): Int {
    io.print("lib base")
    40
}

pub fn dial(host: String, port: Int = base() + 2): String {
    "${host}:${port}"
}
`})
}
