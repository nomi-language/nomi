package irbuild

import "testing"

// A partial of a function imported by name (`import lib.{add3}`) lowers as
// one written `lib.add3(_, 10)` does: placeholders by position and by
// name, an omitted literal default, a computed default lowered in the
// declaring file, a generic function, and a partial a closure captures.
func TestImportedFnPartial_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std/io
import lib.{add3, conn, pair, plain}

fn main() {
    f = add3(_, 10)
    io.inspect(f(2))
    g = add3(1, _, _)
    io.inspect(g(2))
    io.inspect(g(2, 3))
    h = add3(c: _, a: 1, _)
    io.inspect(h(5, 7))
    c = conn("h", _)
    io.print(c())
    io.print(c(1))
    p = pair(1, _)
    io.inspect(p())
    io.inspect(p(4))
    q = plain(_, "z")
    k = 3
    w = fn -> q(k)
    io.print(w())
}
`, "112\n103\n6\n13\nh:42\nh:1\n#(1, 1)\n#(1, 4)\n3z\n", map[string]string{"lib.nomi": `fn base() -> Int {
    40
}

pub fn add3(a: Int, b: Int, c: Int = 100) -> Int {
    a + b + c
}

pub fn conn(host: String, port: Int = base() + 2) -> String {
    "${host}:${port}"
}

pub fn pair<A>(x: A, y: A = x) -> #(A, A) {
    #(x, y)
}

pub fn plain(n: Int, s: String) -> String {
    "${n}${s}"
}
`})
}
