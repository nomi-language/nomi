package rt

import (
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
// to keep apart. So one `*channel` holds everything, and
// `Sender[T]`/`Receiver[T]` are two one-field types over it whose only
// job is to be mutually unassignable.
//
// Two Go types rather than one alias, because Nomi keeps them apart and a Go
// alias would let a wrong-direction call compile. `SenderClose(r)` must be a
// Go type error and it is.
//
// # A wait object, not a Go channel
//
// The channel is rt's own: a buffer, a closed flag and two waiter lists
// (wait.go) under one lock. A Go channel would hand a value to some parked
// goroutine without saying which, and the task registry has to know which
// task a send woke at the moment it woke it (package doc, "Deadlock"). So a
// send that finds a receiver waiting takes it off the list, hands it the
// value and marks its task running, under the lock; a receive does the same
// for a waiting sender; a close wakes every waiter with nothing.
//
// The element is held as `any`: one select waits on channels of several
// element types, and the VM's channels are `Channel<any>` already.
//
// # Blocking is reachable, and it is cancellable
//
// `Sender.send` on a full buffer blocks, and `Receiver.receive` on an empty
// open channel blocks. Both are how programs use a channel: an unbuffered
// send is a rendezvous that only completes because the other half is in a
// sibling task.
//
// So both take a `*rt.Frame` and wait on its context too, which is built
// from the enclosing `concurrent` block; when it ends, the operation raises
// the unwind concurrent.go defines. An operation that can complete without
// waiting completes, whatever the context: cancellation is observed where a
// task waits, and does not poison work already possible.
//
// A wait no task can ever end is a deadlock, which the registry reports to
// the main line once every task waits.
//
// This surface raises no fault: `Channel.select` takes a NonEmptyList of
// arms, and a second `Sender.close` does nothing.

// channel is one channel: its buffer, its closed flag and its waiters.
type channel struct {
	// id orders channels for a select that locks several (Select).
	id uint64
	mu sync.Mutex
	// buf is a ring of the channel's capacity, holding n values from head;
	// nil for an unbuffered channel.
	buf     []any
	head, n int
	closed  bool
	recvq   waitq
	sendq   waitq
	// fed counts what outside the tasks may send on the channel or close
	// it: a signal's feed, a close_on_drain hook. Guarded by the task
	// registry's lock (taskRegistry.feed), since it is read when the
	// registry decides whether a wait could end.
	fed int
	// drainHook says a close_on_drain hook is registered and counted in
	// fed; unhookDrain unregisters it, and the owner's close calls it, so
	// a closed channel is not closed again at shutdown.
	drainHook   bool
	unhookDrain func()
}

var channelIDs atomic.Uint64

func newChannel(capacity int64) *channel {
	c := &channel{id: channelIDs.Add(1)}
	if capacity > 0 {
		c.buf = make([]any, capacity)
	}
	return c
}

// Sender is Nomi's `std/channels.Sender<T>`: the writing half, and the half that
// may close.
//
// A one-field struct, so the field stays unexported and no other package
// can reach the channel directly. One is built only through
// ChannelBuffered / ChannelUnbuffered.
type Sender[T any] struct {
	c *channel
}

// Receiver is Nomi's `std/channels.Receiver<T>`: the reading half. Receiving is
// all it does — it cannot write and it cannot close, which is the type system
// carrying std's rule rather than a comment asking for it.
type Receiver[T any] struct {
	c *channel
}

// ChannelTypeName is the run-time type name of a `Channel<T>` record, the
// engine's descriptor for std's struct. DebugText renders a record of it.
const ChannelTypeName = "channels.Channel"

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
// std declares capacity a PositiveInt rather than aliasing 0 to the
// unbuffered constructor: "a channel is buffered or it is a rendezvous, and 0
// is not a spelling of the second one". A computed capacity goes through
// `PositiveInt.try_from`, so it cannot slide silently from hand-off to
// rendezvous.
func ChannelBuffered[T any](capacity int64) Channel[T] {
	return channelOf[T](newChannel(capacity))
}

// ChannelUnbuffered is `Channel.unbuffered()`: every send is a rendezvous.
func ChannelUnbuffered[T any]() Channel[T] {
	return channelOf[T](newChannel(0))
}

// channelOf wraps one channel as the pair its halves are read out of.
func channelOf[T any](c *channel) Channel[T] {
	return Channel[T]{Sender: Sender[T]{c: c}, Receiver: Receiver[T]{c: c}}
}

// valueOf is a received value as T: `any` holds every element, and a nil
// interface is T's zero.
func valueOf[T any](v any) T {
	if v == nil {
		var zero T
		return zero
	}
	return v.(T)
}

// --- the operations under the lock -------------------------------------------

// trySendLocked finishes a send of v that need not wait: to a waiting
// receiver, which it answers for the caller to release once the lock is
// let go, or into the buffer. ready is false when the send must wait.
// Called with c.mu held and c open.
func (c *channel) trySendLocked(v any) (woke *waitEntry, ready bool) {
	if e := c.recvq.claim(); e != nil {
		e.v, e.ok = v, true
		return e, true
	}
	if c.n < len(c.buf) {
		c.buf[(c.head+c.n)%len(c.buf)] = v
		c.n++
		return nil, true
	}
	return nil, false
}

// tryReceiveLocked finishes a receive that need not wait: from the buffer,
// refilled from a waiting sender, or straight from a waiting sender, or as
// closed (ok false). woke is a sender to release once the lock is let go.
func (c *channel) tryReceiveLocked() (v any, ok bool, woke *waitEntry, ready bool) {
	if c.n > 0 {
		v = c.buf[c.head]
		c.buf[c.head] = nil
		c.head = (c.head + 1) % len(c.buf)
		c.n--
		if e := c.sendq.claim(); e != nil {
			c.buf[(c.head+c.n)%len(c.buf)] = e.v
			c.n++
			e.ok = true
			woke = e
		}
		return v, true, woke, true
	}
	if e := c.sendq.claim(); e != nil {
		e.ok = true
		return e.v, true, e, true
	}
	if c.closed {
		return nil, false, nil, true
	}
	return nil, false, nil, false
}

// --- the three operations ---------------------------------------------------

// SenderSend is `Sender.send(sender, value)`, waiting at site when it must.
//
// A send after close is `Err(ChannelClosed)` and never a panic. A send that
// finds a receiver waiting hands it the value; one with room in the buffer
// buffers it; otherwise it waits until a receiver takes it, the channel
// closes (Err) or the frame's context ends (the unwind). When a close lands
// while it waits, the send answers Err.
func SenderSend[T any](fr *Frame, site Site, s Sender[T], v T) Result[Unit, ChannelClosed] {
	c := s.c
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Err[Unit, ChannelClosed](ChannelClosed{})
	}
	if woke, ready := c.trySendLocked(v); ready {
		c.mu.Unlock()
		if woke != nil {
			woke.p.release()
		}
		return Ok[Unit, ChannelClosed](Unit{})
	}
	p := parkerOf(fr)
	defer p.close()
	e := &p.ents[0]
	e.reset(p, 0)
	e.v = v
	c.sendq.push(e)
	w := wait{kind: waitSend, site: site, ctx: fr.Context(), ch: c}
	p.block(w)
	c.mu.Unlock()
	won := p.sleep(w)
	if won == 0 {
		if e.ok {
			return Ok[Unit, ChannelClosed](Unit{})
		}
		return Err[Unit, ChannelClosed](ChannelClosed{})
	}
	c.mu.Lock()
	c.sendq.remove(e)
	c.mu.Unlock()
	if won == wonDeadlock {
		p.deadlocked()
	}
	raiseCanceled(fr)
	panic("unreachable")
}

