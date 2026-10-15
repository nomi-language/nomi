package irbuild

import "testing"

// A slot a partial application does not mention takes the callee's default
// when the partial is called, evaluated in the callee's scope once per
// call, whatever the default is. Only a literal default was admitted, so
// `conn(_)` over `port: Int = base() + 1` declined. The body's call now
// leaves the slot out: a trailing one unwritten, one before a written slot
// skipped by writing the call's arguments by name.
func TestPartialOmittedDefault_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std/io

fn base(): Int {
    io.print("base")
    10
}

fn conn(host: String, port: Int = base() + 1, tls: Bool = False): String {
    "${host}:${port}:${tls}"
}

fn tag<T>(x: T, label: String = "t${base()}", n: Int = 0): String where T: Display {
    "${label}=${x}/${n}"
}

fn main() {
    c = conn(_)
    io.print(c("h"))
    io.print(c("i"))
    d = conn(_, tls: True)
    io.print(d("j"))
    e = conn(tls: True, host: _)
    io.print(e("k"))
    f = tag(_, n: 2)
    io.print(f(5))
    l = |a: Int, b: Int = a * 2, z: Int = 1| a + b + z
    g = l(_, z: 100)
    io.print(g(1))
}
`, "base\nh:11:False\nbase\ni:11:False\nbase\nj:11:True\nbase\nk:11:True\nbase\nt10=5/2\n103\n")
}
