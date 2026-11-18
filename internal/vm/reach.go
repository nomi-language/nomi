package vm

import (
	"slices"
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
	// implementation for any type, which some module implements without a
	// dispatch entry. A method no module implements is not reported.
	NoImplementation
)

// Unretained is one declaration a program reaches and this machine cannot run.
type Unretained struct {
	Kind UnretainedKind
	// Name is the declaration as the IR names it.
	Name string
	// From is the retained function that reaches it.
	From string
	// Chain is the functions from a root to From, outermost first
	// (`main`, `run_prompt`, `run`): how the program reaches it. Empty
	// when a boot or one of the machine's tables reaches it.
	Chain []string
}

// Unretained walks everything reachable from roots and the boot symbols and
// answers each declaration this machine could not run, sorted by name. A nil
// root is skipped.
func (m *Machine) Unretained(roots []*ir.Func, boots []*ir.Symbol) []Unretained {
	w := reachWalk{m: m, seen: map[*ir.Func]bool{}, found: map[string]Unretained{},
		parent: map[*ir.Func]*ir.Func{}}
	_, found := w.run(roots, boots)
	return found
}

// Reached is Unretained's walk, answering with what it reports every
// function it reached: each body a call, a dispatch, a once, a closure, a
// boot or one of this machine's tables (keys, Display, row Debug) may run.
// A built image keeps only these (PruneImage), so a function this walk
// misses is one a built program cannot call.
func (m *Machine) Reached(roots []*ir.Func, boots []*ir.Symbol) (map[*ir.Func]bool, []Unretained) {
	w := reachWalk{m: m, seen: map[*ir.Func]bool{}, found: map[string]Unretained{}}
	return w.run(roots, boots)
}

// run is Reached's walk over w, which may record parents.
func (w *reachWalk) run(roots []*ir.Func, boots []*ir.Symbol) (map[*ir.Func]bool, []Unretained) {
	m := w.m
	for _, sym := range boots {
		if sym == nil {
			continue
		}
		w.symbol(sym, "boot", NotRetained, EdgeBoot)
		if render := m.bootFailures[sym]; render != nil {
			w.symbol(render, "boot failure", NotRetained, EdgeBoot)
		}
	}
	root := w.rootKind
	if root == 0 {
		root = EdgeRoot
	}
	for _, f := range roots {
		w.push(f, root)
	}
	for len(w.queue) > 0 {
		f := w.queue[len(w.queue)-1]
		w.queue = w.queue[:len(w.queue)-1]
		w.cur = f
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
	return w.seen, out
}

type reachWalk struct {
	m     *Machine
	seen  map[*ir.Func]bool
	queue []*ir.Func
	found map[string]Unretained
	// keys marks the key bodies (keys.go) queued: every hand-written
	// Equatable and Hashable body is reachable from any instruction that
	// may hash or compare a value of a declared type (runsKeyBodies).
	keys bool
	// effects collects what Effects reports; nil for Unretained's walk.
	effects map[string]bool
	// parent, when set, records for each function the function whose visit
	// first reached it; a root and a boot have none. cur is the function
	// being visited.
	parent map[*ir.Func]*ir.Func
	cur    *ir.Func
	// via, recorded with parent, is the instruction of parent's body that
	// reached each function; at is the instruction being visited.
	via map[*ir.Func]ir.Instr
	at  ir.Instr
	// rootKind is the edge kind each root is reached by: EdgeRoot unless
	// the caller says otherwise (EdgeTest for test cases).
	rootKind EdgeKind
	// kinds, recorded with parent, is the edge kind that first reached
	// each function.
	kinds map[*ir.Func]EdgeKind
	// edges, when set, records every edge the walk takes out of each
	// function, a function already seen included: what OutsideCalls
	// follows to name an outside call (outside.go).
	edges map[*ir.Func][]reachEdge
}

// reachEdge is one way the walk reached to from the function being
// visited: at is the instruction of its body that did, and kind how.
type reachEdge struct {
	to   *ir.Func
	kind EdgeKind
	at   ir.Instr
}

// keyBodies queues every recorded Equatable and Hashable body, once; a walk
// that records edges records them from every instruction that may hash or
// compare.
func (w *reachWalk) keyBodies(from string) {
	if w.keys && w.edges == nil {
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
			w.symbol(table[name], from, NotRetained, EdgeKeys)
		}
	}
}

// push queues f, which the walk reached by an edge of kind from the
// function being visited (none for a root or a boot).
func (w *reachWalk) push(f *ir.Func, kind EdgeKind) {
	if f == nil {
		return
	}
	if w.edges != nil && w.cur != nil {
		w.edges[w.cur] = append(w.edges[w.cur], reachEdge{to: f, kind: kind, at: w.at})
	}
	if w.seen[f] {
		return
	}
	w.seen[f] = true
	if w.kinds != nil {
		w.kinds[f] = kind
	}
	if w.parent != nil && w.cur != nil {
		w.parent[f] = w.cur
		if w.via != nil {
			w.via[f] = w.at
		}
	}
	w.queue = append(w.queue, f)
}

