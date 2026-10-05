package rt

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// `concurrent { }` and `std/tasks` — the structured-concurrency runtime the VM
// calls.
//
// # The frame is the mechanism
//
// Cancellation has to reach `timer.sleep` inside a plain function called from
// a task body. frame.go says how: every activation carries a `*Frame`, and the
// Frame carries the `context.Context`. frame.go's own list of the places a new
// Frame belongs names this one — "`concurrent`/`spawn` (a task gets its own
// frame over the parent's context)" — and opaque.go's `TimerSleep` selects on
// `fr.Context().Done()`. So this file adds a field and two constructors to that
// mechanism rather than a threading pass.
//
// Three alternatives do not work, and each is what a reader will propose:
//
//   - **Thread ctx through every signature.** Correct and enormous: it changes
//     every call site, every sibling and stdlib signature, every impl method
//     and every lambda. It is also what the frame already is, so it would be a
//     second copy of it.
//   - **A goroutine-local keyed by goroutine id.** No public API exposes one.
//     `runtime.Stack` parsing is the usual trick and costs ~1µs per read; a
//     cancellation safe point at every function entry would then cost more
//     than the call itself. (Go's goids are monotone and never reused, so that
//     design would be correct; its cost rules it out.)
//   - **A package-level current-scope cell.** Wrong under concurrency by
//     construction: two sibling tasks would share one cell.
//
// # The unwind is a panic, which is the one Go mechanism for "STOP NOW"
//
// rt's operations return plain values with no error channel (trap.go's
// decision), so there is no return value a cancellation could ride. It
// therefore rides a panic, exactly as a Nomi fault does, and is recovered at
// the one boundary that can act on it: the task goroutine's own wrapper.
//
// A panicking task must not kill the process, and it is safe to swallow one
// here for a reason specific to Nomi rather than a general one: Nomi values are
// immutable, so a task that died partway cannot have left shared state
// half-written, and the one piece of mutable state a task can see — the app
// cell — is not reachable from a spawned task at all (appfield.go refuses that
// combination by name). So a failed task's effects are confined to what it
// printed, which is what `Failure` reports.
//
// # The failure taxonomy is std's
//
// `Completed | Cancelled | Failed(Panicked | Errored)`. An `Err` a body returned
// deliberately is `Completed(Err(e))` and is not a failure — std/tasks.nomi says
// so in as many words, and that distinction is why the enum exists. `Failed` is the
// unplanned.
//
// The mapping below is what a program observes:
//
//	Task.outcome on a body doing `1 / 0`  ->  Failed(Errored("line 7: division by zero"))
//	                                          program continues, exit 0
//	Task.await   on the same body         ->  `line 7: division by zero`, exit 1
//
// So `Errored` carries the fault text verbatim including its `line N:` prefix,
// and awaiting a failed task reproduces exactly what running the body inline
// would have printed. A Nomi fault is a panic carrying `*Error`, so `*Error`
// maps to `Errored` and any other panic to `Panicked`: `Panicked` is reserved
// for a Go-level panic, and nothing in Nomi source panics on demand, so an
// end-to-end program cannot exercise it.
//
// # Awaiting a task the caller cancelled
//
// std/tasks.nomi: "Awaiting a task you cancelled propagates the cancellation".
// Inside a task the awaiter unwinds and settles Cancelled; on the main line
// there is no owner above, so the program fails with
// AwaitedCancelledTaskText. See raiseAwaitedCancel.

// Scope is one `concurrent { }` block: the cancellation context its tasks
// observe, and the wait group that makes the block wait for them.
//
// Read-only after construction. `wg`,
// `cancel` and `ctx` are concurrency primitives the goroutines drive directly,
// and Go's own implementations are goroutine-safe, so there is no surrounding
// mutex — the two fields that do need one are `failed`/`firstFailure`, and they
// take a `sync.Once` rather than a mutex because they are written exactly once.
type Scope struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// failedOnce guards the pair below, which together are "some task in this
	// scope has failed, and here is which one failed first".
	//
	// It exists for `Task.await_all`, whose documented rule is that a failure
	// short-circuits while an `Err` does not: "a batch where item 3 fails
	// instantly does not wait out items 1 and 2 first". Watching every task
	// would otherwise need one watcher goroutine per task, which a synctest
	// bubble counts as leftover work.
	//
	// The cost of the shared channel rather than per-task watching is std's own
	// documented cost: "with two failures you get whichever happened first
	// rather than the lowest-indexed one".
	failedOnce   sync.Once
	failed       chan struct{}
	firstFailure Failure
}

