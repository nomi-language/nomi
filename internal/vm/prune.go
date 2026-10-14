package vm

import (
	"io"

	"github.com/nomi-language/nomi/internal/ir"
)

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
