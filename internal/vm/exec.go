package vm

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// regStack is one goroutine's register stack: three banks, each a stack of
// windows. An activation's window starts at the bank tops when it is entered
// and is popped when it exits. See bytecode.go for which values live where.
//
// A STACK BELONGS TO ONE GOROUTINE AT A TIME. A bytecode call runs its callee
// on the caller's stack. A call that arrives from Go — a Run, a host callback,
// an iteration driver, a task body — takes a stack from stackPool and returns
// it when the activation exits, because a callback can run on a goroutine
// other than the one that created it (a lazy sequence handed to a task), and
// nothing the callback receives says which goroutine it is on.
type regStack struct {
	w []uint64
	s []string
	r []any
	// wt, st and rt are the bank tops.
	wt, st, rt int
	// The last return: its value in the bank rcls names, and the value's
	// type, which boxing it needs.
	rw   uint64
	rs   string
	rr   any
	rcls bank
	rty  *ir.ValType
	// Scratch for a tail transfer's arguments, which move from the caller's
	// window into the callee's at the same base.
	tw []uint64
	ts []string
	tr []any
	// frames are the activation records of this stack's activations, reused
	// by depth on the stack: nf are in use. A frame is not allocated per
	// call, because the recursion through loop and exec makes Go's escape
	// analysis move one declared in the loop to the heap.
	frames []*frame
	nf     int
	// hw is the most records in use since the stack was last parked.
	hw int
}

// newFrame is the activation record for the next activation on s, with its
// per-activation state cleared. The caller sets the function, the runtime
// frame, the depth, testBody and the window.
//
// THE STATE IS CLEARED FIELD BY FIELD, AND ONLY WHERE IT IS SET, rather than
// by assigning a zero frame: that is a 22-word write with barriers on every
// call, and these fields are empty on almost every record. They are cleared
// here rather than at exit because a record a recovered panic unwound past
// is reused without its exit having run.
func (s *regStack) newFrame() *frame {
	if s.nf == len(s.frames) {
		s.frames = append(s.frames, &frame{stk: s, level: s.nf})
	}
	fr := s.frames[s.nf]
	if fr.trace != nil || fr.stages != nil || fr.deferred != nil || fr.releases != nil || fr.next != nil || fr.bad != nil {
		fr.trace, fr.stages, fr.deferred, fr.releases, fr.next, fr.bad = nil, nil, nil, nil, nil, nil
	}
	fr.guarded = false
	s.nf++
	if s.nf > s.hw {
		s.hw = s.nf
	}
	return fr
}

var stackPool = sync.Pool{New: func() any {
	return &regStack{w: make([]uint64, 256), s: make([]string, 64), r: make([]any, 128)}
}}

// push opens a window for c at the bank tops and answers its bases.
func (s *regStack) push(c *code) (wb, sb, rb int) {
	wb, sb, rb = s.wt, s.st, s.rt
	s.reserve(wb+c.nW, sb+c.nS, rb+c.nR)
	s.wt, s.st, s.rt = wb+c.nW, sb+c.nS, rb+c.nR
	return
}

// reserve grows each bank to at least the given length.
func (s *regStack) reserve(w, str, r int) {
	if w > len(s.w) {
		s.w = append(s.w, make([]uint64, w-len(s.w)+len(s.w))...)
	}
	if str > len(s.s) {
		s.s = append(s.s, make([]string, str-len(s.s)+len(s.s))...)
	}
	if r > len(s.r) {
		s.r = append(s.r, make([]any, r-len(s.r)+len(s.r))...)
	}
}

// pop closes fr's window, clearing its strings and references so the
// collector does not see them, and releases its activation record. Setting
// the tops and the record count to the frame's own, rather than counting
// down, is what makes a stack a recovered panic unwound through consistent
// again at the next exit below it.
func (s *regStack) pop(fr *frame) {
	if s.st > fr.sb {
		clear(s.s[fr.sb:s.st])
	}
	if s.rt > fr.rb {
		clear(s.r[fr.rb:s.rt])
	}
	s.wt, s.st, s.rt = fr.wb, fr.sb, fr.rb
	s.nf = fr.level
}

// spareStack is a register stack no activation is using. getStack takes it
// with one atomic swap, which serves the common case of one callback at a
// time; the pool serves the rest. Process-wide rather than per machine,
// because a host opens a machine per run and a stack parked on a discarded
// machine would never be reused.
var spareStack atomic.Pointer[regStack]

// getStack is a register stack for a call that arrives from Go.
func getStack() *regStack {
	if s := spareStack.Swap(nil); s != nil {
		return s
	}
	return stackPool.Get().(*regStack)
}

// putStack returns a stack getStack answered, with its tops at zero. The
// activation records it used are cleared, so a parked stack does not keep a
// finished program's functions reachable.
func putStack(s *regStack) {
	for _, fr := range s.frames[:s.hw] {
		// The references that would keep a program reachable; newFrame
		// clears the rest on reuse.
		fr.c, fr.fn, fr.runtime = nil, nil, nil
	}
	s.rr, s.rty, s.nf, s.hw = nil, nil, 0, 0
	if spareStack.CompareAndSwap(nil, s) {
		return
	}
	stackPool.Put(s)
}

