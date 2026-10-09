package rt

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Named task groups — `std/supervisors`, the owner for work that has to outlive
// the call that started it.
//
// In four places below the Go-idiomatic choice gives a wrong answer, not just
// a different style.
//
// # The frame is the threading, as it is for `concurrent`
//
// concurrent.go's header is the long version. A supervisor task needs its own
// cancellation context reaching `timer.sleep` inside a plain function called
// from the body, and `*Frame` already carries a `context.Context` into every
// call. So a supervised task body is `func(*Frame)` over a frame
// built from the supervisor's context, which is also — see the deadline note
// below — what detaches it from the spawner's deadline for free.
//
// # Four semantics where the Go-idiomatic choice is wrong
//
//  1. **`max_running:` is required, with no default.** A CPU-count default
//     answers "how much parallelism can this machine use", which is the wrong
//     question: the limit protects a downstream — a connection pool, a rate
//     limit — and a supervisor is reached from call sites all over the program,
//     so an omitted bound would mean unbounded forever rather than unbounded
//     for one call. Zero is rejected outright: a supervisor that accepts work
//     and never runs it is a deadlock dressed as configuration.
//
//  2. **The `shutdown_timeout:` budget is a grace period, not an unwind
//     window.** In-flight work runs to completion; only at expiry is the
//     remainder cancelled, and then abandoned rather than waited for.
//     Cancelling first would be wrong: a sleeping task sees a cancelled ctx at
//     its first safe point and unwinds without doing its work, so
//     `shutdown_timeout: 30s` would behave like `0` for exactly the work it
//     was meant to protect, and shutdown would be racy on whether a
//     just-spawned goroutine had reached a safe point. Abandoning at expiry is
//     what keeps the budget a ceiling: a CPU-bound task never reaches a safe
//     point, so waiting after expiry could hang shutdown forever.
//
//  3. **Restart is a disposition and the schedule is a separate `Backoff`.**
//     Whether to restart and how patiently are different questions; folding
//     them would make every caller write a schedule. `Permanent` is what makes
//     a task returning on its own count as a failure, and there is
//     deliberately no `permanent: Bool` beside `restart:`. Backoff is
//     mandatory and jittered: a worker against a downed database retrying
//     instantly burns every attempt in milliseconds and gives up exactly when
//     waiting was right, and without jitter a supervisor restarting many
//     identical tasks rebuilds the thundering herd.
//
//  4. **Every failure is reported, not just the give-up.** Failing the same way
//     four times is a different problem from failing four different ways, and
//     only the full sequence separates them; frequency is not reconstructible
//     from terminal events. Reports go to stderr.
//
// # One structural absence, which is not a simplification
//
// There is no supervision tree. Supervisors are a flat set. A Nomi task is a
// closure spawned imperatively rather than a declared child spec, so nothing
// records what a subtree contained and a parent that wanted to restart one
// could not. `boot()` is the language's only child spec and the process is the
// unit of restart — which is exactly why per-task restart is expressible here
// and group-level restart is not. The drain below is therefore one concurrent
// pass rather than a recursion.
//
// # The registry is process-wide
//
// A package-level registry serves one program per process. The one caller that
// needs per-run isolation is the test harness, and it takes it explicitly:
// `ResetSupervisors` drains and clears, and `runTest` calls it after every case
// — inside the virtual-time bubble when there is one, so a drain budget is
// spent in virtual time and no goroutine outlives the bubble.
//
// # Signal handling is not here, and that is a rule rather than a gap
//
// `RunOptions.HandleSignals` is set only by the CLI: a library must not install
// a process-wide handler. The same rule governs `GiveUp.Exit`, which records a
// fatal and lets the program's own exit path read it (SupervisorFatal) rather
// than calling os.Exit from a background goroutine.

// settleCheckInterval is how often a draining permanent supervisor asks whether
// all its work has parked. Only ever runs on the shutdown path, and only until
// the group settles or its budget expires.
const settleCheckInterval = 2 * time.Millisecond

// DefaultShutdownTimeout mirrors the declared default on `Supervisor.new` in
// std/supervisors.nomi, which is where a caller meets it. Long enough for an
// in-flight request or write to land, short enough that one wedged worker does
// not hold up a container restart.
//
// Exported for the same reason BackoffDefaultMaxRestarts is: it is the second
// encoding of a Nomi declaration's default, and the guard that holds the two
// equal has to be able to name it.
const DefaultShutdownTimeout Duration = Duration(5 * time.Second)