func (w *reachWalk) report(kind UnretainedKind, name, from string) {
	key := name + "\x00" + string(rune('0'+kind))
	if _, dup := w.found[key]; !dup {
		w.found[key] = Unretained{Kind: kind, Name: name, From: from, Chain: w.chain(from)}
	}
}

// chain is the functions from a root to the one being visited, outermost
// first, when the walk records parents and from names that function; nil
// otherwise.
func (w *reachWalk) chain(from string) []string {
	if w.parent == nil || w.cur == nil || w.cur.Name() != from {
		return nil
	}
	var names []string
	for f := w.cur; f != nil && len(names) <= len(w.seen); f = w.parent[f] {
		names = append(names, f.Name())
	}
	slices.Reverse(names)
	return names
}

// symbol resolves a callee the way a call does and queues its body, which
// the walk reached by an edge of kind edge.
func (w *reachWalk) symbol(sym *ir.Symbol, from string, kind UnretainedKind, edge EdgeKind) {
	f, err := w.m.resolveFunc(sym, from)
	if err != nil {
		w.report(kind, sym.Name(), from)
		return
	}
	w.push(f, edge)
}

func (w *reachWalk) visit(f *ir.Func) {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			w.at = in
			if w.effects != nil {
				if e := w.m.effectOf(in, f.Name()); e != "" {
					w.effects[e] = true
				}
			}
			if runsKeyBodies(f, in) {
				w.keyBodies(f.Name())
			}
			switch n := in.(type) {
			case *ir.Call:
				w.call(n, f.Name())
			case *ir.Defer:
				w.call(n.Call(), f.Name())
			case *ir.FuncValue:
				w.push(n.Body(), EdgeValue)
			case *ir.Ref:
				switch n.Kind() {
				case ir.RefFunc:
					w.symbol(n.Sym(), f.Name(), NotRetained, EdgeValue)
				case ir.RefOnce:
					cell, found := w.m.onces[n.Sym()]
					if !found || cell == nil {
						w.report(OnceNotRetained, n.Sym().Name(), f.Name())
						continue
					}
					w.push(cell.body, EdgeOnce)
				}
			case *ir.Assert, *ir.Record, *ir.Subtests:
				// A `values:` row renders a value of a type with a
				// hand-written Debug through it (rowText); each is reachable.
				names := make([]string, 0, len(w.m.rowDebugs))
				for name := range w.m.rowDebugs {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					w.symbol(w.m.rowDebugs[name], f.Name(), NotRetained, EdgeDebug)
				}
			case *ir.Render:
				for _, impl := range n.DebugImpls() {
					w.symbol(impl.Fn, f.Name(), NotRetained, EdgeDebug)
				}
				if n.Kind() == ir.RenderDisplay && (n.Erased() || !scalarType(f.TempType(n.Src()))) {
					// Any retained Display body may run; each is reachable.
					// An erased rendering selects one by the value's type,
					// and a structural one (displayStructural) renders each
					// nested value of a declared type through its own.
					names := make([]string, 0, len(w.m.displays))
					for name := range w.m.displays {
						names = append(names, name)
					}
					sort.Strings(names)
					for _, name := range names {
						w.symbol(w.m.displays[name], f.Name(), NotRetained, EdgeDisplay)
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
			if fn, _ := w.m.adapter(name); fn == nil && w.m.hosts[name].fn == nil {
				w.report(NoBinding, name, from)
			}
			return
		}
		w.symbol(n.Callee(), from, NotRetained, EdgeCall)
	case ir.CalleeDispatched:
		impls := w.m.dispatch[n.Callee()]
		if len(impls) == 0 {
			// A method no module implements at all never runs: no value
			// of an implementing type exists, so the call is only ever
			// reached over none (a dispatch over an empty list). Were it
			// reached, it stops with a DispatchMiss.
			if w.m.unretainedImpls[n.Callee()] {
				w.report(NoImplementation, n.Callee().Name(), from)
			}
			return
		}
		for _, impl := range impls {
			w.symbol(impl, from, NotRetained, EdgeDispatch)
		}
	}
}

// scalarType reports whether every value of t is a scalar, whose Display no
// impl answers. A nil type (none stated) is not.
func scalarType(t *ir.ValType) bool {
	if t == nil {
		return false
	}
	switch t.Kind() {
	case ir.KindUnit, ir.KindBool, ir.KindInt, ir.KindFloat, ir.KindDecimal, ir.KindString,
		ir.KindByte, ir.KindBytes:
		return true
	}
	return false
}
