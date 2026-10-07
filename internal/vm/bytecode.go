package vm

// The bytecode: what one `ir.Func` compiles to before this machine runs it.
//
// The graph is compiled once per function, on the function's first call, into
// a flat `[]uint32` whose registers live in three banks of one register stack
// per goroutine (exec.go). The compiler does four things the graph walker did
// on every execution:
//
//  1. It assigns every temporary a register in the bank its stored type's
//     `ValType.Class()` names: `word` (Int, Float, Bool, Byte, a distinct over
//     one), `str` (String, Bytes, a distinct over one) or `ref` (everything
//     else, and every temporary with no stored type). A `none` temporary (Unit,
//     a zero-sized marker) gets no register. Parameters take the first
//     registers of their banks in declaration order, so a caller writes its
//     arguments straight into the callee's window.
//  2. It linearizes the blocks into one instruction array with branch targets
//     as code offsets.
//  3. It resolves every direct, non-crossing callee to an entry in the
//     function's callee table, compiled on first use.
//  4. It records a site per instruction: its position, whether it can fault,
//     and the offset of its block's fault handler. Faults, call-depth errors
//     and `try` propagation read positions from the site table, so a fault's
//     text and line are the instruction's own, as they were in the walker.
//
// # INSTRUCTION FORMAT
//
// Word 0 of an instruction is the opcode in the low 8 bits and an operand in
// the high 24 (usually the destination register). The opcode decides how many
// operand words follow. Register operands index the opcode's bank; `T` below
// marks an operand that is a temporary rather than a register, resolved
// through the function's register map where a value can arrive in any bank.
//
// # WHAT RUNS THROUGH A HANDLER
//
// The scalar core — constants, moves, Int and Float arithmetic, comparisons,
// negation, concatenation, literal and variant tests, payload and field
// reads, variant construction, direct calls and tail calls, and every
// terminator — has typed opcodes. Every other instruction compiles to
// `opIR`, which runs the instruction's handler (`Machine.irInstr`) over
// operands boxed from their banks into rt values and unboxed back into
// the destination's bank. Those handlers are the machine's implementations of
// iteration, assertions, construction of structs, records, tuples and
// collections, projections off them, `try`, defers, once cells, app fields,
// Context, Debug and Display rendering, dispatched, indirect and host calls.
// The boxing seam is here so an instruction kind can move from `opIR` to a
// typed opcode one kind at a time.

