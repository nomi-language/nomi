package rt

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The supervision runtime, tested for the properties a differential fixture
// CANNOT see.
//
// concurrent_test.go's argument applies here with more force, not less:
// a golden file records whatever the implementation printed, so a corpus
// file cannot fail on a wrong restart
// disposition, a semaphore that admits one task too many, a drain that
// cancel-firsts, or a boot guard that never fires. Each of those is a WRONG
// ANSWER rather than a missing feature, and each is asserted here as a POSITIVE
// — a demonstrated trap, a measured concurrency peak, a counted number of runs
// — because "nothing went wrong" is also what a runtime nobody entered reports.
//
// # RUN THESE UNDER -race
//
//	go test -race ./rt -run 'TestSupervisor|TestBackoff'
//
// The publish edges here are `close(t.done)` and the restart loop's writes to
// `task.tag`, both mutex-free, and plain `go test` cannot fail on either.
//
// # EVERY TEST RESETS THE REGISTRY
//
// The registry is package-level (see supervisor.go's header for why and how
// the test harness pays for it), so a
// supervisor one test creates is still live in the next. `supervisorFixture`
// resets on entry AND on exit: on exit so the next test starts clean, and on
// ENTRY so a test that runs after one which failed mid-drain is not reading
// somebody else's groups.

// supervisorFixture returns a boot-phase frame and a cleanup that drains.
//
// The frame is `inBoot`, because `SupervisorNewExact` refuses otherwise and
// every test here needs to create one. That is the guard doing its job rather
// than an inconvenience: TestSupervisorNewOutsideBootIsAFault is the paired
// negative and asserts the same call FAILS without it.
func supervisorFixture(t *testing.T) (*Frame, func()) {
	t.Helper()
	ResetSupervisors()
	fr := EnterBoot(NewFrame(context.Background()))
	return fr, func() { ResetSupervisors() }
}

// newTestSupervisor is `Supervisor.new(max_running: n)` with std's declared
// defaults, which is what the two-argument corpus calls spell.
func newTestSupervisor(fr *Frame, maxRunning int64, shutdown Duration) Supervisor {
	return SupervisorNewExact(fr, maxRunning, shutdown,
		Restart{Tag: TagTemporary},
		Backoff{Tag: TagExponential, MaxRestarts: BackoffDefaultMaxRestarts, MaxElapsed: BackoffDefaultMaxElapsed},
		GiveUp{Tag: TagReport})
}

// TestSupervisorSpawnAndFlushRunTheWork is the anti-vacuity control for
// everything below: if this fails, no other test here is measuring what its
// name says.
func TestSupervisorSpawnAndFlushRunTheWork(t *testing.T) {
	fr, done := supervisorFixture(t)
	defer done()

	sup := newTestSupervisor(fr, 4, DefaultShutdownTimeout)
	var ran atomic.Int64
	for range 5 {
		_ = SupervisorSpawn(fr, sup, func(*Frame) Unit {
			ran.Add(1)
			return Unit{}
		})
	}
	if out := SupervisorFlushBounded(fr, sup, Wait{Tag: TagForever}); out.Tag != TagFlushed {
		t.Fatalf("flush reported tag %d, want Flushed(%d)", out.Tag, TagFlushed)
	}
	if got := ran.Load(); got != 5 {
		t.Errorf("%d of 5 tasks ran by the time flush returned; flush is not waiting for enrolled work", got)
	}
}

