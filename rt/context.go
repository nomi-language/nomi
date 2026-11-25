package rt

import (
	"fmt"
	"math"
	"time"
)

// `std/context.Context`: the per-life execution context, as a Go value.
//
// # Why this is not Go's `context.Context`
//
// The obvious implementation is to wrap `context.Context` and let
// `context.WithDeadline` keep the chain. It is not the one here, and the reason
// is not dependency weight — Go's `context` is standard library and the
// concurrency runtime already carries it.
//
// Nomi's Context is a deadline chain plus value bindings. The deadline
// operations a program can perform on it are "what is the earliest deadline
// along my parent chain" and "give me a child with one more". Go's Context adds
// cancellation with a goroutine-visible `Done()` channel, a `context.Canceled`
// error, and a `CancelFunc` the caller must invoke or leak a timer. None of
// that is observable from a Nomi program, and every one of those is a second
// state to keep right. So this is a hand-rolled chain that resolves the
// deadline by walking at read time.
//
// ## How a deadline reaches a blocking operation
//
// Every blocking operation in this package selects on the frame's Go context.
// `rt.EnterDeadline` (frame.go) derives that context once, at the one place the
// ambient deadline changes — the `with MyApp.context = next` rebind — onto the
// frame every later call carries. So the deadline-only model composes with
// cancellation instead of being replaced by it, and the nine `fr.ctx` readers
// need no per-operation change. Deriving inside `contextNode` would allocate a
// context and a timer per derivation, which frame.go's own
// no-allocation-per-call constraint forbids.
//
// Without that derivation a rebind would bound what a program could read and
// nothing it could wait on. The tour's concurrency chapter depends on it: a
// five-minute `timer.sleep` under a 50ms deadline must answer "timed out,
// falling back" in well under a second. `deadline_floor_test.nomi` covers the
// blocking half: it prints "entered" before blocking, so a pass means the wait
// really was cut short.
//
// ## Why this file is not a first-party adapter, unlike calendar/http/regex
//
// Two checkable facts decide it:
//
//  1. rt is a consumer of Context, not only its provider. `EnterDeadline` needs
//     `chainBound`, and rt imports no adapter. std/calendar's rt types have no rt-side reader, which is
//     why its implementation can live in an adapter.
//  2. The host boundary refuses Context as a value by decision, so an
//     embedder cannot smuggle a ctx through it. An adapter's mechanism is the
//     value crossing that boundary. A host function reads the frame's Go
//     context (Frame.Context), which EnterDeadline derived, instead.
//
// Nothing here wraps a Go library. It is int64 arithmetic and a cons list, the
// same shape `Duration` and `Instant` have in rt/opaque.go.
//
// # Values are keyed by type identity
//
// `Context.with_value<T>` and `Context.value<T>` are generic over a Nomi type
// passed as a runtime value; `Type<T>` has a representation
// (rt/typewitness.go).
//
// The key is a `*TypeID` address, not a name. `with_value` takes the key from
// the argument's type and `value` takes it from the witness; the VM interns one
// `*TypeID` per qualified type name (internal/vm/context.go), so both readers
// see one identity.
//
// A linear walk rather than a map per node: `with_value` returns a child, so
// each node holds exactly one binding and shadowing is "the first hit along
// the parent chain wins". A map would be an allocation and a hash per
// derivation to serve one entry.
//
// One node type for both a deadline link and a value link, and it is what keeps
// `ContextWithFloor`'s splice correct with no changes: `chainBound` skips a
// node with noDeadline, so a value node is invisible to the deadline walk, and
// the floor still pushes its deadline nodes above the whole of `next`. Two chains would have needed the splice to know
// which one it was splicing.
//
// # The zero value is the root
//
// `Context{}` has a nil node, which is a context with no deadline and no
// parent — exactly `Context.root()`. That is the opposite of the choice
// `opaque.go` records for `Duration`, where a zero value is a legitimate span
// and detecting "unset" would double the type; here "unset" and "root" are the
// same state, so the zero value is meaningful rather than ambiguous and
// `ContextRoot` allocates nothing.
//
// Two roots are therefore `==` in Go. Nothing observes it: `Context` has no
// `impl Equatable`, Equal answers false for an Opaque value, and a Context is
// not keyable, so a Nomi program cannot compare two Contexts or key a map by
// one. Anyone adding an equality path here needs to
// know that Go's `==` is not Nomi's answer.
type Context struct{ node *contextNode }

