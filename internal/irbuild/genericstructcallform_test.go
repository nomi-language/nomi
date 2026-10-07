package irbuild

import "testing"

// The call form of a generic struct runs at the instance its fields solve
// (spec §7 Construction): over a record literal, with a default filled, and
// over a record value, whose field kinds solve the instance.
func TestIRGenericStructCallForm_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Box<T> {
    v: T
}

struct Pair<A, B> {
    a: A
    b: B
    note: String = "none"
}

fn defaults(): {a: Int, b: String} {
    {a: 1, b: "x"}
}

fn main() {
    io.inspect(Box({v: 1}))
    io.inspect(Box({v: Some("a")}).v)
    io.inspect(Pair({a: 1.5, b: [True]}))
    io.inspect(Pair(defaults()))
    rec = {v: (1, "z")}
    io.inspect(Box(rec))
}
`
	const want = "Box{v: 1}\n" +
		"Some(\"a\")\n" +
		"Pair{a: 1.5, b: [True], note: \"none\"}\n" +
		"Pair{a: 1, b: \"x\", note: \"none\"}\n" +
		"Box{v: (1, \"z\")}\n"
	irRunSource(t, src, want)
}
