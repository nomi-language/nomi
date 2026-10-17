package irbuild

import "testing"

// A function with defaulted parameters named as a value is the function at
// its full parameter list, since defaults apply to calls and not to function
// values (spec, "Defaults apply to calls"). The builder refused such a
// reference, in this file and from another file (`f = lib.greet`).
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