// Context is the scope's cancellation carrier, for a caller that holds the
// scope rather than a frame.
func (s *Scope) Context() context.Context { return s.ctx }

// noteFailure records the first failure in this scope and wakes every
// `await_all` waiting on it.
//
// The write happens inside the Once and before the close, so a reader that
// observed the closed channel is guaranteed to see the value — the close is the
// happens-before edge, which is what makes `firstFailure` safe to read without
// a lock.
func (s *Scope) noteFailure(f Failure) {
	s.failedOnce.Do(func() {
		s.firstFailure = f
		close(s.failed)
	})
}

// EnterScope opens a `concurrent { }` block and returns the frame its body
// runs on.
//
// A child frame rather than a mutation of the parent, because the parent frame
// outlives the block and must not observe the block's cancellation afterwards.
// `forcing` and `app` are carried across for the reason frame.go gives them:
// they are dynamic context propagated caller-to-callee, and a `once` being
// forced through a `concurrent` block is still being forced, while a `with
// App.<field> = v` wrapping a block is still in force inside it.
//
// `inTask` is inherited rather than set: a block on the main goroutine is not
// inside a task, and that is what makes a stray cancellation raise a legible
// fault instead of a Go traceback. See raiseCanceled.
//
// `underDeadline` is inherited for the same reason. The block's ctx is a child
// of the parent's, so a deadline EnterDeadline put on the parent ends the
// block's ctx too, and the awaits in the body raise on this frame. Without the
// flag, a `with App.context = Context.with_timeout(…)` rebind wrapping a
// block would report the lowering-bug Trap instead of the deadline;
// TestEnterScope_InheritsDeadline pins it.
func EnterScope(parent *Frame) *Frame {
	ctx, cancel := context.WithCancel(parent.ctx)
	s := &Scope{ctx: ctx, cancel: cancel, failed: make(chan struct{})}
	return &Frame{ctx: ctx, forcing: parent.forcing, scopedFields: parent.scopedFields, scopedContext: parent.scopedContext,
		booted: parent.booted, scope: s, inTask: parent.inTask, underDeadline: parent.underDeadline, park: parent.park}
}

// ScopeExit ends a `concurrent { }` block: cancel every task still running,
// then wait for all of them.
//
// Cancel-then-wait on every path, normal and not. The normal path is not an
// exception: the derived ctx runs its deferred cleanup even when no goroutines
// are mid-flight, where `Wait()` is then a no-op. The corpus depends on it: `nested_outer_body` in concurrent_runtime_test.nomi binds its
// two five-second sleepers with `_ia = inner_a` and never awaits them, so the
// block completes normally with both in flight and the cancel here is the only
// thing that stops them.
//
// Called behind a Go `defer`, so it also runs while a panic unwinds — a `try`
// propagating out of the block, a Nomi fault, or a cancellation from an
// ancestor. The panic continues afterwards, so the exit signal is re-raised
// outward for free.
//
// Waiting is unbounded, deliberately. A task that never reaches a safe point
// hangs the block on every path; the `drain:` budget that bounds a supervisor's
// shutdown is a supervisor concept and a block has no equivalent, because a
// block's tasks are all awaited by Rule 2.
func ScopeExit(fr *Frame) {
	s := fr.scope
	if s == nil {
		// Not reachable from the VM: it calls this only behind an EnterScope
		// in the same function. A Trap rather than a nil dereference so a
		// caller defect names itself.
		Trap("concurrent: ScopeExit outside a block")
	}
	s.cancel()
	s.wg.Wait()
}

// --- the cancellation sentinel ----------------------------------------------

