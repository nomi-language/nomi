package vm

// TIME TRAVEL: step back and reverse continue, by recording every effect and
// replaying the program from its start (decision 4 of the debugger plan).
//
// # Steps
//
// A debug machine counts the statement marks (opStmt) the program executes:
// step N is the Nth statement, arm, iteration or pipe stage it reached since
// it started, on any thread. A program with one task executes them in one
// order every time it runs with the same answers from outside, so step N
// names one point of its history. Evaluations (watches, conditions) run on
// views without statement hooks and count nothing.
//
// # Recording
//
// Every crossing out of the machine that a replay cannot compute again is
// recorded in the Debugger's log, in order: its name, the step it happened
// in, and its result, a value or a fault. That is every host adapter and
// intrinsic except the ones that compute their answer from their operands
// (pureCrossings), the ones over the machine's own state (`Context.value`,
// `io.capture`, which call back into Nomi, and an `io.read_line` that an
// `io.capture` or `io.replay` answers from its own input), and output (`io.print`,
// `io.write`, `io.inspect`, `dbg`), which is recorded only so a replay can
// check it makes the same call, and runs again against a writer that drops
// it (Pass.Gate). Input, the clock, randomness, timers, files, the
// environment and std/compiler's hosts are answered from the log. A value is
// kept by reference: values are immutable, so a recorded file's contents is
// the one string the program read. An entry costs about 56 bytes beside its
// value. The log holds at most travelLimit entries; a program that crosses
// more turns time travel off, saying why.
//
// # Passes
//
// One run of the program from its start is a Pass. Going back abandons the
// current pass (its thread unwinds by errAbandon, running nothing more and
// crossing nothing) and plans the next one, which the caller starts on a
// fresh machine (StartPass, then vmhost's Run or Test again). The new pass
// replays: each crossing is checked against the next log entry, which must
// name the same function at the same step, and is answered from it; output
// is dropped. It seeks a target step, passing breakpoints, exceptions and
// pauses by, and stops there. Once it has executed every recorded crossing
// and reached the furthest step any pass ran live (the frontier), it is
// live: crossings are real again and are recorded after the log's end, so
// stepping back and then forward past the present runs on into new history.
//
// A crossing that does not match its log entry is an internal error: the
// thread stops with StopDiverged and the reason, time travel turns off, and
// the program continues live from there.
//
// # Where going back goes
//
// Step back is reverse next: it goes to the latest earlier statement a
// forward next from it could have stopped at, one whose activation is at the
// same depth or shallower (a pipe stage, and a file SetSkipFiles skips, are
// not), so it passes over calls. From the first statement of a function it
// goes to the caller's statement that made the call. From a fault, or main
// returning, it goes to the start of the statement it happened in. Each pass
// keeps, for every depth, the latest such statement (Pass.last), so the
// target is known when going back starts and one replay reaches it.
//
// Reverse continue goes to the latest earlier statement where a breakpoint
// decided to stop, its condition and hit count evaluated as on the pass that
// ran it (a hit count counts from the program's start in every pass), or to
// the program's first statement when there is none. A pass tracks the
// latest such hit as it runs; when breakpoints changed since the pass
// started, a first replay (a scan) runs to the present to find it, and a
// second goes there.
//
// # What a step back costs
//
// A replay re-runs every statement before its target: going back from step N
// to step T executes T statements and answers each recorded crossing from
// memory (no input is read, no clock waited on), recompiling each function
// the replay reaches on the fresh machine. A reverse continue after a
// breakpoint change executes N statements more for the scan. Nothing else is
// kept: no checkpoints, and memory beyond the log is one entry per call
// depth.
//
// # Restart frame
//
// A restart is history too: it is recorded at the step the thread was
// stopped at, and a replay restarts the same frame (by its depth) at that
// step. A restart while replaying changes what happens next, so the log is
// truncated there, the restarts recorded after it are dropped, and the pass
// goes live.
//
// # One task
//
// A program that starts a task (`Task.spawn`, `concurrent`, a supervisor) or
// makes a channel is not deterministic under replay. Time travel turns off
// for good at the first such crossing; the log is dropped, and StepBack and
// ReverseContinue answer why.

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ir"
)

// travelLimit is how many crossings a log holds before time travel turns
// off.
const travelLimit = 1_000_000

// errAbandon unwinds the threads of an abandoned pass. Nothing handles it:
// no fault handler, no deferred call, no exception stop.
var errAbandon = errors.New("vm: abandoning this pass over the program to replay it")