import (
	"fmt"
	"github.com/nomi-language/nomi/hostadapt"
	"math"
	"strings"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

type opcode uint8

const (
	opNop    opcode = iota
	opLoadW         // dst | kw           word constant
	opLoadS         // dst | ks           string constant
	opLoadR         // dst | kr           shared value: a payload-free variant
	opOnce          // dstT | cell | idx  a forced once's value, else the handler
	opMovW          // dst | src
	opMovS          // dst | src
	opMovR          // dst | src
	opXMov          // dstT | srcT        a move across banks, through a box
	opAddI          // dst | a | b        checked
	opSubI          // dst | a | b
	opMulI          // dst | a | b
	opDivI          // dst | a | b
	opRemI          // dst | a | b
	opNegI          // dst | a
	opAddIW         // dst | a | b        wrapping
	opSubIW         // dst | a | b
	opMulIW         // dst | a | b
	opNegIW         // dst | a
	opAddF          // dst | a | b
	opSubF          // dst | a | b
	opMulF          // dst | a | b
	opDivF          // dst | a | b
	opNegF          // dst | a
	opRemF          // dst | a | b        always faults
	opCmpI          // dst | a | b | cond
	opCmpF          // dst | a | b | cond
	opEqS           // dst | a | b | negate
	opEqW           // dst | a | b | negate   Bool, Byte and Int identity
	opNot           // dst | a
	opConcat        // dst | n | parts...
	opMatchV        // dst | subj(ref) | ks(enum) | ks(variant) | idx
	opProjP         // dstT | subj(ref) | idx
	opProjF         // dstT | subj(ref) | ks(field) | idx | cache
	opMake          // dst(ref) | desc | tag | n | (field, srcT)...
	opCall          // dstT | callee | n | args...
	opTail          // dstT | callee | n | args...
	opJmp           // _ | pc
	opBr            // cond(word) | tpc | fpc
	opBrX           // condT | tpc | fpc | block
	opRet           // srcT
	opRetU          // _
	opRetCtl        // idx                a control return, boxed
	opIR            // idx                an instruction run by its handler
	opHost          // idx                a crossing into a bound host adapter
	opFail          // idx                a compile-time refusal, raised on arrival
)

// Comparison conditions for opCmpI and opCmpF.
const (
	condEq uint32 = iota
	condNe
	condLt
	condLe
	condGt
	condGe
)

// bank is one of the register stack's three banks, or none.
type bank uint32

const (
	bankNone bank = iota
	bankW
	bankS
	bankR
)

// loc is where one temporary lives: its bank in the top two bits and its
// register in the rest. An argument operand of a call is a loc too.
type loc uint32

const locRegMask = 1<<30 - 1

func mkLoc(b bank, reg int) loc { return loc(uint32(b)<<30 | uint32(reg)) }
func (l loc) bank() bank        { return bank(l >> 30) }
func (l loc) reg() int          { return int(l & locRegMask) }

// code is one compiled function.
type code struct {
	fn   *ir.Func
	code []uint32
	// nW, nS and nR are the register counts per bank.
	nW, nS, nR int
	// params are the parameters' locations in declaration order.
	params []loc
	// locs and types are each temporary's location and stored type, indexed
	// by the Temp. A nil type is an untyped temporary, which lives in the ref
	// bank as an rt value.
	locs  []loc
	types []*ir.ValType
	// Constant pools.
	kw []uint64
	ks []string
	kr []any
	// descs are the descriptors opMake builds with; pcache is one field
	// cache per opProjF instruction.
	descs  []*rt.TypeDesc
	pcache []atomic.Pointer[projHit]
	// callees are the direct callees, resolved at compile time and compiled
	// on first call.
	callees []*fnSlot
	// irs are the instructions opIR runs; fails the errors opFail raises.
	irs []ir.Instr
	// hostCalls are the crossings opHost makes, each resolved to its bound
	// adapter at compile time.
	hostCalls []hostCall
	ctls      []*ir.Return
	onces     []*onceCell
	fails     []error
	// sites is the line and fault table, one entry per instruction in code
	// order. See site.
	sites []site
	// tails is the set of calls that transfer instead of calling.
	tails map[*ir.Call]bool
	// guard says an activation needs its exit hooks: the function registers
	// deferred calls or rebinds the Context, whose deadlines must end.
	guard bool
}

// site is one instruction's entry in the line and fault table.
type site struct {
	pc  int
	pos ir.Pos
	// faults says the instruction can fault at its own position, so an error
	// out of it takes handler, or leaves the function marked as a Fault.
	faults bool
	// handler is the block the instruction's block faults to, or -1.
	handler int
}

// siteAt is the entry of the instruction starting at pc.
func (c *code) siteAt(pc int) *site {
	lo, hi := 0, len(c.sites)
	for lo < hi {
		mid := (lo + hi) / 2
		if c.sites[mid].pc < pc {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(c.sites) && c.sites[lo].pc == pc {
		return &c.sites[lo]
	}
	return nil
}

// classOf is the bank a value of type ty lives in. An untyped temporary is a
// boxed value in the ref bank.
//
// A distinct over a scalar lives in that scalar's bank, and a zero-sized
// marker has no storage: the value's identity is its stored type's symbol,
// which names the declaring module (`codepoints.Codepoint`), and boxing it
// builds a record over that type's descriptor (values.go's boxWord).
func classOf(ty *ir.ValType) bank {
	if ty == nil {
		return bankR
	}
	switch ty.Class() {
	case ir.ClassNone:
		return bankNone
	case ir.ClassWord:
		return bankW
	case ir.ClassStr:
		return bankS
	}
	return bankR
}

// paramBanks is the bank of each of f's parameters, which is what a caller
// needs to write its arguments into a window it has not compiled.
func paramBanks(f *ir.Func) []bank {
	ps := f.Params()
	out := make([]bank, len(ps))
	for i, p := range ps {
		out[i] = classOf(f.TempType(p.Temp))
	}
	return out
}

// compiler is the state of compiling one function.
type compiler struct {
	m      *Machine
	c      *code
	blocks map[ir.BlockID]int
	// patches are code offsets holding a BlockID to replace with its pc.
	patches []int
	ksIndex map[string]uint32
	kwIndex map[uint64]uint32
	calleeI map[*ir.Func]uint32
	// aliased are the reads of a parameter whose destination shares the
	// parameter's register, and the copies whose source was computed
	// straight into the destination's register, so they compile to nothing.
	aliased map[ir.Instr]bool
}

// compile turns f into bytecode. It cannot fail: an instruction the typed
// opcodes do not cover runs through its handler, and a graph fault the
// walker reported at run time (a missing block, a missing terminator) is an
// opFail raised when control arrives there, with the walker's text. A panic
// while compiling is a compiler bug; the body raises it as a *CompilePanic
// (compilepanic.go).
func (m *Machine) compile(f *ir.Func) (c *code) {
	defer recoverCompile(f, &c)
	if CompileHook != nil {
		CompileHook(f)
	}
	c = &code{fn: f}
	cp := &compiler{m: m, c: c, blocks: map[ir.BlockID]int{},
		ksIndex: map[string]uint32{}, kwIndex: map[uint64]uint32{}, calleeI: map[*ir.Func]uint32{}}
	cp.assignRegisters()
	c.tails = ir.TailTransfers(f)
	blocks := f.Blocks()
	if len(blocks) == 0 {
		cp.fail(f.Pos(), fmt.Errorf("vm: %s has no entry block", f.Name()))
		return c
	}
	for _, b := range blocks {
		cp.blocks[b.ID()] = len(c.code)
		handler := -1
		if h, has := b.Fault(); has {
			handler = -2 - int(h) // patched below
		}
		for _, in := range b.Instrs() {
			cp.instr(in, handler)
		}
		cp.term(b)
	}
	for _, at := range cp.patches {
		id := ir.BlockID(c.code[at])
		pc, ok := cp.blocks[id]
		if !ok {
			// A branch to a block this function does not hold. The walker
			// reported it on arrival; the target becomes a refusal.
			pc = len(c.code)
			cp.fail(f.Pos(), fmt.Errorf("vm: %s: a terminator names %s, which is not a block "+
				"of this function", f.Name(), id))
		}
		c.code[at] = uint32(pc)
	}
	// A jump to a return is the return: the shared exit block every arm of a
	// value-producing `if` or `case` jumps to is one instruction.
	for _, st := range c.sites {
		if opcode(c.code[st.pc]&0xff) != opJmp {
			continue
		}
		target := c.code[c.code[st.pc+1]]
		switch opcode(target & 0xff) {
		case opRet, opRetU:
			c.code[st.pc], c.code[st.pc+1] = target, uint32(opNop)
		}
	}
	for i := range c.sites {
		if h := c.sites[i].handler; h <= -2 {
			id := ir.BlockID(-2 - h)
			pc, ok := cp.blocks[id]
			if !ok {
				pc = len(c.code)
				cp.fail(f.Pos(), fmt.Errorf("vm: %s: the fault edge names %s, which is not a block "+
					"of this function", f.Name(), id))
			}
			c.sites[i].handler = pc
		}
	}
	return c
}

// assignRegisters gives each temporary its bank and register, parameters
// first. Two kinds of temporary share another's register instead of taking
// their own, and the instruction that would move between them is dropped:
//
//   - a temporary whose one definition reads a parameter shares the
//     parameter's register, because a parameter is written only at entry, so
//     the two hold the same value for the whole activation;
//   - a temporary defined once and used once, by a Copy or Bind that follows
//     its definition directly, is computed straight into the copy's
//     destination, when the defining instruction does not read that
//     destination.
func (cp *compiler) assignRegisters() {
	c, f := cp.c, cp.c.fn
	n := f.NumTemps() + 1
	writes := make([]int, n)
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if d := in.Dst(); d != ir.NoTemp && int(d) < n {
				writes[d]++
			}
			if s, isSlot := in.(*ir.Slot); isSlot && s.Slot() != ir.NoTemp && int(s.Slot()) < n {
				writes[s.Slot()] += 2
			}
		}
	}
	c.locs = make([]loc, n)
	c.types = make([]*ir.ValType, n)
	assigned := make([]bool, n)
	counts := [4]int{}
	give := func(t ir.Temp) loc {
		ty := f.TempType(t)
		b := classOf(ty)
		c.types[t] = ty
		reg := 0
		if b != bankNone {
			reg = counts[b]
			counts[b]++
		}
		c.locs[t] = mkLoc(b, reg)
		assigned[t] = true
		return c.locs[t]
	}
	for _, p := range f.Params() {
		if p.Temp == ir.NoTemp || int(p.Temp) >= n {
			continue
		}
		if assigned[p.Temp] {
			c.params = append(c.params, c.locs[p.Temp])
			continue
		}
		c.params = append(c.params, give(p.Temp))
	}
	cp.aliased = map[ir.Instr]bool{}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			r, isRef := in.(*ir.Ref)
			if !isRef || r.Kind() != ir.RefLocal {
				continue
			}
			p, isParam := paramTemp(f, r.Sym())
			d := r.Dst()
			if !isParam || d == ir.NoTemp || int(d) >= n || int(p) >= n || assigned[d] ||
				writes[d] != 1 || writes[p] != 0 {
				continue
			}
			ty := f.TempType(d)
			if classOf(ty) != c.locs[p].bank() || c.locs[p].bank() == bankNone {
				continue
			}
			c.locs[d], c.types[d], assigned[d] = c.locs[p], ty, true
			cp.aliased[r] = true
		}
	}
	uses := make([]int, n)
	var buf []ir.Temp
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			buf = in.AppendUses(buf[:0])
			for _, t := range buf {
				if int(t) < n {
					uses[t]++
				}
			}
		}
		if b.Term() != nil {
			buf = b.Term().AppendUses(buf[:0])
			for _, t := range buf {
				if int(t) < n {
					uses[t]++
				}
			}
		}
	}
	for _, b := range f.Blocks() {
		instrs := b.Instrs()
		for i := 1; i < len(instrs); i++ {
			var src, dst ir.Temp
			switch mv := instrs[i].(type) {
			case *ir.Copy:
				src, dst = mv.Src(), mv.Dst()
			case *ir.Bind:
				src, dst = mv.Src(), mv.Dst()
			default:
				continue
			}
			def := instrs[i-1]
			if src == ir.NoTemp || dst == ir.NoTemp || int(src) >= n || int(dst) >= n || src == dst ||
				def.Dst() != src || assigned[src] || writes[src] != 1 || uses[src] != 1 {
				continue
			}
			if _, isRef := def.(*ir.Ref); isRef && cp.aliased[def] {
				continue
			}
			buf = def.AppendUses(buf[:0])
			readsDst := false
			for _, t := range buf {
				if t == dst {
					readsDst = true
				}
			}
			ty := f.TempType(src)
			if readsDst || classOf(ty) != classOf(f.TempType(dst)) || classOf(ty) == bankNone {
				continue
			}
			if !assigned[dst] {
				give(dst)
			}
			c.locs[src], c.types[src], assigned[src] = c.locs[dst], ty, true
			cp.aliased[instrs[i]] = true
		}
	}
	for t := 1; t < n; t++ {
		if !assigned[t] {
			give(ir.Temp(t))
		}
	}
	c.nW, c.nS, c.nR = counts[bankW], counts[bankS], counts[bankR]
}

