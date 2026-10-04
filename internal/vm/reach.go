package vm

import (
	"sort"

	"github.com/nomi-language/nomi/internal/ir"
)

// WHAT A PROGRAM REACHES THAT THIS MACHINE CANNOT RUN, found before anything
// runs.
//
// `nomi run` and `nomi test` execute on this machine, with no fallback. A
// program that reaches a function the producer
// did not retain must therefore fail and say which function, and it must say
// so BEFORE the program's first effect: a run that printed half its output and
// then stopped on a missing callee would look like a program bug.
//
// The walk uses this machine's own resolution — `resolveFunc`, the dispatch
// table, the once cells, the intrinsics and the bound host adapters — so a callee it accepts
// is one a call will find, and a callee it reports is one a call would fail
// on. It over-approximates in one place: a dispatched call reaches every
// retained implementation of its method, because which one runs depends on a
// value.
//
// The report is the FRONTIER. A function that was not retained has no IR, so
// nothing it would call is visible; fixing one blocker can reveal the next.

// UnretainedKind says why a reached declaration cannot run.
type UnretainedKind uint8

const (
	// NotRetained is a function, impl body or boot the producer did not
	// retain.
	NotRetained UnretainedKind = iota + 1
	// OnceNotRetained is a `once` whose initializer was not retained.
	OnceNotRetained
	// NoBinding is a crossing into Go that neither an intrinsic of this
	// machine nor a bound host adapter answers.
	NoBinding
	// NoImplementation is a dispatched method with no retained
	// implementation for any type.
	NoImplementation
)

// Unretained is one declaration a program reaches and this machine cannot run.
type Unretained struct {
	Kind UnretainedKind
	// Name is the declaration as the IR names it.
	Name string
	// From is the retained function that reaches it.
	From string
}

// Unretained walks everything reachable from roots and the boot symbols and
// answers each declaration this machine could not run, sorted by name. A nil
// root is skipped.
func (m *Machine) Unretained(roots []*ir.Func, boots []*ir.Symbol) []Unretained {
	w := reachWalk{m: m, seen: map[*ir.Func]bool{}, found: map[string]Unretained{}}
	for _, sym := range boots {
		if sym == nil {
			continue
		}
		w.symbol(sym, "boot", NotRetained)
	}
	for _, f := range roots {
		w.push(f)
	}
	for len(w.queue) > 0 {
		f := w.queue[len(w.queue)-1]
		w.queue = w.queue[:len(w.queue)-1]
		w.visit(f)
	}
	out := make([]Unretained, 0, len(w.found))
	for _, u := range w.found {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

type reachWalk struct {
	m     *Machine
	seen  map[*ir.Func]bool
	queue []*ir.Func
	found map[string]Unretained
	// keys marks the key bodies (keys.go) queued: every hand-written
	// Equatable and Hashable body is reachable from any comparison,
	// construction, iteration or crossing that may hash or compare a value.
	keys bool
	// effects collects what Effects reports; nil for Unretained's walk.
	effects map[string]bool
}

// keyBodies queues every recorded Equatable and Hashable body, once.
func (w *reachWalk) keyBodies(from string) {
	if w.keys {
		return
	}
	w.keys = true
	for _, table := range []map[string]*ir.Symbol{w.m.equates, w.m.hashes} {
		names := make([]string, 0, len(table))
		for name := range table {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			w.symbol(table[name], from, NotRetained)
		}
	}
}

func (w *reachWalk) push(f *ir.Func) {
	if f == nil || w.seen[f] {
		return
	}
	w.seen[f] = true
	w.queue = append(w.queue, f)
}

func (w *reachWalk) report(kind UnretainedKind, name, from string) {
	key := name + "\x00" + string(rune('0'+kind))
	if _, dup := w.found[key]; !dup {
		w.found[key] = Unretained{Kind: kind, Name: name, From: from}
	}
}

// symbol resolves a callee the way a call does and queues its body.
func (w *reachWalk) symbol(sym *ir.Symbol, from string, kind UnretainedKind) {
	f, err := w.m.resolveFunc(sym, from)
	if err != nil {
		w.report(kind, sym.Name(), from)
		return
	}
	w.push(f)
}

func (w *reachWalk) visit(f *ir.Func) {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if w.effects != nil {
				if e := effectOf(in, f.Name()); e != "" {
					w.effects[e] = true
				}
			}
			switch in.(type) {
			case *ir.Compare, *ir.Make, *ir.Iter:
				w.keyBodies(f.Name())
			}
			switch n := in.(type) {
			case *ir.Call:
				if n.Crosses() {
					w.keyBodies(f.Name())
				}
				w.call(n, f.Name())
			case *ir.Defer:
				w.call(n.Call(), f.Name())
			case *ir.FuncValue:
				w.push(n.Body())
			case *ir.Ref:
				switch n.Kind() {
				case ir.RefFunc:
					w.symbol(n.Sym(), f.Name(), NotRetained)
				case ir.RefOnce:
					cell, found := w.m.onces[n.Sym()]
					if !found || cell == nil {
						w.report(OnceNotRetained, n.Sym().Name(), f.Name())
						continue
					}
					w.push(cell.body)
				}
			case *ir.Assert, *ir.Record:
				// A `values:` row renders a value of a type with a
				// hand-written Debug through it (rowText); each is reachable.
				names := make([]string, 0, len(w.m.rowDebugs))
				for name := range w.m.rowDebugs {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					w.symbol(w.m.rowDebugs[name], f.Name(), NotRetained)
				}
			case *ir.Render:
				for _, impl := range n.DebugImpls() {
					w.symbol(impl.Fn, f.Name(), NotRetained)
				}
				if n.Erased() {
					// Any retained Display body may run; each is reachable.
					names := make([]string, 0, len(w.m.displays))
					for name := range w.m.displays {
						names = append(names, name)
					}
					sort.Strings(names)
					for _, name := range names {
						w.symbol(w.m.displays[name], f.Name(), NotRetained)
					}
				}
			}
		}
	}
}

func (w *reachWalk) call(n *ir.Call, from string) {
	if n == nil {
		return
	}
	switch n.Form() {
	case ir.CalleeDirect:
		if n.Crosses() {
			name := n.Callee().Name()
			if fn, _ := w.m.adapter(name); fn == nil && w.m.hosts[name] == nil {
				w.report(NoBinding, name, from)
			}
			return
		}
		w.symbol(n.Callee(), from, NotRetained)
	case ir.CalleeDispatched:
		impls := w.m.dispatch[n.Callee()]
		if len(impls) == 0 {
			w.report(NoImplementation, n.Callee().Name(), from)
			return
		}
		for _, impl := range impls {
			w.symbol(impl, from, NotRetained)
		}
	}
}
