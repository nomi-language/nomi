package vm

import (
	"io"
	"slices"
	"sort"
	"strings"

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
// The environment and the arguments cross no call: the machine hands them
// to the entry's boot as a Startup (boot.go). An entry boot that takes one
// is the outside call that reads them, named `startup`, at the boot.

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
	// EdgeDisplay is any Display body, because a value whose Display
	// selects one by its type is rendered.
	EdgeDisplay
	// EdgeDebug is a Debug body a rendering or a test's row names.
	EdgeDebug
	// EdgeKeys is any Equatable or Hashable body, because a value of a
	// declared type is compared or hashed.
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

// ReachedOutsideCalls is every outside call reached from roots, each of
// rootKind (EdgeRoot, or EdgeTest for test cases), and boots, made by code
// outside std, in position order. other answers whether a crossing neither
// an intrinsic nor a std row answers is outside (crossingWorld). Like the
// walk, it over-approximates: a call in a branch the program never takes
// counts, and each Chain is the path by which the walk first reached the
// function holding the call.
func ReachedOutsideCalls(entry *ir.Module, mods []*ir.Module, roots []*ir.Func, rootKind EdgeKind, boots []*ir.Symbol, other func(key string) (isOutside bool, std string)) []OutsideCall {
	m := NewProgram(entry, mods, io.Discard)
	w := reachWalk{m: m, seen: map[*ir.Func]bool{}, found: map[string]Unretained{}, rootKind: rootKind,
		parent: map[*ir.Func]*ir.Func{}, kinds: map[*ir.Func]EdgeKind{}, edges: map[*ir.Func][]reachEdge{}}
	reached, _ := w.run(roots, boots)
	order := make([]*ir.Func, 0, len(reached))
	for f := range reached {
		order = append(order, f)
	}
	sort.Slice(order, func(i, j int) bool { return funcBefore(order[i], order[j]) })

	type crossing struct {
		call *ir.Call
		std  string
	}
	crossings := func(f *ir.Func) []crossing {
		var out []crossing
		note := func(c *ir.Call) {
			if c == nil || c.Form() != ir.CalleeDirect || !c.Crosses() {
				return
			}
			if isOutside, std := m.crossingWorld(c.Callee().Name(), other); isOutside {
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

	// Each std function's outside crossing reached without leaving std:
	// the first the function itself makes, or else one a std function it
	// reaches makes, spread back along the edges between std functions in
	// position order.
	beneath := map[*ir.Func]string{}
	callers := map[*ir.Func][]*ir.Func{}
	var spread []*ir.Func
	for _, f := range order {
		if !f.Pos().Stdlib() {
			continue
		}
		for _, e := range w.edges[f] {
			if e.to.Pos().Stdlib() {
				callers[e.to] = append(callers[e.to], f)
			}
		}
		if cs := crossings(f); len(cs) > 0 {
			beneath[f] = cs[0].call.Callee().Name()
			spread = append(spread, f)
		}
	}
	for len(spread) > 0 {
		g := spread[0]
		spread = spread[1:]
		for _, f := range callers[g] {
			if _, done := beneath[f]; !done {
				beneath[f] = beneath[g]
				spread = append(spread, f)
			}
		}
	}

	chain := func(f *ir.Func) []ChainStep {
		var steps []ChainStep
		for g := f; g != nil && len(steps) <= len(reached); g = w.parent[g] {
			steps = append(steps, ChainStep{Func: g.Name(), Edge: w.kinds[g]})
		}
		for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
			steps[i], steps[j] = steps[j], steps[i]
		}
		return steps
	}

	var out []OutsideCall
	// The entry's boot reads the environment and the arguments when it
	// takes the Startup the machine builds from them. A test's boot takes
	// the Startup the test builds, which crosses nothing.
	if rootKind != EdgeTest && m.boot != nil && slices.Contains(boots, m.boot) {
		if f, err := m.resolveFunc(m.boot, "boot"); err == nil && len(f.Params()) == 1 && !f.Pos().Stdlib() {
			out = append(out, OutsideCall{Std: "startup", At: f.Pos(), Callee: f.Name(), Edge: EdgeBoot, Chain: chain(f)})
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
			out = append(out, OutsideCall{Std: c.std, Crossing: c.call.Callee().Name(), At: c.call.Pos(),
				Callee: c.call.Callee().Name(), Edge: EdgeCall, Chain: steps})
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
			out = append(out, OutsideCall{Std: s.std, Crossing: key, At: e.at.Pos(),
				Callee: reachingName(e.at, e.to), Edge: e.kind, Chain: steps})
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
	return out
}

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
