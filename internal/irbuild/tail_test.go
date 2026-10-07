package irbuild

import (
	"bytes"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// The tail-call cycle detector.
//
// Every case here is DEEP on purpose. Direct Go recursion of the corpus's shape
// survives 1e6 hops on 16 MB of stack and 1e7 on 256 MB, so a shallow fixture
// passes whether or not the tail call is handled, and a corpus run cannot tell
// the difference.

// TestTail_ASynthesizedBodyIsNeverACycleMember pins an impossibility that the
// driver's position handling depends on.
//
// A `derive`d impl's body has no position a programmer wrote: derive synthesis
// allocates from a band around 2^30 that `emittableLine` refuses, so tailSig
// falls back to the RECEIVER's declaration. That fallback is unreachable, and
// this is why: no synthesized body contains a tail-MARKED call, so a synth item
// has no outgoing edge, so it can be neither a self-edge nor a member of a
// component of size > 1. This holds for derive-`Display` and the universal
// `Debug` alike.
//
// Pinned rather than assumed, because if derive synthesis or MarkTailCalls ever
// changes, the failure without this test is a driver arm at a `//line` the Go
// compiler rejects — and no other test in this file would reach it.
func TestTail_ASynthesizedBodyIsNeverACycleMember(t *testing.T) {
	src := `import {
  std/io
}

struct Inner {
  n: Int
}

derive Display for Inner

struct Wrapper {
  inner: Inner
}

derive Display for Wrapper

fn main() {
  io.print("${Wrapper{inner: Inner{n: 1}}}")
}
`
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	g := newGen(p.Entry(), "nomimod0", nil, nil, -1, nil)
	g.declareTypes()
	g.declareFuncs()
	synth := 0
	for i, u := range g.tailUnits {
		if u.impl == nil || !u.impl.synth {
			continue
		}
		synth++
		if len(u.edges) != 0 {
			t.Errorf("%s is synthesized and has tail edges %v; a driver would emit its arm at a fabricated //line",
				u.label, u.edges)
		}
		if g.tailPlanOf[i] != nil {
			t.Errorf("%s is synthesized and sits on a cycle", u.label)
		}
	}
	if synth == 0 {
		t.Fatal("no synthesized impl item was in the graph, so this test measured nothing")
	}
}

// --- across the impl/free boundary -----------------------------------------

// runTailFixtureOnVM runs a fixture's main on the VM, the way `nomi run` does:
// GenerateIR never refuses, and a body it did not retain is a VM blocker.
func runTailFixtureOnVM(t *testing.T, path string) (string, error) {
	t.Helper()
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("analyze %s: %v", path, err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatalf("generate IR for %s: %v", path, err)
	}
	var out bytes.Buffer
	_, err = vm.NewProgram(res.IR[0], res.IRModules(), &out).Run("main")
	return out.String(), err
}

// TestTail_TheVMRunsTheDeepFixturesInConstantStack runs the 1e8-hop fixtures
// on the VM, which replaces the caller's activation at each tail call. 1e8 is a thousand times the VM's call-depth limit, so a machine
// that stacked its tail calls would fault at 100,000 rather than print.
//
// The two impl cases are cycles whose members are not all free functions: an
// interface impl function with a free one, and two inherent impl functions on
// different receivers. internal/vm's tail-call tests cover an impl function
// calling itself, not these.
func TestTail_TheVMRunsTheDeepFixturesInConstantStack(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	for name, want := range map[string]string{
		"tail_self_deep.nomi":      "0\n",
		"tail_mutual_deep.nomi":    "True\n",
		"tail_impl_free_deep.nomi": "0\n",
		"tail_impl_impl_deep.nomi": "0\n",
	} {
		t.Run(name, func(t *testing.T) {
			stdout, err := runTailFixtureOnVM(t, fixture(name))
			if err != nil || stdout != want {
				t.Fatalf("VM: stdout %q, err %v; want %q", stdout, err, want)
			}
		})
	}
}
