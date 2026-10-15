package irbuild

import "testing"

// A function value whose type came from a function with defaults (a partial
// application that leaves a defaulted slot open) runs wherever a function
// value does: through a generic call, as a call's result called at once, in
// a Maybe and a list. A call through a declaration still omits the default.
func TestIRDefaultedFunctionType_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn connect(host: String, port: Int = 8080): String {
    "${host}:${port}"
}

fn id<T>(x: T): T {
    x
}

fn main() {
    c = connect(_, port: _)
    io.print(c("a"))
    r = id(c)
    io.print(r("b", 1))
    p = || connect(_, port: _)
    io.print(p()("c", 2))
    case Some(c) {
        Some(k) -> io.print(k("d", 3))
        None -> io.print("none")
    }
    fs = [c, connect(_, _)]
    io.print(fs |> Iter.map(|f| f("e", 4)) |> String.join(" "))
}
`
	irRunSource(t, src, "a:8080\nb:1\nc:2\nd:3\ne:4 e:4\n")
}
