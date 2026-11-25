package rt

import (
	"slices"
)

// `Channel.select` and `Receiver.receive_within`: waiting on several channel
// operations and timers at once.
//
// # An arm is erased, its handler is not here
//
// std declares `Arm<R>` over three constructors: a receive on a
// `Receiver<T>`, a send of a `T` on a `Sender<T>`, and a timer. One select
// takes arms over channels of different element types, so the wait runs over
// erased cases (SelectCase), each over the channel's `any` elements. The
// handler each arm carries is a Nomi function the caller runs on the winning
// case's outcome; rt waits and reports which case won and with what, and runs
// no Nomi code.
//
// # Which ready case wins
//
// The first listed case that can finish as the select starts wins, so a run
// repeats exactly: the select locks every channel it names, in the order of
// their ids, and tries each case in order. Only when none can finish does it
// wait, on every channel's waiter list at once with one parker (wait.go),
// and then the first case another task completes wins. std documents the
// bias and the rotation that undoes it.
//
// # Closed channels
//
// A receive on a closed channel finishes at once with no value. A send on a
// closed channel finishes at once as closed, and a close that lands while
// the select waits finishes the send case as closed, as SenderSend's waiting
// send answers Err.
//
// # Timers and the clock in force
//
// A timer case is one timer started when the select starts to wait, for the
// shortest of the select's timers; the first listed timer case of that
// length is the one it finishes. A timer of zero or less is ready at once.
// Under `clock Clock.Virtual` the case runs inside a testing/synctest
// bubble, where a timer fires as soon as every goroutine in the bubble is
// blocked, so a select with a timer never waits in real time there.

// selectKind is what one SelectCase waits for.
type selectKind uint8

const (
	selectReceive selectKind = iota
	selectSend
	selectAfter
)

// SelectCase is one erased arm of a select.
type SelectCase struct {
	kind selectKind
	c    *channel
	// value is a send case's value.
	value any
	// wait is a timer case's duration.
	wait Duration
}

// SelectReceive is a case that receives from r.
func SelectReceive[T any](r Receiver[T]) SelectCase {
	return SelectCase{kind: selectReceive, c: r.c}
}

// SelectSend is a case that sends v on s.
func SelectSend[T any](s Sender[T], v T) SelectCase {
	return SelectCase{kind: selectSend, c: s.c, value: v}
}

// SelectAfter is a case that finishes once d has passed.
func SelectAfter(d Duration) SelectCase {
	return SelectCase{kind: selectAfter, wait: d}
}

// Selected is the case a select finished on and its outcome: for a receive,
// the value and whether there was one (false when the channel is closed and
// drained); for a send, whether it was sent (false when the channel is
// closed).
type Selected struct {
	Index int
	Value any
	OK    bool
}

// Select waits at site until one of cases can finish and reports it. cases
// holds at least one: `Channel.select` takes a NonEmptyList.
//
// It takes the frame for SenderSend's reason: the wait is a cancellation
// point, and a cancelled wait unwinds rather than answering.
func Select(fr *Frame, site Site, cases []SelectCase) Selected {
	chans := make([]*channel, 0, len(cases))
	for _, c := range cases {
		if c.c != nil && !slices.Contains(chans, c.c) {
			chans = append(chans, c.c)
		}
	}
	slices.SortFunc(chans, func(a, b *channel) int {
		switch {
		case a.id < b.id:
			return -1
		case a.id > b.id:
			return 1
		}
		return 0
	})
	for _, c := range chans {
		c.mu.Lock()
	}
	unlock := func() {
		for _, c := range chans {
			c.mu.Unlock()
		}
	}
	if got, woke, ready := selectNowLocked(cases); ready {
		unlock()
		if woke != nil {
			woke.p.release()
		}
		return got
	}
	p := parkerOf(fr)
	defer p.close()
	entries := make([]waitEntry, len(cases))
	w := wait{kind: waitSelect, site: site, ctx: fr.Context(), cases: cases}
	for i, c := range cases {
		e := &entries[i]
		e.reset(p, i)
		switch c.kind {
		case selectReceive:
			c.c.recvq.push(e)
		case selectSend:
			e.v = c.value
			c.c.sendq.push(e)
		case selectAfter:
			if !w.timed || c.wait < w.after {
				w.timed, w.after, w.afterWon = true, c.wait, i
			}
		}
	}
	p.block(w)
	unlock()
	won := p.sleep(w)
	for _, c := range chans {
		c.mu.Lock()
	}
	for i, c := range cases {
		switch c.kind {
		case selectReceive:
			c.c.recvq.remove(&entries[i])
		case selectSend:
			c.c.sendq.remove(&entries[i])
		}
	}
	unlock()
	switch won {
	case wonCancelled:
		raiseCanceled(fr)
	case wonDeadlock:
		p.deadlocked()
	}
	e := &entries[won]
	switch cases[won].kind {
	case selectReceive:
		if !e.ok {
			return Selected{Index: won}
		}
		return Selected{Index: won, Value: e.v, OK: true}
	case selectSend:
		return Selected{Index: won, OK: e.ok}
	}
	return Selected{Index: won, OK: true}
}

// selectNowLocked is the first case, in order, that can finish without
// waiting, with every channel's lock held; woke is a waiter it completed,
// for the caller to release once the locks are let go.
func selectNowLocked(cases []SelectCase) (got Selected, woke *waitEntry, ready bool) {
	for i, c := range cases {
		switch c.kind {
		case selectReceive:
			if v, ok, w, now := c.c.tryReceiveLocked(); now {
				if !ok {
					return Selected{Index: i}, w, true
				}
				return Selected{Index: i, Value: v, OK: true}, w, true
			}
		case selectSend:
			if c.c.closed {
				return Selected{Index: i}, nil, true
			}
			if w, now := c.c.trySendLocked(c.value); now {
				return Selected{Index: i, OK: true}, w, true
			}
		case selectAfter:
			if c.wait <= 0 {
				return Selected{Index: i, OK: true}, nil, true
			}
		}
	}
	return Selected{}, nil, false
}

// TimedOut is Nomi's `std/channels.TimedOut`: the marker
// `Receiver.receive_within` reports when its wait ran out. ChannelClosed's
// shape.
type TimedOut struct{}

// ReceiverReceiveWithin is `Receiver.receive_within(receiver, wait)`: a
// select over the receive and one timer, the receive listed first so a value
// already waiting wins over a timer of zero.
func ReceiverReceiveWithin[T any](fr *Frame, site Site, r Receiver[T], wait Duration) Result[Option[T], TimedOut] {
	got := Select(fr, site, []SelectCase{SelectReceive(r), SelectAfter(wait)})
	if got.Index == 1 {
		return Err[Option[T], TimedOut](TimedOut{})
	}
	if !got.OK {
		return Ok[Option[T], TimedOut](None[T]())
	}
	return Ok[Option[T], TimedOut](Some(valueOf[T](got.Value)))
}
