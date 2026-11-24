package vm

// A BOUND ON ONE ACTIVATION of a started program (started.go): its boot,
// its main or one call, for a host that serves a program's calls one at a
// time and gives each a context (vmrunner's Instance, the wasip1 reactor's
// timeout_ms).
//
// It is a test case's time limit (caselimit.go) whose end is a context's:
// the activation runs on a view whose fuel watches a flag, and the
// context's end sets the flag and cancels the context of the frame the
// activation runs on, so a computing step stops at its next call, backward
// branch or iteration element and a wait (a timer, a channel, a task)
// wakes. A task the activation spawned runs on the view, so it stops at its
// next step too, and the `concurrent` block it is in ends with the
// activation. The stop unwinds as a case's does, leaving each `once` it
// was forcing unforced.
//
// The fuel also reads the context's deadline itself, every 1024 steps. On a
// host with one thread and no preemption (a wasip1 module) the timer that
// ends the context runs only when the program waits, so a loop that never
// waits is stopped by that read.

import (
	"context"
	"errors"
)

// within is m bounded by ctx for one activation: the view to run it on,
// whose fuel stops when ctx ends and calls cancel then, and release, which
// ends the bound and answers err as the activation's error: ctx's error
// when the bound stopped it, whatever err was. A ctx that never ends
// answers m itself.
func (m *Machine) within(ctx context.Context, cancel func()) (view *Machine, release func(err error) error) {
	if ctx == nil || ctx.Done() == nil {
		return m, func(err error) error { return err }
	}
	f := &fuel{watch: true, outer: m.fuel, onStop: cancel}
	if d, ok := ctx.Deadline(); ok {
		f.bound.Store(&d)
	}
	v := *m
	v.fuel, v.tasks = f, &taskLimit{}
	stop := context.AfterFunc(ctx, func() {
		f.stopped.Store(true)
		cancel()
	})
	return &v, func(err error) error {
		// ctx's end, once it has begun the stop, ends the activation even
		// when it got to finish: what the stop cancelled is gone.
		fired := !stop()
		f.bound.Store(nil)
		if fired || err != nil && f.stopped.Load() {
			return stoppedBy(ctx)
		}
		return err
	}
}

// stoppedBy is the error of an activation ctx's end stopped: ctx's own,
// or context.DeadlineExceeded when the fuel read the deadline before the
// timer that ends ctx ran.
func stoppedBy(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.DeadlineExceeded
}

// IsStop reports whether err is an activation a bound stopped (within).
func IsStop(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
