package vm

// A TEST CASE'S TIME LIMIT: how `nomi test` ends a case that never finishes.
//
// A machine given WithCaseLimit runs each case RunCases runs on a view whose
// fuel watches one flag (evaluate.go's `fuel.watch`): every call, backward
// branch and Go-driven iteration element reads it, one atomic load, and
// nothing counts. A real-time timer sets it when the case has run for the
// limit, and also cancels the context of the frame the case runs on, so a
// case blocked in a wait (a timer, a channel, a task) wakes. A task the case
// spawned shares the view's fuel, so a spinning task stops at its next step
// too, and the `concurrent` block it is in ends with the case.
//
// A case under `clock Clock.Virtual` runs in a synctest bubble, where a
// timer reads the bubble's fake clock, which does not move while anything
// computes. Its timer is therefore started outside the bubble, around it
// (RunCases), and leaves the bubble's context alone: a bubble blocked on a
// channel from outside it would never advance its clock.
//
// # A wait the stop cannot reach
//
// The flag stops computing and the cancelled context wakes a wait, std's
// waits outside the program among them, so a case on the real clock ends
// once its time runs out, and the tests after it run. A bubbled case
// has no context the limit may cancel, so a wait outside the program (a
// socket, a process) does not wake, and its bubble cannot end: nothing
// outside a bubble may touch its channels, and nothing inside it can run
// while the clock stands still. The limit's timer also closes the case's
// stop channel (rt.Test.Stop), and rt.RunBubbled gives up on a bubble that
// has not ended shortly after: the case fails with a *TimedOut that says it
// was waiting outside the program, and no case starts in the process after
// it, since a bubble beside it could close one of its channels.
//
// Where it waits cannot come from an unwind, since nothing unwinds. A
// bubbled case's view notes each call into Go (an adapter, the only code
// that can wait outside the program) on the calling task's record in rt's
// task registry while it runs (rt.EnterGo): the activation that made it
// and the call's position, the record a task's wait inside the program
// also keeps its place in (rt's package doc, "Deadlock"). The calls still
// open when the bubble is given up on are where the case waits, the
// innermost Nomi frame of each task that waits (rt.CaseStillRunning).
//
// # The stack, paid for only by a case that is stopped
//
// A plain machine keeps no logical call stack (a debug machine does,
// debug.go). It does not need one here: once the flag is set, the error the
// stopped step raises unwinds through Machine.fault at every activation it
// leaves, which knows the activation's function and the instruction it is
// at. stopped records each as an interruption frame, innermost first, so
// the stack is built from the unwind itself and a case that finishes pays
// nothing for it.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/rt/vclock"
)

// WithCaseLimit is m with every test case RunCases runs stopped once it has
// run for d of wall-clock time (its group's clock, boot and setup and its
// body together); the case then fails with a *TimedOut. Zero means no limit.
// A debug machine ignores it, since a paused program must not time out.
func (m *Machine) WithCaseLimit(d time.Duration) *Machine {
	m.caseLimit = d
	return m
}

// caseLimit is one case's time limit while it runs.
type caseLimit struct {
	d    time.Duration
	fuel *fuel
	// timedOut says the timer fired, rather than an outer stop (StopOn).
	timedOut atomic.Bool
	// expired closes when the timer fires (rt.Test.Stop).
	expired chan struct{}
	// virtual says the case runs under a virtual clock, so its view notes
	// each call into Go on the calling task's record (Machine.noteGo).
	virtual bool
	// fileName is the machine's (WithFileNames).
	fileName func(string) string
}

// newCaseLimit is the limit of one case on m, or nil when m has none.
func (m *Machine) newCaseLimit(virtual bool) *caseLimit {
	if m.caseLimit <= 0 || m.dbg != nil {
		return nil
	}
	return &caseLimit{d: m.caseLimit, fuel: &fuel{watch: true, outer: m.fuel}, expired: make(chan struct{}), virtual: virtual, fileName: m.fileName}
}

// arm starts the case's clock, which also calls cancel, when not nil, as it
// runs out. The answer stops the clock. A case arms its limit once.
func (l *caseLimit) arm(cancel func()) (disarm func()) {
	t := time.AfterFunc(l.d, func() {
		l.timedOut.Store(true)
		l.fuel.stopped.Store(true)
		if cancel != nil {
			cancel()
		}
		close(l.expired)
	})
	return func() { t.Stop() }
}

// siteOf is where an activation of fn is at pos, as rt records a wait or a
// call into Go: fn may be nil when the caller does not know the function.
func siteOf(fn *ir.Func, pos ir.Pos) rt.Site {
	s := rt.Site{File: pos.File(), Line: pos.Line(), Stdlib: pos.Stdlib()}
	if fn != nil {
		s.Fn = frameName(fn)
	}
	return s
}

// siteFrame is s as a stack frame of a report.
func siteFrame(s rt.Site) StackFrame {
	f := StackFrame{Name: s.Fn}
	if s.File != "" && s.Line > 0 {
		f.Pos = ir.At(s.File, s.Line, 1)
		if s.Stdlib {
			f.Pos = f.Pos.WithStdlib()
		}
	}
	return f
}

// bubble is vclock.RunCase with the case's clock running around the
// bubble, in real time.
func (l *caseLimit) bubble(body func() error, cleanup func()) error {
	disarm := l.arm(nil)
	defer disarm()
	return vclock.RunCase(body, cleanup)
}

// interruption is the error a stopped step raises once it has left an
// activation: ErrStopped, with each activation it has left so far.
type interruption struct {
	frames []StackFrame
}

