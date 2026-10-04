package irbuild

import (
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// The module's tail-call graph.
//
// # Why this file exists rather than a map of names
//
// Nomi guarantees constant-stack tail calls with no annotation and no
// diagnostic (spec §12.7). Go eliminates none, so a tail-call CYCLE among
// compiled functions is either lowered explicitly or refused by name — never
// lowered into a plain recursive Go call, which works until its input grows
// and then dies as `fatal error: stack overflow` rather than as a Nomi error.
//
// A graph over the module's FREE functions alone would miss impl functions,
// and
//
//	impl Stepper for Down {
//	  fn step(value: Down, n: Int): Int {
//	    if n == 0 { 0 } else { Stepper.step(value, n - 1) }
//	  }
//	}
//
// would then lower to a plain recursive call and overflow the stack at a large
// enough `n`. So the node set is the whole point of this file: it holds one
// node per LOWERED FUNCTION, free or impl, and there is no filter that can
// silently drop a class of them.
//
// # The graph is deliberately an OVER-approximation
//
// A spurious edge widens a cycle, which costs a wider driver. A MISSED edge is
// unbounded Go recursion. The two errors are not comparable, so every
// ambiguous callee spelling adds edges to every unit it could reach:
// `Owner.method(...)` adds one to every impl item named `method` under an impl
// block whose interface or receiver is spelled `Owner`, without resolving which
// one the arguments would actually select. Resolution lives in implCall and
// depends on argument kinds; reproducing it here would be a second
// implementation of it, and the one that got out of step would be the one that
// produces a stack overflow.
//
// # What is NOT an edge, and why that is sound rather than an omission
//
//   - A call into another MODULE. The graph is built per module. The VM runs
//     every marked tail call by replacing the caller's activation
//     (`ir.TailTransfers`), whichever module the callee is in, so a cycle
//     that spans modules needs no plan here.
//   - A call through a function VALUE gets edges of its own, from
//     collectValueTargets: every unit the module names as a value, and every
//     unit a lambda's own tail call reaches.

// tailUnit is one node of the module's tail-call graph: one function this
// builder lowers. Exactly one of fn and item is set.
type tailUnit struct {
	fn   *ast.FuncDef
	item *implItem
	// impl is the block item belongs to, nil for a free function. Read for
	// the position a synthesized body is attributed to and for whether the
	// block itself is one this builder lowers at all: implDecl refuses a whole
	// unlowerable block, so a plan holding one of its items' bodies would
	// cover a body no ordinary path lowers.
	impl *implDef
	// label is what a refusal at a call into this unit names.
	label string
	body  *ast.Block
	edges []int
	// member is this unit's value of the driver's selector.
	member int
}

// buildTailGraph builds the module's tail-call graph over its free functions
// and its impl functions, and records which units sit on a cycle.
//
// Called after buildImpls, because an impl block's items — including the
// interface defaults monomorphized into it — are what half the nodes are.
func (g *gen) buildTailGraph(decls map[string]*ast.FuncDef) {
	g.tailUnits = nil
	g.tailOfFn = map[string]int{}
	g.tailOfItem = map[*implItem]int{}

	for _, n := range g.nodes {
		fd, ok := n.(*ast.FuncDef)
		if !ok || fd.ImplFunction || fd.Body == nil {
			continue
		}
		if decls[fd.Name] != fd {
			continue
		}
		g.tailOfFn[fd.Name] = len(g.tailUnits)
		g.tailUnits = append(g.tailUnits, &tailUnit{fn: fd, label: fd.Name, body: fd.Body})
	}
	for _, d := range g.implOrder {
		for _, it := range d.order {
			if it.body == nil {
				continue
			}
			g.tailOfItem[it] = len(g.tailUnits)
			g.tailUnits = append(g.tailUnits, &tailUnit{
				item:  it,
				impl:  d,
				label: d.label() + "." + it.name,
				body:  it.body,
			})
		}
	}

	// A tail call through a function value can reach any function the module
	// uses as a value, and the tail calls any lambda makes: see
	// collectValueTargets.
	valueTargets := map[int]bool{}
	for _, n := range g.nodes {
		g.collectValueTargets(n, valueTargets)
	}

	for _, u := range g.tailUnits {
		seen := map[int]bool{}
		g.collectTailEdges(u.body, seen)
		if g.makesIndirectTailCall(u.body) {
			for i := range valueTargets {
				seen[i] = true
			}
		}
		for i := range seen {
			u.edges = append(u.edges, i)
		}
		// Sorted, because the adjacency order decides Tarjan's component order
		// and Generate is content addressed: a driver assembled from map
		// iteration order would give one program two cache directories.
		slices.Sort(u.edges)
	}
	g.planTailCycles()
}

// collectTailEdges records every unit a tail-marked call inside n could reach.
func (g *gen) collectTailEdges(n ast.Node, out map[int]bool) {
	if isNilNode(n) {
		return
	}
	if call, ok := n.(*ast.Call); ok && call.IsTailCall {
		switch callee := call.Func.(type) {
		case *ast.Ident:
			if i, local := g.tailOfFn[callee.Name]; local {
				out[i] = true
			}
		case *ast.FieldAccess:
			if ti, isType := callee.Object.(*ast.TypeIdent); isType && callee.Field != nil {
				for _, i := range g.tailItemsFor(ti.Name, callee.Field.Name) {
					out[i] = true
				}
			}
		}
	}
	for _, child := range childNodes(n) {
		g.collectTailEdges(child, out)
	}
}

// collectValueTargets records every unit a tail call through a function value
// could reach: each module function or impl function the module names as a
// value rather than calling it, and every unit a lambda's own tail call
// reaches, since a lambda is a value that tail-calls.
//
// Named function values can close a cycle: `fn apply(f, n) { f(n) }` with
// `fn bounce(n) { ... apply(bounce, n - 1) }` would otherwise overflow the
// stack at 1e8 hops. The over-approximation is the same one the
// rest of the graph makes: a spurious edge costs a refusal, a missed one a
// crash.
func (g *gen) collectValueTargets(n ast.Node, out map[int]bool) {
	if isNilNode(n) {
		return
	}
	switch x := n.(type) {
	case *ast.Call:
		// The callee position is a call, not a value; its arguments are.
		if fa, qualified := x.Func.(*ast.FieldAccess); qualified {
			g.collectValueTargets(fa.Object, out)
		} else if _, named := x.Func.(*ast.Ident); !named {
			g.collectValueTargets(x.Func, out)
		}
		for _, a := range x.Args {
			g.collectValueTargets(a, out)
		}
		return
	case *ast.Ident:
		if i, local := g.tailOfFn[x.Name]; local {
			out[i] = true
		}
		return
	case *ast.FieldAccess:
		if ti, isType := x.Object.(*ast.TypeIdent); isType && x.Field != nil {
			for _, i := range g.tailItemsFor(ti.Name, x.Field.Name) {
				out[i] = true
			}
		}
	case *ast.Lambda:
		g.collectTailEdges(x.Body, out)
	}
	for _, child := range childNodes(n) {
		g.collectValueTargets(child, out)
	}
}

// makesIndirectTailCall reports a body with a tail call whose callee is a
// value: a name that is not one of this module's functions, or an expression.
func (g *gen) makesIndirectTailCall(n ast.Node) bool {
	if isNilNode(n) {
		return false
	}
	if call, ok := n.(*ast.Call); ok && call.IsTailCall {
		switch callee := call.Func.(type) {
		case *ast.Ident:
			if _, local := g.tailOfFn[callee.Name]; !local {
				return true
			}
		case *ast.FieldAccess, *ast.TypeIdent:
		default:
			return true
		}
	}
	for _, child := range childNodes(n) {
		if g.makesIndirectTailCall(child) {
			return true
		}
	}
	return false
}

// tailItemsFor returns every impl unit a call spelled `owner.method` could
// reach in this module.
//
// Matched on the interface name AND on the receiver's spelling, because both
// `Stepper.step(v, n)` and `Down.step(v, n)` reach the same function, and the
// existential form dispatches to every implementor of the interface. No
// argument is looked at: see the over-approximation note at the top.
func (g *gen) tailItemsFor(owner, method string) []int {
	var out []int
	for _, d := range g.implOrder {
		if d.ifaceName != owner && typeText(d.decl.Receiver) != owner {
			continue
		}
		if it := d.items[method]; it != nil {
			if i, known := g.tailOfItem[it]; known {
				out = append(out, i)
			}
		}
	}
	return out
}

// --- the cycles, and what is done with each -------------------------------

// tailPlan is one strongly connected component of the tail-call graph together
// with the decision about how it is lowered.
//
// A component is a component and not a "set of functions that reach
// themselves": a transitive closure answers "can f reach f" and that is the
// same answer for a self-call and for a four-member cycle, while the lowering
// is a rewrite of one body in the first case and a shared driver in the
// second.
type tailPlan struct {
	// units are the member indices, ascending, so the member SELECTOR is
	// declaration order and the plan is deterministic.
	units []int
	// ok is set when this cycle is lowered as a driver loop.
	ok bool
	// why names the cycle SHAPE for a refusal. Empty when a member is refused
	// for its OWN reason, in which case a call site declines instead of adding
	// a second key for the same gap — the cascade.go discipline.
	why string
	// result is the SCC's one result kind, for a multi-member driver.
	result kind
	// sigs is each member's driver shape, parallel to units, settled at
	// decision time, so no later reader can re-derive a different parameter
	// list for a member.
	sigs []tailSig
}

func (p *tailPlan) multi() bool { return len(p.units) > 1 }

// planTailCycles partitions the graph into strongly connected components and
// decides each one.
func (g *gen) planTailCycles() {
	g.tailPlanOf = make([]*tailPlan, len(g.tailUnits))
	for _, comp := range tailSCCs(g.tailUnits) {
		if len(comp) == 1 && !slices.Contains(g.tailUnits[comp[0]].edges, comp[0]) {
			// A one-member component with no self-edge is not a cycle.
			continue
		}
		slices.Sort(comp)
		p := &tailPlan{units: comp}
		g.decideTailPlan(p)
		for _, i := range comp {
			g.tailPlanOf[i] = p
		}
	}
}

// decideTailPlan settles whether a cycle is lowered here or refused by name.
//
// SELF-recursion is always lowered: the rewrite is local to the one body — the
// arguments go into temporaries, the parameters are rebound, and control
// `continue`s — so it is lowerable exactly when the function itself is.
//
// A MULTI-MEMBER cycle holds FREE and IMPL bodies alike. An impl member
// differs from a free one only in where its shape is read from:
//
//   - The RECEIVER is an ordinary parameter in whatever slot the signature put
//     it, which is what implFunc's own doc comment says and what the item
//     carries: `fn step(value: Down, n: Int)` is two parameters with two
//     kinds, the first of them `Down`. The driver's parameter union already
//     holds parameters, so it already holds receivers.
//   - An interface default's MONOMORPHIZATION is already resolved into the
//     implItem by inheritDefaults, before any lowering: params0 and line come
//     from the INTERFACE's declaration. The driver reads the same fields
//     implFunc reads, so a monomorphized default lowers into a driver arm for
//     the same reason it lowers into its own function.
//
// The two shapes are READ from different places — `*ast.FuncDef` plus g.funcs
// for one, `*implItem` for the other — so
// tailSig names the five fields once and both paths supply them.
//
// What is still refused, each a real shape with a witness rather than a hedge:
//
//   - Members whose declared results differ. The driver has one result slot.
//     `fn wide(n: Int): Shape` / `fn narrow(n: Int): Circle` over
//     `enum Shape { embeds Circle; ... }` type-checks clean, so this is a
//     lowering limit rather than a soundness bug, and the
//     refusal costs nothing because the shape had to be constructed.
//   - A cycle closed by EXISTENTIAL dispatch, refused at the dispatch site by
//     tableCall rather than here, because that is a different obstacle and
//     must not be conflated with this one: the receiver is erased, so the
//     callee is chosen at run time and there is no member to select. A driver
//     could hold every implementation's body and still have nothing to write
//     for the hop.
func (g *gen) decideTailPlan(p *tailPlan) {
	if !p.multi() {
		p.ok = true
		return
	}
	sigs := make([]tailSig, len(p.units))
	for n, i := range p.units {
		u := g.tailUnits[i]
		sig, plain := g.tailMemberSig(u)
		if !plain {
			// The member is refused at its own declaration. A second key here
			// would tally one gap twice; the call site declines instead.
			p.ok = false
			p.why = ""
			return
		}
		if n > 0 && sig.result != sigs[0].result {
			p.ok = false
			p.why = g.tailCycleText(p) + " with mixed result types (" +
				g.tailUnits[p.units[0]].label + ": " + sigs[0].result.nomi() + ", " +
				u.label + ": " + sig.result.nomi() + ")"
			return
		}
		sigs[n] = sig
		u.member = n
	}
	p.ok = true
	p.sigs = sigs
	p.result = sigs[0].result
}

// tailSig is everything a plan needs to know about one member, whichever
// lowering path that member's own body takes.
//
// This type IS the impl/free boundary, and naming it is the whole of crossing
// it. A free body's shape is read off *ast.FuncDef plus g.funcs[name]; an impl
// body's off *implItem. Nothing else about the two differed.
type tailSig struct {
	// params are the parameters LOWERED, which for an inherited interface
	// default are the INTERFACE's — so a driver arm's positions point at the
	// interface body, exactly as implFunc's do.
	params []ast.Param
	kinds  []kind
	result kind
	// line is the position this member's arm is attributed to. A synthesized
	// body has none a programmer wrote: derive synthesis allocates from a band
	// around 2^30 that emittableLine refuses, so the arm inherits the
	// RECEIVER's declaration, which is where the `derive` sits. Unreachable —
	// see TestTail_ASynthesizedBodyIsNeverACycleMember — and
	// carried anyway, because it is two lines and the alternative is a driver
	// arm with no source position.
	line int
}

// tailMemberSig is a member's driver shape, or false when its own declaration
// is one this builder refuses. Everything it excludes is already refused where
// it was written, so a cycle containing one DECLINES rather than adding a
// second key for one gap — the cascade.go discipline.
func (g *gen) tailMemberSig(u *tailUnit) (tailSig, bool) {
	if u == nil || u.body == nil {
		return tailSig{}, false
	}
	var sig tailSig
	switch {
	case u.fn != nil:
		fd := u.fn
		if fd.ReturnTypeExpr == nil ||
			len(fd.TypeParams) > 0 || len(fd.WhereClauses) > 0 || len(fd.Decorators) > 0 {
			return tailSig{}, false
		}
		fs := g.funcs[fd.Name]
		if fs == nil || !fs.lowerable {
			return tailSig{}, false
		}
		for _, prm := range fd.Params {
			// funcDecl refuses each of these at the declaration under its own
			// key: `parameter without a declared type`, `destructuring
			// parameter`.
			if prm.TypeAnnotation == nil || prm.Destructure != nil {
				return tailSig{}, false
			}
		}
		sig = tailSig{
			params: fd.Params,
			kinds:  fs.params,
			result: fs.result,
			line:   fd.Line,
		}
	case u.item != nil:
		// An unlowerable BLOCK never reaches implFunc — implDecl refuses the
		// whole thing — so a driver holding one of its items' bodies would emit
		// a body no ordinary path emits, at a position already refused.
		//
		// Unreachable, by two independent routes, and kept for
		// the same reason the synth line fallback below is: a block refused at
		// DECLARATION time has `order` empty, so it contributes no graph node
		// at all; and the only route to an unlowerable block that still has
		// items is buildImpls' speculation, which runs for SYNTHESIZED blocks
		// only — and no synthesized body contains a tail-marked call. Falsified,
		// this predicate is a wrong answer rather than a refusal, which is the
		// wrong side to be cheap on.
		//
		// implItem has a `lowerable` field and it is NOT read here: nothing in
		// this package ever sets it false.
		if u.impl == nil || !u.impl.lowerable {
			return tailSig{}, false
		}
		if len(u.item.params0) != len(u.item.params) {
			return tailSig{}, false
		}
		line := u.item.line
		if u.impl.synth && u.impl.recv.def != nil {
			line = u.impl.recv.def.line
		}
		sig = tailSig{
			params: u.item.params0,
			kinds:  u.item.params,
			result: u.item.result,
			line:   line,
		}
	default:
		return tailSig{}, false
	}
	// kindInvalid: cascade — a member whose result type this builder cannot represent is refused at its own declaration; the cycle declines.
	if sig.result == kindInvalid || len(sig.params) != len(sig.kinds) {
		return tailSig{}, false
	}
	for i, prm := range sig.params {
		// kindInvalid: cascade — a member whose parameter type this builder cannot represent is refused at its own declaration; the cycle declines.
		if prm.Destructure != nil || sig.kinds[i] == kindInvalid {
			return tailSig{}, false
		}
	}
	return sig, true
}

// tailCycleText renders a cycle for a diagnostic: `is_even -> is_odd -> is_even`.
func (g *gen) tailCycleText(p *tailPlan) string {
	var b strings.Builder
	for _, i := range p.units {
		b.WriteString(g.tailUnits[i].label)
		b.WriteString(" -> ")
	}
	b.WriteString(g.tailUnits[p.units[0]].label)
	return b.String()
}

// tailSCCs returns the graph's strongly connected components (Tarjan).
//
// Deterministic: the node order is declaration order and every adjacency list
// is sorted, so the components and their contents do not depend on map
// iteration. That is load-bearing rather than tidy — Generate is content
// addressed, so a nondeterministic driver would give one program two cache
// directories.
func tailSCCs(units []*tailUnit) [][]int {
	const unvisited = -1
	n := len(units)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = unvisited
	}
	var stack []int
	var out [][]int
	next := 0

	// Iterative rather than recursive: a module's tail-call graph is small, but
	// a recursive Tarjan over a deep chain is exactly the unbounded host
	// recursion this file exists to prevent, and writing one here would be
	// funny in the wrong way.
	type frame struct{ v, edge int }
	for root := range n {
		if index[root] != unvisited {
			continue
		}
		work := []frame{{v: root}}
		index[root], low[root] = next, next
		next++
		stack = append(stack, root)
		onStack[root] = true
		for len(work) > 0 {
			f := &work[len(work)-1]
			if f.edge < len(units[f.v].edges) {
				w := units[f.v].edges[f.edge]
				f.edge++
				switch {
				case index[w] == unvisited:
					index[w], low[w] = next, next
					next++
					stack = append(stack, w)
					onStack[w] = true
					work = append(work, frame{v: w})
				case onStack[w]:
					low[f.v] = min(low[f.v], index[w])
				}
				continue
			}
			v := f.v
			work = work[:len(work)-1]
			if len(work) > 0 {
				parent := work[len(work)-1].v
				low[parent] = min(low[parent], low[v])
			}
			if low[v] != index[v] {
				continue
			}
			var comp []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			out = append(out, comp)
		}
	}
	return out
}

// tailPlanForFn is the cycle a tail call to this module's free function belongs
// to, or nil when the callee cannot recur.
func (g *gen) tailPlanForFn(name string) *tailPlan {
	i, ok := g.tailOfFn[name]
	if !ok || i >= len(g.tailPlanOf) {
		return nil
	}
	return g.tailPlanOf[i]
}

// Nothing builds a driver from a plan. A member of a planned cycle is built as
// an ordinary body with its tail calls marked, and the VM runs each marked call by replacing the
// caller's activation (`ir.TailTransfers`, internal/vm/tail.go), so a
// tail-recursive loop runs in constant stack whether it has one member or
// several.