// ReceiverReceive is `Receiver.receive(receiver)`, waiting at site when it
// must.
//
// `None` is the closed-and-drained answer: a closed channel yields its
// buffered values first and answers None only once empty, which is what std
// documents ("after close, any remaining buffered values are delivered to
// subsequent `receive` calls").
func ReceiverReceive[T any](fr *Frame, site Site, r Receiver[T]) Option[T] {
	c := r.c
	c.mu.Lock()
	if v, ok, woke, ready := c.tryReceiveLocked(); ready {
		c.mu.Unlock()
		if woke != nil {
			woke.p.release()
		}
		if !ok {
			return None[T]()
		}
		return Some(valueOf[T](v))
	}
	p := parkerOf(fr)
	defer p.close()
	e := &p.ents[0]
	e.reset(p, 0)
	c.recvq.push(e)
	w := wait{kind: waitReceive, site: site, ctx: fr.Context(), ch: c}
	p.block(w)
	c.mu.Unlock()
	won := p.sleep(w)
	if won == 0 {
		if !e.ok {
			return None[T]()
		}
		return Some(valueOf[T](e.v))
	}
	c.mu.Lock()
	c.recvq.remove(e)
	c.mu.Unlock()
	if won == wonDeadlock {
		p.deadlocked()
	}
	raiseCanceled(fr)
	panic("unreachable")
}

