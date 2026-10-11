package vm

// THE DEBUGGER: what a source-level debugger (`nomi dap`) drives.
//
// A machine opened WithDebugger compiles each function with an `opStmt` at
// every statement mark the producer recorded (ir.Block.Stmts) and an `opPC`
// before every instruction that may call back into Nomi. A machine without
// one compiles neither, so it runs exactly the bytecode it ran before this
// file existed (TestDebug_AMachineWithoutADebuggerCompilesNoHooks).
//
// At an `opStmt` the machine asks the Debugger whether to stop: a breakpoint
// on the statement's line, a step that has reached its target, or a pause.
// A stop parks the goroutine in the hook until a resume. While one goroutine
// is stopped, every other one parks silently at its next statement, so a
// stopped program is a still one. Faults stop at `Machine.fault` with the
// faulting activation still live (Exceptions).
//
// # The logical call stack
//
// Each goroutine running Nomi code is a thread. A bytecode call runs its
// callee on the caller's register stack, but a callback started from Go (an
// iteration driver, a host call, a task body) runs on a pooled stack of its
// own, so one logical chain spans several stacks. A debug machine therefore
// links each activation to the one that started it (frame.parent): the
// innermost activation in progress on the same thread, which each thread
// tracks as its top. A thread is found from the rt frame an activation runs
// on: every frame derived on one goroutine carries its thread
// (rt.Frame.DebugThread), and a task's frame starts a new one. A callback
// started from Go receives the rt frame of the code driving it, so a lazy
// pipeline forced inside a task runs its callbacks on the task's thread.
//
// An activation started from Go under a named operation (`Iter.map`,
// `Result.map_err`, `Task.spawn`) shows that operation as a frame between it
// and its parent, at the parent's call: `main` → `Iter.map` → the lambda.
// A lazy pipeline's callback runs where the pipeline is forced, so its parent
// is the activation forcing it.
//
// A tail call replaces its caller's activation (decision 2 of the debugger
// plan): the caller is gone from the stack and the frame is marked as a tail
// call.

