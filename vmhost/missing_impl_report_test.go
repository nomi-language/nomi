package vmhost_test

import (
	"strings"
	"testing"
)

// A call whose bound check fails reports the missing impl once, at the
// call. `List.contains?` reaches Equatable both through its bound and
// through `impl Contains for List<T> where T: Equatable`, whose demand used
// to report the same missing impl a second time.
func TestMissingImpl_AFailedBoundReportsOnce(t *testing.T) {
	src := "struct Point {\n    x: Int\n    y: Int\n}\n\nfn main() {\n" +
		"    dbg List.contains?([#Point{x: 1, y: 2}], #Point{x: 1, y: 2})\n}\n"
	rejects(t, src,
		"main.nomi:7:9: Point does not implement Equatable (required by `where T: Equatable`)",
		"add `derive Equatable` to `Point`")
	err := checkEntry(t, src)
	var diags []string
	for _, line := range strings.Split(strings.TrimSpace(err.Error()), "\n") {
		if !strings.Contains(line, ": help: ") {
			diags = append(diags, line)
		}
	}
	if len(diags) != 1 {
		t.Errorf("want one diagnostic, got %d:\n%s", len(diags), err)
	}
}

// A demand no bound check covers (an ordering operator) still reports the
// missing impl, and its message names how the impl is required, not where:
// the diagnostic's own position says where.
func TestMissingImpl_ADemandWithoutABoundStillReports(t *testing.T) {
	src := "struct Point {\n    x: Int\n}\n\nfn main() {\n    dbg #Point{x: 1} < #Point{x: 2}\n}\n"
	rejects(t, src,
		"main.nomi:6:22: no impl of `Comparable` for `Point` (required via ordering operator `<`)",
		"add `derive Comparable` to `Point`")
	err := checkEntry(t, src)
	for _, line := range strings.Split(err.Error(), "\n") {
		if i := strings.Index(line, "no impl of"); i >= 0 && strings.Contains(line[i:], "main.nomi") {
			t.Errorf("the message holds a path: %s", line)
		}
	}
}
