package vm

import (
	"io"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibadapters"
)

// OUTSIDE CALLS: what a program reaches outside the machine, by the std
// module its own code called.
//
// An outside call acts on, or reads from, something outside the machine:
// files, the network, processes, output and input streams, the clock,
// entropy, environment variables and arguments, tasks that outlive the call
// that starts them, a nested program, and any Go binding. Each crossing's
// declaration says whether it is one, once: an intrinsic's `world` (vm.go),
// a std row's World (internal/stdlibbindings, read here through
// stdlibadapters.World), and, for a crossing neither answers, the caller's
// table (vmhost asks std/compiler's hosts; anything else, a project's own
// `go` binding or an embedder's host function, is outside).
//
// An outside call is named by the std module the code outside std called,
// not by the module that declares the crossing: `net.dial(...)` is `net`
// though the crossing is http's host function. So the walk records every
// edge it takes (reachWalk.edges), each std function gets the outside
// crossings it reaches without leaving std, and each edge from a function
// outside std into a std function that reaches one is an outside call
// named by that std function's module, at the instruction that took the
// edge. A function outside std that std reaches back into (an impl std
// dispatches to, a Display body) names its own calls: a callback that reads
// a file is named `fs` at its own call, in its own file. A crossing made
// directly by code outside std (`io.print`, `Task.spawn`, a std host
// function used as a value, a project's binding) is named by the std module
// that declares it, which is its key's module path (`fs/host.read`) or the
// intrinsic's `std` when its key does not start with one.
//
// StdOutsideCrossings walks the same way from std functions, and answers
// which of them a call from outside std would be an outside call to, and
// OutsideHostOf which std `host fn` is an outside crossing itself: the
// stdlib reference marks those functions (cmd/nomi-docgen, "Outside calls").
//
// The environment and the arguments cross no call: the machine hands them
// to the entry's boot as a Startup (boot.go). An entry boot that takes one
// is the outside call that reads them, named `startup`, at the boot.
//
// The walk records every edge out of each function it visits, so the
// functions a walk from one function alone reaches are those its edges
// lead to, and the walk answers, for each function outside std, the
// outside calls it can reach (OutsideWalk.Reaches) without walking again:
// one pass back along the edges from the functions holding each kind of
// call. The same pass from one call's holder answers where a set of files,
// a project's own, last steps out of itself toward that call
// (OutsideWalk.Leads), which a refusal of a call in a dependency's file
// names in the project's code.

// world says whether a crossing is an outside call; the zero value is no
// decision.
type world uint8

const (
	inside world = iota + 1
	outside
)

// EdgeKind is how the reach walk got from one function to the next: the
// reason a function on a chain counts as reached.
type EdgeKind uint8

const (
	// EdgeRoot is a function the walk starts from: main, its failure
	// renderer, an exported function.
	EdgeRoot EdgeKind = iota + 1
	// EdgeTest is a test case the walk starts from.
	EdgeTest
	// EdgeBoot is a boot, its failure renderer or a test group's startup.
	EdgeBoot
	// EdgeCall is a direct call, a default-argument accessor's among them.
	EdgeCall
	// EdgeValue is a function used as a value: a closure, a lambda or a
	// named function, counted when the value is made, called or not.
	EdgeValue
	// EdgeDispatch is any implementation of the interface function a
	// dispatched call names, whether or not a value of its type is made.
	EdgeDispatch
	// EdgeDisplay is the Display body of a type a rendered value may be or
	// hold, which the rendering selects by the value's type.
	EdgeDisplay
	// EdgeDebug is a Debug body a rendering or a test's row names.
	EdgeDebug
	// EdgeKeys is the Equatable or Hashable body of a type a compared or
	// hashed value may be or hold.
	EdgeKeys
	// EdgeOnce is a `once`'s initializer.
	EdgeOnce
)

