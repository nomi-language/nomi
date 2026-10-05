package rt

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Nomi's `std/channels` — a typed FIFO channel, held as its two halves.
//
// # One runtime object, two static types
//
// std/channels.nomi declares `pub struct Channel<T> { sender: Sender<T>;
// receiver: Receiver<T> }` over two `pub host type`s, and puts the operations on
// the halves: `Sender.send`, `Receiver.receive`, `Sender.close`. There is
// deliberately no `Channel.send`/`receive`/`close`, and `close` is on `Sender`
// alone — a consumer closing what it reads ends the stream for every producer
// still writing, which is the bug the split exists to prevent.
//
// The split is entirely static: the direction is enforced by which of
// `Sender`/`Receiver` a function will accept, and there is nothing at runtime
// to keep apart. So one `*Chan[T]` holds everything, and
// `Sender[T]`/`Receiver[T]` are two one-field defined types over it whose only
// job is to be mutually unassignable.
//
// Two Go types rather than one alias, because Nomi keeps them apart and a Go
// alias would let a wrong-direction call compile. `SenderClose(r.c)` must be
// a Go type error and it is.
//
// # Why the pair is not `chan T` twice
//
// A Nomi `send` on a closed channel returns `Err(ChannelClosed)`; a bare Go send
// on a closed channel panics. So the closed flag has to be consulted under a
// mutex that `close` also takes, and the flag plus the mutex plus the channel
// are one object with one lifetime — which is what `Chan[T]` is. The sequencing
// below decides which of send-vs-close wins a race.
//
// # Blocking is reachable, and it is cancellable
//
// `Sender.send` on a full buffer blocks, and `Receiver.receive` on an empty open
// channel blocks. Both are how programs use a channel: an unbuffered send is
// a rendezvous that only completes because the other half is in a sibling task.
//
// So both take a `*rt.Frame` and select on its context, which is built from
// the enclosing `concurrent` block; when it fires, the operation raises the
// unwind concurrent.go defines.
//
// A program with no `concurrent` block has a root context that is never
// cancelled, so the ctx arm never fires and a blocked send in a
// single-goroutine program hangs, detected by Go's scheduler ("all goroutines
// are asleep - deadlock!").
//
// # The observable strings live here, once
//
// `Channel.buffered(0)` and a double `Sender.close` are the two faults this
// surface can raise, and their text is user-visible. trap.go's rule applies
// (an observable string has one copy), so the two format functions below are
// the only copy. Neither carries a `line N:` prefix: under `nomi run`,
// `Channel.buffered<Int>(0)` prints the message and nothing else.

// Chan is one channel: the Go channel, its capacity, and the closed flag that
// keeps a Nomi `send` from panicking.
//
// Not exported as a Nomi type. `Sender[T]` and `Receiver[T]` are what a Nomi
// signature names; this is the object both of them point at.
type Chan[T any] struct {
	ch  chan T
	cap int
	// closed is read on the send fast path without the mutex held only after
	// the mutex has been taken — see ChanSend. It is atomic because `close`
	// sets it under CloseMu while a parked sender may be observing it.
	closed  atomic.Bool
	closeMu sync.Mutex
}

// Sender is Nomi's `std/channels.Sender<T>`: the writing half, and the half that
// may close.
//
// A one-field struct rather than a defined type over `*Chan[T]`, so the field
// stays unexported and no other package can reach the channel directly. One
// is built only through ChannelBuffered / ChannelUnbuffered.
type Sender[T any] struct {
	c *Chan[T]
}

// Receiver is Nomi's `std/channels.Receiver<T>`: the reading half. Receiving is
// all it does — it cannot write and it cannot close, which is the type system
// carrying std's rule rather than a comment asking for it.
type Receiver[T any] struct {
	c *Chan[T]
}

// Channel is Nomi's `std/channels.Channel<T>`: the pair, as std declares it.
//
// Both fields are exported because another package (internal/vm) reads
// `ch.sender` / `ch.receiver` out of the pair, and both name the halves in
// std's own field order. Their Go names are what internal/irbuild's
// stdGenStructSpec row spells; a rename here without a rename there produces no
// anchor and every mention of `Channel<T>` refuses.
type Channel[T any] struct {
	Sender   Sender[T]
	Receiver Receiver[T]
}

// ChannelClosed is Nomi's `std/channels.ChannelClosed`: the marker
// `Sender.send` reports when the channel was closed at enqueue time.
//
// `pub type ChannelClosed` with no inner, so a zero-width struct. It is the `E`
// of `Result<Unit, ChannelClosed>` and carries nothing; two of them are equal
// because there is nothing to differ.
type ChannelClosed struct{}

// --- construction -----------------------------------------------------------

// ChannelBuffered is `Channel.buffered(capacity)`.
//
// Zero is rejected rather than aliased to the unbuffered constructor, which is
// std's decision: "a channel is buffered or it is a
// rendezvous, and 0 is not a spelling of the second one". A computed capacity
// sliding silently from hand-off to rendezvous is the thing the rejection buys.
func ChannelBuffered[T any](capacity int64) Channel[T] {
	if capacity < 1 {
		Trap(ChannelCapacityText(capacity))
	}
	return channelOf(&Chan[T]{ch: make(chan T, capacity), cap: int(capacity)})
}

// ChannelUnbuffered is `Channel.unbuffered()`: every send is a rendezvous.
func ChannelUnbuffered[T any]() Channel[T] {
	return channelOf(&Chan[T]{ch: make(chan T)})
}

