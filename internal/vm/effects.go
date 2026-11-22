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

// EFFECTS: the effects a program has, each named by the std module its own
// code called, which an effects list names.
//
// An effects list names every effect but three kinds of crossing. A pure
// crossing has none. A held crossing acts only through a value its operand
// already holds (a Reader, a channel end, a task, a fake run on the frame),
// and the list names the effect where that value was made: reading a file
// already open is `fs` at the open. An unlisted crossing is `dbg`: it
// prints, and the walk reports it (Unlisted on its call, named by its key),
// but no list names it, since `nomi build` refuses a program that holds
// one. Every other crossing is one: what acts
// on or reads from something outside the machine (files, the network,
// processes, output and input streams, the clock, entropy, tasks that
// outlive the call that starts them, a nested program),
// and what makes state inside it (a channel, an in-memory Reader, a
// sandbox). Each crossing's declaration says which it is, once
// (listedCrossing): an intrinsic's `pure`, `held` and `unlisted` (vm.go), a std row's
// Pure and Held (internal/stdlibbindings, read here through
// stdlibadapters.Pure and Held), and, for a crossing neither answers, the
// caller's table (vmhost asks std/compiler's hosts; anything else, a
// an embedder's host function, is one).
// Whether a crossing acts outside the machine (a row's World) decides what
// a target can run, not what a list names. Here and in the exported names,
// an effect call is a call that has an effect a list names.
//
// An effect call is named by the std module the code outside std called,
// not by the module that declares the crossing: `net.dial(...)` is `net`
// though the crossing is http's host function. So the walk records every
// edge it takes (reachWalk.edges), each std function gets the outside
// crossings it reaches without leaving std, and each edge from a function
// outside std into a std function that reaches one is an effect call
// named by that std function's module, at the instruction that took the
// edge. A function outside std that std reaches back into (an impl std
// dispatches to, a Display body) names its own calls: a callback that reads
// a file is named `fs` at its own call, in its own file. A dispatch into a
// std implementation of a type only std code makes is a held use of the
// value it is given, as a held crossing is: it names nothing, and what the
// type's std implementations reach is named where std makes the value, at
// the call that leads into the std function making it (heldDispatch). A
// type code outside std makes keeps its dispatches named, and so does a
// host value, a built-in or an enum no reached code makes. A crossing made
// directly by code outside std (`io.print`, `Task.spawn`, a std host
// function used as a value, a project's binding) is named by the std module
// that declares it, which is its key's module path (`fs/host.read`) or the
// intrinsic's `std` when its key does not start with one.
//
// StdEffectCrossings walks the same way from std functions, and answers
// which of them a call from outside std would be an effect call to, and
// EffectHostOf which std `host fn` is an effect crossing itself: the
// stdlib reference marks those functions (cmd/nomi-docgen, "Effects").
//
// The environment and the arguments cross no call: the machine hands them
// to the entry's boot as a Startup (boot.go). An entry boot that takes one
// is the effect call that reads them, named `startup`, at the boot.
//
// The walk records every edge out of each function it visits, so the
// functions a walk from one function alone reaches are those its edges
// lead to, and the walk answers, for each function outside std, the
// effect calls it can reach (EffectWalk.Reaches) without walking again:
// one pass back along the edges from the functions holding each kind of
// call. The same pass from one call's holder answers where a set of files,
// a project's own, last steps out of itself toward that call
// (EffectWalk.Leads), which a refusal of a call in a dependency's file
// names in the project's code, and the same pass from every holder turns
// the edges from the walk's roots into the call tree `nomi effects
// --tree` prints (EffectWalk.Tree). A pass back from every body that makes
// a crossing its declaration does not mark pure, held ones included, answers
// which functions can act and which calls do (EffectWalk.Acts, ActCalls),
// what an editor marks: a wider set than the effect calls, by the test
// that decides whether a test case is pure (effectOf).

// world says whether a crossing acts outside the machine, the build fact a
// target is checked by (a row's World); the zero value is no decision.
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