// EdgeKinds is every EdgeKind, in order.
func EdgeKinds() []EdgeKind {
	return []EdgeKind{EdgeRoot, EdgeTest, EdgeBoot, EdgeCall, EdgeValue, EdgeDispatch, EdgeDisplay, EdgeDebug, EdgeKeys, EdgeOnce}
}

func (k EdgeKind) String() string {
	switch k {
	case EdgeRoot:
		return "root"
	case EdgeTest:
		return "test"
	case EdgeBoot:
		return "boot"
	case EdgeCall:
		return "call"
	case EdgeValue:
		return "value"
	case EdgeDispatch:
		return "dispatch"
	case EdgeDisplay:
		return "display"
	case EdgeDebug:
		return "debug"
	case EdgeKeys:
		return "keys"
	case EdgeOnce:
		return "once"
	}
	return "unknown"
}

// ChainStep is one function on the way from a root to an outside call, and
// how the walk reached it.
type ChainStep struct {
	Func string
	Edge EdgeKind
	// Impl is the impl block Func is declared in, the zero ImplFunc for a
	// function of none (ir.Symbol.Impl).
	Impl ir.ImplFunc
}

// OutsideCall is one outside call a program's reachable code outside std
// makes.
type OutsideCall struct {
	// Std names the call: the std module the code called, as an import
	// names it after `std::` (`fs`, `fs::host`), or "" for a crossing no std
	// module declares (a project's binding; Crossing says which).
	Std string
	// Crossing is a crossing into Go the call leads to (`fs/host.read`),
	// the first one the walk found; "" for the boot's Startup.
	Crossing string
	// At is the call, in code outside std.
	At ir.Pos
	// Callee is what the code at At calls: a std function (`fs.read_text`),
	// or Crossing when the code crosses itself.
	Callee string
	// Edge is how the code at At reaches Callee.
	Edge EdgeKind
	// Chain is the functions from a root to the one that holds the call,
	// outermost first.
	Chain []ChainStep
}

// stdImportName is a std module path (`fs/host`) as an import names it
// after `std::` (`fs::host`).
func stdImportName(path string) string {
	return strings.ReplaceAll(path, "/", "::")
}

// crossingWorld is whether the crossing named key is an outside call, and
// the std module that declares it as an import names it after `std::`, or
// "" when no std module does. other answers a key neither an intrinsic nor
// a std row does; nil, or one it answers with no std module, is a crossing
// into Go a project or an embedder supplied, which is outside.
func (m *Machine) crossingWorld(key string, other func(key string) (isOutside bool, std string)) (bool, string) {
	if in, ok := m.hosts[key]; ok {
		return in.world == outside, in.std
	}
	if isOutside, known := stdlibadapters.World(key); known {
		module, _, _ := strings.Cut(key, ".")
		return isOutside, stdImportName(module)
	}
	if other != nil {
		return other(key)
	}
	return true, ""
}

// OutsideCrossings is every crossing a machine binds that is an outside
// call, by key, with the std module that declares it as an import names it
// after `std::` (crossingWorld): the VM's intrinsics and the std binding
// rows marked outside. std/compiler's hosts, which vmhost binds, and a
// project's or an embedder's tables are not among them.
func OutsideCrossings() map[string]string {
	m := NewProgram(nil, nil, io.Discard)
	keys := stdlibadapters.Names()
	for key := range m.hosts {
		keys = append(keys, key)
	}
	out := map[string]string{}
	for _, key := range keys {
		if isOutside, std := m.crossingWorld(key, nil); isOutside && std != "" {
			out[key] = std
		}
	}
	return out
}

// OutsideWalk is what one walk over a program reaches outside the machine
// (ReachedOutsideCalls): Calls, every outside call, in position order, and
// the edges the walk took, from which Reaches answers what each function
// outside std can reach.
type OutsideWalk struct {
	Calls []OutsideCall
	// order is the functions the walk reached (inOrder), edges every edge
	// it took out of each, and holders the function whose body holds each
	// of Calls.
	order   []*ir.Func
	edges   map[*ir.Func][]reachEdge
	holders []*ir.Func
	// callersOf is the edges turned around (callers), built once.
	callersOnce sync.Once
	callersOf   map[*ir.Func][]*ir.Func
}