// canceled is the unwind a cancelled blocking operation raises.
//
// Its own type rather than an `*Error`, because the two are recovered by
// different code for different reasons: an `*Error` is a Nomi fault the VM
// reports and exits 1 on, and this is a cooperative unwind that a task
// wrapper turns into `Outcome.Cancelled`. A shared type would make the wrapper
// unable to tell "my task was cancelled" from "my task divided by zero", which
// is precisely the distinction std/tasks' enum exists to draw.
//
// Empty: there is nothing to carry. Which task was cancelled is known to the
// wrapper that recovers it, and a cancellation has no message because it is not
// a failure.
type canceled struct{}

// raiseCanceled unwinds the current task because its context ended.
//
// A raise is only correct where something up the stack recovers it, and the
// only recoverer is TaskSpawn's wrapper — so
// raising on a frame that is not inside a task would reach the top-level
// recover, which re-panics anything that is not an `*Error` in order to keep
// the Go traceback for a runtime bug. That is the right answer for a runtime
// bug and the wrong one for a program.
//
// # The `!inTask` arm has two causes
//
// A frame's ctx is closed by a Scope's `cancel` (only in ScopeExit, after its
// body has finished), by a Task's, or by `rt.EnterDeadline` (frame.go). The
// last closes the ctx on the main goroutine, while the body runs.
//
// Take an ordinary program: `with Prog.context =
// Context.with_timeout(Prog.context, Duration.milliseconds(50))` followed by
// `timer.sleep(Duration.seconds(300))`. The right report is
//
//	deadline exceeded: the context in force when `main` blocked ran out, so
//	the remaining work was not run
//
// and without the deadline arm the program would report `concurrent:
// cancellation reached a frame with no task to unwind`, a diagnostic about a
// lowering bug, to a user whose program was merely bounded. A corpus run cannot
// catch this, because it compares runs where every case passed.
//
// So the two causes are separated by `fr.underDeadline` and each gets its own
// answer. The lowering-bug Trap is kept rather than replaced: a cancellation
// that arrives with no task and no deadline in force still has no explanation,
// and TestConcurrent_StrayCancellationIsAFault constructs exactly that and must
// still see it. Reporting the deadline unconditionally would name a
// deadline that does not exist.
//
// The deadline arm also requires the ctx to have ended by its deadline. Inside
// a block the flag alone is not enough: awaiting a task that `Task.cancel`
// stopped raises here with the block's ctx still live, and naming the deadline
// then would report one that has not run out.
// MainDeadlineFault is the fault a program reports when a deadline its main
// line was given runs out while it blocks. `nomi run` and `compiler.run` spell
// it through this constant.
const MainDeadlineFault = "deadline exceeded: the context in force when `main` blocked " +
	"ran out, so the remaining work was not run"

func raiseCanceled(fr *Frame) {
	if !fr.inTask {
		if fr.underDeadline && errors.Is(fr.ctx.Err(), context.DeadlineExceeded) {
			// Nothing else can cancel main-line code, so a
			// cancellation arriving here is the deadline it was given. It has to
			// be reported or the program exits having silently skipped the rest
			// of its work.
			Trap(MainDeadlineFault)
		}
		Trap("concurrent: cancellation reached a frame with no task to unwind")
	}
	panic(canceled{})
}

// AwaitedCancelledTaskText is the fault a program reports when its main line
// awaits a task that was cancelled. One spelling, used by `Task.await` and
// `Task.await_all`.
func AwaitedCancelledTaskText() string {
	return "task cancelled: `main` awaited a task that was cancelled, so the remaining " +
		"work was not run; use `Task.outcome` to read a cancellation you asked for"
}

// raiseAwaitedCancel propagates the cancellation of a task the caller awaited.
//
// std/tasks.nomi: the cancellation "propagates up the ownership chain rather
// than being returned, the same way it would have surfaced had the body run
// inline". Inside a task that is the ordinary unwind, and the awaiting task
// settles Cancelled. On the main line there is no owner above, so the program
// fails, and neither of raiseCanceled's two answers is the reason: nothing
// cancelled the caller's context and no deadline ran out — `Task.cancel` stopped
// the awaited task. A caller whose own context has ended (a deadline that also
// cancelled the task, say) is raiseCanceled's case and keeps its text.
func raiseAwaitedCancel(fr *Frame) {
	if !fr.inTask && fr.ctx.Err() == nil {
		Trap(AwaitedCancelledTaskText())
	}
	raiseCanceled(fr)
}

