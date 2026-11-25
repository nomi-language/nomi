package rt

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// The task registry and the deadlock report: package doc, "Deadlock".

// Site is a place in a program where a task waits or calls into Go: its
// innermost Nomi frame, as the machine names it. A zero Site is a wait the
// machine gave no place, which a report leaves unplaced.
type Site struct {
	// Fn is the function, or "" when the machine did not name it.
	Fn     string
	File   string
	Line   int
	Stdlib bool
}

func (s Site) String() string {
	if s.File == "" {
		return ""
	}
	at := fmt.Sprintf("%s:%d", DisplayPath(s.File), s.Line)
	if s.Fn != "" {
		at += " in " + s.Fn
	}
	return at
}

// waitKind is what a blocked task waits for. Every kind is a wait object
// with its own waiter list (wait.go), and every kind's waker marks the
// waiter running before it can block itself.
type waitKind uint8

const (
	waitReceive waitKind = iota + 1
	waitSend
	waitSelect
	// waitTask is `Task.await`, `Task.outcome` and `Task.await_all`.
	waitTask
	// waitBlock is the end of a `concurrent` block, waiting for its tasks.
	waitBlock
	// waitSlot is a task waiting for a `max_running` slot.
	waitSlot
	// waitOnce is a `once` another task is forcing.
	waitOnce
	// waitFlush is `Supervisor.flush`, which always could wake: see
	// couldWake.
	waitFlush
	// waitKinds ends the kinds, for the test that each one reads in a
	// report.
	waitKinds
)

func (k waitKind) text() string {
	switch k {
	case waitReceive:
		return "waits to receive on a channel"
	case waitSend:
		return "waits to send on a channel"
	case waitSelect:
		return "waits in Channel.select"
	case waitTask:
		return "waits for a task to end"
	case waitBlock:
		return "waits for the tasks of its concurrent block to end"
	case waitSlot:
		return "waits for a max_running slot"
	case waitOnce:
		return "waits for a once value another task is computing"
	case waitFlush:
		return "waits for a supervisor's work to finish"
	}
	return ""
}

// wait is what a blocked task waits on, recorded while it waits: what wakes
// it and where it waits.
type wait struct {
	kind waitKind
	site Site
	// ctx ends the wait when it ends, or nil when only the wait object does.
	ctx context.Context
	// timed says a timer of the wait's own ends it after `after`, and
	// afterWon is the case it ends.
	timed    bool
	after    Duration
	afterWon int
	// ch is a send's or a receive's channel, and cases a select's.
	ch    *channel
	cases []SelectCase
}

// couldWake reports whether something other than a task of the program
// could still end w: its timer, its context's end or deadline, or a feeder
// of one of its channels (signals.go, SenderCloseOnDrain). Called with the
// registry's lock held, which guards a channel's feeders.
//
// A flush always could: its supervisor's tasks may wait for a slot, a
// restart's backoff or a timer, which are not records' waits. It is a
// record's wait only so the drain counts the task as parked.
func (w *wait) couldWake() bool {
	if w.timed || w.kind == waitFlush {
		return true
	}
	if w.ctx != nil {
		if w.ctx.Err() != nil {
			return true
		}
		if _, ok := w.ctx.Deadline(); ok {
			return true
		}
	}
	if w.ch != nil && w.ch.fed > 0 {
		return true
	}
	for _, c := range w.cases {
		if c.c != nil && c.c.fed > 0 {
			return true
		}
	}
	return false
}

// taskRec is one task's record in the registry: a main line, a
// `concurrent` task or a supervised task.
type taskRec struct {
	// root is the main line the task descends from, itself for a main line,
	// and nil for a task no main line spawned (newTask).
	root *taskRec
	// name names a main line in a report.
	name string
	// spawned is where a task was spawned.
	spawned Site
	// group is the supervisor a task runs under, directly or in a block one
	// of its tasks opened, or nil.
	group *supervisor
	// inBody says the task has a slot and runs its body: its supervisor's
	// drain counts it (taskRegistry.settled).
	inBody bool
	// blocked says the task waits, on w.
	blocked bool
	w       wait
	// p is the task's parker, reused by each of its waits; busy guards it.
	p    parker
	busy atomic.Bool
	// deadlock is the report a main line answers when the registry finds
	// every task waiting.
	deadlock string
	// inGo and inGoAt are the innermost call into Go the task is in, noted
	// only for a case under a virtual clock (EnterGo), and its order among
	// the notes; inGoAt is 0 when there is none.
	inGo   Site
	inGoAt uint64
	// gone says the record has left the registry; a goroutine still holding
	// it changes no count.
	gone       bool
	prev, next *taskRec
}

