package rt

import (
	"context"
	"time"
)

// Frame is the per-activation state a Nomi program needs beyond its registers,
// carried by every VM activation and passed to every rt operation that needs
// it.
//
// It holds the dynamic context that registers cannot: `once` cycle detection,
// cancellation safe points, and scoped application fields. Scoped fields are
// the binding constraint: dynamic scoping (a deep callee reading `App.field` that
// no intermediate function mentions) has exactly three implementations in Go —
// thread a frame, use goroutine-local storage (deliberately absent from the
// language), or stuff values into a context.Context, which is a string-keyed
// allocating version of this struct.
//
// # Threading it must not allocate per call
//
// This is the design's one hard constraint, not a preference. Iter callback
// dispatch is a cancellation safe point, so the frame rides the iterator hot
// loop, and the existential-representation decision measured zero allocations
// per element with no frame parameter at all. A frame allocated per call would
// silently put ~2 allocations per element back — a performance regression rather
// than a wrong answer, which is the hardest kind to notice. Measured at 1000
// elements: threading a pointer and polling ctx.Done() per element costs ~1% and
// allocates nothing; allocating a frame per call costs 22% and 1001 allocations.
//
// # A Frame is allocated only where the scope genuinely changes
//
// A new Frame belongs at a scoped replacement (installing the value in force), forcing a
// `once` (extending a forcing cons-list), entering an `assert`/`refute`
// (setting a trace), and `concurrent`/`spawn` (a task gets its own frame over
// the parent's context). A program with none of those still allocates exactly
// one Frame, in Main, and every call passes the same pointer down unchanged.
//
// `trace` is still absent because a cons-list head's only correct
// implementation depends on the construct that pushes onto it — `trace` needs
// the assertion renderer's collection boundary — and declaring it empty would
// ship a field no code reads and no test can constrain, fixing its shape
// before the construct that determines it exists.
type Frame struct {
	ctx context.Context
	// forcing is this lineage's stack of `once` cells whose RHS is currently
	// being evaluated, as an immutable cons-list. Nil on every frame except
	// the ones a force created, which is every frame in almost every program.
	// It is DYNAMIC context — propagated caller-to-callee by the frame pointer
	// rather than lexically — because that is what makes a cycle through an
	// ordinary function call visible at the re-entry. See once.go.
	forcing *forcing
	// scope is the innermost `concurrent { }` block this lineage is inside, or
	// nil. DYNAMIC context for `forcing`'s reason and more sharply: a
	// `Task.spawn` in a helper function called from inside a block has no
	// lexical route to the block. See concurrent.go, which records the three
	// alternatives to this field and why each is worse.
	scope *Scope
	// inTask marks a frame running INSIDE a spawned task body, which is the
	// one place a cooperative cancellation may be raised as a panic — because
	// the task's own goroutine wrapper is the only thing that recovers one.
	//
	// A bool rather than "scope != nil": a task body has no scope of its own
	// until it opens a nested `concurrent` block, and a block on the MAIN
	// goroutine has a scope while having nothing above it to unwind to. The
	// two questions are genuinely different. See raiseCanceled.
	inTask bool
	// inBoot marks a frame running WHILE the app value is being constructed,
	// which is the one window `Supervisor.new` may be called in.
	//
	// THE REQUIREMENT IS TEMPORAL, NOT LEXICAL, and a frame flag is what makes
	// it so. It was once a lexical `currentFnName != "boot"` check and that was
	// wrong: it rejected a server type creating its own supervisor in a
	// constructor `boot` calls, which cost a second app-struct field per server
	// and made `Restart.Permanent` unusable, since one shared supervisor cannot
	// be permanent for some of its work and not the rest. The rule is that
	// supervisors are created WHILE boot runs, not that the call is written
	// inside it — and a flag propagated caller-to-callee by the frame pointer
	// is exactly that, because every call passes the frame down.
	//
	// A frame field is narrower than a process-wide counter around the boot
	// callback would be: a goroutine started during boot does not inherit it,
	// because a task body's frame is built by rt and sets only `inTask`.
	inBoot bool
	// underDeadline marks a frame whose `ctx` ends because a NOMI DEADLINE in
	// force here ran out, rather than because something cancelled it. Set only
	// by EnterDeadline, so it is exactly "a `with MyApp.context = …` rebind is
	// in scope and it carried a deadline".
	//
	// It exists because `raiseCanceled`'s `!inTask` arm has TWO causes. A
	// frame's ctx is closed by a Scope's `cancel`, a Task's, or EnterDeadline,
	// and the last closes it on the MAIN goroutine, WHILE the body runs.
	// Without the flag an ordinary program (`with MyApp.context =
	// Context.with_timeout(…)`, then a `timer.sleep` past it) reached a fault
	// that reads as a lowering bug instead of the deadline. A flag rather than
	// reading `fr.ctx.Err()`, so the cause is recorded where the deadline is
	// installed rather than reconstructed from the error afterwards.
	//
	// Scoped by the frame like `inBoot`, and for the same reason: the caller
	// uses the child frame for the rebind's scope and the parent after, so a callee
	// several frames down sees the flag and a sibling statement afterwards does
	// not.
	underDeadline bool
	// park is the supervisor tracker a supervised task's blocking waits report
	// to, or nil. Read only by the DRAIN, to tell "still working" from "nothing
	// left to wait for" — see supervisor.go's drainOnce.
	park *parkTracker
	// scopedFields are immutable snapshots inherited by child frames.
	scopedFields  map[string]any
	scopedContext *Context
	// booted is what boot PUBLISHED, which no `with` override changes. It is
	// what a `once` initializer reads instead of the forcer's scoped fields;
	// see forcingOnce. Every frame built by hand must carry it across.
	booted      *bootedApp
	bootCleanup *bootCleanupState
	startup     *Startup
}