// ErrNoHistory is StepBack's and ReverseContinue's answer at the first
// statement the program ran.
var ErrNoHistory = errors.New("there is no history before this statement: it is the first the program ran")

// StopStart is a reverse continue that found no breakpoint and went back to
// the program's first statement.
const StopStart StopReason = "start"

// StopDiverged is a replay that did not make the crossing its log recorded:
// an internal error, with the reason as the stop's text.
const StopDiverged StopReason = "replay diverged"

// Crossing is one recorded crossing out of the machine.
type Crossing struct {
	// Step is the statement the crossing happened in.
	Step int64
	// Name is the function crossed into, as the IR names it.
	Name string
	// Value and Err are its result.
	Value any
	Err   error
}

// restartEvent is one recorded RestartFrame: at step, at a statement or at a
// fault, the activation at depth restarted.
type restartEvent struct {
	step    int64
	atFault bool
	depth   int
}

// mark is a statement a pass executed: its step and position.
type mark struct {
	step int64
	pos  ir.Pos
}

// Pass is one run of the program from its start: the first, live one, or a
// replay a step back or a reverse continue planned.
type Pass struct {
	id        uint64
	abandoned atomic.Bool
	// steps counts the statements this pass executed.
	steps atomic.Int64
	// replaying says the pass is still in recorded history: crossings are
	// answered from the log and output is dropped.
	replaying atomic.Bool
	// pos is the next log entry a replayed crossing answers from; frontier
	// is the furthest step a pass ran live, past which the pass goes live
	// once every entry is used.
	pos      int
	frontier int64
	// nextRestart is the next recorded restart a replay performs.
	nextRestart int
	// seek is the step the pass is running to without stopping, 0 when
	// none; seekPos is where that statement is, which the replay checks.
	seek       int64
	seekPos    ir.Pos
	seekReason StopReason
	// scan says the seek is a reverse continue's first replay: at the
	// target it plans the second instead of stopping. scanInclusive says
	// the position the reverse continue started from is inside the target
	// statement (a fault), so a hit there counts.
	scan          bool
	scanInclusive bool
	// epoch is the breakpoint set's version when the pass started: hit is
	// the latest breakpoint stop under that set.
	epoch uint64
	// cur is the statement the pass executed last; last is, for each call
	// depth, the latest statement there a step could stop at, and prev and
	// prevDepth what cur overwrote in it.
	cur       mark
	last      []mark
	prev      mark
	prevDepth int
	// first is the first statement a step could stop at.
	first mark
	// hit is the latest statement where a breakpoint decided to stop, and
	// hitPrev the one before it.
	hit, hitPrev mark
	// answered counts the crossings this pass answered from the log.
	answered int64
}

// Steps is how many statements the pass executed.
func (p *Pass) Steps() int64 { return p.steps.Load() }

// Abandoned reports whether a step back or reverse continue abandoned the
// pass for another.
func (p *Pass) Abandoned() bool { return p.abandoned.Load() }

// Replaying reports whether the pass is still in recorded history.
func (p *Pass) Replaying() bool { return p.replaying.Load() }

// Gate is w for the program's output on this pass: what the pass writes
// while it replays recorded history, or after it was abandoned, is dropped,
// since the pass that ran that history first already wrote it.
func (p *Pass) Gate(w io.Writer) io.Writer { return &gateWriter{p: p, w: w} }

type gateWriter struct {
	p *Pass
	w io.Writer
}

func (g *gateWriter) Write(b []byte) (int, error) {
	if g.p.abandoned.Load() || g.p.replaying.Load() {
		return len(b), nil
	}
	return g.w.Write(b)
}

// travelLog is what every pass shares: the recorded crossings and restarts.
// Guarded by Debugger.mu.
type travelLog struct {
	// off says why time travel is off, "" while it is on.
	off      string
	entries  []Crossing
	restarts []restartEvent
}

// TravelStats count what time travel did, for a test that measures a step
// back's cost by counting.
type TravelStats struct {
	// Passes is how many passes ran, the first included.
	Passes int
	// Recorded is the log's length; Answered how many crossings the
	// current pass answered from it.
	Recorded int
	Answered int64
	// Steps is how many statements the current pass executed.
	Steps int64
}

// Stats are the debugger's time travel counts.
func (d *Debugger) Stats() TravelStats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return TravelStats{Passes: int(d.passes), Recorded: len(d.tt.entries), Answered: d.pass.answered,
		Steps: d.pass.steps.Load()}
}