func (cp *compiler) emit(words ...uint32) { cp.c.code = append(cp.c.code, words...) }

func (cp *compiler) op(o opcode, a uint32) uint32 { return uint32(o) | a<<8 }

// at records the site of the instruction about to be emitted.
func (cp *compiler) at(pos ir.Pos, faults bool, handler int) {
	cp.c.sites = append(cp.c.sites, site{pc: len(cp.c.code), pos: pos, faults: faults, handler: handler})
}

func (cp *compiler) fail(pos ir.Pos, err error) {
	cp.at(pos, false, -1)
	cp.c.fails = append(cp.c.fails, err)
	cp.emit(cp.op(opFail, uint32(len(cp.c.fails)-1)))
}

// handlerIndex records in as the handler a typed opcode falls back to when
// its operand is not the shape it reads. The handler then reports the
// operand in the words the graph walker did.
func (cp *compiler) handlerIndex(in ir.Instr) uint32 {
	cp.c.irs = append(cp.c.irs, in)
	return uint32(len(cp.c.irs) - 1)
}

func (cp *compiler) viaHandler(in ir.Instr) {
	cp.c.irs = append(cp.c.irs, in)
	cp.emit(cp.op(opIR, uint32(len(cp.c.irs)-1)))
}

func (cp *compiler) str(s string) uint32 {
	if i, ok := cp.ksIndex[s]; ok {
		return i
	}
	i := uint32(len(cp.c.ks))
	cp.c.ks = append(cp.c.ks, s)
	cp.ksIndex[s] = i
	return i
}

