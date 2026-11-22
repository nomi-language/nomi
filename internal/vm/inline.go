package vm

import (
	"os"

	"github.com/nomi-language/nomi/internal/ir"
)

// Calls compiled into their callers: the package doc, "Calls compiled
// into their callers", says what compiles in and how a view runs.

const (
	// maxInlineInstrs is the most instructions a callee compiled into its
	// caller may have, declarations not counted.
	maxInlineInstrs = 16
	// maxInlineDepth is how deep views nest in one function's code.
	maxInlineDepth = 3
)

// NoInlineEnv names the environment variable that turns compiling calls
// into their callers off for every machine the process opens.
const NoInlineEnv = "NOMI_NO_INLINE"

// WithoutInlining makes every direct call the machine compiles from now on
// open an activation, as NoInlineEnv does for a process.
func (m *Machine) WithoutInlining() *Machine {
	m.noInline = true
	return m
}

// inlines reports whether m compiles calls into their callers.
func (m *Machine) inlines() bool {
	return !m.hooked && m.dbg == nil && m.prof == nil && !m.countEntries && !m.noInline &&
		os.Getenv(NoInlineEnv) == ""
}

// inlineSite is what compiling a callee into its caller needs: the
// compiler of the code the call is in (from), the compiler of the code
// the view returns to (parent: from itself, or for a view a tail call
// switches to, the code from's view returns to), the call whose
// destination the view answers, the registers holding the operands, how
// from holds each operand it keeps fused (kind packNone for the others),
// where the view's registers start in each bank, and the view's level,
// how many views of the activation it runs inside, itself counted.
type inlineSite struct {
	from, parent *compiler
	call         *ir.Call
	args         []loc
	packs        []packedTemp
	base         [4]int
	level        int
}

// depth is how many views enclose the function cp compiles, a view a
// tail call switches to counted as its own.
func (cp *compiler) depth() int {
	n := 0
	for s := cp.site; s != nil; s = s.from.site {
		n++
	}
	return n
}

// level is the level of the view cp compiles, or 0 for a function's own
// code.
func (cp *compiler) level() int {
	if cp.site == nil {
		return 0
	}
	return cp.site.level
}

// encloses reports whether g is the function cp compiles or one a view
// around it is compiled from.
func (cp *compiler) encloses(g *ir.Func) bool {
	for p := cp; p != nil; {
		if p.c.fn == g {
			return true
		}
		if p.site == nil {
			return false
		}
		p = p.site.from
	}
	return false
}

// compilesIn reports whether a call of g from cp may compile into a view.
func (cp *compiler) compilesIn(g *ir.Func) bool {
	return cp.m.inlines() && cp.depth() < maxInlineDepth && !cp.encloses(g) && cp.m.inlinable(g) && !cp.m.isBootFunc(g)
}

// planInlines picks the calls cp's function compiles into views, before
// the analyses that read them (fuseMaybes) run.
func (cp *compiler) planInlines() {
	f := cp.c.fn
	if !cp.m.inlines() || cp.depth() >= maxInlineDepth || cp.c.guard {
		return
	}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			n, isCall := in.(*ir.Call)
			if !isCall || n.Form() != ir.CalleeDirect || n.Crosses() || cp.c.tails[n] || cp.viewTails[n] {
				continue
			}
			if _, _, crosses := cp.hostSite(n); crosses {
				continue
			}
			resolved, err := cp.m.resolveFunc(n.Callee(), f.Name())
			if err != nil {
				continue
			}
			// The function its table entry runs, which a REPL's Redirect
			// replaces.
			g := cp.m.slotFor(resolved).f
			if !cp.compilesIn(g) {
				continue
			}
			ps := g.Params()
			if len(ps) != n.NumArgs() {
				continue
			}
			fits := true
			for i, p := range ps {
				a := n.Arg(i)
				if a == ir.NoTemp || int(a) >= len(cp.c.locs) || cp.loc(a).bank() != classOf(g.TempType(p.Temp)) {
					fits = false
				}
			}
			if !fits {
				continue
			}
			if cp.plans == nil {
				cp.plans = map[*ir.Call]*ir.Func{}
			}
			cp.plans[n] = g
		}
	}
}

