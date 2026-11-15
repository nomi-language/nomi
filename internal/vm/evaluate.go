package vm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibadapters"
	"github.com/nomi-language/nomi/rt"
)

// EVALUATING A FUNCTION FOR AN EDITOR.
//
// The language server runs a typed literal's `from_fragments` while the user
// types, to report a literal its handler rejects. It must not run anything
// that acts on the world, and it must not hang. Effects answers the first
// question before anything runs; Evaluate runs under Limits, which answer the
// second.

// Limits bounds one Evaluate. Steps counts the activations the evaluation
// starts (calls, tail transfers, callbacks from Go), the backward branches it
// takes and the elements its iterations pass along, which are the ways a
// program repeats work. Deadline is a wall-clock backstop, read every 1024
// steps; zero means none. A single crossing into Go is not interrupted, so
// only crossings whose cost follows from the size of their operands are
// marked pure (pureCrossing), and the steps bound that size.
type Limits struct {
	Steps    int64
	Deadline time.Time
	// Spent, when set, receives the steps the evaluation took.
	Spent *int64
}

// ErrLimit is what Evaluate answers when the evaluation used up its Limits.
var ErrLimit = errors.New("vm: the evaluation reached its step or time limit")

// ErrDeadline is Evaluate's answer when the evaluation was still running at
// Limits.Deadline. It is an ErrLimit. Unlike the steps, the deadline depends
// on how fast the machine is, so a caller that must answer the same on every
// machine tells it from the step limit.
var ErrDeadline = fmt.Errorf("%w: deadline", ErrLimit)

// fuel is a running evaluation's remaining Limits, shared by every view of the
// machine it runs on, a task's included, so its counters are atomic. Nil on a
// machine that is not evaluating, which is every machine `nomi run` and `nomi
// test` open: the check is one nil comparison.
type fuel struct {
	left     atomic.Int64
	deadline time.Time
	// stop is StopOn's context's Done channel, or nil, read with the
	// deadline every 1024 steps. stopped records that it closed.
	stop    <-chan struct{}
	stopped atomic.Bool
	ticks   atomic.Uint32
	// watch says the fuel counts nothing and only answers whether stopped
	// was set: a test case's time limit (caselimit.go), whose timer sets it.
	// A step then costs one atomic load, which no task contends for. outer
	// is the fuel of the machine the case runs on, StopOn's, still burned.
	watch bool
	outer *fuel
	// prof is the profiler a profiling machine's watching fuel samples for
	// (profile.go): burn takes its pending ticks at the frame it is given.
	prof *Profiler
	// overDeadline records that the run was stopped at its deadline.
	overDeadline atomic.Bool
}

func newFuel(lim Limits) *fuel {
	f := &fuel{deadline: lim.Deadline}
	f.left.Store(lim.Steps)
	return f
}

// burn spends one step. Once the steps are gone every later burn fails too,
// so a program that catches the failure and loops fails again at its next
// call or backward branch. fr is the activation taking the step, at pc, for
// a profiler's sample; nil when the step is taken from Go.
func (f *fuel) burn(fr *frame, pc int) error {
	if f.watch {
		if f.stopped.Load() {
			return ErrStopped
		}
		if f.prof != nil && fr != nil && f.prof.due() {
			f.prof.take(fr, pc, "", ir.Pos{})
		}
		if f.outer != nil {
			return f.outer.burn(fr, pc)
		}
		return nil
	}
	if f.left.Add(-1) < 0 {
		if f.stopped.Load() {
			return ErrStopped
		}
		if f.overDeadline.Load() {
			return ErrDeadline
		}
		return ErrLimit
	}
	if (!f.deadline.IsZero() || f.stop != nil) && f.ticks.Add(1)&1023 == 0 {
		if !f.deadline.IsZero() && time.Now().After(f.deadline) {
			f.overDeadline.Store(true)
			f.left.Store(-1)
			return ErrDeadline
		}
		select {
		case <-f.stop:
			f.stopped.Store(true)
			f.left.Store(-1)
			return ErrStopped
		default:
		}
	}
	return nil
}

func (f *fuel) spent() bool { return f != nil && f.left.Load() < 0 }

// WithLimits is m with every run it starts (Main, Call, RunCases) bounded by
// lim as Evaluate's run is: a run that uses them up stops with ErrLimit, and
// Exhausted reports that it did. The limits are shared by every view of the
// returned machine, so it bounds one run. vmhost's run harness
// (vmhost/run_fuzz_test.go) runs generated programs this way so that none
// hangs.
func (m *Machine) WithLimits(lim Limits) *Machine {
	run := *m
	run.fuel = newFuel(lim)
	return &run
}

// ErrStopped is what a run on a machine StopOn gave answers when the
// context ended while it ran.
var ErrStopped = errors.New("vm: the run was stopped")

