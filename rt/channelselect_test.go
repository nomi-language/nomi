package rt

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// The first pass takes the first listed case that can finish, and leaves the
// others alone: a losing receive must not take a value.
func TestSelect_TheFirstListedReadyCaseWins(t *testing.T) {
	fr := NewFrame(context.Background())
	a := ChannelBuffered[int64](1)
	b := ChannelBuffered[int64](1)
	SenderSend(fr, a.Sender, 1)
	SenderSend(fr, b.Sender, 2)

	got := Select(fr, []SelectCase{SelectReceive(b.Receiver), SelectReceive(a.Receiver)})
	if got.Index != 0 || !got.OK || got.Value != int64(2) {
		t.Fatalf("got %+v, want case 0 with 2", got)
	}
	if v := ReceiverReceive(fr, a.Receiver); v.Tag != TagSome || v.Some != 1 {
		t.Fatalf("the losing case's channel holds %+v, want Some(1)", v)
	}
}

// A send on a closed channel answers as closed rather than reaching Go's
// panicking send, and a receive on one answers with no value.
func TestSelect_AClosedChannelIsReadyAtOnce(t *testing.T) {
	fr := NewFrame(context.Background())
	ch := ChannelBuffered[int64](1)
	SenderClose(ch.Sender)

	if got := Select(fr, []SelectCase{SelectSend(ch.Sender, 1)}); got.Index != 0 || got.OK {
		t.Fatalf("a send on a closed channel answered %+v, want case 0 not sent", got)
	}
	if got := Select(fr, []SelectCase{SelectReceive(ch.Receiver)}); got.Index != 0 || got.OK {
		t.Fatalf("a receive on a closed channel answered %+v, want case 0 with no value", got)
	}
}

// A timer of zero is ready in the first pass, so listed last it turns a
// select into a poll; listed after a ready receive it loses.
func TestSelect_AZeroTimerIsReadyInTheFirstPass(t *testing.T) {
	fr := NewFrame(context.Background())
	ch := ChannelBuffered[int64](1)
	if got := Select(fr, []SelectCase{SelectReceive(ch.Receiver), SelectAfter(0)}); got.Index != 1 {
		t.Fatalf("an empty channel and a zero timer answered %+v, want the timer", got)
	}
	SenderSend(fr, ch.Sender, 4)
	if got := Select(fr, []SelectCase{SelectReceive(ch.Receiver), SelectAfter(0)}); got.Index != 0 {
		t.Fatalf("a ready channel and a zero timer answered %+v, want the channel", got)
	}
}

// Timers run on the clock in force: inside a synctest bubble the select's wait
// is virtual, and the clock moves exactly to the shorter timer.
func TestSelect_TimersRunOnTheVirtualClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fr := NewFrame(context.Background())
		ch := ChannelBuffered[int64](1)
		start := time.Now()
		got := Select(fr, []SelectCase{
			SelectReceive(ch.Receiver),
			SelectAfter(Duration(3 * time.Second)),
			SelectAfter(Duration(2 * time.Second)),
		})
		if got.Index != 2 {
			t.Fatalf("got %+v, want the two-second timer", got)
		}
		if elapsed := time.Since(start); elapsed != 2*time.Second {
			t.Fatalf("the select took %v of virtual time, want 2s", elapsed)
		}
	})
}

// receive_within lists the receive first, so a value already waiting beats a
// zero timer, and a wait that runs out is TimedOut.
func TestReceiverReceiveWithin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fr := NewFrame(context.Background())
		ch := ChannelBuffered[int64](1)
		if got := ReceiverReceiveWithin(fr, ch.Receiver, Duration(time.Second)); got.Tag != TagErr {
			t.Fatalf("an empty channel answered %+v, want TimedOut", got)
		}
		SenderSend(fr, ch.Sender, 8)
		if got := ReceiverReceiveWithin(fr, ch.Receiver, 0); got.Tag != TagOk || got.Ok.Tag != TagSome || got.Ok.Some != 8 {
			t.Fatalf("a waiting value answered %+v, want Ok(Some(8))", got)
		}
		SenderClose(ch.Sender)
		if got := ReceiverReceiveWithin(fr, ch.Receiver, Duration(time.Second)); got.Tag != TagOk || got.Ok.Tag != TagNone {
			t.Fatalf("a closed channel answered %+v, want Ok(None)", got)
		}
	})
}

// A task parked in a select unwinds when its block is cancelled, as one parked
// in a receive does, and the statement after the select never runs.
func TestSelect_AParkedSelectObservesCancellation(t *testing.T) {
	fr := EnterScope(NewFrame(context.Background()))
	ch := ChannelUnbuffered[int64]()
	var reached bool
	var mu sync.Mutex
	h := TaskSpawn(fr, func(inner *Frame) int64 {
		Select(inner, []SelectCase{SelectReceive(ch.Receiver)})
		mu.Lock()
		reached = true
		mu.Unlock()
		return 0
	})
	ScopeExit(fr)

	if h.t.tag != TagCancelled {
		t.Errorf("a task parked in a select settled as tag %d, want TagCancelled (%d)", h.t.tag, TagCancelled)
	}
	mu.Lock()
	defer mu.Unlock()
	if reached {
		t.Error("the statement after the parked select executed, so the select returned instead of unwinding")
	}
}
