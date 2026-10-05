package rt

import (
	"context"
	"strings"
	"testing"
	"time"
)

// `EnterDeadline` is the one place a Nomi deadline becomes something a blocking
// operation can observe, and it is why rt is a consumer of the Context chain
// rather than only its provider. Nine `fr.ctx` readers in this package depend
// on it and none of them mentions a deadline; frame.go's header enumerates
// them.
//
// `tests/15-app-and-defer/deadline_floor/deadline_floor_test.nomi`
// covers it end to end, which is the right place for the observable rule, but a
// corpus fixture cannot say which half failed and it runs only when the whole
// toolchain does. A deadline that is merely too loose produces no wrong string
// until something waits on it, so a defect here stays invisible to any test
// that does not wait.
//
// These also pin the module boundary the first-party-adapter question turns
// on. If the deadline walk ever leaves rt, `contextEffectiveDeadline` stops
// resolving and these stop compiling — which is a better failure than a
// program that reads a deadline correctly and waits past it.

func TestEnterDeadline_NoDeadlineReturnsTheParentUnchanged(t *testing.T) {
	parent := NewFrame(context.Background())
	child, release := EnterDeadline(parent, ContextRoot())
	defer release()

	if child != parent {
		t.Fatal("a deadline-free rebind allocated a frame")
	}
	if _, ok := child.Context().Deadline(); ok {
		t.Fatal("a deadline-free rebind put a deadline on the frame")
	}
}

func TestEnterDeadline_AFutureDeadlineReachesTheFrame(t *testing.T) {
	parent := NewFrame(context.Background())
	at := time.Now().Add(time.Hour)
	child, release := EnterDeadline(parent, ContextWithDeadline(ContextRoot(), Instant(at.UnixNano())))
	defer release()

	got, ok := child.Context().Deadline()
	if !ok {
		t.Fatal("the frame carries no deadline, so every blocking operation would ignore it")
	}
	if !got.Equal(at) {
		t.Fatalf("frame deadline %v, Context deadline %v", got, at)
	}
	if parent.Context().Err() != nil {
		t.Fatal("the parent frame was mutated")
	}
}

// Already spent: a deadline in the past has to yield a context that is Done
// before the first wait starts, not one whose timer fires after the operation
// has been entered. frame.go states this arm.
func TestEnterDeadline_APastDeadlineIsAlreadyDone(t *testing.T) {
	parent := NewFrame(context.Background())
	past := time.Now().Add(-time.Hour)
	child, release := EnterDeadline(parent, ContextWithDeadline(ContextRoot(), Instant(past.UnixNano())))
	defer release()

	select {
	case <-child.Context().Done():
	default:
		t.Fatal("a past deadline produced a live context; the first blocking operation would start a wait")
	}
	if parent.Context().Err() != nil {
		t.Fatal("cancelling the child cancelled the parent")
	}
}

// The earliest link along the chain is what reaches the frame, which is the
// same rule `Context.deadline` reports and the reason `ContextWithFloor` can be
// a splice: a rebind can tighten and never widen.
func TestEnterDeadline_TakesTheEarliestLinkOnTheChain(t *testing.T) {
	parent := NewFrame(context.Background())
	tight := time.Now().Add(time.Minute)
	loose := tight.Add(time.Hour)

	chain := ContextWithDeadline(ContextRoot(), Instant(tight.UnixNano()))
	chain = ContextWithDeadline(chain, Instant(loose.UnixNano()))

	child, release := EnterDeadline(parent, chain)
	defer release()

	got, ok := child.Context().Deadline()
	if !ok {
		t.Fatal("the frame carries no deadline")
	}
	if !got.Equal(tight) {
		t.Fatalf("frame deadline %v, want the tighter %v", got, tight)
	}
}

// A value link leaves `set` false, so it must be invisible to this walk too —
// otherwise `with MyApp.context = Context.with_value(…)` would drop whatever
// bound the caller was under. One node type serves both link kinds, which is
// what makes this fall out rather than needing an arm.
func TestEnterDeadline_AValueLinkDoesNotHideAnAncestorDeadline(t *testing.T) {
	parent := NewFrame(context.Background())
	at := time.Now().Add(time.Hour)

	chain := ContextWithDeadline(ContextRoot(), Instant(at.UnixNano()))
	chain = ContextWithValue(chain, &TypeID{Nomi: "enterdl.TraceId"}, "t-1")

	child, release := EnterDeadline(parent, chain)
	defer release()

	got, ok := child.Context().Deadline()
	if !ok {
		t.Fatal("a value link hid the ancestor deadline from the frame")
	}
	if !got.Equal(at) {
		t.Fatalf("frame deadline %v, want %v", got, at)
	}
}

