package irbuild

import (
	"testing"
)

// TestIRCall_ACallIntoARecursionGraphIsRetained checks that a call to a member
// of a tail-call recursion graph is retained. The VM runs a tail call in
// constant stack (internal/vm/tail.go), so both the caller and the
// tail-recursive callee are retained.
func TestIRCall_ACallIntoARecursionGraphIsRetained(t *testing.T) {
	var c irFuncRetentionCount
	defer c.observe(t, irFromModule)()
	lowerIROnly(t, irFormSource(""+
		"fn count(n: Int, acc: Int): Int {\n"+
		"  if n == 0 { acc } else { count(n - 1, acc + 1) }\n}\n"+
		"fn f(n: Int): Int {\n  count(n, 0)\n}\n"))
	if !slicesContain(c.retained, "f") || !slicesContain(c.retained, "count") {
		t.Fatalf("retained %v; the caller and the tail-recursive callee must both be "+
			"retained for the VM", c.retained)
	}
}