import (
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// StopReason is why a thread stopped.
type StopReason string

const (
	StopBreakpoint StopReason = "breakpoint"
	StopStep       StopReason = "step"
	StopPause      StopReason = "pause"
	// StopTrap is a Nomi fault: overflow, a zero divisor, an index out of
	// range, `todo`, a `case` with no arm, the call-depth limit.
	StopTrap StopReason = "trap"
	// StopAssertion is a failed `assert`, `refute` or `testing.check`.
	StopAssertion StopReason = "assertion"
	// StopMainError is `main` returning `Err`, stopped before its activation
	// ends.
	StopMainError StopReason = "error"
	// StopTaskFailure is a failed task's fault reaching the code that awaited
	// it (`Task.await`, `Task.await_all`), stopped at the await. The fault
	// itself stopped earlier, in the task, under the trap filter.
	StopTaskFailure StopReason = "task failure"
	// StopRestart is a restarted frame (RestartFrame) stopping at its first
	// statement.
	StopRestart StopReason = "restart"
)

// Stop is one thread stopping.
type Stop struct {
	Reason StopReason
	Thread int
	// Pos is where the thread stopped: the statement, or the faulting
	// instruction, or main's return.
	Pos ir.Pos
	// Text describes an exception stop: the fault's text, the assertion's
	// report as `nomi test` prints it undecorated, or the `Err` payload's
	// structural rendering.
	Text string
	// Assertion is the failed assertion's report for StopAssertion, which a
	// client renders as the test report does.
	Assertion *rt.AssertionFailure
	// Functions are the function breakpoints that stopped a StopBreakpoint,
	// by their index in the latest SetFunctionBreakpoints list, and
	// NotLines says no line breakpoint on the statement stopped it too.
	Functions []int
	NotLines  bool
}

// ExceptionFilters says which exceptions stop the program.
type ExceptionFilters struct {
	Traps      bool
	Assertions bool
	MainErrors bool
	// TaskFailures stops where a failed task's fault reaches its awaiter.
	TaskFailures bool
}

// BreakpointSpec is one line breakpoint a client asks for.
type BreakpointSpec struct {
	Line int
	// Hit, when not nil, decides each time a thread reaches the breakpoint
	// whether it stops there: a condition, a hit count, a logpoint. It runs
	// on the reaching thread before the thread stops, with the scope of the
	// statement it reached, and may evaluate expressions in it.
	Hit func(*Scope) Hit
}

// Hit is a breakpoint's decision at one pass.
type Hit struct {
	Stop bool
	// Text is said with the stop, as an exception's text is: why a
	// condition could not be decided.
	Text string
}

// Breakpoint is one requested line breakpoint and where it landed.
type Breakpoint struct {
	// Line is the line asked for.
	Line int
	// Verified says the breakpoint landed on a statement, on Resolved.
	Verified bool
	Resolved int
}

// Thread is one goroutine running Nomi code.
type Thread struct {
	ID int
	// Name is the function its outermost activation runs.
	Name string
}

// StackFrame is one frame of a stopped thread's logical call stack,
// innermost first.
type StackFrame struct {
	// ID names the frame for Locals until the program resumes.
	ID   int
	Name string
	Pos  ir.Pos
	// Tail says a tail call replaced this activation's caller, which the
	// stack no longer shows.
	Tail bool
	// Via says this frame is the Go-driven operation (Name, `Iter.map`)
	// that started the frame above it, rather than a Nomi activation. It has
	// no locals.
	Via bool
}

// Variable is one name visible in a frame.
type Variable struct {
	Name string
	// Type is the variable's type as Nomi spells it.
	Type  string
	Value rt.Inspected
	// Text, when set, is how the variable reads in place of its value's
	// own text: a pipeline stage that has not run, or a lazy one.
	Text string
	// Decl is where the variable is bound (ir.Var.Decl), or where a
	// pipeline stage is written; the zero Pos when unknown.
	Decl ir.Pos
}

// Debugger is the control a debug client has over the machines opened with
// it. Its methods are safe to call from any goroutine.
type Debugger struct {
	mu   sync.Mutex
	cond *sync.Cond
	// active says a statement hook has anything to check: a breakpoint, a
	// pause, a step, or a stopped thread the others must park for. slow says
	// it has more than breakpoints to check, which takes the lock; with
	// breakpoints alone a hook reads bpSnap, an immutable copy of bps, and
	// takes no lock unless it hits one.
	active atomic.Bool
	slow   atomic.Bool
	bpSnap atomic.Pointer[map[string]map[int]bool]

	modules map[*ir.Module]bool
	// marks are every statement mark of the known functions, by file.
	marks map[string][]markLine
	// requested are the breakpoint lines asked for, by the client's file
	// name; bps are where they landed, by the mark's file and line.
	requested map[string][]BreakpointSpec
	bps       map[string]map[int][]func(*Scope) Hit
	// funcs are the functions a function breakpoint can name
	// (debugfunc.go), funcIndex each one's index; requestedFns are the
	// function breakpoints asked for, and fnBps where they landed, which
	// fnSnap holds a copy of that the statement hook reads without the lock.
	funcs        []debugFunc
	funcIndex    map[*ir.Func]int
	requestedFns []FunctionBreakpointSpec
	fnBps        map[*ir.Func][]fnHook
	fnSnap       atomic.Pointer[map[*ir.Func][]fnHook]

	filters ExceptionFilters
	// skip says a step never ends in a file: the standard library's, for a
	// client debugging just its own code. Nil skips nothing.
	skip  func(file string) bool
	pause bool
	// killed says the session ended: every thread parks at its next
	// statement for good.
	killed bool
	// halted says a thread is stopped: every other parks at its next
	// statement. gen counts resumes, which is what a parked thread waits on.
	halted bool
	gen    uint64
	// threads are the live threads: those with an activation in progress.
	// A thread that finishes leaves the list and keeps its ID, and its rt
	// frame keeps it, so a later activation on that frame rejoins it.
	threads []*thread
	nextID  int
	// frames are the stack frames Stack handed out since the last resume,
	// by ID; targets the step-in targets StepInTargets listed last.
	frames  []frameRef
	targets []*stepTarget

	events chan Stop

	// views are the machine views expressions evaluate on (debugeval.go).
	views sync.Map

	// Time travel (debugtravel.go): the pass running now, the one a step
	// back planned, the log every pass shares, and whether it is off.
	pass    *Pass
	plan    *Pass
	tt      travelLog
	ttOff   atomic.Bool
	started bool
	passes  uint64
	// bpEpoch counts changes to the breakpoints.
	bpEpoch uint64
}

// thread is one goroutine's record.
type thread struct {
	owner *Debugger
	id    int
	// pass is the pass the thread runs in (debugtravel.go); entered counts
	// the activations started from Go on it, which a recorded crossing
	// must not start.
	pass    *Pass
	entered atomic.Int64
	// atHook says the thread is parked at a statement hook rather than
	// inside a statement (a fault, main returning, a diverged crossing),
	// and atStep is its pass's step there.
	atHook bool
	atStep int64
	name   string
	// fixed, when set, names the thread in place of name: a task's spawn
	// site (nameTask).
	fixed string
	// m is the machine its latest activation runs on, which an expression
	// evaluated in one of its frames runs on a view of.
	m atomic.Pointer[Machine]
	// top is the innermost activation in progress; live counts the
	// activations it started from Go that have not finished.
	top  *frame
	live int
	// at, atPos and atPoint are where the thread is parked, or at is nil.
	at      *frame
	atPos   ir.Pos
	atPoint debugPoint
	step    stepState
	// pending is a stop this thread reached while another thread was
	// already stopped: it parks with it, and the next resume reports it
	// instead of running anything.
	pending *Stop
	// calling is the innermost activation of this thread that is inside a
	// call into Go (a host function or adapter), and callingName the
	// function: a thread blocked there (on `Task.await`, `io.read_line`)
	// shows its stack from it while the program is stopped.
	calling     *frame
	callingName string
	// crossings are, for each call into Go in progress, innermost last, how
	// many activations started from Go the thread had when it began: one
	// more since means the call called back into Nomi.
	crossings []int
	// restart is the frame RestartFrame asked this parked thread to restart,
	// which it does as it resumes. restartable says the thread is parked
	// where an activation can be unwound: at a statement or a fault.
	restart     *frame
	restartable bool
	// lastFault is the fault this thread reported last, which a caller's
	// fault hook sees again as the fault leaves the callee.
	lastFault error
}

type stepMode uint8

const (
	stepNone stepMode = iota
	stepIn
	stepOver
	stepOut
	// stepRestart stops at the first statement the thread reaches, the
	// restarted frame's first (RestartFrame).
	stepRestart
	// stepInto stops when the thread enters a step-in target
	// (debugtarget.go), or where a step over would once the statement is
	// done.
	stepInto
)

type stepState struct {
	mode  stepMode
	depth int
	// target is a stepInto's, and seen counts the activations of its
	// functions entered so far.
	target *stepTarget
	seen   int
}

// markLine is one statement mark's line, for resolving a breakpoint.
type markLine struct {
	line int
	file string
	// fn and pos are the function the mark is in and where, whose variables
	// a breakpoint condition on its line is checked against.
	fn  *ir.Func
	pos ir.Pos
}

// frameRef is a StackFrame's referent.
type frameRef struct {
	fr    *frame
	pos   ir.Pos
	point debugPoint
	via   bool
	m     *Machine
	th    *thread
}

// NewDebugger is a debugger with no breakpoints and no exception filters.
func NewDebugger() *Debugger {
	d := &Debugger{modules: map[*ir.Module]bool{}, marks: map[string][]markLine{},
		requested: map[string][]BreakpointSpec{}, bps: map[string]map[int][]func(*Scope) Hit{},
		events: make(chan Stop, 64), pass: &Pass{id: 1}}
	d.cond = sync.NewCond(&d.mu)
	d.refreshLocked()
	return d
}

// WithDebugger makes m a debug machine reporting to d: every function it
// compiles from now on carries statement hooks. Call it before the machine
// runs anything.
func (m *Machine) WithDebugger(d *Debugger) *Machine {
	m.dbg = d
	mods := make([]*ir.Module, 0, len(m.modules))
	for mod := range m.modules {
		mods = append(mods, mod)
	}
	d.AddModules(mods...)
	return m
}

// Events is where each stop is reported, in order.
func (d *Debugger) Events() <-chan Stop { return d.events }

// AddModules makes the statements of mods' functions places a breakpoint can
// land, and resolves the breakpoints already asked for again.
func (d *Debugger) AddModules(mods ...*ir.Module) {
	d.mu.Lock()
	defer d.mu.Unlock()
	seen := map[*ir.Func]bool{}
	var walk func(f *ir.Func)
	walk = func(f *ir.Func) {
		if f == nil || seen[f] {
			return
		}
		seen[f] = true
		for _, b := range f.Blocks() {
			for _, mk := range b.Stmts() {
				file := mk.Pos.File()
				d.marks[file] = append(d.marks[file], markLine{line: mk.Pos.Line(), file: file, fn: f, pos: mk.Pos})
			}
			for _, in := range b.Instrs() {
				if fv, isValue := in.(*ir.FuncValue); isValue {
					// A nested `fn` is nameable; a lambda is not.
					d.addDebugFuncLocked(fv.Body(), "")
					walk(fv.Body())
				}
			}
		}
	}
	for _, mod := range mods {
		if mod == nil || d.modules[mod] {
			continue
		}
		d.modules[mod] = true
		for _, f := range mod.Funcs() {
			d.addDebugFuncLocked(f, "")
			walk(f)
		}
		for _, impl := range mod.Impls() {
			d.addDebugFuncLocked(mod.FuncFor(impl.Func), impl.Method.Name())
		}
		for _, c := range mod.Cells() {
			walk(c.Initializer())
		}
		for _, tc := range mod.Tests() {
			walk(tc.Fn())
		}
	}
	for file := range d.marks {
		ls := d.marks[file]
		sort.Slice(ls, func(i, j int) bool { return ls[i].line < ls[j].line })
	}
	d.resolveLocked()
}

// SetBreakpoints replaces the line breakpoints of file with lines, and
// answers where each landed: on the first line at or after it that starts a
// statement of the same file.
func (d *Debugger) SetBreakpoints(file string, lines []int) []Breakpoint {
	specs := make([]BreakpointSpec, len(lines))
	for i, l := range lines {
		specs[i] = BreakpointSpec{Line: l}
	}
	return d.SetBreakpointSpecs(file, specs)
}

// SetBreakpointSpecs is SetBreakpoints for breakpoints that may decide at
// each pass whether to stop.
func (d *Debugger) SetBreakpointSpecs(file string, specs []BreakpointSpec) []Breakpoint {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requested[file] = append([]BreakpointSpec(nil), specs...)
	if len(specs) == 0 {
		delete(d.requested, file)
	}
	d.bpEpoch++
	d.resolveLocked()
	out := make([]Breakpoint, len(specs))
	for i, s := range specs {
		l := s.Line
		out[i] = Breakpoint{Line: l}
		if _, at, ok := d.landing(file, l); ok {
			out[i].Verified, out[i].Resolved = true, at
		}
	}
	return out
}

// resolveLocked rebuilds bps from requested.
func (d *Debugger) resolveLocked() {
	d.bps = map[string]map[int][]func(*Scope) Hit{}
	for file, specs := range d.requested {
		for _, s := range specs {
			if mf, at, ok := d.landing(file, s.Line); ok {
				if d.bps[mf] == nil {
					d.bps[mf] = map[int][]func(*Scope) Hit{}
				}
				d.bps[mf][at] = append(d.bps[mf][at], s.Hit)
			}
		}
	}
	d.resolveFnsLocked()
	d.refreshLocked()
}

// landing is the mark file and line a breakpoint on file:line lands on.
func (d *Debugger) landing(file string, line int) (string, int, bool) {
	best, bestFile := 0, ""
	for mf, ls := range d.marks {
		if !sameFile(mf, file) {
			continue
		}
		i := sort.Search(len(ls), func(i int) bool { return ls[i].line >= line })
		if i < len(ls) && (best == 0 || ls[i].line < best) {
			best, bestFile = ls[i].line, mf
		}
	}
	return bestFile, best, best != 0
}

// sameFile reports whether a position's file and a client's file name the
// same source: equal once cleaned, or one a relative path the other ends in.
func sameFile(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	if filepath.IsAbs(a) == filepath.IsAbs(b) {
		return false
	}
	if filepath.IsAbs(b) {
		a, b = b, a
	}
	return strings.HasSuffix(a, string(filepath.Separator)+b)
}

// SetExceptionFilters says which exceptions stop the program.
func (d *Debugger) SetExceptionFilters(f ExceptionFilters) {
	d.mu.Lock()
	d.filters = f
	d.mu.Unlock()
}

// Pause stops the program at the next statement any thread reaches.
func (d *Debugger) Pause() {
	d.mu.Lock()
	if d.halted {
		// Already stopped: there is nothing to pause.
		d.mu.Unlock()
		return
	}
	d.pause = true
	d.refreshLocked()
	var blocked *thread
	for _, th := range d.threads {
		if !th.blockedInGo() {
			blocked = nil
			break
		}
		if blocked == nil || th.id < blocked.id {
			blocked = th
		}
	}
	if blocked == nil {
		d.mu.Unlock()
		return
	}
	// Every thread is inside a call into Go (waiting for input, a channel,
	// Task.await) and reaches no statement until it returns: the program
	// stops now, each thread shown at its call. A call that returns while
	// the program is stopped parks its thread at its next statement.
	d.pause, d.halted = false, true
	for _, th := range d.threads {
		th.step = stepState{}
	}
	d.frames = nil
	d.refreshLocked()
	pos, _ := callerAt(blocked.calling)
	d.mu.Unlock()
	d.events <- Stop{Reason: StopPause, Thread: blocked.id, Pos: pos}
}

// Kill ends the program for a client that is going away: every thread parks
// at its next statement and never resumes, a stopped one included, and no
// stop is reported again. A thread inside a call into Go parks when the call
// returns.
func (d *Debugger) Kill() {
	d.mu.Lock()
	d.killed = true
	d.refreshLocked()
	d.mu.Unlock()
}

// Continue resumes every thread.
func (d *Debugger) Continue() { d.resume(0, stepNone) }

// StepIn resumes every thread, and stops thread at the next statement it
// reaches, in any activation.
func (d *Debugger) StepIn(thread int) { d.resume(thread, stepIn) }

// StepOver resumes every thread, and stops thread at the next statement it
// reaches in the same activation or one that called it. A pipeline is one
// statement: its stages are passed, though a stage's callbacks still stop a
// step into them.
func (d *Debugger) StepOver(thread int) { d.resume(thread, stepOver) }

// StepOut resumes every thread, and stops thread at the next statement it
// reaches in an activation that called the current one.
func (d *Debugger) StepOut(thread int) { d.resume(thread, stepOut) }

func (d *Debugger) resume(id int, mode stepMode) { d.resumeWith(id, mode, nil) }

// resumeTarget resumes every thread, and steps thread into target.
func (d *Debugger) resumeTarget(id int, target *stepTarget) { d.resumeWith(id, stepInto, target) }

func (d *Debugger) resumeWith(id int, mode stepMode, target *stepTarget) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.killed || d.reportPendingLocked() {
		return
	}
	for _, th := range d.threads {
		th.step = stepState{}
		if th.id == id && th.at != nil && mode != stepNone {
			th.step = stepState{mode: mode, depth: th.at.ldepth, target: target}
		}
	}
	d.halted = false
	d.frames = nil
	d.gen++
	d.refreshLocked()
	d.cond.Broadcast()
}