// contextNode is one link in the chain: a deadline, a value binding, or (for
// the root's absence) neither.
//
// `clock` rather than a `*int64` or a sentinel says whether the link bounds a
// deadline: `ends` uses the full int64 range, so no value of it can mean
// "absent", and a pointer would put a second allocation on every deriver.
//
// `tid` needs no such flag, because nil is unreachable for a real binding: the
// VM passes an interned `*TypeID` at every `with_value` call. So `tid !=
// nil` is "this node binds a value", the same way a clock is "this node bounds
// a deadline", and the two are independent: a value node has noDeadline and
// is therefore invisible to chainBound.
type contextNode struct {
	// ends is when the link's deadline ends, on the clock `clock` names.
	ends   int64
	clock  deadlineClock
	parent *contextNode
	// tid is the identity of the Nomi type this node binds a value for, or nil
	// when the node binds none. Compared by address and never by its contents.
	tid *TypeID
	// val is the bound value, boxed. Its dynamic Go type is the one Go type
	// the caller uses for the Nomi type `tid` names, so the assertion in
	// ContextValue cannot fail on a checked program.
	val any
}

// deadlineClock is the clock a link's deadline is kept on, or noDeadline for
// a link that bounds none.
//
// # Two requests, two clocks
//
// `with_timeout(c, d)` asks for `d` of elapsed time. It is kept on the
// monotonic clock (Monotonic), which only moves forward at the rate of real
// time, so setting the system clock, by NTP, a suspended laptop or a user,
// neither fires it early nor holds it late. `with_deadline(c, at)` asks for a
// moment on the wall clock. It is kept as that Instant and measured against
// the wall clock each time something asks, so a step of the wall clock moves
// it, as it moves the moment it names.
//
// Which of two links on different clocks ends first depends on the clocks'
// readings, so a chain is reduced to its earliest link on each clock
// (chainBound), and a reading decides between those two only when something
// asks (deadlineBound.inForce). Every reader goes through inForce: the Instant
// `Context.deadline` reports, the time left, and the Go context a `with` line
// derives (EnterDeadline).
type deadlineClock uint8

const (
	noDeadline deadlineClock = iota
	// elapsedClock is a timeout's: ends is a Monotonic reading.
	elapsedClock
	// wallClock is a named deadline's: ends is an Instant.
	wallClock
)

// ContextRoot is `Context.root()`: no deadline, no parent.
func ContextRoot() Context { return Context{} }

// ContextWithDeadline is `Context.with_deadline(c, at)`: a deadline when the
// wall clock reads `at`, kept on the wall clock (deadlineClock).
//
// It pushes a node rather than computing min(parent, at) eagerly. The
// documented rule is "if the parent has a tighter deadline already, the parent's
// deadline wins", and it falls out of resolving at read time. Computing the
// minimum here gives the same answer for this call and the wrong one for a later
// widening derivation, because the parent's node would already have been
// collapsed away.
func ContextWithDeadline(c Context, at Instant) Context {
	return Context{node: &contextNode{ends: int64(at), clock: wallClock, parent: c.node}}
}

// ContextWithTimeout is `Context.with_timeout(c, dur)`: a deadline `dur` of
// elapsed time from now, kept on the monotonic clock (deadlineClock), pushed
// for ContextWithDeadline's reason.
func ContextWithTimeout(c Context, dur Duration) Context {
	return contextWithTimeout(c, dur, MonotonicNow())
}

// contextWithTimeout is ContextWithTimeout at the monotonic reading now.
//
// The sum saturates: a timeout that ends past the latest reading ends there,
// about 292 years after the process started, past the latest Instant, which
// std/context documents as a deadline no program reaches. dur itself arrived
// saturated at int64 (hostadapt.DurationNanos), so an unsaturated sum would
// wrap and end a long timeout at once.
func contextWithTimeout(c Context, dur Duration, now Monotonic) Context {
	ends := addSaturating(int64(now), int64(dur))
	return Context{node: &contextNode{ends: ends, clock: elapsedClock, parent: c.node}}
}

// ContextWithValue is `Context.with_value(c, value)`: a child context binding
// `v` under the identity of its own Nomi type.
//
// The key is the caller's, not this function's.
// std declares `with_value<T>(c: Context, value: T): Context` — there is
// no witness argument — so the type under which the value is filed is the
// static type of the argument, which only the call site knows. The IR builder
// reads it off the checker's solved signature and the VM passes that type's
// interned `*TypeID`; a `tid` derived here from Go reflection would be a second answer
// to a question the front end has already answered, and the two could disagree
// for a distinct over a scalar (`type TraceId String` and `type Subject String`
// are one Go `string` at run time and two Nomi types).
//
// Pushes a node, which is ContextWithDeadline's choice for a related reason:
// the documented rule is that a child shadows its parent for that type and the
// parent is unchanged, and it falls out of resolving at read time. Collapsing
// two bindings of one type into a single node would give the same answer for the
// child and the wrong one for the parent, which is exactly the assertion
// context_values_test.nomi makes twice.
func ContextWithValue(c Context, tid *TypeID, v any) Context {
	return Context{node: &contextNode{parent: c.node, tid: tid, val: v}}
}