func (cp *compiler) word(w uint64) uint32 {
	if i, ok := cp.kwIndex[w]; ok {
		return i
	}
	i := uint32(len(cp.c.kw))
	cp.c.kw = append(cp.c.kw, w)
	cp.kwIndex[w] = i
	return i
}

func (cp *compiler) loc(t ir.Temp) loc {
	if t == ir.NoTemp || int(t) >= len(cp.c.locs) {
		return mkLoc(bankNone, 0)
	}
	return cp.c.locs[t]
}

// kind is t's stored type's kind, or 0 for an untyped temporary.
func (cp *compiler) kind(t ir.Temp) ir.ValKind {
	if t == ir.NoTemp || int(t) >= len(cp.c.types) || cp.c.types[t] == nil {
		return 0
	}
	return cp.c.types[t].Kind()
}

// inBank reports whether every temporary lives in bank b.
func (cp *compiler) inBank(b bank, ts ...ir.Temp) bool {
	for _, t := range ts {
		if t == ir.NoTemp || int(t) >= len(cp.c.locs) || cp.c.locs[t].bank() != b {
			return false
		}
	}
	return true
}

// move copies src into dst, through a box when their banks differ.
func (cp *compiler) move(dst, src ir.Temp) {
	d, s := cp.loc(dst), cp.loc(src)
	switch {
	case d.bank() == bankNone:
		// Nothing to store: the destination is Unit or nothing.
		cp.emit(cp.op(opNop, 0))
	case d.bank() == s.bank() && d.bank() == bankW:
		cp.emit(cp.op(opMovW, uint32(d.reg())), uint32(s.reg()))
	case d.bank() == s.bank() && d.bank() == bankS:
		cp.emit(cp.op(opMovS, uint32(d.reg())), uint32(s.reg()))
	case d.bank() == s.bank() && d.bank() == bankR:
		cp.emit(cp.op(opMovR, uint32(d.reg())), uint32(s.reg()))
	default:
		cp.emit(cp.op(opXMov, uint32(dst)), uint32(src))
	}
}

// instr compiles one instruction.
func (cp *compiler) instr(in ir.Instr, handler int) {
	if _, isSlot := in.(*ir.Slot); isSlot {
		// A declaration: the window already holds the storage.
		return
	}
	switch n := in.(type) {
	case *ir.Defer:
		cp.c.guard = true
	case *ir.Store:
		if n.Kind() == ir.StoreContext {
			cp.c.guard = true
		}
	}
	cp.at(in.Pos(), ir.InstrFaults(in).Any(), handler)
	if cp.typed(in) {
		return
	}
	cp.viaHandler(in)
}

