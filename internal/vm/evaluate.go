package vm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
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
// second, with the crossings Effects reports as unbounded refused too.

// Limits bounds one Evaluate. Steps counts the activations the evaluation
// starts (calls, tail transfers, callbacks from Go), the backward branches it
// takes and the elements its iterations pass along, which are the ways a
// program repeats work. Deadline is a wall-clock backstop, read every 1024
// steps; zero means none. A single crossing into Go is not interrupted, so
// the steps bound a pure crossing's cost only when it follows from the size
// of its operands; one whose cost grows with an operand's value is marked
// unbounded (unboundedCrossing), and Effects reports a call into it.
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
	// A watching fuel with a bound (an activation's deadline, within.go;
	// nil for none) also reads the clock every 1024 steps and, past it,
	// sets stopped and calls onStop. The deadline keeps its monotonic
	// reading, so a step of the wall clock neither fires the bound early
	// nor holds it off. The bound is cleared when the activation ends,
	// since a task it started may run on.
	watch  bool
	outer  *fuel
	bound  atomic.Pointer[time.Time]
	onStop func()
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
		if d := f.bound.Load(); d != nil && f.ticks.Add(1)&1023 == 0 && time.Now().After(*d) {
			// A bound's own deadline (within.go), read here because the
			// timer that would set stopped may never run.
			f.stopped.Store(true)
			if f.onStop != nil {
				f.onStop()
			}
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
// app field cannot be evaluated (Effects reports the fields it reads). It
// answers f's result, ErrLimit when lim ran out, or the program's own
// failure.
func (m *Machine) Evaluate(ctx context.Context, f *ir.Func, args []any, lim Limits) (result any, err error) {
	return m.evaluate(ctx, f, args, lim, nil)
}

// evaluate is Evaluate with the app app's lineage reads in force, when app
// is not nil: a debugger's evaluation reads the app of the frame it stopped
// in (Scope.Run).
func (m *Machine) evaluate(ctx context.Context, f *ir.Func, args []any, lim Limits, app *rt.Frame) (result any, err error) {
	run := *m
	run.fuel = newFuel(lim)
	fr := rt.NewFrame(run.withDiagnostics(ctx))
	if app != nil {
		fr = rt.WithAppOf(fr, app)
	}
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

// unboundedCrossing reports whether a pure crossing into Go named name
// costs more as an operand's value grows, rather than its size: a stdlib
// adapter whose internal/stdlibbindings row is marked Unbounded. No
// intrinsic is.
func (m *Machine) unboundedCrossing(name string) bool {
	if _, ok := m.hosts[name]; ok {
		return false
	}
	return stdlibadapters.Unbounded(name)
}

// seededCrossing reports whether a pure crossing into Go named name answers
// a hash keyed by the run's hash seed, which differs between runs: an
// intrinsic marked seeded in the intrinsic table (`vm.hash_value`), or a
// stdlib adapter whose internal/stdlibbindings row is marked Seeded
// (`strings.String.hash`).
func (m *Machine) seededCrossing(name string) bool {
	if in, ok := m.hosts[name]; ok {
		return in.seeded
	}
	return stdlibadapters.Seeded(name)
}

// Effects is what Machine.Effects found the functions it walked reach,
// each list sorted.
type Effects struct {
	// Effects is each effect: a crossing into Go its declaration does not
	// mark pure (output, files, the network, the clock, the system's
	// entropy, tasks, channels), "F calls K", and a declaration this
	// machine could not run.
	Effects []string
	// Unbounded is each call into a pure crossing whose cost grows with an
	// operand's value (unboundedCrossing), "F calls K". An evaluation's
	// steps do not bound it.
	Unbounded []string
	// Seeded is each call into a pure crossing whose answer is a hash keyed
	// by the run's seed (seededCrossing), "F calls K". The same operands
	// give another answer in another run.
	Seeded []string
	// AppFields is each app field the functions read or replace with
	// `with`, the Context field among them, by its name in the app type
	// (`limit` for `App.limit`). Reading an app field is reading an input
	// the caller did not write, so it is no effect, but code run with no
	// app (an editor's evaluation, a rule, a literal) has none to read.
	AppFields []string
}

// Effects walks everything the functions fs can reach, as Unretained does,
// and answers what stops them being evaluated for an editor, a rule or a
// literal: their effects, their calls whose cost no step limit bounds,
// their calls whose answer differs between runs, and the app fields they
// read. All four empty means each computes its result from its operands
// alone, at a cost the steps bound, the same in every run. Each caller
// decides which it refuses.
//
// It over-approximates as the reachability walk does: a dispatched call
// reaches every implementation of its method, and a function value reaches
// every body the evaluation creates a value of.
func (m *Machine) Effects(fs ...*ir.Func) Effects {
	w := reachWalk{m: m, seen: map[*ir.Func]bool{}, found: map[string]Unretained{}, effects: map[string]bool{}, unbounded: map[string]bool{},
		seeded: map[string]bool{}, uses: map[*ir.Func][]string{}}
	for _, f := range fs {
		w.push(f, EdgeRoot)
	}
	for len(w.queue) > 0 {
		next := w.queue[len(w.queue)-1]
		w.queue = w.queue[:len(w.queue)-1]
		w.visit(next)
	}
	for _, u := range w.found {
		w.effects[fmt.Sprintf("%s cannot run here (reached from %s)", u.Name, u.From)] = true
	}
	fields := map[string]bool{}
	for _, used := range w.uses {
		for _, field := range used {
			fields[field] = true
		}
	}
	return Effects{Effects: sortedKeys(w.effects), Unbounded: sortedKeys(w.unbounded), Seeded: sortedKeys(w.seeded), AppFields: sortedKeys(fields)}
}

// sortedKeys is set's keys, sorted, or nil for none.
func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// appFieldOf is the app field one instruction reads or replaces, by its
// name in the app type, or "": a read of a field or of the Context field,
// and a `with` that replaces one. The save and restore of the scope around
// a `with` (RefScope, StoreScope) name no field; the replacement does.
func appFieldOf(in ir.Instr) string {
	var field *ir.Symbol
	switch n := in.(type) {
	case *ir.Ref:
		if k := n.Kind(); k == ir.RefAppField || k == ir.RefContext {
			field = n.Sym()
		}
	case *ir.Store:
		if k := n.Kind(); k == ir.StoreAppField || k == ir.StoreContext {
			field = n.Sym()
		}
	}
	if field == nil {
		return ""
	}
	return strings.TrimPrefix(field.Name(), "$")
}

// evalRefusal is what a crossing into Go is to an evaluation that must act
// on nothing, end and answer as the program's runs will: nothing that stops
// it, an effect, a pure call whose cost grows with an operand's value, or a
// pure call whose answer is a hash keyed by the run's seed.
type evalRefusal uint8

const (
	refuseNothing evalRefusal = iota
	refuseEffect
	refuseUnbounded
	refuseSeeded
)

// effectOf is what one instruction is to an evaluation (evalRefusal), and
// when it is not refuseNothing, "F calls K", which names the crossing.
func (m *Machine) effectOf(in ir.Instr, from string) (call string, kind evalRefusal) {
	switch n := in.(type) {
	case *ir.Call:
		if n.Crosses() {
			return m.crossingEffect(n.Callee().Name(), from)
		}
	case *ir.Defer:
		if c := n.Call(); c != nil && c.Crosses() {
			return m.crossingEffect(c.Callee().Name(), from)
		}
	}
	return "", refuseNothing
}

// crossingEffect is what a crossing into name from from is to effectOf.
func (m *Machine) crossingEffect(name, from string) (call string, kind evalRefusal) {
	switch {
	case !m.pureCrossing(name):
		kind = refuseEffect
	case m.unboundedCrossing(name):
		kind = refuseUnbounded
	case m.seededCrossing(name):
		kind = refuseSeeded
	default:
		return "", refuseNothing
	}
	return fmt.Sprintf("%s calls %s", from, m.crossingName(name)), kind
}

// crossingName is how a refusal names the crossing keyed name: the name an
// intrinsic is written by where its key is the machine's own
// (intrinsic.written), and otherwise its key.
func (m *Machine) crossingName(name string) string {
	if in, ok := m.hosts[name]; ok && in.written != "" {
		return in.written
	}
	return name
}
