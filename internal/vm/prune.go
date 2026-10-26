package vm

import (
	"io"
	"sort"

	"github.com/nomi-language/nomi/internal/ir"
)

// ReachedCrossings names every crossing into Go that a function PruneImage
// keeps for the same roots and boots may make, sorted: what a built image
// asks of the host. `nomi build` reads it to decide which parts of the
// program's CLDR data the image carries.
func ReachedCrossings(entry *ir.Module, mods []*ir.Module, roots []*ir.Func, boots []*ir.Symbol) []string {
	reached, _ := NewProgram(entry, mods, io.Discard).Reached(roots, boots)
	seen := map[string]bool{}
	note := func(c *ir.Call) {
		if c != nil && c.Form() == ir.CalleeDirect && c.Crosses() {
			seen[c.Callee().Name()] = true
		}
	}
	for f := range reached {
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				switch n := in.(type) {
				case *ir.Call:
					note(n)
				case *ir.Defer:
					note(n.Call())
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// CrossingSite is one call into Go that a program's reachable code makes:
// the callee's host key, the call's position, and the functions from a root
// to the one that holds the call, outermost first (`main`, `publish`).
type CrossingSite struct {
	Name  string
	Pos   ir.Pos
	Chain []string
}

// ReachedCrossingSites is every direct call into Go in the functions
// PruneImage keeps for roots and boots whose host key keep accepts, in
// position order: what `nomi build` checks against its target's capability
// classes. Like the walk, it over-approximates: a call in a branch the
// program never takes counts.
func ReachedCrossingSites(entry *ir.Module, mods []*ir.Module, roots []*ir.Func, boots []*ir.Symbol, keep func(name string) bool) []CrossingSite {
	w := reachWalk{m: NewProgram(entry, mods, io.Discard), seen: map[*ir.Func]bool{}, found: map[string]Unretained{},
		parent: map[*ir.Func]*ir.Func{}}
	reached, _ := w.run(roots, boots)
	chain := func(f *ir.Func) []string {
		var names []string
		for g := f; g != nil; g = w.parent[g] {
			names = append([]string{g.Name()}, names...)
			if len(names) > len(reached) {
				break
			}
		}
		return names
	}
	var out []CrossingSite
	note := func(f *ir.Func, c *ir.Call) {
		if c != nil && c.Form() == ir.CalleeDirect && c.Crosses() && keep(c.Callee().Name()) {
			out = append(out, CrossingSite{Name: c.Callee().Name(), Pos: c.Pos(), Chain: chain(f)})
		}
	}
	for f := range reached {
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				switch n := in.(type) {
				case *ir.Call:
					note(f, n)
				case *ir.Defer:
					note(f, n.Call())
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Pos, out[j].Pos
		if a.File() != b.File() {
			return a.File() < b.File()
		}
		if a.Line() != b.Line() {
			return a.Line() < b.Line()
		}
		if a.Col() != b.Col() {
			return a.Col() < b.Col()
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// PruneImage is the module set a built image carries: entry and mods with
// every function this machine's own walk (Reached) cannot reach from roots
// and boots removed, and every module left with nothing dropped. The entry is
// always kept, since a runner opens its machine on it.
//
// The walk is the one Unretained runs before anything executes, so a pruned
// image keeps exactly what that check accepts. A call the walk did not
// foresee finds no function and fails with resolveFunc's "did not retain"
// error rather than running something else; a runner also repeats the
// Unretained check on the decoded image before the program's first effect.
//
// The modules are copies (ir.Module.Pruned): entry and mods are not changed.
func PruneImage(entry *ir.Module, mods []*ir.Module, roots []*ir.Func, boots []*ir.Symbol) (*ir.Module, []*ir.Module) {
	reached, _ := NewProgram(entry, mods, io.Discard).Reached(roots, boots)
	bodies := map[*ir.Symbol]bool{}
	for f := range reached {
		if f.Sym() != nil {
			bodies[f.Sym()] = true
		}
	}
	keep := func(f *ir.Func) bool { return reached[f] }
	keepBody := func(sym *ir.Symbol) bool { return bodies[sym] }
	var prunedEntry *ir.Module
	if entry != nil {
		prunedEntry = entry.Pruned(keep, keepBody)
	}
	out := make([]*ir.Module, 0, len(mods))
	for _, m := range mods {
		if m == entry && entry != nil {
			out = append(out, prunedEntry)
			continue
		}
		if p := m.Pruned(keep, keepBody); !p.Empty() {
			out = append(out, p)
		}
	}
	return prunedEntry, out
}