// typed emits a typed opcode for in and reports whether it did.
func (cp *compiler) typed(in ir.Instr) bool {
	switch n := in.(type) {
	case *ir.Const:
		d := cp.loc(n.Dst())
		switch {
		case d.bank() == bankNone && n.Kind() == ir.ConstUnit:
			// No storage; reading the temporary answers Unit.
			cp.emit(cp.op(opNop, 0))
			return true
		case d.bank() == bankW && n.Kind() == ir.ConstInt && cp.kind(n.Dst()) == ir.KindInt:
			cp.emit(cp.op(opLoadW, uint32(d.reg())), cp.word(uint64(n.Int())))
			return true
		case d.bank() == bankW && n.Kind() == ir.ConstFloat && cp.kind(n.Dst()) == ir.KindFloat:
			cp.emit(cp.op(opLoadW, uint32(d.reg())), cp.word(math.Float64bits(n.Float())))
			return true
		case d.bank() == bankW && n.Kind() == ir.ConstBool && cp.kind(n.Dst()) == ir.KindBool:
			var w uint64
			if n.Bool() {
				w = 1
			}
			cp.emit(cp.op(opLoadW, uint32(d.reg())), cp.word(w))
			return true
		case d.bank() == bankS && n.Kind() == ir.ConstString && cp.kind(n.Dst()) == ir.KindString:
			cp.emit(cp.op(opLoadS, uint32(d.reg())), cp.str(n.Text()))
			return true
		}
		return false

	case *ir.Copy:
		if cp.aliased[n] {
			cp.c.sites = cp.c.sites[:len(cp.c.sites)-1]
			return true
		}
		cp.move(n.Dst(), n.Src())
		return true
	case *ir.Bind:
		if cp.aliased[n] {
			cp.c.sites = cp.c.sites[:len(cp.c.sites)-1]
			return true
		}
		cp.move(n.Dst(), n.Src())
		return true
	case *ir.Ref:
		if n.Kind() == ir.RefOnce {
			cell := cp.m.onces[n.Sym()]
			if cell == nil {
				return false
			}
			cp.c.onces = append(cp.c.onces, cell)
			cp.c.irs = append(cp.c.irs, n)
			cp.emit(cp.op(opOnce, uint32(n.Dst())), uint32(len(cp.c.onces)-1), uint32(len(cp.c.irs)-1))
			return true
		}
		if n.Kind() != ir.RefLocal {
			return false
		}
		if cp.aliased[n] {
			cp.c.sites = cp.c.sites[:len(cp.c.sites)-1]
			return true
		}
		t, isParam := paramTemp(cp.c.fn, n.Sym())
		if !isParam {
			// The handler reports the undeclared local.
			return false
		}
		cp.move(n.Dst(), t)
		return true

	case *ir.Arith:
		return cp.arith(n)

	case *ir.Compare:
		return cp.compare(n)

	case *ir.Not:
		if !cp.inBank(bankW, n.Dst(), n.Val()) || cp.kind(n.Val()) != ir.KindBool {
			return false
		}
		cp.emit(cp.op(opNot, uint32(cp.loc(n.Dst()).reg())), uint32(cp.loc(n.Val()).reg()))
		return true

	case *ir.Concat:
		if !cp.inBank(bankS, n.Dst()) || cp.kind(n.Dst()) != ir.KindString {
			return false
		}
		parts := make([]uint32, n.NumParts())
		for i := range parts {
			if !cp.inBank(bankS, n.Part(i)) || cp.kind(n.Part(i)) != ir.KindString {
				return false
			}
			parts[i] = uint32(cp.loc(n.Part(i)).reg())
		}
		cp.emit(cp.op(opConcat, uint32(cp.loc(n.Dst()).reg())), uint32(len(parts)))
		cp.emit(parts...)
		return true

	case *ir.Match:
		return cp.match(n)

	case *ir.Proj:
		if n.Kind() == ir.ProjInner {
			// A distinct in a scalar register unwraps to the same word or string.
			d, sl := cp.loc(n.Dst()), cp.loc(n.Subject())
			if (d.bank() == bankW || d.bank() == bankS) && d.bank() == sl.bank() {
				cp.move(n.Dst(), n.Subject())
				return true
			}
			return false
		}
		if !cp.inBank(bankR, n.Subject()) {
			return false
		}
		switch {
		case n.Kind() == ir.ProjPayload && n.PayloadEmbeds() == nil && n.PayloadField() == "" && n.Index() == 0:
			cp.emit(cp.op(opProjP, uint32(n.Dst())), uint32(cp.loc(n.Subject()).reg()), cp.handlerIndex(n))
			return true
		case n.Kind() == ir.ProjField || n.Kind() == ir.ProjRecordField:
			cp.c.pcache = append(cp.c.pcache, atomic.Pointer[projHit]{})
			cp.emit(cp.op(opProjF, uint32(n.Dst())), uint32(cp.loc(n.Subject()).reg()), cp.str(n.Name()),
				cp.handlerIndex(n), uint32(len(cp.c.pcache)-1))
			return true
		}
		return false

	case *ir.Make:
		return cp.make(n)

	case *ir.Call:
		return cp.call(n)
	}
	return false
}

