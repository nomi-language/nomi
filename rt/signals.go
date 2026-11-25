package rt

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
)

// The signals a program receives as values: one registry for the process.
//
// SIGINT and SIGTERM are the runtime's (shutdown.go): they drain the
// supervisors and end the program, and nothing here takes them. The signals
// that ask a running program to do something and keep running (SIGHUP,
// SIGUSR1, SIGUSR2) and the terminal's SIGWINCH reach a program only
// through a feed made here (SubscribeSignals): std::signals' receivers are
// feeds, and any other runtime feature a signal drives subscribes the same
// way, with the value it sends computed at each arrival, so the process has
// one signal.Notify registration per signal number and one place that turns
// an arrival into values on channels.
//
// # Watches and feeds
//
// A watch is one signal number the process is catching: a Go channel
// registered with signal.Notify for that number alone and a goroutine that
// hands each arrival to every feed that wants it. A feed is one channel a
// subscriber reads, with a port per signal it wants. The first feed that
// wants a number starts its watch, and the last one to close stops it with
// signal.Stop, which gives the signal back its default action (SIGHUP,
// SIGUSR1 and SIGUSR2 end the process, and SIGWINCH is ignored) unless
// something else in the process still catches it.
//
// # Coalescing
//
// The system does not queue a signal that arrives while one of its number is
// pending, and a port does not either: while a value for a signal waits on a
// feed's channel, unreceived, further arrivals of that signal are dropped.
// Each port sends on its own goroutine and the channel is unbuffered, so a
// value waits exactly until a receive takes it, and a burst delivers one
// value per signal number to a receiver that is busy, as Go's
// signal.Notify does into a channel of capacity 1.
//
// # Who closes a feed
//
// A feed belongs to the frame that made it: it closes when that frame's
// context ends (the task that subscribed returns or is cancelled, the
// `concurrent` block it was made in ends), and when the program starts
// draining its supervisors (OnDrain), as a listener does. Closing the
// channel ends a receive waiting on it with None. A feed the drain closes
// leaves its watches running with no feed to hand an arrival to, so a
// SIGHUP during shutdown is dropped rather than ending the drain early;
// ResetSupervisors, once the program's cleanup has run, stops those
// watches (ReleaseSignals). A feed made once the drain has begun is closed
// at once and starts no watch, so no watch outlives the drain that
// ReleaseSignals follows: a task a test case left running cannot leave one
// for the next case to stop.

// signalRegistry is the process's watches by signal number.
var signalRegistry = struct {
	mu      sync.Mutex
	watches map[os.Signal]*signalWatch
}{watches: map[os.Signal]*signalWatch{}}

// signalWatch is one signal number the process catches, and the feeds that
// want it, by the port each arrival wakes.
type signalWatch struct {
	ch    chan os.Signal
	ports map[*signalPort]struct{}
}

// signalPort is one feed's interest in one signal: pending is set while a
// value for it waits on the feed's channel, and wake starts its sender.
type signalPort struct {
	pending atomic.Bool
	wake    chan struct{}
}