// reportPendingLocked reports a stop a thread reached while the program was
// already stopped, if one is waiting, and keeps every thread parked: the
// resume that asked to run becomes that stop.
func (d *Debugger) reportPendingLocked() bool {
	var next *thread
	for _, th := range d.threads {
		if th.pending != nil && th.at != nil && (next == nil || th.id < next.id) {
			next = th
		}
	}
	if next == nil {
		return false
	}
	stop := *next.pending
	next.pending = nil
	for _, th := range d.threads {
		th.step = stepState{}
	}
	d.frames = nil
	d.mu.Unlock()
	d.events <- stop
	d.mu.Lock()
	return true
}

// SetSkipFiles makes a step never end in a file skip reports, so stepping
// passes through it to the next statement outside it. A breakpoint or an
// exception there still stops. Nil skips nothing.
func (d *Debugger) SetSkipFiles(skip func(file string) bool) {
	d.mu.Lock()
	d.skip = skip
	d.mu.Unlock()
}

// refreshLocked recomputes whether a statement hook has anything to check.
func (d *Debugger) refreshLocked() {
	slow := d.pause || d.halted || d.killed
	for _, th := range d.threads {
		slow = slow || th.step.mode != stepNone
	}
	bps := map[string]map[int]bool{}
	for file, lines := range d.bps {
		bps[file] = map[int]bool{}
		for l := range lines {
			bps[file][l] = true
		}
	}
	d.bpSnap.Store(&bps)
	d.slow.Store(slow)
	// While time travel is on, every statement is counted (debugtravel.go).
	d.active.Store(slow || len(d.bps) > 0 || len(d.fnBps) > 0 || !d.ttOff.Load())
}

