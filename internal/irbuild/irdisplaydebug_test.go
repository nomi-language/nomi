package irbuild

import "testing"

// A value held as std's Display renders through Debug when nothing in the
// program implements or boxes into Display: a scalar, a Decimal and a std
// type enter it unboxed, so no impl names them, and each renders as its own
// Debug text. The Result reaches it through a `?` that widens a
// `List<String>` error into `List<Display>`, which `io.inspect` renders
// through std's List instance at `Display`.
func TestIRDisplayHeldRendersThroughDebug(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io
import std::calendar.Date

fn parse(s: String) -> Result<Int, List<String>>
    if s == "" then
        Err(["empty"])
    else
        Ok(1)
    end
end

fn run(s: String) -> Result<Int, List<Display>>
    n = parse(s)?
    Ok(n + 1)
end

fn main()
    io.inspect(run(""))
    io.inspect(run("x"))
    s: Display = "a"
    io.inspect(s)
    m: Display = 1.50d
    io.inspect(m)
    day: Display = Date.new(2026, 1, 2)!
    io.inspect(day)
    xs: List<Display> = ["a", 1, 2.5]
    io.inspect(xs)
    io.print(Debug.inspect(s))
end
`
	const want = "Err([\"empty\"])\n" +
		"Ok(2)\n" +
		"\"a\"\n" +
		"1.50d\n" +
		"2026-01-02\n" +
		"[\"a\", 1, 2.5]\n" +
		"\"a\"\n"
	irRunSource(t, src, want)
}
