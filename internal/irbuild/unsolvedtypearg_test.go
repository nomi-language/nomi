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

fn is_some<T>(m: Maybe<T>) -> Bool {
    case m {
        Some(_) -> True
        None -> False
    }
}

fn ident<T>(x: T) -> T {
    x
}

fn twice<T>(x: T) -> List<T> {
    [x, x]
}

fn shown<T>(m: Maybe<T>, d: String) -> String where T: Display {
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
`, "False\nTrue\n[Unit, Unit]\nOk(1)\nnone\n1 False\nUnit\nUnit\n", map[string]string{"lib.nomi": `fn is_some<T>(m: Maybe<T>) -> Bool {
    case m {
        Some(_) -> True
        None -> False
    }
}

pub fn show(x: Int, flag: Bool = is_some(None)) -> String {
    "${x} ${flag}"
}
`})
}

// A generic type's argument nothing determines is a fresh inference
// variable in the checker, filled by its position in the builder: a struct
// variant literal (`#Either2.L{a: 1}`), a positional variant (`E.X(1)`), a
// payload-free one (`E.Z`, an imported `Empty`), a struct whose parameter
// no field names (`#Tag{n: 3}`), and a literal passed where a generic
// callee's own parameter is expected (`left(#Either2.R{b: "s"})`). Two
// variants that each fix one argument meet in a list.
func TestUnsolvedTypeArg_AGenericLiteralWithAnOpenArgumentRuns(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std/io
    shapes.Holder.{Empty}
    shapes
}

enum E<A, B> {
    X(A)
    Y(B)
    Z
}

enum Either2<A, B> {
    L {
        a: A
    }
    R {
        b: B
    }
}

struct Tag<T> {
    n: Int
}

fn left<A, B>(e: Either2<A, B>) -> Bool {
    case e {
        #.L{a: _} -> True
        #.R{b: _} -> False
    }
}

fn tag<T>(e: E<T, String>) -> String {
    case e {
        .X(_) -> "x"
        .Y(s) -> s
        .Z -> "z"
    }
}

fn main() {
    l = #Either2.L{a: 1}
    io.inspect(l)
    io.print(left(l))
    io.print(left(#Either2.R{b: "s"}))
    io.inspect([#Either2.L{a: 1}, #Either2.R{b: "s"}])
    io.print(tag(E.Y("y")))
    io.print(tag(E.Z))
    io.inspect(E.X(1))
    io.inspect(#(E.Z, 1))
    io.inspect(Empty)
    io.inspect(shapes.Holder.Empty)
    io.inspect(#Tag{n: 3})
}
`, "#L{a: 1}\nTrue\nFalse\n[#L{a: 1}, #R{b: \"s\"}]\ny\nz\nX(1)\n#(Z, 1)\nEmpty\nEmpty\n#Tag{n: 3}\n", map[string]string{"shapes.nomi": `pub enum Holder<T> {
    Of T
    Empty
}
`})
}

// Another file's generic function called with a type argument nothing
// determines is filled as a local one's is, and solved in its own file:
// `cnt([])` imported bare, `lib.is_some(None)`, and `lib.shown(None)` with
// a bounded parameter.
func TestUnsolvedTypeArg_ASiblingGenericCallRuns(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std/io
    lib.{cnt}
    lib
}

fn main() {
    io.print(cnt([]))
    io.print(cnt([1, 2]))
    io.print(lib.is_some(None))
    io.print(lib.shown(None, "none"))
    io.print(lib.shown(Some(4), "none"))
}
`, "0\n2\nFalse\nnone\n4\n", map[string]string{"lib.nomi": `pub fn cnt<T>(xs: List<T>) -> Int {
    Iter.count(xs)
}

pub fn is_some<T>(m: Maybe<T>) -> Bool {
    case m {
        Some(_) -> True
        None -> False
    }
}

pub fn shown<T>(m: Maybe<T>, d: String) -> String where T: Display {
    case m {
        Some(x) -> T.to_string(x)
        None -> d
    }
}
`})
}