// FuncReach is what one function outside std that a walk reached can reach
// outside the machine (OutsideWalk.Reaches).
type FuncReach struct {
	Func *ir.Func
	// Reached is, for each key of the calls Func can reach, the one the
	// fewest edges from it.
	Reached []FuncReached
	// Sites is where code outside std calls Func, or uses it as a value.
	Sites []ir.Pos
}

// FuncReached is one call a function can reach: Call indexes
// OutsideWalk.Calls, and Through is the functions on the way after the
// function, the one whose body holds the call last; none when the
// function's own body holds it.
type FuncReached struct {
	Call    int
	Through []string
}

// Reaches is each function outside std the walk reached that can reach one
// of Calls, in position order, with the calls it can reach and the places
// code outside std calls it. A function can reach a call when the walk's
// edges lead from it to the function holding the call, so its calls are
// those a walk from it alone finds, as the walk over-approximates them.
// key sorts the calls into kinds, and a function's Reached holds one call
// of each kind it can reach, the nearest: for each kind, one pass
// backward along the edges from the functions holding its calls.
func (o *OutsideWalk) Reaches(key func(call int) string) []FuncReach {
	callers := o.callers()
	sites := map[*ir.Func][]ir.Pos{}
	for _, f := range o.order {
		for _, e := range o.edges[f] {
			if (e.kind == EdgeCall || e.kind == EdgeValue) && e.at != nil && !f.Pos().Stdlib() && !e.to.Pos().Stdlib() {
				sites[e.to] = append(sites[e.to], e.at.Pos())
			}
		}
	}
	var keys []string
	byKey := map[string][]int{}
	for i := range o.Calls {
		k := key(i)
		if _, seen := byKey[k]; !seen {
			keys = append(keys, k)
		}
		byKey[k] = append(byKey[k], i)
	}
	reached := map[*ir.Func][]FuncReached{}
	for _, k := range keys {
		// held is the call of k each holder holds first.
		held := map[*ir.Func]int{}
		for _, i := range byKey[k] {
			if h := o.holders[i]; h != nil {
				if _, seen := held[h]; !seen {
					held[h] = i
				}
			}
		}
		next := toward(callers, o.holdersOf(byKey[k]))
		for f := range next {
			if f.Pos().Stdlib() {
				continue
			}
			var through []string
			h := f
			for g := next[f]; g != nil; g = next[g] {
				through = append(through, g.Name())
				h = g
			}
			reached[f] = append(reached[f], FuncReached{Call: held[h], Through: through})
		}
	}
	var out []FuncReach
	for _, f := range o.order {
		if r := reached[f]; len(r) > 0 {
			slices.SortFunc(r, func(a, b FuncReached) int { return a.Call - b.Call })
			out = append(out, FuncReach{Func: f, Reached: r, Sites: sites[f]})
		}
	}
	return out
}

// Lead is a place code in a set of files steps out of them on the way to
// an outside call (OutsideWalk.Leads): At is the instruction that takes the
// edge, and Text says how, in the words a chain step uses (ReachText): "`main`
// calls `total`".
type Lead struct {
	At   ir.Pos
	Text string
}