// EnterBoot returns the frame the app value is constructed on.
//
// A CHILD frame rather than a mutation, for EnterScope's reason: the parent
// outlives the construction and must not report `inBoot` afterwards. Everything
// else is carried across, because a boot expression is an ordinary expression
// that may force a `once` or read a context.
func EnterBoot(parent *Frame) *Frame {
	child := *parent
	child.inBoot = true
	return &child
}

// EnterDeadline returns the frame a `with MyApp.context = next` scope runs on:
// a child whose Go context carries `next`'s effective deadline, plus a release
// the caller MUST invoke at scope exit.
//
// # WHY THIS EXISTS, AND WHY ITS ABSENCE IS INVISIBLE
//
// A Nomi deadline has two halves. `Context` is a pure deadline CHAIN that
// readers walk (`Context.deadline_remaining`), and `ContextWithFloor` installs
// a tightened one into the app-field cell. But every blocking operation in
// this package selects on `fr.ctx` — NINE sites: `TimerSleep`, `SenderSend`,
// `ReceiverReceive`, `TaskAwait`, `TaskOutcome`, `CancelIfDone`, the scope
// wait in `ScopeExit`, `SupervisorFlush`, and the batch semaphore in
// `spawnScopeTask`. Without this function nothing puts the Nomi deadline onto
// that context, so a rebind bounds what a program can READ and nothing it can
// WAIT on.
//
// That failure produces no wrong string until something waits on it:
// `concurrency.md:L800` bounds a five-minute `timer.sleep` with a 50ms
// deadline and must answer "timed out, falling back" in well under a second.
// A missing deadline there sleeps for the full five minutes.
//
// # ONE CHOKEPOINT, NOT NINE PATCHES
//
// Two things can stop blocking work. The *structural* context comes from the
// enclosing `concurrent { }` block or task. The *ambient* context is the Nomi
// `Context` value in the app's `context` field. A blocking operation answers
// to both.
//
// This composes them at the one place the ambient deadline CHANGES rather than
// at each of the nine places it is read. The structural context is
// `parent.ctx`; the ambient deadline is derived onto it here; and every
// existing `fr.ctx` read is then already correct, with no per-operation change
// — so `spawnScopeTask` selects on the task frame's own `ctx` and needs no
// deadline plumbing of its own. The list of nine sites above covers only
// operations that wait on `fr.ctx`; a route that waits on anything else would
// not appear in it. Deriving per read would
// allocate a context and a timer per Iter element, which frame.go's own hard
// constraint forbids.
//
// ALREADY SPENT rather than skipped: a deadline in the past must yield a
// context that is already Done, so the first blocking operation cancels
// instead of starting a wait that is over before it began.
//
// `context.WithDeadline` DOES THAT ITSELF, checked against Go 1.27 rather than
// reasoned from the docs: `context.WithDeadline(bg, time.Now().Add(-time.Hour))`
// comes back with `Done()` already closed and `Err()` already
// `context.DeadlineExceeded`, because the constructor compares the deadline to
// now and cancels inline rather than arming a timer. A `WithCancel`-then-cancel
// special case would buy nothing and COST the cause (`context.Canceled` in
// place of `context.DeadlineExceeded`), which is a distinction
// `raiseCanceled` needs.
//
// NO DEADLINE returns the parent unchanged and a no-op release, so a program
// that rebinds a deadline-free context neither allocates nor loses the
// structural context it was already under. ContextWithFloor guarantees the
// stored value can only be tighter than what was in force, so the value read
// here is already the effective one.
func EnterDeadline(parent *Frame, next Context) (*Frame, func()) {
	ns, ok := contextEffectiveDeadline(next)
	if !ok {
		return parent, func() {}
	}
	ctx, cancel := context.WithDeadline(parent.ctx, time.Unix(0, ns))
	child := *parent
	child.ctx = ctx
	child.underDeadline = true
	return &child, cancel
}

// NewFrame returns the root frame for a program or a task.
func NewFrame(ctx context.Context) *Frame {
	startup := StartupSnapshot()
	return &Frame{ctx: ctx, bootCleanup: new(bootCleanupState), startup: &startup}
}

// Context is the cancellation and deadline carrier. Idiomatic Go, which is the
// point: Nomi's deadlines interoperate with Go libraries for free, and a
// debugger reads the active context off a real Go struct.
func (fr *Frame) Context() context.Context { return fr.ctx }
