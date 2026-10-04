package vm

import (
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Extend makes entry, another lowering's entry module, this machine's entry,
// and links it and the modules in rest that the machine has not linked yet.
// Everything the machine already holds stays: the function table and its
// compiled bytecode, every once cell and the value a forced one holds, and
// the modules earlier entries linked. The previous entry becomes an ordinary
// linked module, so a function value built from it still calls what it
// called.
//
// The REPL is the one caller. Each input is its own lowering, run on one
// live machine: a closure an earlier input built keeps running its own IR
// and reading its own once cells, and nothing an earlier input computed is
// computed again. A lowering interns fresh symbols for the declarations it
// builds, so an input's module never declares a symbol an earlier one does;
// a module two lowerings share (the cached stdlib) is linked once.
//
// Dispatch and Display bodies are keyed by type name, and a later module's
// entry replaces an earlier one's: a type an input redeclares dispatches to
// the newest impl.
//
// Call it only while nothing runs on the machine, on the machine the caller
// opened rather than a view.
func (m *Machine) Extend(entry *ir.Module, rest []*ir.Module) {
	if m.modules == nil {
		m.modules = map[*ir.Module]bool{}
	}
	if prev := m.mod; prev != nil && prev != entry {
		m.linkFuncs(prev)
	}
	m.mod = entry
	m.boot = entry.Boot()
	m.tasks = &taskLimit{}
	if !m.modules[entry] {
		m.modules[entry] = true
		m.addOnceCells(entry)
		m.addImpls(entry)
		m.addTestBoots(entry)
	}
	for _, other := range rest {
		if other == nil || other == entry || m.modules[other] {
			continue
		}
		m.modules[other] = true
		if m.boot == nil {
			m.boot = other.Boot()
		}
		m.addOnceCells(other)
		m.addTestBoots(other)
		m.addImpls(other)
		m.linkFuncs(other)
	}
}

// linkFuncs adds mod's functions to the link table. A symbol already linked
// is replaced: Extend's lowerings intern disjoint symbols, so a repeat is
// the same declaration.
func (m *Machine) linkFuncs(mod *ir.Module) {
	for _, f := range mod.Funcs() {
		sym := f.Sym()
		if sym == nil {
			continue
		}
		if m.link == nil {
			m.link = map[*ir.Symbol]*ir.Func{}
		}
		m.link[sym] = f
	}
}

// addOnceCells opens a cell for each of mod's once declarations that has
// none, leaving every existing cell, forced or not, as it is.
func (m *Machine) addOnceCells(mod *ir.Module) {
	if m.onces == nil {
		m.onces = map[*ir.Symbol]*onceCell{}
	}
	for _, cell := range mod.Cells() {
		body := cell.Initializer()
		if body == nil {
			continue
		}
		if _, held := m.onces[cell.Sym()]; held {
			continue
		}
		m.onces[cell.Sym()] = &onceCell{body: body, value: rt.NewOnceCell[any](cell.Sym().Name())}
	}
}
