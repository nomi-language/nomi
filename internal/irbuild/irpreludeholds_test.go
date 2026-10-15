package irbuild

import "testing"

// A Range is a Maybe's or a Result's payload, built, matched, compared and
// inspected.
func TestIRRangeInAPreludeWrapper_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    io.inspect(Some(1..=4))
    case Some(1..4) {
        Some(x) -> io.inspect(x |> Iter.to_list())
        None -> io.print("none")
    }
    io.inspect(Some(1..4) == Some(1..4))
    io.inspect([Some(1..=2), None])
    r: Result<Range<Int>, String> = Ok(0..2)
    io.inspect(r)
}
`
	irRunSource(t, src, "Some(1..=4)\n[1, 2, 3]\nTrue\n[Some(1..=2), None]\nOk(0..2)\n")
}