// who names the task in a report.
func (t *taskRec) who() string {
	switch {
	case t.root == t:
		return t.name
	case t.group != nil && t.spawned.File != "":
		return "the supervised task spawned at " + t.spawned.String()
	case t.group != nil:
		return "a supervised task"
	case t.spawned.File != "":
		return "the task spawned at " + t.spawned.String()
	}
	return "a task"
}

// taskRegistry is every task record of the process, and the count of those
// running, under one lock. It is touched only when a task starts or ends,
// blocks or is woken, never on a wait that need not block.
type taskRegistry struct {
	mu          sync.Mutex
	first, last *taskRec
	// running counts the records not blocked.
	running int
	// sources counts the wake sources outside the tasks that are armed: a
	// drain in progress (drainSupervisors).
	sources int
	// unsure is set once two goroutines were found waiting on one record:
	// the registry no longer knows which tasks run, and reports nothing.
	unsure bool
	// noted orders EnterGo's notes.
	noted uint64
}

var tasks taskRegistry

// MainLine makes fr, a frame its caller made for a new activation, the main
// line of a run: `nomi run`'s main, an embedder's call, a test case. The
// tasks it spawns are recorded with it, and a deadlock among them is
// reported to it. name names it in that report. end takes it out of the
// registry.
func MainLine(fr *Frame, name string) (end func()) {
	rec := &taskRec{name: name}
	rec.root = rec
	tasks.add(rec)
	fr.task = rec
	return func() { tasks.remove(rec) }
}

// newTask records a task fr spawns at site, under group (nil for a
// `concurrent` task, which inherits its spawner's). A task whose spawner
// runs on no main line (a program kept for a host's calls, a frame a host
// made by hand) is recorded with no root: its supervisor's drain still
// counts it, and while it lives nothing is reported, since the goroutine
// that spawned it is recorded nowhere.
func newTask(fr *Frame, site Site, group *supervisor) *taskRec {
	var root *taskRec
	if fr != nil && fr.task != nil {
		root = fr.task.root
		if group == nil {
			group = fr.task.group
		}
	}
	rec := &taskRec{root: root, spawned: site, group: group}
	tasks.add(rec)
	return rec
}

// add records rec as running. Its spawner runs, so the count does not reach
// zero meanwhile.
func (r *taskRegistry) add(rec *taskRec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec.prev = r.last
	if r.last != nil {
		r.last.next = rec
	} else {
		r.first = rec
	}
	r.last = rec
	r.running++
}

// remove takes rec out when its task has ended, after every wake the task's
// end makes (its awaiters, its block, its supervisor's flush).
func (r *taskRegistry) remove(rec *taskRec) {
	if rec == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec.gone {
		return
	}
	rec.gone = true
	if rec.prev != nil {
		rec.prev.next = rec.next
	} else {
		r.first = rec.next
	}
	if rec.next != nil {
		rec.next.prev = rec.prev
	} else {
		r.last = rec.prev
	}
	rec.prev, rec.next = nil, nil
	if !rec.blocked {
		r.running--
	}
	r.checkLocked()
}

// block records that rec waits on w. The caller holds the lock of every
// wait object rec is on the list of, so no waker can claim rec before the
// registry knows it waits.
func (r *taskRegistry) block(rec *taskRec, w wait) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec.w = w
	if rec.gone || rec.blocked {
		return
	}
	rec.blocked = true
	r.running--
	r.checkLocked()
}

// resume records that rec runs again: called by whoever claimed its parker
// (parker.release), or by rec itself after its context or timer woke it.
func (r *taskRegistry) resume(rec *taskRec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resumeLocked(rec)
}

func (r *taskRegistry) resumeLocked(rec *taskRec) {
	if rec.gone || !rec.blocked {
		return
	}
	rec.blocked = false
	r.running++
}

// arm and disarm count a wake source outside the tasks: while one is armed
// nothing is reported.
func (r *taskRegistry) arm() {
	r.mu.Lock()
	r.sources++
	r.mu.Unlock()
}

func (r *taskRegistry) disarm() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources--
	r.checkLocked()
}

// feed counts n more (or, negative, fewer) feeders of c: something outside
// the tasks that may send on it or close it. A wait on c could wake while it
// has one.
func (r *taskRegistry) feed(c *channel, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c.fed += n
	if n < 0 {
		r.checkLocked()
	}
}

