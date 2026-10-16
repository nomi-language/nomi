package irbuild

import "testing"

// A block written where a type is expected gives its tail that type: an
// empty set as a `Set<Int>` field's value, and a list of two implementors
// as a `List<Display>` field's, each after a statement.
func TestIRBlockTailTakesTheExpectedType_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Hold {
    v: Set<Int>
}

struct Shown {
    v: List<Display>
}

fn main() {
    io.inspect(Hold{
        v: {
            io.print("in the block")
            #{}
        },
    })
    s = Shown{
        v: {
            io.print("in the other block")
            [1, "a"]
        },
    }
    io.print(s.v |> Iter.map(|d| Display.to_string(d)) |> String.join(","))
}
`
	irRunSource(t, src, "in the block\nHold{v: #{}}\nin the other block\n1,a\n")
}