// inlinable reports whether g's body may be compiled into a caller,
// memoized per machine.
func (m *Machine) inlinable(g *ir.Func) bool {
	if v, ok := m.codes.inlinable.Load(g); ok {
		return v.(bool)
	}
	ok := inlinableBody(g)
	for n := range ir.TailTransfers(g) {
		if !ok {
			break
		}
		// A tail call from the program into the stdlib keeps its line for
		// a trap the stdlib raises (frame.stdEntry), which only the
		// callee's own activation carries.
		target, err := m.resolveFunc(n.Callee(), g.Name())
		if err != nil || (!g.Pos().Stdlib() && target.Pos().Stdlib()) {
			ok = false
		}
	}
	m.codes.inlinable.Store(g, ok)
	return ok
}

// inlinableBody reports whether g is small enough, and made only of
// instructions a view runs as g's activation would.
func inlinableBody(g *ir.Func) bool {
	blocks := g.Blocks()
	if len(blocks) == 0 {
		return false
	}
	params := map[ir.Temp]bool{}
	for _, p := range g.Params() {
		if p.Temp == ir.NoTemp {
			return false
		}
		params[p.Temp] = true
	}
	n := 0
	for _, b := range blocks {
		for _, in := range b.Instrs() {
			if s, isSlot := in.(*ir.Slot); isSlot {
				if params[s.Slot()] {
					return false
				}
				continue
			}
			if n++; n > maxInlineInstrs || !inlinableInstr(in) || params[in.Dst()] {
				return false
			}
		}
		switch t := b.Term().(type) {
		case *ir.Return:
			if t.Ctl() != ir.CtlNone {
				return false
			}
		case nil:
			return false
		}
	}
	for n := range ir.TailTransfers(g) {
		// A tail call through a value transfers in its handler, which a
		// view's caller cannot make in its place.
		if n.Form() != ir.CalleeDirect {
			return false
		}
	}
	return true
}

// inlinableInstr reports whether a view runs in as the callee's own
// activation does. The kinds left out keep something per activation (an
// assertion's trace, a pipeline's stages, deferred calls, a rebound app
// field or Context, a callback's control outcome), or drive callbacks
// whose ownership the activation's mask decides (an Iter). A kind added to
// the IR is left out until it is listed here.
func inlinableInstr(in ir.Instr) bool {
	switch n := in.(type) {
	case *ir.Arith, *ir.Concat, *ir.Compare, *ir.Const, *ir.Bind, *ir.Copy, *ir.Make, *ir.Match,
		*ir.Not, *ir.NoMatch, *ir.Proj, *ir.Render, *ir.Try, *ir.Todo, *ir.Unreachable, *ir.FuncValue, *ir.Call:
		return true
	case *ir.Ref:
		return n.Kind() != ir.RefScope
	}
	return false
}

// inlineCall compiles n, a call planInlines picked, into a view of its
// callee g and emits opInline, or reports false when the view did not
// compile, for the call to open an activation instead.
func (cp *compiler) inlineCall(n *ir.Call, g *ir.Func, args []uint32) bool {
	v := cp.compileIn(n, g, args, false)
	if v == nil {
		return false
	}
	v.place(cp.c, len(cp.c.code), n.Dst(), cp.level()+1)
	if ms := cp.moves[n]; len(ms) > 0 {
		v.hands = movesOf(ms)
	}
	cp.c.views = append(cp.c.views, v)
	cp.c.inlined = append(append(cp.c.inlined, g), v.inlined...)
	cp.emit(cp.op(opInline, uint32(len(cp.c.views)-1)))
	return true
}

// switchCall compiles n, a tail call in the view cp compiles, into a view
// of its callee g that replaces the view, as the tail call replaces the
// callee's activation, and emits opSwitch. The view answers the call cp's
// view stands for, so its place is cp's view's (code.place), set when
// that is. It reports false when g does not compile in, for n to be
// opTailV.
func (cp *compiler) switchCall(n *ir.Call, g *ir.Func, args []uint32) bool {
	if !cp.compilesIn(g) {
		return false
	}
	v := cp.compileIn(n, g, args, true)
	if v == nil {
		return false
	}
	v.switched = true
	if ms := cp.moves[n]; len(ms) > 0 {
		v.hands = movesOf(ms)
	}
	cp.c.views = append(cp.c.views, v)
	cp.c.inlined = append(append(cp.c.inlined, g), v.inlined...)
	cp.emit(cp.op(opSwitch, uint32(len(cp.c.views)-1)))
	return true
}

