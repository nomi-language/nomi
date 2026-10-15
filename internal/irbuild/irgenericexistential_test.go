package irbuild

import "testing"

// An instance of a generic struct held as an interface's existential runs
// its own instance's impl: a derived and a hand-written Display, and a local
// interface's method, at two instances whose bodies differ, passed as an
// argument, bound under an annotation and held in a list.
func TestIRGenericInstanceExistential_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Box<T> {
    v: T
}

derive Display for Box<T>

struct Tag<T> {
    v: T
}

impl Display for Tag<T> {
    fn to_string(t: Tag<T>): String {
        "tag " + Debug.inspect(t.v)
    }
}

interface Named {
    fn name(x: self): String
}

impl Named for Box<T> {
    fn name(b: Box<T>): String {
        "box of " + Debug.inspect(b.v)
    }
}

fn show(d: Display): String {
    Display.to_string(d)
}

fn named(n: Named): String {
    Named.name(n)
}

fn main() {
    io.print(show(Box{v: 1}))
    io.print(show(Box{v: "s"}))
    io.print(show(Tag{v: 2}))
    io.print(show(Tag{v: "t"}))
    d: Display = Tag{v: True}
    io.print(Display.to_string(d))
    io.print(named(Box{v: 3}))
    io.print(named(Box{v: "u"}))
    xs: List<Named> = [Box{v: 4}, Box{v: 5}]
    io.print(xs |> Iter.map(Named.name) |> String.join(", "))
}
`
	const want = "Box{v: 1}\n" +
		"Box{v: s}\n" +
		"tag 2\n" +
		"tag \"t\"\n" +
		"tag True\n" +
		"box of 3\n" +
		"box of \"u\"\n" +
		"box of 4, box of 5\n"
	irRunSource(t, src, want)
}