// fnSlot is one function's entry in the program's function table: its graph
// and its bytecode, compiled on the first call.
type fnSlot struct {
	f    *ir.Func
	code atomic.Pointer[code]
	// value is the function as a value with no captures, built on the
	// first reference and shared by every later one.
	value atomic.Pointer[functionValue]
}

// named is the slot's function as a value.
func (s *fnSlot) named() *functionValue {
	if v := s.value.Load(); v != nil {
		return v
	}
	v := &functionValue{body: s.f, slot: s, arity: len(s.f.Params())}
	if !s.value.CompareAndSwap(nil, v) {
		return s.value.Load()
	}
	return v
}

// codeTable is the machine's function table. Shared by every view of one
// machine, so a function is compiled once per machine.
type codeTable struct {
	slots sync.Map // *ir.Func -> *fnSlot
}

func (m *Machine) slotFor(f *ir.Func) *fnSlot {
	if s, ok := m.codes.slots.Load(f); ok {
		return s.(*fnSlot)
	}
	s, _ := m.codes.slots.LoadOrStore(f, &fnSlot{f: f})
	return s.(*fnSlot)
}

// codeOf is f's bytecode, compiling it on first use.
func (m *Machine) codeOf(s *fnSlot) *code {
	if c := s.code.Load(); c != nil {
		return c
	}
	c := m.compile(s.f)
	if !s.code.CompareAndSwap(nil, c) {
		return s.code.Load()
	}
	return c
}

// frame is one activation: its function's bytecode, its window on the
// register stack, and the state an activation carries beside its registers.
type frame struct {
	c   *code
	fn  *ir.Func
	stk *regStack
	// wb, sb and rb are the window's bases in each bank.
	wb, sb, rb int
	runtime    *rt.Frame
	// trace is the `values:` rows an assertion in THIS activation has
	// recorded so far. Per activation because that is the assertion trace
	// boundary: a callee's comparisons must not reach its caller's report.
	// See assert.go.
	trace []rt.AssertionValueContext
	// stages is the `pipeline values:` rows a piped `testing.check` in this
	// activation has recorded (`ir.RecordStage`), spent by the check's
	// judgement. See assert.go.
	stages []rt.AssertionPipelineStage
	// deferred is this activation's registered, not-yet-run deferred calls,
	// in registration order. See defer.go.
	deferred []pendingDefer
	// releases end the deadlines this activation's Context rebinds entered,
	// run at its exit on every path. See context.go.
	releases []func()
	// testBody marks a test case's own activation. See activate.
	testBody bool
	// level is this record's index in its stack's frames.
	level int
	// depth is how many activations this goroutine has in progress,
	// counting this one. See depth.go.
	depth int
	// next is the tail call a handler handed back instead of making, which
	// the loop enters in this frame. See tail.go.
	next *tailTransfer
	// guarded says the activation's exit hooks are installed. See
	// execGuarded.
	guarded bool
	// bad is a value a handler wrote that its destination's bank cannot
	// hold. The loop reports it after the handler returns.
	bad error
}

// read is temporary t's value, boxed out of its bank.
func (fr *frame) read(t ir.Temp) (any, error) {
	c := fr.c
	if t == ir.NoTemp || int(t) >= len(c.locs) {
		return nil, fmt.Errorf("vm: %s: %s is outside the frame's %d registers",
			fr.fn.Name(), t, len(c.locs)-1)
	}
	l := c.locs[t]
	switch l.bank() {
	case bankW:
		return boxWord(fr.stk.w[fr.wb+l.reg()], c.types[t]), nil
	case bankS:
		return boxStr(fr.stk.s[fr.sb+l.reg()], c.types[t]), nil
	case bankNone:
		return boxNone(c.types[t]), nil
	}
	v := fr.stk.r[fr.rb+l.reg()]
	if v == nil {
		// `ir.Lint`'s RuleTempDefinedBeforeUse is what makes this
		// unreachable for a graph the producer retained. Reported rather
		// than trusted, because a hand-built module can reach it and a
		// silent nil would surface as a crash three frames away.
		return nil, fmt.Errorf("vm: %s: %s is read before anything wrote it; "+
			"ir.Lint's temp-defined-before-use rule should have caught this",
			fr.fn.Name(), t)
	}
	return v, nil
}

// write stores v into temporary t's bank. A value the bank cannot hold is
// recorded on the frame and reported by the loop.
func (fr *frame) write(t ir.Temp, v any) {
	c := fr.c
	if t == ir.NoTemp || int(t) >= len(c.locs) {
		return
	}
	l := c.locs[t]
	switch l.bank() {
	case bankW:
		w, ok := unboxWord(v)
		if !ok {
			fr.bad = fmt.Errorf("vm: %s: %s holds %s and received %T", fr.fn.Name(), t, c.types[t], v)
			return
		}
		fr.stk.w[fr.wb+l.reg()] = w
	case bankS:
		s, ok := unboxStr(v)
		if !ok {
			fr.bad = fmt.Errorf("vm: %s: %s holds %s and received %T", fr.fn.Name(), t, c.types[t], v)
			return
		}
		fr.stk.s[fr.sb+l.reg()] = s
	case bankR:
		fr.stk.r[fr.rb+l.reg()] = v
	}
}

