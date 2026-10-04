package irbuild

import (
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// TestStdlibModuleOrderIsStable is the determinism guard on stdLoweringOrder.
//
// The order decides which cross-module references resolve, which decides which
// std bodies lower, which decides the emitted TEXT of every generated stdlib
// package, and that text is content-addressed into an artifact directory. An
// order that varied would fill a stable identity from something that varies,
// and it would present as a build cache that misses at random rather than as a
// test failure.
//
// The risk is real rather than theoretical: the function walks an import graph
// whose edges come from a MAP in stdModuleImports, and returning that map's
// range order instead of a sorted slice would produce exactly this.
func TestStdlibModuleOrderIsStable(t *testing.T) {
	lib := std.Load()
	names := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		names = append(names, name)
	}
	slices.Sort(names)

	first := stdLoweringOrder(names, lib)
	if len(first) != len(names) {
		t.Fatalf("the order holds %d module(s) for %d loaded; every module must be lowered exactly once",
			len(first), len(names))
	}
	seen := map[string]bool{}
	for _, name := range first {
		if seen[name] {
			t.Errorf("%s appears twice in the lowering order", name)
		}
		seen[name] = true
	}
	for i := range 8 {
		if got := stdLoweringOrder(names, lib); !slices.Equal(got, first) {
			t.Fatalf("run %d produced a different order:\n  first: %v\n  now:   %v", i+2, first, got)
		}
	}
}

// TestStdlibLoweringOrderPutsAModuleAfterItsImports is the PROPERTY the order
// exists for, and it is stated over std rather than over a synthetic graph
// because the interesting inputs — a real cycle and a real deep chain — are
// things std has and a fixture would have to guess at.
//
// A module must be lowered after every sibling it imports, EXCEPT where the
// import is part of a cycle: std/strings and std/iter import each other, so one
// of them necessarily comes first and its reference into the other refuses.
// That exception is enumerated rather than waved at, so a change that turned an
// acyclic edge into a violation cannot hide behind it.
func TestStdlibLoweringOrderPutsAModuleAfterItsImports(t *testing.T) {
	lib := std.Load()
	names := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		names = append(names, name)
	}
	slices.Sort(names)

	position := map[string]int{}
	for i, name := range stdLoweringOrder(names, lib) {
		position[name] = i
	}
	// reaches reports whether `from` can get to `to` through import edges, which
	// is what makes an out-of-order edge part of a CYCLE rather than a bug.
	var reaches func(from, to string, seen map[string]bool) bool
	reaches = func(from, to string, seen map[string]bool) bool {
		if seen[from] {
			return false
		}
		seen[from] = true
		for _, dep := range stdModuleImports(lib.Nodes[from]) {
			if dep == to || reaches(dep, to, seen) {
				return true
			}
		}
		return false
	}

	var cyclic []string
	for _, name := range names {
		for _, dep := range stdModuleImports(lib.Nodes[name]) {
			if _, known := lib.Nodes[dep]; !known {
				continue
			}
			if position[dep] < position[name] {
				continue
			}
			if reaches(dep, name, map[string]bool{}) {
				cyclic = append(cyclic, dep+" <-> "+name)
				continue
			}
			t.Errorf("%s imports %s and is lowered BEFORE it (%d < %d), with no cycle to force it — "+
				"every reference from %s into %s will refuse for no reason",
				name, dep, position[name], position[dep], name, dep)
		}
	}
	// A guard over a population meant to stay small must fail on the EMPTY set
	// too: if std ever loses its one import cycle, the exception above stops
	// being exercised and this test silently becomes weaker.
	if len(cyclic) == 0 {
		t.Error("no cyclic import pair was found in std, so the cycle exception above is untested; " +
			"either std lost its cycle (delete the exception) or stdModuleImports stopped seeing edges")
	}
	slices.Sort(cyclic)
	t.Logf("import cycles forcing an out-of-order edge (%d): %s", len(cyclic), strings.Join(slices.Compact(cyclic), ", "))
}
