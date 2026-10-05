package rt

import (
	"sync"
	"sync/atomic"
)

// A Nomi `once` binding, as Go.
//
// # The three guarantees, and why none of them is Go's package-level `var`
//
// Spec §2 makes `once` lazy, cached, and exactly-once. A Go package-level
// `var x = rhs()` is eager and ordered by the initialization graph, so it
// breaks the first two outright: `once a = b + 1` above `once b = 5` is legal
// Nomi and definition order is irrelevant, while Go would either reorder it or
// (through a function call it cannot see into) run it at a moment the program
// never asked for. An eager RHS also runs its side effects in a program that
// never reads the binding, which is observable.
//
// `sync.Once` is the right shape for exactly-once and caching, and it is
// deliberately not what this uses: `sync.Once.Do` re-entered on the same
// goroutine deadlocks. A cyclic `once` is a diagnosable Nomi error, and
// hanging is the one answer worse than a wrong one — a test run would hang
// with it. So the cell keeps sync.Once's mutex-plus-atomic shape and adds the
// cycle test in front of the lock.
//
// # Caching is on the cell, never written back to the binding's storage
//
// Writing the resolved value back into a shared map would race concurrent
// reads from sibling `spawn` task goroutines and produce a Go `fatal error:
// concurrent map read and map write`. `done` is the atomic publication flag
// whose acquire load pairs with the release store below, so the fast path
// reads `v` with no lock and no possibility of tearing.
//
// # Cycle detection is per-lineage, not per-cell, and that is load-bearing
//
// A flag on the cell would make two goroutines forcing the same once look like
// a cycle: the losing goroutine would report a spurious cyclic-once and
// corrupt the total. What identifies a cycle is that this chain of activations
// is already inside this cell's RHS, so the state belongs to the activation chain: a
// cons-list on *Frame, extended for the RHS and propagated caller-to-callee by
// the frame pointer every call already threads. That is what
// catches a cycle through an ordinary function call, which is the case the
// mechanism exists for — a direct `once a = a` is the easy half.
//
// A static acyclicity proof was considered and rejected: across an indirect
// call it is necessarily conservative, so it would refuse `once a = f()` where
// `f` simply never reaches `a` — idiomatic, safe code — while still missing a
// real cycle through a function value or a spawned task. Rejecting the valid
// and missing the invalid is strictly worse than observing the cycle happen.
//
// # Cost
//
// One atomic load per access after the first force, which laziness plus
// caching requires regardless. The cons push, the lineage walk and the child
// frame are on the first force only — once per cell per program run — so they
// are not a per-access cost.

// onceID is a cell's identity. It is the address of the header embedded in the
// cell that is compared, never the name: two files may each declare a private
// `once config`, and a name-keyed lineage would report the second as a cycle
// through the first. Same discipline as TypeID, and for the same reason — see
// dispatch.go. The struct is non-empty on purpose: Go may give two variables
// of a zero-sized type one address, which would merge two identities.
type onceID struct {
	name string
}

// forcing is one entry in a lineage's stack of `once` cells whose RHS is being
// evaluated. Immutable once built — each force conses a new node onto the
// caller's list — so the head pointer is safe to share across the
// caller-to-callee propagation the frame pointer performs, with no copying and
// no locking.
type forcing struct {
	cell *onceID
	next *forcing
}

// forcingOnce returns a child frame whose lineage records cell as being
// forced. Everything the RHS calls receives this frame, so a cycle through any
// depth of ordinary calls is visible at the re-entry.
//
// A new Frame here is the frame design's own rule: one is allocated only where
// the scope genuinely changes, and forcing a `once` is named as one of the
// four such places. It costs one allocation per cell per run.
//
// The RHS runs under the forcer's cancellation and deadline, and not under its
// scoped fields. `ctx` is the forcer's, so a `Task.cancel` or a deadline stops a
// slow RHS at its next blocking operation; `inTask` and `underDeadline` travel
// with it so that stop unwinds the forcer the way it would unwind any other
// work (a task settles Cancelled, the main line reports its deadline), rather
// than landing on raiseCanceled's producer-bug arm, which a frame built
// without them would reach. `park` travels for the same reason: a supervised task
// blocked inside an RHS is still working. The unwind leaves the cell unforced,
// exactly as a faulting RHS does, so the next access runs the RHS again.
//
// The forcer's `scopedFields` and `scopedContext` deliberately do not travel:
// a cached value must not depend on which caller's `with` overrides were in
// force when it happened to be forced first. The RHS reads what boot
// published instead (spec §27): the module-level app state, which holds
// boot's result and none of the rebinds. A force before boot
// has published sees no fields, and a read traps as any pre-boot read does.
func (fr *Frame) forcingOnce(cell *onceID) *Frame {
	child := &Frame{ctx: fr.ctx, forcing: &forcing{cell: cell, next: fr.forcing},
		booted: fr.booted, inTask: fr.inTask, underDeadline: fr.underDeadline, park: fr.park}
	if fr.booted != nil {
		child.scopedFields, child.scopedContext = fr.booted.fields, fr.booted.context
	}
	return child
}

// CyclicOnceText is the one spelling of the cyclic-`once` diagnostic.
func CyclicOnceText(name string) string {
	return "cyclic 'once' binding: '" + name + "' depends on itself"
}

// OnceCell is one `once` binding's storage. Generated code declares one
// package-level cell per binding and reads it only through Get.
type OnceCell[T any] struct {
	id   onceID
	mu   sync.Mutex
	done atomic.Bool
	v    T
}

// NewOnceCell builds an unforced cell. name is the Nomi binding's own name and
// is used only in the cyclic diagnostic.
func NewOnceCell[T any](name string) *OnceCell[T] {
	return &OnceCell[T]{id: onceID{name: name}}
}

// Get returns the binding's value, forcing rhs on the first access.
//
// Split so the resolved path is a load and a return with nothing else in it —
// that is every access after the first, and it is what Go's inliner can see
// through.
func (c *OnceCell[T]) Get(fr *Frame, rhs func(fr *Frame) T) T {
	if c.done.Load() {
		return c.v
	}
	return c.force(fr, rhs)
}

func (c *OnceCell[T]) force(fr *Frame, rhs func(fr *Frame) T) T {
	// Ahead of the lock, so a re-entrant force reports the cycle instead of
	// deadlocking on the mutex the outer force is holding.
	for n := fr.forcing; n != nil; n = n.next {
		if n.cell == &c.id {
			Trap(CyclicOnceText(c.id.name))
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done.Load() {
		// Another goroutine resolved it while this one waited.
		return c.v
	}
	// Held across the RHS so concurrent forcers wait and observe the one
	// cached value: spec §2's "side effects happen exactly once".
	v := rhs(fr.forcingOnce(&c.id))
	c.v = v
	c.done.Store(true)
	return v
}