// parkTracker counts, for one supervisor, how many of its running tasks are in
// a wait with no scheduled wake.
//
// The drain reads it to stop waiting for work that cannot arrive. It is a count rather than a set because the
// only question asked of it is "are they all parked".
type parkTracker struct {
	live   atomic.Int64 // tasks past the semaphore and not yet finished
	parked atomic.Int64 // of those, how many are in a no-wake wait
}

// settled reports that every running task is parked. False when the group has
// no running tasks at all, which is the ordinary "finished" case the WaitGroup
// already covers — answering true there would make the drain return before the
// WaitGroup had seen the last completion.
func (p *parkTracker) settled() bool {
	if p == nil {
		return false
	}
	live := p.live.Load()
	return live > 0 && p.parked.Load() >= live
}

func (p *parkTracker) enter() {
	if p != nil {
		p.parked.Add(1)
	}
}

func (p *parkTracker) exit() {
	if p != nil {
		p.parked.Add(-1)
	}
}

// supervisor is the state behind a Supervisor value.
//
// Every field is fixed at construction. The primitives — WaitGroup, semaphore
// channel, context — are individually goroutine-safe and drive themselves, so
// the struct needs no mutex.
type supervisor struct {
	// ctx is cancelled when the supervisor drains. Derived from the registry
	// root, so cancelling the root reaches every one of them.
	ctx    context.Context
	cancel context.CancelFunc

	// reg is the registry this supervisor belongs to, so a `GiveUp.Exit` can
	// record the program-level failure it implies.
	reg *supervisorRegistry

	// wg counts enrolled tasks — incremented by the spawning goroutine before
	// its worker starts, so a `Supervisor.flush` that begins after the spawn
	// returned always sees the task. Adding inside the goroutine would leave a
	// window where the supervisor looks idle and a flush silently misses work.
	wg sync.WaitGroup

	// sem is the `max_running` semaphore: a buffered channel with one slot per
	// permitted concurrent task, held by a worker for the duration of its body.
	//
	// A channel rather than a worker pool because a parked goroutine blocked on
	// a chan send is durably blocked, which is precisely what testing/synctest
	// understands — the property that makes the whole surface testable under a
	// virtual clock.
	sem chan struct{}

	shutdownTimeout Duration
	maxRunning      int64

	restart Restart
	backoff Backoff
	giveUp  GiveUp

	park *parkTracker

	// diag is where a failure report goes: the diagnostic output of the frame
	// that created the supervisor (WithDiagnosticOutput).
	diag io.Writer
}

// Supervisor is Nomi's `std/supervisors.Supervisor`.
//
// A one-field struct over an unexported pointer, which is `Task[T]`'s and
// `Sender[T]`'s arrangement and is chosen for their two reasons: the field
// stays unreachable from outside rt, and the value is copyable, so
// `rt.Supervisor` is the Go type a `stdHostSpecs` row names without every
// position — a struct field, a parameter, a `Config{}` literal slot — having to
// spell a pointer.
type Supervisor struct {
	s *supervisor
}

// OpaqueText makes a Supervisor an Opaque value, so the VM can render one held
// in an erased position.
func (Supervisor) OpaqueText() string { return "Supervisor" }

// supervisorRegistry holds every supervisor the program created, plus the root
// context they all descend from.
type supervisorRegistry struct {
	mu sync.Mutex

	root       context.Context
	cancelRoot context.CancelFunc

	groups []*supervisor

	// drained guards against a second drain pass: the program drains after the
	// entry function returns, and a signal handler may have started one.
	drained bool

	// fatal is set when a supervisor with `on_give_up: GiveUp.Exit` gave up.
	// The program has to fail, but the failure happens on a background
	// goroutine with no caller to return it to, so it is recorded and the
	// program's own exit path surfaces it.
	fatal error
}

var (
	supervisorMu  sync.Mutex
	supervisorReg *supervisorRegistry
)

// supervisors returns the process registry, creating it on first use.
func supervisors() *supervisorRegistry {
	supervisorMu.Lock()
	defer supervisorMu.Unlock()
	if supervisorReg == nil {
		ctx, cancel := context.WithCancel(context.Background())
		supervisorReg = &supervisorRegistry{root: ctx, cancelRoot: cancel}
	}
	return supervisorReg
}

