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
}

// newCaseLimit is the limit of one case on m, or nil when m has none.
func (m *Machine) newCaseLimit() *caseLimit {
	if m.caseLimit <= 0 || m.dbg != nil {
		return nil
	}
	return &caseLimit{d: m.caseLimit, fuel: &fuel{watch: true, outer: m.fuel}}
}

// arm starts the case's clock, which also calls cancel, when not nil, as it
// runs out. The answer stops the clock.
func (l *caseLimit) arm(cancel func()) (disarm func()) {
	t := time.AfterFunc(l.d, func() {
		l.timedOut.Store(true)
		l.fuel.stopped.Store(true)
		if cancel != nil {
			cancel()
		}
	})
	return func() { t.Stop() }
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
	// that kept no frames.
	Stack []StackFrame
	// Line is the line of the innermost frame in the case's own file that
	// is not the stdlib's, or 0.
	Line int
	// Hint, when set, is a last `help:` line, which the command that set
	// the limit supplies.
	Hint string
}

// failure is the failure of the case fn, stopped by its limit with err.
func (l *caseLimit) failure(fn *ir.Func, err error) *TimedOut {
	t := &TimedOut{Limit: l.d}
	var in *interruption
	if errors.As(err, &in) {
		t.Stack = in.frames
	}
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
	fmt.Fprintf(&b, "did not finish within %s, so it was stopped", t.Limit)
	if len(t.Stack) > 0 {
		b.WriteString("\nit was running:")
		for i, f := range t.Stack {
			if len(t.Stack) > stackShownInner+stackShownOuter+1 && i >= stackShownInner && i < len(t.Stack)-stackShownOuter {
				if i == stackShownInner {
					fmt.Fprintf(&b, "\n  ... %d more", len(t.Stack)-stackShownInner-stackShownOuter)
				}
				continue
			}
			at := fmt.Sprintf("%s:%d", rt.DisplayPath(f.Pos.File()), f.Pos.Line())
			if f.Name == "" {
				fmt.Fprintf(&b, "\n  %s", at)
			} else {
				fmt.Fprintf(&b, "\n  %s in %s", at, f.Name)
			}
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
