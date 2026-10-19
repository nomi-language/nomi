package irbuild

import "testing"

// A `Struct.update` patch that is a value rather than a literal runs: a
// binding, a call, a tuple field, named arguments with the patch first, and
// an empty list read back at the field's element type. The operands run in
// Nomi's order.
func TestIRStructUpdatePatchValue_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct User {
    name: String
    age: Int
    tags: List<String>
}

fn older() -> #{age: Int} {
    io.print("patch")
    #{age: 40}
}

fn base() -> User {
    io.print("base")
    #User{name: "z", age: 0, tags: ["t"]}
}

fn main() {
    u = #User{name: "a", age: 1, tags: ["u"]}
    patch = #{name: "b"}
    io.inspect(Struct.update(u, patch))
    io.inspect(Struct.update(u, older()))
    io.inspect(Struct.update(updates: older(), original: base()))
    both = #{age: 2, name: "c"}
    io.inspect(Struct.update(original: u, updates: both))
    p = #(1, #{tags: ["x", "y"]})
    io.inspect(Struct.update(u, p.1))
    cleared: #{tags: List<String>} = #{tags: []}
    io.inspect(Struct.update(u, cleared))
}
`
	const want = "#User{name: \"b\", age: 1, tags: [\"u\"]}\n" +
		"patch\n" +
		"#User{name: \"a\", age: 40, tags: [\"u\"]}\n" +
		"patch\n" +
		"base\n" +
		"#User{name: \"z\", age: 40, tags: [\"t\"]}\n" +
		"#User{name: \"c\", age: 2, tags: [\"u\"]}\n" +
		"#User{name: \"a\", age: 1, tags: [\"x\", \"y\"]}\n" +
		"#User{name: \"a\", age: 1, tags: []}\n"
	irRunSource(t, src, want)
}