// compileIn compiles g, the callee of n in the code cp compiles, into a
// view: one cp's code returns from, or with replaces, one replacing the
// view cp compiles. It is nil when the view does not compile.
func (cp *compiler) compileIn(n *ir.Call, g *ir.Func, args []uint32, replaces bool) *code {
	site := &inlineSite{from: cp, parent: cp, call: n, args: make([]loc, len(args)), packs: make([]packedTemp, len(args)),
		base: [4]int{bankW: cp.c.nW, bankS: cp.c.nS, bankR: cp.c.nR}, level: cp.level() + 1}
	if replaces {
		site.parent, site.call, site.level = cp.site.parent, cp.site.call, cp.level()
	}
	taken := cp.m.takenApart(g)
	for i, a := range args {
		site.args[i] = loc(a)
		if cp.c.packed == nil {
			continue
		}
		if site.packs[i] = cp.c.packed[n.Arg(i)]; site.packs[i].kind != packNone {
			if _, apart := taken[i]; !apart || site.packs[i].kind != packMaybe {
				// Held in parts the callee does not take apart: the call
				// passes the value, rebuilt (readPacked).
				return nil
			}
		}
	}
	return cp.m.compileView(g, site)
}

// place records where v returns to: the code parent, after its call at pc
// into dst, at level. A view a tail call in v switches to returns where v
// does.
func (v *code) place(parent *code, pc int, dst ir.Temp, level int) {
	v.parent, v.callPC, v.dst, v.level = parent, pc, dst, level
	for _, x := range v.views {
		if x.switched {
			x.place(parent, pc, dst, level)
		}
	}
}

// compileView compiles g into a view at site, or answers nil when compiling
// it panicked: the call then opens an activation, whose compile of g
// raises the panic where a call of g would.
func (m *Machine) compileView(g *ir.Func, site *inlineSite) (c *code) {
	defer func() {
		if recover() != nil {
			c = nil
		}
	}()
	if CompileHook != nil {
		CompileHook(g)
	}
	if c = m.compileBody(g, site); c.refused {
		return nil
	}
	return c
}

// answers are the temporaries of a view that only its returns read, when
// they live in the bank of the call's destination and the caller keeps the
// destination in one register: each is given the destination's register,
// so the view computes its answer where the caller reads it. Nothing the
// view reads is in that register: it is no operand of the call
// (assignRegisters shares an operand's register with no destination).
func (cp *compiler) answers() []ir.Temp {
	site := cp.site
	if site == nil || site.call.Dst() == ir.NoTemp {
		return nil
	}
	parent, dst, f := site.parent, site.call.Dst(), cp.c.fn
	d := parent.loc(dst)
	if d.bank() == bankNone || (parent.c.packed != nil && parent.c.packed[dst].kind != packNone) {
		return nil
	}
	returned := map[ir.Temp]bool{}
	for _, b := range f.Blocks() {
		if r, isRet := b.Term().(*ir.Return); isRet && r.HasVal() && int(r.Val()) < len(cp.c.locs) {
			returned[r.Val()] = classOf(f.TempType(r.Val())) == d.bank()
		}
	}
	for _, p := range f.Params() {
		delete(returned, p.Temp)
	}
	var buf []ir.Temp
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			for _, t := range in.AppendUses(buf[:0]) {
				delete(returned, t)
			}
		}
		if _, isRet := b.Term().(*ir.Return); !isRet && b.Term() != nil {
			for _, t := range b.Term().AppendUses(buf[:0]) {
				delete(returned, t)
			}
		}
	}
	var out []ir.Temp
	for t, fits := range returned {
		if fits {
			out = append(out, t)
		}
	}
	return out
}

// paramGroup is how a callee holds a Maybe or a Result parameter that it
// only takes apart: the temporaries that are the parameter (its reads, and
// the copies valueGroups joins to them), and the types its payload reads
// land in.
type paramGroup struct {
	members []ir.Temp
	reads   []*ir.ValType
}