// TravelOff is why step back is off for this program, or "".
func (d *Debugger) TravelOff() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tt.off
}

// Recording is a copy of the log, for tests.
func (d *Debugger) Recording() []Crossing {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Crossing(nil), d.tt.entries...)
}

// RenameCrossingForTest renames the log's entry i, so a test can make a
// replay diverge from its recording.
func (d *Debugger) RenameCrossingForTest(i int, name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i >= 0 && i < len(d.tt.entries) {
		d.tt.entries[i].Name = name
	}
}

// recordedTotal counts crossings recorded by every debugger in the process,
// for a test that requires recording to have run.
var recordedTotal atomic.Int64

// RecordedCrossings is how many crossings every debugger in this process
// recorded.
func RecordedCrossings() int64 { return recordedTotal.Load() }

// StartPass makes the pass a step back or reverse continue planned current,
// or answers the current one when none is planned (the first run). Call it
// before each run of the program, once the previous pass's run has returned:
// the threads it starts belong to the pass.
func (d *Debugger) StartPass() *Pass {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.plan != nil {
		d.pass, d.plan = d.plan, nil
		d.threads, d.nextID, d.frames = nil, 0, nil
		d.halted, d.pause = false, false
		d.views.Range(func(k, _ any) bool {
			d.views.Delete(k)
			return true
		})
	}
	if !d.started {
		d.started = true
		d.passes++
	}
	d.pass.epoch = d.bpEpoch
	d.refreshLocked()
	return d.pass
}

// Planned reports whether a pass is planned: the run that just returned was
// abandoned for it.
func (d *Debugger) Planned() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.plan != nil
}

// --- the statement hook ----------------------------------------------------

// travelStmt counts statement n of p, which fr is at, and keeps the history
// going back needs. It answers done when the statement is a replay's and the
// hook has nothing more to decide, with the hook's error.
func (d *Debugger) travelStmt(th *thread, p *Pass, fr *frame, site *stmtSite, fhooks []fnHook) (bool, error) {
	n := p.steps.Add(1)
	p.cur = mark{n, site.pos}
	p.prevDepth = 0
	if site.kind != ir.StmtStage && !d.skipped(site) {
		depth := fr.ldepth
		for len(p.last) <= depth {
			p.last = append(p.last, mark{})
		}
		p.prev, p.prevDepth = p.last[depth], depth
		p.last[depth] = p.cur
		if p.first.step == 0 {
			p.first = p.cur
		}
	}
	if p.seek != 0 {
		return true, d.seekStmt(th, p, fr, site, n, fhooks)
	}
	if p.replaying.Load() && n >= p.frontier {
		d.mu.Lock()
		if p.pos >= len(d.tt.entries) {
			p.replaying.Store(false)
		}
		d.mu.Unlock()
	}
	return false, nil
}

// skipped reports whether a step never ends at site (SetSkipFiles).
func (d *Debugger) skipped(site *stmtSite) bool {
	if site.skip == 0 {
		site.skip = 1
		if d.skip != nil && d.skip(site.pos.File()) {
			site.skip = 2
		}
	}
	return site.skip == 2
}