// writeParam writes an operand arriving from Go into parameter register l:
// a reference or an Int directly, anything else through write.
func (fr *frame) writeParam(l loc, t ir.Temp, v any) {
	switch l.bank() {
	case bankR:
		fr.stk.r[fr.rb+l.reg()] = v
		return
	case bankW:
		if n, isInt := v.(int64); isInt {
			fr.stk.w[fr.wb+l.reg()] = uint64(n)
			return
		}
	}
	fr.write(t, v)
}

// writeField stores field f of rec into temporary t: a scalar slot straight
// into a register of its bank, anything else boxed through write.
func (fr *frame) writeField(t ir.Temp, rec *rt.Record, f *rt.FieldDesc) {
	l := fr.c.locs[t]
	switch l.bank() {
	case bankW:
		switch f.Type {
		case rt.SlotInt, rt.SlotFloat, rt.SlotBool, rt.SlotByte:
			fr.stk.w[fr.wb+l.reg()] = rec.W[f.Slot]
			return
		}
	case bankS:
		if f.Type == rt.SlotString || f.Type == rt.SlotBytes {
			fr.stk.s[fr.sb+l.reg()] = rec.S[f.Slot]
			return
		}
	case bankR:
		if f.Type == rt.SlotRef {
			fr.stk.r[fr.rb+l.reg()] = rec.R[f.Slot]
			return
		}
	}
	fr.write(t, rec.SlotValue(f))
}

// fill writes temporary t into field f of a record under construction: a
// scalar register straight into its slot, anything else boxed.
func (fr *frame) fill(rec *rt.Record, f *rt.FieldDesc, t ir.Temp) error {
	l := fr.c.locs[t]
	switch f.Type {
	case rt.SlotInt, rt.SlotFloat, rt.SlotBool, rt.SlotByte:
		if l.bank() == bankW {
			rec.W[f.Slot] = fr.stk.w[fr.wb+l.reg()]
			return nil
		}
		v, err := fr.read(t)
		if err != nil {
			return err
		}
		w, ok := unboxWord(v)
		if !ok {
			return fmt.Errorf("vm: %s: field %q holds a scalar and %s is %T", fr.fn.Name(), f.Name, t, v)
		}
		rec.W[f.Slot] = w
	case rt.SlotString, rt.SlotBytes:
		if l.bank() == bankS {
			rec.S[f.Slot] = fr.stk.s[fr.sb+l.reg()]
			return nil
		}
		v, err := fr.read(t)
		if err != nil {
			return err
		}
		s, ok := unboxStr(v)
		if !ok {
			return fmt.Errorf("vm: %s: field %q holds a string and %s is %T", fr.fn.Name(), f.Name, t, v)
		}
		rec.S[f.Slot] = s
	default:
		v, err := fr.read(t)
		if err != nil {
			return err
		}
		rec.R[f.Slot] = v
	}
	return nil
}

// projHit is an opProjF instruction's cached field: the descriptor it last
// read and the field it found there.
type projHit struct {
	desc  *rt.TypeDesc
	field *rt.FieldDesc
}

// setRet records v as the last return, boxed.
func (s *regStack) setRet(v any) {
	s.rr, s.rcls, s.rty = v, bankR, nil
}

// ret is the last return, boxed.
func (s *regStack) ret() any {
	switch s.rcls {
	case bankW:
		return boxWord(s.rw, s.rty)
	case bankS:
		return boxStr(s.rs, s.rty)
	case bankR:
		return s.rr
	}
	return boxNone(s.rty)
}

// deliver moves the last return into temporary t.
func (fr *frame) deliver(t ir.Temp) {
	c, s := fr.c, fr.stk
	if t == ir.NoTemp || int(t) >= len(c.locs) {
		return
	}
	l := c.locs[t]
	if l.bank() == s.rcls {
		switch l.bank() {
		case bankW:
			s.w[fr.wb+l.reg()] = s.rw
		case bankS:
			s.s[fr.sb+l.reg()] = s.rs
		case bankR:
			s.r[fr.rb+l.reg()] = s.rr
		}
		return
	}
	if l.bank() == bankNone {
		return
	}
	fr.write(t, s.ret())
}

// activate runs one activation of f over boxed operands: args, then the
// captures a function value appends after them. testBody marks a
// test case's own body, whose `try` propagation leaves the activation as a
// *tryReturn instead of becoming its result. caller is the depth of the
// activation that called it. stk is the caller's register stack when the
// call is made from a bytecode activation on this goroutine, or nil for a
// call that arrives from Go.
func (m *Machine) activate(f *ir.Func, args, caps []any, runtime *rt.Frame, testBody bool, caller int, stk *regStack) (any, error) {
	return m.activateSlot(m.slotFor(f), args, caps, runtime, testBody, caller, stk)
}

