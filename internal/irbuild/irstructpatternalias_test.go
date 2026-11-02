package irbuild

import "testing"

// A struct pattern written with an alias of the value's struct runs, in a
// binding and in a case arm. The builder compared the written name with the
// struct's own and declined `Human{age} = person`; the checker now holds the
// name to the value's struct, so the builder reads only the fields.
func TestIRStructPatternThroughAnAlias_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Person {
    name: String
    age: Int
}

typealias Human Person

fn main() {
    p = Person{name: "Lin", age: 3}
    Human{age} = p
    io.print(age)
    r = case p {
        Human{name, age: 3} -> name
        _ -> "other"
    }
    io.print(r)
}
`
	irRunSource(t, src, "3\nLin\n")
}
