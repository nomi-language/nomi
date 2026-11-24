package rt

import (
	"context"
	"sync/atomic"
	"testing"
)

// A cancel ends a task's context at once and sets its frame's stop flag
// later, from the goroutine context.AfterFunc starts. A task woken from a
// wait by the cancel unwinds in between, and its deferred calls decide by
// Ended whether to run with no safe point. Read from the flag, a cleanup
// begun in that window was stopped at its first call once the flag landed,
// and a `defer` that sends on a channel sent nothing (seen under the race
// detector's scheduling in tests/16-concurrency). The frame here is that
// window, held still: the context has ended and the flag is not set.
func TestFrame_EndedHoldsBeforeTheStopFlagIsSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fr := &Frame{ctx: ctx, stop: new(atomic.Bool)}
	if fr.Stopping() {
		t.Fatal("the frame's stop flag is set; the test needs it unset")
	}
	if !fr.Ended() {
		t.Fatal("Ended is false on a frame whose context has ended")
	}
	if Unstoppable(fr).Stoppable() {
		t.Fatal("the cleanup frame of an ended frame still has a safe point")
	}

	live := NewFrame(context.Background())
	if live.Ended() {
		t.Fatal("Ended is true on a frame whose context has not ended")
	}
	var none *Frame
	if none.Ended() {
		t.Fatal("Ended is true on a nil frame")
	}
}
