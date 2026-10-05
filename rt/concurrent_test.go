package rt

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// The concurrency runtime, tested for the four properties a corpus golden file
// cannot see and the one it can.
//
// # Why these are here and not only in internal/irbuild
//
// The corpus file that exercises this runtime
// (16-concurrency/concurrent_runtime_test.nomi) matches its golden record byte
// for byte, and it would go on matching if
//
//   - a task's panic killed the process on a path no corpus file takes,
//   - the outcome classification put a Nomi fault under `Panicked`,
//   - `close(done)` raced the outcome write,
//   - `ScopeExit` returned before its tasks finished.
//
// Each is asserted below as a positive — a demonstrated firing, a non-zero
// count, a value read — rather than as "nothing went wrong", because "nothing
// went wrong" is what a runtime that was never entered also reports.
//
// # Run these under -race
//
//	go test -race ./rt -run TestConcurrent
//
// The publish edge these rely on is `close(t.done)`, and a mutex-free publish is
// exactly the kind of thing plain `go test` cannot fail on, because `go test`
// does not enable the detector.

// scopeFixture opens a scope on a fresh root frame, the way a lowered
// `concurrent` block does, and returns the body frame plus its closer.
//
// A helper rather than three lines per test because getting the pairing wrong —
// forgetting the exit — turns a failure into a hang, and a hang in a package
// test is attributed to the wrong thing.
func scopeFixture(t *testing.T) (*Frame, func()) {
	t.Helper()
	fr := EnterScope(NewFrame(context.Background()))
	return fr, func() { ScopeExit(fr) }
}

// TestConcurrent_SpawnAndAwaitCarryTheValue is the anti-vacuity control for
// every test below it: if this fails, none of the others is measuring what its
// name says.
func TestConcurrent_SpawnAndAwaitCarryTheValue(t *testing.T) {
	fr, exit := scopeFixture(t)
	defer exit()

	h := TaskSpawn(fr, func(*Frame) int64 { return 42 })
	if got := TaskAwait(fr, h); got != 42 {
		t.Fatalf("await returned %d, want 42", got)
	}
	if o := TaskOutcomeOf(fr, h); o.Tag != TagCompleted || o.Completed != 42 {
		t.Fatalf("outcome is %+v, want Completed(42)", o)
	}
}

// TestConcurrent_APanickingTaskDoesNotKillTheProcess is the property the file
// header names first, and it is asserted by reaching the line after it.
//
// A test that merely called `TaskOutcomeOf` and checked the tag would pass on a
// runtime that re-panicked, because the re-panic would fail the test rather than
// the process — so the assertion has to be that the sibling work and the
// enclosing test both survive.
func TestConcurrent_APanickingTaskDoesNotKillTheProcess(t *testing.T) {
	fr, exit := scopeFixture(t)
	defer exit()

	bad := TaskSpawn(fr, func(*Frame) int64 { panic("deliberate") })
	good := TaskSpawn(fr, func(*Frame) int64 { return 7 })

	o := TaskOutcomeOf(fr, bad)
	if o.Tag != TagFailed {
		t.Fatalf("a panicking task reported tag %d, want TagFailed (%d)", o.Tag, TagFailed)
	}
	// `Panicked`, not `Errored`: a Go panic that is not a *rt.Error means a
	// defect in rt or the VM, and std/tasks reserves `Panicked` for
	// exactly that. Classifying it as `Errored` would make it indistinguishable
	// from a division by zero in an Outcome.
	if o.Failed.Tag != TagPanicked {
		t.Errorf("a Go panic reported failure tag %d, want TagPanicked (%d)", o.Failed.Tag, TagPanicked)
	}
	if !strings.Contains(o.Failed.Msg, "deliberate") {
		t.Errorf("the failure message is %q and does not name the panic value", o.Failed.Msg)
	}
	// The positive: a sibling ran to completion and this line was reached.
	if got := TaskAwait(fr, good); got != 7 {
		t.Errorf("the sibling of a panicking task returned %d, want 7", got)
	}
}