// CancelIfDone raises the cancellation unwind when fr's context has already
// ended, and does nothing otherwise.
//
// Exported for the blocking operations in this package that are declared in
// other files — `TimerSleep` in opaque.go, the channel pair in channel.go — so
// the raise has one implementation and its guard cannot be forgotten at a
// fourth call site.
func CancelIfDone(fr *Frame) {
	select {
	case <-fr.ctx.Done():
		raiseCanceled(fr)
	default:
	}
}

// --- Task ------------------------------------------------------------------

// task is one in-flight or completed task's state.
//
// `value`, `tag` and `fail` are written by the task goroutine before it closes
// `done`, and read by an awaiter only after receiving on `done`. The close is
// the happens-before edge, so no mutex is needed and the race detector agrees —
// which is asserted rather than assumed, see the `-race` fixtures in
// concurrent_test.go.
type task[T any] struct {
	done   chan struct{}
	cancel context.CancelFunc
	value  T
	fail   Failure
	tag    uint8
}

// Task is Nomi's `std/tasks.Task<T>`: a handle on work running in the enclosing
// `concurrent { }` block.
//
// A one-field struct over an unexported pointer, which is `Sender[T]`'s
// arrangement and is chosen for its two reasons: the field stays unreachable
// from another package, and the value is copyable so `rt.Task[int64]` can be
// the Go type a `stdGenHostSpecs` row names without every position having to
// spell a pointer.
type Task[T any] struct {
	t *task[T]
}

// TaskSpawn is `Task.spawn(body)`: run body as a parallel task in the frame's
// enclosing `concurrent { }` block.
//
// The goroutine, its four deferred actions and the body's frame are
// `spawnScopeTask`'s, shared with `Task.spawn_all`. `spawn` is that with no
// batch semaphore, which is the whole of the difference.
func TaskSpawn[T any](fr *Frame, body func(*Frame) T) Task[T] {
	s := fr.scope
	if s == nil {
		// Not reachable from checked source: the analyzer rejects a
		// `Task.spawn` with no `concurrent` ancestor (std/tasks.nomi documents
		// the rule). A Trap rather than a nil dereference so that if the
		// analyzer rule ever relaxes, the failure names itself.
		Trap("Task.spawn: no enclosing concurrent block")
	}
	return spawnScopeTask(fr, s, nil, body)
}