// ContextValue is `Context.value(c, value_type) -> Option<T>`: the nearest binding
// for the witnessed type, walking to the root.
//
// The contract: "child values override parent values without mutating either
// context". The first hit wins, so a shadowing derivation is answered by
// the walk order rather than by anything this function does.
//
// The assertion is checked rather than a bare `.(T)`, and the failure is
// loud: one Nomi type has one `*TypeID` and
// one Go type, so a matching identity carrying a different dynamic type would
// mean a value was filed under an identity that is not its own — a wrong
// answer, and a silent `Option` of the zero value is the one outcome worse than a
// trap. It cannot fire on a checked program.
func ContextValue[T any](c Context, ty Type[T]) Option[T] {
	if ty.TID == nil {
		// No checked program produces a zero witness (rt/typewitness.go), and a
		// nil key must not match a node — every value node carries a non-nil
		// one, so the walk below would already answer None. Returned early so
		// that stays true of a future node shape rather than by coincidence.
		return None[T]()
	}
	for n := c.node; n != nil; n = n.parent {
		if n.tid != ty.TID {
			continue
		}
		v, ok := n.val.(T)
		if !ok {
			panic(&Error{Msg: fmt.Sprintf(
				"nomi: context value for type '%s' holds a %T, which is not that type's representation",
				ty.TID.Nomi, n.val)})
		}
		return Some(v)
	}
	return None[T]()
}

// ContextWithFloor is the rebind rule for `with MyApp.context = next`: the
// result carries `next`, bounded below by whatever deadline was in force
// before the rebind. A rebind can tighten a deadline and never widen one.
//
// A rebind without the floor would let a program escape a bound its caller was
// already under, and a widened deadline is a wrong answer rather than a missing
// feature.
//
// A splice onto the existing chain rather than a new field anywhere, and that is
// what makes it small: chainBound already walks the whole chain and takes the
// earliest link on each clock, so pushing `prev`'s earliest link on each clock
// as nodes above `next` makes every later reader compute the floor with no
// changes. Both are pushed, not only the one in force now, because which of
// the two ends first depends on the clocks' readings (deadlineClock). There
// is nothing to cancel and no blocking operation to teach, which matters because
// missing one is invisible — a blocking call that consulted the unfloored
// deadline would simply wait too long.
//
// The value this reads — the app-field cell before the rebind — is the
// caller's in-force context, put there by the caller's own `with`. Nested
// rebinds compose for the same reason: each tightens against its immediate predecessor, which was itself
// already floored.
//
// `prev` with no deadline returns `next` unchanged rather than splicing an
// unset node, so a program that never had a bound does not acquire an empty link
// per rebind.
func ContextWithFloor(prev, next Context) Context {
	b := chainBound(prev)
	node := next.node
	if b.hasElapsed {
		node = &contextNode{ends: b.elapsed, clock: elapsedClock, parent: node}
	}
	if b.hasWall {
		node = &contextNode{ends: b.wall, clock: wallClock, parent: node}
	}
	return Context{node: node}
}

// ContextWithoutDeadline is the chain with every deadline link dropped and
// every value link kept.
//
// The reason: work handed
// to a Supervisor outlives the call that spawned it, so the ceiling that call
// was under is not its ceiling, while what the context carries — a trace id, a
// request id — still belongs to the work. That is why this strips deadlines
// rather than handing the task `ContextRoot()`.
//
// Supervisor task frames use this value for their designated Context field.
// The underlying node is preserved when the chain has no deadline link.
func ContextWithoutDeadline(c Context) Context {
	return Context{node: withoutDeadlineNode(c.node)}
}

// withoutDeadlineNode rebuilds one chain without its deadline links.
//
// A pure deadline link is dropped rather than rebuilt with noDeadline: the
// node would then carry nothing, and chainBound and
// `contextLookupValue` would both walk past it. Keeping it would grow the chain
// on every detach for no observable.
//
// A node that survives unchanged is returned as itself, so a chain of pure
// value links is not copied. Contexts are immutable, so sharing a tail is safe
// and is what `ContextWithValue` already relies on.
func withoutDeadlineNode(n *contextNode) *contextNode {
	if n == nil {
		return nil
	}
	parent := withoutDeadlineNode(n.parent)
	if n.tid == nil {
		// Nothing but a deadline. Its parent chain replaces it.
		return parent
	}
	if n.clock == noDeadline && parent == n.parent {
		return n
	}
	return &contextNode{parent: parent, tid: n.tid, val: n.val}
}