// TestConcurrent_ANomiFaultIsErroredAndAwaitInheritsIt pins the mapping the file
// header states, in both directions:
//
//	Task.outcome -> Failed(Errored(<the fault's own text>)), program continues
//	Task.await   -> the fault propagates with that exact text
//
// Both halves, because a runtime that classified correctly and swallowed the
// text, or propagated correctly and mislabelled the tag, would pass one each.
func TestConcurrent_ANomiFaultIsErroredAndAwaitInheritsIt(t *testing.T) {
	const text = "line 7: division by zero"
	t.Run("outcome reports it as a value", func(t *testing.T) {
		fr, exit := scopeFixture(t)
		defer exit()
		h := TaskSpawn(fr, func(*Frame) int64 { Trap(text); return 0 })
		o := TaskOutcomeOf(fr, h)
		if o.Tag != TagFailed || o.Failed.Tag != TagErrored {
			t.Fatalf("outcome is %+v, want Failed(Errored(...))", o)
		}
		// Verbatim, including the `line N:` prefix. The report is the body's
		// own fault text with nothing wrapping it, so a wrapper here
		// would be a differing output rather than a differing structure.
		if o.Failed.Msg != text {
			t.Errorf("failure message is %q, want %q exactly", o.Failed.Msg, text)
		}
	})
	t.Run("await inherits it out of band", func(t *testing.T) {
		fr, exit := scopeFixture(t)
		defer exit()
		h := TaskSpawn(fr, func(*Frame) int64 { Trap(text); return 0 })
		err := recoverFault(func() { TaskAwait(fr, h) })
		if err == nil {
			t.Fatal("awaiting a failed task returned normally; the failure channel is out of band and must fail the awaiter")
		}
		if err.Msg != text {
			t.Errorf("the propagated fault is %q, want %q — the same text running the body inline would produce", err.Msg, text)
		}
	})
}

// TestConcurrent_CancelledSleepUnwindsAndReportsCancelled is the property
// `TimerSleep`'s raise exists for, and it is the one a corpus fixture would
// report as a mere timing difference.
//
// The discriminator is the side effect. A runtime whose sleep returned Unit
// would let the body complete and report Completed, which the tag check
// catches. What a tag check alone would miss is a raise that happens after the
// body ran its effect, so the flag is checked too.
func TestConcurrent_CancelledSleepUnwindsAndReportsCancelled(t *testing.T) {
	fr, exit := scopeFixture(t)
	defer exit()

	var ran bool
	var mu sync.Mutex
	h := TaskSpawn(fr, func(inner *Frame) int64 {
		TimerSleep(inner, Duration(time.Hour))
		mu.Lock()
		ran = true
		mu.Unlock()
		return 1
	})
	TaskCancel(h)

	o := TaskOutcomeOf(fr, h)
	if o.Tag != TagCancelled {
		t.Fatalf("outcome is %+v, want Cancelled — a cancelled sleep must unwind, not return Unit", o)
	}
	mu.Lock()
	defer mu.Unlock()
	if ran {
		t.Error("the statement AFTER the cancelled sleep executed, so the sleep returned instead of unwinding")
	}
}

// TestConcurrent_ScopeExitCancelsAndWaitsForUnawaitedTasks is the corpus's own
// shape, in Go: `nested_outer_body` binds two five-second sleepers and never
// awaits them, so the block completes normally with both in flight.
//
// Two assertions, because each catches a different half:
//   - ScopeExit returned, and the tasks had settled by then. A cancel with no
//     wait passes an outcome check and leaves a goroutine behind — which a
//     synctest bubble reports as leftover work and a plain test does not.
//   - It took no real time. A wait with no cancel would sit out the hour.
func TestConcurrent_ScopeExitCancelsAndWaitsForUnawaitedTasks(t *testing.T) {
	fr := EnterScope(NewFrame(context.Background()))
	h := TaskSpawn(fr, func(inner *Frame) int64 {
		TimerSleep(inner, Duration(time.Hour))
		return 1
	})
	start := time.Now()
	ScopeExit(fr)
	elapsed := time.Since(start)

	// Settled, read without waiting: the outcome is available immediately
	// because ScopeExit already waited. A non-blocking read is what makes this
	// an assertion about ScopeExit rather than about TaskOutcomeOf.
	select {
	case <-h.t.done:
	default:
		t.Fatal("ScopeExit returned with a task still running, so it cancelled without waiting")
	}
	if h.t.tag != TagCancelled {
		t.Errorf("the abandoned task settled as tag %d, want TagCancelled (%d)", h.t.tag, TagCancelled)
	}
	if elapsed > 5*time.Second {
		t.Errorf("ScopeExit took %s, so it waited out the sleep instead of cancelling first", elapsed)
	}
}

