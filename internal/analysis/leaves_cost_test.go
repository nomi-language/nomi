package analysis

import (
	"os/exec"
	"strings"
	"testing"
)

// leaves answers whether a statement always leaves its block by walking
// the statement, and the unreachable-code rule asks it of every statement
// of every block, so a program nested d deep costs on the order of d²
// questions: about 10.5·d² for the nested `if`s here (6657 at 25, 420707
// at 200). When leaves asked a block's statements twice the count doubled
// with each level instead, and a check of 200 nested `if`s never ended.
//
// The count comes from leavesCount, which is compiled out of a normal
// build (leavescount_off.go), so under a plain `go test` this test re-runs
// itself with `-tags analysisleavescount` and fails if the child fails.
// There the counter panics past the bound, so work that doubles fails in
// milliseconds with the count and never hangs.
func TestLeaves_CostGrowsWithTheSquareOfTheDepth(t *testing.T) {
	if !leavesCounting {
		runCountingBuild(t, "analysisleavescount", "TestLeaves_CostGrowsWithTheSquareOfTheDepth")
		return
	}
	for _, depth := range []int{25, 50, 100, 200} {
		src := "fn f() -> Int\n  _x = " + strings.Repeat("if True then ", depth) + "1" +
			strings.Repeat(" else 2", depth) + "\n  1\nend\n"
		bound := 12*depth*depth + 100
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%d nested ifs: %v; the bound is 12·depth² + 100", depth, r)
				}
			}()
			leavesCountReset(bound)
			checkSourceWithModules(t, src, nil)
		}()
		if asked := leavesCountRead(); asked == 0 {
			t.Fatalf("%d nested ifs: leaves was never asked, so the counter is not wired", depth)
		}
	}
	leavesCountReset(0)
}

// runCountingBuild runs the test named test in a child `go test` built
// with tag, where a work counter is live, as rt's test of the same name
// does. It requires the child to report a PASS for that test by name,
// because `go test -run` that matches nothing also exits 0.
func runCountingBuild(t *testing.T, tag, test string) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the counting build needs the go command: %v", err)
	}
	out, err := exec.Command(goBin, "test", "-tags="+tag, "-run=^"+test+"$", "-count=1", "-v", ".").CombinedOutput()
	t.Logf("go test -tags=%s -run=^%s$ .\n%s", tag, test, out)
	if err != nil {
		t.Fatalf("counting build failed: %v", err)
	}
	if !strings.Contains(string(out), "--- PASS: "+test) {
		t.Fatalf("counting build did not report %s as passed", test)
	}
}
