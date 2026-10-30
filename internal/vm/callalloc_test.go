package vm_test

// Allocations per activation (exec.go's register stack and bytecode call): the
// machine's cost is its frame and value model, so a per-call allocation is a
// performance regression even when every output is still right.
//
// Each count is a DIFFERENCE between two runs of one program that differ only
// in how many activations they make, divided by that difference, so the
// program's loading, boot and printing cancel out. Every value the programs
// compute stays below 256, which Go boxes into an interface without
// allocating, so what remains is the call path itself.

import (
	"bytes"
	"context"
	"fmt"
	"testing"
)

// allocsPerActivation answers the allocations one activation of `down` costs
// in src, a program whose main calls down(N).
func allocsPerActivation(t *testing.T, src string) float64 {
	t.Helper()
	const low, high = 50, 200
	measure := func(n int) float64 {
		p := depthProgram(t, fmt.Sprintf(src, n))
		var out bytes.Buffer
		if err := p.Run(context.Background(), &out, nil, false); err != nil {
			t.Fatalf("down(%d): %v", n, err)
		}
		return testing.AllocsPerRun(20, func() {
			out.Reset()
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("down(%d): %v", n, err)
			}
		})
	}
	return (measure(high) - measure(low)) / (high - low)
}

// One comparison, one subtraction, one addition and one call per activation.
const oneComparison = `import std/io

fn down(n: Int) -> Int {
  if n < 1 { 0 } else { down(n - 1) + 1 }
}

fn main() {
  down(%d) |> io.print()
}
`

// The same function with three more comparisons per activation, all True for
// every n that recurses.
const fourComparisons = `import std/io

fn down(n: Int) -> Int {
  if n < 1 { 0 } else if n < 250 and n < 251 and n < 252 { down(n - 1) + 1 } else { 0 }
}

fn main() {
  down(%d) |> io.print()
}
`

// A bytecode call allocates nothing: its registers are a window on the
// goroutine's register stack, its arguments are copied straight into the
// callee's parameter registers, its activation record is reused by depth, and
// an Int and a Bool live in a word register without a box.
func TestVMCallAllocations(t *testing.T) {
	perCall := allocsPerActivation(t, oneComparison)
	// The ceiling leaves a tenth of an allocation for
	// noise, which is far below the one allocation a regression adds per call.
	if perCall > 0.1 {
		t.Errorf("one activation of a one-parameter function allocates %.2f times, want 0. "+
			"The likely causes: the activation record escaping to the heap (see "+
			"regStack.newFrame in exec.go), a register or operand slice per call, a "+
			"register stack taken per run instead of reused (see getStack), or an Int "+
			"boxed on the call path", perCall)
	}
	perComparison := (allocsPerActivation(t, fourComparisons) - perCall) / 3
	t.Logf("%.2f allocations per activation, %.2f per comparison", perCall, perComparison)
	if perComparison > 0.25 {
		t.Errorf("a comparison allocates %.2f times, want 0. The likely cause is a Bool built "+
			"per comparison instead of a word register, or of one of the two shared values "+
			"where a Bool is boxed (see boolValue in vm.go)",
			perComparison)
	}
}

// A loop of n tail calls, each making one crossing into an intrinsic.
const crossingLoop = `import std/io

fn spin(n: Int, v: Vector<Int>, acc: Int) -> Int {
  if n < 1 { acc } else { spin(n - 1, v, Vector.length(v)) }
}

fn main() {
  spin(%d, #[1, 2, 3], 0) |> io.print()
}
`

// The same loop with one crossing in all, after it.
const plainLoop = `import std/io

fn spin(n: Int, v: Vector<Int>, acc: Int) -> Int {
  if n < 1 { Vector.length(v) + acc } else { spin(n - 1, v, acc) }
}

fn main() {
  spin(%d, #[1, 2, 3], 0) |> io.print()
}
`

// A crossing into an intrinsic (`Vector.length`, `Map.get`, ...) allocates its
// operand slice and nothing else. The intrinsic runs on a view of the machine
// at the caller's depth, which the caller's activation record keeps (boundAt
// in vm.go), so a loop does not copy the whole machine onto the heap once per
// call.
func TestVMCrossingAllocations(t *testing.T) {
	perCrossing := allocsPerActivation(t, crossingLoop) - allocsPerActivation(t, plainLoop)
	t.Logf("%.2f allocations per crossing into an intrinsic", perCrossing)
	if perCrossing > 1.1 {
		t.Errorf("a crossing into an intrinsic allocates %.2f times, want 1 (its operand "+
			"slice). The likely cause is a view of the machine made per crossing instead "+
			"of reused from the caller's activation record (see boundAt in vm.go)", perCrossing)
	}
}
