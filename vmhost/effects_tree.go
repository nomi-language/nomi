package vmhost

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// CallTree is one root of a walk the Reach holds, with the functions below
// it that lead to an effect (Reach.Trees): what `nomi effects
// --tree` prints.
type CallTree struct {
	// Name is the root function's name, a test case's its test's, and
	// Edge how the walk started from it: EdgeRoot for main, its failure
	// renderer and an exported function, EdgeTest for a test case,
	// EdgeBoot for a boot.
	Name string
	Edge Edge
	// File is the root's file as a report names it (imageName).
	File string
	// Tests reports a test case.
	Tests bool
	Root  *TreeFunc
}

// TreeFunc is a function of the program or of a dependency on a CallTree.
// Within a walk one TreeFunc stands for a function however many functions
// reach it, and the functions can form cycles, so a printer expands each
// once; Key says which functions of different walks are the same one.
type TreeFunc struct {
	// Key is the function's file, position and name.
	Key string
	// Name is the function as a chain names it (`load_config`,
	// `Display.to_string for Card`).
	Name string
	// Package is the dependency whose files declare it, by its path, or ""
	// for the project's own.
	Package string
	// Calls is each effect call its own body makes, once per name, callee,
	// kind and standing.
	Calls []TreeCall
	// Next is each function it reaches that can reach an effect call
	// (vm.CallNode.Next).
	Next []TreeStep
	// Pure counts the functions it reaches that have no effects.
	Pure int
}

// TreeStep is a function on a CallTree and how the function above it
// reached it.
type TreeStep struct {
	Func *TreeFunc
	Edge Edge
}

// TreeCall is an effect call on a CallTree.
type TreeCall struct {
	// Name is what an effects list names it by (`std::fs`), as
	// EffectCall.Name.
	Name string
	// Callee is what the code calls (`fs.read_text`), and Edge how.
	Callee string
	Edge   Edge
	// Refused is the list that refuses the call, as a reader names it
	// (`[effects.self]`, `the entry for lib.example/prices`); "" when
	// nomi.toml's lists allow it or it has none.
	Refused string
}

// Trees is each root of each walk, in the walks' order and each walk's
// roots in position order, with the functions below it that lead outside
// the machine, read from the edges the walk recorded (vm.EffectWalk.Tree),
// and each effect call standing as `nomi check` holds it to nomi.toml's
// lists (standingsOf).
func (r *Reach) Trees() []CallTree {
	var out []CallTree
	for _, w := range r.Walks {
		if w.reach == nil {
			continue
		}
		var standings []standing
		if r.Lists != nil {
			standings = standingsOf(w.Calls, w.Tests, r.Lists, r.declared)
		}
		funcs := map[*vm.CallNode]*TreeFunc{}
		var conv func(n *vm.CallNode) *TreeFunc
		conv = func(n *vm.CallNode) *TreeFunc {
			if t := funcs[n]; t != nil {
				return t
			}
			f := n.Func
			t := &TreeFunc{Key: funcKey(f), Name: f.Name(), Package: w.p.packageOf(f.Pos().File()), Pure: n.Pure}
			funcs[n] = t
			seen := map[TreeCall]bool{}
			for _, i := range n.Calls {
				c := TreeCall{Name: w.Calls[i].Name, Callee: w.Calls[i].Callee, Edge: w.Calls[i].Edge}
				if standings != nil && standings[i].refused {
					c.Refused = refusedBy(standings[i])
				}
				if !seen[c] {
					seen[c] = true
					t.Calls = append(t.Calls, c)
				}
			}
			for _, s := range n.Next {
				t.Next = append(t.Next, TreeStep{Func: conv(s.Node), Edge: s.Edge})
			}
			return t
		}
		for _, root := range w.reach.Tree() {
			f := root.Node.Func
			out = append(out, CallTree{
				Name:  f.Name(),
				Edge:  root.Edge,
				File:  w.p.imageName(f.Pos().File()),
				Tests: w.Tests,
				Root:  conv(root.Node),
			})
		}
	}
	return out
}

// funcKey is a function's file, position and name.
func funcKey(f *ir.Func) string {
	return fmt.Sprintf("%s:%d:%d %s", f.Pos().File(), f.Pos().Line(), f.Pos().Col(), f.Name())
}

// refusedBy is the list that refuses s's call, as a reader names it.
func refusedBy(s standing) string {
	switch s.List {
	case OverSelf:
		return "[effects.self]"
	case OverDependencies:
		return "[effects.dependencies]"
	case OverSelfEntry:
		return "the entry for " + s.Entry
	case OverEntry:
		return strings.ReplaceAll(entriesText(s.Through), "`", "")
	}
	return s.Package + "'s own [effects.self]"
}