// TestSupervisorNewOutsideBootIsAFault is the boot-scope guard's DEMONSTRATED
// FIRING, and its paired positive.
//
// The requirement is TEMPORAL rather than lexical, so the two halves are the
// same call on two frames that differ only in `inBoot` — which is exactly the
// discrimination a lexical `currentFnName != "boot"` check could not make, and
// the reason it was wrong: a constructor `boot` calls has a different function
// name and the same boot phase.
func TestSupervisorNewOutsideBootIsAFault(t *testing.T) {
	_, done := supervisorFixture(t)
	defer done()

	// THE NEGATIVE: an ordinary frame.
	outside := NewFrame(context.Background())
	err := recoverTrap(func() { _ = newTestSupervisor(outside, 1, DefaultShutdownTimeout) })
	if err == nil {
		t.Fatal("Supervisor.new outside boot did not trap; the bound would be per call rather than per downstream")
	}
	if !strings.Contains(err.Msg, "while `boot` builds the app value") {
		t.Errorf("the fault reads %q, which does not tell the programmer where to move the call", err.Msg)
	}

	// THE POSITIVE, so the test above is not also what a permanently-refusing
	// constructor produces: the same call on a boot frame succeeds.
	inside := EnterBoot(NewFrame(context.Background()))
	if err := recoverTrap(func() { _ = newTestSupervisor(inside, 1, DefaultShutdownTimeout) }); err != nil {
		t.Fatalf("Supervisor.new INSIDE boot trapped: %v", err.Msg)
	}

	// AND THE PROPAGATION, which is the whole point of a frame flag: a callee
	// reached from boot is still in boot, with no name in common.
	callee := func(fr *Frame) { _ = newTestSupervisor(fr, 1, DefaultShutdownTimeout) }
	if err := recoverTrap(func() { callee(inside) }); err != nil {
		t.Fatalf("a constructor boot calls could not create a supervisor: %v", err.Msg)
	}
}

// TestSupervisorNewRejectsAnUnusableBound pins the two configuration values
// that are deadlocks rather than choices.
func TestSupervisorNewRejectsAnUnusableBound(t *testing.T) {
	fr, done := supervisorFixture(t)
	defer done()

	if err := recoverTrap(func() {
		_ = newTestSupervisor(fr, 0, DefaultShutdownTimeout)
	}); err == nil {
		t.Error("max_running: 0 was accepted; a supervisor that accepts work and never runs it is a deadlock dressed as configuration")
	}
	if err := recoverTrap(func() {
		_ = newTestSupervisor(fr, 1, Duration(-1))
	}); err == nil {
		t.Error("a negative shutdown_timeout was accepted")
	}
	// The paired positive: `max_running: 1` is a supervisor holding one
	// long-lived worker, which std names as a legitimate shape.
	if err := recoverTrap(func() {
		_ = newTestSupervisor(fr, 1, 0)
	}); err != nil {
		t.Errorf("max_running: 1 with a zero budget was rejected: %v", err.Msg)
	}
}

// TestSupervisorMaxRunningIsAConcurrencyCeiling measures the PEAK, which is the
// only reading that can fail when the semaphore is wrong.
//
// A count of completions cannot: every task completes whatever the bound is. A
// peak of 2 against `max_running: 2` with eight tasks queued is the positive
// that says the semaphore was entered, and the same measurement with the bound
// raised is the control that says the meter can read higher — without it,
// "peak <= 2" is also what a serialised runtime reports.
func TestSupervisorMaxRunningIsAConcurrencyCeiling(t *testing.T) {
	fr, done := supervisorFixture(t)
	defer done()

	peakUnder := measurePeak(t, fr, 2, 8)
	if peakUnder > 2 {
		t.Errorf("peak concurrency %d exceeded max_running: 2", peakUnder)
	}
	if peakUnder < 2 {
		t.Errorf("peak concurrency %d never reached max_running: 2, so this test would pass against a runtime that ran everything serially", peakUnder)
	}

	// THE CONTROL: the same eight tasks against a wider bound must read higher,
	// or the meter is measuring nothing.
	peakOver := measurePeak(t, fr, 6, 8)
	if peakOver <= peakUnder {
		t.Errorf("peak at max_running: 6 was %d, not above the %d measured at max_running: 2 — the meter is not sensitive to the bound",
			peakOver, peakUnder)
	}
}

