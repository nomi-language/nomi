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
    std::io
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
// a named argument, a pipe's head, a struct field, a list element, a return value and an if
// arm, down to no parameters at all.
func TestDefaultedFuncRef_FillsWhereFewerParametersAreExpected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import {
    std::io
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
    io.inspect(greet |> apply("n"))
    io.inspect((Greeter{run: greet}).run("k"))
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
"n!"
"k!"
["l!", "l!!"]
"m!!"
7
`, map[string]string{"lib.nomi": `pub fn shout(s: String, mark: String = "!!") -> String {
    s + mark
}
`})
}

// A pipe's head whose stage's first parameter names a type parameter is
// checked as the call's first argument is, so a function with defaults
// fills them there too, alone, in a list and in a list of tuples, beside a
// function with none and a lambda; each pipe prints what its call prints.
func TestDefaultedFuncRef_FillsAtAGenericPipeHead(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

fn run<T>(f: (String) -> T, s: String) -> T {
    f(s)
}

fn run_all<T>(fs: List<(String) -> T>, s: String) -> List<T> {
    fs |> Iter.map(fn f -> f(s)) |> Iter.to_list()
}

fn run_maybe<T>(f: (String) -> Maybe<T>, s: String) -> Maybe<T> {
    f(s)
}

fn run_pairs<K, T>(ps: List<(K, (String) -> T)>, s: String) -> List<(K, T)> {
    ps |> Iter.map(fn p -> (p.first, p.second(s))) |> Iter.to_list()
}

fn scale(s: String, by: Int = 10) -> Maybe<Int> {
    Some(String.length(s) * by)
}

fn twice(s: String) -> Maybe<Int> {
    Some(String.length(s) * 2)
}

fn size(s: String) -> Int {
    String.length(s)
}

fn main() {
    io.inspect(run(scale, "ab"))
    io.inspect(scale |> run("ab"))
    io.inspect(size |> run("abc"))
    io.inspect((fn s -> s + "!") |> run("x"))
    io.inspect(run_all([scale, twice], "a"))
    io.inspect([scale, twice] |> run_all("a"))
    io.inspect(scale |> run_maybe("abc"))
    io.inspect([("ten", scale), ("two", twice)] |> run_pairs("a"))
}
`, `Some(20)
Some(20)
3
"x!"
[Some(10), Some(2)]
[Some(10), Some(2)]
Some(30)
[("ten", Some(10)), ("two", Some(2))]
`, nil)
}
