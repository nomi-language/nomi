package rt

import (
	"reflect"
	"time"
)

// `Channel.select` and `Receiver.receive_within`: waiting on several channel
// operations and timers at once.
//
// # An arm is erased, its handler is not here
//
// std declares `Arm<R>` over three constructors: a receive on a
// `Receiver<T>`, a send of a `T` on a `Sender<T>`, and a timer. One select
// takes arms over channels of different element types, so the wait runs over
// erased cases (SelectCase), through reflect.Select. The handler each arm
// carries is a Nomi function the caller runs on the winning case's outcome;
// rt waits and reports which case won and with what, and runs no Nomi code.
//
// # Which ready case wins
//
// The first listed case that can finish as the select starts wins, so a run
// repeats exactly. A first pass tries each case in order without blocking;
// only when none can finish does the select block, and then the first case
// to become ready wins (Go breaks a tie between cases that become ready at
// the same instant). std documents the bias and the rotation that undoes it.
//
// # Closed channels
//
// A receive on a closed channel finishes at once with no value, Go's own
// two-value receive. A send on a closed channel finishes at once as closed:
// the first pass tests the closed flag under the channel's close mutex, as
// SenderSend does, so it never reaches Go's panicking send. A close that
// lands while the select is blocked on a send is SenderSend's residual race,
// with the same answer.
//
// # Timers and the clock in force
//
// A timer is a time.Timer started when the select starts. Under `clock
// Clock.Virtual` the case runs inside a testing/synctest bubble, where a
// timer fires as soon as every goroutine in the bubble is blocked, so a
// select with a timer never waits in real time there. A timer of zero or
// less is ready in the first pass.

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
	// ch is the channel's Go channel, as a reflect.Value so cases over
	// different element types sit in one list.
	ch reflect.Value
	// closed reports whether a send case's channel is closed, read under
	// lock, which the first pass holds while it tries the send.
	closed func() bool
	lock   func()
	unlock func()
	// value is a send case's value.
	value reflect.Value
	// wait is a timer case's duration.
	wait Duration
}

// SelectReceive is a case that receives from r.
func SelectReceive[T any](r Receiver[T]) SelectCase {
	return SelectCase{kind: selectReceive, ch: reflect.ValueOf(r.c.ch)}
}

// SelectSend is a case that sends v on s.
func SelectSend[T any](s Sender[T], v T) SelectCase {
	c := s.c
	val := reflect.New(reflect.TypeFor[T]()).Elem()
	if any(v) != nil {
		val.Set(reflect.ValueOf(v))
	}
	return SelectCase{
		kind:   selectSend,
		ch:     reflect.ValueOf(c.ch),
		closed: c.closed.Load,
		lock:   c.closeMu.Lock,
		unlock: c.closeMu.Unlock,
		value:  val,
	}
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

// SelectEmptyText is the fault a select with no arms raises.
func SelectEmptyText() string { return "Channel.select: needs at least one arm" }

// Select waits until one of cases can finish and reports it.
//
// It takes the frame for SenderSend's reason: the blocking wait is a
// cancellation point, and a cancelled wait unwinds rather than answering.
func Select(fr *Frame, cases []SelectCase) Selected {
	if len(cases) == 0 {
		Trap(SelectEmptyText())
	}
	if got, ready := selectNow(cases); ready {
		return got
	}
	// Parked for the blocking wait, as ReceiverReceive is: a task waiting
	// here has no scheduled wake a draining supervisor could see.
	SupervisedPark(fr)
	defer SupervisedUnpark(fr)
	rcases := make([]reflect.SelectCase, 0, len(cases)+1)
	index := make([]int, 0, len(cases))
	for i, c := range cases {
		switch c.kind {
		case selectReceive:
			rcases = append(rcases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: c.ch})
		case selectSend:
			rcases = append(rcases, reflect.SelectCase{Dir: reflect.SelectSend, Chan: c.ch, Send: c.value})
		case selectAfter:
			timer := time.NewTimer(time.Duration(c.wait))
			defer stopTimer(timer)
			rcases = append(rcases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(timer.C)})
		}
		index = append(index, i)
	}
	rcases = append(rcases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(fr.Context().Done())})
	chosen, v, ok := reflect.Select(rcases)
	if chosen == len(index) {
		raiseCanceled(fr)
		panic("unreachable")
	}
	i := index[chosen]
	switch cases[i].kind {
	case selectReceive:
		if !ok {
			return Selected{Index: i}
		}
		return Selected{Index: i, Value: v.Interface(), OK: true}
	case selectSend:
		return Selected{Index: i, OK: true}
	}
	return Selected{Index: i, OK: true}
}

// selectNow is the first pass: the first case, in order, that can finish
// without blocking.
func selectNow(cases []SelectCase) (Selected, bool) {
	for i, c := range cases {
		switch c.kind {
		case selectReceive:
			chosen, v, ok := reflect.Select([]reflect.SelectCase{
				{Dir: reflect.SelectRecv, Chan: c.ch},
				{Dir: reflect.SelectDefault},
			})
			if chosen == 0 {
				if !ok {
					return Selected{Index: i}, true
				}
				return Selected{Index: i, Value: v.Interface(), OK: true}, true
			}
		case selectSend:
			c.lock()
			if c.closed() {
				c.unlock()
				return Selected{Index: i}, true
			}
			chosen, _, _ := reflect.Select([]reflect.SelectCase{
				{Dir: reflect.SelectSend, Chan: c.ch, Send: c.value},
				{Dir: reflect.SelectDefault},
			})
			c.unlock()
			if chosen == 0 {
				return Selected{Index: i, OK: true}, true
			}
		case selectAfter:
			if c.wait <= 0 {
				return Selected{Index: i, OK: true}, true
			}
		}
	}
	return Selected{}, false
}

// stopTimer stops a timer and drains a tick it may have delivered, SleepCtx's
// cleanup.
func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

// TimedOut is Nomi's `std/channels.TimedOut`: the marker
// `Receiver.receive_within` reports when its wait ran out. ChannelClosed's
// shape.
type TimedOut struct{}

// ReceiverReceiveWithin is `Receiver.receive_within(receiver, wait)`: a
// select over the receive and one timer, the receive listed first so a value
// already waiting wins over a timer of zero.
func ReceiverReceiveWithin[T any](fr *Frame, r Receiver[T], wait Duration) Result[Maybe[T], TimedOut] {
	got := Select(fr, []SelectCase{SelectReceive(r), SelectAfter(wait)})
	if got.Index == 1 {
		return Err[Maybe[T], TimedOut](TimedOut{})
	}
	if !got.OK {
		return Ok[Maybe[T], TimedOut](None[T]())
	}
	return Ok[Maybe[T], TimedOut](Some(got.Value.(T)))
}

// Arm is Nomi's `std/channels.Arm<R>`: one case of a select and its handler.
// R is the handler's result; the handler is the embedder's value, which rt
// holds without calling.
type Arm[R any] struct {
	Case    SelectCase
	Handler any
}
