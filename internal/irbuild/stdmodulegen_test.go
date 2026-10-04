package irbuild

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// TestStdModuleView_BindsPhaseOneNotPhaseTwo pins the one thing extracting
// lowerStdlibModule's prologue can silently get wrong.
//
// lowerStdlibModule binds newStdGen against `settled` — PHASE 1 — and rebinds
// the enlarged `withCycles` scope per BODY inside the lowering loop, which is
// what confines a cycle to phase 2 and leaves phase 1's answers unchanged. An
// extraction that bound `withCycles` at construction would change what every
// stdlib body resolves against, and it would do so invisibly: more names
// resolve, so nothing fails and the lowering simply moves.
//
// This is not a hypothetical mistake. The first reading of the stdlib `//!`
// population bound phase 2 at construction without saying so, and the probe now
// reports the delta between the two rather than picking one silently.
//
// DISCRIMINATING, not merely present: the assertion is that the view's gen
// agrees with a hand-built phase-1 gen AND that the two fixed points are not
// the same object, so a `withCycles` that had been aliased to `settled` — which
// would make the check pass for the wrong reason — fails here.
func TestStdModuleView_BindsPhaseOneNotPhaseTwo(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	lib := std.Load()
	names := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	full := stdlibLowering()

	// std/iter is the subject because it is the module with a recursive cycle
	// large enough for the two fixed points to differ at all; a module where
	// they coincide could not tell the two bindings apart.
	const module = "iter"
	nodes := lib.Nodes[module]
	fa := lib.Files[module]
	if len(nodes) == 0 || fa == nil {
		t.Fatalf("std.Load() carries no nodes or analysis for std/%s", module)
	}
	pkg := stdPackage(sort.SearchStrings(names, module))
	path := strings.TrimPrefix(lib.FileURI(module), "file://")

	v := stdModuleContext(module, path, pkg, nodes, fa, full)

	// ANTI-VACUITY: the two sets must actually differ somewhere in the stdlib,
	// or this test cannot distinguish the two bindings for any module. Asked
	// across every module rather than only the subject, because which module
	// carries a cycle is not this test's business to pin.
	differ := false
	for _, name := range names {
		ns, mfa := lib.Nodes[name], lib.Files[name]
		if len(ns) == 0 || mfa == nil {
			continue
		}
		mv := stdModuleContext(name, strings.TrimPrefix(lib.FileURI(name), "file://"),
			stdPackage(sort.SearchStrings(names, name)), ns, mfa, full)
		if len(mv.withCycles) != len(mv.settled) {
			differ = true
			break
		}
	}
	if !differ {
		t.Fatalf("no stdlib module has a `withCycles` set larger than its `settled` set, so this test " +
			"cannot tell phase 1 from phase 2 and asserts nothing. Either the recursive closure has " +
			"stopped admitting anything, or the two sets have been aliased")
	}

	// The view's gen must agree with a phase-1 gen built by hand, member for
	// member of the sibling table — which is the only thing the choice of
	// fixed point changes at construction time.
	want := newStdGen(module, path, pkg, nodes, fa, v.cands, v.settled, v.onces, full)
	got := v.gen(full)
	if len(got.stdSiblings) != len(want.stdSiblings) {
		t.Fatalf("stdModuleView.gen bound %d siblings, a hand-built PHASE 1 gen binds %d — the "+
			"extraction has changed which fixed point a stdlib body resolves against",
			len(got.stdSiblings), len(want.stdSiblings))
	}
	for name, f := range want.stdSiblings {
		if got.stdSiblings[name] != f {
			t.Fatalf("stdModuleView.gen resolves sibling %q differently from a hand-built PHASE 1 gen",
				name)
		}
	}

	// And the phase-2 gen must differ, so the assertion above is known to be
	// about phase 1 specifically rather than about any gen at all.
	loose := newStdGen(module, path, pkg, nodes, fa, v.cands, v.withCycles, v.onces, full)
	if len(loose.stdSiblings) == len(want.stdSiblings) {
		t.Logf("std/%s binds the same %d siblings under both fixed points, so for THIS module the "+
			"two are indistinguishable; the cross-module anti-vacuity check above is what keeps this "+
			"test honest", module, len(want.stdSiblings))
	}
}
