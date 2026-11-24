package vm

import (
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
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
// on. It over-approximates where a value decides: a dispatched call reaches
// every retained implementation of its method. A body one of the machine's
// tables selects by a value's type name (a Display, an Equatable, a
// Hashable) is reached from an instruction that renders, compares or hashes
// a value of a type that is or holds that type (heldTypes), and from every
// such instruction whose operand's type does not bound it: an interface
// value, an erased position, an Iter rendered. A failed test's report
// reaches the hand-written Debug bodies the same way (rowText): an
// assertion's `values:` rows from the values they show, and its
// `returned:` block from the `Err` payload of each `?` and of each boot's
// result (returnedHeld).
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
	// Impl is the impl block a function is declared in, the zero ImplFunc
	// for any other declaration (ir.Symbol.Impl).
	Impl ir.ImplFunc
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
		if f, err := m.resolveFunc(sym, "boot"); err == nil {
			// A group's boot that returns `Err` ends each of its cases,
			// whose report renders the `Err` as a `?`'s (returnedText).
			for _, b := range f.Blocks() {
				if r, isReturn := b.Term().(*ir.Return); isReturn && r.HasVal() {
					w.rowDebugs(w.returnedHeld(f.TempType(r.Val())), "boot")
				}
			}
		}
	}
	root := w.rootKind
	if root == 0 {
		root = EdgeRoot
	}
	for _, f := range roots {
		w.push(f, root)
	}
	w.drain()
	// A late root is taken once everything before it is walked, and only
	// when nothing reached it, so a function both reach keeps the chain
	// from the roots.
	for _, f := range w.late {
		w.pushLate(f, root)
	}
	if len(w.late) > 0 && w.edges != nil {
		w.madeBodies(root)
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

// madeBodies roots, by root, every body outside std that a table or a
// dispatch selects for a value of a declared type that code the late
// roots reach makes (an ir.Make of the type), and then what those bodies
// make, until no more: a caller of the late roots that is handed such a
// value may render, compare, hash or dispatch on it. It follows the
// edges the walk recorded, so a function the earlier roots reached first
// counts too.
func (w *reachWalk) madeBodies(root EdgeKind) {
	bodies := w.m.typeBodies()
	visited := map[*ir.Func]bool{}
	made := map[string]bool{}
	queue := slices.Clone(w.late)
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		if visited[f] {
			continue
		}
		visited[f] = true
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				mk, ok := in.(*ir.Make)
				if !ok || mk.Typ() == nil {
					continue
				}
				name, _, _ := strings.Cut(mk.Typ().Name(), "<")
				if made[name] {
					continue
				}
				made[name] = true
				for _, sym := range bodies[name] {
					g, err := w.m.resolveFunc(sym, f.Name())
					if err != nil || g.Pos().Stdlib() {
						continue
					}
					w.pushLate(g, root)
					queue = append(queue, g)
				}
			}
		}
		for _, e := range w.edges[f] {
			queue = append(queue, e.to)
		}
	}
}

// pushLate takes f as a late root, by root, when nothing reached it yet,
// and walks what it reaches.
func (w *reachWalk) pushLate(f *ir.Func, root EdgeKind) {
	if f == nil || w.seen[f] {
		return
	}
	if w.lateRoots == nil {
		w.lateRoots = map[*ir.Func]bool{}
	}
	w.lateRoots[f] = true
	w.cur = nil
	w.push(f, root)
	w.drain()
}

// typeBodies is every body the machine's dispatch and its Display, row
// Debug, Equatable and Hashable tables hold for each declared type, by
// the type's name without its arguments, each table's in the order of
// the names (byTypeName).
func (m *Machine) typeBodies() map[string][]*ir.Symbol {
	out := map[string][]*ir.Symbol{}
	add := func(table map[string]*ir.Symbol) {
		for _, name := range slices.Sorted(maps.Keys(table)) {
			base, _, _ := strings.Cut(name, "<")
			out[base] = append(out[base], table[name])
		}
	}
	methods := slices.Collect(maps.Keys(m.dispatch))
	sort.Slice(methods, func(i, j int) bool { return methods[i].Name() < methods[j].Name() })
	for _, method := range methods {
		add(m.dispatch[method])
	}
	for _, table := range []map[string]*ir.Symbol{m.displays, m.rowDebugs, m.equates, m.hashes} {
		add(table)
	}
	return out
}

// drain visits every function queued and every one their visits queue.
func (w *reachWalk) drain() {
	for len(w.queue) > 0 {
		f := w.queue[len(w.queue)-1]
		w.queue = w.queue[:len(w.queue)-1]
		w.cur = f
		w.visit(f)
	}
}

