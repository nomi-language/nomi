package vmhost

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// List and Vector membership is a scan in rt, not a Nomi call per element.
// The machine's step budget counts each call, backward branch and iteration
// element a run makes, so a scan of 20 000 list elements or 3 000 vector
// elements fits in a budget of 1 000 steps when it runs in rt, while the same
// list scan written as `Iter.any?` with a lambda uses at least one step per
// element and runs out. The vector is a literal because `Iter.to_vector`
// costs a step per element it collects.
func TestContains_AListOrVectorScanMakesNoNomiCallPerElement(t *testing.T) {
	prelude := `
fn words() -> List<String> {
    String.split(String.repeat("a,", 20_000), ",")
}

fn vector_words() -> Vector<String> {
    #[` + strings.Repeat(`"a", `, 3_000) + `]
}
`
	run := func(t *testing.T, body string) []string {
		t.Helper()
		src := prelude + "\ntest \"scan\" {\n" + body + "\n}\n"
		p, err := LoadSource("main_test.nomi", src)
		if err != nil {
			t.Fatalf("the front end or the builder rejects this: %v", err)
		}
		var out bytes.Buffer
		m := p.machine(&out).WithLimits(vm.Limits{Steps: 1_000})
		failures, _, limits := m.RunCases(p.entry.Tests())
		if len(failures) != 1 || len(limits) != 1 {
			t.Fatalf("want one case, got failures %v, limits %v", failures, limits)
		}
		failure := ""
		if failures[0] != nil {
			failure = failures[0].Error()
		}
		return []string{failure, limits[0]}
	}

	for _, body := range []string{
		`    refute List.contains?(words(), "b")`,
		`    refute Contains.contains?(words(), "b")`,
		`    refute Vector.contains?(vector_words(), "b")`,
	} {
		got := run(t, body)
		if got[1] != "" {
			t.Fatalf("%s reached the step limit (%s): the scan costs a step per element", strings.TrimSpace(body), got[1])
		}
		if got[0] != "" {
			t.Fatalf("%s failed: %s", strings.TrimSpace(body), got[0])
		}
	}

	// The control: the same scan with a Nomi predicate per element.
	if got := run(t, `    refute Iter.any?(words(), fn w -> w == "b")`); got[1] == "" {
		t.Fatalf("Iter.any? with a lambda finished in 1 000 steps, so the budget does not count per-element calls and the test above checks nothing")
	}
}