// distrust is called when a record's parker was already in use: two
// goroutines run on one record, so its state says nothing about either.
func (r *taskRegistry) distrust() {
	r.mu.Lock()
	r.unsure = true
	r.mu.Unlock()
}

// checkLocked reports a deadlock when no record runs, no wake source is
// armed and no blocked record's wait could end from outside the tasks:
// every task waits on another task, so none can run again. Each main line
// among them is woken with the report. Called with the lock held whenever
// running may have reached zero or a could-wake may have ended.
//
// Why zero is exact (package doc, "Deadlock"): a record is blocked only by
// its own goroutine, and every claim of a blocked record's parker is either
// made by a running goroutine, which resumes the record before it can
// block, or by something couldWake answers for.
func (r *taskRegistry) checkLocked() {
	if r.running != 0 || r.sources != 0 || r.unsure || r.first == nil {
		return
	}
	for t := r.first; t != nil; t = t.next {
		if !t.blocked || t.root == nil || t.w.couldWake() {
			return
		}
	}
	var mains []*taskRec
	for t := r.first; t != nil; t = t.next {
		if t.root == t {
			mains = append(mains, t)
		}
	}
	for _, m := range mains {
		report := r.reportLocked(m)
		if !m.p.claim(wonDeadlock) {
			continue
		}
		m.deadlock = report
		r.resumeLocked(m)
		m.p.wake <- struct{}{}
	}
}

// DeadlockText is the first line of a deadlock's report.
const DeadlockText = "every task is waiting, so none can run again:"

// reportLocked is the report a deadlocked main line answers: what each of
// its tasks waits on, and where.
func (r *taskRegistry) reportLocked(main *taskRec) string {
	var b strings.Builder
	b.WriteString(DeadlockText)
	for t := r.first; t != nil; t = t.next {
		if t.root != main {
			continue
		}
		b.WriteString("\n  ")
		b.WriteString(t.who())
		b.WriteByte(' ')
		b.WriteString(t.w.kind.text())
		if at := t.w.site.String(); at != "" {
			b.WriteString(" at ")
			b.WriteString(at)
		}
	}
	return b.String()
}

// settled reports that every task of g that runs its body waits: the drain
// may stop waiting for work that cannot arrive (supervisor.go's grace).
// False when none runs its body, the ordinary finished case.
func (r *taskRegistry) settled(g *supervisor) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for t := r.first; t != nil; t = t.next {
		if t.group != g || !t.inBody {
			continue
		}
		if !t.blocked {
			return false
		}
		n++
	}
	return n > 0
}

// inBody records whether rec runs its body, for settled, which asks only
// about a supervisor's tasks.
func (r *taskRegistry) inBody(rec *taskRec, in bool) {
	if rec == nil || rec.group == nil {
		return
	}
	r.mu.Lock()
	rec.inBody = in
	r.mu.Unlock()
}

// GoNote is what EnterGo answers, for LeaveGo to restore.
type GoNote struct {
	site Site
	at   uint64
}

// EnterGo notes that fr's task calls into Go at site, until LeaveGo; a
// machine notes it for a case under a virtual clock, whose bubble a wait
// outside the program can keep from ending (RunBubbled), so the case's
// report names where each of its tasks waits outside the program.
func EnterGo(fr *Frame, site Site) GoNote {
	rec := fr.task
	if rec == nil {
		return GoNote{}
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	prev := GoNote{rec.inGo, rec.inGoAt}
	tasks.noted++
	rec.inGo, rec.inGoAt = site, tasks.noted
	return prev
}

// LeaveGo ends the call EnterGo noted.
func LeaveGo(fr *Frame, prev GoNote) {
	rec := fr.task
	if rec == nil {
		return
	}
	tasks.mu.Lock()
	rec.inGo, rec.inGoAt = prev.site, prev.at
	tasks.mu.Unlock()
}

// inGo is where each task of root's main line is in a call into Go, the
// latest call first.
func (r *taskRegistry) inGo(root *taskRec) []Site {
	r.mu.Lock()
	defer r.mu.Unlock()
	var in []*taskRec
	for t := r.first; t != nil; t = t.next {
		if t.root == root && t.inGoAt != 0 {
			in = append(in, t)
		}
	}
	sort.Slice(in, func(i, j int) bool { return in[i].inGoAt > in[j].inGoAt })
	sites := make([]Site, len(in))
	for i, t := range in {
		sites[i] = t.inGo
	}
	return sites
}
