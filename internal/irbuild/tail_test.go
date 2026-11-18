package irbuild

import (
	"bytes"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// Tail calls in constant stack.
//
// Every case here is deep on purpose: a shallow fixture passes whether or not
// a tail call replaces its caller's activation.

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
