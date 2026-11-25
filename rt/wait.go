package rt

import (
	"sync"
	"sync/atomic"
	"time"
)

// Wait objects: what a task blocks on. Package doc, "Deadlock", says why
// each one is rt's own rather than a Go channel or WaitGroup.
//
// A waiting goroutine has one parker for the whole wait, and an entry on the
// waiter list of each object it waits on (a select's channels, an
// `await_all`'s task and block). Whoever ends the wait claims the parker,
// once: a waker holding an object's lock, the waiter's own context or timer,
// or the registry reporting a deadlock. The claimer sets what won and hands
// the waiter a token; the waiter takes itself off every other list.

// The cases a parker's claim answers besides an entry's index.
const (
	wonCancelled = -1
	wonDeadlock  = -2
)

// parker is one goroutine's wait.
type parker struct {
	// state is 0 while the wait is open and 1 once claimed.
	state atomic.Uint32
	// won is what claimed it: an entry's index, wonCancelled or
	// wonDeadlock. Written by the claimer before the token, read after it.
	won int
	// wake carries the token a claimer other than the waiter sends. Made
	// by the waiter's goroutine, so inside a virtual-clock bubble the wait
	// is one the bubble counts as blocked.
	wake chan struct{}
	// rec is the task's record, or nil for a goroutine that is no task (a
	// signal's feed, a drain).
	rec *taskRec
	// ents are entries for a wait on one or two objects, reused by each of
	// a task's waits; a select makes its own.
	ents [2]waitEntry
}

// parkerOf is the parker for a wait on fr: its task's own, or a fresh one
// for a goroutine that is no task. A record whose parker is in use is run
// by two goroutines, so the registry stops trusting itself and this wait is
// left out of it.
func parkerOf(fr *Frame) *parker {
	if fr != nil {
		if rec := fr.task; rec != nil {
			if rec.busy.CompareAndSwap(false, true) {
				p := &rec.p
				p.open(rec)
				return p
			}
			tasks.distrust()
		}
	}
	p := &parker{}
	p.open(nil)
	return p
}

// open readies p for a wait.
func (p *parker) open(rec *taskRec) {
	p.state.Store(0)
	p.won = 0
	p.rec = rec
	if p.wake == nil {
		p.wake = make(chan struct{}, 1)
	}
}

// close ends p's wait: its record may wait again.
func (p *parker) close() {
	if p.rec != nil {
		p.rec.busy.Store(false)
	}
}

// claim ends p's wait with won, unless something else ended it first.
func (p *parker) claim(won int) bool {
	if !p.state.CompareAndSwap(0, 1) {
		return false
	}
	p.won = won
	return true
}

// release wakes the waiter whose parker the caller claimed: its record runs
// again before the caller goes on, so the caller cannot block before the
// registry counts the waiter running.
func (p *parker) release() {
	if p.rec != nil {
		tasks.resume(p.rec)
	}
	p.wake <- struct{}{}
}

// block records the wait in the registry, with the locks of every object p
// is on the list of held.
func (p *parker) block(w wait) {
	if p.rec != nil {
		tasks.block(p.rec, w)
	}
}

// sleep waits for p to be claimed: by a waker, by w's context's end
// (wonCancelled) or by its timer (w.afterWon). The waiter claims its own
// parker for the last two, and records itself running.
func (p *parker) sleep(w wait) int {
	var done <-chan struct{}
	if w.ctx != nil {
		done = w.ctx.Done()
	}
	var expired <-chan time.Time
	if w.timed {
		timer := time.NewTimer(time.Duration(w.after))
		defer stopTimer(timer)
		expired = timer.C
	}
	var won int
	select {
	case <-p.wake:
		return p.won
	case <-done:
		won = wonCancelled
	case <-expired:
		won = w.afterWon
	}
	if !p.claim(won) {
		// A waker claimed it first; its token is on the way.
		<-p.wake
		return p.won
	}
	if p.rec != nil {
		tasks.resume(p.rec)
	}
	return won
}

// deadlocked raises the report a main line was woken with.
func (p *parker) deadlocked() {
	Trap(p.rec.deadlock)
}

// stopTimer stops a timer and drains a tick it may have delivered.
func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