func (e *interruption) Error() string { return ErrStopped.Error() }
func (e *interruption) Unwrap() error { return ErrStopped }

// stopped is the error err, raised at pc in fr after the case's fuel was
// stopped, leaving fr: an interruption with fr added to its frames. Whatever
// err was, it is the stop's doing (a wait the stop woke traps, say).
func stopped(fr *frame, pc int, err error) error {
	var in *interruption
	if !errors.As(err, &in) {
		in = &interruption{}
	}
	in.frames = append(in.frames, StackFrame{Name: frameName(fr.fn), Pos: fr.c.posNear(pc)})
	if fr.stdEntry != nil {
		// A tail call from the program into the stdlib replaced the
		// program's activation; its call is still the program's line.
		in.frames = append(in.frames, StackFrame{Pos: fr.stdEntry.pos, Tail: true})
	}
	return in
}

// posNear is the position of the instruction at pc, or of the nearest one
// before it that has one (a backward branch has none), or the function's.
func (c *code) posNear(pc int) ir.Pos {
	i := sort.Search(len(c.sites), func(i int) bool { return c.sites[i].pc > pc }) - 1
	if i >= 0 && c.sites[i].pos.IsValid() {
		return c.sites[i].pos
	}
	return c.fn.Pos()
}

// TimedOut is a test case that did not finish within its time limit
// (WithCaseLimit) and was stopped. It is the case's failure.
type TimedOut struct {
	Limit time.Duration
	// Stack is where the case was when it stopped, innermost first: each
	// activation with the call or statement it was at. A frame with an
	// empty Name is a program call whose tail call into the stdlib replaced
	// its activation. It is empty when the stop reached the case by a path
	// that kept no frames. For a case Waiting, it is the innermost frame of
	// each call into Go the case was still in, the latest first.
	Stack []StackFrame
	// Waiting says the stop could not end the case: it was waiting outside
	// the program, under a virtual clock, and no case after it ran
	// (rt.ErrCaseStillRunning).
	Waiting bool
	// Line is the line of the innermost frame in the case's own file that
	// is not the stdlib's, or 0.
	Line int
	// Hint, when set, is a last `help:` line, which the command that set
	// the limit supplies.
	Hint string
	// fileName names a stack frame's file in Error's text (WithFileNames).
	fileName func(string) string
}

// failure is the failure of the case fn, stopped by its limit with err.
func (l *caseLimit) failure(fn *ir.Func, err error) *TimedOut {
	var in *interruption
	if errors.As(err, &in) {
		return l.timedOutAt(fn, in.frames, false)
	}
	return l.timedOutAt(fn, nil, false)
}

// waiting is the failure of the case fn, whose bubble rt.RunBubbled gave
// up on: the calls into Go its tasks are still in (rt.CaseStillRunning)
// are where it waits.
func (l *caseLimit) waiting(fn *ir.Func, inGo []rt.Site) *TimedOut {
	stack := make([]StackFrame, len(inGo))
	for i, s := range inGo {
		stack[i] = siteFrame(s)
	}
	return l.timedOutAt(fn, stack, true)
}

// timedOutAt is the failure of the case fn, at stack.
func (l *caseLimit) timedOutAt(fn *ir.Func, stack []StackFrame, waiting bool) *TimedOut {
	t := &TimedOut{Limit: l.d, Stack: stack, Waiting: waiting, fileName: l.fileName}
	file := fn.Pos().File()
	for _, f := range t.Stack {
		if !f.Pos.Stdlib() && f.Pos.File() == file && f.Pos.Line() > 0 {
			t.Line = f.Pos.Line()
			break
		}
	}
	return t
}

// stackShownInner and stackShownOuter are how many of a long stack's
// innermost and outermost frames TimedOut's text shows.
const stackShownInner, stackShownOuter = 8, 2

func (t *TimedOut) Error() string {
	var b strings.Builder
	if t.Line > 0 {
		fmt.Fprintf(&b, "line %d: ", t.Line)
	}
	if t.Waiting {
		fmt.Fprintf(&b, "did not finish within %s and could not be stopped: it was waiting outside the program", t.Limit)
	} else {
		fmt.Fprintf(&b, "did not finish within %s, so it was stopped", t.Limit)
	}
	switch {
	case len(t.Stack) > 0 && t.Waiting:
		b.WriteString("\nit was waiting at:")
	case len(t.Stack) > 0:
		b.WriteString("\nit was running:")
	}
	for i, f := range t.Stack {
		if len(t.Stack) > stackShownInner+stackShownOuter+1 && i >= stackShownInner && i < len(t.Stack)-stackShownOuter {
			if i == stackShownInner {
				fmt.Fprintf(&b, "\n  ... %d more", len(t.Stack)-stackShownInner-stackShownOuter)
			}
			continue
		}
		name := rt.DisplayPath(f.Pos.File())
		if t.fileName != nil {
			name = t.fileName(f.Pos.File())
		}
		at := fmt.Sprintf("%s:%d", name, f.Pos.Line())
		if f.Name == "" {
			fmt.Fprintf(&b, "\n  %s", at)
		} else {
			fmt.Fprintf(&b, "\n  %s in %s", at, f.Name)
		}
	}
	if t.Hint != "" {
		b.WriteString("\nhelp: " + t.Hint)
	}
	return b.String()
}

// SourceLine is the line the report locates the failure at.
func (t *TimedOut) SourceLine() int { return t.Line }

// Failure is t as a test case's failure.
func (t *TimedOut) Failure() error {
	if t == nil {
		return nil
	}
	return t
}