// SupervisorNewExact is `std/supervisors`' module-private `new_exact`: build a
// supervisor with every policy stated.
//
// # Why the public `Supervisor.new` is a Nomi body over this
//
// `Supervisor.new` carries four trailing defaults, and internal/irbuild refuses a
// `host fn` with a trailing default outright — with a reason that is about
// which side owns the value, not about difficulty: an extern carries no
// defaults, so a declared default is documentation of whatever the Go
// implementation does with a short argument list, and a builder filling from
// the declaration would agree with a second encoding by luck. The precedent for the fix is `calendar.NaiveDateTime.new`: a `pub fn`
// with a Nomi body carrying the defaults, over a module-private `host fn` of
// full arity carrying none. So there is one owner for `shutdown_timeout:
// Duration = Duration.seconds(5)` and every caller reaches it.
//
// # Creatable only during `boot`, and the requirement is temporal
//
// `fr.inBoot` rather than a lexical check on the enclosing function's name. A
// name check would reject a server type creating its own supervisor in a
// constructor `boot` calls, which would cost a second app-struct field per
// server and make `Restart.Permanent` unusable, since one shared supervisor
// cannot be permanent for some of its work and not the rest. The rule is that
// supervisors are created while boot runs, not that the call is written inside
// it — and a frame flag propagated
// caller-to-callee is exactly that, because every call passes the frame down.
//
// analysis/boot_scope.go enforces the same rule statically through the in-file
// call graph. This is the backstop for the cross-module case its graph does not
// reach.
func SupervisorNewExact(
	fr *Frame,
	maxRunning int64,
	shutdownTimeout Duration,
	restart Restart,
	backoff Backoff,
	onGiveUp GiveUp,
) Supervisor {
	if maxRunning < 1 {
		Trap(fmt.Sprintf("Supervisor.new: max_running must be at least 1, got %d", maxRunning))
	}
	if shutdownTimeout < 0 {
		Trap("Supervisor.new: shutdown_timeout must not be negative, got " + DurationToString(shutdownTimeout))
	}
	if fr == nil || !fr.inBoot {
		Trap("a supervisor here would be a new one every time this runs, and each " +
			"is kept for the life of the program with a `max_running` of its " +
			"own — so the bound would be per call rather than per downstream. " +
			"Create it while `boot` builds the app value: in boot, or in " +
			"something boot calls")
	}

	reg := supervisors()
	reg.mu.Lock()
	defer reg.mu.Unlock()

	ctx, cancel := context.WithCancel(reg.root)
	g := &supervisor{
		ctx:             ctx,
		cancel:          cancel,
		reg:             reg,
		sem:             make(chan struct{}, int(maxRunning)),
		shutdownTimeout: shutdownTimeout,
		maxRunning:      maxRunning,
		restart:         restart,
		backoff:         backoffOrDefault(backoff),
		giveUp:          onGiveUp,
		park:            &parkTracker{},
		diag:            diagnosticOutput(fr.ctx),
	}
	reg.groups = append(reg.groups, g)
	return Supervisor{s: g}
}

// SupervisorSpawn is `Supervisor.spawn(supervisor, body)`: put work under a
// supervisor and return immediately.
//
// The task is enrolled when this returns, not finished, so a spawn past
// `max_running` never blocks the caller — it parks its own worker goroutine on
// the semaphore instead.
//
// It hands back a `Task[Unit]` like `Task.spawn` does but imposes no await
// obligation, because the supervisor drains it. The handle is an extra way to
// observe or cancel one task, not an obligation to.
//
// The body's frame is built over the supervisor's context, not the spawner's.
// Supervised work outlives the call that started it, and the ceiling that call
// was under is not its ceiling. A handler bounding itself is the ordinary
// shape for request work, and inheriting its deadline would kill the
// background task it had just spawned while `Supervisor.flush` reported
// success.
//
// The Go-context half needs nothing: the task's `ctx` descends from the
// supervisor's, which descends from the registry root, so the spawner's
// deadline is not on that chain at all.
//
// The published Context value also loses its deadline while retaining values.
// Other scoped fields inherit the spawn site's immutable snapshot unchanged.
//
// Structural cancellation is untouched: shutdown and `Task.cancel` still reach
// the task.
func SupervisorSpawn(fr *Frame, sup Supervisor, body func(*Frame) Unit) Task[Unit] {
	return supervisorEnrol(fr, sup, body)
}

