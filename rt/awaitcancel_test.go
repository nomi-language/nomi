package rt

import (
	"context"
	"testing"
)

// std/tasks.nomi: awaiting a task you cancelled propagates the cancellation.
// On the main line there is no owner to unwind to, so the program fails with
// AwaitedCancelledTaskText — not the deadline text (no deadline exists) and not
// the stray-cancellation fault (which names a lowering bug). Inside a task the
// same await is the ordinary unwind, and the awaiter settles Cancelled.
//
// Both awaits are covered, because each has its own TagCancelled arm.
func TestTaskAwait_ACancelledTaskOnTheMainLineReportsTheCancel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		await func(fr *Frame, h Task[Unit])
	}{
		{"await", func(fr *Frame, h Task[Unit]) { TaskAwait(fr, Site{}, h) }},
		{"await_all", func(fr *Frame, h Task[Unit]) { TaskAwaitAll(fr, Site{}, listFromSlice([]Task[Unit]{h})) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := EnterScope(NewFrame(context.Background()))
			err := recoverFault(func() {
				defer ScopeExit(block, Site{})
				h := TaskSpawn[Unit](block, Site{}, sleeper)
				TaskCancel(h)
				tc.await(block, h)
			})
			if err == nil {
				t.Fatal("awaiting a cancelled task on the main line returned normally")
			}
			if err.Msg != AwaitedCancelledTaskText() {
				t.Errorf("the main line was told %q", err.Msg)
			}

			// Inside a task: the awaiter unwinds and settles Cancelled.
			outer := EnterScope(NewFrame(context.Background()))
			var got Outcome[Unit]
			func() {
				defer ScopeExit(outer, Site{})
				awaiter := TaskSpawn[Unit](outer, Site{}, func(tf *Frame) Unit {
					inner := EnterScope(tf)
					defer ScopeExit(inner, Site{})
					h := TaskSpawn[Unit](inner, Site{}, sleeper)
					TaskCancel(h)
					tc.await(inner, h)
					return Unit{}
				})
				got = TaskOutcomeOf(outer, Site{}, awaiter)
			}()
			if got.Tag != TagCancelled {
				t.Errorf("a task awaiting a cancelled task settled %v, want Cancelled", got.Tag)
			}
		})
	}
}

// sleeper blocks until its task is cancelled, then takes the unwind.
func sleeper(tf *Frame) Unit {
	<-tf.Context().Done()
	CancelIfDone(tf)
	return Unit{}
}
