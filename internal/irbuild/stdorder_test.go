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
// exists for, stated over std rather than over a synthetic graph because the
// interesting inputs, real cycles and a real deep chain, are things std has
// and a fixture would have to guess at.
//
// Every module is lowered in a unit (stdLoweringUnits) after the units of
// the modules it imports, or in the same unit when the import is part of a
// cycle. The cycles are enumerated, so a guard meant to hold over cycles
// fails if std has none.
func TestStdlibLoweringOrderPutsAModuleAfterItsImports(t *testing.T) {
	lib := std.Load()
	names := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		names = append(names, name)
	}
	slices.Sort(names)

	units := stdLoweringUnits(stdLoweringOrder(names, lib), lib)
	unitOf := map[string]int{}
	for i, unit := range units {
		for _, name := range unit {
			if _, twice := unitOf[name]; twice {
				t.Errorf("%s is in two lowering units", name)
			}
			unitOf[name] = i
		}
	}
	if len(unitOf) != len(names) {
		t.Fatalf("the units hold %d module(s) for %d loaded; every module must be lowered exactly once", len(unitOf), len(names))
	}
	var cycles []string
	for i, unit := range units {
		if len(unit) > 1 {
			cycles = append(cycles, strings.Join(unit, " "))
		}
		for _, name := range unit {
			for _, dep := range stdModuleImports(lib.Nodes[name]) {
				if d, known := unitOf[dep]; known && d > i {
					t.Errorf("%s imports %s and is lowered in unit %d, before %s's unit %d", name, dep, i, dep, d)
				}
			}
		}
	}
	if len(cycles) == 0 {
		t.Error("no import cycle was found in std, so lowering a cycle as one unit is untested; " +
			"either std lost its cycles or stdModuleImports stopped seeing edges")
	}
	t.Logf("import cycles lowered as one unit (%d): %s", len(cycles), strings.Join(cycles, "; "))
}

// TestStdLoweringIsTheSameInEveryCycleOrder lowers the stdlib with the
// members of each import cycle in their order, reversed and rotated, and
// requires the same bodies to lower each time. A cycle is one unit
// (lowerStdUnit), so the order an edit to one member's imports gives its
// members cannot decide which bodies lower: with a cycle lowered a module
// at a time, a body calling into a later member (Byte's `&&&` calling
// `Int.to_byte`, Codepoint's `compare` calling Int's) was refused.
func TestStdLoweringIsTheSameInEveryCycleOrder(t *testing.T) {
	lowers := func() map[string][]string {
		idx := buildStdlibIndex()
		out := map[string][]string{}
		for _, name := range idx.order {
			m := &stdUnitModule{funcs: idx.modFuncs[name], onces: idx.modOnces[name]}
			var keys []string
			for k := range m.loweredKeys() {
				keys = append(keys, k)
			}
			for k := range idx.unlowered[idx.modulePkg[name]] {
				keys = append(keys, "unlowered "+k)
			}
			slices.Sort(keys)
			out[name] = keys
		}
		return out
	}
	want := lowers()
	defer func() { stdUnitOrderForTest = nil }()
	for _, reorder := range []struct {
		name string
		f    func([]string) []string
	}{
		{"reversed", func(ms []string) []string { slices.Reverse(ms); return ms }},
		{"rotated", func(ms []string) []string { return append(ms[1:], ms[0]) }},
	} {
		cycles := 0
		stdUnitOrderForTest = func(ms []string) []string {
			cycles++
			return reorder.f(ms)
		}
		got := lowers()
		if cycles == 0 {
			t.Fatal("the stdlib lowered no import cycle, so no order was changed")
		}
		for name, keys := range want {
			if !slices.Equal(got[name], keys) {
				t.Errorf("with each cycle's members %s, std/%s lowers differently:\n  in order: %v\n  %s: %v",
					reorder.name, name, setMinus(keys, got[name]), reorder.name, setMinus(got[name], keys))
			}
		}
	}
}

// setMinus is the members of a that b lacks.
func setMinus(a, b []string) []string {
	var out []string
	for _, k := range a {
		if !slices.Contains(b, k) {
			out = append(out, k)
		}
	}
	return out
}