// SupervisorSpawnAll is `Supervisor.spawn_all(supervisor, source, f)`: one task
// per item, under the same supervisor.
//
// No `max_running` of its own, which is std's decision and not an omission: a
// limit on this call would protect only the traffic that happened to arrive as
// a batch, leaving every ordinary `Supervisor.spawn` unbounded — which is not
// what anyone means by bounding a supervisor.
//
// A `List` in and a `List` of handles out, in source order. Bodies return Unit,
// as all supervised work does, so the handles come back for cancellation or for
// `Task.await_all` on this batch rather than the supervisor's whole workload.
func SupervisorSpawnAll[T any](fr *Frame, sup Supervisor, source *List[T], f func(*Frame, T) Unit) *List[Task[Unit]] {
	items := listToSlice(source)
	handles := make([]Task[Unit], 0, len(items))
	for _, item := range items {
		handles = append(handles, supervisorEnrol(fr, sup, func(taskFr *Frame) Unit {
			return f(taskFr, item)
		}))
	}
	return listFromSlice(handles)
}

// supervisorEnrol is the shared half of `spawn` and `spawn_all`: one task,
// enrolled and running under the supervisor's policy.
func supervisorEnrol(fr *Frame, sup Supervisor, body func(*Frame) Unit) Task[Unit] {
	g := sup.s
	if g == nil {
		// Not reachable from lowered Nomi: a Supervisor value exists only
		// because SupervisorNewExact made one. A Trap rather than a nil
		// dereference so a producer defect names itself.
		Trap("Supervisor.spawn: supervisor has no runtime state")
	}
	// Per-task cancellable context derived from the supervisor's, so
	// `Task.cancel` stops this task alone while a drain (or a signal reaching
	// the registry root) still stops all of them.
	taskCtx, taskCancel := context.WithCancel(g.ctx)
	t := &task[Unit]{done: make(chan struct{}), cancel: taskCancel}

	// Enrol before returning. See supervisor.wg.
	g.wg.Add(1)

	forcing := (*forcing)(nil)
	var taskFields map[string]any
	var booted *bootedApp
	var capture *Capture
	taskContext := ContextWithoutDeadline(ActiveContext(fr))
	if fr != nil {
		forcing = fr.forcing
		taskFields = fr.scopedFields
		booted = fr.booted
		capture = fr.capture

	}

	go func() {
		defer g.wg.Done()
		defer close(t.done)
		defer taskCancel()

		// Wait for a slot. A task queued behind `max_running` that is still
		// waiting when the supervisor drains never runs at all, which is the
		// right answer: the alternative is starting work during shutdown that
		// shutdown is about to abandon.
		select {
		case g.sem <- struct{}{}:
			defer func() { <-g.sem }()
		case <-taskCtx.Done():
			t.tag = TagCancelled
			return
		}

		g.park.live.Add(1)
		defer g.park.live.Add(-1)

		g.runWithRestarts(t, body, taskCtx, forcing, taskFields, taskContext, booted, capture)
	}()

	return Task[Unit]{t: t}
}

