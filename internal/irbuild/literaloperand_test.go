package irbuild

import "testing"

// A collection literal beside a value of a collection type is built as that
// type and runs: the operand of `+` on either side, over a List, a Vector
// and a Map, and the elements of a list literal beside a spread, each
// holding a function whose result converts to the interface the other
// side's alias returns, and a list of Ints joined to a list of Displays.
func TestIRLiteralBuiltAsTheOtherOperand_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

interface Reply {
    fn body(r: self) -> String
}

struct Page {
    text: String
}

impl Reply for Page {
    fn body(p: Page) -> String {
        p.text
    }
}

typealias Handler (Int) -> Reply

fn page(n: Int) -> Page {
    Page{text: "page ${n}"}
}

fn wrap(h: Handler) -> Handler {
    fn n -> h(n * 10)
}

fn routes() -> List<(String, Handler)> {
    [("a", page)] |> Iter.map(fn (name, h) -> (name, wrap(h))) |> Iter.to_list()
}

fn served(rs: Iter<(String, Handler)>) -> List<String> {
    rs |> Iter.map(fn (name, h) -> "${name}: ${Reply.body(h(1))}") |> Iter.to_list()
}

fn main() {
    io.inspect(served([("b", page)] + routes()))
    io.inspect(served(routes() + [("b", page)]))
    io.inspect(served([("b", page), ..routes()]))
    io.inspect(served([..routes(), ("b", page)]))
    vs: Vector<(String, Handler)> = #[("a", wrap(page))]
    io.inspect(served(#[("b", page)] + vs))
    ms: Map<String, Handler> = %{"a" => wrap(page)}
    joined = %{"b" => page} + ms
    io.inspect(served(joined))
    ds: List<Display> = ["x"]
    io.inspect([1, 2] + ds |> Iter.map(Display.to_string) |> Iter.to_list())
}
`
	irRunSource(t, src, `["b: page 1", "a: page 10"]
["a: page 10", "b: page 1"]
["b: page 1", "a: page 10"]
["a: page 10", "b: page 1"]
["b: page 1", "a: page 10"]
["b: page 1", "a: page 10"]
["1", "2", "x"]
`)
}
