package irbuild

import "testing"

// A generic caller passes a callback to a generic callee whose callback
// parameter types are the callee's type parameters. The callback's
// parameters meet the callee's the other way round (contravariant), and the
// caller's own `E` there is not a parameter the call solves: `call` is
// instantiated at `T = E, U = List<E>` (and `Maybe<E>`), and `apply`'s `T`
// is solved to `E` from the annotated `fn _x: E ->` before any argument solves
// it.
func TestIRGenericCallbackOverCallerTypeParam_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn call<T, U>(item: T, initial: U, f: (U, T) -> U) -> U {
    f(initial, item)
}

fn apply<T>(f: (T) -> Int, x: T) -> Int {
    f(x)
}

fn b<E>(item: E, list: List<E>) -> List<E> {
    call(item, list, fn rest, _item -> rest)
}

fn e<E>(item: E, n: Int) -> Int {
    call(item, n, fn rest, _item -> rest)
}

fn m<E>(item: E, opt: Maybe<E>) -> Maybe<E> {
    call(item, opt, fn _rest, item -> Some(item))
}

fn k<E>(item: E) -> Int {
    apply(fn _x: E -> 7, item)
}

fn main() {
    io.inspect(e(1, 3))
    io.inspect(b(1, [2]))
    io.inspect(m("a", None))
    io.inspect(k("a"))
}
`
	irRunSource(t, src, "3\n[2]\nSome(\"a\")\n7\n")
}