// runWithRestarts runs a task body and, when it fails, applies the supervisor's
// restart policy until the policy gives up.
//
// The loop is the whole of restart: re-invoking the closure is all "restart"
// means here, because the closure is the only description of the work that
// exists. There is nothing else to rebuild — and that is also why restart
// rebuilds whatever state the closure sets up.
//
// Only a failure re-enters the loop. A task that returned normally is done, and
// a cancelled one is being shut down; restarting either would fight the thing
// that stopped it.
//
// Every restart inherits the spawn site's fields and detached Context, so it
// reads the same configuration as the first attempt.
func (g *supervisor) runWithRestarts(t *task[Unit], body func(*Frame) Unit, taskCtx context.Context, forcing *forcing, fields map[string]any, scopedContext Context, booted *bootedApp, capture *Capture) {
	// attempt counts failures across the task's whole life and is what
	// `max_restarts` refers to.
	//
	// backoffStep is tracked separately because a healthy run resets the delay
	// without forgiving the count: a worker that fails once a month should not
	// inherit last month's backoff, but it should still eventually be declared
	// dead rather than retried forever.
	attempt := int64(0)
	backoffStep := 0
	// When this run of trouble started. A healthy run resets it, so the elapsed
	// budget covers one bout rather than the process lifetime.
	var troubleStarted time.Time

	for {
		runStart := time.Now()
		taskFrame := &Frame{ctx: taskCtx, forcing: forcing, scopedFields: fields, scopedContext: &scopedContext, booted: booted, inTask: true, park: g.park, capture: capture}
		outcome, failure := runSupervisedBody(body, taskFrame)
		ranFor := Duration(time.Since(runStart))

		switch outcome {
		case supervisedCancelled:
			// Cancelled is not "returned on its own", so a permanent task
			// stopped by a drain or by `Task.cancel` is not a failure. That is
			// what keeps shutdown clean.
			t.tag = TagCancelled
			return
		case supervisedReturned:
			if restartsOnReturn(g.restart) && taskCtx.Err() == nil {
				// A permanent task returning is the failure the disposition
				// exists to name — nothing else can tell it apart from a
				// one-shot task finishing, so without it a worker whose loop
				// exits early disappears in silence.
				//
				// The context check is load-bearing and the return value alone
				// cannot replace it. A cancelled body does not reliably
				// propagate: the idiomatic `fn serve(): Unit { _f =
				// Iter.loop(...); Unit }` swallows the unwind into a discarded
				// binding and returns Unit, so the task looks like it finished
				// on its own. Asking the context asks the runtime what
				// happened rather than the body, and only the runtime cannot
				// be written around.
				//
				// Synthesized as an ordinary Errored failure so it reports and
				// restarts through exactly the same path as any other, rather
				// than needing a fourth outcome.
				failure = Errored("permanent task returned on its own; work under this supervisor is expected to run until it is stopped")
				break
			}
			t.tag = TagCompleted
			return
		}

		attempt++

		// A run that outlasted the delay ceiling counts as recovered: the next
		// failure starts over with a full count, clock and delay. Without this
		// a worker failing once a month would exhaust its restarts after ten
		// months and stay dead — the budgets are for a run of trouble, not for
		// the lifetime of the process.
		if ranFor > Duration(backoffCap) {
			backoffStep = 0
			attempt = 1
			troubleStarted = time.Time{}
		}
		if troubleStarted.IsZero() {
			troubleStarted = time.Now()
		}

		delay, again := g.decideRestart(attempt, backoffStep, time.Since(troubleStarted))

		// Report before acting, and report every failure rather than only the
		// give-up. See the file header, item 4.
		reportTaskFailure(g.diag, failure, attempt, delay, again)

		if !again {
			// Give-up. Record the failure on the handle so anyone holding it
			// sees what happened, then apply the supervisor's consequence.
			t.tag = TagFailed
			t.fail = failure
			if g.giveUp.Tag == TagExit {
				g.recordFatal(fmt.Errorf("background task %s and its supervisor gave up: %s",
					failureKindName(failure), failure.Msg))
			}
			return
		}
		backoffStep++

		// Wait out the backoff, cancellably. A task waiting for its next
		// attempt when shutdown begins is not restarted, and the delay never
		// consumes the drain budget: restarting work during shutdown is work
		// the shutdown is about to cancel anyway.
		timer := time.NewTimer(time.Duration(delay))
		select {
		case <-timer.C:
		case <-taskCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			t.tag = TagCancelled
			return
		}
	}
}

// supervisedOutcome is how one run of a supervised body ended, before the
// restart policy has been consulted.
type supervisedOutcome uint8

const (
	supervisedReturned supervisedOutcome = iota
	supervisedCancelled
	supervisedFailed
)

// runSupervisedBody runs one attempt and classifies how it ended.
//
// The classification is TaskSpawn's, and deliberately the same three arms in
// the same order: a `canceled` unwind is a cancellation, an `*Error` is a Nomi
// fault reported as `Errored`, and any other panic is a defect in rt or its
// caller reported as
// `Panicked`. Sharing the arms rather than the code because TaskSpawn writes
// its result into a `task[T]` under a different lifecycle; sharing the rule is
// what matters and TestSupervisorClassifiesLikeTaskSpawn asserts it.
func runSupervisedBody(body func(*Frame) Unit, fr *Frame) (outcome supervisedOutcome, failure Failure) {
	defer func() {
		switch r := recover().(type) {
		case nil:
		case canceled:
			outcome = supervisedCancelled
		case *Error:
			outcome, failure = supervisedFailed, Errored(r.Msg)
		default:
			outcome, failure = supervisedFailed, Panicked(fmt.Sprint(r))
		}
	}()
	body(fr)
	return supervisedReturned, Failure{}
}

