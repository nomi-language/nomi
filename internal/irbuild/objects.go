package irbuild

// Linking a lowering through objects.
//
// With LinkThroughObjects on, GenerateIR hands back its program as objects
// encoded apart and linked again: each module of the linked set (the
// program's files, the std modules lowered once for the process, the
// program's std instances) is written with ir.EncodeObject on its own, and
// one ir.Linker decodes them all, resolving every linkage name (link.go) to
// one symbol. Nothing of the lowering's pointers survives: a call reaches
// another module's function only because their names agree. The object
// harness (internal/expectation's TestObjectsLinkToTheSameProgram, and
// `NOMI_LINK_OBJECTS=1` for any other test) runs recorded programs this way
// and requires their output to be the golden files' byte for byte.

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ir"
)

// linkThroughObjects counts the callers that asked for it (LinkThroughObjects).
var linkThroughObjects atomic.Int32

// LinkThroughObjects makes every lowering link its modules through encoded
// objects until the returned function is called.
func LinkThroughObjects() (restore func()) {
	linkThroughObjects.Add(1)
	return func() { linkThroughObjects.Add(-1) }
}

// linksThroughObjects reports whether lowerings link through objects: asked
// for in-process, or by NOMI_LINK_OBJECTS=1 for a whole test run.
func linksThroughObjects() bool {
	return linkThroughObjects.Load() > 0 || os.Getenv("NOMI_LINK_OBJECTS") == "1"
}

// relinkThroughObjects replaces res's modules, and every function and cell
// it points at, with their copies decoded from objects and linked by name.
func relinkThroughObjects(res *Result) error {
	mods := res.IRModules()
	if err := ir.CheckLinkage(mods, nil); err != nil {
		return err
	}
	l := ir.NewLinker()
	out, funcs, cells, err := relinkObjects(l, mods)
	if err != nil {
		return err
	}
	modules := map[*ir.Module]*ir.Module{}
	for i, m := range mods {
		modules[m] = out[i]
	}
	n := len(res.IR)
	res.IR, res.irLibraries = out[:n:n], out[n:]
	linked := make([]*ir.Module, len(res.linkedMods))
	for i, m := range res.linkedMods {
		linked[i] = modules[m]
	}
	res.linkedMods = linked
	remap := func(f *ir.Func) *ir.Func {
		if f == nil {
			return nil
		}
		if g := funcs[f]; g != nil {
			return g
		}
		return f
	}
	for i := range res.Exports {
		x := &res.Exports[i]
		x.Func, x.Encode = remap(x.Func), remap(x.Encode)
		if m := modules[x.Module]; m != nil {
			x.Module = m
		}
		for j := range x.Params {
			x.Params[j].Decode = remap(x.Params[j].Decode)
		}
	}
	for i := range res.HostFns {
		h := &res.HostFns[i]
		h.Decode = remap(h.Decode)
		for j := range h.Params {
			h.Params[j].Encode = remap(h.Params[j].Encode)
		}
	}
	if err := relinkRefinementFuncs(res, l); err != nil {
		return err
	}
	for i := range res.Literals {
		site := &res.Literals[i]
		site.Render = remap(site.Render)
		if c := cells[site.Cell]; c != nil {
			site.Cell = c
		}
	}
	return nil
}

// relinkObjects encodes each of mods as an object and decodes it with l,
// answering the decoded modules and each function's and cell's decoded
// copy.
func relinkObjects(l *ir.Linker, mods []*ir.Module) ([]*ir.Module, map[*ir.Func]*ir.Func, map[*ir.Cell]*ir.Cell, error) {
	out := make([]*ir.Module, len(mods))
	funcs := map[*ir.Func]*ir.Func{}
	cells := map[*ir.Cell]*ir.Cell{}
	for i, m := range mods {
		data, err := ir.EncodeObject(m)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("irbuild: encoding %s as an object: %w", m.Name(), err)
		}
		im, err := ir.DecodeImage(data, ir.WithLinkedSymbols(l.Symbol))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("irbuild: decoding %s's object: %w", m.Name(), err)
		}
		dm := im.Modules[0]
		for j, f := range m.Funcs() {
			funcs[f] = dm.Funcs()[j]
		}
		for j, c := range m.Cells() {
			cells[c] = dm.Cells()[j]
		}
		l.Define(dm)
		out[i] = dm
	}
	return out, funcs, cells, nil
}

// relinkRefinementFuncs relinks the functions of no module that vmhost runs
// to check refinement literals: each literal's Build and generic step's
// Check, and each generic rule's Check. They call the program's functions
// and std instances by symbol, so they go through an object too, decoded by
// l after every module is defined, and call the linked copies; kept as
// they were, they would call symbols the linked program defines nothing
// for. keepRefinements keeps them through an object the same way.
func relinkRefinementFuncs(res *Result, l *ir.Linker) error {
	var slots []**ir.Func
	for i := range res.Refinements {
		site := &res.Refinements[i]
		if site.Build != nil {
			slots = append(slots, &site.Build)
		}
		for j := range site.Steps {
			if site.Steps[j].Check != nil {
				slots = append(slots, &site.Steps[j].Check)
			}
		}
	}
	for i := range res.RefinementRules {
		if res.RefinementRules[i].Check != nil {
			slots = append(slots, &res.RefinementRules[i].Check)
		}
	}
	if len(slots) == 0 {
		return nil
	}
	m := ir.NewModule("refinement literals")
	for _, slot := range slots {
		m.AddFunc(*slot)
	}
	data, err := ir.EncodeObject(m)
	if err != nil {
		return fmt.Errorf("irbuild: encoding the refinement literals as an object: %w", err)
	}
	dm, err := decodeObject(l, data)
	if err != nil {
		return fmt.Errorf("irbuild: decoding the refinement literals' object: %w", err)
	}
	for i, f := range dm.Funcs() {
		*slots[i] = f
	}
	return nil
}
