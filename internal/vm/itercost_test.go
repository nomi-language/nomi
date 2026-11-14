package vm_test

// Allocations per element of an iterator pipeline. As in callalloc_test.go,
// each count is the difference between two runs of one program that differ
// only in how many elements the pipeline walks, divided by that difference,
// so loading, boot and printing cancel out. Every Int the programs hold
// stays below 256, which Go boxes without allocating.

import (
	"bytes"
	"context"
	"fmt"
	"testing"
)

// allocsPerElement answers the allocations one element of src's pipeline
// costs, src being a program whose main walks %d elements.
func allocsPerElement(t *testing.T, src string) float64 {
	t.Helper()
	const low, high = 50, 200
	measure := func(n int) float64 {
		p := depthProgram(t, fmt.Sprintf(src, n))
		var out bytes.Buffer
		if err := p.Run(context.Background(), &out, nil, false); err != nil {
			t.Fatalf("n = %d: %v", n, err)
		}
		return testing.AllocsPerRun(20, func() {
			out.Reset()
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("n = %d: %v", n, err)
			}
		})
	}
	return (measure(high) - measure(low)) / (high - low)
}

// Building and running a pipeline over a two-element list costs the list,
// its view and the fold's closures, and nothing for the machine itself: an
// iteration instruction counts its callbacks' depth through an iterCaller
// instead of copying the Machine into a view per instruction (depth.go's
// `at`), and a lambda that captures nothing is one value, not one per
// evaluation. That was 11 allocations per run, and is 8.
func TestTinyPipelineAllocations(t *testing.T) {
	const src = `import std::io

fn walk(i: Int, n: Int, sum: Int) -> Int
  if i == n then sum else walk(i + 1, n, sum + Iter.reduce([1, 2], 0, fn a, x -> a + x))
end

fn main()
  walk(0, %d, 0) |> io.print()
end
`
	if per := allocsPerElement(t, src); per > 8.1 {
		t.Errorf("one Iter.reduce over [1, 2] allocates %.2f times, want at most 8", per)
	}
}

// An Int range walks in Go: no call to Int's `compare` or `next` and no
// `Some` per element (rt.IntRangeSeq), so a fold over it allocates nothing
// per element.
func TestIntRangeElementsAllocateNothing(t *testing.T) {
	const src = `import std::io

fn main()
  Iter.reduce(0..%d, 0, fn acc, i -> if i == 3 then acc + 1 else acc) |> io.print()
end
`
	if per := allocsPerElement(t, src); per > 0.1 {
		t.Errorf("an element of an Int range's fold allocates %.2f times, want 0", per)
	}
}

// A Codepoint range walks in Go too (rt.CodepointRangeSeq): no call to
// Codepoint's `compare` or `next` and no `Some` per element, only the
// Codepoint record each element is.
func TestCodepointRangeElementsAllocateOneRecord(t *testing.T) {
	const src = `import std::io

fn main()
  last = Int.to_codepoint(32 + %d)!
  Iter.reduce(' '..last, 0, fn acc, c -> if c == 'a' then acc + 1 else acc) |> io.print()
end
`
	if per := allocsPerElement(t, src); per > 1.1 {
		t.Errorf("an element of a Codepoint range's fold allocates %.2f times, want 1", per)
	}
}

// `Iter.windows(k)` builds each window once from a ring of the last k
// elements (rt.WindowsEach): k list cells per element and nothing else.
func TestWindowsAllocateOneCellPerWindowElement(t *testing.T) {
	const src = `import std::io

fn main()
  xs = Iter.to_vector(Iter.map(0..%d, fn _ -> 1))
  Iter.windows(xs, 3) |> Iter.count() |> io.print()
end
`
	if per := allocsPerElement(t, src); per > 3.1 {
		t.Errorf("an element of a 3-wide windows allocates %.2f times, want 3", per)
	}
}

// A pairing stage lends its consumer one tuple per run when the consumer's
// callback only takes it apart (lend.go), so pairing an element costs no
// allocation.
func TestLentPairsAllocateNothingPerElement(t *testing.T) {
	cases := map[string]string{
		"with_index into reduce": `import std::io

fn main()
  xs = Iter.to_vector(Iter.map(0..%d, fn _ -> 1))
  Iter.reduce(Iter.with_index(xs), 0, fn acc, (i, x) -> if i == 3 then acc + x else acc) |> io.print()
end
`,
		"zip into map": `import std::io

fn main()
  xs = Iter.to_vector(Iter.map(0..%d, fn _ -> 1))
  Iter.zip(xs, xs) |> Iter.map(fn (a, b) -> a == b) |> Iter.all_match(fn b -> b) |> io.print()
end
`,
	}
	for name, src := range cases {
		if per := allocsPerElement(t, src); per > 0.1 {
			t.Errorf("%s: an element allocates %.2f times, want 0", name, per)
		}
	}
}