// spawnScopeTask enrols one task in a scope and returns its handle.
//
// Shared by `Task.spawn` and `Task.spawn_all`, which differ only in what they
// hand it: `spawn` a body closing over nothing and no limit, `spawn_all` a body
// that applies the caller's function to one item, plus a semaphore shared across
// the batch. It is shared because the four deferred actions below decide how a task's
// outcome is classified, and two copies of that classification are two things
// that can disagree about whether a Go panic is `Panicked` or `Errored`.
//
// # The goroutine's four deferred actions, in the order they run
//
// Go runs deferred functions LIFO, so they are registered in reverse. The order
// matters and each step depends on the one before it:
//
//  1. Recover, and classify. This must be innermost: it is what stops a task's
//     panic from killing the process, and it is where the outcome is decided.
//  2. `close(done)`, which publishes the outcome. After the recover, so an
//     awaiter never observes a `done` channel whose task has not settled.
//  3. `cancel()`, releasing the derived context. After the publish, because a
//     context leak is cheap and an unpublished outcome is a hang.
//  4. `wg.Done()`, which is what `ScopeExit` waits for. Outermost, so the block
//     cannot proceed until every step above has run.
//
// # The body's frame
//
// A fresh Frame over the task's context, not the scope's, so `Task.cancel` on
// this task alone reaches this body and no sibling's. `inTask` is set here and
// nowhere else: it is the fact that makes a cancellation raisable, and this
// wrapper is the thing that recovers it.
//
// # The semaphore, when there is one
//
// `sem` bounds how many of a batch run at once and is nil for a lone `spawn`.
// The worker takes a slot inside the goroutine and releases it when its body
// ends, so the spawn itself never blocks: `Task.spawn_all` hands every handle
// back at once and only execution is throttled. std/tasks.nomi states that
// contract ("`max_running` throttles execution, not enqueueing").
//
// A task still queued when the block unwinds never runs and settles
// `Cancelled` — starting work the block is about to cancel helps nobody. The
// select is on the task's `ctx`, which is the `fr.ctx` of the frame this body
// will run on, so this wait obeys the one deadline chokepoint like every other
// blocking operation in this package rather than reading a second clock.
func spawnScopeTask[T any](fr *Frame, s *Scope, sem chan struct{}, body func(*Frame) T) Task[T] {
	ctx, cancel := context.WithCancel(s.ctx)
	t := &task[T]{done: make(chan struct{}), cancel: cancel}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer close(t.done)
		defer func() {
			switch r := recover().(type) {
			case nil:
			case canceled:
				t.tag = TagCancelled
			case *Error:
				// A Nomi fault. `Errored` rather than `Panicked` because a Nomi
				// runtime error is reported that way (see the file
				// header), and because `Panicked` is std's name for
				// a bug rather than for arithmetic.
				t.tag = TagFailed
				t.fail = Errored(r.Msg)
			default:
				// A Go panic, which means a defect in rt or in the VM. It
				// is swallowed rather than allowed to kill the process for the
				// reason the file header gives, and it is reported as
				// `Panicked` so the two are distinguishable in an Outcome.
				t.tag = TagFailed
				t.fail = Panicked(fmt.Sprint(r))
			}
			if t.tag == TagFailed {
				s.noteFailure(t.fail)
			}
		}()
		if sem != nil {
			select {
			case sem <- struct{}{}:
				// Released ahead of the classification defer, which is
				// correct in both directions: on the normal path the slot
				// frees as soon as the body ends, and while a panic unwinds
				// the release runs first and the recover above still catches
				// it — a later-running defer recovers a panic just as an
				// earlier one would.
				defer func() { <-sem }()
			case <-ctx.Done():
				// `case nil:` above leaves this tag alone, so returning here
				// settles the task Cancelled without the body ever running.
				t.tag = TagCancelled
				return
			}
		}
		// `app` is carried from the spawn site's frame: a snapshot taken at
		// spawn. A block task keeps the spawner's deadline,
		// unlike a supervised one, because the block joins it — see
		// supervisorEnrol.
		t.value = body(&Frame{ctx: ctx, forcing: fr.forcing, scopedFields: fr.scopedFields, scopedContext: fr.scopedContext, booted: fr.booted, inTask: true, park: fr.park})
		t.tag = TagCompleted
	}()
	return Task[T]{t: t}
}

// TaskAwait is `Task.await(task)`: block until the task settles and return its
// value, inheriting anything that is not a completion.
//
// Two channels, which is std/tasks.nomi's own framing. The value channel is
// ordinary — an `Err` the body returned deliberately arrives here as a value and
// `try Task.await(t)` propagates it like any other Result. The failure channel
// is out of band: there is no `Err` to match on because the task never chose to
// produce one, so awaiting a failed task fails the awaiter with the fault the
// body raised. The report is exactly the body's own fault text, so
// `Trap(fail.Msg)` and not a wrapped one.
//
// A single await deliberately does not react to a sibling's failure. Only
// `await_all` short-circuits, because a caller holding one handle may be about
// to absorb that sibling's failure with `Task.outcome` — reacting here would
// take that decision away from them.
//
// The caller's own context is watched beside the task's `done`, which is how an
// ancestor's cancellation reaches an awaiter parked on a task that will never
// finish.
func TaskAwait[T any](fr *Frame, h Task[T]) T {
	t := h.t
	select {
	case <-t.done:
	case <-fr.ctx.Done():
		raiseCanceled(fr)
	}
	switch t.tag {
	case TagCompleted:
		return t.value
	case TagCancelled:
		// std/tasks.nomi: "awaiting a task you cancelled propagates the
		// cancellation for the same reason". See raiseAwaitedCancel for what
		// that means on the main line.
		raiseAwaitedCancel(fr)
	default:
		Trap(t.fail.Msg)
	}
	// Unreachable: every arm above either returns or panics. Go needs a
	// terminal statement and a zero value would be a wrong answer, so this is a
	// panic rather than a `return`.
	panic("unreachable")
}