// ChainStep is one function on the way from a root to an effect call, and
// how the walk reached it.
type ChainStep struct {
	Func string
	Edge EdgeKind
	// Impl is the impl block Func is declared in, the zero ImplFunc for a
	// function of none (ir.Symbol.Impl).
	Impl ir.ImplFunc
}

// EffectCall is one effect call a program's reachable code outside std
// makes.
type EffectCall struct {
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
	// Unlisted reports an effect no list names (a `dbg`), named by
	// Crossing, its key; Std is then "".
	Unlisted bool
}

// stdImportName is a std module path (`fs/host`) as an import names it
// after `std::` (`fs::host`).
func stdImportName(path string) string {
	return strings.ReplaceAll(path, "/", "::")
}

// listedCrossing is whether the crossing named key is an effect the walk
// reports, neither pure nor held, the std module that declares it as an
// import names it after `std::`, or "" when no std module does, and
// whether it is unlisted, an effect no list names (`dbg`), which names no
// std module. other answers a key neither an intrinsic nor a std row
// does; nil, or one it answers with no std module, is a crossing into Go a
// project or an embedder supplied, which a list names.
func (m *Machine) listedCrossing(key string, other func(key string) (listed bool, std string)) (listed bool, std string, unlisted bool) {
	if in, ok := m.hosts[key]; ok {
		return !in.pure && !in.held, in.std, in.unlisted
	}
	if stdlibadapters.Known(key) {
		module, _, _ := strings.Cut(key, ".")
		return !stdlibadapters.Pure(key) && !stdlibadapters.Held(key), stdImportName(module), false
	}
	if other != nil {
		listed, std := other(key)
		return listed, std, false
	}
	return true, "", false
}

// EffectCrossings is every crossing a machine binds that an effect
// list names, by key, with the std module that declares it as an import
// names it after `std::` (listedCrossing): the VM's intrinsics and the std
// binding rows neither pure nor held. std/compiler's hosts, which vmhost
// binds, and a project's or an embedder's tables are not among them.
func EffectCrossings() map[string]string {
	m := NewProgram(nil, nil, io.Discard)
	keys := stdlibadapters.Names()
	for key := range m.hosts {
		keys = append(keys, key)
	}
	out := map[string]string{}
	for _, key := range keys {
		if listed, std, _ := m.listedCrossing(key, nil); listed && std != "" {
			out[key] = std
		}
	}
	return out
}

// EffectWalk is the effects one walk over a program finds
// (ReachedEffects): Calls, every effect call, in position order, and
// the edges the walk took, from which Reaches answers what each function
// outside std can reach.
type EffectWalk struct {
	Calls []EffectCall
	// order is the functions the walk reached (inOrder), edges every edge
	// it took out of each, and holders the function whose body holds each
	// of Calls.
	order   []*ir.Func
	edges   map[*ir.Func][]reachEdge
	holders []*ir.Func
	// uses is the app fields each function's own body reads or replaces
	// (appFieldOf), which Uses spreads back along the edges.
	uses map[*ir.Func][]string
	// roots is each function the walk started from, a boot among them, in
	// position order, as an edge from nowhere of the kind that reached it
	// (Tree).
	roots []reachEdge
	// parent is the function whose visit first reached each one, and
	// lateRoots each root the walk took late (FromLate).
	parent    map[*ir.Func]*ir.Func
	lateRoots map[*ir.Func]bool
	// callersOf is the edges turned around (callers), built once.
	callersOnce sync.Once
	callersOf   map[*ir.Func][]*ir.Func
	// m is the machine the walk ran on, and acts each function that can
	// act (Acts), built once.
	m        *Machine
	actsOnce sync.Once
	acts     map[*ir.Func]bool
}

// Walked is each function the walk reached, std's among them, in position
// order.
func (o *EffectWalk) Walked() []*ir.Func { return o.order }

// FromLate reports whether the walk first reached f from a late root
// (ReachedEffects' late, or a body madeBodies took), so no earlier root
// reaches it.
func (o *EffectWalk) FromLate(f *ir.Func) bool {
	if len(o.lateRoots) == 0 {
		return false
	}
	for n := 0; f != nil && n <= len(o.order); n++ {
		p, ok := o.parent[f]
		if !ok {
			return o.lateRoots[f]
		}
		f = p
	}
	return false
}

