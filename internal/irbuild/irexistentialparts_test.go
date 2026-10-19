package irbuild

import "testing"

// An interface's existential and Unit are values a tuple, a record, a map's
// value, a `once` and a function value's parameter hold: `with_index` and
// `zip` pair a `List<Display>` or a `List<Unit>` into tuples, an annotated
// tuple, record and map hold Display, a tuple holds a Unit call's result, a
// `once` is Display or Unit (forced once), and `io.inspect` is passed over
// Unit elements.
func TestIRExistentialAndUnitParts_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

once shown: Display = 5

once setup: Unit = io.print("initialized")

fn u() {
}

fn main() {
    xs: List<Display> = [1, "a"]
    io.print(Iter.with_index(xs) |> Iter.map(fn #(i, d) -> "${Int.to_string(i)}=${Display.to_string(d)}") |> String.join(","))
    io.print(Iter.zip(xs, [7, 8]) |> Iter.map(fn #(d, n) -> "${Display.to_string(d)}/${Int.to_string(n)}") |> String.join(","))
    us = [u(), u()]
    io.inspect(Iter.with_index(us) |> Iter.to_list())
    io.inspect(Iter.zip(us, [1, 2]) |> Iter.count())
    t: #(Display, Int) = #(3, 4)
    #(td, ti) = t
    io.print("${Display.to_string(td)} ${Int.to_string(ti)}")
    r: #{d: Display, x: Int} = #{d: True, x: 2}
    io.print(Display.to_string(r.d))
    m: Map<String, Display> = Map.put(Map.empty(), "k", 1.5)
    io.print(Map.get(m, "k") |> Maybe.map(Display.to_string) |> Maybe.with_default(""))
    pair = #(1, io.print("side"))
    io.inspect(pair)
    us |> Iter.each(io.inspect)
    io.print(Display.to_string(shown))
    setup
    setup
    io.print("done")
}
`
	const want = "0=1,1=a\n" +
		"1/7,a/8\n" +
		"[#(0, Unit), #(1, Unit)]\n" +
		"2\n" +
		"3 4\n" +
		"True\n" +
		"1.5\n" +
		"side\n" +
		"#(1, Unit)\n" +
		"Unit\n" +
		"Unit\n" +
		"5\n" +
		"initialized\n" +
		"done\n"
	irRunSource(t, src, want)
}