// waitEntry is a parker on one object's waiter list. For a channel it also
// carries the value: a sender's to send, or the one a receiver was handed,
// and ok, whether one was (false when the channel closed).
type waitEntry struct {
	p          *parker
	index      int
	v          any
	ok         bool
	prev, next *waitEntry
	q          *waitq
}

// reset readies e as p's case index.
func (e *waitEntry) reset(p *parker, index int) {
	e.p, e.index, e.v, e.ok = p, index, nil, false
}

// waitq is an object's waiter list, in arrival order, under the object's
// lock.
type waitq struct {
	head, tail *waitEntry
}

func (q *waitq) push(e *waitEntry) {
	e.q = q
	e.prev, e.next = q.tail, nil
	if q.tail != nil {
		q.tail.next = e
	} else {
		q.head = e
	}
	q.tail = e
}

// remove takes e off q, when it is still on it.
func (q *waitq) remove(e *waitEntry) {
	if e.q != q {
		return
	}
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		q.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		q.tail = e.prev
	}
	e.prev, e.next, e.q = nil, nil, nil
}

// claim takes the first waiter whose wait is still open off q and claims
// it for its entry, dropping entries of waits something else ended. The
// caller hands over the value, then releases the parker.
func (q *waitq) claim() *waitEntry {
	for e := q.head; e != nil; e = q.head {
		q.remove(e)
		if e.p.claim(e.index) {
			return e
		}
	}
	return nil
}

// latch is a wait object that is open while its count is zero: a task's
// end (count 1 until it settles), a block's first failure, a block's tasks
// (one per task still running), a supervisor's enrolled work. Opening wakes
// every waiter.
type latch struct {
	mu    sync.Mutex
	count int
	q     waitq
}

// add changes l's count by n, waking every waiter when it reaches zero.
func (l *latch) add(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.count += n
	if l.count != 0 {
		return
	}
	for e := l.q.claim(); e != nil; e = l.q.claim() {
		e.p.release()
	}
}

// waitLatches waits on fr, as w says, until one of ls is open, and answers
// its index, or what else ended the wait. Two latches are locked in the
// order given, and the one caller that passes two (TaskAwaitAll) is the
// only code that holds two at once.
func waitLatches(fr *Frame, w wait, ls ...*latch) int {
	for _, l := range ls {
		l.mu.Lock()
	}
	for i, l := range ls {
		if l.count == 0 {
			for _, l := range ls {
				l.mu.Unlock()
			}
			return i
		}
	}
	p := parkerOf(fr)
	defer p.close()
	for i, l := range ls {
		e := &p.ents[i]
		e.reset(p, i)
		l.q.push(e)
	}
	p.block(w)
	for _, l := range ls {
		l.mu.Unlock()
	}
	won := p.sleep(w)
	for i, l := range ls {
		l.mu.Lock()
		l.q.remove(&p.ents[i])
		l.mu.Unlock()
	}
	return won
}

// semaphore is a wait object of n slots: `max_running`, for a block's
// `spawn_all` batch and for a supervisor. A release hands its slot straight
// to the first waiter.
type semaphore struct {
	mu   sync.Mutex
	free int64
	q    waitq
}

func newSemaphore(n int64) *semaphore { return &semaphore{free: n} }

// acquire takes a slot for fr's task, waiting as w says. It answers false,
// taking none, when w's context has ended, before or during the wait: a
// task still queued when its block or supervisor stops never runs.
func (s *semaphore) acquire(fr *Frame, w wait) bool {
	if w.ctx.Err() != nil {
		return false
	}
	s.mu.Lock()
	if s.free > 0 {
		s.free--
		s.mu.Unlock()
		return true
	}
	p := parkerOf(fr)
	defer p.close()
	e := &p.ents[0]
	e.reset(p, 0)
	s.q.push(e)
	p.block(w)
	s.mu.Unlock()
	won := p.sleep(w)
	if won == 0 {
		return true
	}
	s.mu.Lock()
	s.q.remove(e)
	s.mu.Unlock()
	if won == wonDeadlock {
		p.deadlocked()
	}
	return false
}

// release gives a slot back: to the first waiter, or to the pool.
func (s *semaphore) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.q.claim(); e != nil {
		e.p.release()
		return
	}
	s.free++
}