// activateSlot is activate for a function whose table entry the caller holds.
func (m *Machine) activateSlot(slot *fnSlot, args, caps []any, runtime *rt.Frame, testBody bool, caller int, stk *regStack) (any, error) {
	f := slot.f
	params := f.Params()
	if len(args)+len(caps) != len(params) {
		return nil, fmt.Errorf("vm: %s takes %d operand(s) and got %d",
			f.Name(), len(params), len(args)+len(caps))
	}
	if caller >= rt.MaxCallDepth {
		return nil, callDepthExceeded{}
	}
	if m.fuel != nil {
		if err := m.fuel.burn(); err != nil {
			return nil, err
		}
	}
	pooled := stk == nil
	if pooled {
		stk = getStack()
	}
	c := m.codeOf(slot)
	fr := stk.newFrame()
	fr.c, fr.fn, fr.runtime, fr.testBody, fr.depth = c, f, runtime, testBody, caller+1
	fr.wb, fr.sb, fr.rb = stk.push(c)
	for i, a := range args {
		fr.writeParam(c.params[i], params[i].Temp, a)
	}
	for i, a := range caps {
		fr.writeParam(c.params[len(args)+i], params[len(args)+i].Temp, a)
	}
	var err error
	if fr.bad != nil {
		err = fr.bad
		stk.pop(fr)
	} else {
		err = m.exec(fr)
	}
	var v any
	if err == nil {
		v = stk.ret()
	}
	if pooled {
		putStack(stk)
	}
	return v, err
}

// exec runs an activation whose window is open and whose parameters are
// written, leaving its result as the stack's last return, and closes the
// window.
func (m *Machine) exec(fr *frame) error {
	var err error
	if fr.c.guard {
		err = m.execGuarded(fr)
	} else {
		err = m.loop(fr, 0)
		if err == errNeedsGuard {
			err = m.execGuarded(fr)
		}
	}
	fr.stk.pop(fr)
	return err
}

// errNeedsGuard is the loop asking to be re-entered with the exit hooks
// installed: a tail transfer entered a function that needs them.
var errNeedsGuard = errors.New("vm: re-enter with exit hooks")

// execGuarded runs an activation with its exit hooks: the Context deadlines
// its rebinds entered end, and a panic unwinding through it (a cancelled
// task) runs its deferred calls first.
func (m *Machine) execGuarded(fr *frame) error {
	defer func() {
		for i := len(fr.releases) - 1; i >= 0; i-- {
			fr.releases[i]()
		}
	}()
	defer func() {
		if len(fr.deferred) == 0 {
			return
		}
		if p := recover(); p != nil {
			m.unwindDefers(fr, nil, nil)
			panic(p)
		}
	}()
	fr.guarded = true
	return m.loop(fr, 0)
}

// boxLoc is the value in register l, boxed as type ty.
func (fr *frame) boxLoc(l loc, ty *ir.ValType) any {
	switch l.bank() {
	case bankW:
		return boxWord(fr.stk.w[fr.wb+l.reg()], ty)
	case bankS:
		return boxStr(fr.stk.s[fr.sb+l.reg()], ty)
	case bankR:
		return fr.stk.r[fr.rb+l.reg()]
	}
	return boxNone(ty)
}

// finish runs the pending deferred calls of an activation that is leaving,
// keeping its result unless a cleanup fails.
func (m *Machine) finish(fr *frame, err error) error {
	if len(fr.deferred) == 0 {
		return err
	}
	if err == nil && m.isBootFunc(fr.fn) {
		m.deferToBootCleanup(fr)
		return nil
	}
	s := fr.stk
	rw, rs, rr, rcls, rty := s.rw, s.rs, s.rr, s.rcls, s.rty
	_, err = m.unwindDefers(fr, nil, err)
	s.rw, s.rs, s.rr, s.rcls, s.rty = rw, rs, rr, rcls, rty
	return err
}

// transfer replaces the activation's function with next's callee, in this
// frame, after the caller's deferred calls run. See tail.go.
func (m *Machine) transfer(fr *frame, next *tailTransfer) error {
	if m.fuel != nil {
		if err := m.fuel.burn(); err != nil {
			return err
		}
	}
	if len(fr.deferred) != 0 && m.isBootFunc(fr.fn) {
		// A boot ending in a tail call: its cleanup still belongs to the app.
		m.deferToBootCleanup(fr)
	}
	if len(fr.deferred) != 0 {
		if _, err := m.unwindDefers(fr, nil, nil); err != nil {
			return err
		}
	}
	if len(next.args) != len(next.fn.Params()) {
		return fmt.Errorf("vm: %s takes %d operand(s) and got %d",
			next.fn.Name(), len(next.fn.Params()), len(next.args))
	}
	fr.enterCode(m.codeOf(m.slotFor(next.fn)))
	for i, p := range next.fn.Params() {
		fr.write(p.Temp, next.args[i])
	}
	return fr.bad
}

// enterCode resizes the frame's window for c at the same bases and makes c
// the frame's function. The caller writes the parameters.
func (fr *frame) enterCode(c *code) {
	s := fr.stk
	clear(s.s[fr.sb:s.st])
	clear(s.r[fr.rb:s.rt])
	s.reserve(fr.wb+c.nW, fr.sb+c.nS, fr.rb+c.nR)
	s.wt, s.st, s.rt = fr.wb+c.nW, fr.sb+c.nS, fr.rb+c.nR
	fr.c, fr.fn = c, c.fn
	fr.trace, fr.stages = nil, nil
}