// TaskOutcomeOf is `Task.outcome(task)`: wait for the task and hand back how it
// ended, rather than inheriting it.
//
// `outcome` is the primitive and `await` is this plus propagation of the two
// non-Completed cases. Both read the one recorded terminal state, so the pair
// cannot drift into two answers.
//
// The wait is identical to `await`'s. The distinction is what happens after it,
// not whether there is one.
func TaskOutcomeOf[T any](fr *Frame, h Task[T]) Outcome[T] {
	t := h.t
	select {
	case <-t.done:
	case <-fr.ctx.Done():
		raiseCanceled(fr)
	}
	switch t.tag {
	case TagCompleted:
		return Completed(t.value)
	case TagCancelled:
		return Cancelled[T]()
	default:
		return Failed[T](t.fail)
	}
}

// TaskCancel is `Task.cancel(task)`: ask one task to stop, leaving its siblings
// alone.
//
// It does not release the caller from awaiting — Rule 2 still holds, so there
// are still no orphan tasks — but the await returns promptly instead of waiting
// for work that will never finish.
//
// Idempotent, and a no-op on a task that already finished. Both fall out of
// `context.CancelFunc`'s own semantics.
func TaskCancel[T any](h Task[T]) Unit {
	h.t.cancel()
	return Unit{}
}

// TaskSpawnAll is `Task.spawn_all(source, f, max_running:)`: spawn one task per
// item of `source`, running at most `maxRunning` of them at once, and hand back
// a handle for each.
//
// # Terminal, not lazy
//
// The source is drained completely before the first handle is returned,
// because a lazy sequence of tasks would spawn after its block had exited, with the bodies never running. So this consumes the whole
// `Iter` and returns a materialized `List<Task<U>>`, and it inherits
// `Iter.to_list`'s infinite-source hazard — `max_running` throttles execution,
// not enqueueing, so memory still grows on an endless source. std/tasks.nomi
// says exactly that and it is not a gap here.
//
// # The drain runs on the caller's frame, the bodies do not
//
// `src.Run(fr, ...)` is the caller's own work: a `Seq` is a push pipeline whose
// stages are ordinary functions, so draining it must see the caller's frame and
// its cancellation, exactly as `rt.SeqToListCells` does. Each body then runs on the
// per-task frame `spawnScopeTask` builds. That split is why
// `spawn_all` accepts a plain module function where `spawn` demands a lambda
// written at the call site: the frame a body needs is supplied here rather than
// captured at the call site, so there is no frame for a callback to get wrong.
//
// # One semaphore for the batch
//
// Sized by the limit and shared by every task, so the tasks contend with each
// other and with nothing else. Spawning stays eager — the caller gets all the
// handles at once — and only execution is throttled; `spawnScopeTask` holds a
// slot for the duration of one body.
//
// `maxRunning < 1` is a fault rather than a clamp. Clamping to 1 would run a
// program whose bound is meaningless.
func TaskSpawnAll[T, U any](fr *Frame, src Seq[T], f func(*Frame, T) U, maxRunning int64) *List[Task[U]] {
	s := fr.scope
	if s == nil {
		// Rule 1 rejects this statically — the analyzer requires a `concurrent`
		// ancestor — so this is TaskSpawn's guard for TaskSpawn's reason: if
		// that rule ever relaxes, the failure names itself instead of
		// dereferencing nil.
		Trap("Task.spawn_all: no enclosing concurrent block")
	}
	if maxRunning < 1 {
		Trap(fmt.Sprintf("spawn_all: max_running must be at least 1, got %d", maxRunning))
	}
	var items []T
	src.Run(fr, func(fr *Frame, item T) bool {
		items = append(items, item)
		return true
	})
	sem := make(chan struct{}, int(maxRunning))
	out := make([]Task[U], 0, len(items))
	for _, item := range items {
		// `item` is captured by value into this closure, which is what makes
		// each body see its own element. Go 1.22+ scopes a range variable per
		// iteration, but the parameter here is explicit rather than relying on
		// that: this closure outlives the loop by construction.
		out = append(out, spawnScopeTask(fr, s, sem, func(taskFr *Frame) U {
			return f(taskFr, item)
		}))
	}
	return listFromSlice(out)
}