// CallFromLate reports whether the function holding Calls[i] is one the
// walk first reached from a late root (FromLate).
func (o *EffectWalk) CallFromLate(i int) bool { return o.FromLate(o.holders[i]) }

// FuncReach is the effect calls one function outside std that a walk
// reached can reach (EffectWalk.Reaches).
type FuncReach struct {
	Func *ir.Func
	// Reached is, for each key of the calls Func can reach, the one the
	// fewest edges from it.
	Reached []FuncReached
	// Sites is where code outside std calls Func, uses it as a value, or
	// reads the `once` it initializes.
	Sites []ir.Pos
}

// FuncReached is one call a function can reach: Call indexes
// EffectWalk.Calls, and Through is the functions on the way after the
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
func (o *EffectWalk) Reaches(key func(call int) string) []FuncReach {
	callers := o.callers()
	sites := o.sites()
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

// sites is, for each function outside std the walk reached, where code
// outside std calls it, a dispatched call and a rendering that names it
// among them, uses it as a value or, for a `once`'s initializer, reads the
// once, in the walk's order.
func (o *EffectWalk) sites() map[*ir.Func][]ir.Pos {
	sites := map[*ir.Func][]ir.Pos{}
	for _, f := range o.order {
		for _, e := range o.edges[f] {
			_, render := e.at.(*ir.Render)
			if (e.kind == EdgeCall || e.kind == EdgeDispatch || e.kind == EdgeValue || e.kind == EdgeOnce || e.kind == EdgeDebug && render) && e.at != nil && !f.Pos().Stdlib() && !e.to.Pos().Stdlib() {
				sites[e.to] = append(sites[e.to], e.at.Pos())
			}
		}
	}
	return sites
}

// FuncUses is the app fields one function outside std that a walk
// reached uses (EffectWalk.Uses).
type FuncUses struct {
	Func *ir.Func
	// Fields is each app field Func reads or replaces, its own body or a
	// function it can reach, by its name in the app type, sorted.
	Fields []string
	// Sites is where code outside std calls Func, uses it as a value, or
	// reads the `once` it initializes.
	Sites []ir.Pos
}

// Uses is each function outside std the walk reached that reads or
// replaces an app field, or can reach one that does, in position order,
// with the fields and the places code outside std calls it. As Reaches
// decides what a function can reach, from one pass backward along the
// edges from the functions whose bodies use each field. A field is an
// input, not an effect call: Uses informs, and no list holds it.
func (o *EffectWalk) Uses() []FuncUses {
	byField := map[string][]*ir.Func{}
	var fields []string
	for _, f := range o.order {
		for _, field := range o.uses[f] {
			if _, seen := byField[field]; !seen {
				fields = append(fields, field)
			}
			byField[field] = append(byField[field], f)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	callers := o.callers()
	used := map[*ir.Func][]string{}
	for _, field := range fields {
		for f := range toward(callers, byField[field]) {
			if !f.Pos().Stdlib() {
				used[f] = append(used[f], field)
			}
		}
	}
	sites := o.sites()
	var out []FuncUses
	for _, f := range o.order {
		if u := used[f]; len(u) > 0 {
			slices.Sort(u)
			out = append(out, FuncUses{Func: f, Fields: u, Sites: sites[f]})
		}
	}
	return out
}

// Acts reports whether f, a function the walk reached, can act: whether
// its body, or a function the walk's edges lead to from it, makes a
// crossing whose declaration does not mark it pure. That is effectOf's
// test, the one that decides whether a test case's result its code alone
// decides (ReachCase), so a held crossing (a write through a Writer it is
// passed) and a `dbg` act, as an effect call does. The edges are the
// walk's: a dispatched call reaches every implementation, and a function
// value counts where it is made. The one edge Acts does not follow is a
// `once`'s initializer: a read of a once is a value, made once.
func (o *EffectWalk) Acts(f *ir.Func) bool { return o.acting()[f] }

// acting is each function that can act (Acts), from one pass backward
// along the edges but a once's from the functions whose bodies cross.
func (o *EffectWalk) acting() map[*ir.Func]bool {
	o.actsOnce.Do(func() {
		callers := map[*ir.Func][]*ir.Func{}
		var starts []*ir.Func
		for _, f := range o.order {
			for _, e := range o.edges[f] {
				if e.kind != EdgeOnce {
					callers[e.to] = append(callers[e.to], f)
				}
			}
			if slices.ContainsFunc(f.Blocks(), func(b *ir.Block) bool { return slices.ContainsFunc(b.Instrs(), o.crossesActing) }) {
				starts = append(starts, f)
			}
		}
		o.acts = map[*ir.Func]bool{}
		for f := range toward(callers, starts) {
			o.acts[f] = true
		}
	})
	return o.acts
}

// crossesActing reports whether in makes a crossing whose declaration does
// not mark it pure (effectOf).
func (o *EffectWalk) crossesActing(in ir.Instr) bool {
	e, unbounded := o.m.effectOf(in, "")
	return e != "" && !unbounded
}

// Acting is each function outside std the walk reached that can act
// (Acts), in position order, with the places code outside std calls it,
// uses it as a value, or reads the `once` it initializes; Reached is
// empty.
func (o *EffectWalk) Acting() []FuncReach {
	acts := o.acting()
	sites := o.sites()
	var out []FuncReach
	for _, f := range o.order {
		if acts[f] && !f.Pos().Stdlib() {
			out = append(out, FuncReach{Func: f, Sites: sites[f]})
		}
	}
	return out
}

// ActCall is a call in code outside std that can act (EffectWalk.ActCalls).
type ActCall struct {
	// At is the call, and In the function whose body holds it.
	At ir.Pos
	In *ir.Func
	// Some is, for a dispatched call that only some of the implementations
	// it reaches can act in, the interface function's name; "" otherwise.
	Some string
}

// ActCalls is each call in code outside std the walk reached that can act,
// in position order: a crossing whose declaration does not mark it pure, a
// direct call of a function that can act (Acts), a dispatched call one of
// whose implementations can, a rendering one of whose named Debug bodies
// can (ir.Render's DebugImpls, which it calls for the parts its value
// holds, as a dispatch calls its implementations), and a call or an
// iteration (an ir.Iter) one of whose operands holds a function value
// whose body can act (`Iter.each(xs, io.print)`), which it may run. A
// value holds a function value when it is one, or a copy, a record, a
// closure or an iteration made over one. A function value named and not
// called and a read of a `once` are no call.
func (o *EffectWalk) ActCalls() []ActCall {
	acts := o.acting()
	var out []ActCall
	for _, f := range o.order {
		if f.Pos().Stdlib() || !acts[f] {
			continue
		}
		// What f's edges say of each instruction that took one: a direct
		// call of a function that can act, a function value whose body
		// can, and each dispatched call's implementations, and those that
		// can act.
		type tally struct{ all, acting int }
		direct := map[ir.Instr]bool{}
		value := map[ir.Instr]bool{}
		dispatch := map[ir.Instr]*tally{}
		for _, e := range o.edges[f] {
			if e.at == nil {
				continue
			}
			switch e.kind {
			case EdgeCall:
				direct[e.at] = direct[e.at] || acts[e.to]
			case EdgeValue:
				value[e.at] = value[e.at] || acts[e.to]
			case EdgeDispatch, EdgeDebug:
				t := dispatch[e.at]
				if t == nil {
					t = &tally{}
					dispatch[e.at] = t
				}
				t.all++
				if acts[e.to] {
					t.acting++
				}
			}
		}
		// held is each temporary that holds a function value whose body
		// can act.
		held := map[ir.Temp]bool{}
		var uses []ir.Temp
		holds := func(in ir.Instr) bool {
			uses = in.AppendUses(uses[:0])
			return slices.ContainsFunc(uses, func(t ir.Temp) bool { return held[t] })
		}
		for changed := true; changed; {
			changed = false
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					d := in.Dst()
					if d == ir.NoTemp || held[d] {
						continue
					}
					switch in.(type) {
					case *ir.Copy, *ir.Make, *ir.FuncValue, *ir.Iter:
						if value[in] || holds(in) {
							held[d] = true
							changed = true
						}
					case *ir.Ref:
						if value[in] {
							held[d] = true
							changed = true
						}
					}
				}
			}
		}
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				var c *ir.Call
				switch n := in.(type) {
				case *ir.Call:
					c = n
				case *ir.Defer:
					c = n.Call()
				case *ir.Iter:
					if holds(in) {
						out = append(out, ActCall{At: in.Pos(), In: f})
					}
					continue
				case *ir.Render:
					// A rendering calls the Debug body it names for each
					// part its value holds of that body's type, as a
					// dispatch does.
					switch t := dispatch[in]; {
					case t != nil && t.acting == t.all:
						out = append(out, ActCall{At: in.Pos(), In: f})
					case t != nil && t.acting > 0:
						some := ""
						if impl, ok := n.DebugImpls()[0].Fn.Impl(); ok {
							some = impl.Interface + "." + impl.Func
						}
						out = append(out, ActCall{At: in.Pos(), In: f, Some: some})
					}
					continue
				}
				if c == nil {
					continue
				}
				if c.Form() == ir.CalleeDirect && c.Crosses() {
					if o.crossesActing(c) || holds(c) {
						out = append(out, ActCall{At: c.Pos(), In: f})
					}
					continue
				}
				switch t := dispatch[in]; {
				case direct[in] || holds(c) || t != nil && t.acting == t.all:
					out = append(out, ActCall{At: in.Pos(), In: f})
				case t != nil && t.acting > 0:
					out = append(out, ActCall{At: in.Pos(), In: f, Some: c.Callee().Name()})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return posBefore(out[i].At, out[j].At) })
	return out
}

// Lead is a place code in a set of files steps out of them on the way to
// an effect call (EffectWalk.Leads): At is the instruction that takes the
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
func (o *EffectWalk) Leads(call int, within func(*ir.Func) bool) []Lead {
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

// CallNode is one function outside std on a walk's call tree
// (EffectWalk.Tree).
type CallNode struct {
	Func *ir.Func
	// Calls indexes the effect calls Func's own body holds
	// (EffectWalk.Calls).
	Calls []int
	// Next is each function outside std that Func reaches by one edge, or
	// by edges through std functions alone (a callback std calls), that
	// can reach one of Calls, each once, in the order the walk took the
	// edges, with the kind of the edge into it.
	Next []CallStep
	// Pure counts the functions outside std Func reaches so that can
	// reach none.
	Pure int
}

// CallStep is a function on a call tree and the kind of edge that
// reached it.
type CallStep struct {
	Node *CallNode
	Edge EdgeKind
}

// Tree is the functions the walk started from, each with the kind it was
// reached by, as the root of the calls below it: from each function, the
// functions outside std it reaches that can reach an effect call, as
// Reaches decides, from the same backward pass along the edges. A
// function has one node however many functions reach it, so the nodes
// form a graph, cycles included, which a caller prints as a tree by
// expanding each node once.
func (o *EffectWalk) Tree() []CallStep {
	all := make([]int, len(o.Calls))
	for i := range all {
		all[i] = i
	}
	reaching := toward(o.callers(), o.holdersOf(all))
	held := map[*ir.Func][]int{}
	for i, h := range o.holders {
		if h != nil {
			held[h] = append(held[h], i)
		}
	}
	nodes := map[*ir.Func]*CallNode{}
	var node func(f *ir.Func) *CallNode
	node = func(f *ir.Func) *CallNode {
		if n := nodes[f]; n != nil {
			return n
		}
		n := &CallNode{Func: f, Calls: held[f]}
		nodes[f] = n
		// The functions outside std f reaches, by one edge or through std
		// functions that lead back out of std.
		type next struct {
			f    *ir.Func
			kind EdgeKind
		}
		var found []next
		seen := map[*ir.Func]bool{f: true}
		var follow func(edges []reachEdge)
		follow = func(edges []reachEdge) {
			for _, e := range edges {
				if seen[e.to] {
					continue
				}
				seen[e.to] = true
				_, reaches := reaching[e.to]
				switch {
				case e.to.Pos().Stdlib() && reaches:
					follow(o.edges[e.to])
				case e.to.Pos().Stdlib():
				case reaches:
					found = append(found, next{e.to, e.kind})
				default:
					n.Pure++
				}
			}
		}
		follow(o.edges[f])
		for _, x := range found {
			n.Next = append(n.Next, CallStep{Node: node(x.f), Edge: x.kind})
		}
		return n
	}
	out := make([]CallStep, 0, len(o.roots))
	for _, r := range o.roots {
		out = append(out, CallStep{Node: node(r.to), Edge: r.kind})
	}
	return out
}

// callers is, for each function the walk reached, the functions with an
// edge to it, each once, in the walk's order; built once.
func (o *EffectWalk) callers() map[*ir.Func][]*ir.Func {
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
func (o *EffectWalk) holdersOf(calls []int) []*ir.Func {
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

// ReachedEffects is every effect call reached from roots, each of
// rootKind (EdgeRoot, or EdgeTest for test cases), and boots, then from
// each of late that nothing reached, made by code outside std, in position
// order, as an EffectWalk: a function late and roots both reach keeps the
// chain from roots. other answers whether
// a crossing neither an intrinsic nor a std row answers is an effect call
// (listedCrossing). Like the walk, it over-approximates: a call in a branch
// the program never takes counts, and each Chain is the path by which the
// walk first reached the function holding the call.
func ReachedEffects(entry *ir.Module, mods []*ir.Module, roots, late []*ir.Func, rootKind EdgeKind, boots []*ir.Symbol, other func(key string) (listed bool, std string)) *EffectWalk {
	o := walkEffects(entry, mods, roots, late, rootKind, boots, other)
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
		EffectCall
		holder *ir.Func
	}
	var out []heldCall
	// The entry's boot reads the environment and the arguments when it
	// takes the Startup the machine builds from them. A test's boot takes
	// the Startup the test builds, which crosses nothing.
	if rootKind != EdgeTest && m.boot != nil && slices.Contains(boots, m.boot) {
		if f, err := m.resolveFunc(m.boot, "boot"); err == nil && len(f.Params()) == 1 && !f.Pos().Stdlib() {
			out = append(out, heldCall{EffectCall{Std: "startup", At: f.Pos(), Callee: f.Name(), Edge: EdgeBoot, Chain: chain(f)}, f})
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
			out = append(out, heldCall{EffectCall{Std: c.std, Crossing: c.call.Callee().Name(), At: c.call.Pos(),
				Callee: c.call.Callee().Name(), Edge: EdgeCall, Chain: steps, Unlisted: c.unlisted}, f})
		}
		type site struct {
			at  ir.Instr
			std string
		}
		named := map[site]bool{}
		for _, e := range w.edges[f] {
			key, reaches := beneath[e.to]
			if !reaches || !e.to.Pos().Stdlib() || o.held(e) {
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
			out = append(out, heldCall{EffectCall{Std: s.std, Crossing: key, At: e.at.Pos(),
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
	walk := &EffectWalk{order: order, edges: w.edges, uses: w.uses, parent: w.parent, lateRoots: w.lateRoots, m: m}
	for _, f := range order {
		if _, reachedBy := w.parent[f]; !reachedBy {
			walk.roots = append(walk.roots, reachEdge{to: f, kind: w.kinds[f]})
		}
	}
	for _, c := range out {
		walk.Calls = append(walk.Calls, c.EffectCall)
		walk.holders = append(walk.holders, c.holder)
	}
	return walk
}

// effectWalk is one reach walk with the effect crossings it found:
// ReachedEffects reads it from a program's roots, StdEffectCrossings
// from std functions.
type effectWalk struct {
	m       *Machine
	w       *reachWalk
	other   func(key string) (listed bool, std string)
	reached map[*ir.Func]bool
	order   []*ir.Func
	// beneath is each reached std function's effect crossing reached
	// without leaving std: the first the function itself makes, or else
	// one a std function it reaches makes, spread back along the edges
	// between std functions in position order, a held dispatch's left out
	// and a making's to the bodies of what it makes included
	// (heldDispatch). An unlisted crossing names no std module, so a call
	// into std is never named for one.
	beneath map[*ir.Func]string
	// heldTypeOf is each std dispatch body a dispatch into names nothing,
	// with its type's name, and makes each std function that makes a value
	// of such a type, with the type's bodies (heldDispatch).
	heldTypeOf map[*ir.Func]string
	makes      map[*ir.Func][]*ir.Func
}

// crossing is a call that crosses into Go and is an effect call, with the
// std module that declares it and whether no list names it
// (listedCrossing).
type crossing struct {
	call     *ir.Call
	std      string
	unlisted bool
}

// walkEffects walks from roots, each of rootKind, and boots, then late
// (ReachedEffects), and finds the effect crossing each std function it
// reaches makes or reaches.
func walkEffects(entry *ir.Module, mods []*ir.Module, roots, late []*ir.Func, rootKind EdgeKind, boots []*ir.Symbol, other func(key string) (listed bool, std string)) *effectWalk {
	m := NewProgram(entry, mods, io.Discard)
	w := &reachWalk{m: m, seen: map[*ir.Func]bool{}, found: map[string]Unretained{}, rootKind: rootKind, late: late,
		parent: map[*ir.Func]*ir.Func{}, kinds: map[*ir.Func]EdgeKind{}, edges: map[*ir.Func][]reachEdge{},
		uses: map[*ir.Func][]string{}}
	reached, _ := w.run(roots, boots)
	o := &effectWalk{m: m, w: w, other: other, reached: reached, order: inOrder(reached), beneath: map[*ir.Func]string{}}
	o.heldDispatch()
	callers := map[*ir.Func][]*ir.Func{}
	var spread []*ir.Func
	for _, f := range o.order {
		if !f.Pos().Stdlib() {
			continue
		}
		for _, e := range w.edges[f] {
			if e.to.Pos().Stdlib() && !o.held(e) {
				callers[e.to] = append(callers[e.to], f)
			}
		}
		for _, impl := range o.makes[f] {
			callers[impl] = append(callers[impl], f)
		}
		for _, c := range o.crossings(f) {
			if !c.unlisted {
				o.beneath[f] = c.call.Callee().Name()
				spread = append(spread, f)
				break
			}
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

// heldDispatch finds the std bodies a dispatched call reaches that act
// only through the value it is given, a handle (held), and the std
// functions that make those values (makes). A value of a declared type
// that only std code constructs (an ir.Make of the type in a reached std
// function, and in no reached function outside std) is made by a call
// into std: what any of the type's std implementations does is named
// there, at the call that leads to the making, as a held crossing's
// effect is named where its value was made, so a dispatch into one of
// them names nothing. A struct no reached code constructs has no value
// for a dispatch to select, so neither does a dispatch into its bodies.
// A type whose values code outside std constructs, and an enum, a
// distinct type, a host value or a built-in that no reached code
// constructs (an adapter may make one), keeps its dispatches named.
func (o *effectWalk) heldDispatch() {
	// impls is each reached std dispatch body by the name of its type, and
	// structs the types whose bodies take a struct.
	impls := map[string][]*ir.Func{}
	structs := map[string]bool{}
	typeOf := map[*ir.Func]string{}
	for _, table := range o.m.dispatch {
		for name, sym := range table {
			f, err := o.m.resolveFunc(sym, "")
			if err != nil || !o.reached[f] || !f.Pos().Stdlib() {
				continue
			}
			base, _, _ := strings.Cut(name, "<")
			if _, done := typeOf[f]; !done {
				typeOf[f] = base
				impls[base] = append(impls[base], f)
				if ps := f.Params(); len(ps) > 0 {
					if t := f.TempType(ps[0].Temp); t != nil && t.Kind() == ir.KindStruct {
						structs[base] = true
					}
				}
			}
		}
	}
	if len(impls) == 0 {
		return
	}
	inStd := map[string][]*ir.Func{}
	outside := map[string]bool{}
	for _, f := range o.order {
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				mk, ok := in.(*ir.Make)
				if !ok || mk.Typ() == nil {
					continue
				}
				base, _, _ := strings.Cut(mk.Typ().Name(), "<")
				if _, has := impls[base]; !has {
					continue
				}
				switch {
				case !f.Pos().Stdlib():
					outside[base] = true
				case !slices.Contains(inStd[base], f):
					inStd[base] = append(inStd[base], f)
				}
			}
		}
	}
	o.heldTypeOf = map[*ir.Func]string{}
	o.makes = map[*ir.Func][]*ir.Func{}
	for base, bodies := range impls {
		makers := inStd[base]
		if outside[base] || len(makers) == 0 && !structs[base] {
			continue
		}
		for _, impl := range bodies {
			o.heldTypeOf[impl] = base
		}
		for _, f := range makers {
			o.makes[f] = append(o.makes[f], bodies...)
		}
	}
	for f := range o.makes {
		sort.Slice(o.makes[f], func(i, j int) bool { return funcBefore(o.makes[f][i], o.makes[f][j]) })
	}
}

// held reports whether e is a dispatch into a std body whose type's values
// only std code makes (heldDispatch), whose effects are named where the
// value is made.
func (o *effectWalk) held(e reachEdge) bool {
	_, ok := o.heldTypeOf[e.to]
	return ok && e.kind == EdgeDispatch
}

// crossings is each effect crossing f makes itself, in instruction order.
func (o *effectWalk) crossings(f *ir.Func) []crossing {
	var out []crossing
	note := func(c *ir.Call) {
		if c == nil || c.Form() != ir.CalleeDirect || !c.Crosses() {
			return
		}
		if listed, std, unlisted := o.m.listedCrossing(c.Callee().Name(), o.other); listed {
			out = append(out, crossing{c, std, unlisted})
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

// StdEffect is what makes a call to a std function, from code outside std,
// an effect call (ReachedEffects).
type StdEffect struct {
	// Std names the call: the std module that declares the function, as
	// an import names it after `std::` (`fs`, `fs::testing`).
	Std string
	// Crossing is the first effect crossing the function reaches without
	// leaving std (`fs/host.read_text`).
	Crossing string
}

// StdEffectCrossings is each of roots, std functions, that reaches an
// effect crossing without leaving std, as ReachedEffects names a
// call to it from code outside std. It is ReachedEffects' walk from
// roots alone, so it over-approximates as that does. other answers a
// crossing neither an intrinsic nor a std row answers (listedCrossing).
func StdEffectCrossings(entry *ir.Module, mods []*ir.Module, roots []*ir.Func, other func(key string) (listed bool, std string)) map[*ir.Func]StdEffect {
	o := walkEffects(entry, mods, roots, nil, EdgeRoot, nil, other)
	out := map[*ir.Func]StdEffect{}
	for _, f := range roots {
		if key, ok := o.beneath[f]; ok {
			out[f] = StdEffect{Std: stdModuleOf(f.Pos().File()), Crossing: key}
		}
	}
	return out
}

// EffectHostOf is the effect crossing that the std module std's own
// `host fn` qualified is (`io` and `print`, `instant` and `Instant.now`), or
// "" when a call to it is no effect call. std is the module as an import
// names it after `std::`, and qualified is the function's name, after its
// owner type's for a type's function. A std binding row is keyed by the
// module's path and the qualified name (`instant.Instant.now`), and an
// intrinsic by the key its table gives it, with its std naming the module
// (`Task.spawn` in std::tasks). other answers a key neither an intrinsic
// nor a std row does, as for listedCrossing.
func EffectHostOf(std, qualified string, other func(key string) (listed bool, std string)) string {
	m := hostsMachine()
	for _, key := range []string{strings.ReplaceAll(std, "::", "/") + "." + qualified, qualified} {
		if listed, declaredIn, _ := m.listedCrossing(key, other); listed && declaredIn == std {
			return key
		}
	}
	return ""
}

// hostsMachine is a machine with no program, built once, whose table of
// intrinsics listedCrossing reads.
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