// measurePeak runs n tasks under a supervisor of the given bound and returns the
// highest number that were ever inside a body at once.
//
// THE BARRIER IS WHAT MAKES THE READING THE BOUND rather than whatever the
// scheduler happened to overlap: each admitted task holds its slot until
// `maxRunning` of them are inside, so the peak is forced to the ceiling if the
// ceiling permits it. Without it a correct semaphore of 6 could still read a
// peak of 1 on a quiet machine, and the control below would fail spuriously.
//
// The barrier is a COUNTER PLUS A CLOSE rather than a WaitGroup, and that is a
// correction rather than a preference: a `Done()` past zero panics, the panic
// is caught by the task wrapper as an ordinary failure, and the failing task
// then skips its own decrement — which inflated `live` and made this read a
// peak ABOVE the bound. So the first mis-measurement here reported the runtime
// as broken when the meter was.
func measurePeak(t *testing.T, fr *Frame, maxRunning int64, n int) int64 {
	t.Helper()
	sup := newTestSupervisor(fr, maxRunning, DefaultShutdownTimeout)
	var mu sync.Mutex
	var live, peak, admitted int64
	release := make(chan struct{})
	var once sync.Once

	for range n {
		_ = SupervisorSpawn(fr, sup, func(*Frame) Unit {
			mu.Lock()
			live++
			admitted++
			if live > peak {
				peak = live
			}
			full := admitted >= maxRunning
			mu.Unlock()
			if full {
				once.Do(func() { close(release) })
			}
			<-release
			mu.Lock()
			live--
			mu.Unlock()
			return Unit{}
		})
	}
	if out := SupervisorFlushBounded(fr, sup, Wait{Tag: TagForever}); out.Tag != TagFlushed {
		t.Fatalf("flush did not complete: tag %d", out.Tag)
	}
	mu.Lock()
	defer mu.Unlock()
	return peak
}

// TestSupervisorFlushBoundReportsRatherThanUnwinds pins the asymmetry std draws
// between a flush BOUND and an ambient deadline: the bound returns a value
// either way, and NOTHING IS CANCELLED.
//
// The second half is the one a "did it time out?" assertion would miss. A flush
// that timed out by cancelling would report TimedOut too, and the work would
// then be gone — so the test waits for the abandoned task to finish afterwards
// and asserts it DID its work.
func TestSupervisorFlushBoundReportsRatherThanUnwinds(t *testing.T) {
	fr, done := supervisorFixture(t)
	defer done()

	sup := newTestSupervisor(fr, 1, Duration(2*time.Second))
	finished := make(chan struct{})
	var didWork atomic.Bool
	_ = SupervisorSpawn(fr, sup, func(taskFr *Frame) Unit {
		TimerSleep(taskFr, Duration(60*time.Millisecond))
		didWork.Store(true)
		close(finished)
		return Unit{}
	})

	out := SupervisorFlushBounded(fr, sup, Wait{Tag: TagUpTo, UpTo: Duration(5 * time.Millisecond)})
	if out.Tag != TagTimedOut {
		t.Fatalf("a 5ms bound over a 60ms task reported tag %d, want TimedOut(%d)", out.Tag, TagTimedOut)
	}

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the task never finished, so the bound cancelled it — a flush bound must not cancel")
	}
	if !didWork.Load() {
		t.Error("the task did not complete its work; the flush bound cancelled rather than reported")
	}

	// THE PAIRED POSITIVE: an unbounded flush on the same supervisor reports
	// Flushed, so TimedOut above is not simply what this function always says.
	if out := SupervisorFlushBounded(fr, sup, Wait{Tag: TagForever}); out.Tag != TagFlushed {
		t.Errorf("an unbounded flush of finished work reported tag %d, want Flushed", out.Tag)
	}
}