// takenApart is, by index, each parameter of g whose every use is a
// variant test, a payload read or `?` (fuseMaybes' takes, over the reads
// of the parameter as valueGroups groups them), memoized per machine. A
// caller compiling g in fuses the operand it passes there.
func (m *Machine) takenApart(g *ir.Func) map[int]paramGroup {
	if v, ok := m.codes.taken.Load(g); ok {
		return v.(map[int]paramGroup)
	}
	out := takenApartOf(m, g)
	m.codes.taken.Store(g, out)
	return out
}

func takenApartOf(m *Machine, g *ir.Func) map[int]paramGroup {
	index := map[*ir.Symbol]int{}
	for i, p := range g.Params() {
		if p.Sym != nil && carriesParts(g.TempType(p.Temp)) {
			index[p.Sym] = i
		}
	}
	if len(index) == 0 {
		return nil
	}
	cp := &compiler{m: m, c: &code{fn: g}}
	cp.assignRegisters()
	reads := func(in ir.Instr) (int, bool) {
		r, isRef := in.(*ir.Ref)
		if !isRef || r.Kind() != ir.RefLocal {
			return 0, false
		}
		i, isParam := index[r.Sym()]
		return i, isParam
	}
	root, groups := cp.valueGroups(func(in ir.Instr) bool {
		_, isRead := reads(in)
		return isRead
	}, cp.fuseTake, false, nil)
	stands := map[ir.Temp]bool{}
	for _, gr := range groups {
		stands[gr] = true
	}
	refused := map[int]bool{}
	of := map[ir.Temp]int{}
	for _, p := range g.Params() {
		if i, ok := index[p.Sym]; ok && cp.uses[p.Temp] != 0 {
			refused[i] = true
		}
	}
	for _, b := range g.Blocks() {
		for _, in := range b.Instrs() {
			if i, isRead := reads(in); isRead {
				if !stands[in.Dst()] {
					refused[i] = true
				}
				of[in.Dst()] = i
			}
		}
	}
	out := map[int]paramGroup{}
	for t, gr := range root {
		if i := of[gr]; !refused[i] {
			pg := out[i]
			pg.members = append(pg.members, t)
			out[i] = pg
		}
	}
	for _, b := range g.Blocks() {
		for _, in := range b.Instrs() {
			if p, isProj := in.(*ir.Proj); isProj && fusedProj(p) {
				if gr, isMember := root[p.Subject()]; isMember && !refused[of[gr]] {
					pg := out[of[gr]]
					pg.reads = append(pg.reads, cp.typeOf(p.Dst()))
					out[of[gr]] = pg
				}
			}
		}
	}
	return out
}

// inheritFused holds each parameter of a view that its caller keeps fused
// as the caller holds the operand: the parameter's reads are the caller's
// tag and payload registers (takenApart's members), which the caller's
// crossing or call wrote.
func (cp *compiler) inheritFused() {
	site := cp.site
	if site == nil {
		return
	}
	var taken map[int]paramGroup
	for i, pk := range site.packs {
		if pk.kind != packMaybe {
			continue
		}
		if taken == nil {
			taken = cp.m.takenApart(cp.c.fn)
		}
		if cp.fused == nil {
			cp.fused = map[ir.Temp]uint32{}
		}
		members := append([]ir.Temp{cp.c.fn.Params()[i].Temp}, taken[i].members...)
		for _, t := range members {
			cp.fused[t] = pk.tag
			cp.pack(t, pk)
		}
	}
}