// fault decides where an error out of the instruction at pc goes: to its
// block's fault handler (answering the handler's pc), out of the function as
// the activation's result (a `try` propagation, answering -1 and nil), or out
// of the function as an error.
func (m *Machine) fault(fr *frame, pc int, err error) (int, error) {
	st := fr.c.siteAt(pc)
	if st != nil {
		// A callee past the depth limit refused to start. This instruction
		// is the call that asked for it, or the host call or iteration
		// whose callback did, so its line is the fault's.
		err = positionCallDepth(err, st.pos)
	}
	if propagated, ok := err.(*tryReturn); ok {
		if fr.testBody {
			return -1, propagated
		}
		fr.stk.setRet(propagated.value)
		return -1, nil
	}
	if st == nil || !st.faults {
		return -1, err
	}
	// GAP 2. Where the fault goes is the block's, and no edge means it
	// leaves the function. See internal/ir/fault.go.
	if st.handler >= 0 {
		return st.handler, nil
	}
	// NO EDGE, SO THE FAULT LEAVES THE FUNCTION — and it is MARKED on the
	// way out. A caller that has to tell a Nomi fault from a machine limit
	// cannot do it from the message, and the graph already answers it: an
	// error out of an instruction that CAN fault, with no edge to take, is a
	// fault. `Fault.Error()` delegates, so every existing reading of the
	// text is unmoved. See assert.go.
	//
	// AN ERROR THAT IS ALREADY CLASSIFIED PASSES THROUGH. An `ir.Call` can
	// fault, so a fault that left a CALLEE reaches this arm again at the
	// call site; wrapping twice would say nothing new, and wrapping an
	// assertion's unwind as a fault would say something false.
	return -1, classifyFault(err)
}

// line is the source line of the instruction at pc.
func (c *code) line(pc int) int {
	if st := c.siteAt(pc); st != nil {
		return st.pos.Line()
	}
	return 0
}

