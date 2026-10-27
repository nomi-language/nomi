package vmhost

import (
	"bytes"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// A case cut off by a machine limit keeps the output it wrote before the
// limit, and the report prints it under the case's BLOCKED line.
func TestCases_ALimitedCaseKeepsItsOutput(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	p, err := LoadSource("main_test.nomi", `import std/io

fn spin(n: Int) -> Int {
    spin(n + 1)
}

test "loops" {
    io.print("before the loop")
    dbg 1
    assert spin(0) == 0
}
`)
	if err != nil {
		t.Fatalf("the front end or the builder rejects this: %v", err)
	}
	var out bytes.Buffer
	m := p.machine(&out).WithLimits(vm.Limits{Steps: 100_000})
	failures, outputs, limits, _ := m.RunCases(p.entry.Tests())
	if len(limits) != 1 || limits[0] == "" {
		t.Fatalf("the case ran to an exit (err %v), so this test checks nothing", failures)
	}
	const want = "before the loop\ndbg line 9: 1 = 1\n"
	if outputs[0] != want {
		t.Fatalf("output %q, want %q", outputs[0], want)
	}
	if out.Len() != 0 {
		t.Fatalf("output outside the case: %q", out.String())
	}
	var report bytes.Buffer
	rep := rt.NewTestReporter(&report)
	rep.Blocked("main_test.nomi :: loops", []string{"[vm] machine limit: " + limits[0]}, outputs[0])
	wantReport := "BLOCKED main_test.nomi :: loops [vm] machine limit: " + limits[0] + "\n" +
		"  before the loop\n  dbg line 9: 1 = 1\n"
	if got := report.String(); got != wantReport {
		t.Fatalf("report:\n%s\nwant:\n%s", got, wantReport)
	}
}