// seekStmt is statement n of a pass running to its target: breakpoints are
// decided, so the pass knows its latest hit, but nothing stops before the
// target.
func (d *Debugger) seekStmt(th *thread, p *Pass, fr *frame, site *stmtSite, n int64, fhooks []fnHook) error {
	// The breakpoints that stopped here, which a reverse continue to this
	// statement reports.
	stop := Stop{Reason: p.seekReason, Pos: site.pos}
	if site.lineStart || len(fhooks) > 0 {
		d.mu.Lock()
		for d.killed {
			d.cond.Wait()
		}
		var hooks []func(*Scope) Hit
		if site.lineStart {
			hooks = d.bps[site.pos.File()][site.pos.Line()]
		}
		d.mu.Unlock()
		if len(hooks) > 0 || len(fhooks) > 0 {
			if hit, lines, fns := d.decide(hooks, fhooks, fr, site); hit.Stop {
				p.noteHit(n, site.pos)
				if stop.Reason == StopBreakpoint {
					stop.Functions, stop.NotLines = fns, !lines
				}
			}
		}
	}
	if n < p.seek {
		return d.replayRestart(th, fr, n, false)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for d.killed {
		d.cond.Wait()
	}
	if site.pos != p.seekPos {
		p.seek = 0
		d.divergedLocked(th, fr, site.pos, site.point, true, fmt.Sprintf(
			"the replay reached statement %d at %s, where the recording has %s", n, posText(site.pos), posText(p.seekPos)))
		return th.takeRestart()
	}
	p.seek = 0
	if p.scan {
		// The first replay of a reverse continue reached the present:
		// plan the second, to the latest hit before it.
		target, reason := p.reverseTarget(n, !p.scanInclusive)
		if target.step == 0 {
			// Nothing before: stop here, where the reverse continue started.
			target, reason = mark{n, site.pos}, StopStart
		}
		d.planLocked(p, target, reason)
		return errAbandon
	}
	th.restartable = true
	th.atHook = true
	d.pause = false
	d.park(th, fr, site.pos, site.point, stop)
	if p.abandoned.Load() {
		return errAbandon
	}
	if th.restart == nil {
		if err := d.replayRestart(th, fr, n, false); err != nil {
			return err
		}
	}
	return th.takeRestart()
}

// noteHit records that a breakpoint decided to stop at step n.
func (p *Pass) noteHit(n int64, pos ir.Pos) {
	if p.hit.step == n {
		return
	}
	p.hitPrev, p.hit = p.hit, mark{n, pos}
}

// replayRestart is the restart a replay performs at step n, at a statement
// or a fault of fr's thread, when the log recorded one there: the unwinding
// that restarts it, or nil.
func (d *Debugger) replayRestart(th *thread, fr *frame, n int64, atFault bool) error {
	p := th.pass
	if p == nil || !p.replaying.Load() || p.nextRestart >= len(d.tt.restarts) {
		return nil
	}
	ev := d.tt.restarts[p.nextRestart]
	if ev.step != n || ev.atFault != atFault {
		return nil
	}
	p.nextRestart++
	for f := fr; f != nil; f = f.parent {
		if f.ldepth == ev.depth {
			return &restartUnwind{target: f}
		}
	}
	return nil
}

// --- crossings -------------------------------------------------------------

type crossingKind uint8

const (
	// crossAnswered is recorded and answered from the log.
	crossAnswered crossingKind = iota
	// crossOutput is recorded, and runs again against a dropping writer.
	crossOutput
	// crossLive runs again: its answer follows from its operands and the
	// machine's own state.
	crossLive
	// crossTasks starts or coordinates tasks: it turns time travel off.
	crossTasks
)

// liveCrossings are the intrinsics over the machine's own state that a
// replay runs again, beside pureCrossings.
var liveCrossings = map[string]bool{
	"Context.with_value": true, "Context.value": true,
	captureKey: true, replayKey: true,
}

// readLineKey is `io.read_line`, which reads the program's input, or the
// input of the `io.capture` or `io.replay` call it runs inside.
const readLineKey = "io.read_line"

// outputCrossings write to the program's output.
var outputCrossings = map[string]bool{"io.print": true, "io.write": true, "io.inspect": true, dbgKey: true}

func kindOfCrossing(name string) crossingKind {
	switch {
	case pureCrossings[name] || liveCrossings[name]:
		return crossLive
	case outputCrossings[name]:
		return crossOutput
	case name == "concurrent", strings.HasPrefix(name, "Task."), strings.HasPrefix(name, "Supervisor."),
		strings.HasPrefix(name, "supervisors."), strings.HasPrefix(name, "Channel."),
		strings.HasPrefix(name, "Sender."), strings.HasPrefix(name, "Receiver."):
		return crossTasks
	}
	return crossAnswered
}

// cross is one crossing out of a debug machine: fr's thread calls name,
// which call performs. It shows on the thread's stack while it runs, and is
// recorded, or answered from the log in a replay.
func (d *Debugger) cross(fr *frame, name string, call func() (any, error)) (any, error) {
	th, prev, prevName := d.crossIn(fr, name)
	defer d.crossOut(th, prev, prevName)
	if th == nil || th.pass == nil || d.ttOff.Load() {
		return call()
	}
	p := th.pass
	if p.abandoned.Load() {
		return nil, errAbandon
	}
	kind := kindOfCrossing(name)
	if kind == crossAnswered && name == readLineKey && fr.runtime.ReadsCaptured() {
		// An `io.capture` or `io.replay` answers this read from its own
		// input, which a replay builds again: the read runs again too, so the
		// capture's transcript and script position are as they were.
		kind = crossLive
	}
	switch kind {
	case crossLive:
		return call()
	case crossTasks:
		pos, _ := callerAt(fr)
		what := "started a task"
		if strings.HasPrefix(name, "Channel.") || strings.HasPrefix(name, "Sender.") || strings.HasPrefix(name, "Receiver.") {
			what = "made a channel"
		}
		d.turnOff(fmt.Sprintf("this program %s (%s at %s), and a program with more than one task does not replay exactly",
			what, name, posText(pos)))
		return call()
	}
	if p.replaying.Load() {
		d.mu.Lock()
		step := p.steps.Load()
		var rec Crossing
		found := p.pos < len(d.tt.entries)
		if found {
			rec = d.tt.entries[p.pos]
		}
		if !found || rec.Name != name || rec.Step != step {
			want := "no further crossing"
			if found {
				want = fmt.Sprintf("%s at statement %d", rec.Name, rec.Step)
			}
			pos, point := callerAt(fr)
			d.divergedLocked(th, fr, pos, point, false, fmt.Sprintf(
				"the replay called %s at statement %d (%s), where the recording has %s", name, step, posText(pos), want))
			err := th.takeRestart()
			d.mu.Unlock()
			if err != nil {
				return nil, err
			}
			if p.abandoned.Load() {
				return nil, errAbandon
			}
			return call()
		}
		p.pos++
		p.answered++
		d.mu.Unlock()
		v, err := rec.Value, rec.Err
		if kind == crossOutput {
			v, err = call()
		}
		d.mu.Lock()
		if p.seek == 0 && step >= p.frontier && p.pos >= len(d.tt.entries) {
			p.replaying.Store(false)
		}
		d.mu.Unlock()
		return v, err
	}
	before := th.entered.Load()
	v, err := call()
	if errors.Is(err, errAbandon) {
		return v, err
	}
	if th.entered.Load() != before {
		d.turnOff(name + " called back into Nomi from Go, which a replay cannot answer from a recording")
		return v, err
	}
	d.mu.Lock()
	if d.tt.off == "" {
		d.tt.entries = append(d.tt.entries, Crossing{Step: p.steps.Load(), Name: name, Value: v, Err: err})
		recordedTotal.Add(1)
		p.pos = len(d.tt.entries)
		if len(d.tt.entries) >= travelLimit {
			d.turnOffLocked(fmt.Sprintf("the recording reached its limit of %d crossings out of the program", travelLimit))
		}
	}
	d.mu.Unlock()
	return v, err
}

// divergedLocked stops th where a replay diverged from its recording, and
// turns time travel off: the program goes on live from here.
func (d *Debugger) divergedLocked(th *thread, fr *frame, pos ir.Pos, point debugPoint, atHook bool, what string) {
	text := "Internal error: " + what + ". Replaying the recorded history went wrong, so step back is now off, " +
		"and the program continues live from here: what it does outside the program from this point really happens."
	p := th.pass
	d.turnOffLocked("a replay of the recorded history diverged (" + what + ")")
	p.replaying.Store(false)
	th.restartable = atHook
	th.atHook = atHook
	d.park(th, fr, pos, point, Stop{Reason: StopDiverged, Pos: pos, Text: text})
}

// turnOff turns time travel off for good, saying why.
func (d *Debugger) turnOff(why string) {
	d.mu.Lock()
	d.turnOffLocked(why)
	d.mu.Unlock()
}

func (d *Debugger) turnOffLocked(why string) {
	if d.tt.off != "" {
		return
	}
	d.tt.off = why
	d.tt.entries, d.tt.restarts = nil, nil
	d.ttOff.Store(true)
	d.refreshLocked()
}

// posText is a position as a message names it: file:line.
func posText(pos ir.Pos) string {
	if !pos.IsValid() {
		return "an unknown position"
	}
	return filepath.Base(pos.File()) + ":" + strconv.Itoa(pos.Line())
}

// --- going back ------------------------------------------------------------

// StepBack goes back to the statement before the one thread is stopped at,
// as reverse next (the file comment): it abandons the current pass and plans
// a replay that stops there with StopStep. The caller starts that pass
// (StartPass) once the current pass's run returns.
func (d *Debugger) StepBack(thread int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	th, p, err := d.travelThreadLocked(thread)
	if err != nil {
		return err
	}
	target := p.backTarget(th)
	if target.step == 0 {
		return ErrNoHistory
	}
	d.planLocked(p, target, StopStep)
	d.wakeLocked()
	return nil
}

// ReverseContinue goes back to the latest earlier statement where a
// breakpoint decided to stop, or to the program's first statement (the file
// comment), as StepBack does.
func (d *Debugger) ReverseContinue(thread int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	th, p, err := d.travelThreadLocked(thread)
	if err != nil {
		return err
	}
	if p.epoch != d.bpEpoch {
		// The breakpoints changed since this pass started, so its hits are
		// another set's: scan to the present under the current set first.
		at := p.cur
		if th.atHook {
			at = mark{th.atStep, th.atPos}
		}
		d.planLocked(p, at, "")
		d.plan.scan, d.plan.scanInclusive = true, !th.atHook
		d.wakeLocked()
		return nil
	}
	target, reason := p.reverseTarget(th.atStep, th.atHook)
	if target.step == 0 {
		return ErrNoHistory
	}
	d.planLocked(p, target, reason)
	d.wakeLocked()
	return nil
}

// travelThreadLocked is the stopped thread id and its pass, or why going
// back from it is not possible.
func (d *Debugger) travelThreadLocked(id int) (*thread, *Pass, error) {
	if d.tt.off != "" {
		return nil, nil, errors.New("Step back is off: " + d.tt.off + ".")
	}
	if d.killed {
		return nil, nil, errors.New("the program has ended")
	}
	th := d.threadByID(id)
	if th == nil || th.at == nil {
		if th != nil && th.calling != nil {
			return nil, nil, errors.New("this thread is inside a call into Go (waiting for input), which cannot be " +
				"unwound; step back once it stops at a statement")
		}
		return nil, nil, errors.New("thread " + strconv.Itoa(id) + " is not stopped")
	}
	p := th.pass
	if p == nil || p != d.pass || p.abandoned.Load() {
		return nil, nil, errors.New("the program is already going back")
	}
	return th, p, nil
}

// backTarget is the statement a step back from th's stop goes to, or a zero
// mark when there is none.
func (p *Pass) backTarget(th *thread) mark {
	depth := th.at.ldepth
	best := mark{}
	for d := 1; d <= depth && d < len(p.last); d++ {
		m := p.last[d]
		if th.atHook && m.step == th.atStep {
			if d != p.prevDepth {
				continue
			}
			m = p.prev
		}
		if m.step > best.step {
			best = m
		}
	}
	return best
}

// reverseTarget is where a reverse continue from step at goes: the latest
// breakpoint hit before it (at it too when the stop is inside the statement,
// !atHook), else the first statement, or a zero mark when there is none.
func (p *Pass) reverseTarget(at int64, atHook bool) (mark, StopReason) {
	h := p.hit
	if atHook && h.step == at {
		h = p.hitPrev
	}
	if h.step != 0 {
		return h, StopBreakpoint
	}
	if f := p.first; f.step != 0 && (f.step < at || (!atHook && f.step <= at)) {
		return f, StopStart
	}
	return mark{}, ""
}

// planLocked abandons p and plans the pass after it, which replays to
// target and stops there for reason.
func (d *Debugger) planLocked(p *Pass, target mark, reason StopReason) {
	next := &Pass{id: p.id + 1, seek: target.step, seekPos: target.pos, seekReason: reason}
	next.frontier = p.frontier
	if !p.replaying.Load() {
		next.frontier = max(next.frontier, p.steps.Load())
	}
	next.replaying.Store(true)
	d.plan = next
	d.passes++
	p.abandoned.Store(true)
}

// wakeLocked resumes every parked thread of an abandoned pass, which
// unwinds.
func (d *Debugger) wakeLocked() {
	for _, th := range d.threads {
		th.step = stepState{}
		th.restart = nil
	}
	d.halted, d.pause = false, false
	d.frames = nil
	d.gen++
	d.refreshLocked()
	d.cond.Broadcast()
}

// recordRestartLocked records that th, stopped at its pass's current step,
// restarts the activation at depth. A restart in recorded history changes
// what follows, so the log is cut there and the pass goes live.
func (d *Debugger) recordRestartLocked(th *thread, depth int) {
	p := th.pass
	if d.tt.off != "" || p == nil {
		return
	}
	n := p.steps.Load()
	if p.replaying.Load() {
		d.tt.entries = d.tt.entries[:p.pos]
		p.frontier = n
		p.replaying.Store(false)
	}
	keep := d.tt.restarts[:0]
	for _, ev := range d.tt.restarts {
		if ev.step < n || (ev.step == n && !th.atHook && !ev.atFault) {
			keep = append(keep, ev)
		}
	}
	d.tt.restarts = append(keep, restartEvent{step: n, atFault: !th.atHook, depth: depth})
	p.nextRestart = len(d.tt.restarts)
}