// decideRestart answers the only question the restart loop asks: after this
// failure, do we go again, and after how long?
//
// `attempt` is 1 on the first failure. The second return is false for give up.
//
// A Backoff whose tag is not Exponential can only come from an rt caller
// hand-building one. It gives up rather than guessing a delay: a supervisor
// that stops retrying reports and carries on, where one that retries forever
// is a crash loop.
func (g *supervisor) decideRestart(attempt int64, backoffStep int, sinceFirstFailure time.Duration) (Duration, bool) {
	if !restartsAtAll(g.restart) {
		return 0, false
	}
	if g.backoff.Tag != TagExponential {
		return 0, false
	}
	// Either budget ending it is a give-up. Two stopping conditions rather than
	// one because "how many times" and "for how long" are different questions,
	// and which binds depends on how fast the failures come.
	if attempt > g.backoff.MaxRestarts || Duration(sinceFirstFailure) >= g.backoff.MaxElapsed {
		return 0, false
	}
	return backoffDelay(backoffStep), true
}

// backoffDelay is exponential from a second, doubling each step, levelling off
// at a minute, with jitter applied.
//
// Jitter is not optional. Without it a supervisor restarting fifty identical
// tasks retries them all at the same instant and reproduces the thundering herd
// the backoff exists to damp. This is the equal-jitter shape: half the delay is
// fixed and half is random, so the delay still grows predictably while the herd
// spreads.
//
// step is 0 for the first retry, so the first delay is `backoffFrom` clamped by
// the ceiling.
func backoffDelay(step int) Duration {
	delay := backoffFrom
	for i := 0; i < step && delay < backoffCap; i++ {
		delay *= 2
	}
	if delay > backoffCap {
		delay = backoffCap
	}
	if delay <= 0 {
		return 0
	}
	half := delay / 2
	return Duration(half + time.Duration(rand.Int64N(int64(half)+1)))
}

// failureKindName is `Failure`'s variant name, for a report.
func failureKindName(f Failure) string {
	if f.Tag == TagPanicked {
		return "panicked"
	}
	return "errored"
}

// reportTaskFailure is the runtime's diagnostic path for a failure in
// supervised work.
//
// Nobody is awaiting this work, so there is no caller to propagate to and
// swallowing it silently is the one option that is definitely wrong. `retrying`
// is what separates a report in a series from the last one.
//
// The text is Nomi-observable, including the em dash and the millisecond
// rounding, and the golden files record it.
func reportTaskFailure(w io.Writer, f Failure, attempt int64, retryIn Duration, retrying bool) {
	tail := "giving up"
	if retrying {
		tail = fmt.Sprintf("retrying in %s", time.Duration(retryIn).Round(time.Millisecond))
	}
	fmt.Fprintf(w, "nomi: background task %s (attempt %d): %s — %s\n",
		failureKindName(f), attempt, f.Msg, tail)
}

type diagnosticOutputKey struct{}

// WithDiagnosticOutput answers ctx carrying w as the writer the runtime's own
// diagnostics go to — a supervised task's failure report — for every frame
// built over it. A program run by a host that captures its error stream (a
// test, a recorder) passes that stream; without it they go to os.Stderr.
func WithDiagnosticOutput(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, diagnosticOutputKey{}, w)
}

// diagnosticOutput is the writer WithDiagnosticOutput put on ctx, or
// os.Stderr.
func diagnosticOutput(ctx context.Context) io.Writer {
	if ctx != nil {
		if w, ok := ctx.Value(diagnosticOutputKey{}).(io.Writer); ok && w != nil {
			return w
		}
	}
	return os.Stderr
}