// `raiseCanceled`'s `!inTask` arm has two causes and each gets its own answer.
// A frame under a Nomi deadline reports the deadline (MainDeadlineFault) and a
// frame under neither task nor deadline keeps the lowering-bug diagnostic that
// TestConcurrent_StrayCancellationIsAFault constructs.
//
// Both rows, because either alone passes under the wrong rule: asserting only
// the deadline row passes if the arm were made unconditional, which would name
// a deadline that never existed.
func TestRaiseCanceled_ADeadlineAndAStrayCancellationReportDifferently(t *testing.T) {
	spent := ContextWithDeadline(ContextRoot(), Instant(time.Now().Add(-time.Hour).UnixNano()))
	fr, release := EnterDeadline(NewFrame(context.Background()), spent)
	defer release()

	err := recoverFault(func() { CancelIfDone(fr) })
	if err == nil {
		t.Fatal("a spent deadline on a task-less frame returned normally")
	}
	if !strings.Contains(err.Msg, "deadline exceeded") {
		t.Errorf("a bounded program was told %q instead of the deadline", err.Msg)
	}
	if strings.Contains(err.Msg, "no task to unwind") {
		t.Errorf("a bounded program was handed the builder-bug diagnostic: %q", err.Msg)
	}

	// The control: no deadline, no task. Same call, other message.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stray := recoverFault(func() { CancelIfDone(&Frame{ctx: ctx}) })
	if stray == nil {
		t.Fatal("a stray cancellation returned normally")
	}
	if !strings.Contains(stray.Msg, "no task to unwind") {
		t.Errorf("a stray cancellation is %q and no longer says why", stray.Msg)
	}
}

// A `concurrent { }` block wrapped by the rebind runs on EnterScope's frame, and
// its awaits raise there. EnterScope builds that frame field by field; if it
// left `underDeadline` out, the block's awaits would see a task-less frame with
// no deadline and report `no task to unwind` instead of the deadline. The
// corpus case "env context timeout cancels blocked channel receives" takes this
// path.
func TestEnterScope_InheritsDeadline(t *testing.T) {
	spent := ContextWithDeadline(ContextRoot(), Instant(time.Now().Add(-time.Hour).UnixNano()))
	fr, release := EnterDeadline(NewFrame(context.Background()), spent)
	defer release()

	block := EnterScope(fr)
	err := recoverFault(func() {
		defer ScopeExit(block)
		CancelIfDone(block)
	})
	if err == nil {
		t.Fatal("a spent deadline on a block frame returned normally")
	}
	if !strings.Contains(err.Msg, "deadline exceeded") {
		t.Errorf("a block under a spent deadline reported %q instead of the deadline", err.Msg)
	}
}

// The control for the row above: inside a block under a live deadline, awaiting
// a task `Task.cancel` stopped raises with the block's ctx still running. The
// flag alone would name a deadline that has not run out, so the cause is
// checked too and the stray-cancellation diagnostic stays.
func TestEnterScope_ACancelledTaskUnderALiveDeadlineIsNotTheDeadline(t *testing.T) {
	live := ContextWithDeadline(ContextRoot(), Instant(time.Now().Add(time.Hour).UnixNano()))
	fr, release := EnterDeadline(NewFrame(context.Background()), live)
	defer release()

	block := EnterScope(fr)
	err := recoverFault(func() {
		defer ScopeExit(block)
		h := TaskSpawn[Unit](block, func(tf *Frame) Unit {
			<-tf.Context().Done()
			CancelIfDone(tf)
			return Unit{}
		})
		TaskCancel(h)
		TaskAwait(block, h)
	})
	if err == nil {
		t.Fatal("awaiting a cancelled task on the main line returned normally")
	}
	if err.Msg != AwaitedCancelledTaskText() {
		t.Errorf("awaiting a cancelled task under a live deadline reported %q", err.Msg)
	}
}