type reachWalk struct {
	m     *Machine
	seen  map[*ir.Func]bool
	queue []*ir.Func
	// late is the roots the walk starts from once it has walked
	// everything the others reach (run).
	late []*ir.Func
	// lateRoots is each function the late phase took as a root (pushLate).
	lateRoots map[*ir.Func]bool
	found     map[string]Unretained
	// keys marks every key body (keys.go) queued, which an instruction
	// that may hash or compare a value whose type does not bound the
	// declared types it holds reaches (keyOperands, heldTypes).
	keys bool
	// held memoizes heldTypes by type and by whether the value is
	// rendered.
	held map[heldKey]heldTypes
	// effects, unbounded and seeded collect what Effects reports; nil for
	// Unretained's walk.
	effects, unbounded, seeded map[string]bool
	// uses, when set, records the app fields each function's own body
	// reads or replaces (appFieldOf), in instruction order, each once:
	// what Effects reports as AppFields and EffectWalk.Uses spreads back
	// along the edges.
	uses map[*ir.Func][]string
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
	// function, a function already seen included: what EffectCalls
	// follows to name an effect call (effects.go).
	edges map[*ir.Func][]reachEdge
}

// reachEdge is one way the walk reached to from the function being
// visited: at is the instruction of its body that did, and kind how.
type reachEdge struct {
	to   *ir.Func
	kind EdgeKind
	at   ir.Instr
}

// namedPart reports whether e is a Display rendering's edge to a body it
// names for a part (ir.Render.PartImpls), which it calls as a Debug
// rendering calls the bodies it names, and not one the machine's table
// holds for a type the value may hold.
func (e reachEdge) namedPart() bool {
	r, render := e.at.(*ir.Render)
	if !render || e.kind != EdgeDisplay || e.to == nil {
		return false
	}
	for _, p := range r.PartImpls() {
		if p.Fn == e.to.Sym() || p.Fn != nil && e.to.Sym() != nil && p.Fn.Link() != "" && p.Fn.Link() == e.to.Sym().Link() {
			return true
		}
	}
	return false
}

// keyBodies queues the recorded Equatable and Hashable bodies a value of
// held may run, from an instruction that may hash or compare it. Every body
// is queued once when held is unbounded; a walk that records edges records
// them from every such instruction.
func (w *reachWalk) keyBodies(held heldTypes, from string) {
	if held.any {
		if w.keys && w.edges == nil {
			return
		}
		w.keys = true
	}
	for _, table := range []map[string]*ir.Symbol{w.m.equates, w.m.hashes} {
		for _, sym := range byTypeName(table, &held) {
			w.symbol(sym, from, NotRetained, EdgeKeys)
		}
	}
}

// rowDebugs queues the hand-written Debug bodies a failed test's report runs
// for a value of held (rowText): every value of a type with one, at any
// depth, renders through it.
func (w *reachWalk) rowDebugs(held heldTypes, from string) {
	for _, sym := range byTypeName(w.m.rowDebugs, &held) {
		w.symbol(sym, from, NotRetained, EdgeDebug)
	}
}

// returnedHeld is the declared types a failed test's report may render
// under `returned:` when a `?` on a value of t ends the case, or a group's
// boot returns one (returnedText): what the payload of its `Err` holds,
// rendered as a row is. A `None` holds nothing, and neither does an `Ok`,
// which does not end the case. A t whose variants are not stated does not
// bound what it holds.
func (w *reachWalk) returnedHeld(t *ir.ValType) heldTypes {
	if t == nil || t.Kind() != ir.KindEnum || t.Layout() == nil {
		return heldTypes{any: true}
	}
	var out heldTypes
	for _, v := range t.Layout().Variants {
		if v.Name != "Err" {
			continue
		}
		for _, field := range v.Fields {
			if out.merge(w.heldIn(field.Type, true)) {
				return out
			}
		}
	}
	return out
}

