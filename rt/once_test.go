package rt

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestOnceCellForcesExactlyOnceUnderConcurrency is the guarantee a fixture
// cannot reach: a single-goroutine program cannot distinguish "forced once"
// from "forced once per goroutine, and they happened to agree".
//
// It also covers cycle state kept on the cell rather than on the lineage: then
// the goroutines that lose the race observe "already forcing" and report a
// spurious cyclic-once. Here every one of them must get the value.
func TestOnceCellForcesExactlyOnceUnderConcurrency(t *testing.T) {
	var forced atomic.Int64
	cell := NewOnceCell[int64]("counter")
	rhs := func(fr *Frame) int64 {
		forced.Add(1)
		return 42
	}

	const goroutines = 64
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(goroutines)
	got := make([]int64, goroutines)
	for i := range goroutines {
		go func() {
			defer done.Done()
			start.Wait()
			got[i] = cell.Get(NewFrame(context.Background()), rhs)
		}()
	}
	start.Done()
	done.Wait()

	if n := forced.Load(); n != 1 {
		t.Fatalf("the RHS ran %d times; `once` forces exactly once", n)
	}
	for i, v := range got {
		if v != 42 {
			t.Fatalf("goroutine %d saw %d, not the one cached value", i, v)
		}
	}
}

// TestOnceCellIsLazy pins that constructing a cell runs nothing. Trivial to
// state and the reason a `once` cannot be a Go package-level `var`.
func TestOnceCellIsLazy(t *testing.T) {
	ran := false
	cell := NewOnceCell[int64]("lazy")
	if ran {
		t.Fatal("constructing the cell ran something")
	}
	v := cell.Get(NewFrame(context.Background()), func(fr *Frame) int64 {
		ran = true
		return 7
	})
	if !ran || v != 7 {
		t.Fatalf("first Get must force: ran=%v v=%d", ran, v)
	}
}

// TestOnceCellIdentityIsTheAddressNotTheName is the anti-collision guard.
//
// Two files may each declare a private `once config`, and a lineage keyed on
// the name would report the second force as a cycle through the first — a
// wrong answer that only appears in a program with two same-named bindings.
// Identity-by-name bugs have exactly that shape. Both halves are
// asserted: same name is not a cycle, and the real re-entry is.
func TestOnceCellIdentityIsTheAddressNotTheName(t *testing.T) {
	outer := NewOnceCell[int64]("config")
	inner := NewOnceCell[int64]("config")

	v := outer.Get(NewFrame(context.Background()), func(fr *Frame) int64 {
		// Forcing a different cell of the same name from inside the first
		// one's RHS. Legal, and the frame handed down carries the outer cell.
		return inner.Get(fr, func(fr *Frame) int64 { return 5 }) + 1
	})
	if v != 6 {
		t.Fatalf("two same-named cells collided: got %d, want 6", v)
	}
}

// TestOnceCellCycleTrapsRatherThanDeadlocking is the property that rules
// sync.Once out. A re-entrant force on one goroutine must report, with
// CyclicOnceText's text.
func TestOnceCellCycleTrapsRatherThanDeadlocking(t *testing.T) {
	var cell *OnceCell[int64]
	cell = NewOnceCell[int64]("seed")
	rhs := func(fr *Frame) int64 {
		// The re-entry, reached through the frame the force installed —
		// which is what a Nomi function call in the RHS would do.
		return cell.Get(fr, func(fr *Frame) int64 { return 0 })
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a re-entrant force must trap, not return")
		}
		err, ok := r.(*Error)
		if !ok {
			t.Fatalf("panicked with %T, want *rt.Error", r)
		}
		if err.Msg != "cyclic 'once' binding: 'seed' depends on itself" {
			t.Fatalf("diagnostic text is %q", err.Msg)
		}
	}()
	cell.Get(NewFrame(context.Background()), rhs)
}

// TestOnceCellCycleIsPerLineage asserts the negative the cycle test needs to
// be worth having: a frame that is not inside a cell's RHS may force it, even
// while another lineage is inside a different one.
func TestOnceCellCycleIsPerLineage(t *testing.T) {
	a := NewOnceCell[int64]("a")
	b := NewOnceCell[int64]("b")
	root := NewFrame(context.Background())

	v := a.Get(root, func(fr *Frame) int64 {
		// `fr` is inside a's RHS; forcing b from it is an ordinary
		// dependency, not a cycle.
		return b.Get(fr, func(fr *Frame) int64 { return 10 }) * 2
	})
	if v != 20 {
		t.Fatalf("a dependency between two onces must not read as a cycle: %d", v)
	}
	// And the root frame is untouched: forcing is not global state.
	if root.forcing != nil {
		t.Fatal("forcing a once mutated the caller's frame")
	}
}

// TestCyclicOnceTextIsTheOneSpelling guards the shared string against a
// paraphrase landing on one path only.
func TestCyclicOnceTextIsTheOneSpelling(t *testing.T) {
	got := CyclicOnceText("route_table")
	if !strings.Contains(got, "cyclic 'once' binding") || !strings.Contains(got, "'route_table'") {
		t.Fatalf("diagnostic reads %q", got)
	}
}