// deliver wakes p's sender unless a value of p's signal is already waiting.
func (p *signalPort) deliver() {
	if p.pending.CompareAndSwap(false, true) {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

// run hands each arrival to every port that wants it, until stop closes ch.
func (w *signalWatch) run() {
	for range w.ch {
		signalRegistry.mu.Lock()
		ports := make([]*signalPort, 0, len(w.ports))
		for p := range w.ports {
			ports = append(ports, p)
		}
		signalRegistry.mu.Unlock()
		for _, p := range ports {
			p.deliver()
		}
	}
}

// watchLocked is sig's watch, started when nothing caught sig. Called with
// the registry's lock held.
func watchLocked(sig os.Signal) *signalWatch {
	w := signalRegistry.watches[sig]
	if w == nil {
		w = &signalWatch{ch: make(chan os.Signal, 1), ports: map[*signalPort]struct{}{}}
		signal.Notify(w.ch, sig)
		signalRegistry.watches[sig] = w
		go w.run()
	}
	return w
}

// stopLocked stops catching sig when no port wants it. Called with the
// registry's lock held.
func stopLocked(sig os.Signal) {
	w := signalRegistry.watches[sig]
	if w == nil || len(w.ports) != 0 {
		return
	}
	signal.Stop(w.ch)
	close(w.ch)
	delete(signalRegistry.watches, sig)
}

// detachedFrame is the frame a port's sender sends on: in no task and
// never cancelled, since the feed's channel closing is what ends a send.
var detachedFrame = &Frame{ctx: context.Background()}

// SubscribeSignals feeds s from the process's signals: from this call on,
// each arrival of a signal wanted names sends the value its function
// answers then, and the signal no longer has its default action. A value
// waits on s until it is received, and an arrival of a signal whose value
// is still waiting is dropped (the file header, "Coalescing"). The feed
// closes s when fr's context ends or the program starts draining, and the
// signals no other feed wants get their default action back. A feed that
// wants no signal, or is made once the drain has begun, closes s at once
// and catches nothing.
func SubscribeSignals[T any](fr *Frame, s Sender[T], wanted map[os.Signal]func() T) {
	if len(wanted) == 0 {
		SenderClose(s)
		return
	}
	c := s.c
	ports := make(map[os.Signal]*signalPort, len(wanted))
	var once sync.Once
	var stopOwner func() bool
	var unhook func()
	var hooked sync.WaitGroup
	hooked.Add(1)
	// stopped ends the ports' senders once the feed closes.
	stopped := make(chan struct{})
	// The feed may send on s or close it from outside the tasks, so a wait
	// on s is not one the registry may report (package doc, "Deadlock").
	// Counted before anything can close the feed, and let go after its close
	// has woken every waiter.
	tasks.feed(c, 1)
	closeFeed := func(draining bool) {
		once.Do(func() {
			signalRegistry.mu.Lock()
			for sig, p := range ports {
				if w := signalRegistry.watches[sig]; w != nil {
					delete(w.ports, p)
					if !draining {
						stopLocked(sig)
					}
				}
			}
			signalRegistry.mu.Unlock()
			close(stopped)
			SenderClose(s)
			tasks.feed(c, -1)
			go func() {
				// The hooks are set once both are registered; the drain's
				// may close the feed before the owner's is.
				hooked.Wait()
				if stopOwner != nil {
					stopOwner()
				}
				unhook()
			}()
		})
	}
	// The drain hook is registered and the watches started under the
	// registry's lock: a drain that begins meanwhile closes the feed once
	// its ports exist, before ReleaseSignals stops the watches it leaves.
	signalRegistry.mu.Lock()
	var began bool
	unhook, began = onDrain(func() { closeFeed(true) })
	if began {
		signalRegistry.mu.Unlock()
		SenderClose(s)
		tasks.feed(c, -1)
		return
	}
	for sig, value := range wanted {
		p := &signalPort{wake: make(chan struct{}, 1)}
		ports[sig] = p
		watchLocked(sig).ports[p] = struct{}{}
		go func() {
			for {
				select {
				case <-p.wake:
				case <-stopped:
					return
				}
				if SenderSend(detachedFrame, Site{}, s, value()).Tag != TagOk {
					return
				}
				p.pending.Store(false)
			}
		}()
	}
	signalRegistry.mu.Unlock()
	if fr != nil && fr.ctx != nil {
		stopOwner = context.AfterFunc(fr.ctx, func() { closeFeed(false) })
	}
	hooked.Done()
}

// ReleaseSignals stops catching every signal no feed wants: the ones the
// drain left caught so an arrival during shutdown is dropped. A signal a
// feed still wants stays caught.
func ReleaseSignals() {
	signalRegistry.mu.Lock()
	defer signalRegistry.mu.Unlock()
	for sig := range signalRegistry.watches {
		stopLocked(sig)
	}
}

// SignalNamed is the signal a POSIX name (`SIGHUP`) stands for on this
// system, and "" for a reason; or nil and why this system delivers no such
// signal to a program.
func SignalNamed(name string) (os.Signal, string) {
	if signalsUnsupported != "" {
		return nil, signalsUnsupported
	}
	sig, known := systemSignals[name]
	if !known {
		return nil, "no signal is named " + name
	}
	return sig, ""
}

// SignalsUnsupported is why this system delivers no signal to a program, or
// "" on a system that does.
func SignalsUnsupported() string { return signalsUnsupported }