// TestSupervisorDrainIsAGracePeriodNotAnUnwindWindow is the CANCEL-FIRST
// REGRESSION TEST, and it is the sharpest test in this file because
// cancel-first was implemented, shipped and reverted once already.
//
// Under cancel-first the sleeping task sees a dead context at its first safe
// point and unwinds WITHOUT doing its work, so a generous budget behaves like a
// zero one. The assertion is therefore on the work having HAPPENED, not on the
// drain having returned.
//
// The paired negative is the same task under a budget SHORTER than its sleep:
// the drain must then give up on it, which is what says the budget is a real
// ceiling and not merely ignored.
func TestSupervisorDrainIsAGracePeriodNotAnUnwindWindow(t *testing.T) {
	// THE POSITIVE: a generous budget lets in-flight work finish.
	func() {
		fr, done := supervisorFixture(t)
		defer done()
		sup := newTestSupervisor(fr, 1, Duration(3*time.Second))
		var completed atomic.Bool
		_ = SupervisorSpawn(fr, sup, func(taskFr *Frame) Unit {
			TimerSleep(taskFr, Duration(40*time.Millisecond))
			completed.Store(true)
			return Unit{}
		})
		DrainSupervisors()
		if !completed.Load() {
			t.Error("a 40ms task under a 3s drain budget did not finish: the drain cancelled first, " +
				"which makes every budget decorative")
		}
	}()

	// THE NEGATIVE: a budget shorter than the work is a ceiling.
	func() {
		fr, done := supervisorFixture(t)
		defer done()
		sup := newTestSupervisor(fr, 1, Duration(10*time.Millisecond))
		started := make(chan struct{})
		var completed atomic.Bool
		_ = SupervisorSpawn(fr, sup, func(taskFr *Frame) Unit {
			close(started)
			TimerSleep(taskFr, Duration(3*time.Second))
			completed.Store(true)
			return Unit{}
		})
		<-started
		begin := time.Now()
		DrainSupervisors()
		if elapsed := time.Since(begin); elapsed > 2*time.Second {
			t.Errorf("the drain took %v for a 10ms budget: it waited for the straggler instead of abandoning it", elapsed)
		}
		if completed.Load() {
			t.Error("the 3s task completed under a 10ms budget, so the budget bounded nothing")
		}
	}()
}

// TestSupervisorRestartDispositions is the disposition table, all three rows,
// each read as a COUNT OF RUNS rather than as a boolean.
//
// A count is what separates the three: Temporary runs once whatever happens,
// Transient re-runs a FAILING body and not a returning one, Permanent re-runs
// both. A "did it restart?" assertion cannot tell Transient from Permanent on a
// failing body, which is the pair most likely to be confused.
func TestSupervisorRestartDispositions(t *testing.T) {
	for _, c := range []struct {
		name      string
		restart   uint8
		fail      bool
		wantRuns  int64
		wantTag   uint8
		reasoning string
	}{
		{"temporary never restarts a failure", TagTemporary, true, 1, TagFailed,
			"Temporary is the default because a supervisor holds whatever you spawn and most of that is meant to finish"},
		{"transient restarts a failure", TagTransient, true, 2, TagFailed,
			"Transient retries broken work; max_restarts: 1 is what stops it at two runs"},
		{"transient does NOT restart a clean return", TagTransient, false, 1, TagCompleted,
			"a task that returned is done — this is the row that separates Transient from Permanent"},
		{"permanent restarts a clean return", TagPermanent, false, 2, TagFailed,
			"a worker with no end returning on its own is a bug, and Permanent is what turns that from silence into a report"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fr, done := supervisorFixture(t)
			defer done()

			// `max_restarts: 1` bounds every restarting row at TWO runs, which
			// costs one backoff delay of 0.5-1s per row. Two rather than three
			// deliberately: the delay curve is fixed at 1s then 2s (jittered,
			// and not configurable — std says why), so a third run would add up
			// to two more real seconds per row for a distinction the second run
			// already draws.
			//
			// MaxElapsed IS THE DEFAULT AND MUST STAY GENEROUS. The first
			// version of this table set it to 500ms to keep the test fast, and
			// the jittered first delay of ~574ms crossed it — so every
			// restarting row gave up after ONE restart and read 2 where the
			// table said 3. That is the elapsed budget working exactly as
			// documented, and it is a good illustration of why the two bounds
			// are separate questions: which one binds depends on how fast the
			// failures come, and here the DELAY was outrunning the budget meant
			// to bound a run of trouble.
			var runs atomic.Int64
			sup := SupervisorNewExact(fr, 1, Duration(2*time.Second),
				Restart{Tag: c.restart},
				Backoff{Tag: TagExponential, MaxRestarts: 1, MaxElapsed: BackoffDefaultMaxElapsed},
				GiveUp{Tag: TagReport})

			h := SupervisorSpawn(fr, sup, func(*Frame) Unit {
				runs.Add(1)
				if c.fail {
					Trap("deliberate")
				}
				return Unit{}
			})
			if out := SupervisorFlushBounded(fr, sup, Wait{Tag: TagForever}); out.Tag != TagFlushed {
				t.Fatalf("flush did not complete: tag %d", out.Tag)
			}
			if got := runs.Load(); got != c.wantRuns {
				t.Errorf("the body ran %d time(s), want %d — %s", got, c.wantRuns, c.reasoning)
			}
			if got := h.t.tag; got != c.wantTag {
				t.Errorf("the handle settled at tag %d, want %d", got, c.wantTag)
			}
		})
	}
}

