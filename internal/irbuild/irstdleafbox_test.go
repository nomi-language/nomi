package irbuild

import "testing"

// A value of a std type that is no container and whose value names no
// Display body the VM can call (a Codepoint, a Duration, a Date) enters
// std's Display and Debug boxed, and a Byte or a Decimal enters Debug boxed:
// at an annotation, a branch join, a parameter, a declared result, a list
// element and `dbg`. Each renders as the direct call does: Display through
// std's impl, host function or body, and Debug through std's Debug.
func TestIRStdLeafErasure_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io
import std/duration.Duration
import std/calendar.{Date, Time}

fn show(d: Display) -> String {
    "<${d}>"
}

fn pick(n: Int) -> Display {
    case n {
        0 -> 'x'
        1 -> Duration.minutes(90)
        2 -> Date` + "`2026-01-02`" + `
        3 -> 7
        _ -> 2.5d
    }
}

fn main() {
    b: Byte = 0x41
    c: Display = 'c'
    io.print(Display.to_string(c))
    g: Debug = 'c'
    io.print(Debug.inspect(g))
    h: Hashable = 'c'
    io.inspect(Hashable.hash(h) == Hashable.hash('c'))
    db: Debug = b
    io.print(Debug.inspect(db))
    dd: Debug = 1.50d
    io.print(Debug.inspect(dd))
    io.print(show(Duration.seconds(5)))
    dg: Debug = Duration.seconds(2)
    io.print(Debug.inspect(dg))
    io.print(show(Date` + "`2026-05-04`" + `))
    up = True
    d: Display = if up { 'c' } else { "s" }
    io.print("${d}")
    io.print(show('q'))
    xs: List<Display> = ['a', Duration.seconds(1), Date` + "`2026-05-04`" + `, 3, Time` + "`10:30:00`" + `, b]
    io.print("${xs}")
    io.print([pick(0), pick(1), pick(2), pick(3), pick(4)] |> Iter.map(fn x -> Display.to_string(x)) |> String.join(", "))
    gs: List<Debug> = ['a', Duration.seconds(1), Date` + "`2026-05-04`" + `, 3, b, 1.5d]
    io.print(Debug.inspect(gs))
    dbg d
}
`
	irRunSource(t, src, "c\nCodepoint(99)\nTrue\n65\n1.50d\n<5s>\n2s\n<2026-05-04>\nc\n<q>\n"+
		"[a, 1s, 2026-05-04, 3, 10:30:00, 65]\nx, 1h30m, 2026-01-02, 7, 2.5\n"+
		"[Codepoint(97), 1s, 2026-05-04, 3, 65, 1.5d]\ndbg line 44: d = Codepoint(99)\n")
}
