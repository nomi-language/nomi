package irbuild

import (
	"strings"
	"testing"
)

// TestVM_PartialQualified runs `_` partial application over callees the
// builder reads off the checker's instantiated signature: an
// interface-qualified call on a local and on an app field, an owner-qualified
// call, a generic function and a stdlib owner function. The checker used to
// type a generic call's partial as the callee's result
// (`Console.write_line(c, _)` was Unit), and the builder lowered only a
// partial over a same-file non-generic function.
func TestVM_PartialQualified(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"> a",
		"> b",
		"> c",
		"> d",
		"app> e",
		"app> f",
		"(1, 2)",
		"[(0, 3), (0, 4)]",
		`["xxx"]`,
		"",
	}, "\n")
	if got := vmReference(fixture("partial_qualified.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s", got, want)
	}
}

// A partial over a generic function inside the block `Iter.count` is given:
// the checker reads that block's type before it checks the block against
// the parameter, so it checks `q`'s binding twice. The partial's `Ok(1)`
// leaves E open, and the second check solves it through `q`'s first type,
// which the call `q([Ok<Int, String>(2)])` solved, so the builder sees
// the partial's whole signature.
func TestVM_PartialInABlockCheckedTwice(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn pick<T>(c: Bool, a: T, b: T): T {
    if c { a } else { b }
}

fn main() {
    n = Iter.count({
        q = pick(True, _, [Ok(1)])
        q([Ok<Int, String>(2), Ok(3)])
    })
    io.inspect(n)
    m = Iter.count({
        q = pick(False, _, [Ok(1)])
        q([Ok<Int, String>(2), Ok(3)])
    })
    io.inspect(m)
}
`
	irRunSource(t, src, "2\n1\n")
}