// TestConcurrent_AwaitAllShortCircuitsOnAFailureAndNotOnAnErr is std's rule, and
// the two halves are what make it a rule rather than a behaviour.
//
// The short-circuit half is timed: item 0 sleeps for an hour and item 1 fails at
// once, so a loop that awaited in order would not return. The not-on-err half
// uses a body that returns a value — the stand-in for `Err(e)`, which
// is an ordinary value at this level — and asserts every element arrives.
func TestConcurrent_AwaitAllShortCircuitsOnAFailureAndNotOnAnErr(t *testing.T) {
	t.Run("a failure short-circuits", func(t *testing.T) {
		fr := EnterScope(NewFrame(context.Background()))
		defer ScopeExit(fr)
		slow := TaskSpawn(fr, func(inner *Frame) int64 {
			TimerSleep(inner, Duration(time.Hour))
			return 1
		})
		quick := TaskSpawn(fr, func(*Frame) int64 { Trap("boom"); return 0 })
		batch := listFromSlice([]Task[int64]{slow, quick})

		start := time.Now()
		err := recoverFault(func() { TaskAwaitAll(fr, batch) })
		elapsed := time.Since(start)

		if err == nil {
			t.Fatal("await_all over a failing batch returned normally")
		}
		if err.Msg != "boom" {
			t.Errorf("propagated %q, want the failing task's own text", err.Msg)
		}
		// The positive that makes this a short-circuit: it did not wait for
		// item 0, which is first in the batch and sleeps for an hour.
		if elapsed > 5*time.Second {
			t.Errorf("await_all took %s, so it waited out the earlier task instead of short-circuiting", elapsed)
		}
	})
	t.Run("an ordinary value does not", func(t *testing.T) {
		fr := EnterScope(NewFrame(context.Background()))
		defer ScopeExit(fr)
		var hs []Task[int64]
		for i := int64(0); i < 3; i++ {
			n := i
			hs = append(hs, TaskSpawn(fr, func(*Frame) int64 { return n * 10 }))
		}
		got := listToSlice(TaskAwaitAll(fr, listFromSlice(hs)))
		if len(got) != 3 {
			t.Fatalf("await_all returned %d values, want 3", len(got))
		}
		// In spawn order, which is std's documented contract and the thing a
		// cons-list fold gets backwards if the reversal is dropped.
		for i, want := range []int64{0, 10, 20} {
			if got[i] != want {
				t.Errorf("result %d is %d, want %d — await_all must preserve spawn order", i, got[i], want)
			}
		}
	})
}

// TestConcurrent_StrayCancellationIsAFault demonstrates raiseCanceled's guard
// firing.
//
// The arm is unreachable from a correct lowering: a cancellation with no task
// and no deadline in force has no explanation. An arm never shown to fire
// cannot be told from one that cannot: this fires it directly, so the claim is
// that the arm is unreachable from the VM rather than dead.
func TestConcurrent_StrayCancellationIsAFault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fr := &Frame{ctx: ctx}
	err := recoverFault(func() { CancelIfDone(fr) })
	if err == nil {
		t.Fatal("a cancellation raised on a frame with no task returned normally; it must be a named fault rather than a bare panic")
	}
	if !strings.Contains(err.Msg, "no task to unwind") {
		t.Errorf("the fault is %q and does not say why", err.Msg)
	}
}

// TestConcurrent_ATaskOutlivesNeitherItsScopeNorItsSiblings is the
// leftover-goroutine property a synctest bubble enforces, asserted here so it is
// not only enforced inside a bubbled test.
//
// Counted rather than sampled: every task increments a counter under a mutex
// before returning, and the count is read after ScopeExit. A sampled check
// ("the last one finished") passes while an earlier one is still running.
func TestConcurrent_ATaskOutlivesNeitherItsScopeNorItsSiblings(t *testing.T) {
	const n = 32
	fr := EnterScope(NewFrame(context.Background()))
	var mu sync.Mutex
	done := 0
	for range n {
		TaskSpawn(fr, func(*Frame) Unit {
			mu.Lock()
			done++
			mu.Unlock()
			return Unit{}
		})
	}
	ScopeExit(fr)
	mu.Lock()
	defer mu.Unlock()
	if done != n {
		t.Errorf("%d of %d tasks had finished when ScopeExit returned", done, n)
	}
}