// TaskAwaitAll is `Task.await_all(tasks)`: wait for every task and collect their
// values, in the order the tasks were spawned.
//
// An `Err` does not short-circuit and a failure does, which is std's rule and
// the reason this is not a loop over `TaskAwait`. What happens to the
// other tasks when item 3 returns `Err` is a policy a program should decide
// deliberately, so the Err arrives as a value; a failure settles the outcome, so
// waiting buys nothing and the batch surfaces it at once.
//
// The short-circuit reads the scope's first-failure record rather than watching
// each task, so it needs no watcher goroutine — see Scope.failedOnce, including
// the cost std documents for it.
//
// A `List` in, a `List` out, in spawn order. Nomi's List is a persistent cons
// cell built head-first, so the results are collected into a slice and folded
// back in reverse; `listFromSlice` is the one place that reversal lives.
func TaskAwaitAll[T any](fr *Frame, tasks *List[Task[T]]) *List[T] {
	handles := listToSlice(tasks)
	out := make([]T, 0, len(handles))
	for _, h := range handles {
		t := h.t
		// Two waits, and which one applies is decided by whether this frame is
		// inside a `concurrent` block.
		//
		// A block-owned batch short-circuits on the scope's failure: some task
		// in the scope died, it may or may not be this one, and either way the
		// batch is settled — so the caller inherits the first failure recorded.
		// That is `Task.await_all`'s stated rule and the reason it watches every
		// task rather than awaiting in order.
		//
		// A group-owned batch — handles from `Supervisor.spawn_all` — has no
		// scope, and none is required. std permits awaiting these outside any
		// block because nothing cancels them at block exit: the supervisor owns
		// them (16-concurrency/supervisors_test.nomi: "Safe for group-owned work
		// in a way it is not for block-owned tasks"). Trapping here with
		// `Task.await_all: no enclosing concurrent block` would reject a valid
		// program.
		//
		// There is nothing to short-circuit on in that case, and that is a fact
		// about supervisors rather than a degradation: a supervisor's failure
		// policy is its own (report, restart, or give up), so a sibling's death
		// never reaches the awaiter.
		if s := fr.scope; s != nil {
			select {
			case <-t.done:
			case <-s.failed:
				Trap(s.firstFailure.Msg)
			case <-fr.ctx.Done():
				raiseCanceled(fr)
			}
		} else {
			select {
			case <-t.done:
			case <-fr.ctx.Done():
				raiseCanceled(fr)
			}
		}
		switch t.tag {
		case TagCompleted:
			out = append(out, t.value)
		case TagCancelled:
			raiseAwaitedCancel(fr)
		default:
			Trap(t.fail.Msg)
		}
	}
	return listFromSlice(out)
}

// listToSlice flattens a cons list into a slice, so a batch can be walked
// forwards without recursion.
//
// Local to this file rather than exported from list.go: the only caller is
// TaskAwaitAll, and a public conversion pair would invite a caller to
// round-trip a list through a slice — which would lose the structural
// sharing list.go's header exists to preserve.
func listToSlice[T any](xs *List[T]) []T {
	n := 0
	if xs != nil {
		n = xs.Len
	}
	out := make([]T, 0, n)
	for c := xs; c != nil; c = c.Tail {
		out = append(out, c.Head)
	}
	return out
}

// listFromSlice folds a slice back into a cons list, preserving order.
//
// Backwards, because `Cons` prepends: walking forwards would reverse the batch,
// and `await_all`'s documented contract is "in the order the tasks were
// spawned".
func listFromSlice[T any](vs []T) *List[T] {
	var out *List[T]
	for i := len(vs) - 1; i >= 0; i-- {
		out = Cons(vs[i], out)
	}
	return out
}