// byTypeName is a table of bodies by type name (an impl of one method, a
// Display, a row Debug, a key body) in the order of the names, which the
// walk queues them in, so the walk, and the chain it reports for each
// function, is the same every time. With held, only the bodies of the types
// it holds; with nil, every body.
func byTypeName(table map[string]*ir.Symbol, held *heldTypes) []*ir.Symbol {
	// A bounded held selects a few of a table that holds every type's body,
	// so only an unbounded one sizes the names to the table.
	var names []string
	if held == nil || held.any {
		names = make([]string, 0, len(table))
	}
	for name := range table {
		if held == nil || held.holds(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := make([]*ir.Symbol, len(names))
	for i, name := range names {
		out[i] = table[name]
	}
	return out
}

// heldTypes is the declared types a value of some type may be or hold, at
// any depth, by the name a table of bodies (Display, Equatable, Hashable)
// keys a type's body under: the bodies an operation on the value may select
// by a value's type name (displayErased, displayShape, keyCaller). any
// says some part's type does not bound what it holds, so every body may be
// selected.
type heldTypes struct {
	names map[string]bool
	any   bool
}

// holds reports whether a table's entry named name is the body of a type h
// holds: its declaration's name, or a generic instance's key (implOf), which
// is the declaration's name with its arguments (`main.Box<Int>`).
func (h *heldTypes) holds(name string) bool {
	if h.any {
		return true
	}
	if i := strings.IndexByte(name, '<'); i >= 0 {
		name = name[:i]
	}
	return h.names[name]
}

// heldKey keys reachWalk.held.
type heldKey struct {
	t        *ir.ValType
	rendered bool
}

// heldBy is the declared types the values of uses, temporaries of f, may be
// or hold (heldTypes). rendered says the values are rendered by Display,
// which renders an Iter as its source: a collection, or a value of any type
// that implements Iter, so an Iter's element type does not bound what a
// rendered one holds. Hashing and comparing read an Iter's elements only.
func (w *reachWalk) heldBy(f *ir.Func, uses []ir.Temp, rendered bool) heldTypes {
	var out heldTypes
	for _, u := range uses {
		if out.merge(w.heldIn(f.TempType(u), rendered)) {
			return out
		}
	}
	return out
}

// merge adds what o holds to h and reports whether h is then unbounded.
func (h *heldTypes) merge(o heldTypes) bool {
	if o.any {
		*h = o
		return true
	}
	for name := range o.names {
		if h.names == nil {
			h.names = map[string]bool{}
		}
		h.names[name] = true
	}
	return false
}

// heldIn is heldTypes for a value of t, memoized.
func (w *reachWalk) heldIn(t *ir.ValType, rendered bool) heldTypes {
	key := heldKey{t, rendered}
	if h, ok := w.held[key]; ok {
		return h
	}
	h := heldTypes{names: map[string]bool{}}
	h.add(t, rendered, map[*ir.ValType]bool{})
	if w.held == nil {
		w.held = map[heldKey]heldTypes{}
	}
	w.held[key] = h
	return h
}

// add adds the declared types a value of t may be or hold. A struct or an
// enum holds what its stated layout's fields and variants hold (an `embeds`
// variant's payload is the value it holds, under its own type's name), a
// distinct its inner value, and a composite or a function what its
// components and result hold (a function's result may be hashed, as
// group_by's key is). A nil type (none stated), an erased position, an
// interface value, a struct or enum whose layout is not stated, and an Iter
// rendered do not bound what they hold. seen holds the types entered, since
// a type may hold itself through a field.
func (h *heldTypes) add(t *ir.ValType, rendered bool, seen map[*ir.ValType]bool) {
	if h.any {
		return
	}
	if t == nil {
		h.any = true
		return
	}
	if seen[t] {
		return
	}
	seen[t] = true
	switch t.Kind() {
	case ir.KindAny, ir.KindIface:
		h.any = true
		return
	case ir.KindSeq:
		if rendered {
			h.any = true
			return
		}
	case ir.KindBytes:
		// A Bytes value is no record; its Display is recorded under the
		// name it always has (displayErased).
		h.names[rt.BytesTypeName] = true
	case ir.KindStruct, ir.KindEnum:
		h.names[t.Sym().Name()] = true
		l := t.Layout()
		if l == nil {
			h.any = true
			return
		}
		for _, f := range l.Fields {
			h.add(f.Type, rendered, seen)
		}
		for _, v := range l.Variants {
			for _, f := range v.Fields {
				h.add(f.Type, rendered, seen)
			}
		}
	case ir.KindDistinct, ir.KindHandle:
		h.names[t.Sym().Name()] = true
	}
	for i := range t.NumElems() {
		h.add(t.Elem(i), rendered, seen)
	}
	if r := t.Result(); r != nil {
		h.add(r, rendered, seen)
	}
}

// inOrder is the functions a walk reached, by position and then name.
func inOrder(reached map[*ir.Func]bool) []*ir.Func {
	out := make([]*ir.Func, 0, len(reached))
	for f := range reached {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return funcBefore(out[i], out[j]) })
	return out
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

func (w *reachWalk) report(kind UnretainedKind, name, from string, impl ir.ImplFunc) {
	key := name + "\x00" + string(rune('0'+kind))
	if _, dup := w.found[key]; !dup {
		w.found[key] = Unretained{Kind: kind, Name: name, Impl: impl, From: from, Chain: w.chain(from)}
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
		impl, _ := sym.Impl()
		w.report(kind, sym.Name(), from, impl)
		return
	}
	w.push(f, edge)
}

func (w *reachWalk) visit(f *ir.Func) {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			w.at = in
			if w.effects != nil {
				switch call, kind := w.m.effectOf(in, f.Name()); kind {
				case refuseEffect:
					w.effects[call] = true
				case refuseUnbounded:
					w.unbounded[call] = true
				case refuseSeeded:
					w.seeded[call] = true
				}
			}
			if w.uses != nil {
				if field := appFieldOf(in); field != "" && !slices.Contains(w.uses[f], field) {
					w.uses[f] = append(w.uses[f], field)
				}
			}
			if uses := keyOperands(in); len(uses) > 0 {
				w.keyBodies(w.heldBy(f, uses, false), f.Name())
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
						w.report(OnceNotRetained, n.Sym().Name(), f.Name(), ir.ImplFunc{})
						continue
					}
					w.push(cell.body, EdgeOnce)
				}
			case *ir.Assert, *ir.Record, *ir.Subtests:
				// A `values:` row renders each value the instruction
				// reports (rowText).
				w.rowDebugs(w.heldBy(f, in.AppendUses(nil), true), f.Name())
			case *ir.Try:
				// A `?` that ends a test body leaves the `Err` or `None` it
				// read in the case's report, rendered as a row is
				// (returnedText). The walk does not tell a case's body
				// from another, so every `?` reaches them: an `Err`
				// payload's written Debug bodies are few.
				w.rowDebugs(w.returnedHeld(f.TempType(n.Src())), f.Name())
			case *ir.Render:
				edge := EdgeDebug
				if n.Kind() == ir.RenderDisplay {
					edge = EdgeDisplay
				}
				for _, impl := range n.PartImpls() {
					w.symbol(impl.Fn, f.Name(), NotRetained, edge)
				}
				if n.Kind() == ir.RenderDisplay {
					// An erased rendering selects a Display body by the
					// value's type, which may be any; one by shape
					// (displayShape) renders each nested value of a
					// declared type its impls do not name through the
					// machine's, which its static type bounds.
					held := heldTypes{any: true}
					if !n.Erased() {
						held = w.heldBy(f, []ir.Temp{n.Src()}, true)
					}
					for _, sym := range byTypeName(w.m.displays, &held) {
						w.symbol(sym, f.Name(), NotRetained, EdgeDisplay)
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
				w.report(NoBinding, name, from, ir.ImplFunc{})
			}
			return
		}
		w.symbol(n.Callee(), from, NotRetained, EdgeCall)
	case ir.CalleeDispatched, ir.CalleeBound:
		// A bound call, in an abstract body, is the interface's function for
		// some type, as a dispatch is for the operand's: either reaches every
		// implementation the program retains, and a bound call those every
		// file records (boundImpls).
		impls := byTypeName(w.m.dispatch[n.Callee()], nil)
		if n.Form() == ir.CalleeBound {
			impls = w.m.boundImpls(n.Callee())
		}
		if len(impls) == 0 {
			// A method no module implements at all never runs: no value
			// of an implementing type exists, so the call is only ever
			// reached over none (a dispatch over an empty list). Were it
			// reached, it stops with a DispatchMiss.
			if w.m.unretainedImpls[n.Callee()] {
				w.report(NoImplementation, n.Callee().Name(), from, ir.ImplFunc{})
			}
			return
		}
		for _, impl := range impls {
			w.symbol(impl, from, NotRetained, EdgeDispatch)
		}
	}
}

// boundImpls is every implementation of the interface function method
// names that the program's modules record, under method and under every
// other symbol of that name, each once, by type name and then name: a std
// interface of a fixed table (Display, Debug, Comparable) has a symbol per
// file, each recording that file's implementations, and a call through a
// type parameter's bound may run any of them. Two interfaces of one name,
// which only a program's own shadowing makes, answer each other's too,
// which over-approximates.
func (m *Machine) boundImpls(method *ir.Symbol) []*ir.Symbol {
	type entry struct {
		typ  string
		impl *ir.Symbol
	}
	var all []entry
	seen := map[*ir.Symbol]bool{}
	for sym, table := range m.dispatch {
		if sym != method && sym.Name() != method.Name() {
			continue
		}
		for typ, impl := range table {
			if !seen[impl] {
				seen[impl] = true
				all = append(all, entry{typ, impl})
			}
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].typ != all[j].typ {
			return all[i].typ < all[j].typ
		}
		return all[i].impl.Name() < all[j].impl.Name()
	})
	out := make([]*ir.Symbol, len(all))
	for i, e := range all {
		out[i] = e.impl
	}
	return out
}