// Leads is each place a function within reaches, by one edge, a function
// outside it that can reach Calls[call], in position order, once per
// position: where code within, the project's own files, last steps toward
// the call on any chain from a root. A function can reach the call when the
// walk's edges lead from it to the function holding the call, as Reaches
// decides, from the same backward pass. Nil when the call is within, or no
// edge of a function within leads to it.
func (o *OutsideWalk) Leads(call int, within func(*ir.Func) bool) []Lead {
	h := o.holders[call]
	if h == nil || within(h) {
		return nil
	}
	reaches := toward(o.callers(), []*ir.Func{h})
	var out []Lead
	seen := map[ir.Pos]bool{}
	for _, f := range o.order {
		if _, on := reaches[f]; !on || !within(f) {
			continue
		}
		for _, e := range o.edges[f] {
			if _, on := reaches[e.to]; !on || e.at == nil || within(e.to) || seen[e.at.Pos()] {
				continue
			}
			seen[e.at.Pos()] = true
			impl, _ := e.to.Sym().Impl()
			out = append(out, Lead{At: e.at.Pos(), Text: chainEdge(f.Name(), ChainStep{Func: e.to.Name(), Edge: e.kind, Impl: impl})})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return posBefore(out[i].At, out[j].At) })
	return out
}

// callers is, for each function the walk reached, the functions with an
// edge to it, each once, in the walk's order; built once.
func (o *OutsideWalk) callers() map[*ir.Func][]*ir.Func {
	o.callersOnce.Do(func() {
		o.callersOf = map[*ir.Func][]*ir.Func{}
		for _, f := range o.order {
			for _, e := range o.edges[f] {
				if cs := o.callersOf[e.to]; len(cs) == 0 || cs[len(cs)-1] != f {
					o.callersOf[e.to] = append(cs, f)
				}
			}
		}
	})
	return o.callersOf
}