// leave compiles a view's return whose value and call destination are
// registers of one bank, neither kept in parts, to a move into the
// destination and opLeave, and reports whether it did. Any other return
// is the function's own, whose answer the loop delivers into the
// destination as a call's (frame.deliver).
func (cp *compiler) leave(t *ir.Return) bool {
	site := cp.site
	if site == nil || t.Ctl() != ir.CtlNone {
		return false
	}
	parent, dst := site.parent, site.call.Dst()
	d := parent.loc(dst)
	if dst != ir.NoTemp && parent.c.packed != nil && parent.c.packed[dst].kind != packNone {
		return false
	}
	if !t.HasVal() {
		if d.bank() != bankNone {
			return false
		}
		cp.emit(cp.op(opLeave, 0))
		return true
	}
	v := t.Val()
	src := cp.loc(v)
	if src.bank() != d.bank() || (cp.c.packed != nil && cp.c.packed[v].kind != packNone) ||
		cp.split[v] != nil || cp.retWrap[v] != ir.NoTemp {
		return false
	}
	if src != d {
		switch d.bank() {
		case bankW:
			cp.emit(cp.op(opMovW, uint32(d.reg())), uint32(src.reg()))
		case bankS:
			cp.emit(cp.op(opMovS, uint32(d.reg())), uint32(src.reg()))
		case bankR:
			cp.emit(cp.op(opMovR, uint32(d.reg())), uint32(src.reg()))
		}
		if d.bank() != bankNone {
			cp.at(t.Pos(), false, -1)
		}
	}
	cp.emit(cp.op(opLeave, 0))
	return true
}

// enterView makes v, a view of fr's code, the activation's code and its
// callee the activation's function, with the parameters its call hands it
// as its own (opInline).
func (fr *frame) enterView(v *code) *code {
	fr.depth++
	fr.c, fr.fn = v, v.fn
	fr.viewOwned[v.level-1], fr.owned = fr.owned, 0
	if v.hands != nil {
		fr.owned = handedOver(fr.viewOwned[v.level-1], v.hands)
	}
	return v
}

// switchView makes v, a view a tail call in fr's view switches to, the
// activation's code in its place, at the same depth, with the parameters
// the tail call hands it as its own (opSwitch).
func (fr *frame) switchView(v *code) *code {
	owned := uint64(0)
	if v.hands != nil {
		owned = handedOver(fr.owned, v.hands)
	}
	fr.c, fr.fn, fr.owned = v, v.fn, owned
	return v
}

// leaveView makes the code fr's view was compiled into the activation's
// again, delivering the answer into the call's destination when deliver
// says the view left it as the stack's last return, and answers that code
// and the call's pc.
func (fr *frame) leaveView(deliver bool) (*code, int) {
	v := fr.c
	c := v.parent
	fr.c, fr.fn, fr.owned = c, c.fn, fr.viewOwned[v.level-1]
	fr.depth--
	if deliver {
		fr.deliver(v.dst)
	}
	return c, v.callPC
}

// leaveViews carries on an activation whose loop returned while a view ran
// (inline.go): a return that left its answer as the stack's last return,
// a `?` that left the value it propagates there, or an error. As a
// callee's exit is its caller's, the view's caller then delivers the
// answer and runs on, or meets the error at the call's site
// (Machine.fault), which places it, takes the call's fault edge, or leaves
// with it, from views around it as from the function. A tail transfer the
// code it carries on makes into a function that needs exit hooks runs
// that function with them (Machine.execGuarded), as Machine.runBody does.
func (m *Machine) leaveViews(fr *frame, err error) error {
	for fr.c.parent != nil {
		_, at := fr.leaveView(err == nil)
		pc := at + 1
		if err == nil && fr.bad != nil {
			err, fr.bad = fr.bad, nil
		}
		if err != nil {
			if pc, err = m.fault(fr, at, err); pc < 0 {
				if fr.c.parent == nil {
					return m.finish(fr, err)
				}
				continue
			}
		}
		if err = m.loop(fr, pc); err == errNeedsGuard {
			return m.execGuarded(fr)
		}
	}
	return err
}

// settleWindow sizes every view of c's tree to the window of the whole
// tree: the most registers any of them reaches, in each bank. A view runs
// over the activation's window, so its code's register counts are the
// window's, as the loop slices it.
func (c *code) settleWindow() {
	nW, nS, nR := 0, 0, 0
	var reach func(v *code)
	reach = func(v *code) {
		nW, nS, nR = max(nW, v.nW), max(nS, v.nS), max(nR, v.nR)
		for _, x := range v.views {
			reach(x)
		}
	}
	reach(c)
	var set func(v *code)
	set = func(v *code) {
		v.nW, v.nS, v.nR = nW, nS, nR
		for _, x := range v.views {
			set(x)
		}
	}
	set(c)
}

// inlines reports whether a view of f was compiled into c.
func (c *code) inlines(f *ir.Func) bool {
	for _, g := range c.inlined {
		if g == f {
			return true
		}
	}
	return false
}
