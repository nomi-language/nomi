package irbuild

import "testing"

// A function with defaulted parameters named as a value is the function at
// its full parameter list where nothing shorter is expected (spec, "Defaults
// apply to calls"). The builder refused such a reference, in this file and
// from another file (`f = lib.greet`).
func TestDefaultedFuncRef_IsTheFullFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import {
    std/io
    lib
}

fn greet(name: String, punct: String = "!") -> String {
    name + punct
}

fn apply2(f: (String, String) -> String) -> String {
    f("x", "y")
}

fn main() {
    f = greet
    io.print(f("a", "?"))
    io.print(apply2(greet))
    g = lib.greet
    io.print(g("b", "."))
    io.print(apply2(lib.greet))
    io.print(greet("c"))
}
`, "a?\nxy\nb.\nxy\nc!\n", map[string]string{"lib.nomi": `pub fn greet(name: String, punct: String = "!") -> String {
    name + punct
}
`})
}

// Where a function type with fewer parameters is expected, the value is the
// function of its first parameters, and its call fills the rest from their
// defaults: a function in this file, another file's through its qualifier and
// through a bare import, an owner's (`String.join`), a std file's, at a
// generic std function's parameter, a user function's, an annotated binding,
// a named argument, a struct field, a list element, a return value and an if
// arm, down to no parameters at all.
func TestDefaultedFuncRef_FillsWhereFewerParametersAreExpected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import {
    std/io
    lib
    lib.shout
}

fn greet(name: String, punct: String = "!") -> String {
    name + punct
}

fn seed(n: Int = 7) -> Int {
    n
}

fn apply(f: (String) -> String, s: String) -> String {
    f(s)
}

struct Greeter {
    run: (String) -> String
}

fn pick(loud: Bool) -> (String) -> String {
    if loud { shout } else { greet }
}

fn main() {
    io.inspect(["a", "b"] |> Iter.map(greet) |> Iter.to_list())
    io.inspect(Maybe.map(Some("c"), lib.shout))
    io.inspect(["d"] |> Iter.map(shout) |> Iter.to_list())
    io.inspect([["e", "f"], ["g"]] |> Iter.map(String.join) |> Iter.to_list())
    f: (String) -> String = greet
    io.inspect(f("h"))
    io.inspect(apply(greet, "i"))
    io.inspect(apply(f: lib.shout, s: "j"))
    io.inspect((#Greeter{run: greet}).run("k"))
    fs: List<(String) -> String> = [greet, shout]
    io.inspect(fs |> Iter.map(fn g -> g("l")) |> Iter.to_list())
    io.inspect(pick(True)("m"))
    s: () -> Int = seed
    io.inspect(s())
}
`, `["a!", "b!"]
Some("c!!")
["d!!"]
["ef", "g"]
"h!"
"i!"
"j!!"
"k!"
["l!", "l!!"]
"m!!"
7
`, map[string]string{"lib.nomi": `pub fn shout(s: String, mark: String = "!!") -> String {
    s + mark
}
`})
}