// holdersOf is the functions whose bodies hold calls, each once, in the
// calls' order.
func (o *OutsideWalk) holdersOf(calls []int) []*ir.Func {
	var out []*ir.Func
	for _, i := range calls {
		if h := o.holders[i]; h != nil && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

// toward is the one pass backward along callers' edges from starts: each
// function that can reach one of them, leading one edge nearer the nearest;
// a start leads to nil.
func toward(callers map[*ir.Func][]*ir.Func, starts []*ir.Func) map[*ir.Func]*ir.Func {
	next := map[*ir.Func]*ir.Func{}
	queue := slices.Clone(starts)
	for _, s := range starts {
		next[s] = nil
	}
	for len(queue) > 0 {
		g := queue[0]
		queue = queue[1:]
		for _, f := range callers[g] {
			if _, seen := next[f]; !seen {
				next[f] = g
				queue = append(queue, f)
			}
		}
	}
	return next
}

// ReachedOutsideCalls is every outside call reached from roots, each of
// rootKind (EdgeRoot, or EdgeTest for test cases), and boots, made by code
// outside std, in position order, as an OutsideWalk. other answers whether
// a crossing neither an intrinsic nor a std row answers is outside
// (crossingWorld). Like the walk, it over-approximates: a call in a branch
// the program never takes counts, and each Chain is the path by which the
// walk first reached the function holding the call.
func ReachedOutsideCalls(entry *ir.Module, mods []*ir.Module, roots []*ir.Func, rootKind EdgeKind, boots []*ir.Symbol, other func(key string) (isOutside bool, std string)) *OutsideWalk {
	o := walkOutside(entry, mods, roots, rootKind, boots, other)
	m, w, reached, order, beneath := o.m, o.w, o.reached, o.order, o.beneath
	crossings := o.crossings

	chain := func(f *ir.Func) []ChainStep {
		var steps []ChainStep
		for g := f; g != nil && len(steps) <= len(reached); g = w.parent[g] {
			impl, _ := g.Sym().Impl()
			steps = append(steps, ChainStep{Func: g.Name(), Edge: w.kinds[g], Impl: impl})
		}
		for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
			steps[i], steps[j] = steps[j], steps[i]
		}
		return steps
	}

	// out is each call with the function whose body holds it.
	type heldCall struct {
		OutsideCall
		holder *ir.Func
	}
	var out []heldCall
	// The entry's boot reads the environment and the arguments when it
	// takes the Startup the machine builds from them. A test's boot takes
	// the Startup the test builds, which crosses nothing.
	if rootKind != EdgeTest && m.boot != nil && slices.Contains(boots, m.boot) {
		if f, err := m.resolveFunc(m.boot, "boot"); err == nil && len(f.Params()) == 1 && !f.Pos().Stdlib() {
			out = append(out, heldCall{OutsideCall{Std: "startup", At: f.Pos(), Callee: f.Name(), Edge: EdgeBoot, Chain: chain(f)}, f})
		}
	}
	for _, f := range order {
		if f.Pos().Stdlib() {
			continue
		}
		var steps []ChainStep
		for _, c := range crossings(f) {
			if steps == nil {
				steps = chain(f)
			}
			out = append(out, heldCall{OutsideCall{Std: c.std, Crossing: c.call.Callee().Name(), At: c.call.Pos(),
				Callee: c.call.Callee().Name(), Edge: EdgeCall, Chain: steps}, f})
		}
		type site struct {
			at  ir.Instr
			std string
		}
		named := map[site]bool{}
		for _, e := range w.edges[f] {
			key, reaches := beneath[e.to]
			if !reaches || !e.to.Pos().Stdlib() {
				continue
			}
			s := site{e.at, stdModuleOf(e.to.Pos().File())}
			if named[s] {
				continue
			}
			named[s] = true
			if steps == nil {
				steps = chain(f)
			}
			out = append(out, heldCall{OutsideCall{Std: s.std, Crossing: key, At: e.at.Pos(),
				Callee: reachingName(e.at, e.to), Edge: e.kind, Chain: steps}, f})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !posEqual(a.At, b.At) {
			return posBefore(a.At, b.At)
		}
		if a.Std != b.Std {
			return a.Std < b.Std
		}
		return a.Crossing < b.Crossing
	})
	walk := &OutsideWalk{order: order, edges: w.edges}
	for _, c := range out {
		walk.Calls = append(walk.Calls, c.OutsideCall)
		walk.holders = append(walk.holders, c.holder)
	}
	return walk
}

// outsideWalk is one reach walk with the outside crossings it found:
// ReachedOutsideCalls reads it from a program's roots, StdOutsideCrossings
// from std functions.
type outsideWalk struct {
	m       *Machine
	w       *reachWalk
	other   func(key string) (isOutside bool, std string)
	reached map[*ir.Func]bool
	order   []*ir.Func
	// beneath is each reached std function's outside crossing reached
	// without leaving std: the first the function itself makes, or else
	// one a std function it reaches makes, spread back along the edges
	// between std functions in position order.
	beneath map[*ir.Func]string
}

// crossing is a call that crosses into Go and is an outside call, with the
// std module that declares it (crossingWorld).
type crossing struct {
	call *ir.Call
	std  string
}

// walkOutside walks from roots, each of rootKind, and boots, and finds the
// outside crossing each std function it reaches makes or reaches.
func walkOutside(entry *ir.Module, mods []*ir.Module, roots []*ir.Func, rootKind EdgeKind, boots []*ir.Symbol, other func(key string) (isOutside bool, std string)) *outsideWalk {
	m := NewProgram(entry, mods, io.Discard)
	w := &reachWalk{m: m, seen: map[*ir.Func]bool{}, found: map[string]Unretained{}, rootKind: rootKind,
		parent: map[*ir.Func]*ir.Func{}, kinds: map[*ir.Func]EdgeKind{}, edges: map[*ir.Func][]reachEdge{}}
	reached, _ := w.run(roots, boots)
	o := &outsideWalk{m: m, w: w, other: other, reached: reached, order: inOrder(reached), beneath: map[*ir.Func]string{}}
	callers := map[*ir.Func][]*ir.Func{}
	var spread []*ir.Func
	for _, f := range o.order {
		if !f.Pos().Stdlib() {
			continue
		}
		for _, e := range w.edges[f] {
			if e.to.Pos().Stdlib() {
				callers[e.to] = append(callers[e.to], f)
			}
		}
		if cs := o.crossings(f); len(cs) > 0 {
			o.beneath[f] = cs[0].call.Callee().Name()
			spread = append(spread, f)
		}
	}
	for len(spread) > 0 {
		g := spread[0]
		spread = spread[1:]
		for _, f := range callers[g] {
			if _, done := o.beneath[f]; !done {
				o.beneath[f] = o.beneath[g]
				spread = append(spread, f)
			}
		}
	}
	return o
}

// crossings is each outside crossing f makes itself, in instruction order.
func (o *outsideWalk) crossings(f *ir.Func) []crossing {
	var out []crossing
	note := func(c *ir.Call) {
		if c == nil || c.Form() != ir.CalleeDirect || !c.Crosses() {
			return
		}
		if isOutside, std := o.m.crossingWorld(c.Callee().Name(), o.other); isOutside {
			out = append(out, crossing{c, std})
		}
	}
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
	return out
}

// StdOutside is what makes a call to a std function, from code outside std,
// an outside call (ReachedOutsideCalls).
type StdOutside struct {
	// Std names the call: the std module that declares the function, as
	// an import names it after `std::` (`fs`, `fs::testing`).
	Std string
	// Crossing is the first outside crossing the function reaches without
	// leaving std (`fs/host.read_text`).
	Crossing string
}

// StdOutsideCrossings is each of roots, std functions, that reaches an
// outside crossing without leaving std, as ReachedOutsideCalls names a
// call to it from code outside std. It is ReachedOutsideCalls' walk from
// roots alone, so it over-approximates as that does. other answers a
// crossing neither an intrinsic nor a std row answers (crossingWorld).
func StdOutsideCrossings(entry *ir.Module, mods []*ir.Module, roots []*ir.Func, other func(key string) (isOutside bool, std string)) map[*ir.Func]StdOutside {
	o := walkOutside(entry, mods, roots, EdgeRoot, nil, other)
	out := map[*ir.Func]StdOutside{}
	for _, f := range roots {
		if key, ok := o.beneath[f]; ok {
			out[f] = StdOutside{Std: stdModuleOf(f.Pos().File()), Crossing: key}
		}
	}
	return out
}

// OutsideHostOf is the outside crossing that the std module std's own
// `host fn` qualified is (`io` and `print`, `instant` and `Instant.now`), or
// "" when a call to it is no outside call. std is the module as an import
// names it after `std::`, and qualified is the function's name, after its
// owner type's for a type's function. A std binding row is keyed by the
// module's path and the qualified name (`instant.Instant.now`), and an
// intrinsic by the key its table gives it, with its std naming the module
// (`Task.spawn` in std::tasks). other answers a key neither an intrinsic
// nor a std row does, as for crossingWorld.
func OutsideHostOf(std, qualified string, other func(key string) (isOutside bool, std string)) string {
	m := hostsMachine()
	for _, key := range []string{strings.ReplaceAll(std, "::", "/") + "." + qualified, qualified} {
		if isOutside, declaredIn := m.crossingWorld(key, other); isOutside && declaredIn == std {
			return key
		}
	}
	return ""
}

// hostsMachine is a machine with no program, built once, whose table of
// intrinsics crossingWorld reads.
var hostsMachine = sync.OnceValue(func() *Machine { return NewProgram(nil, nil, io.Discard) })

// funcBefore orders functions by position, then name.
func funcBefore(a, b *ir.Func) bool {
	if !posEqual(a.Pos(), b.Pos()) {
		return posBefore(a.Pos(), b.Pos())
	}
	return a.Name() < b.Name()
}

func posEqual(a, b ir.Pos) bool {
	return a.File() == b.File() && a.Line() == b.Line() && a.Col() == b.Col()
}

func posBefore(a, b ir.Pos) bool {
	if a.File() != b.File() {
		return a.File() < b.File()
	}
	if a.Line() != b.Line() {
		return a.Line() < b.Line()
	}
	return a.Col() < b.Col()
}