// channelOf wraps one channel as the pair its halves are read out of. Both
// halves hold the same *Chan.
func channelOf[T any](c *Chan[T]) Channel[T] {
	return Channel[T]{Sender: Sender[T]{c: c}, Receiver: Receiver[T]{c: c}}
}

// ChannelCapacityText is the fault `Channel.buffered(n < 1)` raises.
//
// A function rather than an inline literal for trap.go's stated reason: an
// observable string has one copy.
func ChannelCapacityText(capacity int64) string {
	return fmt.Sprintf(
		"Channel.buffered: capacity must be at least 1, got %d — for a channel with no buffer, use Channel.unbuffered()",
		capacity)
}

// ChannelDoubleCloseText is the fault a second `Sender.close` raises. A
// function for ChannelCapacityText's reason.
func ChannelDoubleCloseText() string { return "Sender.close: channel already closed" }

// --- the three operations ---------------------------------------------------

// SenderSend is `Sender.send(sender, value)`.
//
// The order of its three steps is the part that matters rather than the code:
//
//  1. Take CloseMu and test `closed`. A send after close is
//     `Err(ChannelClosed)` and never a panic, and the mutex is what makes the
//     test atomic with respect to a concurrent close.
//  2. Try a non-blocking send while still holding the mutex. This is the whole
//     buffered case under capacity, and it completes before any close can run.
//  3. Otherwise release the mutex and park on a blocking send or the frame's
//     cancellation, so a concurrent close is not deadlocked behind a sender
//     waiting for a receiver and an abandoned sender does not outlive its block.
//
// Step 3 has a residual race: a close completing while a sender is parked.
// The send arm fires only on a successful enqueue, so arriving means the value
// was taken or buffered before the close, and either way the answer is Ok. The
// other half of that race: a close that completes while this is parked makes
// Go's own send panic, which a task wrapper reports as `Failed(Panicked(...))`
// rather than killing the process.
//
// The cancel arm is only in step 3, not in steps 1 and 2. A send that can
// complete without blocking completes: Nomi's rule is that cancellation is
// observed where a task waits,
// not that it poisons work already possible. Putting a ctx test in front of
// step 1 would make a cancelled task's buffered send fail nondeterministically
// depending on whether Go's select happened to pick the Done arm.
func SenderSend[T any](fr *Frame, s Sender[T], v T) Result[Unit, ChannelClosed] {
	c := s.c
	c.closeMu.Lock()
	if c.closed.Load() {
		c.closeMu.Unlock()
		return Err[Unit, ChannelClosed](ChannelClosed{})
	}
	select {
	case c.ch <- v:
		c.closeMu.Unlock()
		return Ok[Unit, ChannelClosed](Unit{})
	default:
	}
	c.closeMu.Unlock()
	// Parked for the duration of the blocking send: a task waiting on a full
	// channel has no scheduled wake, so a draining permanent supervisor must be
	// able to see that there is nothing left to wait for. No-op outside a
	// supervised task. See rt/supervisor.go's drainOnce.
	SupervisedPark(fr)
	defer SupervisedUnpark(fr)
	select {
	case c.ch <- v:
		return Ok[Unit, ChannelClosed](Unit{})
	case <-fr.Context().Done():
		raiseCanceled(fr)
	}
	// Unreachable: both arms above return or panic. See TaskAwait for why this
	// is a panic rather than a zero value.
	panic("unreachable")
}

// ReceiverReceive is `Receiver.receive(receiver)`.
//
// `None` is the closed-and-drained answer, which is Go's own two-value receive
// read directly: a closed channel yields its buffered values first and reports
// !ok only once empty, which is exactly what std documents ("after close, any
// remaining buffered values are delivered to subsequent `receive` calls").
//
// The cancel arm is a peer of the receive arm rather than a test in front of it,
// for SenderSend's reason: a receive that can complete completes. Go picks
// randomly when both are ready, and that nondeterminism is accepted.
func ReceiverReceive[T any](fr *Frame, r Receiver[T]) Maybe[T] {
	// The fast path first, so a receive that can complete now never touches the
	// tracker: a park/unpark pair around a wait that does not happen would make
	// `settled()` flicker for a supervisor that is busy.
	select {
	case v, ok := <-r.c.ch:
		if !ok {
			return None[T]()
		}
		return Some(v)
	default:
	}
	// Parked: a task blocked on an empty open channel has no scheduled wake.
	// This is the arm the message-loop worker sits in for its whole life, and
	// the one the drain's settle check exists for.
	SupervisedPark(fr)
	defer SupervisedUnpark(fr)
	select {
	case v, ok := <-r.c.ch:
		if !ok {
			return None[T]()
		}
		return Some(v)
	case <-fr.Context().Done():
		raiseCanceled(fr)
	}
	panic("unreachable")
}

// SenderClose is `Sender.close(sender)`.
//
// Double close is a fault, matching Go's panic-on-double-close as a Nomi trap
// rather than as a Go panic.
// The flag flips under CloseMu so a sender in SenderSend's mutex-guarded fast
// path observes it consistently.
func SenderClose[T any](s Sender[T]) Unit {
	c := s.c
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if !c.closed.CompareAndSwap(false, true) {
		Trap(ChannelDoubleCloseText())
	}
	close(c.ch)
	return Unit{}
}