func (cp *compiler) arith(n *ir.Arith) bool {
	operands := []ir.Temp{n.Dst(), n.Lhs()}
	if n.Op() != ir.OpNeg {
		operands = append(operands, n.Rhs())
	}
	if !cp.inBank(bankW, operands...) {
		return false
	}
	for _, t := range operands {
		want := ir.KindInt
		if n.Domain() == ir.DomainFloat {
			want = ir.KindFloat
		}
		if cp.kind(t) != want {
			return false
		}
	}
	var o opcode
	wraps := n.Overflow() == ir.OverflowWraps
	switch n.Domain() {
	case ir.DomainInt:
		switch n.Op() {
		case ir.OpAdd:
			o = opAddI
			if wraps {
				o = opAddIW
			}
		case ir.OpSub:
			o = opSubI
			if wraps {
				o = opSubIW
			}
		case ir.OpMul:
			o = opMulI
			if wraps {
				o = opMulIW
			}
		case ir.OpDiv:
			o = opDivI
		case ir.OpRem:
			o = opRemI
		case ir.OpNeg:
			o = opNegI
			if wraps {
				o = opNegIW
			}
		default:
			return false
		}
	case ir.DomainFloat:
		switch n.Op() {
		case ir.OpAdd:
			o = opAddF
		case ir.OpSub:
			o = opSubF
		case ir.OpMul:
			o = opMulF
		case ir.OpDiv:
			o = opDivF
		case ir.OpRem:
			o = opRemF
		case ir.OpNeg:
			o = opNegF
		default:
			return false
		}
	default:
		return false
	}
	cp.emit(cp.op(o, uint32(cp.loc(n.Dst()).reg())), uint32(cp.loc(n.Lhs()).reg()))
	if n.Op() != ir.OpNeg {
		cp.emit(uint32(cp.loc(n.Rhs()).reg()))
	}
	return true
}

func (cp *compiler) compare(n *ir.Compare) bool {
	if n.Ranked() || !cp.inBank(bankW, n.Dst()) || cp.kind(n.Dst()) != ir.KindBool {
		return false
	}
	var cond uint32
	switch n.Op() {
	case ir.OpEq:
		cond = condEq
	case ir.OpNe:
		cond = condNe
	case ir.OpLt:
		cond = condLt
	case ir.OpLe:
		cond = condLe
	case ir.OpGt:
		cond = condGt
	case ir.OpGe:
		cond = condGe
	default:
		return false
	}
	lk, rk := cp.kind(n.Lhs()), cp.kind(n.Rhs())
	var o opcode
	switch {
	case n.Shape() == ir.ValInt && lk == ir.KindInt && rk == ir.KindInt && cp.inBank(bankW, n.Lhs(), n.Rhs()):
		o = opCmpI
	case n.Shape() == ir.ValFloat && lk == ir.KindFloat && rk == ir.KindFloat && cp.inBank(bankW, n.Lhs(), n.Rhs()):
		o = opCmpF
	case n.Shape() == ir.ValString && lk == ir.KindString && rk == ir.KindString && cp.inBank(bankS, n.Lhs(), n.Rhs()) &&
		(cond == condEq || cond == condNe):
		o = opEqS
	case n.Shape() == ir.ValBool && lk == ir.KindBool && rk == ir.KindBool && cp.inBank(bankW, n.Lhs(), n.Rhs()) &&
		(cond == condEq || cond == condNe):
		o = opEqW
	case n.Shape() == ir.ValBool && lk == ir.KindBool && rk == ir.KindBool && cp.inBank(bankW, n.Lhs(), n.Rhs()):
		// A Bool's word is 0 or 1, so False orders before True as an Int.
		o = opCmpI
	default:
		return false
	}
	cp.emit(cp.op(o, uint32(cp.loc(n.Dst()).reg())), uint32(cp.loc(n.Lhs()).reg()),
		uint32(cp.loc(n.Rhs()).reg()), cond)
	return true
}

func (cp *compiler) match(n *ir.Match) bool {
	if !n.Answers() || !cp.inBank(bankW, n.Dst()) || cp.kind(n.Dst()) != ir.KindBool {
		return false
	}
	d := uint32(cp.loc(n.Dst()).reg())
	switch n.Kind() {
	case ir.MatchLit:
		sk, lk := cp.kind(n.Subject()), cp.kind(n.Arg())
		switch {
		case sk == lk && (sk == ir.KindInt || sk == ir.KindBool || sk == ir.KindByte) &&
			cp.inBank(bankW, n.Subject(), n.Arg()):
			// rt.Equal on two Ints, two Bools or two Bytes is identity.
			cp.emit(cp.op(opEqW, d), uint32(cp.loc(n.Subject()).reg()), uint32(cp.loc(n.Arg()).reg()), condEq)
			return true
		case sk == ir.KindString && lk == ir.KindString && cp.inBank(bankS, n.Subject(), n.Arg()):
			cp.emit(cp.op(opEqS, d), uint32(cp.loc(n.Subject()).reg()), uint32(cp.loc(n.Arg()).reg()), condEq)
			return true
		}
	case ir.MatchVariant:
		if n.Embeds() != nil || !cp.inBank(bankR, n.Subject()) {
			return false
		}
		cp.emit(cp.op(opMatchV, d), uint32(cp.loc(n.Subject()).reg()), cp.str(n.Sym().Name()), cp.str(n.Variant()),
			cp.handlerIndex(n))
		return true
	}
	return false
}

