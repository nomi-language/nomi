package vm

import (
	"io"
	"path"
	"sort"
	"strings"

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
//
// When the call is in the standard library, At is the first call in the
// program's own code that reaches it, Via that call's callee (`fs.read_text`
// for a crossing inside std/fs's `read_text`), and Chain ends at the
// function holding that call. Otherwise At is Pos and Via is Name.
type CrossingSite struct {
	Name  string
	Pos   ir.Pos
	Chain []string
	At    ir.Pos
	Via   string
}

// ReachedCrossingSites is every direct call into Go in the functions
// PruneImage keeps for roots and boots whose host key keep accepts, in
// position order: what `nomi build` checks against its target's capability
// classes. Like the walk, it over-approximates: a call in a branch the
// program never takes counts.
func ReachedCrossingSites(entry *ir.Module, mods []*ir.Module, roots []*ir.Func, boots []*ir.Symbol, keep func(name string) bool) []CrossingSite {
	w := reachWalk{m: NewProgram(entry, mods, io.Discard), seen: map[*ir.Func]bool{}, found: map[string]Unretained{},
		parent: map[*ir.Func]*ir.Func{}, via: map[*ir.Func]ir.Instr{}}
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
		if c == nil || c.Form() != ir.CalleeDirect || !c.Crosses() || !keep(c.Callee().Name()) {
			return
		}
		site := CrossingSite{Name: c.Callee().Name(), Pos: c.Pos(), At: c.Pos(), Via: c.Callee().Name()}
		// Out of the standard library: up the reaching calls to the first
		// one the program's own code makes.
		holder := f
		for steps := 0; site.At.Stdlib() && steps <= len(reached); steps++ {
			up, in := w.parent[holder], w.via[holder]
			if up == nil || in == nil {
				break
			}
			site.At, site.Via = in.Pos(), reachingName(in, holder)
			holder = up
		}
		site.Chain = chain(holder)
		out = append(out, site)
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
		a, b := out[i].At, out[j].At
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

// reachingName is the name of what in, the instruction that reached f,
// calls: its callee, or f's own name for a function value or reference. A
// std file's function is named as a program calls it: `fs.read_text`, and
// `Reader.read` for one owned by a type.
func reachingName(in ir.Instr, f *ir.Func) string {
	name := f.Name()
	switch n := in.(type) {
	case *ir.Call:
		name = n.Callee().Name()
	case *ir.Defer:
		if c := n.Call(); c != nil {
			name = c.Callee().Name()
		}
	}
	if f.Pos().Stdlib() && !strings.Contains(name, ".") {
		name = strings.TrimSuffix(path.Base(f.Pos().File()), ".nomi") + "." + name
	}
	return name
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