// recordFatal handles `on_give_up: GiveUp.Exit` — the supervisor has decided
// this task will not run again, and that the program should not carry on
// without it.
//
// It fails the program rather than the supervisor. There is no subtree to tear
// down and nothing to escalate to, so the only rung above a supervisor is
// whatever supervises the process: exiting hands off to that, and the restart
// it performs re-runs `boot` and rebuilds the whole supervisor set — far more
// state cleared than a task restart could manage.
//
// It records rather than exits, which is the library rule the file header
// states: only a host that owns the process may end it, so the program's own
// exit path reads SupervisorFatal. The first fatal wins; later ones are
// already-reported failures on a program that is ending.
func (g *supervisor) recordFatal(err error) {
	if g.reg == nil {
		return
	}
	g.reg.mu.Lock()
	defer g.reg.mu.Unlock()
	if g.reg.fatal == nil {
		g.reg.fatal = err
	}
}

// SupervisorFlushBounded is `std/supervisors`' module-private `flush_bounded`:
// block until every task outstanding under the supervisor has finished, or
// until the bound runs out.
//
// It does not cancel, and that is the whole distinction from the
// `shutdown_timeout:` budget: shutdown cancels what is left when it expires,
// and this never cancels anything. It waits and reports.
//
// "Outstanding" means enrolled by the time you call: `spawn` enrols before it
// returns, so work started on an earlier line is always included, and work a
// flushed task spawns is waited for too. Work that arrives afterwards is not —
// this is a checkpoint, not a barrier that closes the supervisor.
//
// The bound and the ambient deadline end differently and that asymmetry is
// std's: the bound is the caller asking "did it finish?" and getting an answer
// either way, so it returns `TimedOut`; the frame's context is a ceiling on the
// surrounding work, so exceeding it unwinds.
//
// The public `Supervisor.flush` is the Nomi body over this that supplies
// `bound: Wait = Wait.Forever`. See SupervisorNewExact for why.
func SupervisorFlushBounded(fr *Frame, sup Supervisor, bound Wait) FlushOutcome {
	g := sup.s
	if g == nil {
		Trap("Supervisor.flush: supervisor has no runtime state")
	}

	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	var expired <-chan time.Time
	if bound.Tag == TagUpTo {
		timer := time.NewTimer(time.Duration(bound.UpTo))
		defer func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}()
		expired = timer.C
	}

	var ctxDone <-chan struct{}
	if fr != nil && fr.ctx != nil {
		ctxDone = fr.ctx.Done()
	}

	// The waiter is parked for the parkTracker's purposes: a flush that blocks
	// inside a supervised task must not make a draining permanent supervisor
	// look busy. Nil-safe, and nil for a flush called outside any task.
	var park *parkTracker
	if fr != nil && fr.inTask {
		park = g.park
	}
	park.enter()
	defer park.exit()

	select {
	case <-done:
		return FlushOutcome{Tag: TagFlushed}
	case <-expired:
		// Nothing is cancelled: the work carries on, and shutdown's drain is
		// still what eventually bounds it.
		return FlushOutcome{Tag: TagTimedOut}
	case <-ctxDone:
		raiseCanceled(fr)
	}
	panic("unreachable")
}

// DrainSupervisors shuts every supervisor down: give each one up to its own
// budget to finish what it is doing, then cancel and abandon the rest.
//
// They drain concurrently, so a 30-second budget and a 200-millisecond one do
// not queue behind each other — the whole shutdown takes as long as the longest
// single budget, not their sum. That is what the flat set buys.
//
// An abandoned task may still write after this returns. That is the direct
// consequence of not waiting for it, and it is the cost of item 2 in the file
// header rather than a bug.
//
// Idempotent.
func DrainSupervisors() {
	reg := supervisors()
	reg.mu.Lock()
	if reg.drained {
		reg.mu.Unlock()
		return
	}
	reg.drained = true
	groups := make([]*supervisor, len(reg.groups))
	copy(groups, reg.groups)
	cancelRoot := reg.cancelRoot
	reg.mu.Unlock()

	var wg sync.WaitGroup
	for _, g := range groups {
		wg.Add(1)
		go func(g *supervisor) {
			defer wg.Done()
			g.drainOnce()
		}(g)
	}
	wg.Wait()

	// Release the root last. Its only remaining job was to parent the
	// supervisor contexts, which are all cancelled by this point.
	cancelRoot()
}

