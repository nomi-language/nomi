package irbuild

// A SIBLING MODULE'S TAIL CALLS MUST BE MARKED.
//
// `analysis.MarkTailCalls` sets `Call.IsTailCall`, and it is the ONLY input to
// this builder's tail marking: `collectTailEdges` adds an edge only for a
// call with `IsTailCall == true`, and `ir.Call.Tail()` carries the same flag.
// So on a tree where that pass never ran, every tail-recursive function lowers
// to a plain recursive call, and the VM's call depth grows where Nomi
// guarantees constant stack (spec §12.7).
//
// The hazard is a pass that runs on the ENTRY file and not on the other trees
// the same consumer reads. Two other trees have the same exposure and are
// handled at their own seams:
//
//   - `std.Load()` runs the sweeps and `CheckTypes` but not `MarkTailCalls`, so
//     the stdlib's tail calls are marked at the `stdModuleContext` seam
//     (stdmodulegen.go).
//   - Imported modules re-parsed outside the entry-file pipeline need the pass
//     too, or a recursive function in an imported module overflows the stack
//     on deep recursion.
//
// `irbuild.Analyze` takes the entry's nodes from `proj.Nodes`, which the front
// end's `CheckFile` has run the entry pipeline over, `MarkTailCalls` included,
// and every sibling's from `proj.Files[i].Nodes`, which that pipeline does not
// reach. So the sibling trees need marking of their own.
//
// THE CHECK IS A COUNT WITH A POSITIVE CONTROL, not an inspection of the call
// graph. The same source is analysed twice, once AS the entry and once as a
// SIBLING of a trivial entry, and the two mark counts are compared. A zero
// beside a non-zero settles it in one reading, and neither number is predicted
// from the code.
//
// Nothing else catches this. The symptom is silent: nothing produces a wrong
// string, so the golden records and the corpus are blind to it, since a stack
// property is invisible to a byte comparison. And the corpus is mostly
// single-file, so its entry file is the only module most programs have.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// tailMarkCount counts calls with IsTailCall set anywhere under nodes.
func tailMarkCount(nodes []ast.Node) int {
	n := 0
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		if isNilNode(node) {
			return
		}
		if call, ok := node.(*ast.Call); ok && call.IsTailCall {
			n++
		}
		for _, child := range childNodes(node) {
			walk(child)
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return n
}

// siblingTailLib is the module under measurement. `down` is tail-recursive, so
// a correctly analysed tree has at least one mark in it.
const siblingTailLib = `pub fn down(n: Int, acc: Int): Int {
  if n <= 0 {
    acc
  } else {
    down(n - 1, acc + 1)
  }
}
`

// siblingTailEntry imports the library and calls into it, which is what puts
// lib.nomi in the program's import graph as a SIBLING.
const siblingTailEntry = `import {
  std/io
  lib
}

fn main() {
  io.print("${lib.down(3, 0)}")
}
`

// twoModuleFixture writes entry + lib into one temp dir and returns both paths.
func twoModuleFixture(t *testing.T) (entry, lib string) {
	t.Helper()
	dir := t.TempDir()
	entry = filepath.Join(dir, "main.nomi")
	lib = filepath.Join(dir, "lib.nomi")
	if err := os.WriteFile(entry, []byte(siblingTailEntry), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lib, []byte(siblingTailLib), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry, lib
}

// TestSiblingTail_ASiblingModuleGetsItsTailCallsMarked compares a sibling
// module's tail marks with the same source's marks as an entry. See the header
// for why the control is not optional.
func TestSiblingTail_ASiblingModuleGetsItsTailCallsMarked(t *testing.T) {
	entry, lib := twoModuleFixture(t)

	// POSITIVE CONTROL: the identical source analysed AS an entry. If this is
	// zero the fixture is wrong — `down`'s recursive call is not tail-marked
	// for some reason having nothing to do with sibling plumbing — and the
	// comparison below would read a broken fixture as a fixed defect.
	asEntry, err := Analyze(lib)
	if err != nil {
		t.Fatalf("analysing lib.nomi as an entry: %v", err)
	}
	control := tailMarkCount(asEntry.Entry().Nodes)
	if control == 0 {
		t.Fatalf("the positive control is ZERO: `down`'s tail self-call is not marked even " +
			"when its file is the ENTRY. The fixture does not state the property, so a " +
			"zero on the sibling side below would prove nothing. Fix the fixture, not the test.")
	}

	prog, err := Analyze(entry)
	if err != nil {
		t.Fatalf("analysing main.nomi: %v", err)
	}
	var sibling *Module
	for i := range prog.Modules {
		if prog.Modules[i].Name == "lib" {
			sibling = &prog.Modules[i]
			break
		}
	}
	if sibling == nil {
		t.Fatalf("lib.nomi is not among the program's %d modules, so this test has lost its "+
			"subject — Analyze is expected to carry every user file in the import graph",
			len(prog.Modules))
	}
	got := tailMarkCount(sibling.Nodes)
	if got != control {
		t.Fatalf("lib.nomi carries %d tail-call mark(s) as a SIBLING and %d as an ENTRY.\n"+
			"The same source must be marked the same way whichever door it comes through: "+
			"MarkTailCalls is the only input to collectTailEdges, so an unmarked sibling "+
			"forms no tail plan and its tail-recursive functions lower to plain recursive "+
			"calls, with unbounded stack growth where spec §12.7 guarantees constant "+
			"stack. See the header.",
			got, control)
	}
}
