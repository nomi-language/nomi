package irbuild

import "testing"

// A Range is built over every Comparable element, whose order the
// comparator Range.contains builds gives: a List of Lists, Bytes, std's
// Reverse, a Vector of a declared struct, a List of Durations, of a declared
// enum and of Reverse.
func TestIRRangeOverEveryComparable_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import
    std::io
    std::duration.Duration
    std::reverse.Reverse
end

struct P
    x: Int
end

derive Comparable for P

enum E
    A
    B(Int)
end

derive Comparable for E

fn main()
    g = [[1]]..[[2]]
    io.inspect(Range.contains(g, [[1, 5]]))
    h = Bytes.from_list([])..Bytes.from_list([])
    io.inspect(Range.contains(h, Bytes.from_list([])))
    r = Reverse{value: 2}..Reverse{value: 0}
    io.inspect(Range.contains(r, Reverse{value: 1}))
    v: Vector<P> = [P{x: 1}]
    w: Vector<P> = [P{x: 3}]
    io.inspect(Range.contains(v..w, [P{x: 2}]))
    x = [Duration.seconds(1)]..[Duration.seconds(3)]
    io.inspect(Range.contains(x, [Duration.seconds(2)]))
    y = [E.A]..[E.B(2)]
    io.inspect(Range.contains(y, [E.B(1)]))
    z = [Reverse{value: 1}]..[Reverse{value: 0}]
    io.inspect(Range.contains(z, [Reverse{value: 0}]))
end
`
	irRunSource(t, src, "True\nFalse\nTrue\nTrue\nTrue\nTrue\nFalse\n")
}
