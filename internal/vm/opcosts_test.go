package vm_test

// Allocations per operation on the paths the benchmark profiles found hot
// (benchmarks/FINDINGS.md): a struct update, a cons, a host call. Each count
// is allocsPerActivation's difference between two runs (callalloc_test.go),
// so it is the operation's own cost plus the activation around it, which
// allocates nothing.

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A struct update clones its base once and writes each new value into its
// slot from its register: one allocation, with no index or value slice and
// no Float boxed on the way into a Float slot.
func TestOpCosts_AStructUpdateAllocatesItsCloneOnly(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"three words", `import std::io

struct P
    x: Float
    y: Float
    n: Int
end

fn down(n: Int, p: P) -> Int
    if n < 1 then p.n else down(n - 1, {..p, x: p.x + 1.5, n: 1})
end

fn main()
    down(%d, P{x: 0.0, y: 0.0, n: 0}) |> io.print()
end
`},
		// Seven Floats, n-body's Body: one allocation still, since a record
		// of up to eight words takes them with its header.
		{"seven words", `import std::io

struct Body
    x: Float
    y: Float
    z: Float
    vx: Float
    vy: Float
    vz: Float
    mass: Float
end

fn down(n: Int, b: Body) -> Int
    if n < 1 then 0 else down(n - 1, {..b, vx: b.vx - b.x * 0.5, vy: b.vy + 0.25})
end

fn main()
    down(%d, Body{x: 1.0, y: 0.0, z: 0.0, vx: 0.0, vy: 0.0, vz: 0.0, mass: 1.0}) |> io.print()
end
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if per := allocsPerActivation(t, tc.src); per > 1.1 {
				t.Errorf("one struct update allocates %.2f times, want 1 (its clone). The likely "+
					"causes: the update running through its boxed handler (makeValue) instead of "+
					"opUpdate, an update cache that misses on every run, or a record of this many "+
					"words taking its word slice separately (rt.allocRecord)", per)
			}
		})
	}
}

// The update compiles to opUpdate, not to the boxed handler.
func TestOpCosts_AStructUpdateCompilesToUpdate(t *testing.T) {
	p, err := vmhost.LoadSource("main", `import std::io

struct P
    x: Float
    n: Int
end

fn step(p: P) -> P
    {..p, x: p.x + 1.0, n: p.n + 1}
end

fn main()
    step(P{x: 0.0, n: 0}).n |> io.print()
end
`)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := p.Disassemble("step")
	if err != nil {
		t.Fatal(err)
	}
	// step's parameter is an owned one's when its caller hands it over, so
	// the update is opUpdateIn (`update!`), which tests that at run time.
	if !(strings.Contains(listing, " update ") || strings.Contains(listing, " update! ")) || strings.Contains(listing, " ir ") {
		t.Errorf("step's update does not compile to opUpdate alone:\n%s", listing)
	}
}

// A cons is its one cell.
func TestOpCosts_AConsAllocatesItsCell(t *testing.T) {
	per := allocsPerActivation(t, `import std::io

fn down(n: Int, xs: List<Int>) -> Int
    if n < 1 then 0 else down(n - 1, [n %% 7, ..xs])
end

fn main()
    down(%d, []) |> io.print()
end
`)
	if per > 1.1 {
		t.Errorf("one cons allocates %.2f times, want 1 (its cell)", per)
	}
}

// A crossing into an intrinsic (Vector.length) and into a bound adapter
// (Int.bit_count) passes its operands in the activation record's scratch
// slice: neither allocates, where each once allocated a []any per call.
// The count is the difference between an activation making three crossings
// and one making one, halved, so the machine view an intrinsic is handed
// (boundAt, made once per activation record and run) cancels out.
func TestOpCosts_AHostCallAllocatesNothing(t *testing.T) {
	for _, tc := range []struct{ name, one, three string }{
		{"intrinsic", `import std::io

fn down(n: Int, v: Vector<Int>) -> Int
    if n < 1 then 0 else down(n - 1, v) + Vector.length(v) - 3
end

fn main()
    down(%d, [1, 2, 3]) |> io.print()
end
`, `import std::io

fn down(n: Int, v: Vector<Int>) -> Int
    if n < 1 then 0 else down(n - 1, v) + Vector.length(v) + Vector.length(v) - Vector.length(v) - 3
end

fn main()
    down(%d, [1, 2, 3]) |> io.print()
end
`},
		{"adapter", `import std::io

fn down(n: Int, k: Int) -> Int
    if n < 1 then 0 else down(n - 1, k) + Int.bit_count(k) - 2
end

fn main()
    down(%d, 6) |> io.print()
end
`, `import std::io

fn down(n: Int, k: Int) -> Int
    if n < 1 then 0 else down(n - 1, k) + Int.bit_count(k) + Int.bit_count(k) - Int.bit_count(k) - 2
end

fn main()
    down(%d, 6) |> io.print()
end
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			per := (allocsPerActivation(t, tc.three) - allocsPerActivation(t, tc.one)) / 2
			if per > 0.1 {
				t.Errorf("one host call allocates %.2f times, want 0. The likely causes: the "+
					"crossing running through its boxed handler (callInstr) instead of opHost, "+
					"or opHost allocating its operand slice (frame.hostArgs)", per)
			}
		})
	}
}

// An intrinsic crossing compiles to opHost, resolved when the function is
// compiled, rather than to the boxed handler that looks it up by name.
func TestOpCosts_AnIntrinsicCompilesToHost(t *testing.T) {
	p, err := vmhost.LoadSource("main", `import std::io

fn size(v: Vector<Int>) -> Int
    Vector.length(v)
end

fn main()
    size([1, 2]) |> io.print()
end
`)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := p.Disassemble("size")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, " host ") || strings.Contains(listing, " ir ") {
		t.Errorf("Vector.length does not compile to opHost alone:\n%s", listing)
	}
}