// SenderClose is `Sender.close(sender)`.
//
// A second close does nothing: a send after the first already answers
// Err(ChannelClosed), so nothing is lost, and a shutdown path that may close
// twice need not track whether it has.
func SenderClose[T any](s Sender[T]) Unit {
	s.c.close()
	return Unit{}
}

// close closes c: every waiting receiver answers None and every waiting
// sender Err, and a close_on_drain hook no longer feeds it.
func (c *channel) close() {
	c.mu.Lock()
	if c.unhookDrain != nil {
		c.unhookDrain()
		c.unhookDrain = nil
	}
	c.shutLocked()
	c.mu.Unlock()
}

// shutLocked closes c, with c.mu held, waking its waiters. A wake is
// recorded before the hook's feed is let go, so the registry never finds a
// woken waiter unaccounted for.
func (c *channel) shutLocked() {
	if c.closed {
		return
	}
	c.closed = true
	for e := c.recvq.claim(); e != nil; e = c.recvq.claim() {
		e.v, e.ok = nil, false
		e.p.release()
	}
	for e := c.sendq.claim(); e != nil; e = c.sendq.claim() {
		e.ok = false
		e.p.release()
	}
	if c.drainHook {
		c.drainHook = false
		tasks.feed(c, -1)
	}
}

// SenderCloseOnDrain is `Sender.close_on_drain(sender)`: the channel closes
// when the program starts draining its supervisors (OnDrain), so a task that
// reads it until it closes finishes what is queued and ends, and the drain
// does not wait out its grace period for a reader parked on an empty
// channel. A close by the owner first unregisters it; one after it does
// nothing, as any second close does. While the hook is registered the
// channel is fed: a shutdown signal may close it, so a wait on it is not a
// deadlock.
func SenderCloseOnDrain[T any](s Sender[T]) Unit {
	c := s.c
	c.mu.Lock()
	if c.closed || c.drainHook {
		c.mu.Unlock()
		return Unit{}
	}
	// Counted before the lock is let go, so a reader that waits from now
	// on is never one the registry thinks nothing can wake.
	c.drainHook = true
	c.unhookDrain = func() {}
	tasks.feed(c, 1)
	c.mu.Unlock()
	// Registered outside the lock: when the drain has already started,
	// OnDrain runs the hook at once, and the hook takes the lock.
	unhook := OnDrain(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.shutLocked()
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		// The owner closed it, or the hook already ran: nothing is left
		// for the hook to do.
		unhook()
	} else {
		c.unhookDrain = unhook
	}
	return Unit{}
}
