package vmhost_test

import (
	"strings"
	"testing"
)

// A call whose bound check fails reports the missing impl once, at the
// call. `List.contains` reaches Equatable both through its bound and
// through `impl Contains for List<T> where T: Equatable`, whose demand used
// to report the same missing impl a second time. Point holds a function, so
// it has no equality to meet the bound with.
func TestMissingImpl_AFailedBoundReportsOnce(t *testing.T) {
	src := "struct Point {\n    x: Int\n    f: (Int) -> Int\n}\n\nfn main() {\n" +
		"    dbg List.contains([Point{x: 1, f: fn v -> v}], Point{x: 1, f: fn v -> v})\n}\n"
	rejects(t, src,
		"main.nomi:7:9: Point does not implement Equatable (required by `where T: Equatable`)",
		"`Point` holds `(Int) -> Int`, and a function value has no equality")
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

// `x in c` over a collection with no Contains impl says so once, at the
// collection, naming the test, with the help to write the impl. An
// element that misses the bound of the collection's impl is one error
// too, the element's, naming the `in`: one that holds a function, since
// any other has equality, which meets the bound.
func TestMissingImpl_AMembershipTestNamesIn(t *testing.T) {
	for _, tc := range []struct {
		src, want, help string
	}{
		{"struct Bag {\n    items: List<Int>\n}\n\nfn main() {\n    dbg 1 in Bag{items: [1]}\n}\n",
			"main.nomi:6:14: the collection `in` looks in: Bag does not implement Contains",
			"write an `impl Contains<T> for Bag` block"},
		{"struct P {\n    f: (Int) -> Int\n}\n\nfn main() {\n    p = P{f: fn v -> v}\n    dbg p in [p]\n}\n",
			"main.nomi:7:14: no impl of `Equatable` for `P` (required via membership test `in`)",
			"`P` holds a function, an Iter or a handle to running state, which has no equality"},
	} {
		rejects(t, tc.src, tc.want, tc.help)
		err := checkEntry(t, tc.src)
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
}

// A demand no bound check covers (an ordering operator) still reports the
// missing impl, and its message names how the impl is required, not where:
// the diagnostic's own position says where.
func TestMissingImpl_ADemandWithoutABoundStillReports(t *testing.T) {
	src := "struct Point {\n    x: Int\n}\n\nfn main() {\n    dbg Point{x: 1} < Point{x: 2}\n}\n"
	rejects(t, src,
		"main.nomi:6:21: no impl of `Comparable` for `Point` (required via ordering operator `<`)",
		"add `derive Comparable` to `Point`")
	err := checkEntry(t, src)
	for _, line := range strings.Split(err.Error(), "\n") {
		if i := strings.Index(line, "no impl of"); i >= 0 && strings.Contains(line[i:], "main.nomi") {
			t.Errorf("the message holds a path: %s", line)
		}
	}
}
