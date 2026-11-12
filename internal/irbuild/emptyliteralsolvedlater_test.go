package irbuild

import "testing"

// An empty literal inside a list, tuple or record literal whose type a
// later use fixes is built at the checked type: `[[]]` as the scrutinee of
// a function returning a Set<Int>, `(1, []).second` and a record's `[]` field
// stored in a `Set<Int>` field.
func TestIREmptyLiteralSolvedLater_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Hold
    v: Set<Int>
end

fn make() -> Set<Int>
    case [Set[]]
        [p] -> p
        _ -> todo
    end
end

fn main()
    io.inspect(make())
    io.inspect(Hold{v: (1, Set[]).second})
    r = {f: Set[], g: 0}
    io.inspect(Hold{v: r.f})
end
`
	irRunSource(t, src, "Set[]\nHold{v: Set[]}\nHold{v: Set[]}\n")
}
