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

fn base() -> Int {
    io.print("base")
    10
}

fn conn(host: String, port: Int = base() + 1) -> String {
    "${host}:${port}"
}

fn span(a: Int, b: Int = a * 2, c: Int = a + b) -> #(Int, Int, Int) {
    #(a, b, c)
}

fn pair<A>(x: A, y: A = x) -> #(A, A) {
    #(x, y)
}

struct Box {
    v: Int
}

impl Box {
    fn make(a: Int, b: Int = a * 2) -> Box {
        #Box{v: a + b}
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
    cf = fn x: Int, y: Int = x + n -> x * y
    n = 100
    q = cf(2, _)
    io.inspect(q())
    io.inspect(q(3))
    g = fn -> q()
    io.inspect(g())
    l = lib.dial("l", _)
    io.print(l())
    io.print(l(1))
    io.inspect(n)
}
`, "h:3\nbase\nh:11\n99\nbase\nx:11\ny:5\n#(4, 8, 12)\n#(4, 1, 5)\n#(3, 6, 7)\n#(1, 1)\n#Box{v: 3}\n14\n6\n14\n"+
		"lib base\nl:42\nl:1\n100\n", map[string]string{"lib.nomi": `import std/io

fn base() -> Int {
    io.print("lib base")
    40
}

pub fn dial(host: String, port: Int = base() + 2) -> String {
    "${host}:${port}"
}
`})
}

// A partial whose open slot's computed default reads a slot left open by a
// placeholder written after it, `span(b: _, _)`: the partial's parameters
// are its placeholders in written order, `#(b, a)`, and a call omitting b
// fills it after a, as the callee does. So do a partial of that partial,
// a closure calling it, a three-slot callee whose placeholders are written
// in reverse, and a lambda callee.
func TestPartialComputedDefault_ALaterPlaceholderFillsFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std/io

fn span(a: Int, b: Int = a * 2) -> #(Int, Int) {
    #(a, b)
}

fn three(a: Int, b: Int = a + 1, c: Int = a + b) -> #(Int, Int, Int) {
    #(a, b, c)
}

fn main() {
    u = span(b: _, _)
    io.inspect(u(1, 2))
    io.inspect(u(a: 5))
    w = u(_, 7)
    io.inspect(w(3))
    io.inspect(w())
    f = fn -> u(a: 9)
    io.inspect(f())
    t = three(c: _, b: _, _)
    io.inspect(t(a: 1))
    io.inspect(t(b: 10, a: 1))
    io.inspect(t(100, a: 1))
    cf = fn x: Int, y: Int = x + 1 -> x * 10 + y
    q = cf(y: _, _)
    io.inspect(q(x: 4))
    io.inspect(q(0, 4))
}
`, "#(2, 1)\n#(5, 10)\n#(7, 3)\n#(7, 14)\n#(9, 18)\n#(1, 2, 3)\n#(1, 10, 11)\n#(1, 2, 100)\n45\n40\n")
}