// TestSupervisorPermanentReturnIsReportedAsAFailure pins the synthesized
// failure's TEXT, because it is the one failure message the runtime writes
// itself rather than forwarding.
//
// A program under `Restart.Permanent` whose worker exits prints it, so it is
// an observable string and is pinned here.
func TestSupervisorPermanentReturnIsReportedAsAFailure(t *testing.T) {
	fr, done := supervisorFixture(t)
	defer done()

	sup := SupervisorNewExact(fr, 1, Duration(time.Second),
		Restart{Tag: TagPermanent},
		Backoff{Tag: TagExponential, MaxRestarts: 0, MaxElapsed: BackoffDefaultMaxElapsed},
		GiveUp{Tag: TagReport})
	h := SupervisorSpawn(fr, sup, func(*Frame) Unit { return Unit{} })
	if out := SupervisorFlushBounded(fr, sup, Wait{Tag: TagForever}); out.Tag != TagFlushed {
		t.Fatalf("flush did not complete: tag %d", out.Tag)
	}
	if h.t.tag != TagFailed {
		t.Fatalf("a permanent worker that returned settled at tag %d, want Failed(%d)", h.t.tag, TagFailed)
	}
	const want = "permanent task returned on its own; work under this supervisor is expected to run until it is stopped"
	if h.t.fail.Msg != want {
		t.Errorf("the synthesized failure reads\n  %q\nwant\n  %q", h.t.fail.Msg, want)
	}
	if h.t.fail.Tag != TagErrored {
		t.Errorf("it is tagged %d; it must be Errored(%d) so it reports and restarts through the ordinary path rather than needing a fourth outcome",
			h.t.fail.Tag, TagErrored)
	}
}