// loop runs the activation's bytecode from pc to an exit.
//
// There is no bound on transfers. An inlined `Iter.loop` is the one cycle the
// producer builds, and a loop that never breaks runs forever, as the language
// says it does.
func (m *Machine) loop(fr *frame, pc int) (err error) {
	c := fr.c
	code := c.code
	stk := fr.stk
	w := stk.w[fr.wb : fr.wb+c.nW]
	s := stk.s[fr.sb : fr.sb+c.nS]
	r := stk.r[fr.rb : fr.rb+c.nR]
	var at int
	for {
		at = pc
		ins := code[pc]
		d := ins >> 8
		switch opcode(ins & 0xff) {
		case opNop:
			pc++
		case opLoadW:
			w[d] = c.kw[code[pc+1]]
			pc += 2
		case opLoadS:
			s[d] = c.ks[code[pc+1]]
			pc += 2
		case opLoadR:
			r[d] = c.kr[code[pc+1]]
			pc += 2
		case opOnce:
			if v := c.onces[code[pc+1]].forced.Load(); v != nil {
				fr.write(ir.Temp(d), *v)
			} else {
				err = m.irInstr(fr, c.irs[code[pc+2]])
				w, s, r = stk.w[fr.wb:fr.wb+c.nW], stk.s[fr.sb:fr.sb+c.nS], stk.r[fr.rb:fr.rb+c.nR]
			}
			if err == nil && fr.bad != nil {
				err = fr.bad
			}
			if err != nil {
				goto failed
			}
			pc += 3
		case opMovW:
			w[d] = w[code[pc+1]]
			pc += 2
		case opMovS:
			s[d] = s[code[pc+1]]
			pc += 2
		case opMovR:
			r[d] = r[code[pc+1]]
			pc += 2
		case opXMov:
			var v any
			if v, err = fr.read(ir.Temp(code[pc+1])); err == nil {
				fr.write(ir.Temp(d), v)
				err = fr.bad
			}
			if err != nil {
				goto failed
			}
			pc += 2

		case opAddI:
			a, b := int64(w[code[pc+1]]), int64(w[code[pc+2]])
			if rt.AddOverflows(a, b) {
				err = rt.OverflowError(c.line(at), "+", a, b)
				goto failed
			}
			w[d] = uint64(a + b)
			pc += 3
		case opSubI:
			a, b := int64(w[code[pc+1]]), int64(w[code[pc+2]])
			if rt.SubOverflows(a, b) {
				err = rt.OverflowError(c.line(at), "-", a, b)
				goto failed
			}
			w[d] = uint64(a - b)
			pc += 3
		case opMulI:
			a, b := int64(w[code[pc+1]]), int64(w[code[pc+2]])
			if rt.MulOverflows(a, b) {
				err = rt.OverflowError(c.line(at), "*", a, b)
				goto failed
			}
			w[d] = uint64(a * b)
			pc += 3
		case opDivI:
			a, b := int64(w[code[pc+1]]), int64(w[code[pc+2]])
			if b == 0 {
				err = rt.DivByZeroError(c.line(at))
				goto failed
			}
			if rt.QuoOverflows(a, b) {
				// The division case a backend forgets, which is why
				// `rt.QuoOverflows` exists to say so.
				err = rt.OverflowError(c.line(at), "/", a, b)
				goto failed
			}
			w[d] = uint64(a / b)
			pc += 3
		case opRemI:
			a, b := int64(w[code[pc+1]]), int64(w[code[pc+2]])
			if b == 0 {
				err = rt.DivByZeroError(c.line(at))
				goto failed
			}
			// Go's `%` answers 0 for MinInt64 % -1 without a panic, and
			// that is Nomi's answer, so no overflow is recorded.
			w[d] = uint64(a % b)
			pc += 3
		case opNegI:
			// `rt.NegInt` is `SubInt(0, a, line)`, so negation faults on
			// MinInt64 like the subtraction it performs.
			a := int64(w[code[pc+1]])
			if rt.SubOverflows(0, a) {
				err = rt.OverflowError(c.line(at), "-", 0, a)
				goto failed
			}
			w[d] = uint64(-a)
			pc += 2
		case opAddIW:
			w[d] = w[code[pc+1]] + w[code[pc+2]]
			pc += 3
		case opSubIW:
			w[d] = w[code[pc+1]] - w[code[pc+2]]
			pc += 3
		case opMulIW:
			w[d] = uint64(int64(w[code[pc+1]]) * int64(w[code[pc+2]]))
			pc += 3
		case opNegIW:
			w[d] = -w[code[pc+1]]
			pc += 2

		case opAddF:
			w[d] = math.Float64bits(math.Float64frombits(w[code[pc+1]]) + math.Float64frombits(w[code[pc+2]]))
			pc += 3
		case opSubF:
			w[d] = math.Float64bits(math.Float64frombits(w[code[pc+1]]) - math.Float64frombits(w[code[pc+2]]))
			pc += 3
		case opMulF:
			w[d] = math.Float64bits(math.Float64frombits(w[code[pc+1]]) * math.Float64frombits(w[code[pc+2]]))
			pc += 3
		case opDivF:
			// `rt.DivFloat` is Go's own `/`: IEEE has an answer for every
			// input including a zero divisor.
			w[d] = math.Float64bits(math.Float64frombits(w[code[pc+1]]) / math.Float64frombits(w[code[pc+2]]))
			pc += 3
		case opNegF:
			w[d] = math.Float64bits(rt.NegFloat(math.Float64frombits(w[code[pc+1]])))
			pc += 2
		case opRemF:
			// `rt.ModFloat` always traps, and the checker rejects the
			// construct; the text is rt's one home.
			err = errors.New(rt.FloatModuloText(c.line(at)))
			goto failed

		case opCmpI:
			a, b := int64(w[code[pc+1]]), int64(w[code[pc+2]])
			var held bool
			switch code[pc+3] {
			case condEq:
				held = a == b
			case condNe:
				held = a != b
			case condLt:
				held = a < b
			case condLe:
				held = a <= b
			case condGt:
				held = a > b
			default:
				held = a >= b
			}
			w[d] = b2w(held)
			pc += 4
		case opCmpF:
			a, b := math.Float64frombits(w[code[pc+1]]), math.Float64frombits(w[code[pc+2]])
			var held bool
			// Float equality is reflexive for NaN; ordering stays unordered.
			switch code[pc+3] {
			case condEq:
				held = rt.EqFloat(a, b)
			case condNe:
				held = !rt.EqFloat(a, b)
			case condLt:
				held = a < b
			case condLe:
				held = a <= b
			case condGt:
				held = a > b
			default:
				held = a >= b
			}
			w[d] = b2w(held)
			pc += 4
		case opEqS:
			w[d] = b2w((s[code[pc+1]] == s[code[pc+2]]) == (code[pc+3] == condEq))
			pc += 4
		case opEqW:
			w[d] = b2w((w[code[pc+1]] == w[code[pc+2]]) == (code[pc+3] == condEq))
			pc += 4
		case opNot:
			w[d] = w[code[pc+1]] ^ 1
			pc += 2
		case opConcat:
			n := int(code[pc+1])
			size := 0
			for i := range n {
				size += len(s[code[pc+2+i]])
			}
			var b strings.Builder
			b.Grow(size)
			for i := range n {
				b.WriteString(s[code[pc+2+i]])
			}
			s[d] = b.String()
			pc += 2 + n

		case opMatchV:
			if rec, ok := r[code[pc+1]].(*rt.Record); ok && rec.Desc.Kind == rt.KindEnum {
				w[d] = b2w(rec.Desc.Name == c.ks[code[pc+2]] && rec.Desc.Variants[rec.Tag].Name == c.ks[code[pc+3]])
			} else if err = m.irInstr(fr, c.irs[code[pc+4]]); err != nil {
				// Not a variant: the handler reports it as the walker did.
				goto failed
			}
			pc += 5
		case opProjP:
			// A positional variant's one payload, straight from its slot.
			if rec, ok := r[code[pc+1]].(*rt.Record); ok && rec.Desc.Kind == rt.KindEnum &&
				rec.Desc.Variants[rec.Tag].Shape == rt.VariantPositional {
				fr.writeField(ir.Temp(d), rec, &rec.Desc.Variants[rec.Tag].Fields[0])
			} else {
				err = m.irInstr(fr, c.irs[code[pc+2]])
			}
			if err == nil && fr.bad != nil {
				err = fr.bad
			}
			if err != nil {
				goto failed
			}
			pc += 3
		case opProjF:
			// A struct's or record's field by name, through the instruction's
			// cache of the last descriptor it read: one pointer compare and an
			// indexed load when the subject's layout repeats.
			found := false
			if rec, ok := r[code[pc+1]].(*rt.Record); ok && (rec.Desc.Kind == rt.KindStruct || rec.Desc.Kind == rt.KindAnon) {
				cache := &c.pcache[code[pc+4]]
				hit := cache.Load()
				if hit == nil || hit.desc != rec.Desc {
					if i := rec.Desc.FieldIndex(c.ks[code[pc+2]]); i >= 0 {
						hit = &projHit{desc: rec.Desc, field: &rec.Desc.Fields[i]}
						cache.Store(hit)
					} else {
						hit = nil
					}
				}
				if hit != nil {
					fr.writeField(ir.Temp(d), rec, hit.field)
					found = true
				}
			}
			if !found {
				err = m.irInstr(fr, c.irs[code[pc+3]])
			}
			if err == nil && fr.bad != nil {
				err = fr.bad
			}
			if err != nil {
				goto failed
			}
			pc += 5
		case opMake:
			// A struct, record, tuple, distinct or variant over a descriptor
			// the compiler resolved: each operand written into its field's
			// slot, a scalar straight from its register.
			desc := c.descs[code[pc+1]]
			var rec *rt.Record
			if desc.Kind == rt.KindEnum {
				rec = desc.NewVariant(int(code[pc+2]))
			} else {
				rec = desc.New()
			}
			n := int(code[pc+3])
			fields := rec.Layout().Fields
			for i := range n {
				if err = fr.fill(rec, &fields[code[pc+4+2*i]], ir.Temp(code[pc+5+2*i])); err != nil {
					goto failed
				}
			}
			r[d] = rec
			pc += 4 + 2*n

		case opCall, opTail:
			if m.fuel != nil {
				if err = m.fuel.burn(); err != nil {
					goto failed
				}
			}
			callee := m.codeOf(c.callees[code[pc+1]])
			n := int(code[pc+2])
			args := code[pc+3 : pc+3+n]
			if opcode(ins&0xff) == opTail && !fr.testBody {
				// A tail call: the callee replaces this activation. With
				// deferred calls pending, the operands are boxed first,
				// because the deferred calls run on this stack before the
				// callee's window replaces this one.
				if len(fr.deferred) != 0 {
					next := &tailTransfer{fn: callee.fn, args: make([]any, n)}
					for i, a := range args {
						next.args[i] = fr.boxLoc(loc(a), callee.types[callee.fn.Params()[i].Temp])
					}
					if err = m.transfer(fr, next); err != nil {
						return m.finish(fr, err)
					}
				} else {
					m.moveTailArgs(fr, callee, args)
				}
				c = fr.c
				code = c.code
				w, s, r = stk.w[fr.wb:fr.wb+c.nW], stk.s[fr.sb:fr.sb+c.nS], stk.r[fr.rb:fr.rb+c.nR]
				pc = 0
				if c.guard && !fr.guarded {
					return errNeedsGuard
				}
				continue
			}
			if fr.depth >= rt.MaxCallDepth {
				err = callDepthExceeded{}
				goto failed
			}
			cf := stk.newFrame()
			cf.c, cf.fn, cf.runtime, cf.depth, cf.testBody = callee, callee.fn, fr.runtime, fr.depth+1, false
			cf.wb, cf.sb, cf.rb = stk.push(callee)
			pw, ps, pr := cf.wb, cf.sb, cf.rb
			for _, a := range args {
				l := loc(a)
				switch l.bank() {
				case bankW:
					stk.w[pw] = w[l.reg()]
					pw++
				case bankS:
					stk.s[ps] = s[l.reg()]
					ps++
				case bankR:
					stk.r[pr] = r[l.reg()]
					pr++
				}
			}
			err = m.exec(cf)
			w, s, r = stk.w[fr.wb:fr.wb+c.nW], stk.s[fr.sb:fr.sb+c.nS], stk.r[fr.rb:fr.rb+c.nR]
			if err != nil {
				goto failed
			}
			fr.deliver(ir.Temp(d))
			if fr.bad != nil {
				err = fr.bad
				goto failed
			}
			pc += 3 + n

		case opJmp:
			pc = int(code[pc+1])
			if m.fuel != nil && pc <= at {
				if err = m.fuel.burn(); err != nil {
					goto failed
				}
			}
		case opBr:
			if w[d] != 0 {
				pc = int(code[pc+1])
			} else {
				pc = int(code[pc+2])
			}
			if m.fuel != nil && pc <= at {
				if err = m.fuel.burn(); err != nil {
					goto failed
				}
			}
		case opBrX:
			var cond any
			if cond, err = fr.read(ir.Temp(d)); err != nil {
				return m.finish(fr, err)
			}
			taken, isBool := asBool(cond)
			if !isBool {
				return m.finish(fr, fmt.Errorf("vm: %s: %s branches on %T, which is not a Bool",
					fr.fn.Name(), ir.BlockID(code[pc+3]), cond))
			}
			if taken {
				pc = int(code[pc+1])
			} else {
				pc = int(code[pc+2])
			}
			if m.fuel != nil && pc <= at {
				if err = m.fuel.burn(); err != nil {
					goto failed
				}
			}

		case opRet:
			t := ir.Temp(d)
			l := c.locs[t]
			stk.rcls, stk.rty = l.bank(), c.types[t]
			switch l.bank() {
			case bankW:
				stk.rw = w[l.reg()]
			case bankS:
				stk.rs = s[l.reg()]
			case bankR:
				stk.rr = r[l.reg()]
				if stk.rr == nil {
					_, err = fr.read(t)
					return m.finish(fr, err)
				}
			}
			return m.finish(fr, nil)
		case opRetU:
			stk.rcls, stk.rty = bankNone, nil
			return m.finish(fr, nil)
		case opRetCtl:
			t := c.ctls[d]
			out := &ctlValue{ctl: t.Ctl()}
			if t.HasVal() {
				if out.v, err = fr.read(t.Val()); err != nil {
					return m.finish(fr, err)
				}
			}
			stk.setRet(out)
			return m.finish(fr, nil)

		case opIR:
			err = m.irInstr(fr, c.irs[d])
			if err == nil && fr.bad != nil {
				err = fr.bad
			}
			w, s, r = stk.w[fr.wb:fr.wb+c.nW], stk.s[fr.sb:fr.sb+c.nS], stk.r[fr.rb:fr.rb+c.nR]
			if err == errTailTransfer {
				next := fr.next
				fr.next = nil
				if err = m.transfer(fr, next); err != nil {
					return m.finish(fr, err)
				}
				c = fr.c
				code = c.code
				w, s, r = stk.w[fr.wb:fr.wb+c.nW], stk.s[fr.sb:fr.sb+c.nS], stk.r[fr.rb:fr.rb+c.nR]
				pc = 0
				if c.guard && !fr.guarded {
					return errNeedsGuard
				}
				continue
			}
			if err != nil {
				goto failed
			}
			pc++

		case opHost:
			h := &c.hostCalls[d]
			args := make([]any, len(h.args))
			for i, t := range h.args {
				if args[i], err = fr.read(t); err != nil {
					goto failed
				}
			}
			v, herr := callAdapter(h.fn, fr.runtime, args)
			w, s, r = stk.w[fr.wb:fr.wb+c.nW], stk.s[fr.sb:fr.sb+c.nS], stk.r[fr.rb:fr.rb+c.nR]
			if herr != nil {
				err = herr
				goto failed
			}
			fr.write(h.call.Dst(), v)
			if fr.bad != nil {
				err = fr.bad
				goto failed
			}
			pc++

		case opFail:
			return m.finish(fr, c.fails[d])

		default:
			return fmt.Errorf("vm: %s: opcode %d at %d", fr.fn.Name(), ins&0xff, pc)
		}
		continue
	failed:
		fr.bad = nil
		pc, err = m.fault(fr, at, err)
		if pc < 0 {
			return m.finish(fr, err)
		}
		// A fault edge was taken: the handler's block runs next.
		err = nil
	}
}

