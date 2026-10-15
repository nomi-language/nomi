package irbuild

import "testing"

// A type argument nothing determines (spec, "Determined type arguments") is
// read as Unit, and an instance at Unit runs: `is_some(None)`, an argument
// that is Unit, a default in another file that calls a generic with a bare
// `None`, and a generic function value at Unit (`f: (Unit) -> Unit =
// ident`, `Iter.each(io.inspect)` over Units). A bounded type parameter is
// read as Int instead, since Unit implements no interface.
func TestUnsolvedTypeArg_AnInstanceAtUnitRuns(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std/io
    lib
}

fn is_some<T>(m: Maybe<T>): Bool {
    case m {
        Some(_) -> True
        None -> False
    }
}

fn ident<T>(x: T): T {
    x
}

fn twice<T>(x: T): List<T> {
    [x, x]
}

fn shown<T>(m: Maybe<T>, d: String): String where T: Display {
    case m {
        Some(x) -> T.to_string(x)
        None -> d
    }
}

fn u() {
}

fn main() {
    io.print(is_some(None))
    io.print(is_some(Some(u())))
    ident(u())
    io.inspect(twice(u()))
    io.inspect(ident(Ok(1)))
    io.print(shown(None, "none"))
    io.print(lib.show(1))
    f: (Unit) -> Unit = ident
    f(u())
    [u(), u()] |> Iter.each(io.inspect)
}
`, "False\nTrue\n[Unit, Unit]\nOk(1)\nnone\n1 False\nUnit\nUnit\n", map[string]string{"lib.nomi": `fn is_some<T>(m: Maybe<T>): Bool {
    case m {
        Some(_) -> True
        None -> False
    }
}

pub fn show(x: Int, flag: Bool = is_some(None)): String {
    "${x} ${flag}"
}
`})
}
