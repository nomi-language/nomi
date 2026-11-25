// Package vclock runs a lowered Nomi test case under a virtual clock.
//
// `testing/synctest` gives a bubble its own fake clock that advances the moment
// every goroutine in it is durably blocked. For Nomi that turns every
// timing-sensitive test — `timer.sleep`, `Context.with_timeout`, channel
// timeouts — from something that costs real seconds and goes flaky on a loaded
// machine into something instant and deterministic. A genuine deadlock is
// *detected* rather than silently hanging.
//
// # Why this is its own package and not part of rt
//
// It imports `testing` and `testing/synctest`. `nomi/rt` is linked by
// everything that runs a Nomi program, including a hello world, and
// rt/imports_test.go states the standard rt holds itself to: a second
// dependency is "a decision, not a bump", because everything linking rt
// carries it. The same
// argument applies to a heavy standard-library import — `testing` pulls `flag`,
// `regexp` and the profiling packages, and a package's `init` runs whenever the
// package is in the import graph, so the linker's function-level dead-code
// elimination does not get it back.
//
// So this is a sub-package of the same module — no go.mod change, no new
// require — and rt itself does not import it. Its importer is the VM's test
// runner (internal/vm/testrun.go), which runs a case under
// `clock Clock.Virtual` through RunCase.
//
// # Why this needs a fabricated *testing.T
//
// The only bubble entry point Go exposes is
// `synctest.Test(t *testing.T, f func(*testing.T))`. There is no
// `synctest.Run(func())`. A properly constructed `*testing.T` comes only from
// the testing framework's own machinery: `testing.MainStart` takes an
// unexported `testDeps`, and `testing.RunTests` never iterates because
// `cpuList` is populated only by `M.Run`. `testing.Main` works but calls
// `os.Exit` and prints Go-formatted output, which would mean handing a test
// run's reporting and exit handling to the testing package.
//
// So the bubble is opened with a zero-value `testing.T`, under a discipline
// that keeps every failure mode returnable. Each of the three layers closes one
// failure mode:
//
//  1. **Never call a method on the T.** A zero-value T has nil internals;
//     `t.Fatalf` calls `runtime.Goexit`, which leaves `synctest.Test` waiting
//     for a root goroutine that will never return — a permanent hang. rt's
//     reporter reports through return values, so it never needs one.
//  2. **Recover inside the bubble.** An escaping panic reaches synctest's own
//     handler, which touches the nil internals and turns a recoverable panic
//     into a nil-dereference that kills the process. Recovered in the body, it
//     is just a failed test.
//  3. **Recover outside the bubble.** synctest reports deadlock and
//     leftover-goroutine conditions by panicking. Those are results, not
//     crashes.
//
// # A case ends when its bubble does
//
// RunCase returns only once synctest.Test has: every goroutine the case
// started has ended, or waits on the bubble's own channels and timers and
// can never run again. Cases share the process's state, rt's supervisor
// registry and signal watches among it, so a case whose bubble still ran
// beside the next one could close a channel of one bubble from the other,
// which Go ends the process for. Real time does not end a case here: a
// case may compute for long in real time while its virtual clock stands
// still, as a loop over many short sleeps does. `nomi test`'s limit on a
// case's real time runs around the bubble (internal/vm's caselimit.go) and
// stops the case's work, which ends the bubble.
//
// One wait the stop cannot reach: a wait outside the program, a socket or a
// process. Nothing outside a bubble may touch its channels, and nothing in
// it can run while its clock stands still, so such a bubble cannot end. Its
// caller, rt.RunBubbled, runs RunCase on a goroutine of its own and gives up
// on a bubble that has not ended shortly after the case's time ran out: the
// case fails as waiting outside the program, RunCase's goroutine stays where
// it is, and no case starts in the process after it, since a bubble beside
// it could close one of its channels. `nomi test` reports the cases it did
// not run and exits.
//
// # What a bubble changes
//
// It is opt-in — a `group` says `clock Clock.Virtual`, and its tests
// run in a bubble — rather than the default, because a bubble genuinely
// changes semantics:
//
//   - Anything blocking on the outside world — a socket, a process, any
//     host function that waits on it — is not "durably blocked", so the clock
//     does not advance while it waits, and a case that waits on it until
//     its time runs out ends the run (above).
//   - Every goroutine must finish before the test does. Background work a test
//     starts and abandons is an error rather than a leak.
//   - `Instant.now` inside a bubble reads the fake clock, which starts at
//     midnight UTC 2000-01-01.
package vclock

import (
	"fmt"
	"testing"
	"testing/synctest"
)

// RunCase runs one lowered test case under a virtual clock and returns its
// result as an ordinary error, whatever happened inside.
//
// See the package header for why each layer of recovery is here. `body` is the
// test case; `cleanup` runs inside the bubble after it, so any drain budget is
// spent in virtual time and any background work the case started is finished
// before the bubble closes. rt.RunBubbled passes the end of the supervisors;
// a nil `cleanup` is accepted.
//
// The signature is `func(body func() error, cleanup func()) error` because that
// is what `rt.Test.Bubble` is declared as, and rt cannot import this package —
// the dependency runs the other way, so the seam is a function value rather
// than a call.
func RunCase(body func() error, cleanup func()) (result error) {
	// Layer 3: synctest reports deadlock and leftover goroutines by
	// panicking. Both are legitimate test outcomes.
	defer func() {
		if r := recover(); r != nil {
			result = fmt.Errorf("virtual-time bubble: %v", r)
		}
	}()

	synctest.Test(&testing.T{}, func(*testing.T) {
		// Layer 2: keep a panicking test body away from synctest's own
		// handler, which cannot cope with a zero-value T.
		func() {
			defer func() {
				if r := recover(); r != nil {
					result = fmt.Errorf("test panicked: %v", r)
				}
			}()
			result = body()
		}()
		if cleanup != nil {
			cleanup()
		}
	})
	return result
}