// recoverFault runs f and returns the Nomi fault it raised, or nil.
//
// Only an *Error is caught. A cancellation unwind or a Go panic escapes and
// fails the test with its traceback, which is what should happen: this helper
// exists to assert a fault, and swallowing everything would make "no fault" and
// "the wrong panic" indistinguishable.
func recoverFault(f func()) (err *Error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		e, isFault := r.(*Error)
		if !isFault {
			panic(r)
		}
		err = e
	}()
	f()
	return nil
}

// TestConcurrent_ABlockedChannelOperationObservesCancellation covers the two
// cancel arms in channel.go, a parked receive and a parked send. The corpus
// does not reach them. The one corpus case that blocks a receive under a
// deadline ("env context timeout cancels blocked channel receives") writes its
// program inside a triple-quoted string handed to `compiler.run`, so that
// receive runs in a nested program, not in the file under test. Nothing else in
// the corpus abandons a task parked on a channel.
//
// Both directions, because the two arms have different shapes: a receive parks
// on an empty open channel, and a send parks only once the buffer is full — a
// send with room takes SenderSend's non-blocking fast path, which has no cancel
// arm at all and must not grow one (see SenderSend's own comment on why the
// cancel arm is only in step 3).
func TestConcurrent_ABlockedChannelOperationObservesCancellation(t *testing.T) {
	t.Run("a parked receive unwinds", func(t *testing.T) {
		fr := EnterScope(NewFrame(context.Background()))
		ch := ChannelUnbuffered[int64]()
		var reached bool
		var mu sync.Mutex
		h := TaskSpawn(fr, func(inner *Frame) int64 {
			v := ReceiverReceive(inner, ch.Receiver)
			mu.Lock()
			reached = true
			mu.Unlock()
			return v.Some
		})
		// Nobody ever sends, so the task is parked. ScopeExit cancels and waits.
		ScopeExit(fr)

		if h.t.tag != TagCancelled {
			t.Errorf("a task parked on a receive settled as tag %d, want TagCancelled (%d)", h.t.tag, TagCancelled)
		}
		mu.Lock()
		defer mu.Unlock()
		if reached {
			t.Error("the statement after the parked receive executed, so the receive returned instead of unwinding")
		}
	})
	t.Run("a parked send unwinds", func(t *testing.T) {
		fr := EnterScope(NewFrame(context.Background()))
		// Capacity 1, filled before the task starts, so the task's send has to
		// park. A capacity-0 channel would be an unbuffered rendezvous, which
		// parks for a different reason and would not exercise step 3's release
		// of the close mutex.
		ch := ChannelBuffered[int64](1)
		if r := SenderSend(fr, ch.Sender, 1); r.Tag != TagOk {
			t.Fatalf("filling the buffer failed: %+v", r)
		}
		var reached bool
		var mu sync.Mutex
		h := TaskSpawn(fr, func(inner *Frame) int64 {
			SenderSend(inner, ch.Sender, 2)
			mu.Lock()
			reached = true
			mu.Unlock()
			return 0
		})
		ScopeExit(fr)

		if h.t.tag != TagCancelled {
			t.Errorf("a task parked on a full-buffer send settled as tag %d, want TagCancelled (%d)", h.t.tag, TagCancelled)
		}
		mu.Lock()
		defer mu.Unlock()
		if reached {
			t.Error("the statement after the parked send executed, so the send returned instead of unwinding")
		}
	})
	// The paired negative: a channel operation that can complete does complete,
	// even on a frame whose context is already cancelled. Nomi observes
	// cancellation where a task waits rather than poisoning work already
	// possible.
	//
	// `SenderSend` tries a non-blocking send first, in a `select` whose other
	// arm is `default`. There is no ctx arm on that path, so a send with room
	// completes deterministically. Only the send half is asserted here, and it
	// is asserted repeatedly: a single send would pass on a nondeterministic
	// send by luck.
	//
	// The receive half is not asserted. When a value is ready and the context
	// is done, the language leaves the answer open.
	t.Run("a send with room completes on a cancelled frame", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		fr := &Frame{ctx: ctx, inTask: true}
		ch := ChannelBuffered[int64](64)
		for i := range 64 {
			if r := SenderSend(fr, ch.Sender, int64(i)); r.Tag != TagOk {
				t.Fatalf("send %d returned %+v on a cancelled frame; a send with room must complete", i, r)
			}
		}
	})
}
