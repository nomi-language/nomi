package irbuild

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// stdLoadChildEnv marks the re-executed test binary as the measuring child.
const stdLoadChildEnv = "NOMI_IRBUILD_STDLOAD_CHILD"

// stdLoadBudget is how many times lowering hello world in a FRESH process may
// run std.Load: once, std.Shared, which the front end, stdAnchorLib and
// buildStdlibIndex all read.
const stdLoadBudget = 1

// TestStdLoadCount_LoweringHelloWorldLoadsStdAFixedNumberOfTimes holds the
// number of full stdlib analyses per process to a constant.
//
// A std.Load() reached per module, per signature or per anchor pass multiplies
// the cost of lowering by the number of passes, and it is invisible to every
// other test: the answer is right, only slow.
//
// The measurement runs in a CHILD process, because the anchor and index caches
// are per process and any earlier test in this binary would already have filled
// them, so an in-process count would read low whatever the code did.
func TestStdLoadCount_LoweringHelloWorldLoadsStdAFixedNumberOfTimes(t *testing.T) {
	if os.Getenv(stdLoadChildEnv) == "1" {
		t.Skip("the parent reads this process's count through TestStdLoadCount_Child")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestStdLoadCount_Child$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), stdLoadChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the measuring child failed: %v\n%s", err, out)
	}
	m := regexp.MustCompile(`STDLOADS=(\d+)`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("the measuring child reported no count, so nothing was measured:\n%s", out)
	}
	n, _ := strconv.Atoi(string(m[1]))
	// The positive control: AnalyzeSource builds a runtime, and a runtime runs
	// std.Load. A zero means the counter is not counting, not that the lowering
	// is free.
	if n < 1 {
		t.Fatalf("std.Load ran %d times, but AnalyzeSource alone runs it once; std.LoadCount "+
			"is not counting, so this test measures nothing", n)
	}
	if n > stdLoadBudget {
		t.Fatalf("lowering hello world in a fresh process ran std.Load %d times; the budget is %d.\n"+
			"Each run is a full stdlib parse and analysis (~57ms). Look for a std.Load() that "+
			"runs per module, per signature or per anchor pass instead of once per process: "+
			"a question about std's own declarations belongs on stdAnchorLib (internal/irbuild/"+
			"stdlib.go), computed once under sync.OnceValue.", n, stdLoadBudget)
	}
}

// TestStdLoadCount_Child is the measuring half; it does nothing unless the
// parent above started it.
func TestStdLoadCount_Child(t *testing.T) {
	if os.Getenv(stdLoadChildEnv) != "1" {
		t.Skip("runs only as the child of TestStdLoadCount_LoweringHelloWorldLoadsStdAFixedNumberOfTimes")
	}
	const hello = "import std/io\n\nfn main() {\n  io.print(\"hello\")\n}\n"
	before := std.LoadCount()
	p, err := AnalyzeSource("stdloadhello", hello)
	if err != nil {
		t.Fatalf("hello world does not analyze: %v", err)
	}
	if _, _, err := GenerateIR(p); err != nil {
		t.Fatalf("hello world does not lower: %v", err)
	}
	fmt.Printf("STDLOADS=%d\n", std.LoadCount()-before)
}