// call compiles a direct call to a function this program retains, when every
// argument already lives in the bank its parameter does. Every other call —
// dispatched, indirect, crossing into Go, unresolved, or one whose arguments
// change bank — runs through the handler over boxed operands.
func (cp *compiler) call(n *ir.Call) bool {
	if n.Form() == ir.CalleeDirect && n.Crosses() {
		return cp.hostCall(n)
	}
	if n.Form() != ir.CalleeDirect {
		return false
	}
	callee, err := cp.m.resolveFunc(n.Callee(), cp.c.fn.Name())
	if err != nil {
		return false
	}
	if cp.forwardedHostCall(n, callee) {
		return true
	}
	banks := paramBanks(callee)
	if len(banks) != n.NumArgs() {
		return false
	}
	args := make([]uint32, n.NumArgs())
	for i := range args {
		a := cp.loc(n.Arg(i))
		if a.bank() != banks[i] {
			return false
		}
		args[i] = uint32(a)
	}
	idx, ok := cp.calleeI[callee]
	if !ok {
		idx = uint32(len(cp.c.callees))
		cp.c.callees = append(cp.c.callees, cp.m.slotFor(callee))
		cp.calleeI[callee] = idx
	}
	o := opCall
	if cp.c.tails[n] {
		o = opTail
	}
	cp.emit(cp.op(o, uint32(n.Dst())), idx, uint32(len(args)))
	cp.emit(args...)
	return true
}

// hostCall is one opHost site: the call, its bound adapter, the operands it
// reads and the register its result goes to.
type hostCall struct {
	call *ir.Call
	fn   hostadapt.Func
	args []ir.Temp
	dst  ir.Temp
}

// hostCall compiles a crossing whose name a bound adapter answers into
// opHost, which calls the adapter with no lookup. An intrinsic, or a name no
// adapter answers, runs through the handler, which looks it up by name.
func (cp *compiler) hostCall(n *ir.Call) bool {
	name := n.Callee().Name()
	if cp.m.hosts[name] != nil {
		return false
	}
	fn, err := cp.m.adapter(name)
	if err != nil || fn == nil {
		return false
	}
	args := make([]ir.Temp, n.NumArgs())
	for i := range args {
		args[i] = n.Arg(i)
	}
	cp.c.hostCalls = append(cp.c.hostCalls, hostCall{call: n, fn: fn, args: args, dst: n.Dst()})
	cp.emit(cp.op(opHost, uint32(len(cp.c.hostCalls)-1)))
	return true
}

// forwardedHostCall compiles a call to a function whose whole body is one
// crossing over its own parameters, returned as it is, into that crossing
// at the call site: opHost reading the caller's arguments, with no
// activation for the callee. `String.split<String>`'s instance is one: its
// body is `M.split_in(separator, s)`, String's host fn with the parameters
// swapped. Without it each such call costs an activation and two more
// instructions than the crossing it forwards to. A fault the crossing raises
// is reported at the call.
//
// A tail call in a function with deferred calls keeps its transfer, since the
// transfer runs those calls before the callee and the crossing would run
// them after.
func (cp *compiler) forwardedHostCall(n *ir.Call, callee *ir.Func) bool {
	if cp.c.tails[n] && funcDefers(cp.c.fn) {
		return false
	}
	blocks := callee.Blocks()
	if len(blocks) != 1 || len(callee.Params()) != n.NumArgs() {
		return false
	}
	// param is the caller's argument each of the callee's temporaries holds:
	// a parameter's own, or a local read of a parameter.
	param := map[ir.Temp]ir.Temp{}
	bySym := map[*ir.Symbol]ir.Temp{}
	for i, p := range callee.Params() {
		param[p.Temp] = n.Arg(i)
		if p.Sym != nil {
			bySym[p.Sym] = n.Arg(i)
		}
	}
	var inner *ir.Call
	result := map[ir.Temp]bool{}
	for _, in := range blocks[0].Instrs() {
		switch x := in.(type) {
		case *ir.Slot:
		case *ir.Ref:
			arg, isParam := bySym[x.Sym()]
			if inner != nil || x.Kind() != ir.RefLocal || !isParam {
				return false
			}
			param[x.Dst()] = arg
		case *ir.Call:
			if inner != nil || x.Form() != ir.CalleeDirect || !x.Crosses() {
				return false
			}
			inner = x
			result[x.Dst()] = true
		case *ir.Copy:
			if !result[x.Src()] {
				return false
			}
			result[x.Dst()] = true
		default:
			return false
		}
	}
	ret, isRet := blocks[0].Term().(*ir.Return)
	if inner == nil || !isRet || ret.Ctl() != ir.CtlNone || !ret.HasVal() || !result[ret.Val()] {
		return false
	}
	name := inner.Callee().Name()
	if cp.m.hosts[name] != nil {
		return false
	}
	fn, err := cp.m.adapter(name)
	if err != nil || fn == nil {
		return false
	}
	args := make([]ir.Temp, inner.NumArgs())
	for i := range args {
		arg, isParam := param[inner.Arg(i)]
		if !isParam {
			return false
		}
		args[i] = arg
	}
	cp.c.hostCalls = append(cp.c.hostCalls, hostCall{call: inner, fn: fn, args: args, dst: n.Dst()})
	cp.emit(cp.op(opHost, uint32(len(cp.c.hostCalls)-1)))
	return true
}

