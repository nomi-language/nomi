// Package vclock runs a lowered Nomi test case under a virtual clock.
//
// `testing/synctest` gives a bubble its own fake clock that advances the moment
// every goroutine in it is durably blocked. For Nomi that turns every
// timing-sensitive test — `timer.sleep`, `Context.with_timeout`, channel
// timeouts — from something that costs real seconds and goes flaky on a loaded
// machine into something instant and deterministic. A genuine deadlock is
// *detected* rather than silently hanging.
//
// # WHY THIS IS ITS OWN PACKAGE AND NOT PART OF rt
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
// require — and `internal/irbuild` emits an import of it ONLY for a program that
// declares `clock Clock.Virtual`. A program with no virtual-clock group links
// nothing from here. That is the same conditional shape `needsCompilerModule`
// already uses for the compiler module.
//
// # WHY THIS NEEDS A FABRICATED *testing.T
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
// that keeps every failure mode returnable. Each of the four layers was
// verified against its alternative:
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
//  4. **Watchdog the whole thing.** Insurance against a future Go release
//     calling a T method internally on a path we do not exercise. A bubbled
//     test finishes in milliseconds no matter how much virtual time it spends,
//     so a multi-second real-time ceiling cannot produce a false positive.
//
// # WHAT A BUBBLE CHANGES
//
// It is opt-in — a `tests` group says `clock Clock.Virtual`, and its tests
// run in a bubble — rather than the default, because a bubble genuinely
// changes semantics:
//
//   - Anything blocking on the outside world — reading stdin, a real network
//     call through FFI — is not "durably blocked", so the clock never advances
//     and the bubble reports a deadlock.
//   - Every goroutine must finish before the test does. Background work a test
//     starts and abandons is an error rather than a leak.
//   - `Instant.now` inside a bubble reads the fake clock, which starts at
//     midnight UTC 2000-01-01.
package vclock

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

// Watchdog bounds the real time one bubbled test case may take. Virtual time is
// free, so even a case that sleeps for simulated hours returns in
// milliseconds; this only fires if the bubble itself wedges.
//
// Exported so a test can read the ceiling it is asserting against rather than
// hard-coding a second copy of the number.
const Watchdog = 30 * time.Second

// RunCase runs one lowered test case under a virtual clock and returns its
// result as an ordinary error, whatever happened inside.
//
// See the package header for why each layer of recovery is here. `body` is the
// test case; `cleanup` runs inside the bubble after it, so any drain budget is
// spent in virtual time and any background work the case started is finished
// before the bubble closes. A nil `cleanup` is accepted: rt has no supervisor
// registry to drain today, and a caller that has nothing to do should not have
// to synthesise an empty closure.
//
// The signature is `func(body func() error, cleanup func()) error` because that
// is what `rt.Test.Bubble` is declared as, and rt cannot import this package —
// the dependency runs the other way, so the seam is a function value rather
// than a call.
func RunCase(body func() error, cleanup func()) error {
	done := make(chan error, 1)

	go func() {
		var result error

		// Layer 3: synctest reports deadlock and leftover goroutines by
		// panicking. Both are legitimate test outcomes.
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("virtual-time bubble: %v", r)
				return
			}
			done <- result
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
	}()

	// Layer 4.
	select {
	case err := <-done:
		return err
	case <-time.After(Watchdog):
		return fmt.Errorf(
			"virtual-time bubble did not finish within %v of real time — "+
				"a bubbled test should complete in milliseconds however much "+
				"virtual time it spends, so this is the bubble itself wedging "+
				"rather than a slow test", Watchdog)
	}
}
