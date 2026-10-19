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

fn connect(host: String, port: Int = 8080) -> String {
    "${host}:${port}"
}

fn id<T>(x: T) -> T {
    x
}

fn main() {
    c = &connect(&1, port: &2)
    io.print(c("a"))
    r = id(c)
    io.print(r("b", 1))
    p = fn -> &connect(&1, port: &2)
    io.print(p()("c", 2))
    case Some(c) {
        Some(k) -> io.print(k("d", 3))
        None -> io.print("none")
    }
    fs = [c, &connect(&1, &2)]
    io.print(fs |> Iter.map(fn f -> f("e", 4)) |> String.join(" "))
}
`
	irRunSource(t, src, "a:8080\nb:1\nc:2\nd:3\ne:4 e:4\n")
}

// The callees that must pass every argument (a pipe through a nameless
// value, an `if` that picks a function, a `once` holding a partial or a
// lambda) run when they do.
func TestIRDefaultedFunctionType_NamelessCalleesRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn connect(host: String, port: Int = 8080) -> String {
    "${host}:${port}"
}

fn other(host: String, port: Int = 1) -> String {
    "${host}/${port}"
}

fn id<T>(x: T) -> T {
    x
}

once oc = &connect(&1, port: &2)

once ol = fn s: String, n: Int = 5 -> "${s}${n}"

fn main() {
    c = &connect(&1, port: &2)
    d = &other(&1, port: &2)
    r = id(c)
    io.print("a" |> r(1))
    b = False
    io.print((if b { c } else { d })("b", 2))
    io.print(oc("c", 3))
    io.print("d" |> oc(4))
    io.print(ol("e", 6))
    io.print("f" |> c())
}
`
	irRunSource(t, src, "a:1\nb/2\nc:3\nd:4\ne6\nf:8080\n")
}