// funcDefers reports whether f registers a deferred call anywhere.
func funcDefers(f *ir.Func) bool {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if _, isDefer := in.(*ir.Defer); isDefer {
				return true
			}
		}
	}
	return false
}

// term compiles a block's terminator.
func (cp *compiler) term(b *ir.Block) {
	f := cp.c.fn
	switch t := b.Term().(type) {
	case nil:
		cp.fail(f.Pos(), fmt.Errorf("vm: %s: %s has no terminator", f.Name(), b.ID()))
	case *ir.Jump:
		cp.at(t.Pos(), false, -1)
		cp.emit(cp.op(opJmp, 0), uint32(t.Target()))
		cp.patches = append(cp.patches, len(cp.c.code)-1)
	case *ir.Branch:
		cp.at(t.Pos(), false, -1)
		if cp.inBank(bankW, t.Cond()) && cp.kind(t.Cond()) == ir.KindBool {
			cp.emit(cp.op(opBr, uint32(cp.loc(t.Cond()).reg())))
		} else {
			cp.emit(cp.op(opBrX, uint32(t.Cond())))
		}
		cp.emit(uint32(t.IfTrue()), uint32(t.IfFalse()))
		cp.patches = append(cp.patches, len(cp.c.code)-2, len(cp.c.code)-1)
		if opcode(cp.c.code[len(cp.c.code)-3]&0xff) == opBrX {
			cp.emit(uint32(b.ID()))
		}
	case *ir.Return:
		cp.at(t.Pos(), false, -1)
		switch {
		case t.Ctl() != ir.CtlNone:
			// A signalling callback's control return: the outcome and its
			// value travel boxed to the driver that called the callback.
			cp.c.ctls = append(cp.c.ctls, t)
			cp.emit(cp.op(opRetCtl, uint32(len(cp.c.ctls)-1)))
		case !t.HasVal():
			cp.emit(cp.op(opRetU, 0))
		default:
			cp.emit(cp.op(opRet, uint32(t.Val())))
		}
	default:
		cp.fail(f.Pos(), fmt.Errorf("vm: %s: %s ends in %s, which this machine does not run",
			f.Name(), b.ID(), b.Term()))
	}
}

// Disassemble is the bytecode of the function named name in this machine's
// entry module, compiled as a call would compile it, for a test or a
// diagnostic that reads what the compiler produced.
func (m *Machine) Disassemble(name string) (string, error) {
	var found *ir.Func
	for _, f := range m.mod.Funcs() {
		if f.Name() == name {
			if found != nil {
				return "", fmt.Errorf("vm: %s names two of this module's declarations", name)
			}
			found = f
		}
	}
	if found == nil {
		return "", fmt.Errorf("vm: %s is not a function this module retained", name)
	}
	return m.codeOf(m.slotFor(found)).String(), nil
}

var opNames = [...]string{
	opNop: "nop", opLoadW: "loadw", opLoadS: "loads", opLoadR: "loadr", opOnce: "once",
	opMovW: "movw", opMovS: "movs", opMovR: "movr", opXMov: "xmov",
	opAddI: "addi", opSubI: "subi", opMulI: "muli", opDivI: "divi", opRemI: "remi", opNegI: "negi",
	opAddIW: "addiw", opSubIW: "subiw", opMulIW: "muliw", opNegIW: "negiw",
	opAddF: "addf", opSubF: "subf", opMulF: "mulf", opDivF: "divf", opNegF: "negf", opRemF: "remf",
	opCmpI: "cmpi", opCmpF: "cmpf", opEqS: "eqs", opEqW: "eqw", opNot: "not", opConcat: "concat",
	opMatchV: "matchv", opProjP: "projp", opProjF: "projf", opMake: "make",
	opCall: "call", opTail: "tail", opJmp: "jmp", opBr: "br", opBrX: "brx",
	opRet: "ret", opRetU: "retu", opRetCtl: "retctl", opIR: "ir", opHost: "host", opFail: "fail",
}

// String disassembles c, one instruction per line: its offset, opcode, the
// word-0 operand and the operand words, and for an instruction run by its
// handler, the instruction. The site table delimits instructions, so the
// listing needs no per-opcode length.
func (c *code) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d word, %d str, %d ref registers\n", c.fn.Name(), c.nW, c.nS, c.nR)
	for i, st := range c.sites {
		end := len(c.code)
		if i+1 < len(c.sites) {
			end = c.sites[i+1].pc
		}
		if st.pc >= end {
			continue
		}
		ins := c.code[st.pc]
		fmt.Fprintf(&b, "%4d  %-7s %d", st.pc, opNames[ins&0xff], ins>>8)
		for _, w := range c.code[st.pc+1 : end] {
			fmt.Fprintf(&b, " %d", w)
		}
		switch opcode(ins & 0xff) {
		case opIR:
			fmt.Fprintf(&b, "  ; %s", c.irs[ins>>8])
		case opHost:
			fmt.Fprintf(&b, "  ; %s", c.hostCalls[ins>>8].call)
		}
		b.WriteString("\n")
	}
	return b.String()
}