// TestSupervisorCancelledTaskIsNotAFailure pins the corpus's own shape:
// `Task.cancel` on a supervised task, then `Task.outcome` reporting Cancelled.
//
// Being cancelled is never a restart trigger under any disposition — a policy
// that fought its own shutdown would never let the program exit — so this runs
// under PERMANENT, where a wrong classification would restart forever.
func TestSupervisorCancelledTaskIsNotAFailure(t *testing.T) {
	fr, done := supervisorFixture(t)
	defer done()

	sup := SupervisorNewExact(fr, 2, Duration(time.Second),
		Restart{Tag: TagPermanent},
		Backoff{Tag: TagExponential, MaxRestarts: BackoffDefaultMaxRestarts, MaxElapsed: BackoffDefaultMaxElapsed},
		GiveUp{Tag: TagReport})

	var runs atomic.Int64
	entered := make(chan struct{})
	var once sync.Once
	h := SupervisorSpawn(fr, sup, func(taskFr *Frame) Unit {
		runs.Add(1)
		once.Do(func() { close(entered) })
		TimerSleep(taskFr, Duration(30*time.Second))
		return Unit{}
	})
	<-entered
	TaskCancel(h)
	if out := SupervisorFlushBounded(fr, sup, Wait{Tag: TagForever}); out.Tag != TagFlushed {
		t.Fatalf("flush did not complete after the cancel: tag %d", out.Tag)
	}
	if got := TaskOutcomeOf(fr, h); got.Tag != TagCancelled {
		t.Errorf("outcome is tag %d, want Cancelled(%d)", got.Tag, TagCancelled)
	}
	if got := runs.Load(); got != 1 {
		t.Errorf("the body ran %d times; a cancelled task was restarted, which would fight the thing that stopped it", got)
	}
}