// A `once` RHS runs under the forcer's cancellation and deadline. A task
// cancelled while it forces a cell settles Cancelled, not Failed with
// `cancellation reached a frame with no task to unwind`, which is what an RHS
// frame built without `inTask` would report. The cell stays unforced, as it
// does after a faulting RHS, so the next access runs the RHS again and caches
// its value.
func TestOnceCellACancelledForceUnwindsTheTaskAndLeavesTheCellUnforced(t *testing.T) {
	var runs atomic.Int64
	started := make(chan struct{}, 2)
	cell := NewOnceCell[int64]("slow")
	blocking := func(fr *Frame) int64 {
		runs.Add(1)
		started <- struct{}{}
		<-fr.Context().Done()
		CancelIfDone(fr)
		return 1
	}

	block := EnterScope(NewFrame(context.Background()))
	var got Outcome[int64]
	func() {
		defer ScopeExit(block)
		h := TaskSpawn[int64](block, func(tf *Frame) int64 { return cell.Get(tf, blocking) })
		<-started
		TaskCancel(h)
		got = TaskOutcomeOf(block, h)
	}()
	if got.Tag != TagCancelled {
		t.Fatalf("a task cancelled mid-force settled %v (%q), want Cancelled", got.Tag, got.Failed.Msg)
	}

	v := cell.Get(NewFrame(context.Background()), func(fr *Frame) int64 {
		runs.Add(1)
		return 7
	})
	if v != 7 || runs.Load() != 2 {
		t.Fatalf("after a cancelled force the next access returned %d with %d RHS runs; want 7 from a second run", v, runs.Load())
	}
}

// The main-line half: a force under a deadline that runs out reports the
// deadline, as the same blocking operation outside a `once` does.
func TestOnceCellAForceUnderAnExpiredDeadlineReportsTheDeadline(t *testing.T) {
	spent := ContextWithDeadline(ContextRoot(), Instant(time.Now().Add(-time.Hour).UnixNano()))
	fr, release := EnterDeadline(NewFrame(context.Background()), spent)
	defer release()

	cell := NewOnceCell[int64]("slow")
	err := recoverFault(func() {
		cell.Get(fr, func(rf *Frame) int64 {
			CancelIfDone(rf)
			return 1
		})
	})
	if err == nil || !strings.Contains(err.Msg, "deadline exceeded") {
		t.Fatalf("a force under a spent deadline reported %v, want the deadline", err)
	}
}

// A `once` RHS reads what boot published, never the forcer's rebinds: a cached
// value must not depend on who forced it first, so the RHS is evaluated
// against the module-level app state. A forcer that dropped scoped fields
// would make `once seen = App.label` trap "application field label is
// unavailable". Each forcer here has rebound both
// `App.label` and the context; the scope, the block task and the supervised task
// are the hand-built frames that must carry the publication across.
func TestOnceCellRHSReadsBootsPublicationNotTheForcersRebinds(t *testing.T) {
	root := NewFrame(context.Background())
	bootContext := ContextWithDeadline(ContextRoot(), Instant(time.Now().Add(time.Hour).UnixNano()))
	PublishScoped(root, map[string]any{"label": "booted"})
	InstallContext(root, bootContext)
	defer RunBootCleanup(root)

	rebound := EnterContext(EnterScopedField(root, "label", "rebound"),
		ContextWithDeadline(ContextRoot(), Instant(time.Now().Add(2*time.Hour).UnixNano())))
	read := func(fr *Frame) string {
		cell := NewOnceCell[string]("seen")
		return cell.Get(fr, func(rf *Frame) string {
			label := ScopedField[string](rf, "label")
			if ActiveContext(rf).node != bootContext.node {
				return "the forcer's context"
			}
			return label
		})
	}
	if got := ScopedField[string](rebound, "label"); got != "rebound" {
		t.Fatalf("the forcer itself read %q, want its own rebind", got)
	}
	if got := read(rebound); got != "booted" {
		t.Errorf("forced on the main line: %q, want booted", got)
	}

	block := EnterScope(rebound)
	var fromTask string
	func() {
		defer ScopeExit(block)
		if got := read(block); got != "booted" {
			t.Errorf("forced inside a concurrent block: %q, want booted", got)
		}
		fromTask = TaskAwait(block, TaskSpawn[string](block, read))
	}()
	if fromTask != "booted" {
		t.Errorf("forced from a block task: %q, want booted", fromTask)
	}

	ResetSupervisors()
	defer ResetSupervisors()
	sup := newTestSupervisor(EnterBoot(rebound), 1, Duration(time.Second))
	supervised := make(chan string, 1)
	_ = SupervisorSpawn(rebound, sup, func(tf *Frame) Unit {
		supervised <- read(tf)
		return Unit{}
	})
	select {
	case got := <-supervised:
		if got != "booted" {
			t.Errorf("forced from a supervised task: %q, want booted", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the supervised task never answered; did its force trap?")
	}

	// Before boot publishes, the RHS sees no fields, like any pre-boot read.
	if err := recoverFault(func() { read(NewFrame(context.Background())) }); err == nil ||
		!strings.Contains(err.Msg, "application field label is unavailable") {
		t.Errorf("a force before boot published reported %v, want the unavailable-field trap", err)
	}
}
