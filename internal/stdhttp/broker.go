package stdhttp

import (
	"sync"

	"github.com/nomi-language/nomi/rt"
)

// Go calls a program's hooks (a TLS certificate choice or check, a
// server's connection states and error log, a transport's proxy answer) on
// goroutines of its own, where no Nomi code may run. A broker carries each
// call to the Nomi task that is waiting in one of std/http's crossings for
// the work that set the hook off: a server's `next`, a client's `send`, a
// handshake. That task runs it on its own frame, so a hook that traps traps
// the task that was waiting.
type broker struct {
	mu    sync.Mutex
	queue []func(*rt.Frame)
	ready chan struct{}
}

func newBroker() *broker { return &broker{ready: make(chan struct{}, 1)} }

// post queues f to run on the next waiting task, and returns at once.
func (b *broker) post(f func(*rt.Frame)) {
	b.mu.Lock()
	b.queue = append(b.queue, f)
	b.mu.Unlock()
	select {
	case b.ready <- struct{}{}:
	default:
	}
}

// call queues f and waits until it has run, or until stop closes; it
// answers whether f ran.
func (b *broker) call(stop <-chan struct{}, f func(*rt.Frame)) bool {
	done := make(chan struct{})
	b.post(func(fr *rt.Frame) {
		defer close(done)
		f(fr)
	})
	select {
	case <-done:
		return true
	case <-stop:
		return false
	}
}

// run runs everything queued, in order, on fr.
func (b *broker) run(fr *rt.Frame) {
	for {
		b.mu.Lock()
		if len(b.queue) == 0 {
			b.mu.Unlock()
			return
		}
		f := b.queue[0]
		b.queue = b.queue[1:]
		b.mu.Unlock()
		f(fr)
	}
}

// wait runs queued work on fr until done closes, then what is left.
func (b *broker) wait(fr *rt.Frame, done <-chan struct{}) {
	for {
		select {
		case <-done:
			b.run(fr)
			return
		case <-b.ready:
			b.run(fr)
		}
	}
}