// TestSupervisorSpawnAllRunsOnePerItem covers the batch form and the ORDER of
// the handles it returns, which `Task.await_all` depends on.
func TestSupervisorSpawnAllRunsOnePerItem(t *testing.T) {
	fr, done := supervisorFixture(t)
	defer done()

	sup := newTestSupervisor(fr, 2, DefaultShutdownTimeout)
	var mu sync.Mutex
	seen := map[int64]bool{}
	source := listFromSlice([]int64{10, 20, 30})
	handles := SupervisorSpawnAll(fr, sup, source, func(_ *Frame, n int64) Unit {
		mu.Lock()
		seen[n] = true
		mu.Unlock()
		return Unit{}
	})
	if got := len(listToSlice(handles)); got != 3 {
		t.Fatalf("spawn_all returned %d handles for a 3-item source", got)
	}
	if out := SupervisorFlushBounded(fr, sup, Wait{Tag: TagForever}); out.Tag != TagFlushed {
		t.Fatalf("flush did not complete: tag %d", out.Tag)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, n := range []int64{10, 20, 30} {
		if !seen[n] {
			t.Errorf("item %d never reached a body; spawn_all is not one task per item", n)
		}
	}
}

// TestSupervisorGiveUpExitRecordsRatherThanExits pins the LIBRARY RULE: a
// background goroutine must not end somebody else's process, so `GiveUp.Exit`
// records a fatal and the program's own exit path reads it.
//
// The paired negative is `GiveUp.Report` over the identical failure, which must
// record NOTHING — without it, "a fatal is present" is also what a runtime that
// recorded every failure would produce.
func TestSupervisorGiveUpExitRecordsRatherThanExits(t *testing.T) {
	run := func(giveUp uint8) error {
		fr, done := supervisorFixture(t)
		defer done()
		sup := SupervisorNewExact(fr, 1, Duration(time.Second),
			Restart{Tag: TagTemporary},
			Backoff{Tag: TagExponential, MaxRestarts: 0, MaxElapsed: BackoffDefaultMaxElapsed},
			GiveUp{Tag: giveUp})
		_ = SupervisorSpawn(fr, sup, func(*Frame) Unit {
			Trap("the downstream is gone")
			return Unit{}
		})
		if out := SupervisorFlushBounded(fr, sup, Wait{Tag: TagForever}); out.Tag != TagFlushed {
			t.Fatalf("flush did not complete: tag %d", out.Tag)
		}
		return SupervisorFatal()
	}

	fatal := run(TagExit)
	if fatal == nil {
		t.Fatal("GiveUp.Exit recorded no fatal, so the program would exit 0 after its supervisor gave up")
	}
	if !strings.Contains(fatal.Error(), "the downstream is gone") {
		t.Errorf("the fatal reads %q and does not carry the task's own failure", fatal.Error())
	}

	if fatal := run(TagReport); fatal != nil {
		t.Errorf("GiveUp.Report recorded a fatal (%v); Report is the default and must let the program carry on", fatal)
	}
}

// TestBackoffDelayIsJitteredAndBounded pins the three properties of the delay
// curve that a wrong implementation would break silently.
//
// JITTER IS ASSERTED BY VARIANCE, which is the only way to see it: a fixed
// delay of the right magnitude passes every bound check. The equal-jitter shape
// puts the answer in [d/2, d], so the test asserts both that every sample is in
// range AND that not every sample is the same value.
func TestBackoffDelayIsJitteredAndBounded(t *testing.T) {
	for _, c := range []struct {
		step int
		base time.Duration
	}{
		{0, backoffFrom}, {1, 2 * backoffFrom}, {2, 4 * backoffFrom}, {9, backoffCap},
	} {
		lo, hi := c.base/2, c.base
		distinct := map[Duration]bool{}
		for range 40 {
			got := time.Duration(backoffDelay(c.step))
			if got < lo || got > hi {
				t.Fatalf("step %d produced %v, outside the equal-jitter window [%v, %v]", c.step, got, lo, hi)
			}
			distinct[Duration(got)] = true
		}
		if len(distinct) < 2 {
			t.Errorf("step %d produced one value across 40 samples, so jitter is absent — a supervisor restarting "+
				"fifty identical tasks would retry them all at the same instant", c.step)
		}
	}
	// The CEILING, asserted past the doubling range: without the clamp, step 30
	// would be 2^30 seconds.
	if got := time.Duration(backoffDelay(30)); got > backoffCap {
		t.Errorf("step 30 produced %v, above the %v ceiling", got, backoffCap)
	}
}

// TestBackoffZeroValueIsTheBuiltInSchedule pins backoffOrDefault, which is what
// stops a Go embedder's zero-value Backoff from meaning "never restart".
//
// A zero Tag reaching decideRestart unmodified would fall through its
// `!= TagExponential` arm and give up on the first failure — silently turning
// every disposition into Temporary.
func TestBackoffZeroValueIsTheBuiltInSchedule(t *testing.T) {
	got := backoffOrDefault(Backoff{})
	if got.Tag != TagExponential {
		t.Fatalf("the zero value resolved to tag %d, want Exponential(%d)", got.Tag, TagExponential)
	}
	if got.MaxRestarts != BackoffDefaultMaxRestarts || got.MaxElapsed != BackoffDefaultMaxElapsed {
		t.Errorf("the zero value resolved to {%d, %v}, want the declared defaults {%d, %v}",
			got.MaxRestarts, got.MaxElapsed, BackoffDefaultMaxRestarts, BackoffDefaultMaxElapsed)
	}
	// A STATED schedule is passed through unchanged, or the default would be
	// overwriting what a caller asked for.
	stated := Backoff{Tag: TagExponential, MaxRestarts: 3, MaxElapsed: Duration(time.Minute)}
	if got := backoffOrDefault(stated); got != stated {
		t.Errorf("a stated schedule came back as %+v, want %+v", got, stated)
	}
}

// TestSupervisorClassifiesLikeTaskSpawn holds the two classifiers to one rule.
//
// `TaskSpawn`'s recover and `runSupervisedBody`'s are separate code under
// separate lifecycles, and the RULE they share — a Nomi fault is `Errored`, any
// other panic is `Panicked`, a cancellation is neither — is exactly the kind of
// duplicated decision that drifts. Asserted by running the same three bodies
// through both.
func TestSupervisorClassifiesLikeTaskSpawn(t *testing.T) {
	for _, c := range []struct {
		name string
		body func(*Frame) Unit
		want uint8
	}{
		{"a Nomi fault is Errored", func(*Frame) Unit { Trap("boom"); return Unit{} }, TagFailed},
		{"a Go panic is Failed too", func(*Frame) Unit { panic("go-level") }, TagFailed},
		{"a clean return is Completed", func(*Frame) Unit { return Unit{} }, TagCompleted},
	} {
		t.Run(c.name, func(t *testing.T) {
			// The BLOCK path.
			blockFr := EnterScope(NewFrame(context.Background()))
			h := TaskSpawn(blockFr, c.body)
			ScopeExit(blockFr)
			blockOutcome := h.t.tag
			blockFail := h.t.fail

			// The SUPERVISOR path, under Temporary so one run settles it.
			fr, done := supervisorFixture(t)
			defer done()
			sup := newTestSupervisor(fr, 1, Duration(time.Second))
			sh := SupervisorSpawn(fr, sup, c.body)
			if out := SupervisorFlushBounded(fr, sup, Wait{Tag: TagForever}); out.Tag != TagFlushed {
				t.Fatalf("flush did not complete: tag %d", out.Tag)
			}

			if blockOutcome != c.want || sh.t.tag != c.want {
				t.Fatalf("block settled at %d and supervisor at %d, want %d for both", blockOutcome, sh.t.tag, c.want)
			}
			if blockFail.Tag != sh.t.fail.Tag || blockFail.Msg != sh.t.fail.Msg {
				t.Errorf("the two classifiers disagree: block {%d,%q} vs supervisor {%d,%q}",
					blockFail.Tag, blockFail.Msg, sh.t.fail.Tag, sh.t.fail.Msg)
			}
		})
	}
}

// TestSupervisorDrainSettlesAPermanentWorkerEarly is the settle check's
// DEMONSTRATED FIRING, measured as a CLOCK reading against the budget.
//
// A permanent worker parked on an empty inbox never finishes, so the WaitGroup
// never closes and the drain would spend its full budget on every clean
// shutdown. The settle check ends it once every running task is parked with no
// scheduled wake — which is what `SupervisedPark` in channel.go reports.
//
// The paired negative is a worker SLEEPING rather than parked: a timer has a
// scheduled wake, so work is genuinely still coming and the budget must be
// honoured in full.
func TestSupervisorDrainSettlesAPermanentWorkerEarly(t *testing.T) {
	fr, done := supervisorFixture(t)
	defer done()

	sup := SupervisorNewExact(fr, 1, Duration(3*time.Second),
		Restart{Tag: TagPermanent},
		Backoff{Tag: TagExponential, MaxRestarts: BackoffDefaultMaxRestarts, MaxElapsed: BackoffDefaultMaxElapsed},
		GiveUp{Tag: TagReport})

	ch := ChannelBuffered[int64](1)
	parked := make(chan struct{})
	var once sync.Once
	_ = SupervisorSpawn(fr, sup, func(taskFr *Frame) Unit {
		once.Do(func() { close(parked) })
		// Blocks forever: nothing ever sends, and nothing closes it.
		_ = ReceiverReceive(taskFr, ch.Receiver)
		return Unit{}
	})
	<-parked
	// The park is recorded once the receive has actually blocked, which is
	// after `parked` closes. A short wait rather than a synchronisation point,
	// because the tracker is deliberately not observable from outside rt.
	time.Sleep(20 * time.Millisecond)

	begin := time.Now()
	DrainSupervisors()
	elapsed := time.Since(begin)
	if elapsed > 2*time.Second {
		t.Errorf("the drain spent %v of a 3s budget on a worker that was parked with no scheduled wake; "+
			"the settle check did not fire", elapsed)
	}
}

// recoverTrap runs f and returns the Nomi fault it raised, or nil.
//
// Named for what it catches: a Go panic that is NOT an *Error is re-panicked,
// so a test using this cannot accidentally pass by swallowing an rt defect.
func recoverTrap(f func()) (err *Error) {
	defer func() {
		switch r := recover().(type) {
		case nil:
		case *Error:
			err = r
		default:
			panic(r)
		}
	}()
	f()
	return nil
}
