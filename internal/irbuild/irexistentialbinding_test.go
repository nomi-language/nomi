package irbuild

import "testing"

// A name bound under an interface annotation has the interface's type, so a
// record built from it has the record type the checker gives it, and a field
// accessor over that record runs.
func TestIRExistentialBindingHoldsTheInterface_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    d: Display = 1
    rs = [{x: 1, d: d}, {x: 2, d: d}]
    io.inspect(rs |> Iter.map(.x) |> Iter.to_list())
    io.print(rs |> Iter.map(|r| Display.to_string(r.d)) |> String.join(","))
}
`
	irRunSource(t, src, "[1, 2]\n1,1\n")
}

// A list literal of mixed concrete values runs where a `List` of an
// interface is expected without an annotation of its own: a struct field
// and an argument. `Task.spawn_all` takes a function whose parameter is an
// interface the source's elements implement.
func TestIRExistentialElementPositions_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io
import std/tasks.Task

struct Holder {
    ds: List<Display>
}

fn all(ds: List<Display>): String {
    Iter.map(ds, Display.to_string) |> String.join(",")
}

fn show(x: Display): String {
    "<" + Display.to_string(x) + ">"
}

fn main() {
    h = Holder{ds: [1, "a"]}
    io.print(all(h.ds))
    io.print(all([True, 2.5, "b"]))
    xs = concurrent {
        ts = Task.spawn_all([1, 2, 3], show, max_running: 2)
        Task.await_all(ts)
    }
    io.inspect(xs)
}
`
	irRunSource(t, src, "1,a\nTrue,2.5,b\n[\"<1>\", \"<2>\", \"<3>\"]\n")
}
