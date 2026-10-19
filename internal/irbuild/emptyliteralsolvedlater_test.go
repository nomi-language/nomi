package irbuild

import "testing"

// An empty literal inside a list, tuple or record literal whose type a
// later use fixes is built at the checked type: `[%[]]` as the scrutinee of
// a function returning a Set<Int>, `#(1, %[]).1` and a record's `%[]` field
// stored in a `Set<Int>` field.
func TestIREmptyLiteralSolvedLater_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Hold {
    v: Set<Int>
}

fn make() -> Set<Int> {
    case [%[]] {
        [p] -> p
        _ -> todo
    }
}

fn main() {
    io.inspect(make())
    io.inspect(#Hold{v: #(1, %[]).1})
    r = #{f: %[], g: 0}
    io.inspect(#Hold{v: r.f})
}
`
	irRunSource(t, src, "%[]\n#Hold{v: %[]}\n#Hold{v: %[]}\n")
}
