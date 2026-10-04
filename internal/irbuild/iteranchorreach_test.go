package irbuild

// Does the `Iter` anchor reach every stdlib module, or only the one that
// declares it? A count over the whole population, with positive and negative
// controls.
//
// loadIter resolves the anchor through three different doors depending on how
// a module reaches the name: an import, the declaring module itself (see
// iter.go's THE DECLARING MODULE IS ITS OWN PROTOCOL), or the stdlib module
// scopes. A module behind a closed door refuses every `Iter.` call in it, and
// nothing produces a wrong string, so the failure is quiet. The class it
// guards against is a pass that runs on the entry file and not on its
// siblings.
//
// It counts the OUTCOME at the entry point, per door, rather than reading the
// order of loadIter's checks, because the order says which check runs first,
// not which one a module actually reaches. The numbers are printed by the
// test's log rather than embedded here.

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// iterAnchorDoor names which of loadIter's three doors a module's `Iter` comes
// through. The names are the switch arms in loadIter.
type iterAnchorDoor string

const (
	// doorImport is `sym.Resolved != nil` — the name reached through an import.
	doorImport iterAnchorDoor = "import"
	// doorSelf is the declaring module, std/iter itself.
	doorSelf iterAnchorDoor = "declaring module"
	// doorStdScope is `sym == nil` — `Iter` is not in the file's scope at all
	// and the anchor is read out of fa.StdlibModuleScopes["iter"].
	doorStdScope iterAnchorDoor = "StdlibModuleScopes"
	// doorLocalForeign is a LOCAL declaration that is not std's. No stdlib
	// module presents this shape; a user file cannot either, because declaring
	// `interface Iter<T>` is a front-end error. Counted so a non-zero says so.
	doorLocalForeign iterAnchorDoor = "local non-std declaration"
)

// iterDoorOf answers which door g's `Iter` would come through, WITHOUT calling
// loadIter — so the door and the outcome are two independent readings and a
// disagreement between them is visible.
func iterDoorOf(g *gen) iterAnchorDoor {
	if g.fa == nil || g.fa.ModuleScope == nil {
		return doorStdScope
	}
	sym := g.fa.ModuleScope.Lookup("Iter")
	switch {
	case sym == nil:
		return doorStdScope
	case sym.Resolved != nil:
		return doorImport
	case g.declaresStdIter():
		return doorSelf
	default:
		return doorLocalForeign
	}
}

// TestIRIterAnchorReachesEveryStdModule counts anchor resolution across the
// whole stdlib population, per door, with a user file as the positive control.
func TestIRIterAnchorReachesEveryStdModule(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	lib := std.Load()
	names := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)

	// The complete index: this test is a leaf consumer, so it may name every
	// lowered stdlib module.
	full := stdlibLowering()

	perDoor := map[iterAnchorDoor]int{}
	var unanchored []string
	var declaring []string
	modules := 0
	for _, name := range names {
		nodes := lib.Nodes[name]
		if len(nodes) == 0 {
			continue
		}
		modules++
		path := strings.TrimPrefix(lib.FileURI(name), "file://")
		v := stdModuleContext(name, path, full.modulePkg[name], nodes, lib.Files[name], full)
		g := v.gen(full)

		door := iterDoorOf(g)
		g.loadIter()
		if g.iter == nil {
			unanchored = append(unanchored, name+" ("+string(door)+")")
		} else {
			perDoor[door]++
		}
		if g.declaresStdIter() {
			declaring = append(declaring, name)
		}
	}

	// ANTI-VACUITY FIRST, so every count below is known to be about the stdlib.
	if modules < 20 {
		t.Fatalf("only %d stdlib modules were examined; std.Load() is not returning the stdlib and "+
			"every count in this file is about an empty population", modules)
	}

	// THE POSITIVE CONTROL, and it is what makes any zero above readable. A user
	// file reaching std/iter through the prelude's import is rule 1's own path —
	// the one door never in doubt — so it must anchor. If it does not,
	// everything above is measuring a broken harness rather than the stdlib.
	//
	// Writing `import std/iter.Iter` is a front-end error (the prelude already
	// binds `Iter`), so the control calls `Iter`, which is also the door a real
	// call site goes through.
	const userSource = `fn total(xs: Iter<Int>): Int {
  Iter.count(xs)
}

fn main() {
  Unit
}
`
	prog, err := AnalyzeSource("iter_user", userSource)
	if err != nil {
		t.Fatalf("positive control: a user file calling Iter does not analyze: %v", err)
	}
	ug := newUserGenFor(prog)
	ug.loadIter()
	if ug.iter == nil {
		t.Fatalf("POSITIVE CONTROL FAILED: a user file that calls Iter resolves no " +
			"anchor, so rule 1's import path is broken and nothing else in this file can be read " +
			"as a statement about the stdlib")
	}

	// THE NEGATIVE CONTROL, which stops `declaresStdIter` quietly becoming true
	// for everything, the failure that would let the declaring-module door reach
	// user code.
	if ug.declaresStdIter() {
		t.Fatalf("NEGATIVE CONTROL FAILED: a USER gen answers declaresStdIter TRUE, so `stdModule` " +
			"is being set outside newStdGen. The declaring-module door is not confined to " +
			"std/iter, and a user file's own `Iter` could be lowered to rt.Seq, a type identity " +
			"decided by a name")
	}
	if len(declaring) != 1 || declaring[0] != "iter" {
		t.Fatalf("declaresStdIter answered TRUE for %v; it must be exactly [iter], or newStdGen has "+
			"stopped passing the module name through and the predicate is not an identity",
			declaring)
	}

	// THE ASSERTION. Every stdlib module must resolve the anchor, because an
	// unanchored module refuses `Iter without the std protocol` at every `Iter.`
	// call in it.
	if len(unanchored) > 0 {
		t.Fatalf("%d of %d stdlib modules resolve NO `Iter` anchor: %v.\n"+
			"Every `Iter.` call in each of them refuses `Iter without the std protocol`. The "+
			"entry-shaped module works and the rest silently do not. Each name above carries the DOOR loadIter would have used, which is "+
			"which of the three closed. See this file's header.",
			len(unanchored), modules, unanchored)
	}

	// And that each door has members, so the walk is not passing because one
	// door happens to serve everything. Without this, a change that routed
	// every module through `StdlibModuleScopes` — losing the
	// import path entirely — would read as a clean pass.
	for _, d := range []iterAnchorDoor{doorImport, doorSelf} {
		if perDoor[d] == 0 {
			t.Fatalf("no stdlib module resolves its `Iter` anchor through the %q door, so that arm "+
				"of loadIter is unexercised by this walk and a regression in it would be "+
				"invisible here. Per-door counts: %v", d, perDoor)
		}
	}
	if perDoor[doorLocalForeign] > 0 {
		t.Fatalf("%d stdlib module(s) present a LOCAL non-std `Iter` declaration, which no file in "+
			"this repository is supposed to be able to do (declaring `interface Iter<T>` is a "+
			"front-end error). Per-door counts: %v", perDoor[doorLocalForeign], perDoor)
	}

	t.Logf("`Iter` anchor reach: all %d stdlib modules resolve it. Per door: import=%d, "+
		"declaring module=%d, StdlibModuleScopes=%d, local non-std=%d. Positive control (a user "+
		"file calling the prelude's Iter) resolves it; negative control (that same user gen) answers "+
		"declaresStdIter FALSE.",
		modules, perDoor[doorImport], perDoor[doorSelf], perDoor[doorStdScope],
		perDoor[doorLocalForeign])
}