// Threads are the live threads, by ID.
func (d *Debugger) Threads() []Thread {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Thread
	for _, th := range d.threads {
		name := th.name
		if th.fixed != "" {
			name = th.fixed
		}
		out = append(out, Thread{ID: th.id, Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Stack is the logical call stack of a stopped thread, innermost first.
func (d *Debugger) Stack(id int) ([]StackFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	th := d.threadByID(id)
	if th == nil || (th.at == nil && (!d.halted || th.calling == nil)) {
		return nil, errors.New("vm: thread " + strconv.Itoa(id) + " is not stopped")
	}
	var out []StackFrame
	add := func(ref frameRef, sf StackFrame) {
		sf.ID = len(d.frames) + 1
		d.frames = append(d.frames, ref)
		out = append(out, sf)
	}
	top := th.at
	pos, point := th.atPos, th.atPoint
	if top == nil {
		// Blocked in a call into Go while another thread is stopped: the
		// call is the innermost frame, at the activation that made it.
		top = th.calling
		pos, point = callerAt(top)
		add(frameRef{pos: pos, via: true}, StackFrame{Name: th.callingName, Pos: pos, Via: true})
	}
	for fr := top; fr != nil; fr = fr.parent {
		add(frameRef{fr: fr, pos: pos, point: point, m: th.m.Load(), th: th},
			StackFrame{Name: frameName(fr.fn), Pos: pos, Tail: fr.tail})
		if fr.parent == nil {
			break
		}
		pos, point = callerAt(fr.parent)
		if fr.via != "" {
			add(frameRef{pos: pos, via: true}, StackFrame{Name: fr.via, Pos: pos, Via: true})
		}
	}
	return out, nil
}

// callerAt is where a calling activation is: the call it saved its pc at.
func callerAt(fr *frame) (ir.Pos, debugPoint) {
	c := fr.c
	if c == nil || c.dbg == nil {
		return fr.fn.Pos(), debugPoint{}
	}
	pt, ok := c.dbg.pointAt(fr.pc)
	if st := c.siteAt(fr.pc + 1); ok && st != nil && opcode(c.code[fr.pc]&0xff) == opPC {
		return st.pos, pt
	}
	if st := c.siteAt(fr.pc); st != nil {
		return st.pos, pt
	}
	return fr.fn.Pos(), pt
}

// frameName is how a stack frame names its function: a lambda is
// `<lambda>`, anything else its declared name.
func frameName(f *ir.Func) string {
	switch f.Name() {
	case "lambda":
		return "<lambda>"
	case "concurrent":
		return "<concurrent>"
	}
	return f.Name()
}

// Locals are the variables visible in a frame Stack handed out: each name
// bound at the frame's position, its innermost binding when one shadows
// another, in the order they were bound.
func (d *Debugger) Locals(frameID int) ([]Variable, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if frameID < 1 || frameID > len(d.frames) {
		return nil, errors.New("vm: no frame " + strconv.Itoa(frameID) + "; the program resumed")
	}
	ref := d.frames[frameID-1]
	if ref.via || ref.fr.c == nil || ref.fr.c.dbg == nil {
		return nil, nil
	}
	var out []Variable
	for _, v := range visibleVars(ref.fr, ref.pos, ref.point) {
		out = append(out, Variable{Name: v.Name, Type: v.Type, Value: rt.Inspect(v.Value), Decl: v.Decl})
	}
	return out, nil
}

// visibleVars are the variables of fr visible at pos, which compiles point:
// each name's innermost binding, in the order they were bound, with values.
func visibleVars(fr *frame, pos ir.Pos, point debugPoint) []ScopeVar {
	f := fr.c.fn
	defined := fr.c.dbg.defined(f)
	visible := map[string]ir.Var{}
	for _, v := range f.Vars() {
		if !v.Visible(pos.Line(), pos.Col()) || !defined.Before(point.block, point.index, v.Temp) {
			continue
		}
		if prev, has := visible[v.Name]; has && !startsAfter(v.Scope, prev.Scope) {
			continue
		}
		visible[v.Name] = v
	}
	vars := make([]ir.Var, 0, len(visible))
	for _, v := range visible {
		vars = append(vars, v)
	}
	sort.Slice(vars, func(i, j int) bool {
		if startsAfter(vars[j].Scope, vars[i].Scope) {
			return true
		}
		return !startsAfter(vars[i].Scope, vars[j].Scope) && vars[i].Name < vars[j].Name
	})
	out := make([]ScopeVar, 0, len(vars))
	for _, v := range vars {
		val, err := fr.read(v.Temp)
		if err != nil {
			continue
		}
		out = append(out, ScopeVar{Name: v.Name, Type: v.Type, Value: val, Decl: v.Decl})
	}
	return out
}

// startsAfter reports whether scope a starts after scope b.
func startsAfter(a, b ir.Pos) bool {
	return a.Line() > b.Line() || (a.Line() == b.Line() && a.Col() > b.Col())
}

func (d *Debugger) threadByID(id int) *thread {
	for _, th := range d.threads {
		if th.id == id {
			return th
		}
	}
	return nil
}

// --- the machine's side -----------------------------------------------------

// codeDebug is what a function compiled for a debugger carries beside its
// bytecode.
type codeDebug struct {
	// stmts are the statement marks, by opStmt's operand.
	stmts []stmtSite
	// points are the graph position of every hook and instruction, by pc.
	points []debugPoint
	// def is the function's must-defined analysis, computed when a frame's
	// locals are first read.
	defOnce sync.Once
	def     *ir.Defined
	// stageTemps are the temporaries holding a pipeline stage's value,
	// which an opTap follows the write of (debugpipe.go).
	stageTemps map[ir.Temp]bool
}

// stmtSite is one statement mark.
type stmtSite struct {
	pos   ir.Pos
	kind  ir.StmtKind
	point debugPoint
	// lineStart says no mark of the same function starts before this one on
	// its line: a line breakpoint stops here, once per pass over the line,
	// and not again at a later statement on it (an `else` arm written on
	// the `if`'s line).
	lineStart bool
	// skip caches whether a step never ends here (Debugger.skipped): 0
	// unknown, 1 no, 2 yes.
	skip uint8
}

// markLineStarts sets each mark's lineStart.
func (cd *codeDebug) markLineStarts() {
	first := map[string]map[int]int{}
	for _, st := range cd.stmts {
		file, line := st.pos.File(), st.pos.Line()
		if first[file] == nil {
			first[file] = map[int]int{}
		}
		if col, seen := first[file][line]; !seen || st.pos.Col() < col {
			first[file][line] = st.pos.Col()
		}
	}
	for i := range cd.stmts {
		st := &cd.stmts[i]
		st.lineStart = first[st.pos.File()][st.pos.Line()] == st.pos.Col()
	}
}

// debugPoint is a pc and the graph position it compiles: instruction index
// of block (the instruction count is the terminator).
type debugPoint struct {
	pc    int
	block ir.BlockID
	index int
}

func (cd *codeDebug) defined(f *ir.Func) *ir.Defined {
	cd.defOnce.Do(func() { cd.def = ir.DefinedIn(f) })
	return cd.def
}

// pointAt is the graph position of the hook or instruction at pc.
func (cd *codeDebug) pointAt(pc int) (debugPoint, bool) {
	i := sort.Search(len(cd.points), func(i int) bool { return cd.points[i].pc > pc })
	if i == 0 || cd.points[i-1].pc != pc {
		return debugPoint{}, false
	}
	return cd.points[i-1], true
}

// debugBefore emits the hooks before instruction i of b (the terminator when
// in is nil): an opStmt for each of marks that names i, and an opPC when in
// may call back into Nomi. It answers the marks still ahead.
func (cp *compiler) debugBefore(b *ir.Block, i int, in ir.Instr, marks []ir.StmtMark) []ir.StmtMark {
	cd := cp.c.dbg
	pt := func() debugPoint {
		p := debugPoint{pc: len(cp.c.code), block: b.ID(), index: i}
		cd.points = append(cd.points, p)
		return p
	}
	for len(marks) > 0 && marks[0].Index <= i {
		mk := marks[0]
		marks = marks[1:]
		cp.at(mk.Pos, false, -1)
		cd.stmts = append(cd.stmts, stmtSite{pos: mk.Pos, kind: mk.Kind, point: pt()})
		cp.emit(cp.op(opStmt, uint32(len(cd.stmts)-1)))
	}
	if in != nil {
		if debugMayCall(in) {
			pt()
			cp.at(in.Pos(), false, -1)
			cp.emit(cp.op(opPC, 0))
		}
		pt()
	}
	return marks
}

// debugMayCall reports whether an instruction may start a Nomi activation,
// which a caller's frame shows by the pc it saved before it.
func debugMayCall(in ir.Instr) bool {
	switch n := in.(type) {
	case *ir.Const, *ir.Copy, *ir.Bind, *ir.Arith, *ir.Not, *ir.Concat, *ir.Slot:
		return false
	case *ir.Ref:
		return n.Kind() == ir.RefOnce
	}
	return true
}

// debugActivation runs an activation activateSlot opened, linking it into
// its thread: a pooled stack belongs to the goroutine calling from Go, whose
// thread the debugger looks up.
func (m *Machine) debugActivation(fr *frame, pooled bool) error {
	stk := fr.stk
	via := ""
	if pooled {
		th := m.dbg.enter(fr.runtime)
		stk.thread = th
		via = m.dvia
		defer func() {
			stk.thread = nil
			m.dbg.leave(th)
		}()
	}
	return m.execDebug(fr, via)
}

// execDebug is exec on a debug machine.
func (m *Machine) execDebug(fr *frame, via string) error {
	th := fr.stk.thread
	if th == nil {
		// A stack whose first activation did not come through
		// activateSlot. Nothing produces one; it gets a thread anyway.
		th = m.dbg.enter(fr.runtime)
		fr.stk.thread = th
		defer func() {
			fr.stk.thread = nil
			m.dbg.leave(th)
		}()
	}
	fr.parent, fr.via, fr.tail, fr.pc, fr.taps, fr.entered = th.top, via, false, 0, nil, true
	th.m.Store(m)
	fr.ldepth = 1
	if fr.parent != nil {
		fr.ldepth = fr.parent.ldepth + 1
	}
	if th.name == "" {
		th.name = frameName(fr.fn)
	}
	th.top = fr
	defer func() { th.top = fr.parent }()
	var err error
	if fr.c.guard {
		err = m.execGuarded(fr)
	} else {
		err = m.loop(fr, 0)
		if err == errNeedsGuard {
			err = m.execGuarded(fr)
		}
	}
	if err == nil && fr.parent == nil && !fr.testBody && fr.fn.Name() == "main" && len(fr.fn.Params()) == 0 {
		m.dbg.mainReturned(fr)
	}
	fr.stk.pop(fr)
	return err
}

// enter is the thread of an activation started from Go on runtime, counting
// one more such activation on it. A frame that carries no thread of this
// debugger's starts one.
func (d *Debugger) enter(runtime *rt.Frame) *thread {
	d.mu.Lock()
	defer d.mu.Unlock()
	var th *thread
	if runtime != nil {
		th, _ = runtime.DebugThread().(*thread)
	}
	if th == nil || th.owner != d {
		th = d.newThreadLocked()
		if runtime != nil {
			runtime.SetDebugThread(th)
		}
	}
	if th.live == 0 {
		d.threads = append(d.threads, th)
	}
	th.live++
	th.entered.Add(1)
	return th
}

// nameTask gives a task's frame a thread of its own before the task's body
// starts, named by where the task was spawned: `task at worker.nomi:12`.
func (d *Debugger) nameTask(runtime *rt.Frame, spawn ir.Pos) {
	name := "task"
	if spawn.IsValid() && spawn.File() != "" {
		name = "task at " + filepath.Base(spawn.File()) + ":" + strconv.Itoa(spawn.Line())
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	th, _ := runtime.DebugThread().(*thread)
	if th == nil || th.owner != d || th.live > 0 {
		th = d.newThreadLocked()
		runtime.SetDebugThread(th)
	}
	th.fixed = name
}

func (d *Debugger) newThreadLocked() *thread {
	d.nextID++
	return &thread{owner: d, id: d.nextID, pass: d.pass}
}

// adopt gives a root frame a thread before anything derives from it, so a
// program's boot and its main run on one thread.
func (d *Debugger) adopt(runtime *rt.Frame) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if th, _ := runtime.DebugThread().(*thread); th == nil || th.owner != d {
		runtime.SetDebugThread(d.newThreadLocked())
	}
}

func (d *Debugger) leave(th *thread) {
	d.mu.Lock()
	th.live--
	if th.live == 0 {
		th.name = ""
		th.lastFault = nil
		th.calling, th.callingName = nil, ""
		th.crossings = nil
		th.step = stepState{}
		for i, live := range d.threads {
			if live == th {
				d.threads = append(d.threads[:i], d.threads[i+1:]...)
				break
			}
		}
		d.refreshLocked()
	}
	d.mu.Unlock()
}

// crossIn records that fr's thread is calling into Go through name, and
// answers what crossOut restores.
func (d *Debugger) crossIn(fr *frame, name string) (*thread, *frame, string) {
	th := fr.stk.thread
	if th == nil {
		return nil, nil, ""
	}
	d.mu.Lock()
	prev, prevName := th.calling, th.callingName
	th.calling, th.callingName = fr, name
	th.crossings = append(th.crossings, th.live)
	d.mu.Unlock()
	return th, prev, prevName
}

// crossOut ends a call crossIn recorded.
func (d *Debugger) crossOut(th *thread, prev *frame, prevName string) {
	if th == nil {
		return
	}
	d.mu.Lock()
	th.calling, th.callingName = prev, prevName
	if n := len(th.crossings); n > 0 {
		th.crossings = th.crossings[:n-1]
	}
	d.mu.Unlock()
}

// blockedInGo reports whether th is inside a call into Go that has not
// called back into Nomi: it reaches no statement until the call returns.
func (th *thread) blockedInGo() bool {
	n := len(th.crossings)
	return th.at == nil && th.calling != nil && n > 0 && th.crossings[n-1] == th.live
}

// debugCallAdapter is opHost's adapter call on a debug machine, recorded as
// a crossing.
func (m *Machine) debugCallAdapter(fr *frame, h *hostCall, args []any) (any, error) {
	return m.dbg.cross(fr, h.call.Callee().Name(), func() (any, error) { return callAdapter(h.fn, fr.runtime, args) })
}

// stmt is the statement hook: fr is at statement mark i. It answers an
// error only to restart a frame (RestartFrame), which the loop raises as a
// fault the activations above the restarted one leave by.
func (d *Debugger) stmt(fr *frame, i int) error {
	site := &fr.c.dbg.stmts[i]
	th := fr.stk.thread
	if th == nil {
		return nil
	}
	// A function breakpoint stops at the first statement of an activation,
	// and a step into a target there.
	entered := fr.entered
	fhooks := d.entryHooks(fr)
	var p *Pass
	var n int64
	if th.pass != nil && !d.ttOff.Load() {
		p = th.pass
		if p.abandoned.Load() {
			return errAbandon
		}
		if done, err := d.travelStmt(th, p, fr, site, fhooks); done {
			return err
		}
		n = p.steps.Load()
	}
	if !d.slow.Load() && len(fhooks) == 0 {
		if bps := d.bpSnap.Load(); bps == nil || !site.lineStart || !(*bps)[site.pos.File()][site.pos.Line()] {
			if p != nil {
				// Replaying forward through recorded history, a restart
				// recorded here happens again.
				return d.replayRestart(th, fr, n, false)
			}
			return nil
		}
	}
	d.mu.Lock()
	for d.killed {
		d.cond.Wait()
	}
	var hooks []func(*Scope) Hit
	if site.lineStart {
		// Decided while another thread is stopped too: a breakpoint this
		// thread reaches then parks with it as its pending stop.
		hooks = d.bps[site.pos.File()][site.pos.Line()]
	}
	stop := Stop{Pos: site.pos}
	if (len(hooks) > 0 || len(fhooks) > 0) && !(d.pause && !d.halted) {
		// A breakpoint that decides at each pass decides now, with the
		// lock released: its condition runs Nomi code on a view of the
		// machine, and other threads keep running meanwhile.
		d.mu.Unlock()
		hit, lines, fns := d.decide(hooks, fhooks, fr, site)
		d.mu.Lock()
		for d.killed {
			d.cond.Wait()
		}
		if hit.Stop {
			stop.Reason, stop.Text, stop.Functions, stop.NotLines = StopBreakpoint, hit.Text, fns, !lines
			if p != nil {
				p.noteHit(n, site.pos)
			}
		}
	}
	switch {
	case d.pause && !d.halted:
		d.pause = false
		stop.Reason, stop.Text = StopPause, ""
	case stop.Reason != "":
	case th.step.mode == stepRestart:
		stop.Reason = StopRestart
	case th.step.mode == stepInto:
		if done, note := d.targetStop(th, fr, site.kind, entered); done {
			stop.Reason, stop.Text = StopStep, note
		}
	case th.step.mode != stepNone && d.stepDone(th, fr, site.kind) &&
		(d.skip == nil || !d.skip(site.pos.File())):
		stop.Reason = StopStep
	}
	if stop.Reason == "" && !d.halted {
		d.mu.Unlock()
		if p != nil {
			return d.replayRestart(th, fr, n, false)
		}
		return nil
	}
	th.restartable = true
	th.atHook = true
	d.park(th, fr, site.pos, site.point, stop)
	if p != nil && p.abandoned.Load() {
		d.mu.Unlock()
		return errAbandon
	}
	if p != nil && th.restart == nil {
		th.restart = restartTarget(d.replayRestart(th, fr, n, false))
	}
	err := th.takeRestart()
	d.mu.Unlock()
	return err
}

// restartTarget is the frame an unwinding restarts, or nil.
func restartTarget(err error) *frame {
	if ru, isRestart := err.(*restartUnwind); isRestart {
		return ru.target
	}
	return nil
}

// decide asks a statement's line breakpoints, and the function breakpoints
// on its activation's entry, whether the thread stops: it stops when one of
// them has no decision of its own, or one decides to. It answers whether a
// line breakpoint stopped it, and which function breakpoints did.
func (d *Debugger) decide(hooks []func(*Scope) Hit, fhooks []fnHook, fr *frame, site *stmtSite) (Hit, bool, []int) {
	var scope *Scope
	out := Hit{}
	ask := func(h func(*Scope) Hit) bool {
		if h == nil {
			return true
		}
		if scope == nil {
			scope = d.scopeOf(fr, site, fr.stk.thread.m.Load())
		}
		hit := h(scope)
		if hit.Stop && out.Text == "" {
			out.Text = hit.Text
		}
		return hit.Stop
	}
	lines := false
	for _, h := range hooks {
		if ask(h) {
			out.Stop, lines = true, true
		}
	}
	var fns []int
	for _, fh := range fhooks {
		if ask(fh.hit) {
			out.Stop = true
			fns = append(fns, fh.index)
		}
	}
	return out, lines, fns
}

// stepDone reports whether th's step ends at a statement of kind in fr.
func (d *Debugger) stepDone(th *thread, fr *frame, kind ir.StmtKind) bool {
	switch th.step.mode {
	case stepIn:
		return true
	case stepOver:
		return fr.ldepth <= th.step.depth && kind != ir.StmtStage
	case stepOut:
		return fr.ldepth < th.step.depth && kind != ir.StmtStage
	}
	return false
}

// park stops th at fr until the next resume, reporting stop unless it has no
// reason (a thread parking because another stopped). Called with d.mu held,
// and answers with it held.
func (d *Debugger) park(th *thread, fr *frame, pos ir.Pos, point debugPoint, stop Stop) {
	th.at, th.atPos, th.atPoint = fr, pos, point
	if th.pass != nil {
		th.atStep = th.pass.steps.Load()
	}
	for d.killed {
		d.cond.Wait()
	}
	gen := d.gen
	if stop.Reason != "" && d.halted {
		// Another thread stopped first. This one parks with its stop, which
		// the next resume reports.
		stop.Thread = th.id
		pending := stop
		th.pending = &pending
		stop.Reason = ""
	}
	if stop.Reason != "" {
		d.halted = true
		for _, other := range d.threads {
			other.step = stepState{}
		}
		d.frames = nil
		d.refreshLocked()
		stop.Thread = th.id
		d.mu.Unlock()
		d.events <- stop
		d.mu.Lock()
	}
	for d.gen == gen {
		d.cond.Wait()
	}
	th.at, th.pending = nil, nil
}

// fault is the exception hook: the instruction at pc of fr failed with err.
// A fault that left a callee arrives again at each caller, so a thread
// stops for one only where it started.
func (d *Debugger) fault(m *Machine, fr *frame, pc int, err error) error {
	th := fr.stk.thread
	if th == nil {
		return nil
	}
	c := fr.c
	st := c.siteAt(pc)
	if st == nil || c.dbg == nil {
		return nil
	}
	stop := Stop{Pos: st.pos}
	switch {
	case th.lastFault != nil && errors.Is(err, th.lastFault):
		return nil
	case isAssertAt(c, pc):
		var failure *rt.AssertionFailure
		if a, isAssertion := asAssertFailure(err); isAssertion {
			failure = a
		} else if tr, isTry := err.(*tryReturn); isTry {
			if e, isRec := enumRecord(tr.value); isRec {
				if payload, has := payloadOf(e); has {
					if f, isFailure := nomiFailureOf(payload); isFailure {
						failure = f.Report()
					}
				}
			}
		}
		if failure == nil {
			return nil
		}
		stop.Reason, stop.Assertion, stop.Text = StopAssertion, failure, rt.FormatAssertionFailure(failure)
	default:
		if _, isTry := err.(*tryReturn); isTry {
			return nil
		}
		positioned := positionCallDepth(err, st.pos)
		_, classified := asFault(err)
		if positioned == err && (!st.faults || classified) && !isRtFault(err) {
			return nil
		}
		if strings.HasPrefix(err.Error(), "vm: ") {
			// A machine limit, not the program's fault.
			return nil
		}
		stop.Reason, stop.Text = StopTrap, positioned.Error()
	}
	th.lastFault = err
	if stop.Reason == StopTrap {
		var e *rt.Error
		if errors.As(err, &e) && e.Awaited {
			stop.Reason = StopTaskFailure
		}
	}
	p := th.pass
	if p != nil && d.ttOff.Load() {
		p = nil
	}
	if p != nil && p.seek != 0 {
		// A replay running to its target passes exceptions by, restarting
		// a frame where the recording did.
		return d.replayRestart(th, fr, p.steps.Load(), true)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if (stop.Reason == StopTrap && !d.filters.Traps) || (stop.Reason == StopAssertion && !d.filters.Assertions) ||
		(stop.Reason == StopTaskFailure && !d.filters.TaskFailures) {
		return nil
	}
	fr.pc = pc
	point, _ := c.dbg.pointAt(pc)
	th.restartable = true
	th.atHook = false
	d.park(th, fr, st.pos, point, stop)
	if p != nil && p.abandoned.Load() {
		return errAbandon
	}
	if p != nil && th.restart == nil {
		th.restart = restartTarget(d.replayRestart(th, fr, p.steps.Load(), true))
	}
	return th.takeRestart()
}

// isRtFault reports whether err is an rt trap that left rt as a Fault where
// it started: rt's kernels panic, and the machine turns the panic into a
// Fault at the instruction that called them.
func isRtFault(err error) bool {
	f, ok := asFault(err)
	if !ok {
		return false
	}
	var e *rt.Error
	return errors.As(f.err, &e) && f.err == error(e)
}

// isAssertAt reports whether the instruction at pc is an assertion.
func isAssertAt(c *code, pc int) bool {
	if pc >= len(c.code) || opcode(c.code[pc]&0xff) != opIR {
		return false
	}
	_, is := c.irs[c.code[pc]>>8].(*ir.Assert)
	return is
}

// mainReturned is the hook at the end of `main`'s activation, which is
// still live: a main returning `Err` stops when the filter asks.
func (d *Debugger) mainReturned(fr *frame) {
	th := fr.stk.thread
	e, isRec := enumRecord(fr.stk.ret())
	if th == nil || !isRec || !isEnum(e, "results.Result") || variantName(e) != "Err" {
		return
	}
	payload, has := payloadOf(e)
	if !has {
		return
	}
	if _, isFailure := nomiFailureOf(payload); isFailure {
		// A failed assertion in main; it stopped at the assertion.
		return
	}
	if p := th.pass; p != nil && p.seek != 0 && !d.ttOff.Load() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.filters.MainErrors {
		return
	}
	pos, point := callerAt(fr)
	th.restartable = false
	th.atHook = false
	d.park(th, fr, pos, point, Stop{Reason: StopMainError, Pos: pos, Text: rt.RowText(payload)})
}

// debugEveryMachine makes every machine NewProgram opens a debug machine,
// with a debugger of its own that has no breakpoints and no filters. A test
// that requires a debug machine to run a program exactly as a plain one does
// sets it (DebugEveryMachine).
var debugEveryMachine atomic.Bool

// DebugEveryMachine makes every machine opened from now on a debug machine
// with an idle debugger, and answers the function that turns it off. For
// tests: it changes no program's behaviour, which is what they check.
func DebugEveryMachine() (restore func()) {
	debugEveryMachine.Store(true)
	return func() { debugEveryMachine.Store(false) }
}
