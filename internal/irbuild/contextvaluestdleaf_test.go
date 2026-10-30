package irbuild

import "testing"

// A Context value keyed by one of std's rt-backed types runs: an opaque
// one (`Duration`, `Codepoint`) and a host type (`Regex`), named bare after
// an import, so the witness's type comes from the checker. Each one is its
// own slot, apart from Int's.
func TestIRContextValueStdLeafWitness_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io
import std/duration.Duration
import std/regex.Regex

fn main() {
    io.inspect(Context.value(Context.root(), Duration))
    c = Context.with_value(Context.root(), Duration.seconds(2))
    io.inspect(Context.value(c, Duration))
    io.inspect(Context.value(c, Int))
    case Regex.compile("a+") {
        Ok(re) -> {
            c2 = Context.with_value(c, re)
            io.inspect(Maybe.is_some(Context.value(c2, Regex)))
            io.inspect(Context.value(c2, Duration))
        }
        Err(_) -> io.print("bad")
    }
    io.inspect(Context.value(Context.with_value(c, 'x'), Codepoint))
}
`
	irRunSource(t, src, "None\nSome(2s)\nNone\nTrue\nSome(2s)\nSome(Codepoint(120))\n")
}
