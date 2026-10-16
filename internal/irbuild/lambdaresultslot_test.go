package irbuild

import "testing"

// A lambda or nested fn whose declared result is an interface, with a tail
// `case` whose arms are an implementor and a `todo`, runs: the arms write
// a slot of the declared type, so the implementor enters it by erasure and
// the `todo` agrees with it. The slot was once untyped, took the first
// arm's Pt, and the `todo`'s existential did not lint against it.
func TestIRLambdaTailRegionWritesTheDeclaredResult_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

interface Named {
    fn name(value: self): String
}

struct Pt {
    x: Int
}

impl Named for Pt {
    fn name(p: Pt): String {
        "pt${p.x}"
    }
}

fn main() {
    fn named(): Named {
        case [Pt{x: 5}] {
            [p] -> p
            _ -> todo
        }
    }

    later: () -> Named = || case 1 {
        1 -> Pt{x: 7}
        _ -> todo
    }

    io.print(Named.name(named()))
    io.print(Named.name(later()))
}
`
	irRunSource(t, src, "pt5\npt7\n")
}