// StopOn is m with every run it starts (Main, Call, RunCases) ending when
// ctx does. A run blocked in a wait (a timer, a channel, a task) wakes at
// once, because the frames it builds from Go are over ctx; a run that is
// computing stops at its next call, backward branch or iteration element,
// checked as WithLimits' deadline is, with ErrStopped. It replaces any
// limits WithLimits gave. `nomi run --watch` stops a run this way when a
// source file changes.
func (m *Machine) StopOn(ctx context.Context) *Machine {
	run := *m
	run.stopCtx = ctx
	f := newFuel(Limits{Steps: math.MaxInt64})
	f.stop = ctx.Done()
	run.fuel = f
	return &run
}

// Exhausted reports whether a run on m used up the limits WithLimits gave it.
func (m *Machine) Exhausted() bool { return m.fuel.spent() }

// metered is seq burning one step per element it passes along, so an
// iteration driven in Go (`Iter.count(Iter.repeat(x))`) is bounded too. It
// keeps seq's source and count.
func (m *Machine) metered(seq rt.Seq[any]) rt.Seq[any] {
	f, run := m.fuel, seq.Run
	seq.Run = func(fr *rt.Frame, yield func(*rt.Frame, any) bool) bool {
		return run(fr, func(fr *rt.Frame, item any) bool {
			if err := f.burn(nil, -1); err != nil {
				panic(iterationFailure{err})
			}
			return yield(fr, item)
		})
	}
	return seq
}

// Evaluate calls f with args under lim, on a fresh rt frame of ctx and
// without booting the program: there is no app, so a function that reads an
// app field cannot be evaluated (Effects reports the read). It answers f's
// result, ErrLimit when lim ran out, or the program's own failure.
func (m *Machine) Evaluate(ctx context.Context, f *ir.Func, args []any, lim Limits) (result any, err error) {
	run := *m
	run.fuel = newFuel(lim)
	fr := rt.NewFrame(run.withDiagnostics(ctx))
	run.hostFrame = fr
	defer func() {
		if r := recover(); r != nil {
			e, isFault := r.(*rt.Error)
			if !isFault {
				panic(r)
			}
			result, err = nil, &Fault{err: e}
		}
		if run.fuel.spent() {
			result, err = nil, ErrLimit
			if run.fuel.overDeadline.Load() {
				err = ErrDeadline
			}
		}
		if lim.Spent != nil {
			*lim.Spent = lim.Steps - max(run.fuel.left.Load(), 0)
		}
	}()
	return run.callWithFrame(f, args, fr)
}

// pureCrossing reports whether a crossing into Go named name is pure:
// it computes its answer from its operands alone and acts on nothing
// outside the machine. That is a property of the crossing's declaration:
// an intrinsic marked pure in the intrinsic table (vm.go), or a generated
// stdlib adapter whose internal/stdlibbindings row is marked Pure. An
// intrinsic answers a name before the adapters do, as at a call. Every other
// crossing, a binding of a project's own `go` declarations, std/compiler's
// hosts and a REPL's session values among them, is an effect.
func (m *Machine) pureCrossing(name string) bool {
	if in, ok := m.hosts[name]; ok {
		return in.pure
	}
	return stdlibadapters.Pure(name)
}

// Effects walks everything the functions fs can reach, as Unretained does,
// and answers what stops them being evaluated for an editor, sorted: each
// crossing into Go its declaration does not mark pure (output, files, the
// network, the clock, randomness, tasks, channels, the Context), each read
// or write of the running app's fields, Context or scope, and each
// declaration this machine could not run. Nil means each computes its result from its operands alone.
//
// It over-approximates as the reachability walk does: a dispatched call
// reaches every implementation of its method, and a function value reaches
// every body the evaluation creates a value of.
func (m *Machine) Effects(fs ...*ir.Func) []string {
	w := reachWalk{m: m, seen: map[*ir.Func]bool{}, found: map[string]Unretained{}, effects: map[string]bool{}}
	for _, f := range fs {
		w.push(f)
	}
	for len(w.queue) > 0 {
		next := w.queue[len(w.queue)-1]
		w.queue = w.queue[:len(w.queue)-1]
		w.visit(next)
	}
	for _, u := range w.found {
		w.effects[fmt.Sprintf("%s cannot run here (reached from %s)", u.Name, u.From)] = true
	}
	if len(w.effects) == 0 {
		return nil
	}
	out := make([]string, 0, len(w.effects))
	for e := range w.effects {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// effectOf is the effect one instruction has on m, or "".
func (m *Machine) effectOf(in ir.Instr, from string) string {
	switch n := in.(type) {
	case *ir.Ref:
		switch n.Kind() {
		case ir.RefAppField, ir.RefContext, ir.RefScope:
			name := ""
			if n.Sym() != nil {
				name = " " + n.Sym().Name()
			}
			return fmt.Sprintf("%s reads the running app (%s%s)", from, n.Kind(), name)
		}
	case *ir.Store:
		return fmt.Sprintf("%s writes the running app", from)
	case *ir.Call:
		if n.Crosses() && !m.pureCrossing(n.Callee().Name()) {
			return fmt.Sprintf("%s calls %s", from, n.Callee().Name())
		}
	case *ir.Defer:
		if c := n.Call(); c != nil && c.Crosses() && !m.pureCrossing(c.Callee().Name()) {
			return fmt.Sprintf("%s calls %s", from, c.Callee().Name())
		}
	}
	return ""
}