func b2w(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// moveTailArgs is a direct tail call: the caller's deferred calls run after
// the operands are read, then the callee replaces the caller in this frame
// and its parameters receive the operands.
func (m *Machine) moveTailArgs(fr *frame, callee *code, args []uint32) {
	stk := fr.stk
	stk.tw, stk.ts, stk.tr = stk.tw[:0], stk.ts[:0], stk.tr[:0]
	for _, a := range args {
		l := loc(a)
		switch l.bank() {
		case bankW:
			stk.tw = append(stk.tw, stk.w[fr.wb+l.reg()])
		case bankS:
			stk.ts = append(stk.ts, stk.s[fr.sb+l.reg()])
		case bankR:
			stk.tr = append(stk.tr, stk.r[fr.rb+l.reg()])
		}
	}
	fr.enterCode(callee)
	copy(stk.w[fr.wb:], stk.tw)
	copy(stk.s[fr.sb:], stk.ts)
	copy(stk.r[fr.rb:], stk.tr)
	clear(stk.ts)
	clear(stk.tr)
}

// call runs one function to its Return, from Go.
func (m *Machine) call(f *ir.Func, args []any) (any, error) {
	runtime := m.hostFrame
	if runtime == nil {
		runtime = rt.NewFrame(m.background())
	}
	return m.callWithFrame(f, args, runtime)
}

func (m *Machine) callWithFrame(f *ir.Func, args []any, runtime *rt.Frame) (any, error) {
	return m.activate(f, args, nil, runtime, false, m.depth, nil)
}

// callFrom runs f as a callee of the activation fr, on fr's runtime frame and
// register stack.
func (m *Machine) callFrom(fr *frame, f *ir.Func, args []any) (any, error) {
	return m.activate(f, args, nil, fr.runtime, false, fr.depth, fr.stk)
}
