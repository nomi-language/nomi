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
// the newest impl. A Debug rendering an earlier input compiled names the
// bodies its parts had then, and a type with none rendered by its shape;
// on an extended machine it also renders a value no named body owns
// through the hand-written `impl Debug` the machine holds for its type
// (rowDebugs, debugImplHook), so an impl a later input writes for an
// earlier type reaches it. The machine's boot is the entry's, or the one
// it had when the entry declares none.
//
// Call it only while nothing runs on the machine, on the machine the caller
// opened rather than a view.
func (m *Machine) Extend(entry *ir.Module, rest []*ir.Module) {
	if m.modules == nil {
		m.modules = map[*ir.Module]bool{}
	}
	m.extended = true
	if prev := m.mod; prev != nil && prev != entry {
		m.linkFuncs(prev)
	}
	m.mod = entry
	if boot := entry.Boot(); boot != nil {
		// An entry with no boot of its own runs the boot the machine ran
		// before: a REPL input runs under the boot an earlier input
		// declared, as one program of every input did.
		m.boot = boot
	}
	if m.dbg != nil {
		m.dbg.AddModules(append([]*ir.Module{entry}, rest...)...)
	}
	m.tasks = &taskLimit{}
	if !m.modules[entry] {
		m.modules[entry] = true
		m.addOnceCells(entry)
		m.addImpls(entry)
		m.addBoots(entry)
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
		m.addBoots(other)
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

// shapeOp is how the machine answers an interface function by its
// arguments' shape: the instruction over the function's arity arguments.
type shapeOp struct {
	arity int
	instr func(f *ir.Func, pos ir.Pos, args []ir.Temp) ir.Instr
}

// shapeOps are the interface functions the machine answers by a value's
// shape, by interface and function name: Equatable's equal is the
// comparison a derived Equatable's `==` is (ir.Compare at ValIface,
// rt.Equal through the key table), Hashable's hash the hash a derived
// Hashable's is (the hashValueHost intrinsic, rt.Hash through the key
// table), Debug's inspect the rendering a type with no written inspect
// gets (ir.Render at RenderDebug, rt.DebugText), and Display's to_string
// a derived Display's (ir.Render at RenderDisplay, rt.DisplayShapeText).
var shapeOps = map[[2]string]shapeOp{
	{"Equatable", "equal"}: {2, func(f *ir.Func, pos ir.Pos, args []ir.Temp) ir.Instr {
		return ir.NewCompare(pos, f.NewTemp(), ir.OpEq, ir.ValIface, args[0], args[1])
	}},
	{"Hashable", "hash"}: {1, func(f *ir.Func, pos ir.Pos, args []ir.Temp) ir.Instr {
		return ir.NewHostCall(pos, f.NewTemp(), ir.OrdinaryCall, ir.NewSymbol(hashValueName), args[0])
	}},
	{"Debug", "inspect"}: {1, func(f *ir.Func, pos ir.Pos, args []ir.Temp) ir.Instr {
		return ir.NewRenderDebug(pos, f.NewTemp(), args[0])
	}},
	{"Display", "to_string"}: {1, func(f *ir.Func, pos ir.Pos, args []ir.Temp) ir.Instr {
		return ir.NewRenderDisplay(pos, f.NewTemp(), args[0])
	}},
}

// ByShape is the machine's own function for the interface function old
// implements, answering it by its arguments' shape, or nil when the
// machine has none for it: the target Redirect gives an impl function
// whose type's later impl writes no function for it, as a derived
// Equatable writes no equal, a derived Hashable no hash, a derived Debug
// no inspect and a derived Display no to_string
// (vmhost.Session.Supersede). One function serves
// every type, and a comparison or a rendering inside it reaches a part
// through the impl the machine holds for the part's type (keysFor,
// debugImplHook, displayShape).
func (m *Machine) ByShape(old *ir.Func) *ir.Func {
	if old == nil {
		return nil
	}
	impl, ok := old.Sym().Impl()
	op, known := shapeOps[[2]string{impl.Interface, impl.Func}]
	if !ok || !known || len(old.Params()) != op.arity {
		return nil
	}
	key := impl.Interface + "." + impl.Func
	if f := m.shapeFuncs[key]; f != nil {
		return f
	}
	pos := old.Pos()
	f := ir.NewFunc(pos, key+" by shape")
	args := make([]ir.Temp, op.arity)
	for i := range args {
		args[i] = f.AddParam(ir.NewSymbol("value"), ir.ValUnknown)
	}
	b := f.NewBlock(pos, "entry")
	in := op.instr(f, pos, args)
	b.Append(in)
	b.SetTerm(ir.NewReturn(pos, in.Dst()))
	if m.shapeFuncs == nil {
		m.shapeFuncs, m.shapes = map[string]*ir.Func{}, map[*ir.Func]bool{}
	}
	m.shapeFuncs[key] = f
	m.shapes[f] = true
	return f
}

// Redirect makes every call to old, another lowering's function this
// machine links, run replacement instead: a call compiled before it, one
// compiled after it, and old as a value. The REPL redirects an earlier
// input's impl function to the function of the impl a later input
// redefined it with, so code an earlier input compiled calls the newest
// impl, as code a later input compiles does.
//
// The replacement may be the machine's own function for an interface
// function by the value's shape (ByShape): the later impl writes no
// function for it, as a derived Debug writes none. Then no per-type table
// names old any more (rowDebugs, displays, equates, hashes), so a value of
// the type renders and compares by its shape there too.
//
// Call it only while nothing runs on the machine.
func (m *Machine) Redirect(old, replacement *ir.Func) {
	if old == nil || replacement == nil || old == replacement {
		return
	}
	if sym := old.Sym(); sym != nil && m.link != nil {
		m.link[sym] = replacement
	}
	if sym := old.Sym(); sym != nil && m.shapes[replacement] {
		for _, table := range []map[string]*ir.Symbol{m.rowDebugs, m.displays, m.equates, m.hashes} {
			for name, named := range table {
				if named == sym {
					delete(table, name)
				}
			}
		}
	}
	s := m.slotFor(old)
	s.f = replacement
	s.code.Store(m.codeOf(m.slotFor(replacement)))
	s.value.Store(nil)
	// A function old was compiled into (inline.go) compiles again on its
	// next call, with the replacement in its place.
	m.codes.slots.Range(func(_, v any) bool {
		slot := v.(*fnSlot)
		if c := slot.code.Load(); c != nil && c.inlines(old) {
			slot.code.CompareAndSwap(c, nil)
		}
		return true
	})
}