// drainOnce gives one supervisor its grace period, then stops it.
func (g *supervisor) drainOnce() {
	// Cancel on every path out, including the one where it went idle on its
	// own: the context is a child of the registry root and would otherwise stay
	// registered on it.
	defer g.cancel()

	if g.shutdownTimeout <= 0 {
		// A zero budget is a legitimate choice — stop now, wait for nobody. The
		// deferred cancel is the whole shutdown.
		return
	}

	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(time.Duration(g.shutdownTimeout))
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	// A permanent supervisor's work is declared never to finish, so `done` may
	// never close and the budget would be spent in full on every clean
	// shutdown — five seconds to stop a message-loop server that was sitting
	// idle on its inbox.
	//
	// Cancelling permanent supervisors first is wrong for the header's item-2
	// reason: the grace period is what lets work spawned just before shutdown
	// run at all. So instead of shortening the wait, end it when there is
	// nothing left to wait for: every running task parked with no scheduled
	// wake. A task sleeping on a timer is not parked and still gets its full
	// budget, because work is genuinely still coming.
	//
	// Scoped to permanent supervisors deliberately. Everywhere else the budget
	// already terminates on its own, so there is nothing to fix and no reason
	// to take on the one case this cannot see: a parked task that another
	// supervisor would have woken.
	var settle <-chan time.Time
	if restartsOnReturn(g.restart) {
		ticker := time.NewTicker(settleCheckInterval)
		defer ticker.Stop()
		settle = ticker.C
	}

	for {
		select {
		case <-done:
			// Everything finished inside the budget — the good case, and the
			// one that keeps shutdown fast rather than always costing the full
			// grace period.
			return
		case <-timer.C:
			// Budget spent. The deferred cancel asks the stragglers to stop and
			// we do not wait to see whether they do.
			return
		case <-settle:
			// Nil channel for every non-permanent supervisor, so this arm never
			// fires there and the select is the plain two-way one.
			if g.park.settled() {
				return
			}
		}
	}
}

// ResetSupervisors drains every supervisor and clears the registry, so the next
// one created starts from a fresh root context.
//
// For the test harness, and it is the one thing a package-level registry needs
// to isolate runs. Each test case boots its
// own app value, so without this a supervisor built in one case's `boot` would
// still be holding live goroutines while the next case ran. That is a leak in
// any mode and a hard error under testing/synctest, where a bubble reports
// "main bubble goroutine has exited but blocked goroutines remain".
//
// Call it inside a bubble when there is one, so the drain budgets are spent in
// virtual time rather than real. runTest does.
func ResetSupervisors() {
	DrainSupervisors()
	supervisorMu.Lock()
	defer supervisorMu.Unlock()
	supervisorReg = nil
}

// SupervisorFatal returns the failure recorded by a supervisor whose
// `on_give_up` is `GiveUp.Exit`, or nil.
//
// A program's exit path consults it after the entry function returns, so the
// program exits non-zero. Reading it does not clear it.
func SupervisorFatal() error {
	reg := supervisors()
	reg.mu.Lock()
	defer reg.mu.Unlock()
	return reg.fatal
}

// SupervisedPark and SupervisedUnpark bracket a wait with no scheduled wake, so
// a draining permanent supervisor can tell "still working" from "nothing left
// to wait for".
//
// Exported for the blocking operations in this package that are declared in
// other files — the channel pair in channel.go — for CancelIfDone's reason: the
// bracket has one implementation and its pairing cannot be forgotten at a
// fourth call site. A nil frame or a frame outside a task is a no-op, which is
// what makes them safe to call unconditionally.
func SupervisedPark(fr *Frame) {
	if p := supervisorParkOf(fr); p != nil {
		p.enter()
	}
}

// SupervisedUnpark is SupervisedPark's other half.
func SupervisedUnpark(fr *Frame) {
	if p := supervisorParkOf(fr); p != nil {
		p.exit()
	}
}

// supervisorParkOf is the tracker a frame's task belongs to, or nil.
//
// Two conditions, and the `inTask` half is not redundant: `Frame` is copied by
// value at EnterBoot and by field at EnterScope, so a nested `concurrent` block
// inside a supervised task carries the tracker onwards, which is wanted — a
// task blocked inside a nested block is still parked. What is not wanted is a
// tracker surviving onto a frame nobody supervises, and `inTask` is the fact
// that separates them.
func supervisorParkOf(fr *Frame) *parkTracker {
	if fr == nil || !fr.inTask {
		return nil
	}
	return fr.park
}
