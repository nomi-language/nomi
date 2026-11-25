package rt

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt/vclock"
)

// bubbleThen is vclock.RunCase with then run inside the bubble after the
// case's own cleanup has returned: what a task the case left running does
// once the case has ended its supervisors and before the bubble does.
func bubbleThen(then func()) func(body func() error, cleanup func()) error {
	return func(body func() error, cleanup func()) error {
		return vclock.RunCase(body, func() {
			cleanup()
			then()
		})
	}
}

// A drain hook a virtual-clock case registers after its cleanup runs at
// once, inside the case's bubble, and the next case's drain does not run
// it. Run by the next case, it would close a channel the first case's
// bubble made, and Go ends the process for that ("close of synctest channel
// from outside bubble"), so a regression stops this test binary.
func TestRunBubbled_AHookRegisteredAfterTheCleanupStaysInItsCase(t *testing.T) {
	ResetSupervisors()
	defer ResetSupervisors()

	var lateClosed bool
	err := RunBubbled(bubbleThen(func() {
		late := ChannelBuffered[Int](1)
		SenderCloseOnDrain(late.Sender)
		lateClosed = ReceiverReceive(NewFrame(context.Background()), Site{}, late.Receiver).Tag == TagNone
	}), func() error { return nil }, nil)
	if err != nil {
		t.Fatalf("the first case: %v", err)
	}
	if !lateClosed {
		t.Error("a channel registered after the case's cleanup was not closed at once")
	}
	if err := RunBubbled(vclock.RunCase, func() error { return nil }, nil); err != nil {
		t.Fatalf("the next case: %v", err)
	}
}

// A bubble still waiting outside the program caseGrace after its case's time
// ran out is given up on: RunBubbled answers ErrCaseStillRunning without
// waiting for it, and no case starts in the process after it, bubbled or
// not, since a second bubble beside the first could close a channel of the
// first. The wait here is on a channel made outside the bubble, which the
// bubble's clock does not count as blocked, as a socket's is not.
func TestRunBubbled_ABubbleWaitingOutsideIsTheLastCaseToStart(t *testing.T) {
	ResetSupervisors()
	defer ResetSupervisors()
	defer func(grace time.Duration) { caseGrace = grace }(caseGrace)
	caseGrace = 10 * time.Millisecond

	outside := make(chan struct{})
	stop := make(chan struct{})
	close(stop)
	err := RunBubbled(vclock.RunCase, func() error { <-outside; return nil }, stop)
	if !errors.Is(err, ErrCaseStillRunning) {
		t.Fatalf("a bubble waiting outside answered %v, want ErrCaseStillRunning", err)
	}
	stuck := stuckCase.Load()
	defer func() {
		close(outside)
		if err := <-stuck.ended; err != nil {
			t.Errorf("the bubble given up on ended with %v", err)
		}
		stuckCase.Store(nil)
	}()

	ran := 0
	body := func() error { ran++; return nil }
	if err := RunBubbled(vclock.RunCase, body, nil); !errors.Is(err, ErrCaseNotRun) {
		t.Errorf("a bubble after it answered %v, want ErrCaseNotRun", err)
	}
	failures, _, _ := RunTestCases(context.Background(), []Test{
		{Name: "real clock", Fn: func(*Frame) TestFailure { ran++; return nil }},
		{Name: "virtual clock", Fn: func(*Frame) TestFailure { ran++; return nil }, Bubble: vclock.RunCase},
	})
	for i, f := range failures {
		if !errors.Is(f, ErrCaseNotRun) {
			t.Errorf("case %d after it answered %v, want ErrCaseNotRun", i, f)
		}
	}
	if ran != 0 {
		t.Errorf("%d cases started after the bubble given up on, want 0", ran)
	}
}

// A bubble that ends within caseGrace of its case's time running out is the
// case's result, and the next case starts.
func TestRunBubbled_ABubbleThatEndsOnItsStopIsItsCasesResult(t *testing.T) {
	ResetSupervisors()
	defer ResetSupervisors()

	stop := make(chan struct{})
	close(stop)
	want := errors.New("stopped")
	if err := RunBubbled(vclock.RunCase, func() error { return want }, stop); err != want {
		t.Fatalf("a bubble that ended answered %v, want its own error", err)
	}
	if err := RunBubbled(vclock.RunCase, func() error { return nil }, nil); err != nil {
		t.Fatalf("the next case: %v", err)
	}
}