// deadlineBound is the deadline a chain is under: its earliest link on each
// clock. chainBound decides it without reading either clock, and inForce
// decides at a reading which of the two ends first.
type deadlineBound struct {
	elapsed, wall       int64
	hasElapsed, hasWall bool
}

// chainBound is the earliest deadline on this node or any ancestor, per
// clock, which is where "the parent's deadline wins if tighter" is decided.
func chainBound(c Context) deadlineBound {
	var b deadlineBound
	for n := c.node; n != nil; n = n.parent {
		switch n.clock {
		case elapsedClock:
			if !b.hasElapsed || n.ends < b.elapsed {
				b.elapsed, b.hasElapsed = n.ends, true
			}
		case wallClock:
			if !b.hasWall || n.ends < b.wall {
				b.wall, b.hasWall = n.ends, true
			}
		}
	}
	return b
}

// set reports whether the chain has a deadline at all.
func (b deadlineBound) set() bool { return b.hasElapsed || b.hasWall }

// clockReading is one reading of both clocks: the wall clock as an Instant
// and the monotonic clock as a Monotonic.
type clockReading struct {
	wall Instant
	mono Monotonic
}

// readingOf is the reading a Go time carries. Under `clock Clock.Virtual`
// time.Now carries no monotonic reading, so mono is the bubble's clock minus
// the origin, as MonotonicNow says, and both clocks are the bubble's.
func readingOf(t time.Time) clockReading {
	return clockReading{wall: Instant(t.UnixNano()), mono: Monotonic(t.Sub(monotonicOrigin))}
}

// inForce is the deadline in force at the reading now: the time left until
// it ends, negative once it has passed, and the Instant it ends at.
//
// A timeout's time left is its end minus the monotonic reading, and the
// Instant it ends at is the wall clock's reading plus that time. A named
// deadline's Instant is the one it names, and its time left is that minus the
// wall clock's reading. The one with less time left is in force; on a tie the
// named one, whose Instant is exact.
func (b deadlineBound) inForce(now clockReading) (left Duration, at Instant) {
	if b.hasWall {
		left, at = Duration(subSaturating(b.wall, int64(now.wall))), Instant(b.wall)
	}
	if b.hasElapsed {
		l := Duration(subSaturating(b.elapsed, int64(now.mono)))
		if !b.hasWall || l < left {
			left, at = l, Instant(addSaturating(int64(now.wall), int64(l)))
		}
	}
	return left, at
}

// ContextDeadline is `Context.deadline(c) -> Option<Instant>`: the Instant the
// deadline in force ends at, read now (deadlineBound.inForce).
func ContextDeadline(c Context) Option[Instant] {
	b := chainBound(c)
	if !b.set() {
		return None[Instant]()
	}
	_, at := b.inForce(readingOf(time.Now()))
	return Some(at)
}

// ContextDeadlineRemaining is `Context.deadline_remaining(c) -> Option<Duration>`.
//
// Clamped at zero, because std/context documents `Some(0)` as "the deadline has
// passed" and a negative Duration would render as `-5s` through
// DurationToString, a different string, not a different number.
func ContextDeadlineRemaining(c Context) Option[Duration] {
	b := chainBound(c)
	if !b.set() {
		return None[Duration]()
	}
	left, _ := b.inForce(readingOf(time.Now()))
	return Some(max(left, 0))
}

// addSaturating is a + b, held at the int64 range instead of wrapping.
func addSaturating(a, b int64) int64 {
	sum := a + b
	switch {
	case b > 0 && sum < a:
		return math.MaxInt64
	case b < 0 && sum > a:
		return math.MinInt64
	}
	return sum
}

// subSaturating is a - b, held at the int64 range instead of wrapping.
func subSaturating(a, b int64) int64 {
	diff := a - b
	switch {
	case b < 0 && diff < a:
		return math.MaxInt64
	case b > 0 && diff > a:
		return math.MinInt64
	}
	return diff
}

// ContextInspect renders a Context, and it renders the contents away on purpose.
//
// The reason: a Context's deadline is read against the clock, so rendering it
// would make every `${ctx}` in a test output time-dependent. A program that
// wants the deadline asks `Context.deadline` for it.
func ContextInspect(c Context) string { return "<context>" }

// OpaqueText makes a Context an Opaque value, so the VM holding one in an
// erased position renders it `<context>` in every rendering, equal to
// nothing, never a Map key.
func (c Context) OpaqueText() string { return ContextInspect(c) }
